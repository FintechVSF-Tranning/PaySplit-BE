package repository

import (
	"context"

	"paysplit-backend/internal/modules/admin/domain"
)

// ListAccountsFilter chứa các tiêu chí tìm kiếm, lọc và phân trang danh sách tài khoản.
type ListAccountsFilter struct {
	Search    string
	Status    *string
	Role      *string
	SortBy    string
	SortOrder string
	Limit     int
	Offset    int
}

// UpdateStatusInput chứa dữ liệu để cập nhật trạng thái người dùng kèm hủy phiên và ghi log.
type UpdateStatusInput struct {
	TargetUserID string
	AdminID      string
	NewStatus    string
	Reason       string
}

// Repository định nghĩa giao diện truy xuất và biến đổi dữ liệu của module Admin.
type Repository interface {
	ListAccounts(ctx context.Context, filter ListAccountsFilter) ([]domain.AccountSummary, int64, error)
	GetAccountDetail(ctx context.Context, userID string) (*domain.AccountDetail, error)
	UpdateAccountStatusWithRevocation(ctx context.Context, input UpdateStatusInput) (*domain.SafeUser, *domain.WarningMeta, error)
	GetSystemOverview(ctx context.Context) (*domain.SystemOverview, error)
}
