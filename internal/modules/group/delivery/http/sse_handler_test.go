package http

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"paysplit-backend/internal/modules/group/domain"
)

func TestWriteSSEEvent_EmitsIDOnlyForVersionedEvents(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := writeSSEEvent(rec, rec, "member_joined", 42, map[string]any{"a": 1}); err != nil {
		t.Fatal(err)
	}
	got := rec.Body.String()
	want := "id: 42\nevent: member_joined\ndata: {\"a\":1}\n\n"
	if got != want {
		t.Fatalf("frame = %q, want %q", got, want)
	}

	// Heartbeat và close không thuộc dãy version, nên không được ghi id: —
	// client dùng EventSource sẽ hiểu nhầm đó là điểm tiếp tục khi reconnect.
	rec = httptest.NewRecorder()
	if err := writeSSEEvent(rec, rec, "heartbeat", 0, map[string]int64{"timestamp": 1}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rec.Body.String(), "id:") {
		t.Fatalf("heartbeat frame must not carry an id: %q", rec.Body.String())
	}
}

func TestCloseReason(t *testing.T) {
	const caller = "11111111-1111-1111-1111-111111111111"
	const other = "22222222-2222-2222-2222-222222222222"

	tests := []struct {
		name       string
		event      Event
		wantReason string
		wantClosed bool
	}{
		{
			name:       "nhóm bị giải tán thì mọi stream đều đóng",
			event:      Event{Type: domain.EventGroupArchived},
			wantReason: "group_archived",
			wantClosed: true,
		},
		{
			name:       "chính caller bị xóa khỏi nhóm",
			event:      Event{Type: domain.EventMemberRemoved, Data: json.RawMessage(`{"user_id":"` + caller + `"}`)},
			wantReason: "membership_ended",
			wantClosed: true,
		},
		{
			name:       "chính caller rời nhóm từ một thiết bị khác",
			event:      Event{Type: domain.EventMemberLeft, Data: json.RawMessage(`{"user_id":"` + caller + `"}`)},
			wantReason: "membership_ended",
			wantClosed: true,
		},
		{
			name:  "người khác rời nhóm thì stream vẫn chạy",
			event: Event{Type: domain.EventMemberLeft, Data: json.RawMessage(`{"user_id":"` + other + `"}`)},
		},
		{
			name:  "envelope gầy không kèm data thì không được đoán là đã bị xóa",
			event: Event{Type: domain.EventMemberRemoved},
		},
		{
			name:  "thành viên mới vào không đóng stream của ai",
			event: Event{Type: domain.EventMemberJoined, Data: json.RawMessage(`{"member":{"user_id":"` + caller + `"}}`)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason, closed := closeReason(tt.event, caller)
			if closed != tt.wantClosed || reason != tt.wantReason {
				t.Fatalf("closeReason() = (%q, %v), want (%q, %v)", reason, closed, tt.wantReason, tt.wantClosed)
			}
		})
	}
}

// Payload rời server phải mang avatar_url chứ không phải object key, đúng một
// quy tắc với newMemberResponse của REST.
func TestRenderEventPayload_MapsAvatarObjectKeyToURL(t *testing.T) {
	// Chỉ cần avatarURL: renderEventPayload không chạm tới service.
	h := &Handler{avatarURL: func(key string) string { return "https://cdn.test/" + key }}

	got := h.renderEventPayload(json.RawMessage(`{"member":{"display_name":"Nam","avatar_object_key":"avatars/nam.webp"},"active_member_count":3}`))
	member := got.(map[string]any)["member"].(map[string]any)
	if _, leaked := member["avatar_object_key"]; leaked {
		t.Fatal("avatar_object_key must not reach the client")
	}
	if member["avatar_url"] != "https://cdn.test/avatars/nam.webp" {
		t.Fatalf("avatar_url = %v", member["avatar_url"])
	}

	// Thành viên chưa có ảnh: field vẫn phải xuất hiện với giá trị null để
	// client không phải phân biệt "thiếu key" với "không có ảnh".
	got = h.renderEventPayload(json.RawMessage(`{"member":{"avatar_object_key":null}}`))
	member = got.(map[string]any)["member"].(map[string]any)
	value, present := member["avatar_url"]
	if !present || value != nil {
		t.Fatalf("avatar_url = %v (present=%v), want an explicit null", value, present)
	}

	// Payload hỏng vẫn phải phát được: client chỉ cần version để tự catch-up.
	if got = h.renderEventPayload(json.RawMessage(`{oops`)); len(got.(map[string]any)) != 0 {
		t.Fatalf("malformed payload should render as an empty object, got %v", got)
	}
}
