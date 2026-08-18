package river

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

type Config struct {
	MaxWorkers    int
	FetchCooldown time.Duration
}

// AutoMigrate tự động chạy migration để tạo/cập nhật các bảng nội bộ của River trong PostgreSQL
func AutoMigrate(ctx context.Context, dbPool *pgxpool.Pool) error {
	if dbPool == nil {
		return fmt.Errorf("db pool must not be nil")
	}

	migrator, err := rivermigrate.New(riverpgxv5.New(dbPool), nil)
	if err != nil {
		return fmt.Errorf("create river migrator: %w", err)
	}

	_, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil)
	if err != nil {
		return fmt.Errorf("run river migrations: %w", err)
	}
	return nil
}

// NewClient khởi tạo River Client sử dụng pgx/v5 driver kết nối trực tiếp với PostgreSQL connection pool
func NewClient(dbPool *pgxpool.Pool, workers *river.Workers, cfg Config) (*river.Client[pgx.Tx], error) {
	if dbPool == nil {
		return nil, fmt.Errorf("db pool must not be nil")
	}
	if workers == nil {
		return nil, fmt.Errorf("workers registry must not be nil")
	}

	maxWorkers := cfg.MaxWorkers
	if maxWorkers <= 0 {
		maxWorkers = 5
	}
	fetchCooldown := cfg.FetchCooldown
	if fetchCooldown <= 0 {
		fetchCooldown = 100 * time.Millisecond
	}

	riverClient, err := river.NewClient(riverpgxv5.New(dbPool), &river.Config{
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: maxWorkers},
		},
		Workers:       workers,
		FetchCooldown: fetchCooldown,
	})
	if err != nil {
		return nil, fmt.Errorf("create river client: %w", err)
	}

	return riverClient, nil
}
