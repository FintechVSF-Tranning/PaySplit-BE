package domain

import (
	"crypto/rand"
	"encoding/base64"
	"time"
)

// Invite is a Captain issued code that lets someone join a group.
type Invite struct {
	ID        string
	GroupID   string
	Code      string
	CreatedBy string
	ExpiresAt time.Time
	MaxUses   *int
	UseCount  int
	RevokedAt *time.Time
	CreatedAt time.Time
}

// NewInviteCode returns 32 cryptographically random bytes encoded as
// base64url without padding, per spec 0002's invite code source.
func NewInviteCode() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// InvitePreview is shown to an authenticated nonmember before they join.
type InvitePreview struct {
	GroupName          string
	ActiveMemberCount  int
	CaptainDisplayName string
}

// JoinResult is the outcome of redeeming an invite code.
type JoinResult struct {
	GroupID      string
	MembershipID string
	Role         string
	Status       string
	// Result is "joined", "reactivated", or "already_active".
	Result string
}

const (
	JoinResultJoined        = "joined"
	JoinResultReactivated   = "reactivated"
	JoinResultAlreadyActive = "already_active"
)
