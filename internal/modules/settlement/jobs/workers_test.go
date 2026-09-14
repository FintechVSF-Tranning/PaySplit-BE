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
	cleanupErr error
	cleaned    bool
}

func (r *workerRepository) ProcessAutomatedReminders(_ context.Context, before time.Time, max int, _ repository.BeforeCommit) error {
	return r.reminderFn(before, max)
}
func (r *workerRepository) DeleteExpiredIdempotency(context.Context) error {
	r.cleaned = true
	return r.cleanupErr
}

func TestScanWorker_AC10UsesConfiguredReminderWindow(t *testing.T) {
	now := time.Now()
	var reminderBefore time.Time
	repo := &workerRepository{
		reminderFn: func(before time.Time, max int) error {
			reminderBefore = before
			if max != 3 {
				t.Fatalf("max=%d", max)
			}
			return nil
		},
	}
	worker := &ScanWorker{service: usecase.NewService(repo), reminderAge: 72 * time.Hour, maxCount: 3}
	if err := worker.Work(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if delta := reminderBefore.Sub(now.Add(-72 * time.Hour)); delta < 0 || delta > time.Second {
		t.Fatalf("reminder cutoff delta=%s", delta)
	}
}

func TestScanWorker_AC10ReturnsReminderFailure(t *testing.T) {
	want := errors.New("reminder failure")
	repo := &workerRepository{reminderFn: func(time.Time, int) error { return want }}
	err := (&ScanWorker{service: usecase.NewService(repo), reminderAge: time.Hour, maxCount: 3}).Work(context.Background(), nil)
	if !errors.Is(err, want) {
		t.Fatalf("err=%v", err)
	}
}

func TestCleanupWorker_AC11DeletesExpiredIdempotencyKeys(t *testing.T) {
	repo := &workerRepository{}
	if err := (&CleanupWorker{repo: repo}).Work(context.Background(), nil); err != nil || !repo.cleaned {
		t.Fatalf("err=%v cleaned=%v", err, repo.cleaned)
	}
	want := errors.New("cleanup failure")
	repo = &workerRepository{cleanupErr: want}
	if err := (&CleanupWorker{repo: repo}).Work(context.Background(), nil); !errors.Is(err, want) {
		t.Fatalf("err=%v", err)
	}
}
