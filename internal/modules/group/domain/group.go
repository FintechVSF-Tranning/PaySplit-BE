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
	// Emoji là icon hiển thị của nhóm. NULL khi nhóm chưa chọn icon; client tự
	// quyết định icon mặc định thay vì backend áp đặt một giá trị.
	Emoji *string
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
// Mọi trường ở đây được dựng trong một truy vấn duy nhất để màn hình danh sách
// nhóm không phải gọi thêm GET /groups/{id} cho từng nhóm.
type GroupListItem struct {
	Group              Group
	CallerMembershipID string
	CallerRole         string
	ActiveMemberCount  int
	// CallerNetBalance là số dư ròng VND của caller trong nhóm: dương nghĩa là
	// được nhận về, âm nghĩa là còn nợ, 0 khi nhóm chưa phát sinh công nợ.
	CallerNetBalance int64
	// PendingBillCount đếm hóa đơn chưa chốt (khác finalized và voided), cùng
	// định nghĩa với rào chắn giải tán nhóm.
	PendingBillCount int
	// LastActivity là hoạt động gần nhất của nhóm, nil khi nhóm chưa có hoạt động.
	LastActivity *ActivitySummary
}

// ActivitySummary là bản rút gọn của một dòng hoạt động, đủ để hiển thị trên
// thẻ nhóm mà không phải tải cả trang hoạt động.
type ActivitySummary struct {
	Description string
	CreatedAt   time.Time
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
	Group   Group
	Members []Member
	// Version là roster_version của nhóm tại thời điểm đọc. Client lưu lại làm
	// điểm xuất phát cho stream SSE và cho mọi lần catch-up sau đó, nên nó phải
	// đến từ cùng một lần đọc với Members chứ không phải một truy vấn rời.
	Version    int64
	Balances   []Balance
	CallerRole string
	// PendingBillCount đếm hóa đơn chưa chốt, cùng định nghĩa với trường cùng
	// tên trong danh sách nhóm. Có mặt ở đây để client làm mới đúng một thẻ
	// nhóm mà không phải tải lại cả trang danh sách.
	PendingBillCount int
	// CallerMembershipID cho client tự nhận ra mình trong Members mà không phải
	// suy từ vai trò (nhiều thành viên cùng vai trò) hay gọi thêm GET /groups.
	CallerMembershipID string
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
