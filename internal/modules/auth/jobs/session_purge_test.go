package jobs

// White box test (package jobs, khớp workers_internal_test.go): worker và enqueuer
// nằm cùng gói, và điều đáng kiểm nhất là hành vi của Work chứ không phải River.
//
// Job này là tuyến phòng thủ duy nhất còn lại cho việc khoá tài khoản. Sau spec
// 0011 middleware không đọc `users.status` nữa, nên một lệnh DEL lỡ nghĩa là
// người bị khoá vẫn dùng app tới hết TTL, trừ khi job này dọn được. Trước file
// này nó không có một test nào.
//
// covers: AC-11 (backstop cho việc khoá tài khoản), AC-12 (khớp sid trước khi
// xoá nên một lần chạy trễ không giết nhầm phiên mới).

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/riverqueue/river"
)

// fakeRevoker thay cho kho phiên Redis, ghi lại đúng những gì worker yêu cầu xoá.
type fakeRevoker struct {
	mu      sync.Mutex
	calls   int
	gotUser string
	gotSIDs []string

	revoked bool
	err     error
}

func (f *fakeRevoker) RevokeUserSIDs(ctx context.Context, userID string, sids []string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.gotUser = userID
	f.gotSIDs = append([]string(nil), sids...)
	return f.revoked, f.err
}

func job(args SessionPurgeArgs) *river.Job[SessionPurgeArgs] {
	return &river.Job[SessionPurgeArgs]{Args: args}
}

func TestSessionPurgeArgs_Kind(t *testing.T) {
	t.Parallel()

	// Kind là hợp đồng với hàng đợi: đổi chuỗi này làm mọi job đang nằm trong
	// bảng river_job trở thành mồ côi, không worker nào nhận.
	got := (SessionPurgeArgs{}).Kind()
	if got != "session_redis_purge" {
		t.Fatalf("Kind() = %q, want session_redis_purge", got)
	}
}

func TestSessionPurgeArgs_RetriesFastEnoughToMatter(t *testing.T) {
	t.Parallel()

	opts := (SessionPurgeArgs{}).InsertOpts()
	if opts.MaxAttempts < 2 {
		t.Fatalf("MaxAttempts = %d; một lần thử duy nhất thì job không còn là backstop", opts.MaxAttempts)
	}
	if opts.MaxAttempts != 10 {
		t.Fatalf("MaxAttempts = %d, want 10", opts.MaxAttempts)
	}
}

func TestNewSessionPurgeWorker_PanicsWithoutStore(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Fatal("NewSessionPurgeWorker(nil) không panic: worker không có kho phiên sẽ báo thành công mà không xoá gì")
		}
	}()
	_ = NewSessionPurgeWorker(nil)
}

func TestSessionPurgeWorker_RevokesTheRequestedSIDs(t *testing.T) {
	t.Parallel()

	revoker := &fakeRevoker{revoked: true}
	worker := NewSessionPurgeWorker(revoker)

	err := worker.Work(context.Background(), job(SessionPurgeArgs{
		UserID: "user-1",
		SIDs:   []string{"sid-a", "sid-b"},
	}))
	if err != nil {
		t.Fatalf("Work lỗi bất ngờ: %v", err)
	}
	if revoker.calls != 1 {
		t.Fatalf("kho phiên được gọi %d lần, want 1", revoker.calls)
	}
	if revoker.gotUser != "user-1" {
		t.Fatalf("user_id = %q, want user-1", revoker.gotUser)
	}
	if len(revoker.gotSIDs) != 2 || revoker.gotSIDs[0] != "sid-a" || revoker.gotSIDs[1] != "sid-b" {
		t.Fatalf("sids = %v, want [sid-a sid-b]", revoker.gotSIDs)
	}
}

// AC-12: kho phiên khớp sid trước khi xoá và trả false khi không khớp. Worker
// phải coi đó là thành công, không phải lỗi — nếu nó trả lỗi, River sẽ thử lại
// mười lần cho một job vốn dĩ không còn việc gì để làm.
func TestSessionPurgeWorker_StaleJobThatMatchesNothingSucceeds(t *testing.T) {
	t.Parallel()

	revoker := &fakeRevoker{revoked: false}
	worker := NewSessionPurgeWorker(revoker)

	err := worker.Work(context.Background(), job(SessionPurgeArgs{
		UserID: "user-1",
		SIDs:   []string{"sid-cu-da-bi-thay-the"},
	}))
	if err != nil {
		t.Fatalf("job trễ không khớp sid nào phải thành công, nhận lỗi: %v", err)
	}
	if revoker.calls != 1 {
		t.Fatalf("kho phiên được gọi %d lần, want 1", revoker.calls)
	}
}

func TestSessionPurgeWorker_SkipsEmptyArgsWithoutTouchingTheStore(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args SessionPurgeArgs
	}{
		{name: "không có user_id", args: SessionPurgeArgs{SIDs: []string{"sid-a"}}},
		{name: "danh sách sid rỗng", args: SessionPurgeArgs{UserID: "user-1", SIDs: []string{}}},
		{name: "danh sách sid nil", args: SessionPurgeArgs{UserID: "user-1"}},
		{name: "rỗng hoàn toàn", args: SessionPurgeArgs{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			revoker := &fakeRevoker{}
			worker := NewSessionPurgeWorker(revoker)

			if err := worker.Work(context.Background(), job(tt.args)); err != nil {
				t.Fatalf("Work lỗi bất ngờ: %v", err)
			}
			if revoker.calls != 0 {
				// Một job rỗng mà vẫn gọi RevokeUserSIDs với danh sách rỗng có
				// nguy cơ bị kho phiên hiểu là "xoá tất cả".
				t.Fatalf("kho phiên được gọi %d lần với args rỗng, want 0", revoker.calls)
			}
		})
	}
}

// Redis hỏng phải trả lỗi để River giữ job lại và thử lại. Nuốt lỗi ở đây làm
// backstop im lặng biến mất, đúng kịch bản mà job này sinh ra để chặn.
func TestSessionPurgeWorker_ReturnsErrorSoRiverRetries(t *testing.T) {
	t.Parallel()

	revoker := &fakeRevoker{err: errors.New("dial tcp: connection refused")}
	worker := NewSessionPurgeWorker(revoker)

	err := worker.Work(context.Background(), job(SessionPurgeArgs{
		UserID: "user-1",
		SIDs:   []string{"sid-a"},
	}))
	if err == nil {
		t.Fatal("Redis hỏng nhưng Work trả nil: River sẽ đánh dấu hoàn tất và không bao giờ thử lại")
	}
	if !strings.Contains(err.Error(), "purge redis sessions") {
		t.Fatalf("lỗi = %q, muốn có ngữ cảnh \"purge redis sessions\"", err)
	}
	if !errors.Is(err, revoker.err) {
		t.Fatalf("lỗi gốc bị mất khi bọc: %v", err)
	}
}

func TestSessionPurgeWorker_PropagatesContext(t *testing.T) {
	t.Parallel()

	type ctxKey string
	revoker := &ctxCapturingRevoker{}
	worker := NewSessionPurgeWorker(revoker)

	ctx := context.WithValue(context.Background(), ctxKey("trace"), "abc")
	if err := worker.Work(ctx, job(SessionPurgeArgs{UserID: "user-1", SIDs: []string{"sid-a"}})); err != nil {
		t.Fatalf("Work lỗi bất ngờ: %v", err)
	}
	if revoker.gotCtx == nil {
		t.Fatal("worker không chuyển context xuống kho phiên")
	}
	if v := revoker.gotCtx.Value(ctxKey("trace")); v != "abc" {
		t.Fatalf("context bị thay thế, trace = %v; job bị huỷ sẽ không dừng được lệnh Redis", v)
	}
}

type ctxCapturingRevoker struct {
	gotCtx context.Context
}

func (c *ctxCapturingRevoker) RevokeUserSIDs(ctx context.Context, userID string, sids []string) (bool, error) {
	c.gotCtx = ctx
	return true, nil
}

func TestSessionPurgeEnqueuer_NoOpsWhenThereIsNothingToDo(t *testing.T) {
	t.Parallel()

	// Enqueuer với client nil là trạng thái hợp lệ lúc bootstrap chưa dựng xong
	// hàng đợi. Nó phải im lặng bỏ qua chứ không panic, nếu không một nhánh khởi
	// tạo chậm sẽ làm sập cả đường thu hồi.
	tests := []struct {
		name     string
		enqueuer *SessionPurgeEnqueuer
		userID   string
		sids     []string
	}{
		{name: "enqueuer nil", enqueuer: nil, userID: "user-1", sids: []string{"sid-a"}},
		{name: "client nil", enqueuer: NewSessionPurgeEnqueuer(nil), userID: "user-1", sids: []string{"sid-a"}},
		{name: "không có user_id", enqueuer: NewSessionPurgeEnqueuer(nil), sids: []string{"sid-a"}},
		{name: "không có sid", enqueuer: NewSessionPurgeEnqueuer(nil), userID: "user-1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if err := tt.enqueuer.EnqueueTx(context.Background(), nil, tt.userID, tt.sids); err != nil {
				t.Fatalf("EnqueueTx = %v, want nil", err)
			}
		})
	}
}
