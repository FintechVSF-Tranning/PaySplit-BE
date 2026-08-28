package jobs

import (
	"context"
	"time"

	"github.com/riverqueue/river"

	"paysplit-backend/internal/modules/settlement/repository"
	"paysplit-backend/internal/modules/settlement/usecase"
	platformmetrics "paysplit-backend/internal/platform/metrics"
	"paysplit-backend/internal/platform/queue/appjob"
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

func NewScanWorker(service *usecase.Service, reminderAge, stalledAge time.Duration, maxCount int) *ScanWorker {
	if reminderAge <= 0 {
		reminderAge = 72 * time.Hour
	}
	if stalledAge <= 0 {
		stalledAge = 48 * time.Hour
	}
	if maxCount <= 0 {
		maxCount = 3
	}
	return &ScanWorker{service: service, reminderAge: reminderAge, stalledAge: stalledAge, maxCount: maxCount}
}

// Execute triển khai appjob.JobHandler cho ScanWorker (Spec 0010 AC-11)
func (w *ScanWorker) Execute(ctx context.Context, job appjob.Job) error {
	return w.Work(ctx, &river.Job[ScanArgs]{})
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

func NewCleanupWorker(repo repository.Repository, storage cleanupStorage) *CleanupWorker {
	return &CleanupWorker{repo: repo, storage: storage}
}

func (w *CleanupWorker) Work(ctx context.Context, _ *river.Job[CleanupArgs]) error {
	if err := w.repo.DeleteExpiredIdempotency(ctx); err != nil {
		platformmetrics.RecordSettlementWorkerRun("cleanup", "error")
		return err
	}
	err := w.repo.ProcessMediaCleanup(ctx, w.storage.Delete, platformmetrics.RecordMediaCleanupFailure)
	if err != nil {
		platformmetrics.RecordSettlementWorkerRun("cleanup", "error")
	} else {
		platformmetrics.RecordSettlementWorkerRun("cleanup", "success")
	}
	return err
}

// Execute triển khai appjob.JobHandler cho CleanupWorker (Spec 0010 AC-11)
func (w *CleanupWorker) Execute(ctx context.Context, job appjob.Job) error {
	return w.Work(ctx, &river.Job[CleanupArgs]{})
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
