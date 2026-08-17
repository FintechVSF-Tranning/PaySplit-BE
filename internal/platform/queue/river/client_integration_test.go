package river

import (
	"context"
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
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, databaseURL)
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
	client, err := NewClient(pool, workers, Config{MaxWorkers: 2})
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

	// 5. Enqueue job
	_, err = client.Insert(ctx, testJobArgs{Message: "hello river"}, nil)
	if err != nil {
		t.Fatalf("client.Insert failed: %v", err)
	}

	// 6. Wait for job to process
	select {
	case <-worker.doneCh:
		worker.mu.Lock()
		defer worker.mu.Unlock()
		if len(worker.received) == 0 || worker.received[0] != "hello river" {
			t.Fatalf("expected 'hello river', got %v", worker.received)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for river job to process")
	}
}
