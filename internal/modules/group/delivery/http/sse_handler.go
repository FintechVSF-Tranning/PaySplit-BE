package http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"paysplit-backend/internal/modules/group/domain"
	"paysplit-backend/internal/modules/group/usecase"
	platformmetrics "paysplit-backend/internal/platform/metrics"
	"paysplit-backend/internal/platform/realtime"
	"paysplit-backend/internal/transport/http/helpers"
	authmw "paysplit-backend/internal/transport/http/middleware"
)

// SSEHandler phục vụ stream sự kiện nhóm: GET /api/v1/groups/{id}/events.
//
// Stream chỉ là lớp tăng tốc. Nguồn sự thật vẫn là nhật ký group_events, nên
// mọi thứ ở đây đều được thiết kế để mất kết nối là chuyện bình thường: client
// nào cũng mang theo since và tự hàn gắp qua /sync khi phát hiện lỗ hổng.
type SSEHandler struct {
	handler           *Handler
	hub               Broadcaster
	heartbeatInterval time.Duration
	maxConnectionAge  time.Duration
	minAppVersion     string
}

func (h *SSEHandler) SetMinAppVersion(version string) {
	h.minAppVersion = version
}

func NewSSEHandler(handler *Handler, hub Broadcaster, heartbeatInterval, maxConnectionAge time.Duration) *SSEHandler {
	if handler == nil {
		panic("group sse handler requires a group handler")
	}
	if hub == nil {
		panic("group sse handler requires a hub")
	}
	if heartbeatInterval <= 0 {
		heartbeatInterval = 15 * time.Second
	}
	if maxConnectionAge <= 0 {
		maxConnectionAge = 15 * time.Minute
	}
	return &SSEHandler{
		handler:           handler,
		hub:               hub,
		heartbeatInterval: heartbeatInterval,
		maxConnectionAge:  maxConnectionAge,
	}
}

// StreamGroupEvents mở stream sự kiện cho một nhóm.
func (h *SSEHandler) StreamGroupEvents(w http.ResponseWriter, r *http.Request) {
	platformmetrics.RecordLegacySSERequest("group", realtime.ParseAppVersion(r.Header.Get("X-App-Version")).Class(realtime.ParseAppVersion(h.minAppVersion)))
	flusher, ok := w.(http.Flusher)
	if !ok {
		_ = helpers.WriteAPIError(w, http.StatusInternalServerError, "STREAMING_UNSUPPORTED", "streaming is not supported by server", nil)
		return
	}

	groupIDStr := chi.URLParam(r, "id")
	groupID, err := uuid.Parse(groupIDStr)
	if err != nil {
		_ = helpers.WriteAPIError(w, http.StatusNotFound, "GROUP_NOT_FOUND", "group not found", nil)
		return
	}
	userID, ok := authmw.UserID(r.Context())
	if !ok {
		_ = helpers.WriteAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
		return
	}
	since, ok := parseSince(w, r)
	if !ok {
		return
	}

	// Đăng ký TRƯỚC khi đọc trạng thái ban đầu. Làm ngược lại sẽ để hở một khe:
	// một mutation commit đúng giữa lúc đọc và lúc đăng ký sẽ không xuất hiện ở
	// cả hai đường, và client mất sự kiện đó vĩnh viễn.
	eventCh, unsubscribe := h.hub.Subscribe(groupID)
	defer unsubscribe()

	// Xác thực thành viên và lấy trạng thái ban đầu trong cùng một lời gọi.
	// Lỗi ở bước này vẫn còn trả được JSON vì chưa ghi header SSE nào.
	page, err := h.handler.service.Sync(r.Context(), usecase.SyncInput{
		GroupID:      groupIDStr,
		CallerUserID: userID,
		Since:        since,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// lastSent chặn phát trùng: backlog vừa gửi và stream có thể chồng lấn nhau
	// vì cả hai cùng chạy từ lúc Subscribe.
	lastSent := page.Version
	if err = h.writeInitial(w, flusher, page); err != nil {
		return
	}

	heartbeat := time.NewTicker(h.heartbeatInterval)
	defer heartbeat.Stop()
	maxAge := time.NewTimer(h.maxConnectionAge)
	defer maxAge.Stop()

	for {
		select {
		case <-r.Context().Done():
			return

		case <-maxAge.C:
			// Đóng sạch để client mở lại với since mới. Việc này cũng giới hạn
			// thời gian một phiên đã bị thu hồi còn giữ được stream.
			_ = writeSSEEvent(w, flusher, "close", 0, map[string]string{"reason": "max_connection_age"})
			return

		case <-heartbeat.C:
			if err := writeSSEEvent(w, flusher, "heartbeat", 0, map[string]int64{"timestamp": time.Now().Unix()}); err != nil {
				return
			}

		case event, ok := <-eventCh:
			if !ok {
				return
			}
			if event.Version <= lastSent {
				continue
			}
			lastSent = event.Version
			if err := h.writeLogEvent(w, flusher, event.Version, event.Type, event.Data); err != nil {
				return
			}
			// Caller vừa mất quyền đọc nhóm: đóng stream ngay thay vì để nó
			// treo và tiếp tục nhận sự kiện của một nhóm không còn thuộc về họ.
			if reason, closed := closeReason(event, userID); closed {
				_ = writeSSEEvent(w, flusher, "close", 0, map[string]string{"reason": reason})
				return
			}
		}
	}
}

// writeInitial gửi trạng thái xuất phát: snapshot khi client tụt quá xa hoặc
// chưa có version, ngược lại là các sự kiện còn thiếu. Client đã bắt kịp nhận
// một sự kiện sync rỗng để biết chắc nó đang ở đúng version.
func (h *SSEHandler) writeInitial(w http.ResponseWriter, flusher http.Flusher, page *domain.SyncPage) error {
	if page.Mode == domain.SyncModeSnapshot {
		body := map[string]any{"version": page.Version}
		if page.Snapshot != nil {
			body = h.handler.newGroupDetailResponse(*page.Snapshot)
		}
		return writeSSEEvent(w, flusher, "snapshot", page.Version, body)
	}

	if len(page.Events) == 0 {
		return writeSSEEvent(w, flusher, "sync", page.Version, map[string]any{"version": page.Version})
	}
	for _, event := range page.Events {
		if err := h.writeLogEvent(w, flusher, event.Version, event.Type, event.Payload); err != nil {
			return err
		}
	}
	return nil
}

// writeLogEvent ghi một sự kiện nhật ký. Thân frame mang đúng shape với phần tử
// events của GET /sync — {version, type, data} — để client dùng lại y nguyên
// một hàm giải mã cho cả kênh nóng lẫn kênh nguội, thay vì phải đọc version từ
// trường id: mà proxy có quyền bỏ qua.
func (h *SSEHandler) writeLogEvent(w http.ResponseWriter, flusher http.Flusher, version int64, eventType string, payload json.RawMessage) error {
	return writeSSEEvent(w, flusher, eventType, version, map[string]any{
		"version": version,
		"type":    eventType,
		"data":    h.handler.renderEventPayload(payload),
	})
}

// closeReason cho biết sự kiện vừa phát có chấm dứt quyền đọc nhóm của caller
// hay không.
func closeReason(event Event, callerUserID string) (string, bool) {
	switch event.Type {
	case domain.EventGroupArchived:
		return "group_archived", true
	case domain.EventMemberLeft, domain.EventMemberRemoved:
		var payload struct {
			UserID string `json:"user_id"`
		}
		if err := json.Unmarshal(event.Data, &payload); err == nil && payload.UserID == callerUserID {
			return "membership_ended", true
		}
	}
	return "", false
}

// writeSSEEvent ghi một sự kiện. version > 0 được ghi vào trường id để client
// nào dùng EventSource chuẩn có thể tự gửi lại Last-Event-ID khi reconnect.
func writeSSEEvent(w http.ResponseWriter, flusher http.Flusher, eventType string, version int64, data any) error {
	body, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if version > 0 {
		if _, err = fmt.Fprintf(w, "id: %d\n", version); err != nil {
			return err
		}
	}
	if _, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, body); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}
