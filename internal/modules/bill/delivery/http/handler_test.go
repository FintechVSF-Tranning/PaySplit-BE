package http_test

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

	billhttp "paysplit-backend/internal/modules/bill/delivery/http"
	"paysplit-backend/internal/modules/bill/domain"
	"paysplit-backend/internal/modules/bill/repository"
	"paysplit-backend/internal/modules/bill/usecase"
	authmw "paysplit-backend/internal/transport/http/middleware"
)

type mockHandlerRepo struct {
	repository.Repository
	member *repository.GroupMember
	bill   *domain.Bill
}

func (m *mockHandlerRepo) GetGroupMember(ctx context.Context, groupID, userID uuid.UUID) (*repository.GroupMember, error) {
	if m.member != nil {
		return m.member, nil
	}
	return nil, domain.ErrInvalidInput
}

func (m *mockHandlerRepo) CreateBill(ctx context.Context, params repository.CreateBillParams) (*domain.Bill, error) {
	return params.Bill, nil
}

func (m *mockHandlerRepo) GetBillByID(ctx context.Context, id, groupID uuid.UUID) (*domain.Bill, error) {
	if m.bill != nil {
		return m.bill, nil
	}
	return nil, domain.ErrBillNotFound
}

func (m *mockHandlerRepo) GetBillOnlyByID(ctx context.Context, id uuid.UUID) (*domain.Bill, error) {
	if m.bill != nil {
		return m.bill, nil
	}
	return nil, domain.ErrBillNotFound
}

func (m *mockHandlerRepo) GetGroupMemberUser(ctx context.Context, memberID, groupID uuid.UUID) (*repository.GroupMemberWithUser, error) {
	bankCode := "970422"
	bankAccount := "0123456789"
	bankHolder := "NGUYEN VAN A"
	return &repository.GroupMemberWithUser{
		ID:                    memberID,
		GroupID:               groupID,
		Role:                  "captain",
		Status:                "active",
		DefaultBankCode:       &bankCode,
		DefaultBankAccountNum: &bankAccount,
		DefaultBankHolder:     &bankHolder,
	}, nil
}

func (m *mockHandlerRepo) ListBillsByGroup(ctx context.Context, groupID uuid.UUID, limit, offset int32) ([]*domain.Bill, error) {
	if m.bill != nil {
		return []*domain.Bill{m.bill}, nil
	}
	return []*domain.Bill{}, nil
}

func (m *mockHandlerRepo) ListBillsByGroupCursor(ctx context.Context, params repository.ListBillsCursorParams) (*repository.ListBillsCursorResult, error) {
	bills := []*domain.Bill{}
	if m.bill != nil {
		bills = append(bills, m.bill)
	}
	return &repository.ListBillsCursorResult{
		Bills: bills,
	}, nil
}

func (m *mockHandlerRepo) ReviewBill(ctx context.Context, id, groupID uuid.UUID, expectedVersion int32, reviewerMemberID uuid.UUID) (*domain.Bill, error) {
	if m.bill != nil {
		m.bill.Status = domain.BillStatusReviewed
		m.bill.Version = expectedVersion + 1
		now := time.Now()
		m.bill.ReviewedAt = &now
		m.bill.ReviewedByMemberID = &reviewerMemberID
		return m.bill, nil
	}
	return nil, domain.ErrBillNotFound
}

func (m *mockHandlerRepo) FinalizeBill(ctx context.Context, params repository.FinalizeBillParams) (*domain.Bill, error) {
	if m.bill != nil {
		m.bill.Status = domain.BillStatusFinalized
		m.bill.Shares = params.Shares
		return m.bill, nil
	}
	return nil, domain.ErrBillNotFound
}

func (m *mockHandlerRepo) VoidBill(ctx context.Context, params repository.VoidBillParams) (*domain.Bill, error) {
	if m.bill != nil {
		m.bill.Status = domain.BillStatusVoided
		return m.bill, nil
	}
	return nil, domain.ErrBillNotFound
}

func (m *mockHandlerRepo) DeleteDraftBill(ctx context.Context, id, groupID uuid.UUID) error {
	return nil
}

func (m *mockHandlerRepo) EnqueueMediaCleanup(ctx context.Context, prefix, kind string) error {
	return nil
}

type mockHandlerProcessor struct{}

func (m *mockHandlerProcessor) Process(ctx context.Context, input []byte) ([]byte, error) {
	return input, nil
}
func (m *mockHandlerProcessor) IsUnsupported(err error) bool { return false }

type mockHandlerStorage struct{}

func (m *mockHandlerStorage) Upload(ctx context.Context, data []byte, publicID string) (string, error) {
	return publicID, nil
}
func (m *mockHandlerStorage) SignedURL(publicID string, ttl time.Duration) (string, error) {
	return "https://signed.url/" + publicID, nil
}
func (m *mockHandlerStorage) Download(ctx context.Context, publicID string) ([]byte, error) {
	return nil, nil
}
func (m *mockHandlerStorage) Delete(ctx context.Context, publicID string) error { return nil }
func (m *mockHandlerStorage) DeleteByPrefix(ctx context.Context, prefix string) error {
	return nil
}

type mockHandlerOCR struct{}

func (m *mockHandlerOCR) ExtractReceipt(ctx context.Context, imageBytes []byte, mimeType string) (*domain.OCRCandidate, []byte, error) {
	return nil, nil, nil
}

func TestCreateBill_JSON_Success(t *testing.T) {
	groupID := uuid.New()
	userID := uuid.New()

	repo := &mockHandlerRepo{
		member: &repository.GroupMember{
			ID:      uuid.New(),
			GroupID: groupID,
			UserID:  userID,
			Role:    "member",
			Status:  "active",
		},
	}

	service := usecase.NewService(repo, &mockHandlerOCR{}, &mockHandlerStorage{}, &mockHandlerProcessor{}, nil)
	hub := billhttp.NewHub(nil)
	sseHandler := billhttp.NewSSEHandler(hub, repo, 0, 0)
	handler := billhttp.NewHandler(service, sseHandler)

	reqBody := usecase.CreateBillRequest{
		GroupID:  groupID,
		Subtotal: 50000,
		Total:    50000,
	}
	bodyBytes, _ := json.Marshal(reqBody)

	r := chi.NewRouter()
	handler.RegisterRoutes(r, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authmw.WithAuthContext(req.Context(), userID.String(), "s-1", "user")
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})

	req, _ := http.NewRequest(http.MethodPost, "/", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201 Created, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestGetBillDetail_Success(t *testing.T) {
	groupID := uuid.New()
	userID := uuid.New()
	billID := uuid.New()

	repo := &mockHandlerRepo{
		member: &repository.GroupMember{
			ID:      uuid.New(),
			GroupID: groupID,
			UserID:  userID,
			Role:    "member",
			Status:  "active",
		},
		bill: &domain.Bill{
			ID:       billID,
			GroupID:  groupID,
			Status:   domain.BillStatusDraft,
			Subtotal: 50000,
			Total:    50000,
		},
	}

	service := usecase.NewService(repo, &mockHandlerOCR{}, &mockHandlerStorage{}, &mockHandlerProcessor{}, nil)
	handler := billhttp.NewHandler(service, nil)

	r := chi.NewRouter()
	handler.RegisterRoutes(r, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authmw.WithAuthContext(req.Context(), userID.String(), "s-1", "user")
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})

	req, _ := http.NewRequest(http.MethodGet, "/"+billID.String()+"?group_id="+groupID.String(), nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 OK, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestReviewBill_Handler_Success(t *testing.T) {
	// covers: AC-7 (Review endpoint transitions bill)
	groupID := uuid.New()
	userID := uuid.New()
	billID := uuid.New()
	memberID := uuid.New()

	repo := &mockHandlerRepo{
		member: &repository.GroupMember{
			ID:      memberID,
			GroupID: groupID,
			UserID:  userID,
			Role:    "captain",
			Status:  "active",
		},
		bill: &domain.Bill{
			ID:               billID,
			GroupID:          groupID,
			CreditorMemberID: memberID,
			Status:           domain.BillStatusDraft,
			Subtotal:         50000,
			Total:            50000,
			Version:          1,
			Items: []*domain.BillItem{
				{
					ID:        uuid.New(),
					LineTotal: 50000,
					Assignments: []*domain.BillItemAssignment{
						{MemberID: memberID, Weight: "1"},
					},
				},
			},
		},
	}

	service := usecase.NewService(repo, &mockHandlerOCR{}, &mockHandlerStorage{}, &mockHandlerProcessor{}, nil)
	handler := billhttp.NewHandler(service, nil)

	r := chi.NewRouter()
	handler.RegisterRoutes(r, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authmw.WithAuthContext(req.Context(), userID.String(), "s-1", "user")
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})

	bodyBytes, _ := json.Marshal(map[string]int{"version": 1})
	req, _ := http.NewRequest(http.MethodPost, "/"+billID.String()+"/review?group_id="+groupID.String(), bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 OK, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestFinalizeBill_Handler_Success(t *testing.T) {
	// covers: AC-9 (Finalize endpoint creates shares & debts)
	groupID := uuid.New()
	userID := uuid.New()
	billID := uuid.New()
	memberID := uuid.New()

	repo := &mockHandlerRepo{
		member: &repository.GroupMember{
			ID:      memberID,
			GroupID: groupID,
			UserID:  userID,
			Role:    "captain",
			Status:  "active",
		},
		bill: &domain.Bill{
			ID:               billID,
			GroupID:          groupID,
			CreditorMemberID: memberID,
			Status:           domain.BillStatusReviewed,
			Subtotal:         50000,
			Total:            50000,
			Version:          2,
			Items: []*domain.BillItem{
				{
					ID:        uuid.New(),
					LineTotal: 50000,
					Assignments: []*domain.BillItemAssignment{
						{MemberID: memberID, Weight: "1"},
					},
				},
			},
		},
	}

	service := usecase.NewService(repo, &mockHandlerOCR{}, &mockHandlerStorage{}, &mockHandlerProcessor{}, nil)
	handler := billhttp.NewHandler(service, nil)

	r := chi.NewRouter()
	handler.RegisterRoutes(r, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authmw.WithAuthContext(req.Context(), userID.String(), "s-1", "user")
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})

	bodyBytes, _ := json.Marshal(map[string]int{"version": 2})
	req, _ := http.NewRequest(http.MethodPost, "/"+billID.String()+"/finalize?group_id="+groupID.String(), bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 OK, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestVoidBill_Handler_Success(t *testing.T) {
	// covers: AC-11 (Void endpoint cancels bill and debts)
	groupID := uuid.New()
	userID := uuid.New()
	billID := uuid.New()
	memberID := uuid.New()

	repo := &mockHandlerRepo{
		member: &repository.GroupMember{
			ID:      memberID,
			GroupID: groupID,
			UserID:  userID,
			Role:    "captain",
			Status:  "active",
		},
		bill: &domain.Bill{
			ID:               billID,
			GroupID:          groupID,
			CreditorMemberID: memberID,
			Status:           domain.BillStatusFinalized,
			Version:          3,
		},
	}

	service := usecase.NewService(repo, &mockHandlerOCR{}, &mockHandlerStorage{}, &mockHandlerProcessor{}, nil)
	handler := billhttp.NewHandler(service, nil)

	r := chi.NewRouter()
	handler.RegisterRoutes(r, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authmw.WithAuthContext(req.Context(), userID.String(), "s-1", "user")
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})

	bodyBytes, _ := json.Marshal(map[string]interface{}{"version": 3, "reason": "Wrong amount"})
	req, _ := http.NewRequest(http.MethodPost, "/"+billID.String()+"/void?group_id="+groupID.String(), bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 OK, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestDeleteDraftBill_Handler_Success(t *testing.T) {
	// covers: AC-13 (Delete draft endpoint returns 204 No Content)
	groupID := uuid.New()
	userID := uuid.New()
	billID := uuid.New()
	memberID := uuid.New()

	repo := &mockHandlerRepo{
		member: &repository.GroupMember{
			ID:      memberID,
			GroupID: groupID,
			UserID:  userID,
			Role:    "captain",
			Status:  "active",
		},
		bill: &domain.Bill{
			ID:               billID,
			GroupID:          groupID,
			CreditorMemberID: memberID,
			Status:           domain.BillStatusDraft,
		},
	}

	service := usecase.NewService(repo, &mockHandlerOCR{}, &mockHandlerStorage{}, &mockHandlerProcessor{}, nil)
	handler := billhttp.NewHandler(service, nil)

	r := chi.NewRouter()
	handler.RegisterRoutes(r, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authmw.WithAuthContext(req.Context(), userID.String(), "s-1", "user")
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})

	req, _ := http.NewRequest(http.MethodDelete, "/"+billID.String()+"?group_id="+groupID.String(), nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected status 204 No Content, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}
