package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"paysplit-backend/internal/modules/admin/domain"
	"paysplit-backend/internal/modules/admin/repository"
	"paysplit-backend/internal/modules/admin/usecase"
	authmw "paysplit-backend/internal/transport/http/middleware"
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

func setupTestRouter(repo *mockRepository) (http.Handler, *usecase.Service) {
	svc := usecase.NewService(repo)
	handler := NewHandler(svc, func(k string) string { return "https://res.cloudinary.com/" + k })

	r := chi.NewRouter()
	r.Route("/api/v1/admin", func(admin chi.Router) {
		admin.Get("/accounts", handler.ListAccounts)
		admin.Get("/accounts/{id}", handler.GetAccountDetail)
		admin.Put("/accounts/{id}/status", handler.UpdateAccountStatus)
		admin.Get("/system/overview", handler.GetSystemOverview)
	})
	return r, svc
}

func TestHandler_ListAccounts(t *testing.T) {
	t.Run("returns 200 with paginated accounts", func(t *testing.T) {
		now := time.Now()
		mockRepo := &mockRepository{
			listAccountsFn: func(ctx context.Context, filter repository.ListAccountsFilter) ([]domain.AccountSummary, int64, error) {
				return []domain.AccountSummary{
					{
						ID:          uuid.New().String(),
						Email:       "user@example.com",
						DisplayName: "Test User",
						Role:        "user",
						Status:      "active",
						CreatedAt:   now,
						UpdatedAt:   now,
					},
				}, 1, nil
			},
		}

		router, _ := setupTestRouter(mockRepo)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts?page=1&limit=20", nil)
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var resp struct {
			Items      []accountSummaryResponse `json:"items"`
			Pagination paginationResponse       `json:"pagination"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if len(resp.Items) != 1 || resp.Pagination.Total != 1 {
			t.Errorf("unexpected items or pagination: %+v", resp)
		}
	})

	t.Run("returns 400 for invalid query parameters", func(t *testing.T) {
		router, _ := setupTestRouter(&mockRepository{})
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts?page=abc", nil)
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d", rec.Code)
		}
	})
}

func TestHandler_GetAccountDetail(t *testing.T) {
	targetID := uuid.New().String()

	t.Run("returns 200 with full account detail", func(t *testing.T) {
		maskedBank := "******1234"
		mockRepo := &mockRepository{
			getAccountDetailFn: func(ctx context.Context, userID string) (*domain.AccountDetail, error) {
				return &domain.AccountDetail{
					Summary: domain.AccountSummary{
						ID:          userID,
						Email:       "target@example.com",
						DisplayName: "Target User",
						Role:        "user",
						Status:      "active",
						CreatedAt:   time.Now(),
						UpdatedAt:   time.Now(),
					},
					Bank: domain.BankSnapshot{
						BankAccountNumber: &maskedBank,
					},
					ActiveSessionsCount: 1,
					Groups: []domain.GroupMembershipSummary{
						{GroupID: uuid.New().String(), GroupName: "Trip Group", Role: "member", Status: "active", JoinedAt: time.Now()},
					},
					Financials: domain.FinancialSummary{
						OutstandingDebtsCount: 1,
						TotalDebtAmountVND:    50000,
					},
				}, nil
			},
		}

		router, _ := setupTestRouter(mockRepo)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/"+targetID, nil)
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var resp accountDetailResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if resp.ID != targetID || *resp.Bank.BankAccountNumber != "******1234" {
			t.Errorf("unexpected account detail response: %+v", resp)
		}
	})

	t.Run("returns 400 for malformed UUID", func(t *testing.T) {
		router, _ := setupTestRouter(&mockRepository{})
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/invalid-uuid", nil)
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d", rec.Code)
		}
	})

	t.Run("returns 404 if account not found", func(t *testing.T) {
		mockRepo := &mockRepository{
			getAccountDetailFn: func(ctx context.Context, userID string) (*domain.AccountDetail, error) {
				return nil, domain.ErrAccountNotFound
			},
		}
		router, _ := setupTestRouter(mockRepo)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/"+targetID, nil)
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected status 404, got %d", rec.Code)
		}
	})
}

func TestHandler_UpdateAccountStatus(t *testing.T) {
	adminID := uuid.New().String()
	targetID := uuid.New().String()

	t.Run("returns 200 on successful status change", func(t *testing.T) {
		mockRepo := &mockRepository{
			updateStatusFn: func(ctx context.Context, input repository.UpdateStatusInput) (*domain.SafeUser, *domain.WarningMeta, error) {
				return &domain.SafeUser{
					ID:        input.TargetUserID,
					Email:     "user@example.com",
					Role:      "user",
					Status:    input.NewStatus,
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
				}, &domain.WarningMeta{UnsettledDebtsCount: 1}, nil
			},
		}

		router, _ := setupTestRouter(mockRepo)
		body, _ := json.Marshal(updateStatusRequest{Status: "suspended", Reason: "Policy breach"})
		req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/accounts/"+targetID+"/status", bytes.NewReader(body))
		ctx := authmw.WithAuthContext(req.Context(), adminID, uuid.New().String(), "admin")
		req = req.WithContext(ctx)

		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var resp updateStatusResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if resp.Account.Status != "suspended" || resp.Warning.UnsettledDebtsCount != 1 {
			t.Errorf("unexpected status response: %+v", resp)
		}
	})

	t.Run("returns 403 CANNOT_MODIFY_SELF", func(t *testing.T) {
		mockRepo := &mockRepository{
			updateStatusFn: func(ctx context.Context, input repository.UpdateStatusInput) (*domain.SafeUser, *domain.WarningMeta, error) {
				return nil, nil, domain.ErrCannotModifySelf
			},
		}

		router, _ := setupTestRouter(mockRepo)
		body, _ := json.Marshal(updateStatusRequest{Status: "locked", Reason: "Self lock test"})
		req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/accounts/"+adminID+"/status", bytes.NewReader(body))
		ctx := authmw.WithAuthContext(req.Context(), adminID, uuid.New().String(), "admin")
		req = req.WithContext(ctx)

		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected status 403, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("returns 403 CANNOT_MODIFY_ADMIN", func(t *testing.T) {
		mockRepo := &mockRepository{
			updateStatusFn: func(ctx context.Context, input repository.UpdateStatusInput) (*domain.SafeUser, *domain.WarningMeta, error) {
				return nil, nil, domain.ErrCannotModifyAdmin
			},
		}

		router, _ := setupTestRouter(mockRepo)
		body, _ := json.Marshal(updateStatusRequest{Status: "locked", Reason: "Lock admin test"})
		req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/accounts/"+targetID+"/status", bytes.NewReader(body))
		ctx := authmw.WithAuthContext(req.Context(), adminID, uuid.New().String(), "admin")
		req = req.WithContext(ctx)

		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected status 403, got %d: %s", rec.Code, rec.Body.String())
		}
	})
}

func TestHandler_GetSystemOverview(t *testing.T) {
	mockRepo := &mockRepository{
		getSystemOverviewFn: func(ctx context.Context) (*domain.SystemOverview, error) {
			return &domain.SystemOverview{
				Users: domain.UsersOverview{Total: 100, Active: 90, Suspended: 5},
				Bills: domain.BillsOverview{TotalFinalized: 25, TotalDraft: 5},
			}, nil
		},
	}

	router, _ := setupTestRouter(mockRepo)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/system/overview", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp systemOverviewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode overview response: %v", err)
	}
	if resp.Users.Total != 100 || resp.Bills.TotalFinalized != 25 {
		t.Errorf("unexpected system overview response: %+v", resp)
	}
}
