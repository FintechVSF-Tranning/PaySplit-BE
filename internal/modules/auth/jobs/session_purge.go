package jobs

import (
	"context"
	"fmt"
	"log"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	platformmetrics "paysplit-backend/internal/platform/metrics"
)

// SessionPurgeArgs là backstop bền vững cho việc thu hồi phiên trên Redis.
//
// Vì sao cần: trước đây ValidateSession JOIN `users` với `status='active'` trên
// mọi request, nên admin khoá tài khoản là request kế tiếp chết ngay — kể cả khi
// lệnh UPDATE sessions không khớp hàng nào. Sau khi chuyển sang credential đục,
// middleware chỉ đọc Redis và KHÔNG BAO GIỜ chạm users.status nữa, nên lệnh DEL
// trên Redis trở thành cơ chế cưỡng chế duy nhất. Một lần ghi Redis lỡ đồng nghĩa
// người bị khoá vẫn dùng app bình thường tới hết TTL.
//
// Job được enqueue bằng InsertTx ngay trong transaction thu hồi, nên nó chỉ tồn
// tại khi transaction commit — cùng ngữ nghĩa mà pg_notify đang dựa vào. Không
// cần bảng outbox riêng.
type SessionPurgeArgs struct {
	UserID string   `json:"user_id"`
	SIDs   []string `json:"sids"`
	// Unconditional cho phép job thu hồi theo user mà không khớp SID.
	//
	// Mặc định là false, và mặc định đó mới là luật chung: khớp SID là thứ chặn
	// một lần chạy trễ giết nhầm phiên mà người dùng vừa tạo lại (spec 0011 AC-12).
	//
	// Chỉ đường khóa và đình chỉ tài khoản bật cờ này, vì ở đó lệnh
	// `UPDATE sessions ... WHERE revoked_at IS NULL RETURNING id` khớp không hàng
	// nào khi tài khoản đã revoked từ trước, nên không có SID nào để mang theo và
	// backstop sẽ không tồn tại. Bật được là vì `CreateSession` từ chối mọi user
	// có status khác `active`, nên ở đường này không có phiên mới nào để giết nhầm.
	// Xem spec 0012.
	Unconditional bool `json:"unconditional,omitempty"`
}

func (SessionPurgeArgs) Kind() string { return "session_redis_purge" }

// InsertOpts đặt lịch retry theo phút chứ không theo nhịp dọn dẹp 24 giờ của
// AUTH_CLEANUP_INTERVAL_HOURS: mỗi phút chậm trễ là một phút tài khoản bị khoá
// vẫn gọi được API.
func (SessionPurgeArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 10}
}

// SessionRevoker là cổng tới kho phiên Redis.
type SessionRevoker interface {
	RevokeUserSIDs(ctx context.Context, userID string, sids []string) (bool, error)
	// RevokeUser thu hồi phiên đang sống của user mà không khớp SID. Chỉ dùng cho
	// job mang Unconditional.
	RevokeUser(ctx context.Context, userID string) (bool, error)
}

type SessionPurgeWorker struct {
	river.WorkerDefaults[SessionPurgeArgs]
	sessions SessionRevoker
}

func NewSessionPurgeWorker(sessions SessionRevoker) *SessionPurgeWorker {
	if sessions == nil {
		panic("session purge worker requires a session store")
	}
	return &SessionPurgeWorker{sessions: sessions}
}

func (w *SessionPurgeWorker) Work(ctx context.Context, job *river.Job[SessionPurgeArgs]) error {
	if job.Args.UserID == "" {
		return nil
	}
	// Job khớp SID mà không có SID nào thì không có việc gì để làm; gọi xuống kho
	// phiên với danh sách rỗng có nguy cơ bị hiểu thành "xoá tất cả".
	if !job.Args.Unconditional && len(job.Args.SIDs) == 0 {
		return nil
	}
	var (
		revoked bool
		err     error
	)
	if job.Args.Unconditional {
		revoked, err = w.sessions.RevokeUser(ctx, job.Args.UserID)
	} else {
		revoked, err = w.sessions.RevokeUserSIDs(ctx, job.Args.UserID, job.Args.SIDs)
	}
	if err != nil {
		// Lượt thử cuối: sau khi trả lỗi lần này River loại bỏ job và im lặng.
		// Backstop hỏng ở đây nghĩa là một tài khoản đã bị khoá trong Postgres
		// vẫn còn phiên sống trên Redis và không còn cơ chế nào dọn nó — phải
		// phát tín hiệu cho người vận hành thay vì để mất trong im lặng.
		if job.Attempt >= job.MaxAttempts {
			platformmetrics.SessionPurgeExhaustedTotal.Inc()
			log.Printf("event=session_purge_exhausted user_id=%s sids=%d attempt=%d err=%v", job.Args.UserID, len(job.Args.SIDs), job.Attempt, err)
		}
		return fmt.Errorf("purge redis sessions: %w", err)
	}
	if revoked {
		// Xoá được ở đây nghĩa là lần gọi trực tiếp lúc thu hồi đã lỡ — đáng ghi
		// log vì nó báo hiệu Redis đang chập chờn.
		log.Printf("event=session_purge_recovered user_id=%s sids=%d", job.Args.UserID, len(job.Args.SIDs))
	}
	return nil
}

// SessionPurgeEnqueuer đặt job vào hàng đợi bên trong transaction thu hồi.
type SessionPurgeEnqueuer struct {
	client *river.Client[pgx.Tx]
}

func NewSessionPurgeEnqueuer(client *river.Client[pgx.Tx]) *SessionPurgeEnqueuer {
	return &SessionPurgeEnqueuer{client: client}
}

func (e *SessionPurgeEnqueuer) EnqueueTx(ctx context.Context, tx pgx.Tx, userID string, sids []string) error {
	return e.enqueue(ctx, tx, SessionPurgeArgs{UserID: userID, SIDs: sids})
}

// EnqueueUnconditionalTx đặt job thu hồi theo user, không khớp SID.
//
// Tồn tại riêng vì EnqueueTx cố ý thoát sớm khi danh sách SID rỗng, và chính chỗ
// thoát sớm đó làm lần khóa tài khoản thứ hai không để lại backstop nào: lệnh
// UPDATE khớp không hàng nào nên không có SID để mang theo. Chỉ đường khóa và
// đình chỉ được gọi hàm này. Xem spec 0012.
func (e *SessionPurgeEnqueuer) EnqueueUnconditionalTx(ctx context.Context, tx pgx.Tx, userID string) error {
	return e.enqueue(ctx, tx, SessionPurgeArgs{UserID: userID, Unconditional: true})
}

func (e *SessionPurgeEnqueuer) enqueue(ctx context.Context, tx pgx.Tx, args SessionPurgeArgs) error {
	if e == nil || e.client == nil || args.UserID == "" {
		return nil
	}
	if !args.Unconditional && len(args.SIDs) == 0 {
		return nil
	}
	if _, err := e.client.InsertTx(ctx, tx, args, nil); err != nil {
		return fmt.Errorf("enqueue session purge: %w", err)
	}
	return nil
}
