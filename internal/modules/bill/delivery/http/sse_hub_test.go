package http_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	billhttp "paysplit-backend/internal/modules/bill/delivery/http"
)

func TestSSEHub_SubscribeAndPublish(t *testing.T) {
	hub := billhttp.NewHub(nil)
	billID := uuid.New()

	ch, unsubscribe := hub.Subscribe(billID)
	defer unsubscribe()

	testData := map[string]string{"status": "succeeded"}
	hub.Publish(billhttp.Event{
		Type:   "ocr.updated",
		BillID: billID,
		Data:   testData,
	})

	select {
	case event := <-ch:
		if event.Type != "ocr.updated" {
			t.Errorf("expected event type ocr.updated, got %s", event.Type)
		}
		if event.BillID != billID {
			t.Errorf("expected bill ID %s, got %s", billID, event.BillID)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for SSE event")
	}
}

func TestSSEHub_Unsubscribe(t *testing.T) {
	hub := billhttp.NewHub(nil)
	billID := uuid.New()

	ch, unsubscribe := hub.Subscribe(billID)
	unsubscribe()

	// Kênh ch phải được đóng sau khi unsubscribe
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("expected channel to be closed after unsubscribe")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for channel close")
	}
}
