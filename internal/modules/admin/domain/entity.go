package domain

import "time"

// AccountSummary chứa thông tin tổng quan của người dùng phục vụ danh sách quản trị.
type AccountSummary struct {
	ID              string
	Email           string
	DisplayName     string
	AvatarObjectKey *string
	AvatarURL       string
	PhoneNumber     string
	Role            string
	Status          string
	EmailVerifiedAt *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// SafeUser đại diện cho thông tin tài khoản an toàn không chứa secret token hay password hash.
type SafeUser = AccountSummary

// BankSnapshot chứa thông tin tài khoản ngân hàng mặc định của người dùng.
type BankSnapshot struct {
	BankCode          *string
	BankAccountNumber *string
	BankAccountHolder *string
}

// GroupMembershipSummary tóm tắt vai trò và trạng thái của người dùng trong một nhóm.
type GroupMembershipSummary struct {
	GroupID   string
	GroupName string
	Role      string
	Status    string
	JoinedAt  time.Time
}

// FinancialSummary tóm tắt các khoản nợ và khoản phải thu còn mở của người dùng.
type FinancialSummary struct {
	OutstandingDebtsCount   int64
	TotalDebtAmountVND      int64
	OutstandingCreditsCount int64
	TotalCreditAmountVND    int64
}

// AuditLogEntry ghi lại một hành động quản trị viên tác động lên tài khoản người dùng.
type AuditLogEntry struct {
	ID           string
	AdminID      string
	AdminEmail   string
	TargetUserID string
	Action       string
	Reason       string
	CreatedAt    time.Time
}

// AccountDetail chứa toàn bộ thông tin chi tiết của người dùng phục vụ tra cứu quản trị.
type AccountDetail struct {
	Summary             AccountSummary
	FailedLoginCount    int
	LoginBlockedUntil   *time.Time
	Bank                BankSnapshot
	ActiveSessionsCount int64
	Groups              []GroupMembershipSummary
	Financials          FinancialSummary
	RecentAuditLogs     []AuditLogEntry
}

// WarningMeta chứa cảnh báo nếu người dùng bị khóa/đình chỉ còn nghĩa vụ tài chính chưa tất toán.
type WarningMeta struct {
	UnsettledDebtsCount   int64
	UnsettledCreditsCount int64
}

// PaginationMeta chứa metadata phân trang chuẩn cho danh sách quản trị.
type PaginationMeta struct {
	Total      int64
	Page       int
	Limit      int
	TotalPages int
}

// UsersOverview thống kê số lượng người dùng theo từng trạng thái.
type UsersOverview struct {
	Total               int64
	Active              int64
	PendingVerification int64
	Suspended           int64
	Locked              int64
}

// GroupsOverview thống kê tổng số nhóm trên hệ thống.
type GroupsOverview struct {
	Total int64
}

// BillsOverview thống kê hóa đơn theo trạng thái finalized và draft.
type BillsOverview struct {
	TotalFinalized int64
	TotalDraft     int64
}

// DebtsOverview thống kê số lượng khoản nợ theo trạng thái.
type DebtsOverview struct {
	Awaiting            int64
	PendingConfirmation int64
	StalledConfirmation int64
	Rejected            int64
	Settled             int64
}

// MediaCleanupOverview thống kê hàng đợi dọn dẹp file rác.
type MediaCleanupOverview struct {
	PendingJobsCount int64
}

// OCRJobsOverview thống kê tiến độ xử lý tác vụ OCR.
type OCRJobsOverview struct {
	Queued     int64
	Processing int64
	Succeeded  int64
	Failed     int64
}

// RuntimeOverview chứa số liệu tài nguyên tiến trình ứng dụng.
type RuntimeOverview struct {
	GoroutinesCount  int
	AllocMemoryBytes uint64
	UptimeSeconds    int64
}

// SystemOverview tổng hợp toàn bộ số liệu thống kê quản trị hệ thống.
type SystemOverview struct {
	Users        UsersOverview
	Groups       GroupsOverview
	Bills        BillsOverview
	Debts        DebtsOverview
	MediaCleanup MediaCleanupOverview
	OCRJobs      OCRJobsOverview
	Runtime      RuntimeOverview
}
