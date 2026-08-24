package cloudinary

import (
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"paysplit-backend/internal/config"
	"paysplit-backend/internal/modules/bill/usecase"
)

var _ usecase.BillStorage = (*BillStorage)(nil)

func TestBillStorage_SignedURL(t *testing.T) {
	storage, err := NewBillStorage(config.CloudinaryConfig{
		CloudName: "test-cloud",
		APIKey:    "test-key",
		APISecret: "test-secret",
	}, 15*time.Second)
	if err != nil {
		t.Fatalf("NewBillStorage() error = %v", err)
	}

	signedURL, err := storage.SignedURL("bills/op-123/0", 5*time.Minute)
	if err != nil {
		t.Fatalf("SignedURL() error = %v", err)
	}

	parsed, err := url.Parse(signedURL)
	if err != nil {
		t.Fatalf("parse signed URL: %v", err)
	}
	if parsed.Host != "api.cloudinary.com" || !strings.Contains(parsed.Path, "/v1_1/test-cloud/image/download") {
		t.Errorf("expected private Cloudinary download URL, got %s", signedURL)
	}
	query := parsed.Query()
	if query.Get("public_id") != "bills/op-123/0" || query.Get("format") != "jpg" || query.Get("signature") == "" {
		t.Errorf("expected signed private download parameters, got %s", signedURL)
	}
	expiresAt, err := strconv.ParseInt(query.Get("expires_at"), 10, 64)
	if err != nil {
		t.Fatalf("expected expires_at in signed URL, got %s", signedURL)
	}
	remaining := time.Until(time.Unix(expiresAt, 0))
	if remaining < 4*time.Minute+55*time.Second || remaining > 5*time.Minute+5*time.Second {
		t.Errorf("expected five minute expiry, got %s", remaining)
	}
}

func TestBillStorage_SignedURL_EmptyPublicID(t *testing.T) {
	storage, err := NewBillStorage(config.CloudinaryConfig{
		CloudName: "test-cloud",
		APIKey:    "test-key",
		APISecret: "test-secret",
	}, 15*time.Second)
	if err != nil {
		t.Fatalf("NewBillStorage() error = %v", err)
	}

	_, err = storage.SignedURL("", 5*time.Minute)
	if err == nil {
		t.Fatal("expected error for empty publicID")
	}
}

func TestProofStorage_SignedURLUsesWebP(t *testing.T) {
	storage, err := NewProofStorage(config.CloudinaryConfig{
		CloudName: "test-cloud",
		APIKey:    "test-key",
		APISecret: "test-secret",
	}, time.Second)
	if err != nil {
		t.Fatalf("NewProofStorage() error = %v", err)
	}

	signedURL, err := storage.SignedURL("payments/payment/proofs/operation", 5*time.Minute)
	if err != nil {
		t.Fatalf("SignedURL() error = %v", err)
	}
	parsed, err := url.Parse(signedURL)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	if got := parsed.Query().Get("format"); got != "webp" {
		t.Fatalf("format=%q, want webp", got)
	}
}
