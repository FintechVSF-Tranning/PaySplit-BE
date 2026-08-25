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
	// BillSubmissionLockedAt là mốc thời gian khóa gửi hóa đơn một chiều của V1
	// (Spec 0008 AC-1). NULL nghĩa là nhóm còn mở, khác NULL là đã khóa vĩnh viễn.
	BillSubmissionLockedAt *time.Time
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
	// Captain batch navigation (Spec 0008 Public response fields): chỉ điền khi
	// caller là Captain active, omitted cho thành viên thường để họ không suy ra
	// được ID batch hay kết quả xử lý.
	ActiveBillFinalizeBatchID *string
	LatestBillFinalizeBatchID *string
}

// CaptainTransfer is the outcome of moving the Captain role to another
// active member.
type CaptainTransfer struct {
	PreviousCaptainMembershipID string
	CurrentCaptainMembershipID  string
}
