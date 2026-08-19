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
	"log"
	"time"

	"github.com/disintegration/imaging"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"paysplit-backend/internal/modules/bill/domain"
	"paysplit-backend/internal/modules/bill/repository"
	"paysplit-backend/internal/modules/bill/usecase"
	platformmetrics "paysplit-backend/internal/platform/metrics"
)

// Bộ mã lỗi OCR đóng (bounded), dùng cho ocr_jobs.error_message, SSE và nhãn Prometheus.
// Không bao giờ dùng chuỗi lỗi thô của provider/DB làm 1 trong 3 nơi trên (Spec 3 security model, AC-14).
const (
	ocrErrorBillNotFound        = "bill_not_found"
	ocrErrorNoImages            = "no_images"
	ocrErrorDownloadFailed      = "download_failed"
	ocrErrorSchemaInvalid       = "schema_invalid"
	ocrErrorProviderTimeout     = "provider_timeout"
	ocrErrorProviderUnavailable = "provider_unavailable"
	ocrErrorProvider            = "provider_error"
)

// classifyProviderError rút gọn một lỗi provider/DB bất kỳ thành 1 mã trong bộ mã đóng ở trên.
func classifyProviderError(err error) string {
	switch {
	case errors.Is(err, domain.ErrOcrTimeout):
		return ocrErrorProviderTimeout
	case errors.Is(err, domain.ErrOcrProviderUnavailable):
		return ocrErrorProviderUnavailable
	default:
		return ocrErrorProvider
	}
}

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
	repo           repository.Repository
	storage        usecase.BillStorage
	ocrProvider    usecase.OCRProvider
	broadcaster    EventBroadcaster
	timeout        time.Duration
	retryBaseDelay time.Duration
}

// SetRetryBaseDelay cấu hình độ trễ nền cho backoff giữa các lần retry (BILL_OCR_RETRY_BASE_DELAY, Spec 3 AC-3).
func (w *OCRWorker) SetRetryBaseDelay(d time.Duration) {
	if d > 0 {
		w.retryBaseDelay = d
	}
}

// NextRetry ghi đè lịch retry mặc định của River (ATTEMPT^4 giây) bằng exponential backoff dựa trên
// BILL_OCR_RETRY_BASE_DELAY đã cấu hình, chỉ áp dụng cho job OCR (Spec 3 AC-3: "configured timeout and retry values").
func (w *OCRWorker) NextRetry(job *river.Job[OCRJobArgs]) time.Time {
	base := w.retryBaseDelay
	if base <= 0 {
		base = time.Second
	}
	attempt := job.Attempt
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 20 {
		attempt = 20 // chặn tràn số khi shift bit với số mũ quá lớn
	}
	return time.Now().Add(base * time.Duration(uint64(1)<<uint(attempt-1)))
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
		return errors.New("ocr worker dependencies not configured")
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
			_ = w.failJob(ctx, jobID, billID, ocrErrorBillNotFound)
			return nil
		}
		return fmt.Errorf("get bill by id: %w", err)
	}

	if len(bill.Images) == 0 {
		_ = w.failJob(ctx, jobID, billID, ocrErrorNoImages)
		return nil
	}

	// 4. Tải byte ảnh riêng tư từ Cloudinary (hỗ trợ ghép 1-5 ảnh cho hóa đơn nhiều trang, Spec 3 AC-1)
	var allBytes [][]byte
	for _, img := range bill.Images {
		b, err := w.storage.Download(ctx, img.ImageKey)
		if err != nil {
			if job.Attempt >= job.MaxAttempts {
				log.Printf("event=ocr_download_failed job_id=%s bill_id=%s attempt=%d err=%v", jobID, billID, job.Attempt, err)
				_ = w.failJob(ctx, jobID, billID, ocrErrorDownloadFailed)
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

	startExtract := time.Now()
	candidate, rawJSON, err := w.ocrProvider.ExtractReceipt(extractCtx, imageBytes, "image/jpeg")
	extractDur := time.Since(startExtract)
	imageBytes = nil
	if err != nil {
		platformmetrics.RecordOCRProviderDuration("llamaextract", "failure", extractDur)
		// Nếu lỗi do schema AI không đọc được hoặc cấu trúc sai -> đánh dấu failed, không retry
		if errors.Is(err, domain.ErrOcrSchemaInvalid) {
			log.Printf("event=ocr_schema_invalid job_id=%s bill_id=%s err=%v", jobID, billID, err)
			_ = w.failJob(ctx, jobID, billID, ocrErrorSchemaInvalid)
			return nil
		}

		// Nếu hết số lần retry tối đa của River Queue
		if job.Attempt >= job.MaxAttempts {
			log.Printf("event=ocr_provider_failed job_id=%s bill_id=%s attempt=%d err=%v", jobID, billID, job.Attempt, err)
			_ = w.failJob(ctx, jobID, billID, classifyProviderError(err))
			return nil
		}

		// Trả về error để River Queue tự động retry với exponential backoff
		return fmt.Errorf("extract receipt: %w", err)
	}

	platformmetrics.RecordOCRProviderDuration("llamaextract", "success", extractDur)
	platformmetrics.RecordOCRJob("succeeded", "none")

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
	platformmetrics.RecordOCRJob("failed", reason)
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

// OCRRetentionJobArgs định nghĩa payload công việc dọn dẹp raw OCR cũ hơn 30 ngày (Spec 3 index.md:176, 0001:50).
type OCRRetentionJobArgs struct {
	OlderThanHours int `json:"older_than_hours"`
}

// Kind định danh loại job trong River Queue.
func (OCRRetentionJobArgs) Kind() string { return "ocr_raw_retention_cleanup" }

// OCRRetentionWorker là worker định kỳ dọn dẹp raw_response của các job OCR đã hoàn tất sau 30 ngày.
type OCRRetentionWorker struct {
	river.WorkerDefaults[OCRRetentionJobArgs]
	repo repository.Repository
}

// NewOCRRetentionWorker khởi tạo OCRRetentionWorker.
func NewOCRRetentionWorker(repo repository.Repository) *OCRRetentionWorker {
	return &OCRRetentionWorker{repo: repo}
}

// PollQueueDepth định kỳ đọc trực tiếp số lượng job OCR đang queued/processing từ database và ghi
// vào gauge paysplit_ocr_queue_depth. Đây là nguồn sự thật thay cho việc cộng/trừ trong tiến trình,
// nên luôn chính xác qua restart, rollback giao dịch và khi chạy nhiều replica (Spec 3 AC-14).
func PollQueueDepth(ctx context.Context, repo repository.Repository, interval time.Duration) {
	if repo == nil {
		return
	}
	if interval <= 0 {
		interval = 15 * time.Second
	}
	pollQueueDepthOnce(ctx, repo)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pollQueueDepthOnce(ctx, repo)
		}
	}
}

func pollQueueDepthOnce(ctx context.Context, repo repository.Repository) {
	count, err := repo.CountActiveOCRJobs(ctx)
	if err != nil {
		log.Printf("event=ocr_queue_depth_poll_failed err=%v", err)
		return
	}
	platformmetrics.SetOCRQueueDepth(float64(count))
}

// Work thực hiện xóa dữ liệu raw_response của các job đã hoàn tất quá thời hạn retention.
func (w *OCRRetentionWorker) Work(ctx context.Context, job *river.Job[OCRRetentionJobArgs]) error {
	if w.repo == nil {
		return nil
	}
	hours := job.Args.OlderThanHours
	if hours <= 0 {
		hours = 24 * 30 // 30 ngày mặc định
	}
	retention := time.Duration(hours) * time.Hour
	_, err := w.repo.PurgeExpiredRawOCRResponses(ctx, retention)
	return err
}

// Enqueuer hỗ trợ đẩy công việc OCR vào River Queue.
type Enqueuer struct {
	client         *river.Client[pgx.Tx]
	ocrMaxAttempts int
}

// NewEnqueuer khởi tạo Enqueuer cho module Bill & OCR.
// ocrMaxAttempts áp dụng cho các job OCR (BILL_OCR_MAX_ATTEMPTS, Spec 3 AC-3); <=0 dùng mặc định của River.
func NewEnqueuer(client *river.Client[pgx.Tx], ocrMaxAttempts int) *Enqueuer {
	return &Enqueuer{client: client, ocrMaxAttempts: ocrMaxAttempts}
}

func (e *Enqueuer) ocrInsertOpts() *river.InsertOpts {
	if e.ocrMaxAttempts <= 0 {
		return nil
	}
	return &river.InsertOpts{MaxAttempts: e.ocrMaxAttempts}
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
	}, e.ocrInsertOpts())
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
	}, e.ocrInsertOpts())
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
