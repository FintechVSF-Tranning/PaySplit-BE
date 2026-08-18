package usecase

import (
	"context"
	"math"
	"strings"

	"github.com/google/uuid"

	"paysplit-backend/internal/modules/admin/domain"
	"paysplit-backend/internal/modules/admin/repository"
)

// ListAccountsInput chứa các tham số truy vấn phân trang và tìm kiếm danh sách người dùng.
type ListAccountsInput struct {
	Page      int
	Limit     int
	Search    string
	Status    *string
	Role      *string
	SortBy    string
	SortOrder string
}

// ListAccountsOutput chứa danh sách kết quả người dùng kèm metadata phân trang.
type ListAccountsOutput struct {
	Items      []domain.AccountSummary
	Pagination domain.PaginationMeta
}

// UpdateAccountStatusInput chứa thông tin thay đổi trạng thái tài khoản.
type UpdateAccountStatusInput struct {
	TargetUserID string
	AdminID      string
	Status       string
	Reason       string
}

// Service quản lý nghiệp vụ quản trị tài khoản và giám sát hệ thống.
type Service struct {
	repo repository.Repository
}

// NewService khởi tạo usecase service cho module Admin.
func NewService(repo repository.Repository) *Service {
	if repo == nil {
		panic("admin service repository must not be nil")
	}
	return &Service{repo: repo}
}

// ListAccounts xử lý tìm kiếm, lọc và phân trang danh sách tài khoản theo đúng quy chuẩn AC-1.
func (s *Service) ListAccounts(ctx context.Context, input ListAccountsInput) (*ListAccountsOutput, error) {
	page := input.Page
	if page == 0 {
		page = 1
	}
	if page < 0 {
		return nil, domain.ErrInvalidInput
	}

	limit := input.Limit
	if limit == 0 {
		limit = 20
	}
	if limit < 0 {
		return nil, domain.ErrInvalidInput
	}
	// AC-1: Khi limit vượt quá 100 thì tự động clamp về 100
	if limit > 100 {
		limit = 100
	}

	if input.Status != nil {
		st := strings.TrimSpace(*input.Status)
		if st != "" && st != "pending_verification" && st != "active" && st != "suspended" && st != "locked" {
			return nil, domain.ErrInvalidInput
		}
	}

	if input.Role != nil {
		r := strings.TrimSpace(*input.Role)
		if r != "" && r != "user" && r != "admin" {
			return nil, domain.ErrInvalidInput
		}
	}

	sortBy := strings.ToLower(strings.TrimSpace(input.SortBy))
	if sortBy == "" {
		sortBy = "created_at"
	}
	if sortBy != "created_at" && sortBy != "display_name" && sortBy != "email" {
		return nil, domain.ErrInvalidInput
	}

	sortOrder := strings.ToLower(strings.TrimSpace(input.SortOrder))
	if sortOrder == "" {
		sortOrder = "desc"
	}
	if sortOrder != "asc" && sortOrder != "desc" {
		return nil, domain.ErrInvalidInput
	}

	offset := (page - 1) * limit

	items, total, err := s.repo.ListAccounts(ctx, repository.ListAccountsFilter{
		Search:    strings.TrimSpace(input.Search),
		Status:    input.Status,
		Role:      input.Role,
		SortBy:    sortBy,
		SortOrder: sortOrder,
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		return nil, err
	}

	totalPages := 0
	if total > 0 {
		totalPages = int(math.Ceil(float64(total) / float64(limit)))
	}

	return &ListAccountsOutput{
		Items: items,
		Pagination: domain.PaginationMeta{
			Total:      total,
			Page:       page,
			Limit:      limit,
			TotalPages: totalPages,
		},
	}, nil
}

// GetAccountDetail truy xuất chi tiết tài khoản theo ID kèm thông tin bảo mật đã ẩn số ngân hàng theo AC-2.
func (s *Service) GetAccountDetail(ctx context.Context, userID string) (*domain.AccountDetail, error) {
	if _, err := uuid.Parse(userID); err != nil {
		return nil, domain.ErrInvalidInput
	}
	return s.repo.GetAccountDetail(ctx, userID)
}

// UpdateAccountStatus thực hiện thay đổi trạng thái, thu hồi phiên và ghi log kiểm toán theo AC-3 & AC-4.
func (s *Service) UpdateAccountStatus(ctx context.Context, input UpdateAccountStatusInput) (*domain.SafeUser, *domain.WarningMeta, error) {
	if _, err := uuid.Parse(input.TargetUserID); err != nil {
		return nil, nil, domain.ErrInvalidInput
	}
	if _, err := uuid.Parse(input.AdminID); err != nil {
		return nil, nil, domain.ErrInvalidInput
	}

	status := strings.ToLower(strings.TrimSpace(input.Status))
	if status != "active" && status != "suspended" && status != "locked" {
		return nil, nil, domain.ErrInvalidInput
	}

	reason := strings.TrimSpace(input.Reason)
	if (status == "suspended" || status == "locked") && reason == "" {
		return nil, nil, domain.ErrReasonRequired
	}

	return s.repo.UpdateAccountStatusWithRevocation(ctx, repository.UpdateStatusInput{
		TargetUserID: input.TargetUserID,
		AdminID:      input.AdminID,
		NewStatus:    status,
		Reason:       reason,
	})
}

// GetSystemOverview thu thập toàn bộ số liệu thống kê quản trị và tài nguyên theo AC-7.
func (s *Service) GetSystemOverview(ctx context.Context) (*domain.SystemOverview, error) {
	return s.repo.GetSystemOverview(ctx)
}
