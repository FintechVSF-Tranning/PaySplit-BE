package domain

import "time"

const (
	StatusPendingVerification = "pending_verification"
	StatusActive              = "active"
	StatusSuspended           = "suspended"
	StatusLocked              = "locked"
	TokenEmailVerification    = "email_verification"
	TokenPasswordReset        = "password_reset"
)

type User struct {
	ID                         string
	Email                      string
	PhoneNumber                string
	DisplayName                string
	PasswordHash               string
	AvatarObjectKey            *string
	BankCode                   *string
	BankAccountNumber          *string
	BankAccountHolder          *string
	Role                       string
	Status                     string
	EmailVerifiedAt            *time.Time
	CreatedAt                  time.Time
	UpdatedAt                  time.Time
	FailedLoginCount           int
	FailedLoginWindowStartedAt *time.Time
	LoginBlockedUntil          *time.Time
}

// Session là bản ghi một lần đăng nhập. Địa chỉ push của thiết bị không nằm ở
// đây mà ở bảng device_tokens: nó thuộc về thiết bị và phải sống lâu hơn phiên.
type Session struct {
	ID        string
	UserID    string
	DeviceID  string
	ExpiresAt time.Time
}

type SessionIdentity struct {
	UserID    string
	Role      string
	SessionID string
}

type BankProfile struct {
	Code          *string
	AccountNumber *string
	AccountHolder *string
}

type MediaCleanupJob struct {
	ID, ObjectKey string
	AttemptCount  int
}
