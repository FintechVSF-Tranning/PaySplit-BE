package jobs

import (
	"context"
	"errors"
	"testing"
	"time"

	"paysplit-backend/internal/modules/settlement/repository"
	"paysplit-backend/internal/modules/settlement/usecase"
)

type workerRepository struct {
	repository.Repository
	reminderFn func(time.Time, int) error
	stalledFn  func(time.Time) error
	cleanupErr error
	mediaFn    func(func(context.Context, string) error, func(string)) error
}

func (r *workerRepository) ProcessAutomatedReminders(_ context.Context, before time.Time, max int, _ repository.BeforeCommit) error {
	return r.reminderFn(before, max)
}
func (r *workerRepository) ProcessStalledPayments(_ context.Context, before time.Time, _ repository.BeforeCommit) error {
	return r.stalledFn(before)
}
func (r *workerRepository) DeleteExpiredIdempotency(context.Context) error { return r.cleanupErr }
func (r *workerRepository) ProcessMediaCleanup(_ context.Context, deleteFn func(context.Context, string) error, recordFailure func(string)) error {
	return r.mediaFn(deleteFn, recordFailure)
}

type cleanupStorageStub struct{ deleted string }

func (s *cleanupStorageStub) Delete(_ context.Context, key string) error { s.deleted = key; return nil }

func TestScanWorker_AC10UsesConfiguredEligibilityWindows(t *testing.T) {
	now := time.Now()
	var reminderBefore, stalledBefore time.Time
	repo := &workerRepository{
		reminderFn: func(before time.Time, max int) error {
			reminderBefore = before
			if max != 3 {
				t.Fatalf("max=%d", max)
			}
			return nil
		},
		stalledFn: func(before time.Time) error { stalledBefore = before; return nil },
	}
	worker := &ScanWorker{service: usecase.NewService(repo), reminderAge: 72 * time.Hour, stalledAge: 48 * time.Hour, maxCount: 3}
	if err := worker.Work(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if delta := reminderBefore.Sub(now.Add(-72 * time.Hour)); delta < 0 || delta > time.Second {
		t.Fatalf("reminder cutoff delta=%s", delta)
	}
	if delta := stalledBefore.Sub(now.Add(-48 * time.Hour)); delta < 0 || delta > time.Second {
		t.Fatalf("stalled cutoff delta=%s", delta)
	}
}

func TestScanWorker_AC10StopsWhenReminderClaimFails(t *testing.T) {
	want := errors.New("reminder failure")
	stalledCalled := false
	repo := &workerRepository{
		reminderFn: func(time.Time, int) error { return want },
		stalledFn:  func(time.Time) error { stalledCalled = true; return nil },
	}
	err := (&ScanWorker{service: usecase.NewService(repo), reminderAge: time.Hour, stalledAge: time.Hour, maxCount: 3}).Work(context.Background(), nil)
	if !errors.Is(err, want) || stalledCalled {
		t.Fatalf("err=%v stalledCalled=%v", err, stalledCalled)
	}
}

func TestCleanupWorker_AC6AndAC11RunsExpiryBeforeExactMediaDelete(t *testing.T) {
	storage := &cleanupStorageStub{}
	repo := &workerRepository{mediaFn: func(deleteFn func(context.Context, string) error, _ func(string)) error {
		return deleteFn(context.Background(), "payments/payment/proofs/operation")
	}}
	if err := (&CleanupWorker{repo: repo, storage: storage}).Work(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if storage.deleted != "payments/payment/proofs/operation" {
		t.Fatalf("deleted=%q", storage.deleted)
	}
}

func TestCleanupWorker_AC11DoesNotProcessMediaWhenExpiryCleanupFails(t *testing.T) {
	want := errors.New("cleanup failure")
	mediaCalled := false
	repo := &workerRepository{cleanupErr: want, mediaFn: func(func(context.Context, string) error, func(string)) error { mediaCalled = true; return nil }}
	err := (&CleanupWorker{repo: repo, storage: &cleanupStorageStub{}}).Work(context.Background(), nil)
	if !errors.Is(err, want) || mediaCalled {
		t.Fatalf("err=%v mediaCalled=%v", err, mediaCalled)
	}
}
