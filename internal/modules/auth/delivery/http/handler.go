package http

import (
	"net/http"

	"paysplit-backend/internal/modules/auth/usecase"
)

type Handler struct {
	service *usecase.Service
}

func NewHandler(service *usecase.Service) *Handler {
	panic("TODO: implement http.NewHandler")
}

func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	panic("TODO: implement Handler.Register")
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	panic("TODO: implement Handler.Login")
}
