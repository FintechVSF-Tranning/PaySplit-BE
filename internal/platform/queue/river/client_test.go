package river

import (
	"context"
	"testing"

	"github.com/riverqueue/river"
)

func TestNewClient_NilPool(t *testing.T) {
	workers := river.NewWorkers()
	client, err := NewClient(nil, workers, Config{})
	if err == nil {
		t.Fatalf("expected error when pool is nil, got client: %v", client)
	}
}

func TestNewClient_NilWorkers(t *testing.T) {
	client, err := NewClient(nil, nil, Config{})
	if err == nil {
		t.Fatalf("expected error when workers is nil, got client: %v", client)
	}
}

func TestAutoMigrate_NilPool(t *testing.T) {
	err := AutoMigrate(context.Background(), nil)
	if err == nil {
		t.Fatalf("expected error when pool is nil")
	}
}
