package jobs

import (
	"context"
	"time"

	"github.com/riverqueue/river"

	"paysplit-backend/internal/modules/settlement/repository"
	"paysplit-backend/internal/modules/settlement/usecase"
	platformmetrics "paysplit-backend/internal/platform/metrics"
)

type ScanArgs struct{}

func (ScanArgs) Kind() string { return "settlement_scan" }

type ScanWorker struct {
	river.WorkerDefaults[ScanArgs]
	service                 *usecase.Service
	reminderAge, stalledAge time.Duration
	maxCount                int
}

func (w *ScanWorker) Work(ctx context.Context, _ *river.Job[ScanArgs]) error {
	if err := w.service.ProcessAutomatedReminders(ctx, time.Now().Add(-w.reminderAge), w.maxCount); err != nil {
		platformmetrics.RecordSettlementWorkerRun("reminders", "error")
		return err
	}
	platformmetrics.RecordSettlementWorkerRun("reminders", "success")
	err := w.service.ProcessStalledPayments(ctx, time.Now().Add(-w.stalledAge))
	if err != nil {
		platformmetrics.RecordSettlementWorkerRun("stalled", "error")
	} else {
		platformmetrics.RecordSettlementWorkerRun("stalled", "success")
	}
	return err
}

type CleanupArgs struct{}

func (CleanupArgs) Kind() string { return "settlement_cleanup" }

type cleanupStorage interface {
	Delete(context.Context, string) error
}
type CleanupWorker struct {
	river.WorkerDefaults[CleanupArgs]
	repo    repository.Repository
	storage cleanupStorage
}

func (w *CleanupWorker) Work(ctx context.Context, _ *river.Job[CleanupArgs]) error {
	if err := w.repo.DeleteExpiredIdempotency(ctx); err != nil {
		platformmetrics.RecordSettlementWorkerRun("cleanup", "error")
		return err
	}
	err := w.repo.ProcessMediaCleanup(ctx, w.storage.Delete)
	if err != nil {
		platformmetrics.RecordSettlementWorkerRun("cleanup", "error")
	} else {
		platformmetrics.RecordSettlementWorkerRun("cleanup", "success")
	}
	return err
}

func Register(workers *river.Workers, service *usecase.Service, repo repository.Repository, storage cleanupStorage, reminderAge, stalledAge time.Duration, maxCount int) []*river.PeriodicJob {
	if reminderAge <= 0 {
		reminderAge = 72 * time.Hour
	}
	if stalledAge <= 0 {
		stalledAge = 48 * time.Hour
	}
	if maxCount <= 0 {
		maxCount = 3
	}
	river.AddWorker(workers, &ScanWorker{service: service, reminderAge: reminderAge, stalledAge: stalledAge, maxCount: maxCount})
	river.AddWorker(workers, &CleanupWorker{repo: repo, storage: storage})
	return []*river.PeriodicJob{
		river.NewPeriodicJob(river.PeriodicInterval(time.Hour), func() (river.JobArgs, *river.InsertOpts) { return ScanArgs{}, nil }, &river.PeriodicJobOpts{RunOnStart: true}),
		river.NewPeriodicJob(river.PeriodicInterval(24*time.Hour), func() (river.JobArgs, *river.InsertOpts) { return CleanupArgs{}, nil }, &river.PeriodicJobOpts{RunOnStart: true}),
	}
}
