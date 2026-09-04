package http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"

	platformmetrics "paysplit-backend/internal/platform/metrics"
	"paysplit-backend/internal/platform/realtime"
	"paysplit-backend/internal/transport/http/helpers"
	authmw "paysplit-backend/internal/transport/http/middleware"
)

type ListenerHealth interface {
	Connected() bool
}

type SSEHandler struct {
	hub               *Hub
	publisher         *realtime.Publisher
	exec              realtime.Executor
	listener          ListenerHealth
	heartbeatInterval time.Duration
	maxConnectionAge  time.Duration
	admissionTimeout  time.Duration
	limiter           *sidLimiter
}

// defaultAdmissionTimeout giới hạn thời gian chờ control `stream.replace` của
// chính kết nối này quay lại qua listener. Vượt quá nghĩa là đường NOTIFY không
// còn tin cậy được, nên từ chối trước khi ghi header còn hơn mở một stream
// không nằm trong thứ tự thay thế.
const defaultAdmissionTimeout = 5 * time.Second

func NewSSEHandler(
	hub *Hub,
	publisher *realtime.Publisher,
	exec realtime.Executor,
	listener ListenerHealth,
	heartbeatInterval, maxConnectionAge time.Duration,
) *SSEHandler {
	if hub == nil {
		panic("user sse handler requires a hub")
	}
	if publisher == nil {
		panic("user sse handler requires a publisher")
	}
	if exec == nil {
		panic("user sse handler requires a notify executor")
	}
	if listener == nil {
		panic("user sse handler requires listener health")
	}
	if heartbeatInterval <= 0 {
		heartbeatInterval = 15 * time.Second
	}
	if maxConnectionAge <= 0 {
		maxConnectionAge = 15 * time.Minute
	}
	return &SSEHandler{
		hub:               hub,
		publisher:         publisher,
		exec:              exec,
		listener:          listener,
		heartbeatInterval: heartbeatInterval,
		maxConnectionAge:  maxConnectionAge,
		admissionTimeout:  defaultAdmissionTimeout,
		limiter:           newSIDLimiter(10, time.Minute),
	}
}

func (h *SSEHandler) StreamUserEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		platformmetrics.RecordUserSSEConnection("streaming_unsupported")
		_ = helpers.WriteAPIError(w, http.StatusInternalServerError, "STREAMING_UNSUPPORTED", "streaming is not supported by server", nil)
		return
	}

	userIDRaw, ok := authmw.UserID(r.Context())
	sessionIDRaw, sidOK := authmw.SessionID(r.Context())
	if !ok || !sidOK {
		platformmetrics.RecordUserSSEConnection("auth_failed")
		_ = helpers.WriteAPIError(w, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "authentication required", nil)
		return
	}
	userID, err := uuid.Parse(userIDRaw)
	sid, sidErr := uuid.Parse(sessionIDRaw)
	if err != nil || sidErr != nil || userID == uuid.Nil || sid == uuid.Nil {
		platformmetrics.RecordUserSSEConnection("auth_failed")
		_ = helpers.WriteAPIError(w, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "authentication required", nil)
		return
	}

	if !h.listener.Connected() {
		platformmetrics.RecordUserSSEConnection("listener_unavailable")
		_ = helpers.WriteAPIError(w, http.StatusServiceUnavailable, "REALTIME_UNAVAILABLE", "realtime listener is unavailable", nil)
		return
	}

	if retryAfter, limited := h.limiter.allow(sid); limited {
		platformmetrics.RecordUserSSEConnection("rate_limited")
		w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
		_ = helpers.WriteAPIError(w, http.StatusTooManyRequests, "RATE_LIMITED", "rate limit exceeded", nil)
		return
	}

	streamID := uuid.Must(uuid.NewV7())
	frames, admitted, unsubscribe := h.hub.RegisterPaused(userID, sid, streamID)
	if err = h.publisher.NotifyStreamReplace(r.Context(), h.exec, sid, streamID); err != nil {
		unsubscribe()
		platformmetrics.RecordUserSSEConnection("replace_publish_failed")
		_ = helpers.WriteAPIError(w, http.StatusServiceUnavailable, "REALTIME_UNAVAILABLE", "realtime stream could not be replaced", nil)
		return
	}

	// Không tự áp control tại chỗ: chờ nó quay lại qua listener. Đó là đường duy
	// nhất giữ đúng thứ tự commit của PostgreSQL, nên hai kết nối đồng thời của
	// cùng một phiên luôn chọn ra đúng một người thắng thay vì đóng lẫn nhau.
	admissionTimer := time.NewTimer(h.admissionTimeout)
	defer admissionTimer.Stop()
	select {
	case <-admitted:
	case <-r.Context().Done():
		unsubscribe()
		platformmetrics.RecordUserSSEConnection("client_gone")
		return
	case <-admissionTimer.C:
		unsubscribe()
		platformmetrics.RecordUserSSEConnection("replace_admission_timeout")
		_ = helpers.WriteAPIError(w, http.StatusServiceUnavailable, "REALTIME_UNAVAILABLE", "realtime stream could not be admitted", nil)
		return
	}
	if !h.hub.Activated(streamID) {
		// Bị một control mới hơn thay thế, hoặc phiên đã kết thúc, trong lúc còn
		// đang chờ. Kết nối này thua và chưa ghi header nào.
		unsubscribe()
		platformmetrics.RecordUserSSEConnection("replaced_before_ready")
		_ = helpers.WriteAPIError(w, http.StatusServiceUnavailable, "REALTIME_UNAVAILABLE", "realtime stream was replaced", nil)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	if err = writeUserSSEEvent(w, flusher, "ready", map[string]any{
		"stream_id": streamID,
		"timestamp": rfc3339UTC(time.Now()),
	}); err != nil {
		unsubscribe()
		return
	}
	platformmetrics.RecordUserSSEConnection("opened")
	platformmetrics.RecordUserEvent(realtime.ChannelUserEvents, "ready")
	platformmetrics.SetUserSSEActiveConnections(1)
	defer platformmetrics.SetUserSSEActiveConnections(-1)
	defer unsubscribe()

	heartbeat := time.NewTicker(h.heartbeatInterval)
	defer heartbeat.Stop()
	maxAge := time.NewTimer(h.maxConnectionAge)
	defer maxAge.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-maxAge.C:
			_ = writeUserSSEEvent(w, flusher, "close", map[string]string{"reason": "max_connection_age"})
			platformmetrics.RecordUserSSEClose("max_connection_age")
			platformmetrics.RecordUserEvent(realtime.ChannelUserEvents, "close")
			return
		case <-heartbeat.C:
			if err := writeUserSSEEvent(w, flusher, "heartbeat", map[string]string{"timestamp": rfc3339UTC(time.Now())}); err != nil {
				return
			}
			platformmetrics.RecordUserEvent(realtime.ChannelUserEvents, "heartbeat")
		case frame, ok := <-frames:
			if !ok {
				return
			}
			if err := writeUserSSEEvent(w, flusher, frame.Event, frame.Data); err != nil {
				return
			}
			if frame.Event == "close" {
				platformmetrics.RecordUserEvent(realtime.ChannelUserEvents, "close")
				return
			}
		}
	}
}

func writeUserSSEEvent(w http.ResponseWriter, flusher http.Flusher, eventType string, data any) error {
	body, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if _, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, body); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

type sidLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	entries map[uuid.UUID]rateWindow
}

type rateWindow struct {
	count       int
	windowStart time.Time
}

func newSIDLimiter(limit int, window time.Duration) *sidLimiter {
	return &sidLimiter{limit: limit, window: window, entries: make(map[uuid.UUID]rateWindow)}
}

func (l *sidLimiter) allow(sid uuid.UUID) (int, bool) {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	entry := l.entries[sid]
	if entry.windowStart.IsZero() || now.Sub(entry.windowStart) >= l.window {
		entry = rateWindow{windowStart: now}
	}
	entry.count++
	l.entries[sid] = entry
	if entry.count <= l.limit {
		return 0, false
	}
	retryAfter := int((l.window - now.Sub(entry.windowStart) + time.Second - 1) / time.Second)
	if retryAfter < 1 {
		retryAfter = 1
	}
	return retryAfter, true
}
