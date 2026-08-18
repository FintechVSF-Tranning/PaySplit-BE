package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	transportmw "paysplit-backend/internal/transport/http/middleware"
)

// RegisterRoutes đăng ký các endpoint quản trị (/api/v1/admin/*) được bảo vệ bằng liveAuth và RequireRole("admin").
func (h *Handler) RegisterRoutes(r chi.Router, liveAuth func(http.Handler) http.Handler) {
	r.Group(func(admin chi.Router) {
		admin.Use(liveAuth)
		admin.Use(transportmw.RequireRole("admin"))

		admin.Get("/accounts", h.ListAccounts)
		admin.Get("/accounts/{id}", h.GetAccountDetail)
		admin.Put("/accounts/{id}/status", h.UpdateAccountStatus)
		admin.Get("/system/overview", h.GetSystemOverview)
	})
}
