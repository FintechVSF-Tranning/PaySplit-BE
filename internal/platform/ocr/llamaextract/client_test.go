package llamaextract

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"paysplit-backend/internal/config"
	"paysplit-backend/internal/modules/bill/domain"
)

func TestClient_MissingAPIKey(t *testing.T) {
	client := New(config.OCRConfig{
		APIKey:          "",
		ProviderTimeout: 5 * time.Second,
	})

	_, _, err := client.ExtractReceipt(context.Background(), []byte("fake-image"), "image/jpeg")
	if !errors.Is(err, domain.ErrOcrProviderUnavailable) {
		t.Fatalf("expected ErrOcrProviderUnavailable, got %v", err)
	}
}

func TestClient_EmptyImageBytes(t *testing.T) {
	client := New(config.OCRConfig{
		APIKey:          "test-key",
		ProviderTimeout: 5 * time.Second,
	})

	_, _, err := client.ExtractReceipt(context.Background(), nil, "image/jpeg")
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput, got %v", err)
	}
}

func TestClient_SuccessFlow(t *testing.T) {
	pollCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Kiểm tra Authorization header
		if r.Header.Get("Authorization") != "Bearer test-api-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/beta/files":
			// Bước 1: Upload file
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id": "fil-test-123"}`))

		case r.Method == http.MethodPost && r.URL.Path == "/api/v2/extract":
			// Bước 2: Trigger extract job
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id": "job-test-456"}`))

		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/extract/job-test-456":
			// Bước 3: Polling
			w.Header().Set("Content-Type", "application/json")
			pollCount++
			if pollCount == 1 {
				// Lần 1: PENDING
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"status": "PENDING"}`))
			} else {
				// Lần 2: COMPLETED
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{
					"status": "COMPLETED",
					"extract_result": {
						"merchant_name": "Nhà Hàng Sen Tây Hồ",
						"bill_date": "2026-08-17",
						"items": [
							{"name": "Buffet Trưa", "quantity": "2", "unit_price": 350000, "line_total": 700000}
						],
						"subtotal": 700000,
						"service_charge": 0,
						"vat": 70000,
						"discount": 0,
						"total": 770000,
						"confidence": 0.98
					}
				}`))
			}

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := New(config.OCRConfig{
		APIKey:          "test-api-key",
		Endpoint:        server.URL,
		ProviderTimeout: 5 * time.Second,
	})
	client.SetHTTPClient(server.Client())

	candidate, rawJSON, err := client.ExtractReceipt(context.Background(), []byte("dummy-receipt-bytes"), "image/jpeg")
	if err != nil {
		t.Fatalf("ExtractReceipt() error = %v", err)
	}

	if candidate == nil {
		t.Fatal("expected non-nil candidate")
	}
	if candidate.MerchantName == nil || *candidate.MerchantName != "Nhà Hàng Sen Tây Hồ" {
		t.Errorf("unexpected merchant name: %v", candidate.MerchantName)
	}
	if candidate.Total != 770000 {
		t.Errorf("unexpected total: %d", candidate.Total)
	}
	if len(rawJSON) == 0 {
		t.Error("expected non-empty rawJSON")
	}
}

func TestClient_JobFailed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/beta/files":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id": "fil-test-123"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v2/extract":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id": "job-test-fail"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/extract/job-test-fail":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status": "FAILED", "error": "unreadable image"}`))
		}
	}))
	defer server.Close()

	client := New(config.OCRConfig{
		APIKey:          "test-api-key",
		Endpoint:        server.URL,
		ProviderTimeout: 5 * time.Second,
	})
	client.SetHTTPClient(server.Client())

	_, _, err := client.ExtractReceipt(context.Background(), []byte("dummy-receipt-bytes"), "image/jpeg")
	if err == nil {
		t.Fatal("expected error for failed extraction job")
	}
	if !errors.Is(err, domain.ErrOcrSchemaInvalid) {
		t.Errorf("expected ErrOcrSchemaInvalid, got %v", err)
	}
}

func TestClient_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/beta/files":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id": "fil-test-123"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v2/extract":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id": "job-test-timeout"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/extract/job-test-timeout":
			// Luôn trả về PENDING để test timeout
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status": "PENDING"}`))
		}
	}))
	defer server.Close()

	client := New(config.OCRConfig{
		APIKey:          "test-api-key",
		Endpoint:        server.URL,
		ProviderTimeout: 800 * time.Millisecond,
	})
	client.SetHTTPClient(server.Client())

	_, _, err := client.ExtractReceipt(context.Background(), []byte("dummy-receipt-bytes"), "image/jpeg")
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !errors.Is(err, domain.ErrOcrTimeout) {
		t.Errorf("expected ErrOcrTimeout, got %v", err)
	}
}
