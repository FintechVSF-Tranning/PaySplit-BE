package jobs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"paysplit-backend/internal/modules/bill/domain"
	"paysplit-backend/internal/modules/bill/repository"
	"paysplit-backend/internal/modules/bill/usecase"
)

// OCRJobArgs định nghĩa payload công việc bóc tách hóa đơn trong River Queue (Spec 3 AC-2).
type OCRJobArgs struct {
	BillID  string `json:"bill_id"`
	JobID   string `json:"job_id"`
	GroupID string `json:"group_id"`
}

// Kind định danh loại job trong River Queue.
func (OCRJobArgs) Kind() string { return "bill_ocr" }

// EventBroadcaster định nghĩa interface phát sự kiện realtime từ background worker (Spec 3 AC-2).
type EventBroadcaster interface {
	Broadcast(billID uuid.UUID, eventType string, data any)
}

// OCRWorker là background worker xử lý bóc tách hóa đơn bằng AI qua River Queue.
type OCRWorker struct {
	river.WorkerDefaults[OCRJobArgs]
	repo        repository.Repository
	storage     usecase.BillStorage
	ocrProvider usecase.OCRProvider
	broadcaster EventBroadcaster
	timeout     time.Duration
}

// NewOCRWorker khởi tạo OCRWorker với các dependencies cần thiết.
func NewOCRWorker(
	repo repository.Repository,
	storage usecase.BillStorage,
	ocrProvider usecase.OCRProvider,
	broadcaster EventBroadcaster,
	timeout time.Duration,
) *OCRWorker {
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	return &OCRWorker{
		repo:        repo,
		storage:     storage,
		ocrProvider: ocrProvider,
		broadcaster: broadcaster,
		timeout:     timeout,
	}
}

// Work được River Queue tự động kích hoạt khi có job OCR trong hàng đợi.
func (w *OCRWorker) Work(ctx context.Context, job *river.Job[OCRJobArgs]) error {
	if w.repo == nil || w.storage == nil || w.ocrProvider == nil {
		return nil
	}

	billID, err := uuid.Parse(job.Args.BillID)
	if err != nil {
		return nil
	}
	jobID, err := uuid.Parse(job.Args.JobID)
	if err != nil {
		return nil
	}
	groupID, err := uuid.Parse(job.Args.GroupID)
	if err != nil {
		return nil
	}

	// 1. Lấy thông tin OCR Job từ DB
	ocrJob, err := w.repo.GetOCRJobByID(ctx, jobID)
	if err != nil {
		if errors.Is(err, domain.ErrOcrJobNotFound) {
			return nil
		}
		return fmt.Errorf("get ocr job by id: %w", err)
	}

	// Nếu job đã hoàn tất trước đó thì bỏ qua (idempotency)
	if ocrJob.Status == domain.OCRJobStatusSucceeded || ocrJob.Status == domain.OCRJobStatusFailed {
		return nil
	}

	// 2. Chuyển trạng thái sang processing
	currentVersion := ocrJob.Version
	if err := w.repo.UpdateOCRJobProcessing(ctx, jobID, currentVersion); err != nil {
		if errors.Is(err, domain.ErrOcrJobConflict) {
			// Job đã được worker khác nhận xử lý
			return nil
		}
		return fmt.Errorf("update ocr job to processing: %w", err)
	}
	currentVersion++

	if w.broadcaster != nil {
		w.broadcaster.Broadcast(billID, "ocr.updated", map[string]any{
			"job_id":  jobID,
			"bill_id": billID,
			"status":  "processing",
		})
	}

	// 3. Lấy thông tin hóa đơn và danh sách ảnh từ DB
	bill, err := w.repo.GetBillByID(ctx, billID, groupID)
	if err != nil {
		if errors.Is(err, domain.ErrBillNotFound) {
			_ = w.failJob(ctx, jobID, billID, currentVersion, "bill not found")
			return nil
		}
		return fmt.Errorf("get bill by id: %w", err)
	}

	if len(bill.Images) == 0 {
		_ = w.failJob(ctx, jobID, billID, currentVersion, "no images found for receipt")
		return nil
	}

	// 4. Tải byte ảnh riêng tư từ Cloudinary (lấy ảnh vị trí 0 làm ảnh chính để OCR)
	primaryImageKey := bill.Images[0].ImageKey
	imageBytes, err := w.storage.Download(ctx, primaryImageKey)
	if err != nil {
		// Lỗi mạng khi tải Cloudinary -> trả về error để River tự động retry
		if job.Attempt >= job.MaxAttempts {
			_ = w.failJob(ctx, jobID, billID, currentVersion, fmt.Sprintf("download receipt failed after max attempts: %v", err))
			return nil
		}
		return fmt.Errorf("download receipt from storage: %w", err)
	}

	// 5. Gửi byte ảnh sang LlamaExtract Adapter để bóc tách AI
	extractCtx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()

	candidate, rawJSON, err := w.ocrProvider.ExtractReceipt(extractCtx, imageBytes, "image/jpeg")
	if err != nil {
		// Nếu lỗi do schema AI không đọc được hoặc cấu trúc sai -> đánh dấu failed, không retry
		if errors.Is(err, domain.ErrOcrSchemaInvalid) {
			_ = w.failJob(ctx, jobID, billID, currentVersion, "ocr schema invalid or unparseable receipt")
			return nil
		}

		// Nếu hết số lần retry tối đa của River Queue
		if job.Attempt >= job.MaxAttempts {
			_ = w.failJob(ctx, jobID, billID, currentVersion, fmt.Sprintf("ocr provider failed after %d attempts: %v", job.Attempt, err))
			return nil
		}

		// Trả về error để River Queue tự động retry với exponential backoff
		return fmt.Errorf("extract receipt: %w", err)
	}

	// 6. Ghi nhận kết quả bóc tách thành công vào database và phát SSE event
	if err := w.repo.UpdateOCRJobSuccess(ctx, jobID, currentVersion, candidate, rawJSON); err != nil {
		if errors.Is(err, domain.ErrOcrJobConflict) {
			return nil
		}
		return fmt.Errorf("update ocr job success: %w", err)
	}

	if w.broadcaster != nil {
		w.broadcaster.Broadcast(billID, "ocr.updated", map[string]any{
			"job_id":    jobID,
			"bill_id":   billID,
			"status":    "succeeded",
			"candidate": candidate,
		})
	}

	return nil
}

func (w *OCRWorker) failJob(ctx context.Context, jobID, billID uuid.UUID, version int32, reason string) error {
	err := w.repo.UpdateOCRJobFailed(ctx, jobID, version, reason)
	if w.broadcaster != nil {
		w.broadcaster.Broadcast(billID, "ocr.updated", map[string]any{
			"job_id":  jobID,
			"bill_id": billID,
			"status":  "failed",
			"error":   reason,
		})
	}
	return err
}

// Enqueuer hỗ trợ đẩy công việc OCR vào River Queue.
type Enqueuer struct {
	client *river.Client[pgx.Tx]
}

// NewEnqueuer khởi tạo Enqueuer cho module Bill & OCR.
func NewEnqueuer(client *river.Client[pgx.Tx]) *Enqueuer {
	return &Enqueuer{client: client}
}

// EnqueueOCRJobTx đẩy job OCR vào River Queue trong cùng database transaction tx.
func (e *Enqueuer) EnqueueOCRJobTx(ctx context.Context, tx pgx.Tx, billID, jobID, groupID uuid.UUID) error {
	if e == nil || e.client == nil {
		return nil
	}

	_, err := e.client.InsertTx(ctx, tx, OCRJobArgs{
		BillID:  billID.String(),
		JobID:   jobID.String(),
		GroupID: groupID.String(),
	}, nil)
	return err
}

// EnqueueOCRJob đẩy job OCR vào River Queue không dùng transaction ngoài.
func (e *Enqueuer) EnqueueOCRJob(ctx context.Context, billID, jobID, groupID uuid.UUID) error {
	if e == nil || e.client == nil {
		return nil
	}

	_, err := e.client.Insert(ctx, OCRJobArgs{
		BillID:  billID.String(),
		JobID:   jobID.String(),
		GroupID: groupID.String(),
	}, nil)
	return err
}
