package domain

import "time"

const (
	RoleCaptain = "captain"
	RoleMember  = "member"
)

const (
	MembershipActive   = "active"
	MembershipInactive = "inactive"
)

const (
	GroupActive   = "active"
	GroupArchived = "archived"
)

type Group struct {
	ID        string
	Name      string
	Currency  string
	CreatedBy string
	CreatedAt time.Time
	Status    string
}

type Membership struct {
	ID       string
	GroupID  string
	UserID   string
	Role     string
	Status   string
	JoinedAt time.Time
	LeftAt   *time.Time
}

// GroupListItem is one row of the caller's active group membership list.
type GroupListItem struct {
	Group              Group
	CallerMembershipID string
	CallerRole         string
	ActiveMemberCount  int
}

// Member is an active group member as shown on the group detail screen.
type Member struct {
	MembershipID    string
	UserID          string
	DisplayName     string
	AvatarObjectKey *string
	Role            string
	JoinedAt        time.Time
}

// Balance is a member's net VND balance within a group.
type Balance struct {
	MemberID   string
	NetBalance int64
}

// GroupDetail is the full group view returned to an active member.
type GroupDetail struct {
	Group      Group
	Members    []Member
	Balances   []Balance
	CallerRole string
}

// CaptainTransfer is the outcome of moving the Captain role to another
// active member.
type CaptainTransfer struct {
	PreviousCaptainMembershipID string
	CurrentCaptainMembershipID  string
}
