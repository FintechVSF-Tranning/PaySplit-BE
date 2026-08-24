package repository

import (
	"context"
	"time"

	"paysplit-backend/internal/modules/auth/domain"
)

type CreateUserParams struct {
	Email, PhoneNumber, DisplayName, PasswordHash string
	VerificationTokenHash                         []byte
	VerificationExpiresAt                         time.Time
}

type CreateSessionParams struct {
	UserID, ExpectedPasswordHash, DeviceID, DeviceName, FCMToken string
	RefreshTokenHash                                             []byte
	Now, ExpiresAt                                               time.Time
}

type RotateRefreshResult struct {
	User      *domain.User
	SessionID string
	ExpiresAt time.Time
}

type ProfilePatch struct {
	DisplayName *string
	PhoneNumber *string
	Bank        *domain.BankProfile
}

type Repository interface {
	CreateUser(context.Context, CreateUserParams) (*domain.User, error)
	GetByEmail(context.Context, string) (*domain.User, error)
	GetByID(context.Context, string) (*domain.User, error)
	CreateUserToken(context.Context, string, string, []byte, time.Time) error
	VerifyEmail(context.Context, string, []byte, time.Time) (*domain.User, error)
	RecordLoginFailure(context.Context, string, time.Time) (time.Duration, error)
	CreateSession(context.Context, CreateSessionParams) (*domain.User, *domain.Session, error)
	RotateRefresh(context.Context, []byte, []byte, string, time.Time) (*RotateRefreshResult, error)
	ValidateSession(context.Context, string, string, time.Time) (*domain.SessionIdentity, error)
	RevokeSession(context.Context, string, string, string, time.Time) error
	ResetPassword(context.Context, string, []byte, string, time.Time) error
	ChangePassword(context.Context, string, string, string, time.Time) error
	UpdateProfile(context.Context, string, ProfilePatch) (*domain.User, error)
	SetAvatar(context.Context, string, string) (*domain.User, *string, error)
	ClearAvatar(context.Context, string) (*string, error)
	CheckAndRecordRateLimit(context.Context, string, map[string][]byte, time.Time) (time.Duration, error)
	EnqueueMediaCleanup(context.Context, string, string) error
	ClaimMediaCleanup(context.Context, time.Time, int) ([]domain.MediaCleanupJob, error)
	CompleteMediaCleanup(context.Context, string, time.Time) error
	FailMediaCleanup(context.Context, string, string, time.Time) error
	UpdateSessionFCMToken(context.Context, string, string) error
	CleanupExpiredAuth(context.Context, time.Time, int) (int64, error)
}
