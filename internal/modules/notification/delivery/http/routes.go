package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// RegisterRoutes đăng ký các API routes cho quản lý thông báo /notifications
func (h *Handler) RegisterRoutes(router chi.Router, liveAuth func(http.Handler) http.Handler) {
	router.Group(func(protected chi.Router) {
		protected.Use(liveAuth)
		protected.Get("/", h.ListNotifications)
		protected.Get("/unread-count", h.GetUnreadCount)
		protected.Patch("/read-all", h.MarkAllAsRead)
		protected.Patch("/{id}/read", h.MarkAsRead)
	})
}
