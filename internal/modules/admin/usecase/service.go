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
// SessionRevoker là cổng tới kho phiên Redis. Khai báo tại đây thay vì import
// auth/usecase để hai module không phụ thuộc nhau; bootstrap tiêm cùng một
// implementation cho cả hai.
type SessionRevoker interface {
	RevokeUserSIDs(ctx context.Context, userID string, sids []string) (bool, error)
	// RevokeUser thu hồi vô điều kiện, không khớp SID. Đường khóa tài khoản phải
	// dùng hàm này: danh sách SID đến từ Postgres nên nó chỉ chạm được những phiên
	// mà Postgres còn tin là đang sống, trong khi Redis mới là nguồn phán quyết.
	RevokeUser(ctx context.Context, userID string) (bool, error)
	// HasLiveSession trả lời "user này còn phiên sống không". Đây là câu hỏi đọc,
	// nhưng nó vẫn thuộc cổng này vì chỉ Redis trả lời đúng được: bảng `sessions`
	// nay là audit và `expires_at` của nó mang trần tuyệt đối, nên đếm ở đó sẽ báo
	// một phiên đã chết vì không hoạt động là vẫn đang sống.
	HasLiveSession(ctx context.Context, userID string) (bool, error)
}

type Service struct {
	repo     repository.Repository
	sessions SessionRevoker
}

// NewService khởi tạo usecase service cho module Admin.
func NewService(repo repository.Repository, sessions SessionRevoker) *Service {
	if repo == nil || sessions == nil {
		panic("admin service dependencies must not be nil")
	}
	return &Service{repo: repo, sessions: sessions}
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
	detail, err := s.repo.GetAccountDetail(ctx, userID)
	if err != nil {
		return nil, err
	}
	// Repository trả con số đếm từ Postgres, và con số đó sai kể từ khi Redis thành
	// nguồn phán quyết: hàng audit mang `expires_at = now + trần tuyệt đối` (30
	// ngày) trong khi phiên thật chết sau TTL trượt (7 ngày) nếu người dùng ngừng
	// mở app. Hỏi lại Redis rồi ghi đè.
	//
	// Mỗi user có tối đa một phiên sống nên con số luôn là 0 hoặc 1.
	//
	// Lỗi Redis được trả thẳng ra ngoài thay vì lặng lẽ giữ số cũ: chính request
	// này đã phải đi qua middleware xác thực vốn cũng đọc Redis, nên Redis chết thì
	// quản trị viên không vào được tới đây. Nuốt lỗi ở đây chỉ đổi một lỗi nhìn
	// thấy được thành một con số nói dối.
	live, err := s.sessions.HasLiveSession(ctx, userID)
	if err != nil {
		return nil, err
	}
	detail.ActiveSessionsCount = 0
	if live {
		detail.ActiveSessionsCount = 1
	}
	return detail, nil
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
	// admin_audit_logs.reason is NOT NULL and CHECK (reason <> ''): every audit entry needs a
	// reason even though AC-3 only requires the caller to supply one for suspend/lock.
	if reason == "" {
		reason = "Reactivated by admin"
	}

	// Postgres TRƯỚC, Redis SAU: không được để một tài khoản bị khoá trên Redis mà
	// thiếu bản ghi admin_audit_logs bền vững, và transaction vẫn có thể hỏng ở
	// bước ghi log hoặc cập nhật trạng thái.
	user, warning, _, err := s.repo.UpdateAccountStatusWithRevocation(ctx, repository.UpdateStatusInput{
		TargetUserID: input.TargetUserID,
		AdminID:      input.AdminID,
		NewStatus:    status,
		Reason:       reason,
	})
	if err != nil {
		return nil, nil, err
	}
	if status == "suspended" || status == "locked" {
		// Đây là nơi việc khoá tài khoản thực sự có hiệu lực. Middleware không còn
		// đọc users.status nữa, nên nếu lệnh này lỡ thì người bị khoá vẫn dùng app
		// bình thường. Trả lỗi ra ngoài để admin thấy — trạng thái Postgres đã bền
		// và job session_redis_purge đã nằm trong hàng đợi, nên nó sẽ hội tụ.
		//
		// Thu hồi VÔ ĐIỀU KIỆN, không gác theo len(revokedSIDs). Danh sách đó đến
		// từ `UPDATE sessions ... WHERE revoked_at IS NULL RETURNING id`, nên nó
		// rỗng ngay khi hàng audit đã revoked — kể cả lúc key Redis vẫn còn sống.
		// Gác theo nó biến lệnh khoá lần hai thành lệnh rỗng, và đó chính là lần
		// mà quản trị viên bấm khoá vì lần đầu đã lỡ.
		if _, revokeErr := s.sessions.RevokeUser(ctx, input.TargetUserID); revokeErr != nil {
			return nil, nil, revokeErr
		}
	}
	return user, warning, nil
}

// GetSystemOverview thu thập toàn bộ số liệu thống kê quản trị và tài nguyên theo AC-7.
func (s *Service) GetSystemOverview(ctx context.Context) (*domain.SystemOverview, error) {
	return s.repo.GetSystemOverview(ctx)
}
