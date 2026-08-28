package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"paysplit-backend/internal/transport/http/helpers"
)

// RegisterGroupCloseRoutes đăng ký các endpoint group bill close v1 (Spec 0008)
// trên mount /api/v1/groups, cạnh RegisterGroupRoutes của module group:
//   - POST /{groupId}/bills/lock-submissions
//   - POST /{groupId}/bills/finalize-all
//   - GET  /{groupId}/bill-finalize-batches/{batchId}
func (h *Handler) RegisterGroupCloseRoutes(r chi.Router, authMiddleware func(http.Handler) http.Handler) {
	r.Group(func(protected chi.Router) {
		protected.Use(authMiddleware)
		protected.Post("/{groupId}/bills/lock-submissions", h.LockSubmissions)
		protected.Post("/{groupId}/bills/unlock-submissions", h.UnlockSubmissions)
		protected.Post("/{groupId}/bills/finalize-all", h.StartBulkFinalize)
		protected.Get("/{groupId}/bill-finalize-batches/{batchId}", h.GetFinalizeBatch)
	})
}

// LockSubmissions xử lý POST /groups/{groupId}/bills/lock-submissions.
func (h *Handler) LockSubmissions(w http.ResponseWriter, r *http.Request) {
	callerUserID := getUserID(r)
	groupID, err := uuid.Parse(chi.URLParam(r, "groupId"))
	if err != nil {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "INVALID_GROUP_ID", "invalid group ID", nil)
		return
	}

	handled, rawKey := h.checkIdempotency(w, r, callerUserID, "lock_bill_submissions", []byte("group:"+groupID.String()))
	if handled {
		return
	}

	result, err := h.service.LockSubmissions(r.Context(), callerUserID, groupID)
	if err != nil {
		_ = h.service.ReleaseIdempotency(r.Context(), callerUserID, "lock_bill_submissions", rawKey)
		writeDomainError(w, err)
		return
	}

	resourceID := groupID
	_ = h.service.CompleteIdempotency(r.Context(), callerUserID, "lock_bill_submissions", rawKey, http.StatusOK, result, &resourceID)

	_ = helpers.WriteJSON(w, http.StatusOK, result)
}

// UnlockSubmissions xử lý POST /groups/{groupId}/bills/unlock-submissions.
func (h *Handler) UnlockSubmissions(w http.ResponseWriter, r *http.Request) {
	callerUserID := getUserID(r)
	groupID, err := uuid.Parse(chi.URLParam(r, "groupId"))
	if err != nil {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "INVALID_GROUP_ID", "invalid group ID", nil)
		return
	}

	err = h.service.UnlockSubmissions(r.Context(), callerUserID, groupID)
	if err != nil {
		writeDomainError(w, err)
		return
	}

	_ = helpers.WriteJSON(w, http.StatusOK, map[string]any{
		"bill_submission_locked":    false,
		"bill_submission_locked_at": nil,
	})
}

// StartBulkFinalize xử lý POST /groups/{groupId}/bills/finalize-all: mở batch chốt
// toàn bộ và trả 202 kèm tóm tắt batch (Spec 0008 AC-4). Xử lý từng bill chạy bất
// đồng bộ qua River; kết quả đọc được qua endpoint batch detail (AC-6).
func (h *Handler) StartBulkFinalize(w http.ResponseWriter, r *http.Request) {
	callerUserID := getUserID(r)
	groupID, err := uuid.Parse(chi.URLParam(r, "groupId"))
	if err != nil {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "INVALID_GROUP_ID", "invalid group ID", nil)
		return
	}

	handled, rawKey := h.checkIdempotency(w, r, callerUserID, "bulk_finalize_all", []byte("group:"+groupID.String()))
	if handled {
		return
	}

	result, err := h.service.StartBulkFinalizeIdempotent(r.Context(), callerUserID, groupID, rawKey)
	if err != nil {
		_ = h.service.ReleaseIdempotency(r.Context(), callerUserID, "bulk_finalize_all", rawKey)
		writeDomainError(w, err)
		return
	}

	_ = helpers.WriteJSON(w, http.StatusAccepted, result)
}

// GetFinalizeBatch xử lý GET /groups/{groupId}/bill-finalize-batches/{batchId}
// với phân trang cursor trên kết quả item (Spec 0008 AC-6). Chỉ Captain active
// được đọc; thành viên thường nhận 403 CAPTAIN_REQUIRED.
func (h *Handler) GetFinalizeBatch(w http.ResponseWriter, r *http.Request) {
	callerUserID := getUserID(r)
	groupID, err := uuid.Parse(chi.URLParam(r, "groupId"))
	if err != nil {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "INVALID_GROUP_ID", "invalid group ID", nil)
		return
	}
	batchID, err := uuid.Parse(chi.URLParam(r, "batchId"))
	if err != nil {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "INVALID_BATCH_ID", "invalid batch ID", nil)
		return
	}

	limit := int32(20)
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, ok := parsePositiveLimit(raw)
		if !ok {
			_ = helpers.WriteAPIError(w, http.StatusBadRequest, "VALIDATION_FAILED", "limit must be between 1 and 100", nil)
			return
		}
		limit = parsed
	}

	var cursor *string
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		cursor = &raw
	}

	result, err := h.service.GetFinalizeBatch(r.Context(), callerUserID, groupID, batchID, cursor, limit)
	if err != nil {
		writeDomainError(w, err)
		return
	}

	resp := map[string]any{
		"batch": result.Batch,
		"items": result.Items,
	}
	if result.NextCursor != nil {
		resp["next_cursor"] = *result.NextCursor
	}
	_ = helpers.WriteJSON(w, http.StatusOK, resp)
}

// parsePositiveLimit đọc limit query param cho trang item: mặc định 20, chấp
// nhận 1 đến 100 theo Spec 0008 API surface.
func parsePositiveLimit(raw string) (int32, bool) {
	const (
		defaultItemPageLimit = 20
		maxItemPageLimit     = 100
	)
	if raw == "" {
		return defaultItemPageLimit, true
	}
	n := 0
	for _, c := range raw {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
		if n > maxItemPageLimit {
			return 0, false
		}
	}
	if n < 1 {
		return 0, false
	}
	return int32(n), true
}
