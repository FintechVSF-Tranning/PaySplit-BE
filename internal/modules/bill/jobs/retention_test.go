package jobs_test

import (
	"testing"
	"time"

	"github.com/riverqueue/river"

	"paysplit-backend/internal/modules/bill/jobs"
)

// TestRegisterRetentionJobs kiểm tra tầng nối dây, không phải logic nghiệp vụ. Lỗi mà nó chặn là
// worker được viết đầy đủ nhưng không ai đăng ký và không có job định kỳ nào chạy nó.
func TestRegisterRetentionJobs(t *testing.T) {
	workers := river.NewWorkers()

	periodic := jobs.RegisterRetentionJobs(workers, nil, 30*24*time.Hour)

	if len(periodic) != 2 {
		t.Fatalf("mong đợi 2 job định kỳ, nhận %d", len(periodic))
	}

	// Đăng ký lại cùng worker sẽ panic, đó là bằng chứng lần đầu đã đăng ký thành công.
	assertPanics(t, "OCRRetentionWorker chưa được đăng ký", func() {
		river.AddWorker(workers, jobs.NewOCRRetentionWorker(nil))
	})
	assertPanics(t, "IdempotencyRetentionWorker chưa được đăng ký", func() {
		river.AddWorker(workers, jobs.NewIdempotencyRetentionWorker(nil))
	})
}

func TestRegisterRetentionJobs_DefaultsWhenRetentionUnset(t *testing.T) {
	workers := river.NewWorkers()

	if periodic := jobs.RegisterRetentionJobs(workers, nil, 0); len(periodic) != 2 {
		t.Fatalf("mong đợi 2 job định kỳ kể cả khi retention chưa đặt, nhận %d", len(periodic))
	}
}

func TestRetentionWorkers_NilRepo_NoPanic(t *testing.T) {
	ocr := jobs.NewOCRRetentionWorker(nil)
	if err := ocr.Work(t.Context(), &river.Job[jobs.OCRRetentionJobArgs]{}); err != nil {
		t.Errorf("OCRRetentionWorker với repo nil phải bỏ qua, nhận %v", err)
	}

	idem := jobs.NewIdempotencyRetentionWorker(nil)
	if err := idem.Work(t.Context(), &river.Job[jobs.IdempotencyRetentionJobArgs]{}); err != nil {
		t.Errorf("IdempotencyRetentionWorker với repo nil phải bỏ qua, nhận %v", err)
	}
}

func assertPanics(t *testing.T, msg string, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Error(msg)
		}
	}()
	fn()
}
