package jobs

import (
	"time"

	"github.com/riverqueue/river"

	"paysplit-backend/internal/modules/bill/repository"
)

// RetentionCleanupInterval là nhịp chạy của hai job dọn dẹp định kỳ của module bill.
const RetentionCleanupInterval = 24 * time.Hour

// StaleOCRReapInterval là nhịp quét job OCR mắc kẹt. Ngắn hơn hẳn nhịp retention vì
// mỗi job kẹt là một spinner "đang quét" đứng yên trên máy người dùng, chứ không
// phải một hàng dữ liệu thừa nằm im.
const StaleOCRReapInterval = 2 * time.Minute

// RegisterRetentionJobs đăng ký hai worker dọn dẹp của module bill và trả về danh sách job định kỳ
// tương ứng để truyền vào River client.
//
// Hàm này tồn tại tách khỏi bootstrap để có thể kiểm thử được. Lỗi trước đây là OCRRetentionWorker
// được viết đầy đủ nhưng không ai đăng ký, và không có test nào ở tầng nối dây bắt được điều đó
// (Spec 3 AC-11, AC-13).
func RegisterRetentionJobs(workers *river.Workers, repo repository.Repository, ocrRetention, ocrStaleJobAge time.Duration) []*river.PeriodicJob {
	ocrRetentionHours := int(ocrRetention.Hours())
	if ocrRetentionHours <= 0 {
		ocrRetentionHours = 24 * 30
	}
	staleSeconds := int(ocrStaleJobAge.Seconds())
	if staleSeconds <= 0 {
		staleSeconds = int((15 * time.Minute).Seconds())
	}

	river.AddWorker(workers, NewOCRRetentionWorker(repo))
	river.AddWorker(workers, NewIdempotencyRetentionWorker(repo))
	river.AddWorker(workers, NewStaleOCRReaperWorker(repo))

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
		// RunOnStart có chủ đích: tiến trình vừa chết giữa lúc đang chạy OCR chính là
		// cách phổ biến nhất để bỏ lại một job kẹt, nên lượt quét đáng giá nhất là
		// lượt ngay sau khi khởi động lại.
		river.NewPeriodicJob(
			river.PeriodicInterval(StaleOCRReapInterval),
			func() (river.JobArgs, *river.InsertOpts) {
				return StaleOCRJobArgs{OlderThanSeconds: staleSeconds}, nil
			},
			&river.PeriodicJobOpts{RunOnStart: true},
		),
	}
}
