package jobs_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"paysplit-backend/internal/modules/bill/domain"
	"paysplit-backend/internal/modules/bill/jobs"
	"paysplit-backend/internal/modules/bill/repository"
	platformmetrics "paysplit-backend/internal/platform/metrics"
)

// MockRepo triển khai repository.Repository cho unit test
type mockRepo struct {
	repository.Repository
	ocrJob           *domain.OCRJob
	bill             *domain.Bill
	updated          bool
	success          bool
	failed           bool
	failReason       string
	purgedOlderThan  time.Duration
	purgeCalled      bool
	activeOCRJobs    int64
	activeOCRJobsErr error
}

func (m *mockRepo) PurgeExpiredRawOCRResponses(ctx context.Context, olderThan time.Duration) (int64, error) {
	m.purgeCalled = true
	m.purgedOlderThan = olderThan
	return 0, nil
}

func (m *mockRepo) CountActiveOCRJobs(ctx context.Context) (int64, error) {
	if m.activeOCRJobsErr != nil {
		return 0, m.activeOCRJobsErr
	}
	return m.activeOCRJobs, nil
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

func (m *mockRepo) UpdateOCRJobProcessing(ctx context.Context, id uuid.UUID) error {
	m.updated = true
	return nil
}

func (m *mockRepo) UpdateOCRJobSuccess(ctx context.Context, id uuid.UUID, candidate *domain.OCRCandidate, raw []byte) error {
	m.success = true
	return nil
}

func (m *mockRepo) UpdateOCRJobFailed(ctx context.Context, id uuid.UUID, errReason string) error {
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

func TestOCRWorker_DownloadError_MaxAttempts_FailsWithCleanedCodeNotRawError(t *testing.T) {
	// covers: AC-14, security model ("raw provider content never appears in ... logs";
	// AC-14.7 metric labels must never contain internals). The raw error text below deliberately
	// looks like something that must never reach ocr_jobs.error_message, SSE, or a Prometheus label.
	billID := uuid.New()
	jobID := uuid.New()
	groupID := uuid.New()

	rawErr := errors.New("dial tcp res.cloudinary.com:443: connect: signature=abc123&api_key=sk_live_deadbeef")

	repo := &mockRepo{
		ocrJob: &domain.OCRJob{
			ID: jobID, BillID: billID, Status: domain.OCRJobStatusQueued, Version: 1,
		},
		bill: &domain.Bill{
			ID: billID, GroupID: groupID,
			Images: []*domain.BillImage{{ID: uuid.New(), BillID: billID, ImageKey: "bills/op-1/0", Position: 0}},
		},
	}
	storage := &mockStorage{downloadErr: rawErr}
	broadcaster := &mockBroadcaster{}
	worker := jobs.NewOCRWorker(repo, storage, &mockOCRProvider{}, broadcaster, 5*time.Second)

	job := &river.Job[jobs.OCRJobArgs]{
		JobRow: &rivertype.JobRow{Attempt: 3, MaxAttempts: 3}, // last attempt -> failJob path
		Args: jobs.OCRJobArgs{
			BillID: billID.String(), JobID: jobID.String(), GroupID: groupID.String(),
		},
	}

	before := testutil.ToFloat64(platformmetrics.OCRJobsTotal.WithLabelValues("failed", "download_failed"))

	if err := worker.Work(context.Background(), job); err != nil {
		t.Fatalf("Work() error = %v, want nil (terminal failJob path)", err)
	}

	if repo.failReason != "download_failed" {
		t.Errorf("ocr_jobs.error_message = %q, want the bounded code %q, not the raw provider error", repo.failReason, "download_failed")
	}

	after := testutil.ToFloat64(platformmetrics.OCRJobsTotal.WithLabelValues("failed", "download_failed"))
	if got, want := after, before+1; got != want {
		t.Errorf("paysplit_ocr_jobs_total{state=failed,error_code=download_failed} = %v, want %v", got, want)
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

func TestOCRWorker_MissingDependencies_ReturnsError(t *testing.T) {
	worker := jobs.NewOCRWorker(nil, nil, nil, nil, 5*time.Second)

	job := &river.Job[jobs.OCRJobArgs]{
		Args: jobs.OCRJobArgs{
			BillID:  uuid.New().String(),
			JobID:   uuid.New().String(),
			GroupID: uuid.New().String(),
		},
	}

	err := worker.Work(context.Background(), job)
	if err == nil {
		t.Fatal("expected error when OCRWorker dependencies are nil, got nil")
	}
}

func TestOCRWorker_NextRetry_UsesConfiguredBaseDelay(t *testing.T) {
	// covers: AC-3 (provider retries must use the configured retry base delay, not River's default ATTEMPT^4 schedule)
	worker := jobs.NewOCRWorker(&mockRepo{}, &mockStorage{}, &mockOCRProvider{}, nil, 5*time.Second)
	worker.SetRetryBaseDelay(10 * time.Second)

	before := time.Now()
	next := worker.NextRetry(&river.Job[jobs.OCRJobArgs]{JobRow: &rivertype.JobRow{Attempt: 1}})
	delay := next.Sub(before)

	if delay < 9*time.Second || delay > 11*time.Second {
		t.Errorf("NextRetry() for attempt 1 with a 10s base delay = %v from now, want ~10s", delay)
	}

	next2 := worker.NextRetry(&river.Job[jobs.OCRJobArgs]{JobRow: &rivertype.JobRow{Attempt: 2}})
	delay2 := next2.Sub(before)
	if delay2 < 19*time.Second || delay2 > 21*time.Second {
		t.Errorf("NextRetry() for attempt 2 with a 10s base delay = %v from now, want ~20s (exponential)", delay2)
	}
}

func TestOCRWorker_NextRetry_NoConfiguredDelay_FallsBackToOneSecondBase(t *testing.T) {
	// covers: AC-3 (an unset retry base delay must not panic or produce a zero/negative delay)
	worker := jobs.NewOCRWorker(&mockRepo{}, &mockStorage{}, &mockOCRProvider{}, nil, 5*time.Second)

	before := time.Now()
	next := worker.NextRetry(&river.Job[jobs.OCRJobArgs]{JobRow: &rivertype.JobRow{Attempt: 1}})

	if next.Sub(before) < time.Second || next.Sub(before) > 2*time.Second {
		t.Errorf("NextRetry() with no configured base delay = %v from now, want ~1s default", next.Sub(before))
	}
}

func TestPollQueueDepth_SetsGaugeFromDBCount(t *testing.T) {
	// covers: AC-14 (paysplit_ocr_queue_depth is set from a direct DB count, correct across
	// restarts, rollbacks and replicas, rather than an in-process running total)
	repo := &mockRepo{activeOCRJobs: 3}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // stop after the immediate first poll, before any ticker fires
	jobs.PollQueueDepth(ctx, repo, time.Hour)

	if got, want := testutil.ToFloat64(platformmetrics.OCRQueueDepth), 3.0; got != want {
		t.Errorf("paysplit_ocr_queue_depth = %v, want %v", got, want)
	}
}

func TestPollQueueDepth_RepoError_LeavesGaugeUnchanged(t *testing.T) {
	// covers: AC-14 (a transient DB error during polling must not zero out or corrupt the gauge)
	platformmetrics.SetOCRQueueDepth(5)
	t.Cleanup(func() { platformmetrics.SetOCRQueueDepth(0) })

	repo := &mockRepo{activeOCRJobsErr: errors.New("db unavailable")}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	jobs.PollQueueDepth(ctx, repo, time.Hour)

	if got, want := testutil.ToFloat64(platformmetrics.OCRQueueDepth), 5.0; got != want {
		t.Errorf("paysplit_ocr_queue_depth = %v, want unchanged %v after a poll error", got, want)
	}
}

func TestOCRRetentionWorker_Work_PurgesUsingConfiguredRetention(t *testing.T) {
	// covers: AC-3, security model ("raw provider responses removed 30 days after job completion")
	repo := &mockRepo{}
	worker := jobs.NewOCRRetentionWorker(repo)

	job := &river.Job[jobs.OCRRetentionJobArgs]{
		Args: jobs.OCRRetentionJobArgs{OlderThanHours: 720}, // 30 days, matching the configured BILL_OCR_RAW_RETENTION_DAYS
	}

	if err := worker.Work(context.Background(), job); err != nil {
		t.Fatalf("Work() error = %v", err)
	}

	if !repo.purgeCalled {
		t.Fatal("expected PurgeExpiredRawOCRResponses to be called")
	}
	if repo.purgedOlderThan != 720*time.Hour {
		t.Errorf("purge retention = %v, want %v", repo.purgedOlderThan, 720*time.Hour)
	}
}

func TestOCRRetentionWorker_Work_ZeroOlderThanHours_FallsBackToThirtyDays(t *testing.T) {
	// covers: AC-3 (a misconfigured or zero-value periodic job arg must not disable retention entirely)
	repo := &mockRepo{}
	worker := jobs.NewOCRRetentionWorker(repo)

	job := &river.Job[jobs.OCRRetentionJobArgs]{Args: jobs.OCRRetentionJobArgs{OlderThanHours: 0}}

	if err := worker.Work(context.Background(), job); err != nil {
		t.Fatalf("Work() error = %v", err)
	}

	if repo.purgedOlderThan != 30*24*time.Hour {
		t.Errorf("purge retention = %v, want the 30 day default", repo.purgedOlderThan)
	}
}
