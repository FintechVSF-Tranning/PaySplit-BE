package domain

import (
	"crypto/rand"
	"io"
	"time"
)

const (
	InviteCodeLength   = 8
	inviteCodeAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	inviteCodeByteCeil = 248
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

// NewInviteCode returns exactly eight unbiased, case-sensitive Base62
// characters. Values 248 through 255 are rejected before modulo 62 because
// 248 is the largest multiple of 62 representable by one byte.
func NewInviteCode() (string, error) {
	return newInviteCode(rand.Reader)
}

func newInviteCode(source io.Reader) (string, error) {
	code := make([]byte, 0, InviteCodeLength)
	raw := make([]byte, InviteCodeLength)
	for len(code) < InviteCodeLength {
		if _, err := io.ReadFull(source, raw); err != nil {
			return "", err
		}
		for _, value := range raw {
			if value >= inviteCodeByteCeil {
				continue
			}
			code = append(code, inviteCodeAlphabet[int(value)%len(inviteCodeAlphabet)])
			if len(code) == InviteCodeLength {
				break
			}
		}
	}
	return string(code), nil
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
