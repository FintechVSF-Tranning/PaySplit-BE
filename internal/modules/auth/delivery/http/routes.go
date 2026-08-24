package http

import (
	"github.com/go-chi/chi/v5"
	"net/http"
)

func (h *Handler) RegisterAuthRoutes(router chi.Router, tokenAuth func(http.Handler) http.Handler) {
	router.Post("/sign-up", h.SignUp)
	router.Post("/verify-email", h.VerifyEmail)
	router.Post("/resend-verification", h.ResendVerification)
	router.Post("/sign-in", h.SignIn)
	router.Post("/refresh", h.Refresh)
	router.Post("/forgot-password", h.ForgotPassword)
	router.Post("/reset-password", h.ResetPassword)
	router.With(tokenAuth).Post("/sign-out", h.SignOut)
}
func (h *Handler) RegisterUserRoutes(router chi.Router, liveAuth func(http.Handler) http.Handler) {
	router.Group(func(protected chi.Router) {
		protected.Use(liveAuth)
		protected.Get("/me", h.GetProfile)
		protected.Patch("/me", h.PatchProfile)
		protected.Put("/me/password", h.ChangePassword)
		protected.Put("/me/avatar", h.UploadAvatar)
		protected.Delete("/me/avatar", h.DeleteAvatar)
		protected.Put("/me/fcm-token", h.UpdateFCMToken)
	})
}
