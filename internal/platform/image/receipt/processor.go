package receipt

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/jpeg"
	_ "image/png"
	"net/http"
	"strings"
	"time"

	"github.com/disintegration/imaging"
	"github.com/rwcarlsen/goexif/exif"

	"paysplit-backend/internal/modules/bill/usecase"
)

var _ usecase.ReceiptProcessor = (*Processor)(nil)

var (
	// ErrUnsupportedFormat trả về khi định dạng ảnh không được hỗ trợ.
	ErrUnsupportedFormat = errors.New("unsupported receipt image format")

	// ErrInvalidImage trả về khi ảnh bị rỗng, hỏng hoặc vượt quá 10MB.
	ErrInvalidImage = errors.New("invalid receipt image")
)

const maxReceiptBytes = 10 * 1024 * 1024 // 10 MB theo Spec 3 AC-1

// Processor chịu trách nhiệm kiểm tra chữ ký file, chuẩn hóa hướng xoay EXIF và convert sang JPEG (Spec 3 AC-1).
type Processor struct {
	timeout time.Duration
	slots   chan struct{}
}

// NewProcessor khởi tạo Receipt Processor với timeout và giới hạn tác vụ đồng thời.
func NewProcessor(timeout time.Duration, maxConcurrent int) *Processor {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	if maxConcurrent <= 0 {
		maxConcurrent = 2
	}
	return &Processor{
		timeout: timeout,
		slots:   make(chan struct{}, maxConcurrent),
	}
}

// IsUnsupported kiểm tra xem lỗi có phải do định dạng không hỗ trợ hoặc timeout hay không.
func (p *Processor) IsUnsupported(err error) bool {
	return errors.Is(err, ErrUnsupportedFormat) || errors.Is(err, context.DeadlineExceeded)
}

// Process tiền xử lý ảnh: kiểm tra kích thước, nhận diện định dạng, xoay đúng chiều EXIF và xuất ra JPEG chuẩn.
func (p *Processor) Process(ctx context.Context, input []byte) ([]byte, error) {
	if len(input) == 0 || len(input) > maxReceiptBytes {
		return nil, ErrInvalidImage
	}

	contentType := http.DetectContentType(input)
	if !isSupportedType(contentType, input) {
		return nil, ErrUnsupportedFormat
	}

	// Nếu là HEIC nhưng Go image chưa có C-Go decoder cho HEIC, kiểm tra signature
	if looksLikeHEIC(input) {
		// Thử giải mã, nếu không giải mã được thì báo lỗi định dạng
		_, _, err := image.Decode(bytes.NewReader(input))
		if err != nil {
			return nil, ErrUnsupportedFormat
		}
	}

	select {
	case p.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	result := make(chan struct {
		data []byte
		err  error
	}, 1)

	go func() {
		defer func() { <-p.slots }()
		data, err := convertToJPEG(input)
		result <- struct {
			data []byte
			err  error
		}{data: data, err: err}
	}()

	timer := time.NewTimer(p.timeout)
	defer timer.Stop()

	select {
	case out := <-result:
		return out.data, out.err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, context.DeadlineExceeded
	}
}

// convertToJPEG giải mã ảnh, tự động xoay theo EXIF và mã hóa thành JPEG chất lượng 90%.
func convertToJPEG(input []byte) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(input))
	if err != nil {
		return nil, ErrInvalidImage
	}

	img = orientImage(img, input)
	bounds := img.Bounds()
	if bounds.Dx() <= 0 || bounds.Dy() <= 0 {
		return nil, ErrInvalidImage
	}

	// Nếu kích thước quá lớn (> 4096px mỗi chiều), resize để tối ưu OCR và lưu trữ
	if bounds.Dx() > 4096 || bounds.Dy() > 4096 {
		if bounds.Dx() >= bounds.Dy() {
			img = imaging.Resize(img, 4096, 0, imaging.Lanczos)
		} else {
			img = imaging.Resize(img, 0, 4096, imaging.Lanczos)
		}
	}

	var output bytes.Buffer
	if err = jpeg.Encode(&output, img, &jpeg.Options{Quality: 90}); err != nil {
		return nil, ErrInvalidImage
	}

	return output.Bytes(), nil
}

// orientImage đọc thẻ EXIF Orientation và xoay ảnh về góc thẳng chuẩn 0 độ.
func orientImage(img image.Image, input []byte) image.Image {
	metadata, err := exif.Decode(bytes.NewReader(input))
	if err != nil {
		return img
	}

	tag, err := metadata.Get(exif.Orientation)
	if err != nil {
		return img
	}

	val, err := tag.Int(0)
	if err != nil {
		return img
	}

	switch val {
	case 2:
		return imaging.FlipH(img)
	case 3:
		return imaging.Rotate180(img)
	case 4:
		return imaging.FlipV(img)
	case 5:
		return imaging.Transpose(img)
	case 6:
		return imaging.Rotate270(img)
	case 7:
		return imaging.Transverse(img)
	case 8:
		return imaging.Rotate90(img)
	default:
		return img
	}
}

func isSupportedType(contentType string, data []byte) bool {
	if strings.HasPrefix(contentType, "image/jpeg") || strings.HasPrefix(contentType, "image/png") {
		return true
	}
	return looksLikeHEIC(data)
}

func looksLikeHEIC(data []byte) bool {
	return len(data) >= 12 && string(data[4:8]) == "ftyp" &&
		(strings.HasPrefix(string(data[8:12]), "hei") || strings.HasPrefix(string(data[8:12]), "mif1"))
}
