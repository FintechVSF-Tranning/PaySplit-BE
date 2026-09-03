package avatar

import (
	"bytes"
	"context"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"strings"
	"time"

	"github.com/deepteams/webp"
	"github.com/disintegration/imaging"
	"github.com/rwcarlsen/goexif/exif"
	_ "golang.org/x/image/webp"
)

var ErrUnsupportedFormat = errors.New("unsupported local image format")
var ErrInvalidImage = errors.New("invalid image")

type Processor struct {
	timeout time.Duration
	slots   chan struct{}
}

// conversion là một struct helper để trả về kết quả từ goroutine.
type conversion struct {
	data []byte
	err  error
}

// NewProcessor khởi tạo processor chuyển đổi ảnh avatar.
// timeout: thời gian chờ tối đa cho mỗi lần convert, tính cả việc chờ slot rảnh.
// maxConcurrent: số lượng ảnh có thể xử lý đồng thời (số slot).
// Nếu timeout hoặc maxConcurrent ≤ 0, processor không thể khởi tạo và hàm sẽ panic.
func NewProcessor(timeout time.Duration, maxConcurrent int) *Processor {
	if timeout <= 0 || maxConcurrent <= 0 {
		panic("avatar processor settings must be positive")
	}
	return &Processor{timeout: timeout, slots: make(chan struct{}, maxConcurrent)}
}

// IsUnsupported kiểm tra xem lỗi có phải là định dạng ảnh không được hỗ trợ hoặc do timeout hay không.
// Nó trả về true nếu err là ErrUnsupportedFormat hoặc context.DeadlineExceeded.
func (p *Processor) IsUnsupported(err error) bool {
	return errors.Is(err, ErrUnsupportedFormat) || errors.Is(err, context.DeadlineExceeded)
}

// Convert chuyển đổi ảnh đầu vào thành định dạng WebP với các ràng buộc về kích thước và chất lượng.
// input: dữ liệu ảnh dưới dạng byte slice.
// Trả về dữ liệu ảnh đã chuyển đổi sang WebP hoặc lỗi nếu có vấn đề.
func (p *Processor) Convert(ctx context.Context, input []byte) ([]byte, error) {
	if len(input) == 0 || len(input) > 10<<20 {
		return nil, ErrInvalidImage
	}
	contentType := http.DetectContentType(input)
	if !localType(contentType) {
		if strings.HasPrefix(contentType, "image/") || looksLikeHEIC(input) {
			return nil, ErrUnsupportedFormat
		}
		return nil, ErrInvalidImage
	}
	select {
	case p.slots <- struct{}{}:
		// Chiếm được 1 slot thành công -> Chạy tiếp xuống dưới!
	case <-ctx.Done():
		// Trong lúc đang đứng chờ slot mà client ngắt kết nối/tắt app -> Hủy, thoát ngay!
		return nil, ctx.Err()
	}
	// Tạo channel để nhận kết quả từ goroutine
	result := make(chan conversion, 1)
	// Goroutine để xử lý convert
	go func() {
		defer func() { <-p.slots }() // Giải phóng slot sau khi xử lý xong
		data, err := convert(input)
		result <- conversion{data: data, err: err}
	}()
	// Tạo timer để giới hạn thời gian xử lý (bao gồm cả chờ slot)
	timer := time.NewTimer(p.timeout)
	// Đảm bảo timer được stop để không rò rỉ tài nguyên
	defer timer.Stop()
	// Chờ kết quả từ goroutine hoặc context timeout
	select {
	case out := <-result:
		// Nhận kết quả convert từ goroutine
		return out.data, out.err
	case <-ctx.Done():
		// Context bị cancel (client ngắt kết nối) hoặc timeout (không có kết quả trong thời gian p.timeout)
		return nil, ctx.Err()
	case <-timer.C:
		// Thời gian chờ xử lý đã hết -> Timeout
		return nil, context.DeadlineExceeded
	}
}

// convert thực hiện chuyển đổi thực tế từ ảnh đầu vào sang WebP.
// Nó chỉ được gọi nội bộ bởi Convert và không tiếp xúc trực tiếp với các channel hoặc timeout.
// Hàm này sẽ panic nếu gặp lỗi nghiêm trọng như image.Decode thất bại hoặc lỗi webp.Encode.
func convert(input []byte) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(input))
	if err != nil {
		return nil, ErrInvalidImage
	}
	img = orient(img, input)
	bounds := img.Bounds()
	if bounds.Dx() <= 0 || bounds.Dy() <= 0 {
		return nil, ErrInvalidImage
	}
	if bounds.Dx() > 1024 || bounds.Dy() > 1024 {
		if bounds.Dx() >= bounds.Dy() {
			img = imaging.Resize(img, 1024, 0, imaging.Lanczos)
		} else {
			img = imaging.Resize(img, 0, 1024, imaging.Lanczos)
		}
	}
	var output bytes.Buffer
	if err = webp.Encode(&output, img, &webp.EncoderOptions{Quality: 82}); err != nil {
		return nil, ErrInvalidImage
	}
	return output.Bytes(), nil
}

// orient xoay ảnh dựa trên thông tin EXIF Orientation.
// Nếu không có EXIF hoặc lỗi đọc EXIF, trả về ảnh gốc.
// Các giá trị Orientation được hỗ trợ: 2 (Flip Horizontal), 3 (Rotate 180), 4 (Flip Vertical), 5 (Transpose), 6 (Rotate 270), 7 (Transverse), 8 (Rotate 90).
func orient(img image.Image, input []byte) image.Image {
	metadata, err := exif.Decode(bytes.NewReader(input))
	if err != nil {
		return img
	}
	tag, err := metadata.Get(exif.Orientation)
	if err != nil {
		return img
	}
	value, err := tag.Int(0)
	if err != nil {
		return img
	}
	switch value {
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

// localType kiểm tra xem một Content-Type có phải là định dạng ảnh nội bộ được hỗ trợ hay không.
// Các định dạng được hỗ trợ: image/jpeg, image/png, image/gif, image/webp.
func localType(value string) bool {
	return strings.HasPrefix(value, "image/jpeg") || strings.HasPrefix(value, "image/png") || strings.HasPrefix(value, "image/gif") || strings.HasPrefix(value, "image/webp")
}

// looksLikeHEIC kiểm tra xem dữ liệu byte có giống với định dạng HEIC hay không dựa trên magic number.
// Lưu ý: Đây chỉ là kiểm tra heuristics đơn giản, không đảm bảo chính xác 100%.
func looksLikeHEIC(data []byte) bool {
	return len(data) >= 12 && string(data[4:8]) == "ftyp" && (strings.HasPrefix(string(data[8:12]), "hei") || strings.HasPrefix(string(data[8:12]), "mif1"))
}
