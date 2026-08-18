package llamaextract_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joho/godotenv"

	"paysplit-backend/internal/config"
	"paysplit-backend/internal/platform/ocr/llamaextract"
)

// TestIntegration_LlamaExtract chạy kiểm thử thực tế với API LlamaCloud và file ảnh trong thư mục bills/.
func TestIntegration_LlamaExtract(t *testing.T) {
	// Đọc file .env nếu có
	_ = godotenv.Load("../../../../.env")

	apiKey := strings.TrimSpace(os.Getenv("LLAMAINDEX_API_KEY"))
	if apiKey == "" {
		t.Skip("Bỏ qua integration test: LLAMAINDEX_API_KEY chưa được thiết lập trong môi trường / .env")
	}

	endpoint := strings.TrimSpace(os.Getenv("LLAMAINDEX_EXTRACT_ENDPOINT"))
	if endpoint == "" {
		endpoint = "https://api.cloud.llamaindex.ai"
	}

	cfg := config.OCRConfig{
		APIKey:          apiKey,
		Endpoint:        endpoint,
		ProviderTimeout: 60 * time.Second,
	}

	client := llamaextract.New(cfg)

	// Đọc ảnh thật từ testdata/bills/
	imagePath := "../../../../testdata/bills/images.jpeg"
	imageBytes, err := os.ReadFile(imagePath)
	if err != nil {
		// Thử tìm bất kỳ ảnh nào trong thư mục testdata/bills/
		matches, _ := filepath.Glob("../../../../testdata/bills/*")
		for _, m := range matches {
			if strings.HasSuffix(m, ".jpg") || strings.HasSuffix(m, ".jpeg") || strings.HasSuffix(m, ".png") {
				imageBytes, err = os.ReadFile(m)
				imagePath = m
				break
			}
		}
	}

	if err != nil || len(imageBytes) == 0 {
		t.Fatalf("Không tìm thấy ảnh hợp lệ trong thư mục testdata/bills/: %v", err)
	}

	t.Logf("Bắt đầu gửi ảnh %s (%d bytes) sang LlamaExtract...", imagePath, len(imageBytes))

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	start := time.Now()
	candidate, rawJSON, err := client.ExtractReceipt(ctx, imageBytes, "image/jpeg")
	duration := time.Since(start)

	if err != nil {
		if strings.Contains(err.Error(), "timeout") || strings.Contains(err.Error(), "handshake") || strings.Contains(err.Error(), "connection refused") || strings.Contains(err.Error(), "unavailable") {
			t.Skipf("Bỏ qua do sự cố mạng kết nối đến LlamaCloud: %v", err)
		}
		t.Fatalf("LlamaExtract extraction failed sau %v: err = %v\nRaw JSON: %s", duration, err, string(rawJSON))
	}

	t.Logf("Extraction thành công sau %v!", duration)

	candidateJSON, _ := json.MarshalIndent(candidate, "", "  ")
	t.Logf("=== KẾT QUẢ CHUẨN HÓA (Candidate) ===\n%s", string(candidateJSON))

	if len(rawJSON) > 0 {
		t.Logf("=== RAW JSON TỪ LLAMACLOUD ===\n%s", string(rawJSON))
	}

	// Assertions cơ bản
	if candidate == nil {
		t.Fatal("expected non-nil candidate")
	}
	if len(candidate.Items) == 0 {
		t.Log("Cảnh báo: không bóc tách được món nào (items rỗng)")
	}
}
