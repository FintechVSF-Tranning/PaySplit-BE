package domain

import (
	"errors"
	"time"
)

var (
	ErrInvalidInput           = errors.New("invalid input")
	ErrEmailAlreadyExists     = errors.New("email already exists")
	ErrPhoneAlreadyExists     = errors.New("phone already exists")
	ErrInvalidCredentials     = errors.New("invalid credentials")
	ErrUserNotFound           = errors.New("user not found")
	ErrEmailNotVerified       = errors.New("email not verified")
	ErrAccountUnavailable     = errors.New("account unavailable")
	ErrInvalidOrExpiredToken  = errors.New("invalid or expired token")
	ErrSessionRevoked         = errors.New("session revoked")
	ErrInvalidCurrentPassword = errors.New("invalid current password")
	ErrUnsupportedBank        = errors.New("unsupported bank")
	ErrInvalidImage           = errors.New("invalid image")
	ErrPayloadTooLarge        = errors.New("payload too large")
	ErrImageStorage           = errors.New("image storage failed")
)

type RateLimitError struct {
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string { return "rate limited" }
