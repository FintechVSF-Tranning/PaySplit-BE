package http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"paysplit-backend/internal/modules/bill/repository"
	platformmetrics "paysplit-backend/internal/platform/metrics"
	"paysplit-backend/internal/platform/realtime"
	"paysplit-backend/internal/transport/http/helpers"
	authmw "paysplit-backend/internal/transport/http/middleware"
)

// SSEHandler xử lý kết nối Server-Sent Events (SSE) cho hóa đơn (Spec 3 AC-2, AC-8).
type SSEHandler struct {
	hub               Broadcaster
	repo              repository.Repository
	heartbeatInterval time.Duration
	maxConnectionAge  time.Duration
	minAppVersion     string
}

func (h *SSEHandler) SetMinAppVersion(version string) {
	h.minAppVersion = version
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
	platformmetrics.RecordLegacySSERequest("bill", realtime.ParseAppVersion(r.Header.Get("X-App-Version")).Class(realtime.ParseAppVersion(h.minAppVersion)))
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

	// Kiểm tra authentication và quyền thành viên nhóm bắt buộc (Spec 3 AC-8)
	userIDStr, ok := authmw.UserID(r.Context())
	if !ok {
		_ = helpers.WriteAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
		return
	}
	callerUserID, err := uuid.Parse(userIDStr)
	if err != nil {
		_ = helpers.WriteAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid user ID", nil)
		return
	}

	var groupID uuid.UUID
	if groupIDStr := r.URL.Query().Get("group_id"); groupIDStr != "" {
		if gID, err := uuid.Parse(groupIDStr); err == nil {
			groupID = gID
		}
	}

	// Nếu group_id không truyền trong query, tra cứu từ bills table
	if groupID == uuid.Nil {
		bill, err := h.repo.GetBillOnlyByID(r.Context(), billID)
		if err != nil {
			_ = helpers.WriteAPIError(w, http.StatusNotFound, "BILL_NOT_FOUND", "bill not found", nil)
			return
		}
		groupID = bill.GroupID
	}

	// Kiểm tra caller là active member của nhóm (Spec 3 AC-8)
	member, err := h.repo.GetGroupMember(r.Context(), groupID, callerUserID)
	if err != nil || member.Status != "active" {
		_ = helpers.WriteAPIError(w, http.StatusForbidden, "FORBIDDEN", "caller is not an active member of this group", nil)
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

	// 3. Gửi Snapshot ban đầu (Current Snapshot on Connect - không lộ raw OCR hay item text, Spec 3 0001:70)
	bill, _ := h.repo.GetBillByID(r.Context(), billID, groupID)
	snapshotData := map[string]any{
		"bill_id": billID,
	}
	if bill != nil {
		snapshotData["version"] = bill.Version
		snapshotData["status"] = bill.Status
		snapshotData["reviewed_at"] = bill.ReviewedAt
		snapshotData["reviewed_by_member_id"] = bill.ReviewedByMemberID
		snapshotData["subtotal"] = bill.Subtotal
		snapshotData["total"] = bill.Total
		snapshotData["mismatch_codes"] = bill.MismatchCodes
	}

	latestJob, _ := h.repo.GetLatestOCRJobByBillID(r.Context(), billID)
	if latestJob != nil {
		ocrJobSummary := map[string]any{
			"id":           latestJob.ID,
			"status":       latestJob.Status,
			"attempts":     latestJob.Attempts,
			"error":        latestJob.ErrorMessage,
			"version":      latestJob.Version,
			"created_at":   latestJob.CreatedAt,
			"completed_at": latestJob.CompletedAt,
		}
		if latestJob.Candidate != nil {
			ocrJobSummary["warnings"] = latestJob.Candidate.Warnings
		}
		snapshotData["ocr_job"] = ocrJobSummary
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
			// Heartbeat giữ kết nối
			_ = writeSSEEvent(w, flusher, "heartbeat", map[string]int64{
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
