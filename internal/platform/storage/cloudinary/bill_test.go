package cloudinary

import (
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

	url, err := storage.SignedURL("bills/op-123/0", 5*time.Minute)
	if err != nil {
		t.Fatalf("SignedURL() error = %v", err)
	}

	if !strings.Contains(url, "test-cloud") {
		t.Errorf("expected URL to contain cloud name, got %s", url)
	}
	if !strings.Contains(url, "bills/op-123/0") {
		t.Errorf("expected URL to contain public ID, got %s", url)
	}
	if !strings.Contains(url, "s--") {
		t.Errorf("expected URL to be signed with signature token 's--', got %s", url)
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
