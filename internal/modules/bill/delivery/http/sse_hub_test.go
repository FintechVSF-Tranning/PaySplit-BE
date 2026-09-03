package http_test

import (
	"context"
	"strings"
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

func TestSSEHub_HandlePostgresNotificationValidatesEnvelope(t *testing.T) {
	hub := billhttp.NewHub(nil)
	billID := uuid.New()
	ch, unsubscribe := hub.Subscribe(billID)
	defer unsubscribe()

	payload := `{"type":"ocr.updated","bill_id":"` + billID.String() + `","data":{"status":"done"}}`
	if err := hub.HandlePostgresNotification(context.Background(), payload); err != nil {
		t.Fatalf("valid payload rejected: %v", err)
	}
	if event := <-ch; event.BillID != billID || event.Type != "ocr.updated" {
		t.Fatalf("unexpected event: %+v", event)
	}

	for _, invalid := range []string{
		`not-json`,
		`{"type":"ocr.updated","bill_id":"00000000-0000-0000-0000-000000000000"}`,
		`{"type":" ","bill_id":"` + billID.String() + `"}`,
	} {
		if err := hub.HandlePostgresNotification(context.Background(), invalid); err == nil {
			t.Fatalf("invalid payload accepted: %s", invalid)
		}
	}
}

func TestSSEHub_CloseSubscribersIsSafeWithDeferredUnsubscribe(t *testing.T) {
	hub := billhttp.NewHub(nil)
	ch, unsubscribe := hub.Subscribe(uuid.New())
	hub.CloseSubscribers()
	unsubscribe()
	unsubscribe()
	if _, open := <-ch; open {
		t.Fatal("subscriber channel should be closed")
	}
}

func TestSSEHub_SubscriberBufferIs16(t *testing.T) {
	// covers: AC-2
	const billSubscriberBuffer = 16
	hub := billhttp.NewHub(nil)
	billID := uuid.New()
	ch, unsubscribe := hub.Subscribe(billID)
	defer unsubscribe()

	for i := 0; i < billSubscriberBuffer+8; i++ {
		hub.Publish(billhttp.Event{Type: "ocr.updated", BillID: billID})
	}

	got := 0
	for {
		select {
		case <-ch:
			got++
		default:
			if got != billSubscriberBuffer {
				t.Fatalf("buffered events = %d, want %d", got, billSubscriberBuffer)
			}
			return
		}
	}
}

func TestSSEHub_HandlePostgresNotificationErrorOmitsRawPayload(t *testing.T) {
	// covers: AC-4
	hub := billhttp.NewHub(nil)
	const raw = "SECRET_RAW_BILL_PAYLOAD"
	err := hub.HandlePostgresNotification(context.Background(), raw)
	if err == nil {
		t.Fatal("invalid payload accepted")
	}
	if strings.Contains(err.Error(), raw) {
		t.Fatalf("error leaked raw payload: %v", err)
	}
}
