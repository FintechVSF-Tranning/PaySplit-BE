package http

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	chiMiddleware "github.com/go-chi/chi/v5/middleware"

	"paysplit-backend/internal/modules/auth/domain"
	"paysplit-backend/internal/modules/auth/usecase"
	"paysplit-backend/internal/transport/http/helpers"
	authmw "paysplit-backend/internal/transport/http/middleware"
)

type Handler struct {
	service   *usecase.Service
	avatarURL func(string) string
}

func NewHandler(service *usecase.Service, avatarURL func(string) string) *Handler {
	if service == nil {
		panic("auth handler service must not be nil")
	}
	if avatarURL == nil {
		avatarURL = func(string) string { return "" }
	}
	return &Handler{service: service, avatarURL: avatarURL}
}

func (h *Handler) SignUp(w http.ResponseWriter, r *http.Request) {
	var req signUpRequest
	if !read(w, r, &req) {
		return
	}
	out, err := h.service.SignUp(r.Context(), usecase.SignUpInput{Email: req.Email, PhoneNumber: req.PhoneNumber, DisplayName: req.DisplayName, Password: req.Password, ClientIP: clientIP(r)})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	u := h.userResponse(out.User)
	writeJSON(w, http.StatusCreated, map[string]any{"user": u, "verification_email_sent": out.VerificationEmailSent, "verification_expires_at": out.VerificationExpiresAt})
}
func (h *Handler) VerifyEmail(w http.ResponseWriter, r *http.Request) {
	var req tokenRequest
	if !read(w, r, &req) {
		return
	}
	if _, err := h.service.VerifyEmail(r.Context(), req.Token); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "active"})
}
func (h *Handler) ResendVerification(w http.ResponseWriter, r *http.Request) {
	h.emailAction(w, r, h.service.ResendVerification)
}
func (h *Handler) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	h.emailAction(w, r, h.service.ForgotPassword)
}
func (h *Handler) emailAction(w http.ResponseWriter, r *http.Request, action func(ctx context.Context, email, ip string) error) {
	var req emailRequest
	if !read(w, r, &req) {
		return
	}
	if err := action(r.Context(), req.Email, clientIP(r)); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"message": "If the account is eligible, an email will be sent."})
}
func (h *Handler) SignIn(w http.ResponseWriter, r *http.Request) {
	var req signInRequest
	if !read(w, r, &req) {
		return
	}
	out, err := h.service.SignIn(r.Context(), usecase.SignInInput{Email: req.Email, Password: req.Password, DeviceID: req.DeviceID, DeviceName: req.DeviceName})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	u := h.userResponse(out.User)
	writeJSON(w, http.StatusOK, tokenResponse{User: &u, TokenType: "Bearer", AccessToken: out.AccessToken, AccessTokenExpiresAt: out.AccessExpiresAt, RefreshToken: out.RefreshToken, RefreshTokenExpiresAt: out.RefreshExpiresAt})
}
func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if !read(w, r, &req) {
		return
	}
	out, err := h.service.Refresh(r.Context(), req.RefreshToken, req.DeviceID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tokenResponse{TokenType: "Bearer", AccessToken: out.AccessToken, AccessTokenExpiresAt: out.AccessExpiresAt, RefreshToken: out.RefreshToken, RefreshTokenExpiresAt: out.RefreshExpiresAt})
}
func (h *Handler) SignOut(w http.ResponseWriter, r *http.Request) {
	userID, _ := authmw.UserID(r.Context())
	sessionID, _ := authmw.SessionID(r.Context())
	if err := h.service.SignOut(r.Context(), userID, sessionID); err != nil {
		writeDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (h *Handler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	var req resetPasswordRequest
	if !read(w, r, &req) {
		return
	}
	if err := h.service.ResetPassword(r.Context(), req.Token, req.NewPassword); err != nil {
		writeDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (h *Handler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	var req changePasswordRequest
	if !read(w, r, &req) {
		return
	}
	userID, _ := authmw.UserID(r.Context())
	sessionID, _ := authmw.SessionID(r.Context())
	if err := h.service.ChangePassword(r.Context(), userID, sessionID, req.CurrentPassword, req.NewPassword); err != nil {
		writeDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (h *Handler) GetProfile(w http.ResponseWriter, r *http.Request) {
	userID, _ := authmw.UserID(r.Context())
	user, err := h.service.GetProfile(r.Context(), userID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": h.userResponse(user)})
}
func (h *Handler) PatchProfile(w http.ResponseWriter, r *http.Request) {
	var req patchProfileRequest
	if !read(w, r, &req) {
		return
	}
	in := usecase.PatchProfileInput{}
	if req.DisplayName.Set {
		if req.DisplayName.Value == nil {
			writeDomainError(w, domain.ErrInvalidInput)
			return
		}
		in.DisplayName = req.DisplayName.Value
	}
	if req.PhoneNumber.Set {
		if req.PhoneNumber.Value == nil {
			writeDomainError(w, domain.ErrInvalidInput)
			return
		}
		in.PhoneNumber = req.PhoneNumber.Value
	}
	bankSet := req.BankCode.Set || req.BankAccountNumber.Set || req.BankAccountHolder.Set
	if bankSet {
		if !req.BankCode.Set || !req.BankAccountNumber.Set || !req.BankAccountHolder.Set {
			writeDomainError(w, domain.ErrInvalidInput)
			return
		}
		in.Bank = &domain.BankProfile{Code: req.BankCode.Value, AccountNumber: req.BankAccountNumber.Value, AccountHolder: req.BankAccountHolder.Value}
	}
	userID, _ := authmw.UserID(r.Context())
	user, err := h.service.PatchProfile(r.Context(), userID, in)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": h.userResponse(user)})
}

func (h *Handler) UploadAvatar(w http.ResponseWriter, r *http.Request) {
	const maxImage = 10 << 20
	r.Body = http.MaxBytesReader(w, r.Body, maxImage+(1<<20))
	reader, err := r.MultipartReader()
	if err != nil {
		writeDomainError(w, domain.ErrInvalidImage)
		return
	}
	var data []byte
	found := false
	for {
		part, nextErr := reader.NextPart()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			writeDomainError(w, domain.ErrInvalidImage)
			return
		}
		if part.FormName() != "avatar" || part.FileName() == "" || found {
			part.Close()
			writeDomainError(w, domain.ErrInvalidImage)
			return
		}
		data, err = io.ReadAll(io.LimitReader(part, maxImage+1))
		part.Close()
		if err != nil {
			writeDomainError(w, domain.ErrInvalidImage)
			return
		}
		if len(data) > maxImage {
			writeDomainError(w, domain.ErrPayloadTooLarge)
			return
		}
		found = true
	}
	if !found {
		writeDomainError(w, domain.ErrInvalidImage)
		return
	}
	userID, _ := authmw.UserID(r.Context())
	user, err := h.service.UploadAvatar(r.Context(), userID, data)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	response := h.userResponse(user)
	writeJSON(w, http.StatusOK, map[string]*string{"avatar_url": response.AvatarURL})
}
func (h *Handler) DeleteAvatar(w http.ResponseWriter, r *http.Request) {
	userID, _ := authmw.UserID(r.Context())
	if err := h.service.DeleteAvatar(r.Context(), userID); err != nil {
		writeDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func read(w http.ResponseWriter, r *http.Request, d any) bool {
	if err := helpers.ReadJSON(w, r, d); err != nil {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "VALIDATION_FAILED", "invalid request body", nil)
		return false
	}
	return true
}
func writeJSON(w http.ResponseWriter, status int, data any) {
	if err := helpers.WriteJSON(w, status, data); err != nil {
		log.Printf("event=response_write_failed request_id=%s", chiMiddleware.GetReqID(context.Background()))
	}
}
func writeDomainError(w http.ResponseWriter, err error) {
	status, code, message := http.StatusInternalServerError, "INTERNAL_ERROR", "unable to process request"
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		status, code, message = http.StatusBadRequest, "VALIDATION_FAILED", "request validation failed"
	case errors.Is(err, domain.ErrEmailAlreadyExists):
		status, code, message = http.StatusConflict, "EMAIL_EXISTS", "email already exists"
	case errors.Is(err, domain.ErrPhoneAlreadyExists):
		status, code, message = http.StatusConflict, "PHONE_EXISTS", "phone number already exists"
	case errors.Is(err, domain.ErrInvalidCredentials):
		status, code, message = http.StatusUnauthorized, "INVALID_CREDENTIALS", "invalid email or password"
	case errors.Is(err, domain.ErrEmailNotVerified):
		status, code, message = http.StatusForbidden, "EMAIL_NOT_VERIFIED", "email verification is required"
	case errors.Is(err, domain.ErrAccountUnavailable):
		status, code, message = http.StatusForbidden, "ACCOUNT_UNAVAILABLE", "account is unavailable"
	case errors.Is(err, domain.ErrInvalidOrExpiredToken):
		status, code, message = http.StatusBadRequest, "INVALID_OR_EXPIRED_TOKEN", "token is invalid or expired"
	case errors.Is(err, domain.ErrSessionRevoked):
		status, code, message = http.StatusUnauthorized, "SESSION_REVOKED", "session is no longer active"
	case errors.Is(err, domain.ErrInvalidCurrentPassword):
		status, code, message = http.StatusBadRequest, "INVALID_CURRENT_PASSWORD", "current password is invalid"
	case errors.Is(err, domain.ErrUnsupportedBank):
		status, code, message = http.StatusBadRequest, "UNSUPPORTED_BANK", "bank is not supported"
	case errors.Is(err, domain.ErrInvalidImage):
		status, code, message = http.StatusBadRequest, "INVALID_IMAGE", "image is invalid"
	case errors.Is(err, domain.ErrPayloadTooLarge):
		status, code, message = http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "image exceeds 10 MB"
	case errors.Is(err, domain.ErrImageStorage):
		status, code, message = http.StatusBadGateway, "IMAGE_STORAGE_FAILED", "image storage failed"
	}
	var rate *domain.RateLimitError
	if errors.As(err, &rate) {
		status, code, message = http.StatusTooManyRequests, "RATE_LIMITED", "too many requests"
		seconds := int((rate.RetryAfter + time.Second - 1) / time.Second)
		if seconds < 1 {
			seconds = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
	}
	_ = helpers.WriteAPIError(w, status, code, message, nil)
}
func clientIP(r *http.Request) string {
	if value := chiMiddleware.GetClientIP(r.Context()); value != "" {
		return value
	}
	return r.RemoteAddr
}
