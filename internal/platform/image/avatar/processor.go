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

func NewProcessor(timeout time.Duration, maxConcurrent int) *Processor {
	if timeout <= 0 || maxConcurrent <= 0 {
		panic("avatar processor settings must be positive")
	}
	return &Processor{timeout: timeout, slots: make(chan struct{}, maxConcurrent)}
}
func (p *Processor) IsUnsupported(err error) bool {
	return errors.Is(err, ErrUnsupportedFormat) || errors.Is(err, context.DeadlineExceeded)
}

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
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	result := make(chan conversion, 1)
	go func() {
		defer func() { <-p.slots }()
		data, err := convert(input)
		result <- conversion{data: data, err: err}
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

type conversion struct {
	data []byte
	err  error
}

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
func localType(value string) bool {
	return strings.HasPrefix(value, "image/jpeg") || strings.HasPrefix(value, "image/png") || strings.HasPrefix(value, "image/gif") || strings.HasPrefix(value, "image/webp")
}
func looksLikeHEIC(data []byte) bool {
	return len(data) >= 12 && string(data[4:8]) == "ftyp" && (strings.HasPrefix(string(data[8:12]), "hei") || strings.HasPrefix(string(data[8:12]), "mif1"))
}
