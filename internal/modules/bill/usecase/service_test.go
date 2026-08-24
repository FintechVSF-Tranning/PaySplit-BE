package usecase_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"

	"paysplit-backend/internal/modules/bill/domain"
	"paysplit-backend/internal/modules/bill/repository"
	"paysplit-backend/internal/modules/bill/usecase"
	platformmetrics "paysplit-backend/internal/platform/metrics"
)

// histogramSampleCount đọc số lượng observation đã ghi nhận cho một label combination
// của HistogramVec, dùng để xác nhận các hàm Record* thực sự được gọi (Spec 3 AC-14).
func histogramSampleCount(t *testing.T, hv *prometheus.HistogramVec, labelValues ...string) uint64 {
	t.Helper()
	m, ok := hv.WithLabelValues(labelValues...).(prometheus.Metric)
	if !ok {
		t.Fatalf("metric for labels %v does not implement prometheus.Metric", labelValues)
	}
	pb := &dto.Metric{}
	if err := m.Write(pb); err != nil {
		t.Fatalf("write histogram metric: %v", err)
	}
	return pb.GetHistogram().GetSampleCount()
}

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
	if m.activeMembers != nil {
		return m.activeMembers, nil
	}
	if m.bill != nil {
		members := make([]*repository.GroupMember, 0)
		seen := make(map[uuid.UUID]bool)
		for _, it := range m.bill.Items {
			for _, a := range it.Assignments {
				if !seen[a.MemberID] {
					seen[a.MemberID] = true
					members = append(members, &repository.GroupMember{
						ID:      a.MemberID,
						GroupID: groupID,
						UserID:  uuid.New(),
						Role:    "member",
						Status:  "active",
					})
				}
			}
		}
		if !seen[m.bill.CreditorMemberID] {
			members = append(members, &repository.GroupMember{
				ID:      m.bill.CreditorMemberID,
				GroupID: groupID,
				UserID:  uuid.New(),
				Role:    "captain",
				Status:  "active",
			})
		}
		return members, nil
	}
	if m.member != nil {
		return []*repository.GroupMember{m.member}, nil
	}
	return []*repository.GroupMember{}, nil
}

func (m *mockServiceRepo) CreateBill(ctx context.Context, params repository.CreateBillParams) (*domain.Bill, error) {
	m.createdBill = params.Bill
	m.createdBill.Images = params.Images
	m.createdBill.Items = params.Items
	if params.BeforeCommit != nil {
		if err := params.BeforeCommit(ctx, nil); err != nil {
			return nil, err
		}
	}
	return m.createdBill, nil
}

func (m *mockServiceRepo) GetBillByID(ctx context.Context, id, groupID uuid.UUID) (*domain.Bill, error) {
	if m.bill != nil {
		return m.bill, nil
	}
	return nil, domain.ErrBillNotFound
}

func (m *mockServiceRepo) GetBillOnlyByID(ctx context.Context, id uuid.UUID) (*domain.Bill, error) {
	if m.bill != nil {
		return m.bill, nil
	}
	return nil, domain.ErrBillNotFound
}

func (m *mockServiceRepo) GetGroupMemberUser(ctx context.Context, memberID, groupID uuid.UUID) (*repository.GroupMemberWithUser, error) {
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

func (m *mockServiceRepo) ListBillsByGroupCursor(ctx context.Context, params repository.ListBillsCursorParams) (*repository.ListBillsCursorResult, error) {
	bills := []*domain.Bill{}
	if m.bill != nil {
		bills = append(bills, m.bill)
	}
	return &repository.ListBillsCursorResult{
		Bills: bills,
	}, nil
}

func (m *mockServiceRepo) GetActiveOCRJobByBillID(ctx context.Context, billID uuid.UUID) (*domain.OCRJob, error) {
	return nil, nil
}

func (m *mockServiceRepo) CountManualOCRAttemptsInWindow(ctx context.Context, billID uuid.UUID, since time.Time) (int64, error) {
	return m.manualAttempts, nil
}

func (m *mockServiceRepo) CreateOCRJob(ctx context.Context, job *domain.OCRJob, beforeCommit func(ctx context.Context, tx pgx.Tx) error) (*domain.OCRJob, error) {
	if beforeCommit != nil {
		if err := beforeCommit(ctx, nil); err != nil {
			return nil, err
		}
	}
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
	if m.bill != nil && params.ExpectedVersion != m.bill.Version {
		return nil, domain.ErrBillConflict
	}
	m.updatedBill = params.Bill
	m.updatedBill.Items = params.Items
	return m.updatedBill, nil
}

func (m *mockServiceRepo) ReviewBill(ctx context.Context, id, groupID uuid.UUID, expectedVersion int32, reviewerMemberID uuid.UUID) (*domain.Bill, error) {
	m.bill.Status = domain.BillStatusReviewed
	m.bill.Version = expectedVersion + 1
	now := time.Now()
	m.bill.ReviewedAt = &now
	m.bill.ReviewedByMemberID = &reviewerMemberID
	return m.bill, nil
}

func (m *mockServiceRepo) FinalizeBill(ctx context.Context, params repository.FinalizeBillParams) (*domain.Bill, error) {
	if params.BeforeCommit != nil {
		if err := params.BeforeCommit(ctx, nil); err != nil {
			return nil, err
		}
	}
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

func (m *mockServiceRepo) DeleteDraftBill(ctx context.Context, params repository.DeleteDraftBillParams) error {
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
	errToReturn   error
}

func (m *mockEnqueuer) EnqueueOCRJobTx(ctx context.Context, tx pgx.Tx, billID, jobID, groupID uuid.UUID) error {
	if m.errToReturn != nil {
		return m.errToReturn
	}
	m.enqueuedCount++
	return nil
}
func (m *mockEnqueuer) EnqueueOCRJob(ctx context.Context, billID, jobID, groupID uuid.UUID) error {
	if m.errToReturn != nil {
		return m.errToReturn
	}
	m.enqueuedCount++
	return nil
}
func (m *mockEnqueuer) EnqueueNotificationTx(ctx context.Context, tx pgx.Tx, notificationID string) error {
	if m.errToReturn != nil {
		return m.errToReturn
	}
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
			Role:    "captain",
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

func TestRetryOCR_NonCreditorNonCaptain_ReturnsForbidden(t *testing.T) {
	groupID := uuid.New()
	userID := uuid.New()
	memberID := uuid.New()
	creditorID := uuid.New()
	billID := uuid.New()

	repo := &mockServiceRepo{
		member: &repository.GroupMember{
			ID:      memberID,
			GroupID: groupID,
			UserID:  userID,
			Role:    "member",
			Status:  "active",
		},
		bill: &domain.Bill{
			ID:               billID,
			CreditorMemberID: creditorID,
			Status:           domain.BillStatusDraft,
			Images: []*domain.BillImage{
				{ID: uuid.New(), ImageKey: "bills/op-1/0"},
			},
		},
	}

	service := usecase.NewService(repo, &mockOCRProvider{}, &mockStorage{}, &mockProcessor{}, &mockEnqueuer{})

	_, err := service.RetryOCR(context.Background(), userID, billID, groupID)
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("expected ErrForbidden for non-creditor non-captain, got %v", err)
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

func TestApplyCandidate_StaleProtection(t *testing.T) {
	// covers: AC-4 (Stale protection returns conflict if version changed)
	groupID := uuid.New()
	userID := uuid.New()
	memberID := uuid.New()
	billID := uuid.New()
	jobID := uuid.New()

	repo := &mockServiceRepo{
		member: &repository.GroupMember{
			ID:      memberID,
			GroupID: groupID,
			UserID:  userID,
			Role:    "captain",
			Status:  "active",
		},
		activeMembers: []*repository.GroupMember{
			{
				ID:      memberID,
				GroupID: groupID,
				UserID:  userID,
				Role:    "captain",
				Status:  "active",
			},
		},
		bill: &domain.Bill{
			ID:               billID,
			GroupID:          groupID,
			CreditorMemberID: memberID,
			Status:           domain.BillStatusDraft,
			Version:          2, // current version is 2
		},
		ocrJob: &domain.OCRJob{
			ID:      jobID,
			BillID:  billID,
			Status:  domain.OCRJobStatusSucceeded,
			Version: 1, // job started on version 1
			Candidate: &domain.OCRCandidate{
				Items: []domain.OCRCandidateItem{
					{Name: "Item 1", Quantity: "1", UnitPrice: 50000, LineTotal: 50000},
				},
				Subtotal: 50000,
				Total:    50000,
			},
		},
	}

	service := usecase.NewService(repo, &mockOCRProvider{}, &mockStorage{}, &mockProcessor{}, &mockEnqueuer{})

	// 1. Calling with stale expected version 1 should fail with ErrVersionConflict
	_, err := service.ApplyCandidate(context.Background(), userID, billID, groupID, jobID, 1)
	if err != domain.ErrVersionConflict {
		t.Errorf("expected ErrVersionConflict when expectedVersion != bill.Version, got %v", err)
	}

	// 2. Calling with matching expected version 2 but job was on version 1 should fail with ErrOcrResultStale
	_, err = service.ApplyCandidate(context.Background(), userID, billID, groupID, jobID, 2)
	if err != domain.ErrOcrResultStale {
		t.Errorf("expected ErrOcrResultStale when bill.Version != ocrJob.Version, got %v", err)
	}
}

func TestApplyCandidate_JobBelongsToDifferentBill_ReturnsNotFound(t *testing.T) {
	// covers: AC-4, AC-8 (a job from another bill must never be applied cross-bill/cross-group)
	groupID := uuid.New()
	userID := uuid.New()
	memberID := uuid.New()
	billID := uuid.New()
	otherBillID := uuid.New() // the job actually belongs to this bill
	jobID := uuid.New()

	repo := &mockServiceRepo{
		member: &repository.GroupMember{
			ID: memberID, GroupID: groupID, UserID: userID, Role: "captain", Status: "active",
		},
		bill: &domain.Bill{
			ID: billID, GroupID: groupID, CreditorMemberID: memberID,
			Status: domain.BillStatusDraft, Version: 1,
		},
		ocrJob: &domain.OCRJob{
			ID:      jobID,
			BillID:  otherBillID, // mismatched bill
			Status:  domain.OCRJobStatusSucceeded,
			Version: 1,
			Candidate: &domain.OCRCandidate{
				Items:    []domain.OCRCandidateItem{{Name: "Item 1", Quantity: "1", UnitPrice: 50000, LineTotal: 50000}},
				Subtotal: 50000,
				Total:    50000,
			},
		},
	}

	service := usecase.NewService(repo, &mockOCRProvider{}, &mockStorage{}, &mockProcessor{}, &mockEnqueuer{})

	_, err := service.ApplyCandidate(context.Background(), userID, billID, groupID, jobID, 1)
	if !errors.Is(err, domain.ErrOcrJobNotFound) {
		t.Errorf("expected ErrOcrJobNotFound when the OCR job belongs to a different bill, got %v", err)
	}
}

func TestReviewBill_Mismatch_FailsWhenTotalsDoNotReconcile(t *testing.T) {
	// covers: AC-7 (Review requires subtotal = sum(line_total) and total = subtotal + service + vat - discount)
	groupID := uuid.New()
	userID := uuid.New()
	creditorID := uuid.New()
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
			Total:            120000, // mismatch: subtotal 100k + 0 + 0 - 0 = 100k != 120k
			Version:          1,
			Items: []*domain.BillItem{
				{
					ID:        uuid.New(),
					LineTotal: 100000,
					Assignments: []*domain.BillItemAssignment{
						{MemberID: creditorID, Weight: "1"},
					},
				},
			},
		},
	}

	service := usecase.NewService(repo, &mockOCRProvider{}, &mockStorage{}, &mockProcessor{}, &mockEnqueuer{})

	_, err := service.ReviewBill(context.Background(), userID, billID, groupID, 1)
	if err == nil {
		t.Error("expected error due to total mismatch, got nil")
	}
}

func TestVoidBill_NonCaptain_ReturnsForbidden(t *testing.T) {
	// covers: AC-8, AC-11 (Only Captain can void finalized bill)
	groupID := uuid.New()
	userID := uuid.New()
	memberID := uuid.New()
	billID := uuid.New()

	repo := &mockServiceRepo{
		member: &repository.GroupMember{
			ID:      memberID,
			GroupID: groupID,
			UserID:  userID,
			Role:    "member", // regular member, not captain
			Status:  "active",
		},
		bill: &domain.Bill{
			ID:               billID,
			GroupID:          groupID,
			CreditorMemberID: memberID,
			Status:           domain.BillStatusFinalized,
			Version:          2,
		},
	}

	service := usecase.NewService(repo, &mockOCRProvider{}, &mockStorage{}, &mockProcessor{}, &mockEnqueuer{})

	_, err := service.VoidBill(context.Background(), userID, billID, groupID, 2, "Test void")
	if err != domain.ErrForbidden {
		t.Errorf("expected ErrForbidden, got %v", err)
	}
}

func TestDeleteDraftBill_FinalizedBill_ReturnsImmutable(t *testing.T) {
	// covers: AC-13 (Deleting a finalized bill is rejected as immutable)
	groupID := uuid.New()
	userID := uuid.New()
	creditorID := uuid.New()
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
			Status:           domain.BillStatusFinalized, // cannot delete finalized bill
			Version:          2,
		},
	}

	service := usecase.NewService(repo, &mockOCRProvider{}, &mockStorage{}, &mockProcessor{}, &mockEnqueuer{})

	err := service.DeleteDraftBill(context.Background(), userID, billID, groupID)
	if err != domain.ErrBillImmutable {
		t.Errorf("expected ErrBillImmutable, got %v", err)
	}
}

func TestFinalizeBill_DraftStatus_ReturnsReviewRequired(t *testing.T) {
	// covers: AC-7, AC-9 (Finalizing an unreviewed bill returns ErrReviewRequired)
	groupID := uuid.New()
	userID := uuid.New()
	creditorID := uuid.New()
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
			Status:           domain.BillStatusDraft, // not reviewed yet
			Version:          1,
		},
	}

	service := usecase.NewService(repo, &mockOCRProvider{}, &mockStorage{}, &mockProcessor{}, &mockEnqueuer{})

	_, err := service.FinalizeBill(context.Background(), userID, billID, groupID, 1)
	if err != domain.ErrReviewRequired {
		t.Errorf("expected ErrReviewRequired, got %v", err)
	}
}

func TestFinalizeBill_NonCaptain_ReturnsForbidden(t *testing.T) {
	// covers: AC-8, AC-9 (Only Captain can finalize bill)
	groupID := uuid.New()
	userID := uuid.New()
	creditorID := uuid.New()
	billID := uuid.New()

	repo := &mockServiceRepo{
		member: &repository.GroupMember{
			ID:      creditorID,
			GroupID: groupID,
			UserID:  userID,
			Role:    "member", // regular member, even if Creditor
			Status:  "active",
		},
		bill: &domain.Bill{
			ID:               billID,
			GroupID:          groupID,
			CreditorMemberID: creditorID,
			Status:           domain.BillStatusReviewed,
			Version:          2,
		},
	}

	service := usecase.NewService(repo, &mockOCRProvider{}, &mockStorage{}, &mockProcessor{}, &mockEnqueuer{})

	_, err := service.FinalizeBill(context.Background(), userID, billID, groupID, 2)
	if err != domain.ErrForbidden {
		t.Errorf("expected ErrForbidden, got %v", err)
	}
}

func TestFinalizeBill_FinalizedBill_ReturnsImmutable(t *testing.T) {
	// covers: AC-9, AC-11 (Finalizing an already finalized bill returns ErrBillImmutable)
	groupID := uuid.New()
	userID := uuid.New()
	creditorID := uuid.New()
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
			Status:           domain.BillStatusFinalized,
			Version:          3,
		},
	}

	service := usecase.NewService(repo, &mockOCRProvider{}, &mockStorage{}, &mockProcessor{}, &mockEnqueuer{})

	_, err := service.FinalizeBill(context.Background(), userID, billID, groupID, 3)
	if err != domain.ErrBillImmutable {
		t.Errorf("expected ErrBillImmutable, got %v", err)
	}
}

func TestCreateBill_EnqueueOCRFails_ReturnsError(t *testing.T) {
	// covers: AC-2 (Failure during River OCR enqueue propagates error)
	groupID := uuid.New()
	userID := uuid.New()

	repo := &mockServiceRepo{
		member: &repository.GroupMember{
			ID:      uuid.New(),
			GroupID: groupID,
			UserID:  userID,
			Role:    "member",
			Status:  "active",
		},
	}

	enqueuer := &mockEnqueuer{
		errToReturn: errors.New("river enqueue connection failed"),
	}

	service := usecase.NewService(repo, &mockOCRProvider{}, &mockStorage{}, &mockProcessor{}, enqueuer)

	_, err := service.CreateBill(context.Background(), userID, usecase.CreateBillRequest{
		GroupID: groupID,
		Files: [][]byte{
			[]byte("fake-image-bytes"),
		},
	})
	if err == nil {
		t.Error("expected error when OCR enqueue fails, got nil")
	}
}

func TestRetryOCR_EnqueueOCRFails_ReturnsError(t *testing.T) {
	// covers: AC-2 (Failure during RetryOCR River enqueue propagates error)
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
	}

	enqueuer := &mockEnqueuer{
		errToReturn: errors.New("river enqueue failed"),
	}

	service := usecase.NewService(repo, &mockOCRProvider{}, &mockStorage{}, &mockProcessor{}, enqueuer)

	_, err := service.RetryOCR(context.Background(), userID, billID, groupID)
	if err == nil {
		t.Error("expected error when RetryOCR enqueue fails, got nil")
	}
	// covers: AC-2 (insert and enqueue happen in one transaction: a failed enqueue must not leave
	// behind a "queued" row that no worker will ever pick up, permanently wedging the bill's OCR)
	if repo.ocrJob != nil {
		t.Error("expected no ocr job row to be created when the enqueue fails (insert and enqueue must be atomic)")
	}
}

func TestRetryOCR_Success(t *testing.T) {
	// covers: AC-2 (Happy path for RetryOCR creates OCR job and enqueues)
	groupID := uuid.New()
	userID := uuid.New()
	billID := uuid.New()

	repo := &mockServiceRepo{
		member: &repository.GroupMember{
			ID:      uuid.New(),
			GroupID: groupID,
			UserID:  userID,
			Role:    "captain",
			Status:  "active",
		},
		bill: &domain.Bill{
			ID:     billID,
			Status: domain.BillStatusDraft,
			Images: []*domain.BillImage{
				{ID: uuid.New(), ImageKey: "bills/op-1/0"},
			},
		},
	}

	enqueuer := &mockEnqueuer{}

	service := usecase.NewService(repo, &mockOCRProvider{}, &mockStorage{}, &mockProcessor{}, enqueuer)

	job, err := service.RetryOCR(context.Background(), userID, billID, groupID)
	if err != nil {
		t.Fatalf("RetryOCR() error = %v", err)
	}
	if job.Status != domain.OCRJobStatusQueued {
		t.Errorf("expected job status queued, got %s", job.Status)
	}
	if enqueuer.enqueuedCount != 1 {
		t.Errorf("expected 1 job enqueued, got %d", enqueuer.enqueuedCount)
	}
}

func TestFinalizeBill_MissingBankAccount_ReturnsBankAccountRequired(t *testing.T) {
	// covers: B-2, Spec 3 AC-9 (422 BANK_ACCOUNT_REQUIRED when creditor has no bank info)
	groupID := uuid.New()
	userID := uuid.New()
	captainMemberID := uuid.New()
	creditorMemberID := uuid.New()
	billID := uuid.New()

	repo := &mockServiceRepo{
		member: &repository.GroupMember{
			ID:      captainMemberID,
			GroupID: groupID,
			UserID:  userID,
			Role:    "captain",
			Status:  "active",
		},
		bill: &domain.Bill{
			ID:               billID,
			GroupID:          groupID,
			CreditorMemberID: creditorMemberID,
			Status:           domain.BillStatusReviewed,
			Version:          1,
			Subtotal:         100000,
			Total:            100000,
			Items: []*domain.BillItem{
				{
					ID:        uuid.New(),
					LineTotal: 100000,
					Assignments: []*domain.BillItemAssignment{
						{MemberID: captainMemberID, Weight: "1.0"},
					},
				},
			},
		},
	}

	// Override GetGroupMemberUser to return nil bank code
	service := usecase.NewService(&mockRepoWithNoBank{mockServiceRepo: repo}, &mockOCRProvider{}, &mockStorage{}, &mockProcessor{}, &mockEnqueuer{})

	_, err := service.FinalizeBill(context.Background(), userID, billID, groupID, 1)
	if !errors.Is(err, domain.ErrBankAccountRequired) {
		t.Errorf("expected ErrBankAccountRequired, got %v", err)
	}
}

type mockRepoWithNoBank struct {
	*mockServiceRepo
}

func (m *mockRepoWithNoBank) GetGroupMemberUser(ctx context.Context, memberID, groupID uuid.UUID) (*repository.GroupMemberWithUser, error) {
	return &repository.GroupMemberWithUser{
		ID:                    memberID,
		GroupID:               groupID,
		Role:                  "captain",
		Status:                "active",
		DefaultBankCode:       nil,
		DefaultBankAccountNum: nil,
	}, nil
}

func TestFinalizeBill_ReconciliationMismatch_ReturnsBillNotReady(t *testing.T) {
	// covers: B-2, Spec 3 AC-5, AC-9 (subtotal or total mismatch fails finalization)
	groupID := uuid.New()
	userID := uuid.New()
	captainMemberID := uuid.New()
	billID := uuid.New()

	repo := &mockServiceRepo{
		member: &repository.GroupMember{
			ID:      captainMemberID,
			GroupID: groupID,
			UserID:  userID,
			Role:    "captain",
			Status:  "active",
		},
		bill: &domain.Bill{
			ID:               billID,
			GroupID:          groupID,
			CreditorMemberID: captainMemberID,
			Status:           domain.BillStatusReviewed,
			Version:          1,
			Subtotal:         100000,
			Total:            120000, // mismatch: items sum = 100000, but Total = 120000 without fees
			Items: []*domain.BillItem{
				{
					ID:        uuid.New(),
					LineTotal: 100000,
					Assignments: []*domain.BillItemAssignment{
						{MemberID: captainMemberID, Weight: "1.0"},
					},
				},
			},
		},
	}

	service := usecase.NewService(repo, &mockOCRProvider{}, &mockStorage{}, &mockProcessor{}, &mockEnqueuer{})

	_, err := service.FinalizeBill(context.Background(), userID, billID, groupID, 1)
	if !errors.Is(err, domain.ErrBillNotReady) {
		t.Errorf("expected ErrBillNotReady, got %v", err)
	}
}

func TestFinalizeBill_VersionMismatch_ReturnsConflict(t *testing.T) {
	// covers: B-2, Spec 3 AC-9 (version check before state transition)
	groupID := uuid.New()
	userID := uuid.New()
	captainMemberID := uuid.New()
	billID := uuid.New()

	repo := &mockServiceRepo{
		member: &repository.GroupMember{
			ID:      captainMemberID,
			GroupID: groupID,
			UserID:  userID,
			Role:    "captain",
			Status:  "active",
		},
		bill: &domain.Bill{
			ID:               billID,
			GroupID:          groupID,
			CreditorMemberID: captainMemberID,
			Status:           domain.BillStatusReviewed,
			Version:          2, // current is 2
			Subtotal:         100000,
			Total:            100000,
			Items: []*domain.BillItem{
				{
					ID:        uuid.New(),
					LineTotal: 100000,
					Assignments: []*domain.BillItemAssignment{
						{MemberID: captainMemberID, Weight: "1.0"},
					},
				},
			},
		},
	}

	service := usecase.NewService(repo, &mockOCRProvider{}, &mockStorage{}, &mockProcessor{}, &mockEnqueuer{})

	_, err := service.FinalizeBill(context.Background(), userID, billID, groupID, 1) // expects 1
	if !errors.Is(err, domain.ErrVersionConflict) {
		t.Errorf("expected ErrVersionConflict, got %v", err)
	}
}

func TestVoidBill_StatusChecks(t *testing.T) {
	// covers: M-1, Spec 3 AC-11 (only finalized bills can be voided, already voided returns ErrBillAlreadyVoided)
	groupID := uuid.New()
	userID := uuid.New()
	captainMemberID := uuid.New()
	billID := uuid.New()

	t.Run("Draft bill returns ErrBillNotFinalized", func(t *testing.T) {
		repo := &mockServiceRepo{
			member: &repository.GroupMember{
				ID:      captainMemberID,
				GroupID: groupID,
				UserID:  userID,
				Role:    "captain",
				Status:  "active",
			},
			bill: &domain.Bill{
				ID:      billID,
				GroupID: groupID,
				Status:  domain.BillStatusDraft,
				Version: 1,
			},
		}
		service := usecase.NewService(repo, &mockOCRProvider{}, &mockStorage{}, &mockProcessor{}, &mockEnqueuer{})

		_, err := service.VoidBill(context.Background(), userID, billID, groupID, 1, "Mistake")
		if !errors.Is(err, domain.ErrBillNotFinalized) {
			t.Errorf("expected ErrBillNotFinalized, got %v", err)
		}
	})

	t.Run("Already voided bill returns ErrBillAlreadyVoided", func(t *testing.T) {
		repo := &mockServiceRepo{
			member: &repository.GroupMember{
				ID:      captainMemberID,
				GroupID: groupID,
				UserID:  userID,
				Role:    "captain",
				Status:  "active",
			},
			bill: &domain.Bill{
				ID:      billID,
				GroupID: groupID,
				Status:  domain.BillStatusVoided,
				Version: 1,
			},
		}
		service := usecase.NewService(repo, &mockOCRProvider{}, &mockStorage{}, &mockProcessor{}, &mockEnqueuer{})

		_, err := service.VoidBill(context.Background(), userID, billID, groupID, 1, "Mistake")
		if !errors.Is(err, domain.ErrBillAlreadyVoided) {
			t.Errorf("expected ErrBillAlreadyVoided, got %v", err)
		}
	})

	t.Run("Version mismatch returns ErrVersionConflict", func(t *testing.T) {
		repo := &mockServiceRepo{
			member: &repository.GroupMember{
				ID:      captainMemberID,
				GroupID: groupID,
				UserID:  userID,
				Role:    "captain",
				Status:  "active",
			},
			bill: &domain.Bill{
				ID:      billID,
				GroupID: groupID,
				Status:  domain.BillStatusFinalized,
				Version: 3,
			},
		}
		service := usecase.NewService(repo, &mockOCRProvider{}, &mockStorage{}, &mockProcessor{}, &mockEnqueuer{})

		_, err := service.VoidBill(context.Background(), userID, billID, groupID, 1, "Mistake")
		if !errors.Is(err, domain.ErrVersionConflict) {
			t.Errorf("expected ErrVersionConflict, got %v", err)
		}
	})
}

func TestUpdateDraftBill_ReviewedStatus_Allowed(t *testing.T) {
	// covers: B-3, Spec 3 AC-7 (editing a reviewed bill reverts to draft and increments version)
	groupID := uuid.New()
	userID := uuid.New()
	captainMemberID := uuid.New()
	billID := uuid.New()

	repo := &mockServiceRepo{
		member: &repository.GroupMember{
			ID:      captainMemberID,
			GroupID: groupID,
			UserID:  userID,
			Role:    "captain",
			Status:  "active",
		},
		bill: &domain.Bill{
			ID:      billID,
			GroupID: groupID,
			Status:  domain.BillStatusReviewed,
			Version: 2,
		},
	}

	service := usecase.NewService(repo, &mockOCRProvider{}, &mockStorage{}, &mockProcessor{}, &mockEnqueuer{})

	req := usecase.UpdateDraftRequest{
		Version:  2,
		Subtotal: 200000,
		Total:    200000,
		Items: []usecase.CreateBillItemRequest{
			{Name: "Item 1", LineTotal: 200000},
		},
	}

	updated, err := service.UpdateDraftBill(context.Background(), userID, billID, groupID, req)
	if err != nil {
		t.Fatalf("UpdateDraftBill() error = %v", err)
	}
	if updated == nil {
		t.Fatal("expected updated bill, got nil")
	}
}

func TestListBillsCursor_Success(t *testing.T) {
	// covers: M-3, Spec 3 AC-12 (cursor-based bill listing)
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
			ID:      billID,
			GroupID: groupID,
			Status:  domain.BillStatusFinalized,
		},
	}

	service := usecase.NewService(repo, &mockOCRProvider{}, &mockStorage{}, &mockProcessor{}, &mockEnqueuer{})

	res, err := service.ListBillsCursor(context.Background(), userID, groupID, nil, 10)
	if err != nil {
		t.Fatalf("ListBillsCursor() error = %v", err)
	}
	if len(res.Bills) != 1 {
		t.Errorf("expected 1 bill, got %d", len(res.Bills))
	}
}

func TestAuthorization_InactiveOrNonMember_ReturnsForbidden(t *testing.T) {
	groupID := uuid.New()
	userID := uuid.New()
	billID := uuid.New()

	repo := &mockServiceRepo{
		member: &repository.GroupMember{
			ID:      uuid.New(),
			GroupID: groupID,
			UserID:  userID,
			Role:    "member",
			Status:  "inactive", // Inactive member
		},
		bill: &domain.Bill{
			ID:      billID,
			GroupID: groupID,
			Status:  domain.BillStatusDraft,
		},
	}

	service := usecase.NewService(repo, &mockOCRProvider{}, &mockStorage{}, &mockProcessor{}, &mockEnqueuer{})

	ctx := context.Background()

	// 1. GetBillDetail
	_, err := service.GetBillDetail(ctx, userID, billID, groupID)
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("GetBillDetail expected ErrForbidden, got %v", err)
	}

	// 2. ListBills
	_, err = service.ListBills(ctx, userID, groupID, 10, 0)
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("ListBills expected ErrForbidden, got %v", err)
	}

	// 3. ListBillsCursor
	_, err = service.ListBillsCursor(ctx, userID, groupID, nil, 10)
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("ListBillsCursor expected ErrForbidden, got %v", err)
	}

	// 4. RetryOCR
	_, err = service.RetryOCR(ctx, userID, billID, groupID)
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("RetryOCR expected ErrForbidden, got %v", err)
	}

	// 5. ApplyCandidate
	_, err = service.ApplyCandidate(ctx, userID, billID, groupID, uuid.New(), 1)
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("ApplyCandidate expected ErrForbidden, got %v", err)
	}

	// 6. UpdateDraftBill
	_, err = service.UpdateDraftBill(ctx, userID, billID, groupID, usecase.UpdateDraftRequest{})
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("UpdateDraftBill expected ErrForbidden, got %v", err)
	}

	// 7. ReviewBill
	_, err = service.ReviewBill(ctx, userID, billID, groupID, 1)
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("ReviewBill expected ErrForbidden, got %v", err)
	}

	// 8. FinalizeBill
	_, err = service.FinalizeBill(ctx, userID, billID, groupID, 1)
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("FinalizeBill expected ErrForbidden, got %v", err)
	}

	// 9. VoidBill
	_, err = service.VoidBill(ctx, userID, billID, groupID, 1, "reason")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("VoidBill expected ErrForbidden, got %v", err)
	}

	// 10. DeleteDraftBill
	err = service.DeleteDraftBill(ctx, userID, billID, groupID)
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("DeleteDraftBill expected ErrForbidden, got %v", err)
	}
}

func TestApplyCandidate_PropagatesMismatchWarnings(t *testing.T) {
	groupID := uuid.New()
	userID := uuid.New()
	captainMemberID := uuid.New()
	billID := uuid.New()
	jobID := uuid.New()

	repo := &mockServiceRepo{
		member: &repository.GroupMember{
			ID:      captainMemberID,
			GroupID: groupID,
			UserID:  userID,
			Role:    "captain",
			Status:  "active",
		},
		bill: &domain.Bill{
			ID:               billID,
			GroupID:          groupID,
			CreditorMemberID: captainMemberID,
			Status:           domain.BillStatusDraft,
			Version:          1,
		},
		ocrJob: &domain.OCRJob{
			ID:      jobID,
			BillID:  billID,
			Status:  domain.OCRJobStatusSucceeded,
			Version: 1,
			Candidate: &domain.OCRCandidate{
				Subtotal: 100000,
				Total:    120000,
				Warnings: []string{domain.WarningTotalMismatch, domain.WarningSubtotalMismatch},
				Items: []domain.OCRCandidateItem{
					{Name: "Item 1", LineTotal: 100000, Quantity: "1", UnitPrice: 100000},
				},
			},
		},
		activeMembers: []*repository.GroupMember{
			{ID: captainMemberID, GroupID: groupID, UserID: userID, Role: "captain", Status: "active"},
		},
	}

	service := usecase.NewService(repo, &mockOCRProvider{}, &mockStorage{}, &mockProcessor{}, &mockEnqueuer{})

	updated, err := service.ApplyCandidate(context.Background(), userID, billID, groupID, jobID, 1)
	if err != nil {
		t.Fatalf("ApplyCandidate() error = %v", err)
	}

	if len(updated.MismatchCodes) != 2 {
		t.Errorf("expected 2 mismatch codes, got %v", updated.MismatchCodes)
	}
}

func TestReviewBill_ExcessiveDiscount_ReturnsBillNotReady(t *testing.T) {
	groupID := uuid.New()
	userID := uuid.New()
	captainMemberID := uuid.New()
	billID := uuid.New()

	repo := &mockServiceRepo{
		member: &repository.GroupMember{
			ID:      captainMemberID,
			GroupID: groupID,
			UserID:  userID,
			Role:    "captain",
			Status:  "active",
		},
		bill: &domain.Bill{
			ID:               billID,
			GroupID:          groupID,
			CreditorMemberID: captainMemberID,
			Status:           domain.BillStatusDraft,
			Version:          1,
			Subtotal:         100000,
			Discount:         150000, // Discount > Subtotal
			Total:            0,
			Items: []*domain.BillItem{
				{
					ID:        uuid.New(),
					LineTotal: 100000,
					Assignments: []*domain.BillItemAssignment{
						{MemberID: captainMemberID, Weight: "1"},
					},
				},
			},
		},
		activeMembers: []*repository.GroupMember{
			{ID: captainMemberID, GroupID: groupID, UserID: userID, Role: "captain", Status: "active"},
		},
	}

	service := usecase.NewService(repo, &mockOCRProvider{}, &mockStorage{}, &mockProcessor{}, &mockEnqueuer{})

	_, err := service.ReviewBill(context.Background(), userID, billID, groupID, 1)
	if !errors.Is(err, domain.ErrBillNotReady) {
		t.Errorf("expected ErrBillNotReady for excessive discount, got %v", err)
	}
}

func TestFinalizeBill_EnqueuesNotifications(t *testing.T) {
	groupID := uuid.New()
	userID := uuid.New()
	captainMemberID := uuid.New()
	otherMemberID := uuid.New()
	otherUserID := uuid.New()
	billID := uuid.New()

	repo := &mockServiceRepo{
		member: &repository.GroupMember{
			ID:      captainMemberID,
			GroupID: groupID,
			UserID:  userID,
			Role:    "captain",
			Status:  "active",
		},
		activeMembers: []*repository.GroupMember{
			{ID: captainMemberID, GroupID: groupID, UserID: userID, Role: "captain", Status: "active"},
			{ID: otherMemberID, GroupID: groupID, UserID: otherUserID, Role: "member", Status: "active"},
		},
		bill: &domain.Bill{
			ID:               billID,
			GroupID:          groupID,
			CreditorMemberID: captainMemberID,
			Status:           domain.BillStatusReviewed,
			Version:          1,
			Subtotal:         100000,
			Total:            100000,
			Items: []*domain.BillItem{
				{
					ID:        uuid.New(),
					LineTotal: 100000,
					Assignments: []*domain.BillItemAssignment{
						{MemberID: captainMemberID, Weight: "1"},
						{MemberID: otherMemberID, Weight: "1"},
					},
				},
			},
		},
	}

	enqueuer := &mockEnqueuer{}
	service := usecase.NewService(repo, &mockOCRProvider{}, &mockStorage{}, &mockProcessor{}, enqueuer)

	finalized, err := service.FinalizeBill(context.Background(), userID, billID, groupID, 1)
	if err != nil {
		t.Fatalf("FinalizeBill() error = %v", err)
	}
	if finalized == nil {
		t.Fatal("expected finalized bill, got nil")
	}
}

func TestCreateBill_ReplacesBillID_Success(t *testing.T) {
	groupID := uuid.New()
	userID := uuid.New()
	captainMemberID := uuid.New()
	voidedBillID := uuid.New()

	repo := &mockServiceRepo{
		member: &repository.GroupMember{
			ID:      captainMemberID,
			GroupID: groupID,
			UserID:  userID,
			Role:    "captain",
			Status:  "active",
		},
		bill: &domain.Bill{
			ID:      voidedBillID,
			GroupID: groupID,
			Status:  domain.BillStatusVoided,
		},
	}

	service := usecase.NewService(repo, &mockOCRProvider{}, &mockStorage{}, &mockProcessor{}, &mockEnqueuer{})

	res, err := service.CreateBill(context.Background(), userID, usecase.CreateBillRequest{
		GroupID:        groupID,
		ReplacesBillID: &voidedBillID,
		Total:          50000,
	})
	if err != nil {
		t.Fatalf("CreateBill() unexpected error = %v", err)
	}
	if res.Bill.ReplacesBillID == nil || *res.Bill.ReplacesBillID != voidedBillID {
		t.Errorf("expected ReplacesBillID to be %v, got %v", voidedBillID, res.Bill.ReplacesBillID)
	}
}

func TestCreateBill_ReplacesBillID_NotVoided_ReturnsInvalidInput(t *testing.T) {
	groupID := uuid.New()
	userID := uuid.New()
	captainMemberID := uuid.New()
	activeBillID := uuid.New()

	repo := &mockServiceRepo{
		member: &repository.GroupMember{
			ID:      captainMemberID,
			GroupID: groupID,
			UserID:  userID,
			Role:    "captain",
			Status:  "active",
		},
		bill: &domain.Bill{
			ID:      activeBillID,
			GroupID: groupID,
			Status:  domain.BillStatusDraft, // Not voided
		},
	}

	service := usecase.NewService(repo, &mockOCRProvider{}, &mockStorage{}, &mockProcessor{}, &mockEnqueuer{})

	_, err := service.CreateBill(context.Background(), userID, usecase.CreateBillRequest{
		GroupID:        groupID,
		ReplacesBillID: &activeBillID,
		Total:          50000,
	})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput when replacing non-voided bill, got %v", err)
	}
}

func TestCreateBill_InvalidWeight_ReturnsInvalidInput(t *testing.T) {
	groupID := uuid.New()
	userID := uuid.New()
	captainMemberID := uuid.New()

	repo := &mockServiceRepo{
		member: &repository.GroupMember{
			ID:      captainMemberID,
			GroupID: groupID,
			UserID:  userID,
			Role:    "captain",
			Status:  "active",
		},
	}

	service := usecase.NewService(repo, &mockOCRProvider{}, &mockStorage{}, &mockProcessor{}, &mockEnqueuer{})

	_, err := service.CreateBill(context.Background(), userID, usecase.CreateBillRequest{
		GroupID: groupID,
		Total:   50000,
		Items: []usecase.CreateBillItemRequest{
			{
				Name:      "Item 1",
				LineTotal: 50000,
				Assignments: []usecase.CreateItemAssignmentRequest{
					{MemberID: captainMemberID, Weight: "-5"}, // Invalid negative weight
				},
			},
		},
	})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput for negative weight, got %v", err)
	}
}

func TestUpdateDraftBill_InvalidWeight_ReturnsInvalidInput(t *testing.T) {
	groupID := uuid.New()
	userID := uuid.New()
	captainMemberID := uuid.New()
	billID := uuid.New()

	repo := &mockServiceRepo{
		member: &repository.GroupMember{
			ID:      captainMemberID,
			GroupID: groupID,
			UserID:  userID,
			Role:    "captain",
			Status:  "active",
		},
		bill: &domain.Bill{
			ID:               billID,
			GroupID:          groupID,
			CreditorMemberID: captainMemberID,
			Status:           domain.BillStatusDraft,
			Version:          1,
		},
	}

	service := usecase.NewService(repo, &mockOCRProvider{}, &mockStorage{}, &mockProcessor{}, &mockEnqueuer{})

	_, err := service.UpdateDraftBill(context.Background(), userID, billID, groupID, usecase.UpdateDraftRequest{
		Version: 1,
		Total:   50000,
		Items: []usecase.CreateBillItemRequest{
			{
				Name:      "Item 1",
				LineTotal: 50000,
				Assignments: []usecase.CreateItemAssignmentRequest{
					{MemberID: captainMemberID, Weight: "0"}, // Invalid zero weight
				},
			},
		},
	})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput for zero weight in UpdateDraftBill, got %v", err)
	}
}

func TestRetryOCR_CapturesCurrentBillVersion(t *testing.T) {
	groupID := uuid.New()
	userID := uuid.New()
	captainMemberID := uuid.New()
	billID := uuid.New()

	repo := &mockServiceRepo{
		member: &repository.GroupMember{
			ID:      captainMemberID,
			GroupID: groupID,
			UserID:  userID,
			Role:    "captain",
			Status:  "active",
		},
		bill: &domain.Bill{
			ID:               billID,
			GroupID:          groupID,
			CreditorMemberID: captainMemberID,
			Status:           domain.BillStatusDraft,
			Version:          4, // Current bill version is 4
			Images: []*domain.BillImage{
				{ID: uuid.New(), ImageKey: "bills/op/0"},
			},
		},
	}

	service := usecase.NewService(repo, &mockOCRProvider{}, &mockStorage{}, &mockProcessor{}, &mockEnqueuer{})

	job, err := service.RetryOCR(context.Background(), userID, billID, groupID)
	if err != nil {
		t.Fatalf("RetryOCR() unexpected error = %v", err)
	}
	if job.Version != 4 {
		t.Errorf("expected retry OCR job to capture bill version 4, got %d", job.Version)
	}
}

func TestCreateBill_Exceeds100Items_ReturnsInvalidInput(t *testing.T) {
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

	service := usecase.NewService(repo, &mockOCRProvider{}, &mockStorage{}, &mockProcessor{}, &mockEnqueuer{})

	items := make([]usecase.CreateBillItemRequest, 101)
	for i := range items {
		items[i] = usecase.CreateBillItemRequest{
			Name:      "Item",
			Quantity:  "1",
			UnitPrice: 1000,
			LineTotal: 1000,
		}
	}

	_, err := service.CreateBill(context.Background(), userID, usecase.CreateBillRequest{
		GroupID: groupID,
		Items:   items,
	})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput when creating bill with > 100 items, got %v", err)
	}
}

func TestUpdateDraftBill_Exceeds100Items_ReturnsInvalidInput(t *testing.T) {
	groupID := uuid.New()
	userID := uuid.New()
	memberID := uuid.New()
	billID := uuid.New()

	repo := &mockServiceRepo{
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
			Version:          1,
		},
	}

	service := usecase.NewService(repo, &mockOCRProvider{}, &mockStorage{}, &mockProcessor{}, &mockEnqueuer{})

	items := make([]usecase.CreateBillItemRequest, 101)
	for i := range items {
		items[i] = usecase.CreateBillItemRequest{
			Name:      "Item",
			Quantity:  "1",
			UnitPrice: 1000,
			LineTotal: 1000,
		}
	}

	_, err := service.UpdateDraftBill(context.Background(), userID, billID, groupID, usecase.UpdateDraftRequest{
		Version: 1,
		Items:   items,
	})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput when updating bill with > 100 items, got %v", err)
	}
}

func TestFinalizeBill_Success_RecordsFinalizeDurationMetric(t *testing.T) {
	// covers: AC-9, AC-14 (paysplit_bill_finalize_duration_seconds observes a success sample)
	groupID := uuid.New()
	userID := uuid.New()
	creditorID := uuid.New()
	m2ID := uuid.New()
	billID := uuid.New()

	repo := &mockServiceRepo{
		member: &repository.GroupMember{
			ID: creditorID, GroupID: groupID, UserID: userID, Role: "captain", Status: "active",
		},
		bill: &domain.Bill{
			ID: billID, GroupID: groupID, CreditorMemberID: creditorID,
			Status: domain.BillStatusReviewed, Subtotal: 100000, Total: 100000, Version: 2,
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

	before := histogramSampleCount(t, platformmetrics.BillFinalizeDuration, "success")

	_, err := service.FinalizeBill(context.Background(), userID, billID, groupID, 2)
	if err != nil {
		t.Fatalf("FinalizeBill() error = %v", err)
	}

	after := histogramSampleCount(t, platformmetrics.BillFinalizeDuration, "success")
	if got, want := after, before+1; got != want {
		t.Errorf("finalize duration histogram sample count for outcome=success = %d, want %d", got, want)
	}
}

func TestFinalizeBill_Forbidden_RecordsFinalizeDurationMetricAsError(t *testing.T) {
	// covers: AC-8, AC-14 (a rejected finalize attempt is still observed, tagged outcome=error)
	groupID := uuid.New()
	userID := uuid.New()
	memberID := uuid.New()
	billID := uuid.New()

	repo := &mockServiceRepo{
		member: &repository.GroupMember{
			ID: memberID, GroupID: groupID, UserID: userID, Role: "member", Status: "active", // not captain
		},
		bill: &domain.Bill{ID: billID, GroupID: groupID, Status: domain.BillStatusReviewed, Version: 1},
	}
	service := usecase.NewService(repo, &mockOCRProvider{}, &mockStorage{}, &mockProcessor{}, &mockEnqueuer{})

	before := histogramSampleCount(t, platformmetrics.BillFinalizeDuration, "error")

	_, err := service.FinalizeBill(context.Background(), userID, billID, groupID, 1)
	if !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("expected ErrForbidden for non-captain finalize, got %v", err)
	}

	after := histogramSampleCount(t, platformmetrics.BillFinalizeDuration, "error")
	if got, want := after, before+1; got != want {
		t.Errorf("finalize duration histogram sample count for outcome=error = %d, want %d", got, want)
	}
}

func TestReviewBill_Mismatch_RecordsMismatchBlockMetric(t *testing.T) {
	// covers: AC-7, AC-14 (paysplit_bill_mismatch_block_total counts a totals mismatch block at review)
	groupID := uuid.New()
	userID := uuid.New()
	creditorID := uuid.New()
	billID := uuid.New()

	repo := &mockServiceRepo{
		member: &repository.GroupMember{
			ID: creditorID, GroupID: groupID, UserID: userID, Role: "captain", Status: "active",
		},
		bill: &domain.Bill{
			ID: billID, GroupID: groupID, CreditorMemberID: creditorID,
			Status: domain.BillStatusDraft, Subtotal: 100000, Total: 120000, Version: 1, // mismatched total
			Items: []*domain.BillItem{
				{
					ID:          uuid.New(),
					LineTotal:   100000,
					Assignments: []*domain.BillItemAssignment{{MemberID: creditorID, Weight: "1"}},
				},
			},
		},
	}
	service := usecase.NewService(repo, &mockOCRProvider{}, &mockStorage{}, &mockProcessor{}, &mockEnqueuer{})

	before := testutil.ToFloat64(platformmetrics.BillMismatchBlockTotal.WithLabelValues("totals_mismatch"))

	_, err := service.ReviewBill(context.Background(), userID, billID, groupID, 1)
	if !errors.Is(err, domain.ErrBillNotReady) {
		t.Fatalf("expected ErrBillNotReady for total mismatch, got %v", err)
	}

	after := testutil.ToFloat64(platformmetrics.BillMismatchBlockTotal.WithLabelValues("totals_mismatch"))
	if got, want := after, before+1; got != want {
		t.Errorf("mismatch block counter for reason=totals_mismatch = %v, want %v", got, want)
	}
}

func TestApplyCandidate_Stale_RecordsStaleApplyMetric(t *testing.T) {
	// covers: AC-4, AC-14 (paysplit_ocr_stale_apply_total counts a stale candidate apply rejection)
	groupID := uuid.New()
	userID := uuid.New()
	memberID := uuid.New()
	billID := uuid.New()
	jobID := uuid.New()

	repo := &mockServiceRepo{
		member: &repository.GroupMember{
			ID: memberID, GroupID: groupID, UserID: userID, Role: "captain", Status: "active",
		},
		activeMembers: []*repository.GroupMember{
			{ID: memberID, GroupID: groupID, UserID: userID, Role: "captain", Status: "active"},
		},
		bill: &domain.Bill{
			ID: billID, GroupID: groupID, CreditorMemberID: memberID,
			Status: domain.BillStatusDraft, Version: 2, // bill moved on to version 2
		},
		ocrJob: &domain.OCRJob{
			ID: jobID, BillID: billID, Status: domain.OCRJobStatusSucceeded, Version: 1, // job started on version 1
			Candidate: &domain.OCRCandidate{
				Items:    []domain.OCRCandidateItem{{Name: "Item 1", Quantity: "1", UnitPrice: 50000, LineTotal: 50000}},
				Subtotal: 50000,
				Total:    50000,
			},
		},
	}
	service := usecase.NewService(repo, &mockOCRProvider{}, &mockStorage{}, &mockProcessor{}, &mockEnqueuer{})

	before := testutil.ToFloat64(platformmetrics.OCRStaleApplyTotal.WithLabelValues("bill_version_changed"))

	_, err := service.ApplyCandidate(context.Background(), userID, billID, groupID, jobID, 2)
	if err != domain.ErrOcrResultStale {
		t.Fatalf("expected ErrOcrResultStale, got %v", err)
	}

	after := testutil.ToFloat64(platformmetrics.OCRStaleApplyTotal.WithLabelValues("bill_version_changed"))
	if got, want := after, before+1; got != want {
		t.Errorf("stale apply counter for reason=bill_version_changed = %v, want %v", got, want)
	}
}

func TestGetBillDetail_DraftBill_RecordsPreviewDurationMetric(t *testing.T) {
	// covers: AC-6, AC-14 (paysplit_bill_preview_duration_seconds observes the draft preview calculation)
	groupID := uuid.New()
	userID := uuid.New()
	creditorID := uuid.New()
	billID := uuid.New()

	repo := &mockServiceRepo{
		member: &repository.GroupMember{
			ID: creditorID, GroupID: groupID, UserID: userID, Role: "captain", Status: "active",
		},
		bill: &domain.Bill{
			ID: billID, GroupID: groupID, CreditorMemberID: creditorID,
			Status: domain.BillStatusDraft, Subtotal: 100000, Total: 100000, Version: 1,
			Items: []*domain.BillItem{
				{
					ID:          uuid.New(),
					LineTotal:   100000,
					Assignments: []*domain.BillItemAssignment{{MemberID: creditorID, Weight: "1"}},
				},
			},
		},
	}
	service := usecase.NewService(repo, &mockOCRProvider{}, &mockStorage{}, &mockProcessor{}, &mockEnqueuer{})

	before := histogramSampleCount(t, platformmetrics.BillPreviewDuration, "success")

	detail, err := service.GetBillDetail(context.Background(), userID, billID, groupID)
	if err != nil {
		t.Fatalf("GetBillDetail() error = %v", err)
	}
	if len(detail.Breakdown) == 0 {
		t.Error("expected a nonempty preview breakdown for a draft bill")
	}

	after := histogramSampleCount(t, platformmetrics.BillPreviewDuration, "success")
	if got, want := after, before+1; got != want {
		t.Errorf("preview duration histogram sample count for outcome=success = %d, want %d", got, want)
	}
}

func TestGetBillDetail_FinalizedBill_DoesNotRecordPreviewDurationMetric(t *testing.T) {
	// covers: AC-14 (a finalized bill has an immutable breakdown, not a recomputed preview)
	groupID := uuid.New()
	userID := uuid.New()
	creditorID := uuid.New()
	billID := uuid.New()

	repo := &mockServiceRepo{
		member: &repository.GroupMember{
			ID: creditorID, GroupID: groupID, UserID: userID, Role: "captain", Status: "active",
		},
		bill: &domain.Bill{
			ID: billID, GroupID: groupID, CreditorMemberID: creditorID,
			Status: domain.BillStatusFinalized, Subtotal: 100000, Total: 100000, Version: 3,
		},
	}
	service := usecase.NewService(repo, &mockOCRProvider{}, &mockStorage{}, &mockProcessor{}, &mockEnqueuer{})

	before := histogramSampleCount(t, platformmetrics.BillPreviewDuration, "success")

	if _, err := service.GetBillDetail(context.Background(), userID, billID, groupID); err != nil {
		t.Fatalf("GetBillDetail() error = %v", err)
	}

	after := histogramSampleCount(t, platformmetrics.BillPreviewDuration, "success")
	if got, want := after, before; got != want {
		t.Errorf("preview duration histogram sample count changed for a finalized bill: before=%d after=%d", before, after)
	}
}

// mockNilReserveRepo mô phỏng một repository trả về (nil, nil) từ ReserveIdempotencyKey. Trước khi
// lớp chắn được thêm vào, usecase dereference thẳng con trỏ này và làm sập tiến trình.
type mockNilReserveRepo struct {
	repository.Repository
}

func (m *mockNilReserveRepo) ReserveIdempotencyKey(
	ctx context.Context,
	params repository.ReserveIdempotencyParams,
) (*repository.IdempotencyRecord, error) {
	return nil, nil
}

func TestCheckOrReserveIdempotency_NilRecord_ReturnsErrorNotPanic(t *testing.T) {
	service := usecase.NewService(&mockNilReserveRepo{}, &mockOCRProvider{}, &mockStorage{}, &mockProcessor{}, &mockEnqueuer{})

	rec, err := service.CheckOrReserveIdempotency(
		context.Background(),
		uuid.New(),
		"bill.create",
		"key-abc",
		[]byte(`{"a":1}`),
	)

	if rec != nil {
		t.Fatalf("mong đợi record nil, nhận %+v", rec)
	}
	if !errors.Is(err, domain.ErrIdempotencyInProgress) {
		t.Fatalf("mong đợi ErrIdempotencyInProgress, nhận %v", err)
	}
}

// TestCreateBill_ItemDiscount_ComputesFinalPriceAndTotals khớp Spec 3 AC-20: POST /bills nhận
// discount_amount theo món, tự tính final_price và total_item_discount/general_discount.
func TestCreateBill_ItemDiscount_ComputesFinalPriceAndTotals(t *testing.T) {
	groupID := uuid.New()
	userID := uuid.New()
	captainMemberID := uuid.New()

	repo := &mockServiceRepo{
		member: &repository.GroupMember{
			ID:      captainMemberID,
			GroupID: groupID,
			UserID:  userID,
			Role:    "captain",
			Status:  "active",
		},
	}

	service := usecase.NewService(repo, &mockOCRProvider{}, &mockStorage{}, &mockProcessor{}, &mockEnqueuer{})

	res, err := service.CreateBill(context.Background(), userID, usecase.CreateBillRequest{
		GroupID:  groupID,
		Discount: 80000, // 50000 theo món (Bò bít tết) + 30000 giảm giá chung
		Total:    170000,
		Items: []usecase.CreateBillItemRequest{
			{
				Name:           "Bò bít tết",
				LineTotal:      250000,
				DiscountAmount: 50000,
				Assignments: []usecase.CreateItemAssignmentRequest{
					{MemberID: captainMemberID, Weight: "1"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateBill() unexpected error = %v", err)
	}
	if len(res.Bill.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(res.Bill.Items))
	}
	item := res.Bill.Items[0]
	if item.DiscountAmount != 50000 || item.FinalPrice != 200000 {
		t.Errorf("expected discount_amount 50000 and final_price 200000, got %d/%d", item.DiscountAmount, item.FinalPrice)
	}
	if res.Bill.TotalItemDiscount != 50000 {
		t.Errorf("expected total_item_discount 50000, got %d", res.Bill.TotalItemDiscount)
	}
	if res.Bill.GeneralDiscount != 30000 {
		t.Errorf("expected general_discount 30000, got %d", res.Bill.GeneralDiscount)
	}
}

// TestCreateBill_ItemDiscountExceedsLineTotal_ReturnsInvalidInput khớp Spec 3 AC-19/AC-20: validate
// discount_amount dựa trên line_total đã suy ra (unit_price × quantity), không phải giá trị thô.
func TestCreateBill_ItemDiscountExceedsLineTotal_ReturnsInvalidInput(t *testing.T) {
	groupID := uuid.New()
	userID := uuid.New()
	captainMemberID := uuid.New()

	repo := &mockServiceRepo{
		member: &repository.GroupMember{
			ID:      captainMemberID,
			GroupID: groupID,
			UserID:  userID,
			Role:    "captain",
			Status:  "active",
		},
	}

	service := usecase.NewService(repo, &mockOCRProvider{}, &mockStorage{}, &mockProcessor{}, &mockEnqueuer{})

	_, err := service.CreateBill(context.Background(), userID, usecase.CreateBillRequest{
		GroupID: groupID,
		Total:   0,
		Items: []usecase.CreateBillItemRequest{
			{
				// line_total gửi lên là 0, nhưng giá trị dùng để validate phải là giá trị suy ra
				// 100000 x 2 = 200000, nên discount_amount 250000 phải bị từ chối.
				Name:           "Trà sữa",
				LineTotal:      0,
				UnitPrice:      100000,
				Quantity:       "2",
				DiscountAmount: 250000,
				Assignments: []usecase.CreateItemAssignmentRequest{
					{MemberID: captainMemberID, Weight: "1"},
				},
			},
		},
	})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput for discount_amount exceeding derived line_total, got %v", err)
	}
}

// TestCreateBill_DiscountCompositionBug_NoLongerFails khớp Spec 3 AC-21: trước đây CreateBill
// không set total_item_discount/general_discount, khiến discount > 0 vi phạm
// check_bills_discount_composition ở tầng database (500 không kiểm soát trên DB thật). Ở tầng
// usecase, hồi quy được xác nhận qua việc general_discount giờ được tính đúng thay vì bỏ trống.
func TestCreateBill_DiscountCompositionBug_NoLongerFails(t *testing.T) {
	groupID := uuid.New()
	userID := uuid.New()
	captainMemberID := uuid.New()

	repo := &mockServiceRepo{
		member: &repository.GroupMember{
			ID:      captainMemberID,
			GroupID: groupID,
			UserID:  userID,
			Role:    "captain",
			Status:  "active",
		},
	}

	service := usecase.NewService(repo, &mockOCRProvider{}, &mockStorage{}, &mockProcessor{}, &mockEnqueuer{})

	res, err := service.CreateBill(context.Background(), userID, usecase.CreateBillRequest{
		GroupID:  groupID,
		Discount: 20000,
		Total:    30000,
		Items: []usecase.CreateBillItemRequest{
			{
				Name:      "Cà phê",
				LineTotal: 50000,
				Assignments: []usecase.CreateItemAssignmentRequest{
					{MemberID: captainMemberID, Weight: "1"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateBill() unexpected error = %v", err)
	}
	if res.Bill.TotalItemDiscount != 0 {
		t.Errorf("expected total_item_discount 0, got %d", res.Bill.TotalItemDiscount)
	}
	if res.Bill.GeneralDiscount != 20000 {
		t.Errorf("expected general_discount 20000 (= discount, no item discount), got %d", res.Bill.GeneralDiscount)
	}
	if res.Bill.Discount != res.Bill.TotalItemDiscount+res.Bill.GeneralDiscount {
		t.Errorf("discount composition invariant broken: discount=%d, total_item_discount=%d, general_discount=%d",
			res.Bill.Discount, res.Bill.TotalItemDiscount, res.Bill.GeneralDiscount)
	}
}

// TestUpdateDraftBill_ItemDiscount_RoundTrips khớp Spec 3 AC-19: PUT /bills/{id} nhận lại
// discount_amount theo món khi người dùng gán lại (reassign) món cho đúng thành viên, không còn
// bị quy hết về general_discount như hành vi cũ.
func TestUpdateDraftBill_ItemDiscount_RoundTrips(t *testing.T) {
	groupID := uuid.New()
	userID := uuid.New()
	captainMemberID := uuid.New()
	member2ID := uuid.New()
	billID := uuid.New()

	repo := &mockServiceRepo{
		member: &repository.GroupMember{
			ID:      captainMemberID,
			GroupID: groupID,
			UserID:  userID,
			Role:    "captain",
			Status:  "active",
		},
		bill: &domain.Bill{
			ID:               billID,
			GroupID:          groupID,
			CreditorMemberID: captainMemberID,
			Status:           domain.BillStatusDraft,
			Version:          1,
		},
	}

	service := usecase.NewService(repo, &mockOCRProvider{}, &mockStorage{}, &mockProcessor{}, &mockEnqueuer{})

	updated, err := service.UpdateDraftBill(context.Background(), userID, billID, groupID, usecase.UpdateDraftRequest{
		Version:  1,
		Discount: 50000,
		Total:    200000,
		Items: []usecase.CreateBillItemRequest{
			{
				Name:           "Bò bít tết",
				LineTotal:      250000,
				DiscountAmount: 50000,
				Assignments: []usecase.CreateItemAssignmentRequest{
					{MemberID: captainMemberID, Weight: "1"}, // gán lại cho creditor thay vì chia đều
				},
			},
			{
				Name:      "Nước ngọt",
				LineTotal: 30000,
				Assignments: []usecase.CreateItemAssignmentRequest{
					{MemberID: member2ID, Weight: "1"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("UpdateDraftBill() unexpected error = %v", err)
	}
	if updated.TotalItemDiscount != 50000 {
		t.Errorf("expected total_item_discount 50000 to survive the manual edit, got %d", updated.TotalItemDiscount)
	}
	if updated.GeneralDiscount != 0 {
		t.Errorf("expected general_discount 0, got %d", updated.GeneralDiscount)
	}
	if len(updated.Items) != 2 || updated.Items[0].FinalPrice != 200000 {
		t.Fatalf("expected item 0 final_price 200000, got %+v", updated.Items)
	}
}

// TestCreateBill_NegativeItemDiscount_ReturnsInvalidInput khớp Spec 3 AC-19/AC-20: discount_amount
// âm phải bị từ chối, không chỉ trường hợp vượt line_total.
func TestCreateBill_NegativeItemDiscount_ReturnsInvalidInput(t *testing.T) {
	groupID := uuid.New()
	userID := uuid.New()
	captainMemberID := uuid.New()

	repo := &mockServiceRepo{
		member: &repository.GroupMember{
			ID:      captainMemberID,
			GroupID: groupID,
			UserID:  userID,
			Role:    "captain",
			Status:  "active",
		},
	}

	service := usecase.NewService(repo, &mockOCRProvider{}, &mockStorage{}, &mockProcessor{}, &mockEnqueuer{})

	_, err := service.CreateBill(context.Background(), userID, usecase.CreateBillRequest{
		GroupID: groupID,
		Total:   50000,
		Items: []usecase.CreateBillItemRequest{
			{
				Name:           "Item 1",
				LineTotal:      50000,
				DiscountAmount: -1,
				Assignments: []usecase.CreateItemAssignmentRequest{
					{MemberID: captainMemberID, Weight: "1"},
				},
			},
		},
	})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput for negative discount_amount, got %v", err)
	}
}

// TestCreateBill_ItemDiscount_ErrorIdentifiesFailingItemIndex khớp Spec 3 AC-19: thông báo lỗi
// phải chỉ đúng item nào sai khi có nhiều món, không phải luôn báo item 0.
func TestCreateBill_ItemDiscount_ErrorIdentifiesFailingItemIndex(t *testing.T) {
	groupID := uuid.New()
	userID := uuid.New()
	captainMemberID := uuid.New()

	repo := &mockServiceRepo{
		member: &repository.GroupMember{
			ID:      captainMemberID,
			GroupID: groupID,
			UserID:  userID,
			Role:    "captain",
			Status:  "active",
		},
	}

	service := usecase.NewService(repo, &mockOCRProvider{}, &mockStorage{}, &mockProcessor{}, &mockEnqueuer{})

	_, err := service.CreateBill(context.Background(), userID, usecase.CreateBillRequest{
		GroupID: groupID,
		Total:   80000,
		Items: []usecase.CreateBillItemRequest{
			{
				Name:      "Item hợp lệ",
				LineTotal: 30000,
				Assignments: []usecase.CreateItemAssignmentRequest{
					{MemberID: captainMemberID, Weight: "1"},
				},
			},
			{
				// Món thứ hai (index 1) mới là món sai, thông báo lỗi phải nêu đúng index này.
				Name:           "Item lỗi",
				LineTotal:      50000,
				DiscountAmount: 999999,
				Assignments: []usecase.CreateItemAssignmentRequest{
					{MemberID: captainMemberID, Weight: "1"},
				},
			},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "item 1") {
		t.Errorf("expected error message to identify item 1 as the failing item, got %v", err)
	}
}

// TestUpdateDraftBill_ItemDiscountExceedsLineTotal_ReturnsInvalidInput khớp Spec 3 AC-19: PUT
// cũng phải áp dụng đúng validate discount_amount theo món như POST.
func TestUpdateDraftBill_ItemDiscountExceedsLineTotal_ReturnsInvalidInput(t *testing.T) {
	groupID := uuid.New()
	userID := uuid.New()
	captainMemberID := uuid.New()
	billID := uuid.New()

	repo := &mockServiceRepo{
		member: &repository.GroupMember{
			ID:      captainMemberID,
			GroupID: groupID,
			UserID:  userID,
			Role:    "captain",
			Status:  "active",
		},
		bill: &domain.Bill{
			ID:               billID,
			GroupID:          groupID,
			CreditorMemberID: captainMemberID,
			Status:           domain.BillStatusDraft,
			Version:          1,
		},
	}

	service := usecase.NewService(repo, &mockOCRProvider{}, &mockStorage{}, &mockProcessor{}, &mockEnqueuer{})

	_, err := service.UpdateDraftBill(context.Background(), userID, billID, groupID, usecase.UpdateDraftRequest{
		Version: 1,
		Total:   0,
		Items: []usecase.CreateBillItemRequest{
			{
				Name:           "Item lỗi",
				LineTotal:      50000,
				DiscountAmount: 60000,
				Assignments: []usecase.CreateItemAssignmentRequest{
					{MemberID: captainMemberID, Weight: "1"},
				},
			},
		},
	})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput for discount_amount exceeding line_total in UpdateDraftBill, got %v", err)
	}
}

// TestUpdateDraftBill_GeneralDiscountNegative_ReturnsInvalidInput khớp Spec 3 AC-19: nếu tổng
// discount_amount theo món vượt quá bill.discount khai báo, general_discount âm phải bị từ chối.
func TestUpdateDraftBill_GeneralDiscountNegative_ReturnsInvalidInput(t *testing.T) {
	groupID := uuid.New()
	userID := uuid.New()
	captainMemberID := uuid.New()
	billID := uuid.New()

	repo := &mockServiceRepo{
		member: &repository.GroupMember{
			ID:      captainMemberID,
			GroupID: groupID,
			UserID:  userID,
			Role:    "captain",
			Status:  "active",
		},
		bill: &domain.Bill{
			ID:               billID,
			GroupID:          groupID,
			CreditorMemberID: captainMemberID,
			Status:           domain.BillStatusDraft,
			Version:          1,
		},
	}

	service := usecase.NewService(repo, &mockOCRProvider{}, &mockStorage{}, &mockProcessor{}, &mockEnqueuer{})

	_, err := service.UpdateDraftBill(context.Background(), userID, billID, groupID, usecase.UpdateDraftRequest{
		Version:  1,
		Discount: 10000, // nhỏ hơn tổng discount_amount theo món (20000)
		Total:    0,
		Items: []usecase.CreateBillItemRequest{
			{
				Name:           "Item 1",
				LineTotal:      50000,
				DiscountAmount: 20000,
				Assignments: []usecase.CreateItemAssignmentRequest{
					{MemberID: captainMemberID, Weight: "1"},
				},
			},
		},
	})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput for general_discount going negative, got %v", err)
	}
}
