package river

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

type testJobArgs struct {
	Message string `json:"message"`
}

func (testJobArgs) Kind() string { return "test_queue_job" }

type testWorker struct {
	river.WorkerDefaults[testJobArgs]
	mu       sync.Mutex
	received []string
	doneCh   chan struct{}
}

func (w *testWorker) Work(ctx context.Context, job *river.Job[testJobArgs]) error {
	w.mu.Lock()
	w.received = append(w.received, job.Args.Message)
	w.mu.Unlock()
	select {
	case w.doneCh <- struct{}{}:
	default:
	}
	return nil
}

func TestRiverClient_Integration(t *testing.T) {
	// covers: AC-5, AC-7, AC-10
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse pool config: %v", err)
	}
	poolConfig.ConnConfig.RuntimeParams["application_name"] = "paysplit-river-poll-integration"
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatalf("connect to database: %v", err)
	}
	defer pool.Close()

	// 1. Test AutoMigrate
	if err := AutoMigrate(ctx, pool); err != nil {
		t.Fatalf("AutoMigrate failed: %v", err)
	}

	// 2. Register worker
	worker := &testWorker{doneCh: make(chan struct{}, 1)}
	workers := river.NewWorkers()
	river.AddWorker(workers, worker)

	// 3. Create client
	client, err := NewClient(pool, workers, Config{
		MaxWorkers:        2,
		FetchCooldown:     50 * time.Millisecond,
		PollOnly:          true,
		FetchPollInterval: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	// 4. Start client
	if err := client.Start(ctx); err != nil {
		t.Fatalf("client.Start failed: %v", err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		_ = client.Stop(stopCtx)
	}()

	var listenSessions int
	if err = pool.QueryRow(ctx, `
		SELECT count(*)
		FROM pg_stat_activity
		WHERE application_name = 'paysplit-river-poll-integration'
		  AND state = 'idle'
		  AND query LIKE 'LISTEN %'
	`).Scan(&listenSessions); err != nil {
		t.Fatalf("count River LISTEN sessions: %v", err)
	}
	if listenSessions != 0 {
		t.Fatalf("River poll-only LISTEN sessions = %d, want 0", listenSessions)
	}

	// 5. Enqueue job
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin enqueue transaction: %v", err)
	}
	if _, err = client.InsertTx(ctx, tx, testJobArgs{Message: "hello river"}, nil); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("insert job in transaction: %v", err)
	}
	started := time.Now()
	if err = tx.Commit(ctx); err != nil {
		t.Fatalf("commit enqueue transaction: %v", err)
	}

	// 6. Wait for job to process
	select {
	case <-worker.doneCh:
		if latency := time.Since(started); latency > 3*time.Second {
			t.Fatalf("poll-only pickup latency = %s, want under test tolerance", latency)
		}
		worker.mu.Lock()
		defer worker.mu.Unlock()
		if len(worker.received) == 0 || worker.received[0] != "hello river" {
			t.Fatalf("expected 'hello river', got %v", worker.received)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for river job to process")
	}
}

type retryJobArgs struct {
	Nonce string `json:"nonce"`
}

func (retryJobArgs) Kind() string { return "test_retry_job" }

type retryWorker struct {
	river.WorkerDefaults[retryJobArgs]
	mu        sync.Mutex
	attempts  int
	succeeded chan struct{}
}

func (w *retryWorker) Work(_ context.Context, _ *river.Job[retryJobArgs]) error {
	w.mu.Lock()
	w.attempts++
	attempts := w.attempts
	w.mu.Unlock()
	if attempts == 1 {
		return errors.New("temporary failure")
	}
	select {
	case w.succeeded <- struct{}{}:
	default:
	}
	return nil
}

type periodicJobArgs struct {
	Nonce string `json:"nonce"`
}

func (periodicJobArgs) Kind() string { return "test_periodic_job" }

type periodicWorker struct {
	river.WorkerDefaults[periodicJobArgs]
	doneCh chan struct{}
}

func (w *periodicWorker) Work(_ context.Context, _ *river.Job[periodicJobArgs]) error {
	select {
	case w.doneCh <- struct{}{}:
	default:
	}
	return nil
}

func TestRiverClient_IntegrationScheduledRetryAndPeriodic(t *testing.T) {
	// covers: AC-5, AC-7
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse pool config: %v", err)
	}
	poolConfig.ConnConfig.RuntimeParams["application_name"] = "paysplit-river-poll-jobs"
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatalf("connect to database: %v", err)
	}
	defer pool.Close()

	if err := AutoMigrate(ctx, pool); err != nil {
		t.Fatalf("AutoMigrate failed: %v", err)
	}

	scheduledWorker := &testWorker{doneCh: make(chan struct{}, 1)}
	retry := &retryWorker{succeeded: make(chan struct{}, 1)}
	periodic := &periodicWorker{doneCh: make(chan struct{}, 1)}
	workers := river.NewWorkers()
	river.AddWorker(workers, scheduledWorker)
	river.AddWorker(workers, retry)
	river.AddWorker(workers, periodic)

	client, err := NewClient(pool, workers, Config{
		MaxWorkers:        2,
		FetchCooldown:     50 * time.Millisecond,
		PollOnly:          true,
		FetchPollInterval: 100 * time.Millisecond,
		PeriodicJobs: []*river.PeriodicJob{
			river.NewPeriodicJob(river.PeriodicInterval(time.Hour), func() (river.JobArgs, *river.InsertOpts) {
				return periodicJobArgs{Nonce: "start"}, nil
			}, &river.PeriodicJobOpts{RunOnStart: true}),
		},
	})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	if err := client.Start(ctx); err != nil {
		t.Fatalf("client.Start failed: %v", err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		_ = client.Stop(stopCtx)
	}()

	scheduledAt := time.Now().Add(250 * time.Millisecond)
	if _, err = client.Insert(ctx, testJobArgs{Message: "scheduled"}, &river.InsertOpts{ScheduledAt: scheduledAt}); err != nil {
		t.Fatalf("insert scheduled job: %v", err)
	}
	select {
	case <-scheduledWorker.doneCh:
		if time.Now().Before(scheduledAt) {
			t.Fatal("scheduled job ran before ScheduledAt")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for scheduled job")
	}

	if _, err = client.Insert(ctx, retryJobArgs{Nonce: "retry"}, &river.InsertOpts{MaxAttempts: 2}); err != nil {
		t.Fatalf("insert retry job: %v", err)
	}
	select {
	case <-retry.succeeded:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for retried job")
	}
	retry.mu.Lock()
	attempts := retry.attempts
	retry.mu.Unlock()
	if attempts < 2 {
		t.Fatalf("retry attempts = %d, want at least 2", attempts)
	}

	select {
	case <-periodic.doneCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for periodic job")
	}
}
