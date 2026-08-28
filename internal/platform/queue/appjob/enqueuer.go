package appjob

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresEnqueuer triển khai Enqueuer bằng cách ghi trực tiếp vào bảng app_jobs
type PostgresEnqueuer struct {
	pool *pgxpool.Pool
}

// NewPostgresEnqueuer khởi tạo Enqueuer kết nối với PostgreSQL
func NewPostgresEnqueuer(pool *pgxpool.Pool) *PostgresEnqueuer {
	return &PostgresEnqueuer{pool: pool}
}

// Enqueue chèn job mới với pool kết nối độc lập
func (e *PostgresEnqueuer) Enqueue(ctx context.Context, kind string, idempotencyKey string, args any, priority int, delay time.Duration) (*Job, error) {
	rawArgs, err := marshalArgs(args)
	if err != nil {
		return nil, fmt.Errorf("marshal job args: %w", err)
	}

	availableAt := time.Now().UTC().Add(delay)
	jobID := uuid.New()

	const query = `
		INSERT INTO app_jobs (id, kind, args, idempotency_key, priority, available_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (kind, idempotency_key) DO UPDATE
		SET updated_at = now()
		RETURNING id, kind, args, idempotency_key, status, priority, available_at, attempts, max_attempts,
		          lease_token, lease_expires_at, last_error, completed_at, created_at, updated_at
	`

	row := e.pool.QueryRow(ctx, query, jobID, kind, rawArgs, idempotencyKey, priority, availableAt)
	return scanJob(row)
}

// EnqueueTx chèn job mới bên trong một transaction hiện có
func (e *PostgresEnqueuer) EnqueueTx(ctx context.Context, tx pgx.Tx, kind string, idempotencyKey string, args any, priority int, delay time.Duration) (*Job, error) {
	rawArgs, err := marshalArgs(args)
	if err != nil {
		return nil, fmt.Errorf("marshal job args: %w", err)
	}

	availableAt := time.Now().UTC().Add(delay)
	jobID := uuid.New()

	const query = `
		INSERT INTO app_jobs (id, kind, args, idempotency_key, priority, available_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (kind, idempotency_key) DO UPDATE
		SET updated_at = now()
		RETURNING id, kind, args, idempotency_key, status, priority, available_at, attempts, max_attempts,
		          lease_token, lease_expires_at, last_error, completed_at, created_at, updated_at
	`

	row := tx.QueryRow(ctx, query, jobID, kind, rawArgs, idempotencyKey, priority, availableAt)
	return scanJob(row)
}

func marshalArgs(args any) (json.RawMessage, error) {
	if args == nil {
		return json.RawMessage("{}"), nil
	}
	if raw, ok := args.(json.RawMessage); ok {
		return raw, nil
	}
	if bytes, ok := args.([]byte); ok {
		return json.RawMessage(bytes), nil
	}
	bytes, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(bytes), nil
}

func scanJob(row pgx.Row) (*Job, error) {
	var j Job
	err := row.Scan(
		&j.ID,
		&j.Kind,
		&j.Args,
		&j.IdempotencyKey,
		&j.Status,
		&j.Priority,
		&j.AvailableAt,
		&j.Attempts,
		&j.MaxAttempts,
		&j.LeaseToken,
		&j.LeaseExpiresAt,
		&j.LastError,
		&j.CompletedAt,
		&j.CreatedAt,
		&j.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &j, nil
}
