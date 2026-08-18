package usecase

import (
	"context"

	"paysplit-backend/internal/modules/bill/domain"
)

// OCRProvider là interface định nghĩa khả năng bóc tách dữ liệu từ ảnh hóa đơn (Spec 3 AC-3).
// Triển khai của interface này nằm ở platform adapter (ví dụ: internal/platform/ocr/llamaextract).
type OCRProvider interface {
	// ExtractReceipt gửi byte ảnh hóa đơn sang dịch vụ OCR/AI và nhận về kết quả chuẩn hóa
	// (*domain.OCRCandidate) cùng với raw JSON payload gốc ([]byte) để phục vụ lưu trữ audit.
	ExtractReceipt(ctx context.Context, imageBytes []byte, mimeType string) (*domain.OCRCandidate, []byte, error)
}

// Service quản lý toàn bộ nghiệp vụ của module Bill và OCR.
type Service struct {
	ocrProvider OCRProvider
}

// NewService khởi tạo Bill usecase service với các dependencies.
func NewService(ocrProvider OCRProvider) *Service {
	return &Service{
		ocrProvider: ocrProvider,
	}
}
