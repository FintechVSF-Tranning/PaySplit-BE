package http

import (
	"paysplit-backend/internal/modules/auth/domain"
	"time"
)

type userResponse struct {
	ID                string     `json:"id"`
	Email             string     `json:"email"`
	PhoneNumber       string     `json:"phone_number"`
	DisplayName       string     `json:"display_name"`
	Role              string     `json:"role"`
	Status            string     `json:"status"`
	EmailVerifiedAt   *time.Time `json:"email_verified_at"`
	BankCode          *string    `json:"bank_code"`
	BankAccountNumber *string    `json:"bank_account_number"`
	BankAccountHolder *string    `json:"bank_account_holder"`
	AvatarURL         *string    `json:"avatar_url"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}
type tokenResponse struct {
	User                  *userResponse `json:"user,omitempty"`
	TokenType             string        `json:"token_type"`
	AccessToken           string        `json:"access_token"`
	AccessTokenExpiresAt  time.Time     `json:"access_token_expires_at"`
	RefreshToken          string        `json:"refresh_token"`
	RefreshTokenExpiresAt time.Time     `json:"refresh_token_expires_at"`
}

func (h *Handler) userResponse(user *domain.User) userResponse {
	out := userResponse{ID: user.ID, Email: user.Email, PhoneNumber: user.PhoneNumber, DisplayName: user.DisplayName, Role: user.Role, Status: user.Status, EmailVerifiedAt: user.EmailVerifiedAt, BankCode: user.BankCode, BankAccountNumber: user.BankAccountNumber, BankAccountHolder: user.BankAccountHolder, CreatedAt: user.CreatedAt, UpdatedAt: user.UpdatedAt}
	if user.AvatarObjectKey != nil {
		url := h.avatarURL(*user.AvatarObjectKey)
		out.AvatarURL = &url
	}
	return out
}
