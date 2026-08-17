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
