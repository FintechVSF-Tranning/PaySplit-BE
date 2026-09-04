package http

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/google/uuid"

	platformmetrics "paysplit-backend/internal/platform/metrics"
	"paysplit-backend/internal/platform/realtime"
)

const subscriberBuffer = 64

type Frame struct {
	Event string
	Data  any
}

type stream struct {
	id     uuid.UUID
	userID uuid.UUID
	sid    uuid.UUID
	ch     chan Frame

	// active phân biệt stream đã được control `stream.replace` của chính nó kích
	// hoạt với ứng viên còn đang tạm dừng. Một control cũ không được phép đóng
	// ứng viên đăng ký sau nó, nếu không hai kết nối đồng thời của cùng một phiên
	// có thể đóng lẫn nhau và không còn stream nào sống sót.
	active bool

	// admit đóng lại đúng một lần, khi stream được kích hoạt hoặc bị đóng. Handler
	// chờ trên kênh này nên thứ tự commit của PostgreSQL mới là thứ tự quyết định
	// ai thắng, chứ không phải thứ tự gọi hàm trong tiến trình.
	admit     chan struct{}
	admitOnce sync.Once
}

func (s *stream) signalAdmit() {
	s.admitOnce.Do(func() { close(s.admit) })
}

type Hub struct {
	mu           sync.Mutex
	byID         map[uuid.UUID]*stream
	bySID        map[uuid.UUID]map[uuid.UUID]*stream
	byUser       map[uuid.UUID]map[uuid.UUID]*stream
	renderRoster func(json.RawMessage) any
}

func NewHub(renderRoster func(json.RawMessage) any) *Hub {
	if renderRoster == nil {
		renderRoster = func(raw json.RawMessage) any {
			if len(raw) == 0 {
				return map[string]any{}
			}
			var payload any
			if err := json.Unmarshal(raw, &payload); err != nil {
				return map[string]any{}
			}
			return payload
		}
	}
	return &Hub{
		byID:         make(map[uuid.UUID]*stream),
		bySID:        make(map[uuid.UUID]map[uuid.UUID]*stream),
		byUser:       make(map[uuid.UUID]map[uuid.UUID]*stream),
		renderRoster: renderRoster,
	}
}

// RegisterPaused đăng ký một stream ở trạng thái tạm dừng. Nó đã nhận frame vào
// buffer nhưng chưa được thừa nhận: kênh thứ hai trả về đóng lại khi control
// `stream.replace` mang đúng streamID này được áp (kích hoạt) hoặc khi stream bị
// đóng trước đó.
func (h *Hub) RegisterPaused(userID, sid, streamID uuid.UUID) (<-chan Frame, <-chan struct{}, func()) {
	s := &stream{
		id:     streamID,
		userID: userID,
		sid:    sid,
		ch:     make(chan Frame, subscriberBuffer),
		admit:  make(chan struct{}),
	}
	h.mu.Lock()
	h.byID[streamID] = s
	if h.bySID[sid] == nil {
		h.bySID[sid] = make(map[uuid.UUID]*stream)
	}
	h.bySID[sid][streamID] = s
	if h.byUser[userID] == nil {
		h.byUser[userID] = make(map[uuid.UUID]*stream)
	}
	h.byUser[userID][streamID] = s
	h.mu.Unlock()

	return s.ch, s.admit, func() { h.remove(streamID, "") }
}

// Activated cho biết stream còn sống và đã được thừa nhận. Handler gọi nó sau
// khi chờ admit để phân biệt "được kích hoạt" với "bị đóng khi còn tạm dừng".
func (h *Hub) Activated(streamID uuid.UUID) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.byID[streamID]
	return ok && s.active
}

func (h *Hub) Remove(streamID uuid.UUID) {
	h.remove(streamID, "")
}

// ApplyReplace áp một control `stream.replace` đã commit. Chỉ đường dẫn này —
// tức là listener PostgreSQL — được gọi nó, nên các control luôn được áp theo
// đúng thứ tự commit, kể cả khi phát sinh từ nhiều tiến trình.
//
// Nó chỉ đóng những stream đang hoạt động: một ứng viên đăng ký sau nhưng chưa
// được control của chính nó kích hoạt phải sống sót, vì control đó sẽ tới sau và
// mới là người quyết định.
func (h *Hub) ApplyReplace(targetSIDs []uuid.UUID, replacement uuid.UUID) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, sid := range targetSIDs {
		for streamID, s := range h.bySID[sid] {
			if streamID == replacement || !s.active {
				continue
			}
			h.closeLocked(s, "replaced")
		}
		if s, ok := h.bySID[sid][replacement]; ok {
			h.activateLocked(s)
		}
	}
}

func (h *Hub) activateLocked(s *stream) {
	if _, ok := h.byID[s.id]; !ok {
		return
	}
	s.active = true
	s.signalAdmit()
}

func (h *Hub) ApplySessionEnded(sids []uuid.UUID) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, sid := range sids {
		for _, s := range h.bySID[sid] {
			h.closeLocked(s, "session_ended")
		}
	}
}

func (h *Hub) CloseSubscribers() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, s := range h.byID {
		h.closeLocked(s, "listener_reset")
	}
}

func (h *Hub) HandleGroupNotification(_ context.Context, payload string) error {
	env, err := realtime.DecodeGroupEnvelope(payload)
	if err != nil {
		platformmetrics.RecordUserEventInvalidPayload(realtime.ChannelGroupEvents, err.Error())
		return err
	}
	frame := Frame{
		Event: "roster",
		Data: map[string]any{
			"group_id": env.GroupID,
			"version":  env.Version,
			"type":     env.Type,
			"data":     h.renderRoster(env.Data),
		},
	}
	h.dispatchUsers(env.AudienceUserIDs, frame)
	platformmetrics.RecordUserEvent(realtime.ChannelGroupEvents, "roster")
	return nil
}

func (h *Hub) HandleBillNotification(_ context.Context, payload string) error {
	env, err := realtime.DecodeBillEnvelope(payload)
	if err != nil {
		platformmetrics.RecordUserEventInvalidPayload(realtime.ChannelBillEvents, err.Error())
		return err
	}
	var data map[string]any
	if len(env.Data) > 0 {
		_ = json.Unmarshal(env.Data, &data)
	}
	if data == nil {
		data = map[string]any{}
	}
	data["group_id"] = env.GroupID
	data["bill_id"] = env.BillID
	h.dispatchUsers(env.AudienceUserIDs, Frame{Event: "ocr.updated", Data: data})
	platformmetrics.RecordUserEvent(realtime.ChannelBillEvents, "ocr_updated")
	return nil
}

func (h *Hub) HandleUserNotification(_ context.Context, payload string) error {
	env, err := realtime.DecodeUserEnvelope(payload)
	if err != nil {
		platformmetrics.RecordUserEventInvalidPayload(realtime.ChannelUserEvents, err.Error())
		return err
	}
	switch env.Kind {
	case realtime.KindInvalidate:
		h.dispatchUsers(env.AudienceUserIDs, Frame{Event: "invalidate", Data: env.Body})
		platformmetrics.RecordUserEvent(realtime.ChannelUserEvents, "invalidate")
	case realtime.KindStreamReplace:
		h.ApplyReplace(env.TargetSIDs, *env.ReplacementStreamID)
		platformmetrics.RecordUserEvent(realtime.ChannelUserEvents, "stream_replace")
	case realtime.KindSessionEnded:
		h.ApplySessionEnded(env.TargetSIDs)
		platformmetrics.RecordUserEvent(realtime.ChannelUserEvents, "session_ended")
	}
	return nil
}

func (h *Hub) dispatchUsers(userIDs []uuid.UUID, frame Frame) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, userID := range userIDs {
		for _, s := range h.byUser[userID] {
			h.sendLocked(s, frame)
		}
	}
}

func (h *Hub) sendLocked(s *stream, frame Frame) {
	select {
	case s.ch <- frame:
	default:
		h.closeLocked(s, "backpressure")
	}
}

func (h *Hub) closeLocked(s *stream, reason string) {
	if _, ok := h.byID[s.id]; !ok {
		return
	}
	select {
	case s.ch <- Frame{Event: "close", Data: map[string]string{"reason": reason}}:
	default:
	}
	close(s.ch)
	s.signalAdmit()
	h.detachLocked(s)
	platformmetrics.RecordUserSSEClose(reason)
}

func (h *Hub) remove(streamID uuid.UUID, reason string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.byID[streamID]
	if !ok {
		return
	}
	if reason != "" {
		h.closeLocked(s, reason)
		return
	}
	close(s.ch)
	s.signalAdmit()
	h.detachLocked(s)
}

func (h *Hub) detachLocked(s *stream) {
	delete(h.byID, s.id)
	if subs := h.bySID[s.sid]; subs != nil {
		delete(subs, s.id)
		if len(subs) == 0 {
			delete(h.bySID, s.sid)
		}
	}
	if subs := h.byUser[s.userID]; subs != nil {
		delete(subs, s.id)
		if len(subs) == 0 {
			delete(h.byUser, s.userID)
		}
	}
}

func rfc3339UTC(now time.Time) string {
	return now.UTC().Format(time.RFC3339)
}
