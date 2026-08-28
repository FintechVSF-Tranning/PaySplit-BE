package http

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestHub_PublishReachesOnlySubscribersOfThatGroup(t *testing.T) {
	hub := NewHub(nil)
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
	hub := NewHub(nil)
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
	hub := NewHub(nil)
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
