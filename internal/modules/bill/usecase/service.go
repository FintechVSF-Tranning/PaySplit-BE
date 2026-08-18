package usecase

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"paysplit-backend/internal/modules/bill/domain"
	"paysplit-backend/internal/modules/bill/repository"
)

// OCRProvider là interface định nghĩa khả năng bóc tách dữ liệu từ ảnh hóa đơn (Spec 3 AC-3).
// Triển khai của interface này nằm ở platform adapter (ví dụ: internal/platform/ocr/llamaextract).
type OCRProvider interface {
	// ExtractReceipt gửi byte ảnh hóa đơn sang dịch vụ OCR/AI và nhận về kết quả chuẩn hóa
	// (*domain.OCRCandidate) cùng với raw JSON payload gốc ([]byte) để phục vụ lưu trữ audit.
	ExtractReceipt(ctx context.Context, imageBytes []byte, mimeType string) (*domain.OCRCandidate, []byte, error)
}

// BillStorage là interface định nghĩa khả năng lưu trữ ảnh hóa đơn riêng tư trên Cloudinary (Spec 3 AC-1, AC-8, AC-13).
type BillStorage interface {
	// Upload lưu ảnh lên Cloudinary dưới dạng Private Asset với publicID theo quy tắc "bills/{op_id}/{position}".
	Upload(ctx context.Context, data []byte, publicID string) (string, error)

	// SignedURL sinh URL có chữ ký tạm thời (5 phút) để client mobile xem ảnh riêng tư an toàn.
	SignedURL(publicID string, ttl time.Duration) (string, error)

	// Download tải byte ảnh private về cho River Background Worker phục vụ trích xuất OCR.
	Download(ctx context.Context, publicID string) ([]byte, error)

	// Delete xóa một ảnh hóa đơn cụ thể.
	Delete(ctx context.Context, publicID string) error

	// DeleteByPrefix xóa toàn bộ ảnh hóa đơn theo tiền tố "bills/{op_id}/" (dùng cho media cleanup khi hủy draft).
	DeleteByPrefix(ctx context.Context, prefix string) error
}

// ReceiptProcessor là interface tiền xử lý ảnh hóa đơn (Spec 3 AC-1).
type ReceiptProcessor interface {
	// Process kiểm tra magic bytes, xoay góc ảnh theo EXIF và chuẩn hóa sang JPEG chất lượng cao.
	Process(ctx context.Context, input []byte) ([]byte, error)

	// IsUnsupported kiểm tra xem lỗi có phải do định dạng ảnh không được hỗ trợ hay không.
	IsUnsupported(err error) bool
}

// JobEnqueuer là interface đẩy công việc OCR vào hàng đợi River Queue.
type JobEnqueuer interface {
	EnqueueOCRJobTx(ctx context.Context, tx pgx.Tx, billID, jobID, groupID uuid.UUID) error
	EnqueueOCRJob(ctx context.Context, billID, jobID, groupID uuid.UUID) error
}

// Service quản lý toàn bộ nghiệp vụ của module Bill và OCR.
type Service struct {
	repo        repository.Repository
	ocrProvider OCRProvider
	storage     BillStorage
	processor   ReceiptProcessor
	enqueuer    JobEnqueuer
}

// NewService khởi tạo Bill usecase service với các dependencies.
func NewService(
	repo repository.Repository,
	ocrProvider OCRProvider,
	storage BillStorage,
	processor ReceiptProcessor,
	enqueuer JobEnqueuer,
) *Service {
	return &Service{
		repo:        repo,
		ocrProvider: ocrProvider,
		storage:     storage,
		processor:   processor,
		enqueuer:    enqueuer,
	}
}
