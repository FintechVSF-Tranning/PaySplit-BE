package jobs_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"paysplit-backend/internal/modules/bill/domain"
	"paysplit-backend/internal/modules/bill/jobs"
	"paysplit-backend/internal/modules/bill/repository"
)

// MockRepo triển khai repository.Repository cho unit test
type mockRepo struct {
	repository.Repository
	ocrJob     *domain.OCRJob
	bill       *domain.Bill
	updated    bool
	success    bool
	failed     bool
	failReason string
}

func (m *mockRepo) GetOCRJobByID(ctx context.Context, id uuid.UUID) (*domain.OCRJob, error) {
	if m.ocrJob != nil && m.ocrJob.ID == id {
		return m.ocrJob, nil
	}
	return nil, domain.ErrOcrJobNotFound
}

func (m *mockRepo) GetBillByID(ctx context.Context, id, groupID uuid.UUID) (*domain.Bill, error) {
	if m.bill != nil && m.bill.ID == id {
		return m.bill, nil
	}
	return nil, domain.ErrBillNotFound
}

func (m *mockRepo) UpdateOCRJobProcessing(ctx context.Context, id uuid.UUID, version int32) error {
	m.updated = true
	return nil
}

func (m *mockRepo) UpdateOCRJobSuccess(ctx context.Context, id uuid.UUID, version int32, candidate *domain.OCRCandidate, raw []byte) error {
	m.success = true
	return nil
}

func (m *mockRepo) UpdateOCRJobFailed(ctx context.Context, id uuid.UUID, version int32, errReason string) error {
	m.failed = true
	m.failReason = errReason
	return nil
}

// MockStorage triển khai usecase.BillStorage cho unit test
type mockStorage struct {
	downloadBytes []byte
	downloadErr   error
}

func (m *mockStorage) Upload(ctx context.Context, data []byte, publicID string) (string, error) {
	return publicID, nil
}
func (m *mockStorage) SignedURL(publicID string, ttl time.Duration) (string, error) {
	return "https://signed.url", nil
}
func (m *mockStorage) Download(ctx context.Context, publicID string) ([]byte, error) {
	return m.downloadBytes, m.downloadErr
}
func (m *mockStorage) Delete(ctx context.Context, publicID string) error {
	return nil
}
func (m *mockStorage) DeleteByPrefix(ctx context.Context, prefix string) error {
	return nil
}

// MockOCRProvider triển khai usecase.OCRProvider cho unit test
type mockOCRProvider struct {
	candidate *domain.OCRCandidate
	rawJSON   []byte
	err       error
}

func (m *mockOCRProvider) ExtractReceipt(ctx context.Context, imageBytes []byte, mimeType string) (*domain.OCRCandidate, []byte, error) {
	return m.candidate, m.rawJSON, m.err
}

type mockBroadcaster struct {
	events []string
}

func (m *mockBroadcaster) Broadcast(billID uuid.UUID, eventType string, data any) {
	m.events = append(m.events, eventType)
}

func TestOCRWorker_Success(t *testing.T) {
	billID := uuid.New()
	jobID := uuid.New()
	groupID := uuid.New()

	repo := &mockRepo{
		ocrJob: &domain.OCRJob{
			ID:      jobID,
			BillID:  billID,
			Status:  domain.OCRJobStatusQueued,
			Version: 1,
		},
		bill: &domain.Bill{
			ID:      billID,
			GroupID: groupID,
			Images: []*domain.BillImage{
				{
					ID:       uuid.New(),
					BillID:   billID,
					ImageKey: "bills/op-1/0",
					Position: 0,
				},
			},
		},
	}

	storage := &mockStorage{
		downloadBytes: []byte("fake-image-bytes"),
	}

	provider := &mockOCRProvider{
		candidate: &domain.OCRCandidate{
			Total:    100000,
			Subtotal: 100000,
		},
		rawJSON: []byte(`{"total": 100000}`),
	}

	broadcaster := &mockBroadcaster{}
	worker := jobs.NewOCRWorker(repo, storage, provider, broadcaster, 5*time.Second)

	job := &river.Job[jobs.OCRJobArgs]{
		JobRow: &rivertype.JobRow{
			Attempt:     1,
			MaxAttempts: 3,
		},
		Args: jobs.OCRJobArgs{
			BillID:  billID.String(),
			JobID:   jobID.String(),
			GroupID: groupID.String(),
		},
	}

	err := worker.Work(context.Background(), job)
	if err != nil {
		t.Fatalf("Work() error = %v", err)
	}

	if !repo.updated {
		t.Error("expected job to be updated to processing")
	}
	if !repo.success {
		t.Error("expected job to be marked as succeeded")
	}
	if len(broadcaster.events) < 2 {
		t.Errorf("expected broadcast events (processing, ocr.updated), got %+v", broadcaster.events)
	}
}

func TestOCRWorker_AlreadyCompleted(t *testing.T) {
	billID := uuid.New()
	jobID := uuid.New()
	groupID := uuid.New()

	repo := &mockRepo{
		ocrJob: &domain.OCRJob{
			ID:      jobID,
			BillID:  billID,
			Status:  domain.OCRJobStatusSucceeded,
			Version: 2,
		},
	}

	worker := jobs.NewOCRWorker(repo, &mockStorage{}, &mockOCRProvider{}, nil, 5*time.Second)

	job := &river.Job[jobs.OCRJobArgs]{
		Args: jobs.OCRJobArgs{
			BillID:  billID.String(),
			JobID:   jobID.String(),
			GroupID: groupID.String(),
		},
	}

	err := worker.Work(context.Background(), job)
	if err != nil {
		t.Fatalf("expected nil for already completed job, got %v", err)
	}

	if repo.updated {
		t.Error("expected completed job to not be updated to processing")
	}
}

func TestOCRWorker_SchemaInvalid_FailsWithoutRetry(t *testing.T) {
	billID := uuid.New()
	jobID := uuid.New()
	groupID := uuid.New()

	repo := &mockRepo{
		ocrJob: &domain.OCRJob{
			ID:      jobID,
			BillID:  billID,
			Status:  domain.OCRJobStatusQueued,
			Version: 1,
		},
		bill: &domain.Bill{
			ID:      billID,
			GroupID: groupID,
			Images: []*domain.BillImage{
				{ID: uuid.New(), BillID: billID, ImageKey: "bills/op-1/0", Position: 0},
			},
		},
	}

	storage := &mockStorage{downloadBytes: []byte("fake-image")}
	provider := &mockOCRProvider{err: domain.ErrOcrSchemaInvalid}
	broadcaster := &mockBroadcaster{}

	worker := jobs.NewOCRWorker(repo, storage, provider, broadcaster, 5*time.Second)

	job := &river.Job[jobs.OCRJobArgs]{
		JobRow: &rivertype.JobRow{
			Attempt:     1,
			MaxAttempts: 3,
		},
		Args: jobs.OCRJobArgs{
			BillID:  billID.String(),
			JobID:   jobID.String(),
			GroupID: groupID.String(),
		},
	}

	err := worker.Work(context.Background(), job)
	if err != nil {
		t.Fatalf("expected nil return so River does not retry schema invalid error, got %v", err)
	}

	if !repo.failed {
		t.Error("expected job to be marked as failed in DB")
	}
}

func TestOCRWorker_DownloadError_ReturnsErrorForRetry(t *testing.T) {
	billID := uuid.New()
	jobID := uuid.New()
	groupID := uuid.New()

	repo := &mockRepo{
		ocrJob: &domain.OCRJob{
			ID:      jobID,
			BillID:  billID,
			Status:  domain.OCRJobStatusQueued,
			Version: 1,
		},
		bill: &domain.Bill{
			ID:      billID,
			GroupID: groupID,
			Images: []*domain.BillImage{
				{ID: uuid.New(), BillID: billID, ImageKey: "bills/op-1/0", Position: 0},
			},
		},
	}

	storage := &mockStorage{downloadErr: errors.New("network timeout")}
	provider := &mockOCRProvider{}

	worker := jobs.NewOCRWorker(repo, storage, provider, nil, 5*time.Second)

	job := &river.Job[jobs.OCRJobArgs]{
		JobRow: &rivertype.JobRow{
			Attempt:     1,
			MaxAttempts: 3,
		},
		Args: jobs.OCRJobArgs{
			BillID:  billID.String(),
			JobID:   jobID.String(),
			GroupID: groupID.String(),
		},
	}

	err := worker.Work(context.Background(), job)
	if err == nil {
		t.Fatal("expected error return so River can retry network failure")
	}
}

func TestOCRWorker_MultipleImagesStitched_Success(t *testing.T) {
	// covers: AC-1, AC-3 (Multi-image stitching for multi-page bills)
	billID := uuid.New()
	jobID := uuid.New()
	groupID := uuid.New()

	repo := &mockRepo{
		ocrJob: &domain.OCRJob{
			ID:      jobID,
			BillID:  billID,
			Status:  domain.OCRJobStatusQueued,
			Version: 1,
		},
		bill: &domain.Bill{
			ID:      billID,
			GroupID: groupID,
			Images: []*domain.BillImage{
				{ID: uuid.New(), BillID: billID, ImageKey: "bills/op-1/0", Position: 0},
				{ID: uuid.New(), BillID: billID, ImageKey: "bills/op-1/1", Position: 1},
			},
		},
	}

	storage := &mockStorage{
		downloadBytes: []byte("fake-image-bytes"),
	}

	provider := &mockOCRProvider{
		candidate: &domain.OCRCandidate{
			Total: 250000,
		},
		rawJSON: []byte(`{"total": 250000}`),
	}

	broadcaster := &mockBroadcaster{}
	worker := jobs.NewOCRWorker(repo, storage, provider, broadcaster, 5*time.Second)

	job := &river.Job[jobs.OCRJobArgs]{
		JobRow: &rivertype.JobRow{
			Attempt:     1,
			MaxAttempts: 3,
		},
		Args: jobs.OCRJobArgs{
			BillID:  billID.String(),
			JobID:   jobID.String(),
			GroupID: groupID.String(),
		},
	}

	err := worker.Work(context.Background(), job)
	if err != nil {
		t.Fatalf("Work() error = %v", err)
	}

	if !repo.success {
		t.Error("expected multi-image job to succeed")
	}
}

