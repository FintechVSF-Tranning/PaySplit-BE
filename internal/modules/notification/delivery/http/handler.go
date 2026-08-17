package http

import (
	"net/http"

	"paysplit-backend/internal/modules/notification/usecase"
	"paysplit-backend/internal/transport/http/helpers"
	transportmw "paysplit-backend/internal/transport/http/middleware"

	"github.com/go-chi/chi/v5"
)

type Handler struct {
	service *usecase.Service
}

func NewHandler(service *usecase.Service) *Handler {
	return &Handler{service: service}
}

type updateFCMTokenRequest struct {
	FCMToken string `json:"fcm_token"`
}

// UpdateFCMToken cập nhật FCM Token cho session hiện tại
func (h *Handler) UpdateFCMToken(w http.ResponseWriter, r *http.Request) {
	sessionID, ok := transportmw.SessionID(r.Context())
	if !ok || sessionID == "" {
		_ = helpers.WriteAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication session required", nil)
		return
	}

	var req updateFCMTokenRequest
	if err := helpers.ReadJSON(w, r, &req); err != nil {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body", map[string]string{"body": err.Error()})
		return
	}

	if req.FCMToken == "" {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "VALIDATION_FAILED", "fcm_token is required", nil)
		return
	}

	if err := h.service.UpdateFCMToken(r.Context(), sessionID, req.FCMToken); err != nil {
		_ = helpers.WriteAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to update FCM token", nil)
		return
	}

	_ = helpers.WriteJSON(w, http.StatusOK, map[string]string{"message": "FCM token updated successfully"})
}

// ListNotifications lấy danh sách thông báo của user đang đăng nhập (có phân trang)
func (h *Handler) ListNotifications(w http.ResponseWriter, r *http.Request) {
	userID, ok := transportmw.UserID(r.Context())
	if !ok || userID == "" {
		_ = helpers.WriteAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required", nil)
		return
	}

	pager := helpers.ParseOffsetPager(r)
	page, err := h.service.ListNotifications(r.Context(), userID, pager)
	if err != nil {
		_ = helpers.WriteAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list notifications", nil)
		return
	}

	_ = helpers.WritePaginated(w, http.StatusOK, page)
}

// GetUnreadCount trả về số lượng thông báo chưa đọc
func (h *Handler) GetUnreadCount(w http.ResponseWriter, r *http.Request) {
	userID, ok := transportmw.UserID(r.Context())
	if !ok || userID == "" {
		_ = helpers.WriteAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required", nil)
		return
	}

	count, err := h.service.GetUnreadCount(r.Context(), userID)
	if err != nil {
		_ = helpers.WriteAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get unread count", nil)
		return
	}

	_ = helpers.WriteJSON(w, http.StatusOK, map[string]int64{"unread_count": count})
}

// MarkAsRead đánh dấu 1 thông báo là đã đọc
func (h *Handler) MarkAsRead(w http.ResponseWriter, r *http.Request) {
	userID, ok := transportmw.UserID(r.Context())
	if !ok || userID == "" {
		_ = helpers.WriteAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required", nil)
		return
	}

	notifID := chi.URLParam(r, "id")
	if notifID == "" {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "VALIDATION_FAILED", "notification id is required", nil)
		return
	}

	if err := h.service.MarkAsRead(r.Context(), userID, notifID); err != nil {
		_ = helpers.WriteAPIError(w, http.StatusNotFound, "NOT_FOUND", err.Error(), nil)
		return
	}

	_ = helpers.WriteJSON(w, http.StatusOK, map[string]string{"message": "Notification marked as read"})
}

// MarkAllAsRead đánh dấu toàn bộ thông báo của user là đã đọc
func (h *Handler) MarkAllAsRead(w http.ResponseWriter, r *http.Request) {
	userID, ok := transportmw.UserID(r.Context())
	if !ok || userID == "" {
		_ = helpers.WriteAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required", nil)
		return
	}

	if err := h.service.MarkAllAsRead(r.Context(), userID); err != nil {
		_ = helpers.WriteAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to mark all as read", nil)
		return
	}

	_ = helpers.WriteJSON(w, http.StatusOK, map[string]string{"message": "All notifications marked as read"})
}
