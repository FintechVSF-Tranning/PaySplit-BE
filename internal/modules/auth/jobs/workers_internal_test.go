package jobs

// White box test file (package jobs, not jobs_test): cleanupMedia is unexported and
// Run() only drives it off real tickers, so calling it directly is the deterministic way
// to test it without sleeping on wall clock time (Spec 3 AC-13, AC-14).

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"paysplit-backend/internal/modules/auth/domain"
	platformmetrics "paysplit-backend/internal/platform/metrics"
)

type fakeCleanupRepo struct {
	jobs         []domain.MediaCleanupJob
	completedIDs []string
	failedIDs    []string
	failReasons  []string
	completeErr  error
	failErr      error
}

func (f *fakeCleanupRepo) CleanupExpiredAuth(ctx context.Context, before time.Time, limit int) (int64, error) {
	return 0, nil
}
func (f *fakeCleanupRepo) ClaimMediaCleanup(ctx context.Context, now time.Time, limit int) ([]domain.MediaCleanupJob, error) {
	return f.jobs, nil
}
func (f *fakeCleanupRepo) CompleteMediaCleanup(ctx context.Context, id string, at time.Time) error {
	f.completedIDs = append(f.completedIDs, id)
	return f.completeErr
}
func (f *fakeCleanupRepo) FailMediaCleanup(ctx context.Context, id, reason string, retryAt time.Time) error {
	f.failedIDs = append(f.failedIDs, id)
	f.failReasons = append(f.failReasons, reason)
	return f.failErr
}

type fakeCleanupStorage struct {
	deleteErr error
}

func (f *fakeCleanupStorage) Delete(ctx context.Context, objectKey string) error {
	return f.deleteErr
}

func TestCleanupMedia_DeleteFails_RecordsMediaCleanupFailureMetric(t *testing.T) {
	// covers: AC-13, AC-14 (paysplit_media_cleanup_failures_total increments on a Cloudinary delete failure)
	repo := &fakeCleanupRepo{
		jobs: []domain.MediaCleanupJob{{ID: "job-1", ObjectKey: "bills/op-1/0", AttemptCount: 1}},
	}
	storage := &fakeCleanupStorage{deleteErr: errFakeDelete}
	w := New(repo, storage, time.Hour, time.Hour, time.Hour)

	before := testutil.ToFloat64(platformmetrics.MediaCleanupFailuresTotal.WithLabelValues("delete_failed"))

	w.cleanupMedia(context.Background(), time.Now())

	after := testutil.ToFloat64(platformmetrics.MediaCleanupFailuresTotal.WithLabelValues("delete_failed"))
	if got, want := after, before+1; got != want {
		t.Errorf("media cleanup failure counter for reason=delete_failed = %v, want %v", got, want)
	}
	if len(repo.failedIDs) != 1 || repo.failedIDs[0] != "job-1" {
		t.Errorf("expected job-1 to be marked failed for retry, got %v", repo.failedIDs)
	}
	if len(repo.completedIDs) != 0 {
		t.Errorf("expected no job marked complete when delete fails, got %v", repo.completedIDs)
	}
}

func TestCleanupMedia_DeleteSucceeds_DoesNotRecordFailureMetric(t *testing.T) {
	// covers: AC-13, AC-14 (a successful delete must not increment the failure counter)
	repo := &fakeCleanupRepo{
		jobs: []domain.MediaCleanupJob{{ID: "job-2", ObjectKey: "bills/op-2/0", AttemptCount: 1}},
	}
	storage := &fakeCleanupStorage{}
	w := New(repo, storage, time.Hour, time.Hour, time.Hour)

	before := testutil.ToFloat64(platformmetrics.MediaCleanupFailuresTotal.WithLabelValues("delete_failed"))

	w.cleanupMedia(context.Background(), time.Now())

	after := testutil.ToFloat64(platformmetrics.MediaCleanupFailuresTotal.WithLabelValues("delete_failed"))
	if got, want := after, before; got != want {
		t.Errorf("media cleanup failure counter changed on a successful delete: before=%v after=%v", before, after)
	}
	if len(repo.completedIDs) != 1 || repo.completedIDs[0] != "job-2" {
		t.Errorf("expected job-2 to be marked complete, got %v", repo.completedIDs)
	}
	if len(repo.failedIDs) != 0 {
		t.Errorf("expected no job marked failed on a successful delete, got %v", repo.failedIDs)
	}
}

var errFakeDelete = fakeErr("cloudinary delete failed")

type fakeErr string

func (e fakeErr) Error() string { return string(e) }
