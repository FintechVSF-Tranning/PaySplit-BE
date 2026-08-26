package http

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"paysplit-backend/internal/modules/group/domain"
	"paysplit-backend/internal/modules/group/usecase"
	"paysplit-backend/internal/transport/http/helpers"
	authmw "paysplit-backend/internal/transport/http/middleware"
)

// SyncGroup phục vụ GET /api/v1/groups/{id}/sync?since=N — đường catch-up nguội
// của giao thức, dùng khi mở app lạnh, app resume, mạng vừa nối lại, bị push
// đánh thức, hoặc khi client không dùng được SSE và phải poll.
//
// Body luôn nhẹ khi không có gì mới, nên poll thưa là chiến lược dự phòng chấp
// nhận được.
func (h *Handler) SyncGroup(w http.ResponseWriter, r *http.Request) {
	userID, _ := authmw.UserID(r.Context())
	groupID := chi.URLParam(r, "id")

	since, ok := parseSince(w, r)
	if !ok {
		return
	}

	page, err := h.service.Sync(r.Context(), usecase.SyncInput{
		GroupID:      groupID,
		CallerUserID: userID,
		Since:        since,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}

	resp := map[string]any{"version": page.Version, "mode": page.Mode}
	if page.Mode == domain.SyncModeSnapshot {
		if page.Snapshot != nil {
			resp["snapshot"] = h.newGroupDetailResponse(*page.Snapshot)
		}
		writeJSON(w, r, http.StatusOK, resp)
		return
	}

	events := make([]map[string]any, 0, len(page.Events))
	for _, event := range page.Events {
		events = append(events, map[string]any{
			"version":    event.Version,
			"type":       event.Type,
			"data":       h.renderEventPayload(event.Payload),
			"created_at": event.CreatedAt,
		})
	}
	resp["events"] = events
	writeJSON(w, r, http.StatusOK, resp)
}

// parseSince đọc tham số since dùng chung cho /sync và /events. Thiếu tham số
// nghĩa là client chưa có gì và sẽ nhận snapshot.
func parseSince(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := r.URL.Query().Get("since")
	if raw == "" {
		return 0, true
	}
	since, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || since < 0 {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "VALIDATION_FAILED", "since must be a non-negative integer", nil)
		return 0, false
	}
	return since, true
}
