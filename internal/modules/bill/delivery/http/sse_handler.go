package http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"paysplit-backend/internal/modules/bill/repository"
	"paysplit-backend/internal/transport/http/helpers"
)

// SSEHandler xử lý kết nối Server-Sent Events (SSE) cho hóa đơn (Spec 3 AC-2, AC-8).
type SSEHandler struct {
	hub               Broadcaster
	repo              repository.Repository
	heartbeatInterval time.Duration
	maxConnectionAge  time.Duration
}

// NewSSEHandler khởi tạo SSEHandler.
func NewSSEHandler(
	hub Broadcaster,
	repo repository.Repository,
	heartbeatInterval time.Duration,
	maxConnectionAge time.Duration,
) *SSEHandler {
	if heartbeatInterval <= 0 {
		heartbeatInterval = 15 * time.Second
	}
	if maxConnectionAge <= 0 {
		maxConnectionAge = 15 * time.Minute
	}
	return &SSEHandler{
		hub:               hub,
		repo:              repo,
		heartbeatInterval: heartbeatInterval,
		maxConnectionAge:  maxConnectionAge,
	}
}

// StreamBillEvents xử lý route GET /api/v1/bills/{id}/events.
func (h *SSEHandler) StreamBillEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		_ = helpers.WriteAPIError(w, http.StatusInternalServerError, "STREAMING_UNSUPPORTED", "streaming is not supported by server", nil)
		return
	}

	billIDStr := chi.URLParam(r, "id")
	billID, err := uuid.Parse(billIDStr)
	if err != nil {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "INVALID_BILL_ID", "bill ID is invalid", nil)
		return
	}

	// 1. Thiết lập SSE Response Headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// 2. Đăng ký nhận sự kiện từ Hub
	eventCh, unsubscribe := h.hub.Subscribe(billID)
	defer unsubscribe()

	// 3. Gửi Snapshot ban đầu (Current Snapshot on Connect)
	latestJob, _ := h.repo.GetLatestOCRJobByBillID(r.Context(), billID)
	snapshotData := map[string]any{
		"bill_id": billID,
	}
	if latestJob != nil {
		snapshotData["ocr_job"] = map[string]any{
			"id":        latestJob.ID,
			"status":    latestJob.Status,
			"candidate": latestJob.Candidate,
			"error":     latestJob.ErrorMessage,
		}
	}
	_ = writeSSEEvent(w, flusher, "snapshot", snapshotData)

	// 4. Khởi tạo Heartbeat Ticker và Max Connection Age Timer
	heartbeatTicker := time.NewTicker(h.heartbeatInterval)
	defer heartbeatTicker.Stop()

	maxAgeTimer := time.NewTimer(h.maxConnectionAge)
	defer maxAgeTimer.Stop()

	// 5. Event Loop lắng nghe sự kiện
	for {
		select {
		case <-r.Context().Done():
			// Client đã ngắt kết nối
			return

		case <-maxAgeTimer.C:
			// Đạt thời gian sống tối đa của kết nối (15m) -> đóng sạch để client reconnect
			_ = writeSSEEvent(w, flusher, "close", map[string]string{
				"reason": "max_connection_age",
			})
			return

		case <-heartbeatTicker.C:
			// Ping giữ kết nối
			_ = writeSSEEvent(w, flusher, "ping", map[string]int64{
				"timestamp": time.Now().Unix(),
			})

		case event, ok := <-eventCh:
			if !ok {
				return
			}
			_ = writeSSEEvent(w, flusher, event.Type, event.Data)
		}
	}
}

func writeSSEEvent(w http.ResponseWriter, flusher http.Flusher, eventType string, data any) error {
	dataBytes, err := json.Marshal(data)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, string(dataBytes))
	if err != nil {
		return err
	}

	flusher.Flush()
	return nil
}
