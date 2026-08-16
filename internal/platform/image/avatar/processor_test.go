package avatar

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"testing"
	"time"

	"github.com/deepteams/webp"
)

func TestConvertPNGToBoundedWebP(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 1200, 600))
	source.Set(0, 0, color.RGBA{R: 255, A: 255})
	var input bytes.Buffer
	if err := png.Encode(&input, source); err != nil {
		t.Fatal(err)
	}
	processor := NewProcessor(10*time.Second, 1)
	output, err := processor.Convert(context.Background(), input.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := webp.Decode(bytes.NewReader(output))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Bounds().Dx() != 1024 || decoded.Bounds().Dy() != 512 {
		t.Fatalf("unexpected dimensions %v", decoded.Bounds())
	}
}
func TestRejectNonImage(t *testing.T) {
	processor := NewProcessor(time.Second, 1)
	if _, err := processor.Convert(context.Background(), []byte("not an image")); err == nil {
		t.Fatal("expected invalid image error")
	}
}

func TestConvertExistingWebP(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 16, 16))
	var input bytes.Buffer
	if err := webp.Encode(&input, source, &webp.EncoderOptions{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	processor := NewProcessor(time.Second, 1)
	output, err := processor.Convert(context.Background(), input.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = webp.Decode(bytes.NewReader(output)); err != nil {
		t.Fatalf("converted WebP is invalid: %v", err)
	}
}
