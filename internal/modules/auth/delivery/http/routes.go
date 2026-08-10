package http

import "github.com/go-chi/chi/v5"

// RegisterRoutes declares the endpoints owned by the authentication module.
// The caller is responsible for mounting this group at its API prefix.
func (h *Handler) RegisterRoutes(router chi.Router) {
	router.Post("/register", h.Register)
	router.Post("/login", h.Login)
}
