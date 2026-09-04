package http

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"paysplit-backend/internal/platform/realtime"
)

func TestHubReplaceKeepsReplacementStream(t *testing.T) {
	// covers: AC-14
	hub := NewHub(nil)
	userID := uuid.Must(uuid.NewV7())
	sid := uuid.Must(uuid.NewV7())
	oldID := uuid.Must(uuid.NewV7())
	newID := uuid.Must(uuid.NewV7())
	oldCh, oldAdmit, _ := hub.RegisterPaused(userID, sid, oldID)
	// Control của chính stream cũ tới trước: nó trở thành stream đang hoạt động.
	hub.ApplyReplace([]uuid.UUID{sid}, oldID)
	select {
	case <-oldAdmit:
	default:
		t.Fatal("old stream was not admitted by its own control")
	}
	newCh, newAdmit, _ := hub.RegisterPaused(userID, sid, newID)

	hub.ApplyReplace([]uuid.UUID{sid}, newID)

	select {
	case frame := <-oldCh:
		if frame.Event != "close" {
			t.Fatalf("old stream event = %s", frame.Event)
		}
	case <-time.After(time.Second):
		t.Fatal("old stream was not closed")
	}
	select {
	case <-newAdmit:
	default:
		t.Fatal("replacement stream was not admitted")
	}
	if !hub.Activated(newID) {
		t.Fatal("replacement stream is not active")
	}
	select {
	case frame := <-newCh:
		t.Fatalf("replacement stream received %s instead of staying open", frame.Event)
	default:
	}
}

// Hai kết nối đồng thời của cùng một phiên: control cũ không được đóng ứng viên
// đăng ký sau nó, và người thắng luôn là control commit sau cùng — bất kể thứ tự
// đăng ký.
func TestHubConcurrentReplaceKeepsExactlyOneStream(t *testing.T) {
	// covers: AC-14
	for _, tc := range []struct {
		name       string
		applyOrder []int
	}{
		{name: "a_then_b", applyOrder: []int{0, 1}},
		{name: "b_then_a", applyOrder: []int{1, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hub := NewHub(nil)
			userID := uuid.Must(uuid.NewV7())
			sid := uuid.Must(uuid.NewV7())
			ids := []uuid.UUID{uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())}
			chans := make([]<-chan Frame, 2)
			// Cả hai đăng ký xong trước khi bất kỳ control nào được áp.
			for i, id := range ids {
				ch, _, _ := hub.RegisterPaused(userID, sid, id)
				chans[i] = ch
			}

			for _, idx := range tc.applyOrder {
				hub.ApplyReplace([]uuid.UUID{sid}, ids[idx])
			}

			winner := ids[tc.applyOrder[len(tc.applyOrder)-1]]
			loser := ids[tc.applyOrder[0]]
			if !hub.Activated(winner) {
				t.Fatal("last committed control did not win")
			}
			if hub.Activated(loser) {
				t.Fatal("loser stream is still active")
			}
			loserCh := chans[tc.applyOrder[0]]
			select {
			case frame := <-loserCh:
				if frame.Event != "close" {
					t.Fatalf("loser event = %s", frame.Event)
				}
			case <-time.After(time.Second):
				t.Fatal("loser stream was not closed")
			}
			winnerCh := chans[tc.applyOrder[len(tc.applyOrder)-1]]
			select {
			case frame := <-winnerCh:
				t.Fatalf("winner stream received %s", frame.Event)
			default:
			}
		})
	}
}

// Một control cũ đến sau khi ứng viên mới đã đăng ký nhưng chưa được kích hoạt:
// ứng viên phải sống sót để control của chính nó quyết định.
func TestHubStaleReplaceDoesNotClosePausedCandidate(t *testing.T) {
	// covers: AC-14
	hub := NewHub(nil)
	userID := uuid.Must(uuid.NewV7())
	sid := uuid.Must(uuid.NewV7())
	candidate := uuid.Must(uuid.NewV7())
	ch, admit, _ := hub.RegisterPaused(userID, sid, candidate)

	hub.ApplyReplace([]uuid.UUID{sid}, uuid.Must(uuid.NewV7()))

	select {
	case frame, ok := <-ch:
		t.Fatalf("paused candidate was disturbed (frame=%v open=%t)", frame.Event, ok)
	default:
	}
	select {
	case <-admit:
		t.Fatal("paused candidate was admitted by a control naming another stream")
	default:
	}
	if hub.Activated(candidate) {
		t.Fatal("paused candidate became active without its own control")
	}
}

func TestHubOverflowClosesWithBackpressure(t *testing.T) {
	hub := NewHub(nil)
	userID := uuid.Must(uuid.NewV7())
	sid := uuid.Must(uuid.NewV7())
	streamID := uuid.Must(uuid.NewV7())
	ch, _, _ := hub.RegisterPaused(userID, sid, streamID)
	body := realtime.InvalidateBody{Scope: realtime.ScopeHome, GroupID: uuid.Must(uuid.NewV7()), Type: "home.balance_changed"}
	for i := 0; i < subscriberBuffer+1; i++ {
		hub.dispatchUsers([]uuid.UUID{userID}, Frame{Event: "invalidate", Data: body})
	}
	var sawClose bool
	deadline := time.After(time.Second)
	for !sawClose {
		select {
		case frame, ok := <-ch:
			if !ok {
				sawClose = true
			} else if frame.Event == "close" {
				sawClose = true
			}
		case <-deadline:
			t.Fatal("overflow did not close the stream")
		}
	}
}

func TestHubInvalidateDoesNotCrossUsers(t *testing.T) {
	// covers: AC-19
	hub := NewHub(nil)
	alice := uuid.Must(uuid.NewV7())
	bob := uuid.Must(uuid.NewV7())
	aliceCh, _, unsubAlice := hub.RegisterPaused(alice, uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()))
	bobCh, _, unsubBob := hub.RegisterPaused(bob, uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()))
	defer unsubAlice()
	defer unsubBob()

	hub.dispatchUsers([]uuid.UUID{alice}, Frame{Event: "invalidate", Data: map[string]string{"type": "bill.created"}})
	select {
	case frame := <-aliceCh:
		if frame.Event != "invalidate" {
			t.Fatalf("alice event = %s", frame.Event)
		}
	case <-time.After(time.Second):
		t.Fatal("alice did not receive invalidate")
	}
	select {
	case <-bobCh:
		t.Fatal("bob received alice audience event")
	default:
	}
}

func TestHubSessionEndedClosesOnlyTargetSID(t *testing.T) {
	// covers: AC-14
	hub := NewHub(nil)
	userID := uuid.Must(uuid.NewV7())
	ended := uuid.Must(uuid.NewV7())
	kept := uuid.Must(uuid.NewV7())
	endedCh, _, _ := hub.RegisterPaused(userID, ended, uuid.Must(uuid.NewV7()))
	keptCh, _, unsub := hub.RegisterPaused(userID, kept, uuid.Must(uuid.NewV7()))
	defer unsub()

	hub.ApplySessionEnded([]uuid.UUID{ended})

	select {
	case frame := <-endedCh:
		if frame.Event != "close" {
			t.Fatalf("ended stream event = %s", frame.Event)
		}
	case <-time.After(time.Second):
		t.Fatal("target sid was not closed")
	}
	select {
	case <-keptCh:
		t.Fatal("other sid was closed")
	default:
	}
}

func TestHubRosterOmitsAudience(t *testing.T) {
	hub := NewHub(func(raw json.RawMessage) any {
		return map[string]any{"ok": true}
	})
	userID := uuid.Must(uuid.NewV7())
	sid := uuid.Must(uuid.NewV7())
	streamID := uuid.Must(uuid.NewV7())
	ch, _, unsub := hub.RegisterPaused(userID, sid, streamID)
	defer unsub()

	groupID := uuid.Must(uuid.NewV7())
	payload, _, err := realtime.EncodeGroupEnvelope(realtime.GroupEnvelope{
		GroupID:         groupID,
		Version:         3,
		Type:            "member_joined",
		Data:            json.RawMessage(`{"member":{}}`),
		AudienceUserIDs: []uuid.UUID{userID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = hub.HandleGroupNotification(nil, string(payload)); err != nil {
		t.Fatal(err)
	}
	select {
	case frame := <-ch:
		if frame.Event != "roster" {
			t.Fatalf("event = %s", frame.Event)
		}
		body, _ := json.Marshal(frame.Data)
		if string(body) != "" && containsAudience(body) {
			t.Fatalf("public roster leaked audience: %s", body)
		}
	case <-time.After(time.Second):
		t.Fatal("missing roster frame")
	}
}

func containsAudience(body []byte) bool {
	return json.Valid(body) && (string(body) != "" && (contains(body, "audience_user_ids") || contains(body, "avatar_object_key")))
}

func contains(body []byte, token string) bool {
	return len(body) > 0 && json.Valid(body) && (string(body) != "" && containsBytes(body, []byte(token)))
}

func containsBytes(haystack, needle []byte) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && (string(haystack) != "" && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if string(haystack[i:i+len(needle)]) == string(needle) {
				return true
			}
		}
		return false
	}())))
}
