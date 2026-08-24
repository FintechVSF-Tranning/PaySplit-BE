package http

import (
	"errors"
	"net/http"

	"paysplit-backend/internal/modules/notification/domain"
	"paysplit-backend/internal/modules/notification/usecase"
	"paysplit-backend/internal/transport/http/helpers"
	transportmw "paysplit-backend/internal/transport/http/middleware"

	"github.com/brpaz/lib-go/pagination"
	"github.com/go-chi/chi/v5"
)

type Handler struct {
	service *usecase.Service
}

func NewHandler(service *usecase.Service) *Handler {
	if service == nil {
		panic("notification handler service must not be nil")
	}
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
		writeDomainError(w, err)
		return
	}

	items := make([]notificationResponse, len(page.Items))
	for i, item := range page.Items {
		items[i] = toNotificationResponse(item)
	}

	respPage := pagination.NewPage(items, page.Total, pager)
	_ = helpers.WritePaginated(w, http.StatusOK, respPage)
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
		writeDomainError(w, err)
		return
	}

	_ = helpers.WriteJSON(w, http.StatusOK, unreadCountResponse{UnreadCount: count})
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
		writeDomainError(w, err)
		return
	}

	_ = helpers.WriteJSON(w, http.StatusOK, messageResponse{Message: "Notification marked as read"})
}

// MarkAllAsRead đánh dấu toàn bộ thông báo của user là đã đọc
func (h *Handler) MarkAllAsRead(w http.ResponseWriter, r *http.Request) {
	userID, ok := transportmw.UserID(r.Context())
	if !ok || userID == "" {
		_ = helpers.WriteAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required", nil)
		return
	}

	if err := h.service.MarkAllAsRead(r.Context(), userID); err != nil {
		writeDomainError(w, err)
		return
	}

	_ = helpers.WriteJSON(w, http.StatusOK, messageResponse{Message: "All notifications marked as read"})
}

func writeDomainError(w http.ResponseWriter, err error) {
	status, code, message := http.StatusInternalServerError, "INTERNAL_ERROR", "unable to process request"
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		status, code, message = http.StatusBadRequest, "VALIDATION_FAILED", "request validation failed"
	case errors.Is(err, domain.ErrNotificationNotFound):
		status, code, message = http.StatusNotFound, "NOT_FOUND", "notification not found"
	}
	_ = helpers.WriteAPIError(w, status, code, message, nil)
}
