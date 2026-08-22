package cloudinary_test

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/joho/godotenv"

	"paysplit-backend/internal/config"
	"paysplit-backend/internal/platform/storage/cloudinary"
)

// TestIntegration_CloudinaryBillStorage chạy kiểm thử thực tế với Cloudinary nếu có credentials trong .env.
func TestIntegration_CloudinaryBillStorage(t *testing.T) {
	_ = godotenv.Load("../../../../.env")

	cloudName := strings.TrimSpace(os.Getenv("CLOUDINARY_CLOUD_NAME"))
	apiKey := strings.TrimSpace(os.Getenv("CLOUDINARY_API_KEY"))
	apiSecret := strings.TrimSpace(os.Getenv("CLOUDINARY_API_SECRET"))

	if cloudName == "" || apiKey == "" || apiSecret == "" || strings.HasPrefix(apiKey, "replace-") || strings.HasPrefix(cloudName, "replace-") {
		t.Skip("Bỏ qua integration test: Cloudinary credentials chưa được cấu hình thực tế trong .env")
	}

	cfg := config.CloudinaryConfig{
		CloudName: cloudName,
		APIKey:    apiKey,
		APISecret: apiSecret,
	}

	storage, err := cloudinary.NewBillStorage(cfg, 30*time.Second)
	if err != nil {
		t.Fatalf("NewBillStorage() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 1x1 sample JPEG
	sampleJPEG := []byte{
		0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46, 0x49, 0x46, 0x00, 0x01,
		0x01, 0x01, 0x00, 0x48, 0x00, 0x48, 0x00, 0x00, 0xFF, 0xDB, 0x00, 0x43,
		0x00, 0x08, 0x06, 0x06, 0x07, 0x06, 0x05, 0x08, 0x07, 0x07, 0x07, 0x09,
		0x09, 0x08, 0x0A, 0x0C, 0x14, 0x0D, 0x0C, 0x0B, 0x0B, 0x0C, 0x19, 0x12,
		0x13, 0x0F, 0x14, 0x1D, 0x1A, 0x1F, 0x1E, 0x1D, 0x1A, 0x1C, 0x1C, 0x20,
		0x24, 0x2E, 0x27, 0x20, 0x22, 0x2C, 0x23, 0x1C, 0x1C, 0x28, 0x37, 0x29,
		0x2C, 0x30, 0x31, 0x34, 0x34, 0x34, 0x1F, 0x27, 0x39, 0x3D, 0x38, 0x32,
		0x3C, 0x2E, 0x33, 0x34, 0x32, 0xFF, 0xC0, 0x00, 0x0B, 0x08, 0x00, 0x01,
		0x00, 0x01, 0x01, 0x01, 0x11, 0x00, 0xFF, 0xC4, 0x00, 0x1F, 0x00, 0x00,
		0x01, 0x05, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
		0x09, 0x0A, 0x0B, 0xFF, 0xDA, 0x00, 0x08, 0x01, 0x01, 0x00, 0x00, 0x3F,
		0x00, 0xBF, 0x80, 0xFF, 0xD9,
	}

	testPublicID := "bills/test-integration-op/0"

	// 1. Upload Private Asset
	t.Logf("Uploading private test receipt asset: %s", testPublicID)
	uploadedID, err := storage.Upload(ctx, sampleJPEG, testPublicID)
	if err != nil {
		t.Fatalf("Upload() error = %v", err)
	}
	t.Logf("Uploaded successfully! PublicID = %s", uploadedID)

	// Cleanup sau khi test
	defer func() {
		_ = storage.DeleteByPrefix(context.Background(), "bills/test-integration-op")
	}()

	// 2. Sinh Signed URL
	_, err = storage.SignedURL(uploadedID, 5*time.Minute)
	if err != nil {
		t.Fatalf("SignedURL() error = %v", err)
	}

	// 3. Download bytes
	downloadedBytes, err := storage.Download(ctx, uploadedID)
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	if len(downloadedBytes) == 0 {
		t.Fatal("expected non-empty downloaded bytes")
	}
	t.Logf("Downloaded %d bytes successfully!", len(downloadedBytes))

	// 4. Delete
	if err := storage.Delete(ctx, uploadedID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	t.Logf("Deleted test receipt successfully!")
}

func TestIntegration_CloudinaryProofStorageConvertsPNGToWebP(t *testing.T) {
	_ = godotenv.Load("../../../../.env")

	cloudName := strings.TrimSpace(os.Getenv("CLOUDINARY_CLOUD_NAME"))
	apiKey := strings.TrimSpace(os.Getenv("CLOUDINARY_API_KEY"))
	apiSecret := strings.TrimSpace(os.Getenv("CLOUDINARY_API_SECRET"))
	if cloudName == "" || apiKey == "" || apiSecret == "" || strings.HasPrefix(apiKey, "replace-") || strings.HasPrefix(cloudName, "replace-") {
		t.Skip("Bỏ qua integration test: Cloudinary credentials chưa được cấu hình thực tế trong .env")
	}

	storage, err := cloudinary.NewProofStorage(config.CloudinaryConfig{
		CloudName: cloudName,
		APIKey:    apiKey,
		APISecret: apiSecret,
	}, 30*time.Second)
	if err != nil {
		t.Fatalf("NewProofStorage() error = %v", err)
	}

	source := image.NewRGBA(image.Rect(0, 0, 2, 2))
	source.Set(0, 0, color.White)
	source.Set(1, 0, color.Black)
	var input bytes.Buffer
	if err = png.Encode(&input, source); err != nil {
		t.Fatalf("png.Encode() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	publicID := "payments/test-integration-payment/proofs/png-to-webp"
	uploadedID, err := storage.Upload(ctx, input.Bytes(), publicID)
	if err != nil {
		t.Fatalf("Upload() error = %v", err)
	}
	defer func() { _ = storage.Delete(context.Background(), uploadedID) }()

	signedURL, err := storage.SignedURL(uploadedID, 5*time.Minute)
	if err != nil {
		t.Fatalf("SignedURL() error = %v", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, signedURL, nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext() error = %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("download WebP proof: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("download status=%d", resp.StatusCode)
	}
	output, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if len(output) < 12 || string(output[:4]) != "RIFF" || string(output[8:12]) != "WEBP" {
		t.Fatalf("downloaded proof is not WebP, first bytes=%x", output)
	}
}
