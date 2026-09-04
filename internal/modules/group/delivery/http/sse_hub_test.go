package http

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestHub_PublishReachesOnlySubscribersOfThatGroup(t *testing.T) {
	hub := NewHub()
	groupA, groupB := uuid.New(), uuid.New()

	chA, unsubA := hub.Subscribe(groupA)
	defer unsubA()
	chB, unsubB := hub.Subscribe(groupB)
	defer unsubB()

	hub.Publish(Event{GroupID: groupA, Version: 1, Type: "member_joined"})

	select {
	case event := <-chA:
		if event.Version != 1 {
			t.Fatalf("version = %d, want 1", event.Version)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber of the published group received nothing")
	}

	select {
	case event := <-chB:
		t.Fatalf("subscriber of another group received %+v", event)
	default:
	}
}

func TestHub_UnsubscribeStopsDeliveryAndIsIdempotent(t *testing.T) {
	hub := NewHub()
	groupID := uuid.New()

	ch, unsubscribe := hub.Subscribe(groupID)
	unsubscribe()
	// Hàm hủy có thể được gọi lại qua defer trên đường lỗi; gọi hai lần không
	// được panic vì đóng channel lần thứ hai.
	unsubscribe()

	hub.Publish(Event{GroupID: groupID, Version: 1})

	if _, open := <-ch; open {
		t.Fatal("channel should be closed and drained after unsubscribe")
	}
}

// Client chậm không được phép chặn server. Sự kiện bị bỏ là chấp nhận được vì
// version fencing khiến client tự phát hiện lỗ hổng và gọi /sync.
func TestHub_PublishDoesNotBlockOnFullSubscriberBuffer(t *testing.T) {
	hub := NewHub()
	groupID := uuid.New()

	_, unsubscribe := hub.Subscribe(groupID)
	defer unsubscribe()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < subscriberBuffer*3; i++ {
			hub.Publish(Event{GroupID: groupID, Version: int64(i + 1)})
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a subscriber that never reads")
	}
}

// Envelope trên dây phải giải mã đúng shape mà repository ghi ra, kể cả bản
// "gầy" không kèm data khi vượt giới hạn 8000 byte của pg_notify.
func TestEvent_DecodesNotifyEnvelope(t *testing.T) {
	groupID := uuid.New()

	var fat Event
	fatJSON := `{"group_id":"` + groupID.String() + `","version":42,"type":"member_joined","data":{"member":{"display_name":"Nam"}}}`
	if err := json.Unmarshal([]byte(fatJSON), &fat); err != nil {
		t.Fatal(err)
	}
	if fat.GroupID != groupID || fat.Version != 42 || fat.Type != "member_joined" || len(fat.Data) == 0 {
		t.Fatalf("unexpected decoded event: %+v", fat)
	}

	var thin Event
	thinJSON := `{"group_id":"` + groupID.String() + `","version":43,"type":"member_joined"}`
	if err := json.Unmarshal([]byte(thinJSON), &thin); err != nil {
		t.Fatal(err)
	}
	if thin.Version != 43 || len(thin.Data) != 0 {
		t.Fatalf("thin envelope should decode with no data: %+v", thin)
	}
}

func TestHub_HandlePostgresNotificationValidatesEnvelope(t *testing.T) {
	hub := NewHub()
	groupID := uuid.New()
	ch, unsubscribe := hub.Subscribe(groupID)
	defer unsubscribe()

	payload := `{"group_id":"` + groupID.String() + `","version":4,"type":"member_joined"}`
	if err := hub.HandlePostgresNotification(context.Background(), payload); err != nil {
		t.Fatalf("valid payload rejected: %v", err)
	}
	if event := <-ch; event.GroupID != groupID || event.Version != 4 {
		t.Fatalf("unexpected event: %+v", event)
	}

	for _, invalid := range []string{
		`not-json`,
		`{"group_id":"00000000-0000-0000-0000-000000000000","version":1,"type":"member_joined"}`,
		`{"group_id":"` + groupID.String() + `","version":0,"type":"member_joined"}`,
		`{"group_id":"` + groupID.String() + `","version":1,"type":" "}`,
	} {
		if err := hub.HandlePostgresNotification(context.Background(), invalid); err == nil {
			t.Fatalf("invalid payload accepted: %s", invalid)
		}
	}
}

func TestHub_CloseSubscribersIsSafeWithDeferredUnsubscribe(t *testing.T) {
	hub := NewHub()
	ch, unsubscribe := hub.Subscribe(uuid.New())
	hub.CloseSubscribers()
	unsubscribe()
	unsubscribe()
	if _, open := <-ch; open {
		t.Fatal("subscriber channel should be closed")
	}
}

func TestHub_SubscriberBufferIs32(t *testing.T) {
	// covers: AC-2
	hub := NewHub()
	groupID := uuid.New()
	ch, unsubscribe := hub.Subscribe(groupID)
	defer unsubscribe()

	for i := 0; i < subscriberBuffer+8; i++ {
		hub.Publish(Event{GroupID: groupID, Version: int64(i + 1), Type: "member_joined"})
	}

	got := 0
	for {
		select {
		case <-ch:
			got++
		default:
			if got != subscriberBuffer {
				t.Fatalf("buffered events = %d, want %d", got, subscriberBuffer)
			}
			return
		}
	}
}

func TestHub_HandlePostgresNotificationErrorOmitsRawPayload(t *testing.T) {
	// covers: AC-4
	hub := NewHub()
	const raw = "SECRET_RAW_GROUP_PAYLOAD"
	err := hub.HandlePostgresNotification(context.Background(), raw)
	if err == nil {
		t.Fatal("invalid payload accepted")
	}
	if strings.Contains(err.Error(), raw) {
		t.Fatalf("error leaked raw payload: %v", err)
	}
}
