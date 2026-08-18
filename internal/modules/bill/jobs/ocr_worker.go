package jobs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png"
	"time"

	"github.com/disintegration/imaging"
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
	if err := w.repo.UpdateOCRJobProcessing(ctx, jobID); err != nil {
		if errors.Is(err, domain.ErrOcrJobConflict) {
			// Job đã được worker khác nhận xử lý
			return nil
		}
		return fmt.Errorf("update ocr job to processing: %w", err)
	}

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
			_ = w.failJob(ctx, jobID, billID, "bill not found")
			return nil
		}
		return fmt.Errorf("get bill by id: %w", err)
	}

	if len(bill.Images) == 0 {
		_ = w.failJob(ctx, jobID, billID, "no images found for receipt")
		return nil
	}

	// 4. Tải byte ảnh riêng tư từ Cloudinary (hỗ trợ ghép 1-5 ảnh cho hóa đơn nhiều trang, Spec 3 AC-1)
	var allBytes [][]byte
	for _, img := range bill.Images {
		b, err := w.storage.Download(ctx, img.ImageKey)
		if err != nil {
			if job.Attempt >= job.MaxAttempts {
				_ = w.failJob(ctx, jobID, billID, fmt.Sprintf("download receipt failed after max attempts: %v", err))
				return nil
			}
			return fmt.Errorf("download receipt from storage: %w", err)
		}
		allBytes = append(allBytes, b)
	}

	imageBytes := allBytes[0]
	if len(allBytes) > 1 {
		stitched, err := stitchReceiptImages(allBytes)
		if err == nil && len(stitched) > 0 {
			imageBytes = stitched
		}
	}

	// 5. Gửi byte ảnh sang LlamaExtract Adapter để bóc tách AI
	extractCtx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()

	candidate, rawJSON, err := w.ocrProvider.ExtractReceipt(extractCtx, imageBytes, "image/jpeg")
	imageBytes = nil
	if err != nil {
		// Nếu lỗi do schema AI không đọc được hoặc cấu trúc sai -> đánh dấu failed, không retry
		if errors.Is(err, domain.ErrOcrSchemaInvalid) {
			_ = w.failJob(ctx, jobID, billID, "ocr schema invalid or unparseable receipt")
			return nil
		}

		// Nếu hết số lần retry tối đa của River Queue
		if job.Attempt >= job.MaxAttempts {
			_ = w.failJob(ctx, jobID, billID, fmt.Sprintf("ocr provider failed after %d attempts: %v", job.Attempt, err))
			return nil
		}

		// Trả về error để River Queue tự động retry với exponential backoff
		return fmt.Errorf("extract receipt: %w", err)
	}

	// 6. Ghi nhận kết quả bóc tách thành công vào database và phát SSE event
	if err := w.repo.UpdateOCRJobSuccess(ctx, jobID, candidate, rawJSON); err != nil {
		if errors.Is(err, domain.ErrOcrJobConflict) {
			return nil
		}
		return fmt.Errorf("update ocr job success: %w", err)
	}

	if w.broadcaster != nil {
		w.broadcaster.Broadcast(billID, "ocr.updated", map[string]any{
			"job_id":   jobID,
			"bill_id":  billID,
			"status":   "succeeded",
			"warnings": candidate.Warnings,
		})
	}

	return nil
}

func (w *OCRWorker) failJob(ctx context.Context, jobID, billID uuid.UUID, reason string) error {
	err := w.repo.UpdateOCRJobFailed(ctx, jobID, reason)
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

// NotificationJobArgs định nghĩa payload công việc gửi thông báo trong River Queue.
type NotificationJobArgs struct {
	NotificationID string `json:"notification_id"`
}

// Kind định danh loại job trong River Queue.
func (NotificationJobArgs) Kind() string { return "send_notification" }

// EnqueueNotificationTx đẩy job gửi thông báo vào River Queue trong cùng database transaction tx.
func (e *Enqueuer) EnqueueNotificationTx(ctx context.Context, tx pgx.Tx, notificationID string) error {
	if e == nil || e.client == nil {
		return nil
	}

	_, err := e.client.InsertTx(ctx, tx, NotificationJobArgs{
		NotificationID: notificationID,
	}, nil)
	return err
}

func stitchReceiptImages(images [][]byte) ([]byte, error) {
	if len(images) == 0 {
		return nil, errors.New("empty images")
	}
	if len(images) == 1 {
		return images[0], nil
	}

	decodedImages := make([]image.Image, 0, len(images))
	maxWidth := 0
	totalHeight := 0

	for i, b := range images {
		img, _, err := image.Decode(bytes.NewReader(b))
		if err != nil {
			return nil, fmt.Errorf("decode receipt image %d: %w", i, err)
		}
		// Thu nhỏ ảnh nếu chiều ngang quá lớn (> 1200px) để tiết kiệm bộ nhớ khi ghép
		if img.Bounds().Dx() > 1200 {
			img = imaging.Resize(img, 1200, 0, imaging.Lanczos)
		}
		decodedImages = append(decodedImages, img)
		bnds := img.Bounds()
		if bnds.Dx() > maxWidth {
			maxWidth = bnds.Dx()
		}
		totalHeight += bnds.Dy()
	}

	if len(decodedImages) == 0 || maxWidth == 0 || totalHeight == 0 {
		return images[0], nil
	}

	dst := imaging.New(maxWidth, totalHeight, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	currY := 0
	for _, img := range decodedImages {
		dst = imaging.Paste(dst, img, image.Pt(0, currY))
		currY += img.Bounds().Dy()
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 90}); err != nil {
		return images[0], nil
	}
	return buf.Bytes(), nil
}

