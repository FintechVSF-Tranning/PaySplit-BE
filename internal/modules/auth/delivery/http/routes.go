package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (h *Handler) RegisterAuthRoutes(router chi.Router) {
	router.Post("/sign-up", h.SignUp)
	router.Post("/verify-email", h.VerifyEmail)
	router.Post("/resend-verification", h.ResendVerification)
	router.Post("/sign-in", h.SignIn)
	router.Post("/forgot-password", h.ForgotPassword)
	router.Post("/reset-password", h.ResetPassword)
	// Không gắn middleware xác thực — xem chú thích ở Handler.SignOut.
	router.Post("/sign-out", h.SignOut)
}
func (h *Handler) RegisterUserRoutes(router chi.Router, liveAuth func(http.Handler) http.Handler, sse *SSEHandler) {
	router.Group(func(protected chi.Router) {
		protected.Use(liveAuth)
		protected.Get("/me", h.GetProfile)
		protected.Patch("/me", h.PatchProfile)
		protected.Put("/me/password", h.ChangePassword)
		protected.Put("/me/avatar", h.UploadAvatar)
		protected.Delete("/me/avatar", h.DeleteAvatar)
		protected.Put("/me/fcm-token", h.UpdateFCMToken)
		if sse != nil {
			protected.Get("/me/events", sse.StreamUserEvents)
		}
	})
}
