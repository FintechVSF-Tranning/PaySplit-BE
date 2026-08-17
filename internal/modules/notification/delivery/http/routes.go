package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// RegisterRoutes đăng ký các API routes cho thông báo
func (h *Handler) RegisterRoutes(r chi.Router, authMiddleware func(http.Handler) http.Handler) {
	r.Route("/api/v1", func(api chi.Router) {
		api.Group(func(protected chi.Router) {
			protected.Use(authMiddleware)

			// Cập nhật FCM token thiết bị
			protected.Put("/users/me/fcm-token", h.UpdateFCMToken)

			// Quản lý thông báo in-app
			protected.Route("/notifications", func(notif chi.Router) {
				notif.Get("/", h.ListNotifications)
				notif.Get("/unread-count", h.GetUnreadCount)
				notif.Patch("/read-all", h.MarkAllAsRead)
				notif.Patch("/{id}/read", h.MarkAsRead)
			})
		})
	})
}
