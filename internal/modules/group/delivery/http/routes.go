package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (h *Handler) RegisterGroupRoutes(router chi.Router, liveAuth, inviteAttempts func(http.Handler) http.Handler) {
	if inviteAttempts == nil {
		panic("group invite attempt limiter must not be nil")
	}
	router.Group(func(protected chi.Router) {
		protected.Use(liveAuth)
		protected.Post("/", h.CreateGroup)
		protected.Get("/", h.ListGroups)
		protected.Get("/{id}", h.GetGroupDetail)
		protected.Patch("/{id}", h.RenameGroup)
		protected.Delete("/{id}", h.DisbandGroup)
		protected.Get("/{id}/invites", h.ListInvites)
		protected.Post("/{id}/invites", h.CreateInvite)
		protected.Delete("/{id}/invites/{inviteId}", h.RevokeInvite)
		protected.With(inviteAttempts).Get("/invites/{code}", h.PreviewInvite)
		protected.With(inviteAttempts).Post("/join", h.JoinGroup)
		protected.Delete("/{id}/members/{memberId}", h.LeaveOrRemoveMember)
		protected.Put("/{id}/members/{memberId}/role", h.TransferRole)
		protected.Get("/{id}/activities", h.ListActivities)
	})
}
