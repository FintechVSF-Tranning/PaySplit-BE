package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
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
	member      *repository.GroupMember
	bill        *domain.Bill
	errToReturn error // when set, GetBillByID returns this raw error instead of ErrBillNotFound
	idempotency map[string]*repository.IdempotencyRecord
}

func idempotencyMockKey(actorUserID uuid.UUID, operation, keyHash string) string {
	return actorUserID.String() + "|" + operation + "|" + keyHash
}

func (m *mockHandlerRepo) ReserveIdempotencyKey(ctx context.Context, p repository.ReserveIdempotencyParams) (*repository.IdempotencyRecord, error) {
	if m.idempotency == nil {
		m.idempotency = map[string]*repository.IdempotencyRecord{}
	}
	k := idempotencyMockKey(p.ActorUserID, p.Operation, p.KeyHash)
	if existing, ok := m.idempotency[k]; ok {
		return existing, nil
	}
	rec := &repository.IdempotencyRecord{
		ActorUserID: p.ActorUserID, Operation: p.Operation, KeyHash: p.KeyHash,
		CanonicalRequestHash: p.CanonicalRequestHash, OperationID: p.OperationID, State: "in_progress",
	}
	m.idempotency[k] = rec
	return rec, nil
}

func (m *mockHandlerRepo) CompleteIdempotencyKey(ctx context.Context, p repository.CompleteIdempotencyParams) error {
	if rec, ok := m.idempotency[idempotencyMockKey(p.ActorUserID, p.Operation, p.KeyHash)]; ok {
		rec.State = "completed"
		rec.ResponseCode = p.ResponseCode
		rec.ResponseBody = p.ResponseBody
	}
	return nil
}

func (m *mockHandlerRepo) GetIdempotencyKey(ctx context.Context, actorUserID uuid.UUID, operation, keyHash string) (*repository.IdempotencyRecord, error) {
	if rec, ok := m.idempotency[idempotencyMockKey(actorUserID, operation, keyHash)]; ok {
		return rec, nil
	}
	return nil, domain.ErrInvalidInput
}

func (m *mockHandlerRepo) ReleaseIdempotencyKey(ctx context.Context, actorUserID uuid.UUID, operation, keyHash string) error {
	k := idempotencyMockKey(actorUserID, operation, keyHash)
	if rec, ok := m.idempotency[k]; ok && rec.State == "in_progress" {
		delete(m.idempotency, k)
	}
	return nil
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
	if m.errToReturn != nil {
		return nil, m.errToReturn
	}
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

func (m *mockHandlerRepo) ListActiveGroupMembers(ctx context.Context, groupID uuid.UUID) ([]*repository.GroupMember, error) {
	if m.member != nil {
		return []*repository.GroupMember{m.member}, nil
	}
	return []*repository.GroupMember{}, nil
}

func (m *mockHandlerRepo) DeleteDraftBill(ctx context.Context, params repository.DeleteDraftBillParams) error {
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

func TestGetBillDetail_UnmappedRepoError_ReturnsRedactedInternalError(t *testing.T) {
	// covers: AC-14, security model ("raw provider content never appears in API responses or logs")
	groupID := uuid.New()
	userID := uuid.New()
	billID := uuid.New()

	rawErr := errors.New("pgx: connection refused to internal-db.paysplit-prod.svc:5432 (constraint fk_bills_group)")

	repo := &mockHandlerRepo{
		member: &repository.GroupMember{
			ID:      uuid.New(),
			GroupID: groupID,
			UserID:  userID,
			Role:    "member",
			Status:  "active",
		},
		errToReturn: rawErr,
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

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "pgx") || strings.Contains(rec.Body.String(), "internal-db") || strings.Contains(rec.Body.String(), "fk_bills_group") {
		t.Errorf("response body leaked the raw internal error: %s", rec.Body.String())
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

func TestVoidBill_FailedMutation_ReleasesIdempotencyKeyForRetry(t *testing.T) {
	// covers: AC-1, AC-9 (a failed mutation must not wedge its Idempotency-Key in_progress for 24h;
	// a retry with the same key must be able to reach the service again, not get stuck on 409)
	groupID := uuid.New()
	userID := uuid.New()
	billID := uuid.New()
	memberID := uuid.New()

	repo := &mockHandlerRepo{
		member: &repository.GroupMember{
			ID: memberID, GroupID: groupID, UserID: userID, Role: "captain", Status: "active",
		},
		// bill is nil: GetBillByID returns ErrBillNotFound, so VoidBill fails after the idempotency
		// reservation is already made.
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

	makeRequest := func() *httptest.ResponseRecorder {
		req, _ := http.NewRequest(http.MethodPost, "/"+billID.String()+"/void?group_id="+groupID.String(), bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "retry-key-1")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	first := makeRequest()
	if first.Code != http.StatusNotFound {
		t.Fatalf("first attempt: expected status 404 (bill not found), got %d (body: %s)", first.Code, first.Body.String())
	}

	second := makeRequest()
	if second.Code == http.StatusConflict && strings.Contains(second.Body.String(), "IDEMPOTENCY_IN_PROGRESS") {
		t.Fatalf("retry with the same Idempotency-Key is wedged on IDEMPOTENCY_IN_PROGRESS instead of reaching the service again: %s", second.Body.String())
	}
	if second.Code != http.StatusNotFound {
		t.Fatalf("retry: expected the request to reach the service again (404 bill not found), got %d (body: %s)", second.Code, second.Body.String())
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

func TestCreateBill_Multipart_InvalidMetadataJSON_ReturnsBadRequest(t *testing.T) {
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
	handler := billhttp.NewHandler(service, nil)

	r := chi.NewRouter()
	handler.RegisterRoutes(r, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authmw.WithAuthContext(req.Context(), userID.String(), "s-1", "user")
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})

	body := &bytes.Buffer{}
	body.WriteString("--boundary\r\n")
	body.WriteString("Content-Disposition: form-data; name=\"metadata\"\r\n\r\n")
	body.WriteString("{invalid-json\r\n")
	body.WriteString("--boundary--\r\n")

	req, _ := http.NewRequest(http.MethodPost, "/", body)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=boundary")
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 Bad Request, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

func newMultipartCreateBillRequest(t *testing.T, groupID uuid.UUID, images [][]byte) *http.Request {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	metadata, _ := json.Marshal(map[string]any{"group_id": groupID})
	if err := writer.WriteField("metadata", string(metadata)); err != nil {
		t.Fatalf("write metadata field: %v", err)
	}
	for i, img := range images {
		part, err := writer.CreateFormFile("images", "receipt.jpg")
		if err != nil {
			t.Fatalf("create form file %d: %v", i, err)
		}
		if _, err := part.Write(img); err != nil {
			t.Fatalf("write form file %d: %v", i, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req, _ := http.NewRequest(http.MethodPost, "/", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

func TestCreateBill_Multipart_ImageExceedsMaxBytes_ReturnsBadRequest(t *testing.T) {
	// covers: AC-1, security (a file over BILL_IMAGE_MAX_BYTES must be rejected before being read into memory)
	groupID := uuid.New()
	userID := uuid.New()

	repo := &mockHandlerRepo{
		member: &repository.GroupMember{ID: uuid.New(), GroupID: groupID, UserID: userID, Role: "member", Status: "active"},
	}
	service := usecase.NewService(repo, &mockHandlerOCR{}, &mockHandlerStorage{}, &mockHandlerProcessor{}, nil)
	handler := billhttp.NewHandler(service, nil)
	handler.SetImageLimits(10, 5) // 10 bytes max per image, well under any real receipt

	r := chi.NewRouter()
	handler.RegisterRoutes(r, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authmw.WithAuthContext(req.Context(), userID.String(), "s-1", "user")
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})

	req := newMultipartCreateBillRequest(t, groupID, [][]byte{bytes.Repeat([]byte("x"), 100)}) // 100 bytes > 10 byte limit
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 Bad Request, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "INVALID_IMAGE") {
		t.Errorf("expected INVALID_IMAGE error code, got body: %s", rec.Body.String())
	}
}

func TestCreateBill_Multipart_TooManyImages_ReturnsBadRequest(t *testing.T) {
	// covers: AC-1 (more than BILL_IMAGE_MAX_COUNT images must be rejected)
	groupID := uuid.New()
	userID := uuid.New()

	repo := &mockHandlerRepo{
		member: &repository.GroupMember{ID: uuid.New(), GroupID: groupID, UserID: userID, Role: "member", Status: "active"},
	}
	service := usecase.NewService(repo, &mockHandlerOCR{}, &mockHandlerStorage{}, &mockHandlerProcessor{}, nil)
	handler := billhttp.NewHandler(service, nil)
	handler.SetImageLimits(10*1024*1024, 2) // at most 2 images

	r := chi.NewRouter()
	handler.RegisterRoutes(r, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authmw.WithAuthContext(req.Context(), userID.String(), "s-1", "user")
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})

	req := newMultipartCreateBillRequest(t, groupID, [][]byte{[]byte("a"), []byte("b"), []byte("c")}) // 3 images > limit of 2
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 Bad Request, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "TOO_MANY_IMAGES") {
		t.Errorf("expected TOO_MANY_IMAGES error code, got body: %s", rec.Body.String())
	}
}

func TestCreateBill_Multipart_BodyExceedsMaxBytesReader_ReturnsPayloadTooLarge(t *testing.T) {
	// covers: AC-1, security (the request body itself must be bounded before ParseMultipartForm
	// reads it into memory/disk, not just the per-file size check that runs afterward)
	groupID := uuid.New()
	userID := uuid.New()

	repo := &mockHandlerRepo{
		member: &repository.GroupMember{ID: uuid.New(), GroupID: groupID, UserID: userID, Role: "member", Status: "active"},
	}
	service := usecase.NewService(repo, &mockHandlerOCR{}, &mockHandlerStorage{}, &mockHandlerProcessor{}, nil)
	handler := billhttp.NewHandler(service, nil)
	handler.SetImageLimits(100, 1) // maxBodyBytes = 100 + 1MB slack; send well over that

	r := chi.NewRouter()
	handler.RegisterRoutes(r, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authmw.WithAuthContext(req.Context(), userID.String(), "s-1", "user")
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})

	oversized := bytes.Repeat([]byte("x"), 2<<20) // 2MB, over the ~1MB body ceiling
	req := newMultipartCreateBillRequest(t, groupID, [][]byte{oversized})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected status 413 Payload Too Large, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}
