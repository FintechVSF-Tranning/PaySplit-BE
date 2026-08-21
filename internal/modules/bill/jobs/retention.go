package jobs

import (
	"time"

	"github.com/riverqueue/river"

	"paysplit-backend/internal/modules/bill/repository"
)

// RetentionCleanupInterval là nhịp chạy của hai job dọn dẹp định kỳ của module bill.
const RetentionCleanupInterval = 24 * time.Hour

// RegisterRetentionJobs đăng ký hai worker dọn dẹp của module bill và trả về danh sách job định kỳ
// tương ứng để truyền vào River client.
//
// Hàm này tồn tại tách khỏi bootstrap để có thể kiểm thử được. Lỗi trước đây là OCRRetentionWorker
// được viết đầy đủ nhưng không ai đăng ký, và không có test nào ở tầng nối dây bắt được điều đó
// (Spec 3 AC-11, AC-13).
func RegisterRetentionJobs(workers *river.Workers, repo repository.Repository, ocrRetention time.Duration) []*river.PeriodicJob {
	ocrRetentionHours := int(ocrRetention.Hours())
	if ocrRetentionHours <= 0 {
		ocrRetentionHours = 24 * 30
	}

	river.AddWorker(workers, NewOCRRetentionWorker(repo))
	river.AddWorker(workers, NewIdempotencyRetentionWorker(repo))

	return []*river.PeriodicJob{
		river.NewPeriodicJob(
			river.PeriodicInterval(RetentionCleanupInterval),
			func() (river.JobArgs, *river.InsertOpts) {
				return OCRRetentionJobArgs{OlderThanHours: ocrRetentionHours}, nil
			},
			&river.PeriodicJobOpts{RunOnStart: true},
		),
		river.NewPeriodicJob(
			river.PeriodicInterval(RetentionCleanupInterval),
			func() (river.JobArgs, *river.InsertOpts) {
				return IdempotencyRetentionJobArgs{}, nil
			},
			&river.PeriodicJobOpts{RunOnStart: true},
		),
	}
}
