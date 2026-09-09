package jobs

import (
	"context"
	"fmt"
	"log"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
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
	if job.Args.UserID == "" || len(job.Args.SIDs) == 0 {
		return nil
	}
	revoked, err := w.sessions.RevokeUserSIDs(ctx, job.Args.UserID, job.Args.SIDs)
	if err != nil {
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
	if e == nil || e.client == nil || userID == "" || len(sids) == 0 {
		return nil
	}
	_, err := e.client.InsertTx(ctx, tx, SessionPurgeArgs{UserID: userID, SIDs: sids}, nil)
	if err != nil {
		return fmt.Errorf("enqueue session purge: %w", err)
	}
	return nil
}
