package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// RegisterGroupRoutes mounts the group module under /api/v1/groups.
//
// sse có thể nil ở môi trường không bật realtime (ví dụ test); khi đó route
// /events không được đăng ký và client tự rơi về đường poll /sync.
func (h *Handler) RegisterGroupRoutes(router chi.Router, sse *SSEHandler, liveAuth, inviteAttempts func(http.Handler) http.Handler) {
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
		protected.Get("/{id}/sync", h.SyncGroup)
		if sse != nil {
			// Hậu tố /events được middleware Timeout miễn trừ; đổi tên route là
			// stream bị cắt sau 15 giây.
			protected.Get("/{id}/events", sse.StreamGroupEvents)
		}
	})
}
