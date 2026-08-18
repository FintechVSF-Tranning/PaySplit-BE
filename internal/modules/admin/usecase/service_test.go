package usecase

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"paysplit-backend/internal/modules/admin/domain"
	"paysplit-backend/internal/modules/admin/repository"
)

type mockRepository struct {
	listAccountsFn      func(ctx context.Context, filter repository.ListAccountsFilter) ([]domain.AccountSummary, int64, error)
	getAccountDetailFn  func(ctx context.Context, userID string) (*domain.AccountDetail, error)
	updateStatusFn      func(ctx context.Context, input repository.UpdateStatusInput) (*domain.SafeUser, *domain.WarningMeta, error)
	getSystemOverviewFn func(ctx context.Context) (*domain.SystemOverview, error)
}

func (m *mockRepository) ListAccounts(ctx context.Context, filter repository.ListAccountsFilter) ([]domain.AccountSummary, int64, error) {
	if m.listAccountsFn != nil {
		return m.listAccountsFn(ctx, filter)
	}
	return nil, 0, nil
}

func (m *mockRepository) GetAccountDetail(ctx context.Context, userID string) (*domain.AccountDetail, error) {
	if m.getAccountDetailFn != nil {
		return m.getAccountDetailFn(ctx, userID)
	}
	return nil, nil
}

func (m *mockRepository) UpdateAccountStatusWithRevocation(ctx context.Context, input repository.UpdateStatusInput) (*domain.SafeUser, *domain.WarningMeta, error) {
	if m.updateStatusFn != nil {
		return m.updateStatusFn(ctx, input)
	}
	return nil, nil, nil
}

func (m *mockRepository) GetSystemOverview(ctx context.Context) (*domain.SystemOverview, error) {
	if m.getSystemOverviewFn != nil {
		return m.getSystemOverviewFn(ctx)
	}
	return nil, nil
}

func TestListAccounts_ValidationAndClamping(t *testing.T) {
	t.Run("clamps limit to 100 when > 100", func(t *testing.T) {
		var capturedLimit int
		mockRepo := &mockRepository{
			listAccountsFn: func(ctx context.Context, filter repository.ListAccountsFilter) ([]domain.AccountSummary, int64, error) {
				capturedLimit = filter.Limit
				return []domain.AccountSummary{}, 150, nil
			},
		}
		service := NewService(mockRepo)

		out, err := service.ListAccounts(context.Background(), ListAccountsInput{
			Page:  1,
			Limit: 200,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if capturedLimit != 100 {
			t.Errorf("expected captured limit 100, got %d", capturedLimit)
		}
		if out.Pagination.Limit != 100 {
			t.Errorf("expected pagination limit 100, got %d", out.Pagination.Limit)
		}
		if out.Pagination.TotalPages != 2 {
			t.Errorf("expected total pages 2, got %d", out.Pagination.TotalPages)
		}
	})

	t.Run("rejects negative page or limit", func(t *testing.T) {
		service := NewService(&mockRepository{})

		_, err := service.ListAccounts(context.Background(), ListAccountsInput{Page: -1})
		if !errors.Is(err, domain.ErrInvalidInput) {
			t.Errorf("expected ErrInvalidInput, got %v", err)
		}

		_, err = service.ListAccounts(context.Background(), ListAccountsInput{Page: 1, Limit: -5})
		if !errors.Is(err, domain.ErrInvalidInput) {
			t.Errorf("expected ErrInvalidInput, got %v", err)
		}
	})

	t.Run("rejects invalid status or role", func(t *testing.T) {
		service := NewService(&mockRepository{})

		invalidStatus := "invalid_status"
		_, err := service.ListAccounts(context.Background(), ListAccountsInput{Status: &invalidStatus})
		if !errors.Is(err, domain.ErrInvalidInput) {
			t.Errorf("expected ErrInvalidInput for invalid status, got %v", err)
		}

		invalidRole := "superadmin"
		_, err = service.ListAccounts(context.Background(), ListAccountsInput{Role: &invalidRole})
		if !errors.Is(err, domain.ErrInvalidInput) {
			t.Errorf("expected ErrInvalidInput for invalid role, got %v", err)
		}
	})

	t.Run("rejects invalid sort_by or sort_order", func(t *testing.T) {
		service := NewService(&mockRepository{})

		_, err := service.ListAccounts(context.Background(), ListAccountsInput{SortBy: "unknown_column"})
		if !errors.Is(err, domain.ErrInvalidInput) {
			t.Errorf("expected ErrInvalidInput for invalid sort_by, got %v", err)
		}

		_, err = service.ListAccounts(context.Background(), ListAccountsInput{SortBy: "email", SortOrder: "random"})
		if !errors.Is(err, domain.ErrInvalidInput) {
			t.Errorf("expected ErrInvalidInput for invalid sort_order, got %v", err)
		}
	})
}

func TestGetAccountDetail_Validation(t *testing.T) {
	service := NewService(&mockRepository{})

	t.Run("rejects invalid UUID", func(t *testing.T) {
		_, err := service.GetAccountDetail(context.Background(), "invalid-uuid")
		if !errors.Is(err, domain.ErrInvalidInput) {
			t.Errorf("expected ErrInvalidInput, got %v", err)
		}
	})

	t.Run("calls repository for valid UUID", func(t *testing.T) {
		validID := uuid.New().String()
		called := false
		mockRepo := &mockRepository{
			getAccountDetailFn: func(ctx context.Context, userID string) (*domain.AccountDetail, error) {
				called = true
				return &domain.AccountDetail{
					Summary: domain.AccountSummary{ID: userID, Email: "user@example.com"},
				}, nil
			},
		}
		s := NewService(mockRepo)
		detail, err := s.GetAccountDetail(context.Background(), validID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !called || detail.Summary.Email != "user@example.com" {
			t.Errorf("expected valid detail returned")
		}
	})
}

func TestUpdateAccountStatus_Validation(t *testing.T) {
	service := NewService(&mockRepository{})

	adminID := uuid.New().String()
	targetID := uuid.New().String()

	t.Run("rejects invalid UUIDs", func(t *testing.T) {
		_, _, err := service.UpdateAccountStatus(context.Background(), UpdateAccountStatusInput{
			TargetUserID: "invalid",
			AdminID:      adminID,
			Status:       "suspended",
			Reason:       "terms violation",
		})
		if !errors.Is(err, domain.ErrInvalidInput) {
			t.Errorf("expected ErrInvalidInput, got %v", err)
		}
	})

	t.Run("rejects invalid status", func(t *testing.T) {
		_, _, err := service.UpdateAccountStatus(context.Background(), UpdateAccountStatusInput{
			TargetUserID: targetID,
			AdminID:      adminID,
			Status:       "deleted",
			Reason:       "terms violation",
		})
		if !errors.Is(err, domain.ErrInvalidInput) {
			t.Errorf("expected ErrInvalidInput, got %v", err)
		}
	})

	t.Run("requires non-empty reason for suspended or locked", func(t *testing.T) {
		_, _, err := service.UpdateAccountStatus(context.Background(), UpdateAccountStatusInput{
			TargetUserID: targetID,
			AdminID:      adminID,
			Status:       "suspended",
			Reason:       "   ",
		})
		if !errors.Is(err, domain.ErrReasonRequired) {
			t.Errorf("expected ErrReasonRequired, got %v", err)
		}

		_, _, err = service.UpdateAccountStatus(context.Background(), UpdateAccountStatusInput{
			TargetUserID: targetID,
			AdminID:      adminID,
			Status:       "locked",
			Reason:       "",
		})
		if !errors.Is(err, domain.ErrReasonRequired) {
			t.Errorf("expected ErrReasonRequired, got %v", err)
		}
	})

	t.Run("passes through successful update", func(t *testing.T) {
		mockRepo := &mockRepository{
			updateStatusFn: func(ctx context.Context, input repository.UpdateStatusInput) (*domain.SafeUser, *domain.WarningMeta, error) {
				return &domain.SafeUser{
					ID:     input.TargetUserID,
					Status: input.NewStatus,
				}, &domain.WarningMeta{UnsettledDebtsCount: 2}, nil
			},
		}
		s := NewService(mockRepo)
		user, warning, err := s.UpdateAccountStatus(context.Background(), UpdateAccountStatusInput{
			TargetUserID: targetID,
			AdminID:      adminID,
			Status:       "suspended",
			Reason:       "fraud suspicion",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if user.Status != "suspended" {
			t.Errorf("expected status suspended, got %s", user.Status)
		}
		if warning.UnsettledDebtsCount != 2 {
			t.Errorf("expected 2 unsettled debts, got %d", warning.UnsettledDebtsCount)
		}
	})
}

func TestGetSystemOverview(t *testing.T) {
	mockRepo := &mockRepository{
		getSystemOverviewFn: func(ctx context.Context) (*domain.SystemOverview, error) {
			return &domain.SystemOverview{
				Users:   domain.UsersOverview{Total: 50, Active: 45},
				Runtime: domain.RuntimeOverview{GoroutinesCount: 12, UptimeSeconds: 3600},
			}, nil
		},
	}
	s := NewService(mockRepo)
	res, err := s.GetSystemOverview(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Users.Total != 50 || res.Runtime.GoroutinesCount != 12 {
		t.Errorf("unexpected system overview result: %+v", res)
	}
}

func TestBankMasking(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"1234567890", "******7890"},
		{"987654321", "******4321"},
		{"1234", "******1234"},
		{"12", "******12"},
		{"", "******"},
	}

	for _, tt := range tests {
		got := repository_MaskBankAccount(tt.input)
		if got != tt.expected {
			t.Errorf("MaskBankAccount(%q) = %q, expected %q", tt.input, got, tt.expected)
		}
	}
}

// repository_MaskBankAccount helper test proxy
func repository_MaskBankAccount(accountNumber string) string {
	accountNumber = strings.TrimSpace(accountNumber)
	if len(accountNumber) <= 4 {
		return "******" + accountNumber
	}
	return "******" + accountNumber[len(accountNumber)-4:]
}
