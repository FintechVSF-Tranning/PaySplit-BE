package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"paysplit-backend/internal/modules/bill/domain"
	"paysplit-backend/internal/modules/bill/repository"
	"paysplit-backend/internal/modules/bill/usecase"
)

type mockServiceRepo struct {
	repository.Repository
	member         *repository.GroupMember
	activeMembers  []*repository.GroupMember
	bill           *domain.Bill
	ocrJob         *domain.OCRJob
	manualAttempts int64
	createdBill    *domain.Bill
	updatedBill    *domain.Bill
	finalizedBill  *domain.Bill
	voidedBill     *domain.Bill
	deletedDraft   bool
}

func (m *mockServiceRepo) GetGroupMember(ctx context.Context, groupID, userID uuid.UUID) (*repository.GroupMember, error) {
	if m.member != nil {
		return m.member, nil
	}
	return nil, domain.ErrInvalidInput
}

func (m *mockServiceRepo) ListActiveGroupMembers(ctx context.Context, groupID uuid.UUID) ([]*repository.GroupMember, error) {
	return m.activeMembers, nil
}

func (m *mockServiceRepo) CreateBill(ctx context.Context, params repository.CreateBillParams) (*domain.Bill, error) {
	m.createdBill = params.Bill
	m.createdBill.Images = params.Images
	m.createdBill.Items = params.Items
	return m.createdBill, nil
}

func (m *mockServiceRepo) GetBillByID(ctx context.Context, id, groupID uuid.UUID) (*domain.Bill, error) {
	if m.bill != nil {
		return m.bill, nil
	}
	return nil, domain.ErrBillNotFound
}

func (m *mockServiceRepo) GetActiveOCRJobByBillID(ctx context.Context, billID uuid.UUID) (*domain.OCRJob, error) {
	return nil, nil
}

func (m *mockServiceRepo) CountManualOCRAttemptsInWindow(ctx context.Context, billID uuid.UUID, since time.Time) (int64, error) {
	return m.manualAttempts, nil
}

func (m *mockServiceRepo) CreateOCRJob(ctx context.Context, job *domain.OCRJob) (*domain.OCRJob, error) {
	m.ocrJob = job
	return job, nil
}

func (m *mockServiceRepo) GetOCRJobByID(ctx context.Context, id uuid.UUID) (*domain.OCRJob, error) {
	if m.ocrJob != nil {
		return m.ocrJob, nil
	}
	return nil, domain.ErrOcrJobNotFound
}

func (m *mockServiceRepo) UpdateDraftBill(ctx context.Context, params repository.UpdateDraftParams) (*domain.Bill, error) {
	m.updatedBill = params.Bill
	m.updatedBill.Items = params.Items
	return m.updatedBill, nil
}

func (m *mockServiceRepo) ReviewBill(ctx context.Context, id, groupID uuid.UUID, expectedVersion int32) (*domain.Bill, error) {
	m.bill.Status = domain.BillStatusReviewed
	m.bill.Version = expectedVersion + 1
	return m.bill, nil
}

func (m *mockServiceRepo) FinalizeBill(ctx context.Context, params repository.FinalizeBillParams) (*domain.Bill, error) {
	m.finalizedBill = m.bill
	m.finalizedBill.Status = domain.BillStatusFinalized
	m.finalizedBill.Shares = params.Shares
	return m.finalizedBill, nil
}

func (m *mockServiceRepo) VoidBill(ctx context.Context, params repository.VoidBillParams) (*domain.Bill, error) {
	m.voidedBill = m.bill
	m.voidedBill.Status = domain.BillStatusVoided
	return m.voidedBill, nil
}

func (m *mockServiceRepo) DeleteDraftBill(ctx context.Context, id, groupID uuid.UUID) error {
	m.deletedDraft = true
	return nil
}

func (m *mockServiceRepo) EnqueueMediaCleanup(ctx context.Context, prefix, kind string) error {
	return nil
}

type mockProcessor struct{}

func (m *mockProcessor) Process(ctx context.Context, input []byte) ([]byte, error) {
	return input, nil
}
func (m *mockProcessor) IsUnsupported(err error) bool { return false }

type mockEnqueuer struct {
	enqueuedCount int
}

func (m *mockEnqueuer) EnqueueOCRJobTx(ctx context.Context, tx pgx.Tx, billID, jobID, groupID uuid.UUID) error {
	m.enqueuedCount++
	return nil
}
func (m *mockEnqueuer) EnqueueOCRJob(ctx context.Context, billID, jobID, groupID uuid.UUID) error {
	m.enqueuedCount++
	return nil
}

type mockStorage struct{}

func (m *mockStorage) Upload(ctx context.Context, data []byte, publicID string) (string, error) {
	return publicID, nil
}
func (m *mockStorage) SignedURL(publicID string, ttl time.Duration) (string, error) {
	return "https://signed.url/" + publicID, nil
}
func (m *mockStorage) Download(ctx context.Context, publicID string) ([]byte, error) {
	return []byte("data"), nil
}
func (m *mockStorage) Delete(ctx context.Context, publicID string) error { return nil }
func (m *mockStorage) DeleteByPrefix(ctx context.Context, prefix string) error {
	return nil
}

type mockOCRProvider struct{}

func (m *mockOCRProvider) ExtractReceipt(ctx context.Context, imageBytes []byte, mimeType string) (*domain.OCRCandidate, []byte, error) {
	return nil, nil, nil
}

func TestCreateBill_WithImages_EnqueuesOCR(t *testing.T) {
	groupID := uuid.New()
	userID := uuid.New()
	memberID := uuid.New()

	repo := &mockServiceRepo{
		member: &repository.GroupMember{
			ID:      memberID,
			GroupID: groupID,
			UserID:  userID,
			Role:    "member",
			Status:  "active",
		},
	}
	enqueuer := &mockEnqueuer{}

	service := usecase.NewService(repo, &mockOCRProvider{}, &mockStorage{}, &mockProcessor{}, enqueuer)

	merchant := "Nhà Hàng A"
	res, err := service.CreateBill(context.Background(), userID, usecase.CreateBillRequest{
		GroupID:      groupID,
		MerchantName: &merchant,
		Total:        100000,
		Files:        [][]byte{[]byte("image-data-1")},
	})
	if err != nil {
		t.Fatalf("CreateBill() error = %v", err)
	}

	if !res.IsAccepted {
		t.Error("expected IsAccepted = true for bill with images")
	}
	if res.OCRJob == nil {
		t.Error("expected OCR job to be created")
	}
	if enqueuer.enqueuedCount != 1 {
		t.Errorf("expected 1 enqueued job, got %d", enqueuer.enqueuedCount)
	}
}

func TestRetryOCR_LimitEnforced(t *testing.T) {
	groupID := uuid.New()
	userID := uuid.New()
	billID := uuid.New()

	repo := &mockServiceRepo{
		member: &repository.GroupMember{
			ID:      uuid.New(),
			GroupID: groupID,
			UserID:  userID,
			Role:    "member",
			Status:  "active",
		},
		bill: &domain.Bill{
			ID:     billID,
			Status: domain.BillStatusDraft,
			Images: []*domain.BillImage{
				{ID: uuid.New(), ImageKey: "bills/op-1/0"},
			},
		},
		manualAttempts: 5,
	}

	service := usecase.NewService(repo, &mockOCRProvider{}, &mockStorage{}, &mockProcessor{}, &mockEnqueuer{})

	_, err := service.RetryOCR(context.Background(), userID, billID, groupID)
	if err != domain.ErrOcrLimitReached {
		t.Errorf("expected ErrOcrLimitReached, got %v", err)
	}
}

func TestReviewAndFinalizeBill_Success(t *testing.T) {
	groupID := uuid.New()
	userID := uuid.New()
	creditorID := uuid.New()
	m2ID := uuid.New()
	billID := uuid.New()

	repo := &mockServiceRepo{
		member: &repository.GroupMember{
			ID:      creditorID,
			GroupID: groupID,
			UserID:  userID,
			Role:    "captain",
			Status:  "active",
		},
		bill: &domain.Bill{
			ID:               billID,
			GroupID:          groupID,
			CreditorMemberID: creditorID,
			Status:           domain.BillStatusDraft,
			Subtotal:         100000,
			Total:            100000,
			Version:          1,
			Items: []*domain.BillItem{
				{
					ID:        uuid.New(),
					LineTotal: 100000,
					Assignments: []*domain.BillItemAssignment{
						{MemberID: creditorID, Weight: "1"},
						{MemberID: m2ID, Weight: "1"},
					},
				},
			},
		},
	}

	service := usecase.NewService(repo, &mockOCRProvider{}, &mockStorage{}, &mockProcessor{}, &mockEnqueuer{})

	// 1. Review Bill
	reviewed, err := service.ReviewBill(context.Background(), userID, billID, groupID, 1)
	if err != nil {
		t.Fatalf("ReviewBill() error = %v", err)
	}
	if reviewed.Status != domain.BillStatusReviewed {
		t.Errorf("expected reviewed status, got %s", reviewed.Status)
	}

	// 2. Finalize Bill
	finalized, err := service.FinalizeBill(context.Background(), userID, billID, groupID, 2)
	if err != nil {
		t.Fatalf("FinalizeBill() error = %v", err)
	}
	if finalized.Status != domain.BillStatusFinalized {
		t.Errorf("expected finalized status, got %s", finalized.Status)
	}
	if len(finalized.Shares) != 2 {
		t.Errorf("expected 2 shares, got %d", len(finalized.Shares))
	}
}
