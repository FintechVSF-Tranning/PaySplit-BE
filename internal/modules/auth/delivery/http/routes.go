package http

import "github.com/go-chi/chi/v5"

// RegisterRoutes đăng ký các endpoint cục bộ do module auth sở hữu. Bootstrap
// chịu trách nhiệm mount nhóm route này vào prefix chung như /api/v1/auth.
func (h *Handler) RegisterRoutes(router chi.Router) {
	router.Post("/register", h.Register)
	router.Post("/login", h.Login)
}
