package receipt

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"testing"
	"time"
)

func TestProcessor_ValidJPEG(t *testing.T) {
	proc := NewProcessor(5*time.Second, 2)

	// Tạo 1 ảnh JPEG mẫu 100x100
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	for x := 0; x < 100; x++ {
		for y := 0; y < 100; y++ {
			img.Set(x, y, color.RGBA{R: 255, G: 0, B: 0, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}

	out, err := proc.Process(context.Background(), buf.Bytes())
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}

	if len(out) == 0 {
		t.Fatal("expected non-empty output")
	}

	contentType := http.DetectContentType(out)
	if contentType != "image/jpeg" {
		t.Errorf("expected image/jpeg, got %s", contentType)
	}
}

func TestProcessor_ValidPNGConvertedToJPEG(t *testing.T) {
	proc := NewProcessor(5*time.Second, 2)

	// Tạo 1 ảnh PNG mẫu
	img := image.NewRGBA(image.Rect(0, 0, 50, 50))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}

	out, err := proc.Process(context.Background(), buf.Bytes())
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}

	contentType := http.DetectContentType(out)
	if contentType != "image/jpeg" {
		t.Errorf("expected PNG to be converted to image/jpeg, got %s", contentType)
	}
}

func TestProcessor_EmptyAndOversized(t *testing.T) {
	proc := NewProcessor(5*time.Second, 2)

	_, err := proc.Process(context.Background(), nil)
	if !errors.Is(err, ErrInvalidImage) {
		t.Errorf("expected ErrInvalidImage for nil input, got %v", err)
	}

	oversized := make([]byte, 11*1024*1024) // 11MB
	_, err = proc.Process(context.Background(), oversized)
	if !errors.Is(err, ErrInvalidImage) {
		t.Errorf("expected ErrInvalidImage for oversized input, got %v", err)
	}
}

func TestProcessor_UnsupportedFormat(t *testing.T) {
	proc := NewProcessor(5*time.Second, 2)

	textData := []byte("this is plain text, not an image")
	_, err := proc.Process(context.Background(), textData)
	if !errors.Is(err, ErrUnsupportedFormat) {
		t.Errorf("expected ErrUnsupportedFormat, got %v", err)
	}
}

func TestProcessor_TimeoutReleasesSlot(t *testing.T) {
	// Processor with 1 slot and very small timeout
	proc := NewProcessor(10*time.Millisecond, 1)

	// Create valid image
	img := image.NewRGBA(image.Rect(0, 0, 50, 50))
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)

	// Create a context that is already canceled to trigger immediate timeout/cancellation
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()
	time.Sleep(2 * time.Millisecond)

	_, err := proc.Process(ctx, buf.Bytes())
	if err == nil {
		t.Error("expected error due to canceled context, got nil")
	}

	// Next call with healthy context must acquire the slot without blocking
	healthyCtx, cancelHealthy := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelHealthy()

	out, err := proc.Process(healthyCtx, buf.Bytes())
	if err != nil {
		t.Fatalf("expected second call to succeed after slot release, got %v", err)
	}
	if len(out) == 0 {
		t.Error("expected non-empty output")
	}
}
