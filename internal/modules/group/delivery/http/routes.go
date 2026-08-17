package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (h *Handler) RegisterGroupRoutes(router chi.Router, liveAuth func(http.Handler) http.Handler) {
	router.Group(func(protected chi.Router) {
		protected.Use(liveAuth)
		protected.Post("/", h.CreateGroup)
		protected.Get("/", h.ListGroups)
		protected.Get("/{id}", h.GetGroupDetail)
		protected.Post("/{id}/invites", h.CreateInvite)
		protected.Delete("/{id}/invites/{inviteId}", h.RevokeInvite)
		protected.Get("/invites/{code}", h.PreviewInvite)
		protected.Post("/join", h.JoinGroup)
		protected.Delete("/{id}/members/{memberId}", h.LeaveOrRemoveMember)
		protected.Put("/{id}/members/{memberId}/role", h.TransferRole)
		protected.Get("/{id}/activities", h.ListActivities)
	})
}
