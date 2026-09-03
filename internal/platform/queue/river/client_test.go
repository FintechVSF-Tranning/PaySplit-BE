package river

import (
	"context"
	"testing"
	"time"

	"github.com/riverqueue/river"
)

func TestNewClient_NilPool(t *testing.T) {
	workers := river.NewWorkers()
	client, err := NewClient(nil, workers, Config{})
	if err == nil {
		t.Fatalf("expected error when pool is nil, got client: %v", client)
	}
}

func TestClientConfigMapsPollOnlySettings(t *testing.T) {
	workers := river.NewWorkers()
	cfg := clientConfig(workers, Config{
		MaxWorkers:        2,
		FetchCooldown:     75 * time.Millisecond,
		PollOnly:          true,
		FetchPollInterval: 1500 * time.Millisecond,
	})
	if cfg.Workers != workers || !cfg.PollOnly {
		t.Fatalf("poll only config was not mapped: %+v", cfg)
	}
	if cfg.FetchCooldown != 75*time.Millisecond || cfg.FetchPollInterval != 1500*time.Millisecond {
		t.Fatalf("unexpected fetch intervals: cooldown=%s poll=%s", cfg.FetchCooldown, cfg.FetchPollInterval)
	}
	if cfg.Queues[river.QueueDefault].MaxWorkers != 2 {
		t.Fatalf("max workers = %d, want 2", cfg.Queues[river.QueueDefault].MaxWorkers)
	}
}

func TestClientConfigUsesSafeDefaults(t *testing.T) {
	cfg := clientConfig(river.NewWorkers(), Config{})
	if cfg.PollOnly || cfg.FetchCooldown != 100*time.Millisecond || cfg.FetchPollInterval != time.Second {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestClientConfigMapsPeriodicJobs(t *testing.T) {
	// covers: AC-5
	workers := river.NewWorkers()
	jobs := []*river.PeriodicJob{
		river.NewPeriodicJob(river.PeriodicInterval(time.Hour), func() (river.JobArgs, *river.InsertOpts) {
			return testJobArgs{Message: "periodic"}, nil
		}, nil),
	}
	cfg := clientConfig(workers, Config{PeriodicJobs: jobs})
	if len(cfg.PeriodicJobs) != 1 {
		t.Fatalf("periodic jobs = %d, want 1", len(cfg.PeriodicJobs))
	}
}

func TestNewClient_NilWorkers(t *testing.T) {
	client, err := NewClient(nil, nil, Config{})
	if err == nil {
		t.Fatalf("expected error when workers is nil, got client: %v", client)
	}
}

func TestAutoMigrate_NilPool(t *testing.T) {
	err := AutoMigrate(context.Background(), nil)
	if err == nil {
		t.Fatalf("expected error when pool is nil")
	}
}
