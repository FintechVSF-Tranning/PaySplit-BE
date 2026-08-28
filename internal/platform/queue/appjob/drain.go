package appjob

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"paysplit-backend/internal/config"
)

// Drain thực hiện kích hoạt slot reservation, xử lý batch tối đa 5 job và giải phóng slot an toàn
type Drain struct {
	pool        *pgxpool.Pool
	cfg         config.JobConfig
	dispatchURL string
	mu          sync.RWMutex
	handlers    map[string]JobHandler
}

// NewDrain khởi tạo Drain worker engine
func NewDrain(pool *pgxpool.Pool, cfg config.JobConfig, dispatchURL string) *Drain {
	return &Drain{
		pool:        pool,
		cfg:         cfg,
		dispatchURL: dispatchURL,
		handlers:    make(map[string]JobHandler),
	}
}

// RegisterHandler đăng ký handler xử lý cho một kind cụ thể
func (d *Drain) RegisterHandler(kind string, handler JobHandler) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.handlers[kind] = handler
}

// Drain nhận yêu cầu drain một slot đã được reserve và xử lý batch công việc
func (d *Drain) Drain(ctx context.Context, req DrainRequest) (*DrainResponse, error) {
	if !d.cfg.ProcessingEnabled {
		return nil, nil
	}

	// 1. Kích hoạt slot reservation: chuyển từ 'reserved' sang 'leased' với token khớp
	const activateQuery = `
		UPDATE job_drain_slots
		SET state = 'leased', updated_at = now()
		WHERE slot_no = $1 AND lease_token = $2 AND state = 'reserved' AND lease_expires_at > now()
		RETURNING slot_no
	`
	var activatedSlot int16
	err := d.pool.QueryRow(ctx, activateQuery, req.SlotNo, req.SlotToken).Scan(&activatedSlot)
	if err != nil {
		if err == pgx.ErrNoRows {
			// Slot reservation không hợp lệ, đã hết hạn hoặc đã được kích hoạt -> trả 204
			return nil, nil
		}
		return nil, fmt.Errorf("activate drain slot: %w", err)
	}

	// Đảm bảo luôn giải phóng slot và wave ở mọi nhánh thoát
	defer func() {
		_ = d.releaseSlotAndWave(context.Background(), req)
	}()

	startTime := time.Now()
	wallClockBudget := d.cfg.InvocationTimeout
	if wallClockBudget <= 0 {
		wallClockBudget = 45 * time.Second
	}
	stopClaimingAfter := d.cfg.StopClaimingAfter
	if stopClaimingAfter <= 0 {
		stopClaimingAfter = 40 * time.Second
	}
	batchSize := d.cfg.BatchSize
	if batchSize <= 0 {
		batchSize = 5
	}
	externalTimeout := d.cfg.ExternalTimeout
	if externalTimeout <= 0 {
		externalTimeout = 35 * time.Second
	}
	jobLease := d.cfg.LeaseDuration
	if jobLease <= 0 {
		jobLease = 75 * time.Second
	}
	jobLeaseInterval := fmt.Sprintf("%d milliseconds", jobLease.Milliseconds())

	resp := &DrainResponse{}

	// 2. Vòng lặp xử lý tối đa 5 jobs tuần tự trong ngân sách thời gian
	for i := 0; i < batchSize; i++ {
		if time.Since(startTime) >= stopClaimingAfter {
			break
		}

		// Claim 1 job trong transaction ngắn với FOR UPDATE SKIP LOCKED
		job, jobLeaseToken, err := d.claimOneJob(ctx, jobLeaseInterval)
		if err != nil {
			return resp, fmt.Errorf("claim job: %w", err)
		}
		if job == nil {
			// Không còn job đến hạn
			break
		}

		resp.Claimed++

		// Tìm handler phù hợp
		d.mu.RLock()
		handler, exists := d.handlers[job.Kind]
		d.mu.RUnlock()

		var execErr error
		if !exists {
			execErr = fmt.Errorf("unregistered job kind: %s", job.Kind)
		} else {
			// Giới hạn timeout cho external operation
			remainingBudget := wallClockBudget - time.Since(startTime) - (5 * time.Second)
			opTimeout := externalTimeout
			if remainingBudget < opTimeout {
				opTimeout = remainingBudget
			}
			if opTimeout < time.Second {
				opTimeout = time.Second
			}

			jobCtx, jobCancel := context.WithTimeout(ctx, opTimeout)
			execErr = handler.Execute(jobCtx, *job)
			jobCancel()
		}

		// Ghi nhận kết quả xử lý với kiểm tra lease token nghiêm ngặt
		if execErr == nil {
			err = d.markJobCompleted(ctx, job.ID, jobLeaseToken)
			if err == nil {
				resp.Completed++
			}
		} else {
			lastErrStr := execErr.Error()
			if job.Attempts >= job.MaxAttempts {
				err = d.markJobDiscarded(ctx, job.ID, jobLeaseToken, lastErrStr)
				if err == nil {
					resp.Discarded++
				}
			} else {
				backoff := calculateBackoff(job.Attempts)
				err = d.markJobRetry(ctx, job.ID, jobLeaseToken, backoff, lastErrStr)
				if err == nil {
					resp.Retried++
				}
			}
		}
	}

	return resp, nil
}

func (d *Drain) claimOneJob(ctx context.Context, leaseInterval string) (*Job, uuid.UUID, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return nil, uuid.Nil, err
	}
	defer tx.Rollback(ctx)

	const selectQuery = `
		SELECT id, kind, args, idempotency_key, priority, available_at, attempts, max_attempts, created_at, updated_at
		FROM app_jobs
		WHERE status = 'available' AND available_at <= now()
		ORDER BY priority DESC, available_at ASC, created_at ASC
		LIMIT 1
		FOR UPDATE SKIP LOCKED
	`

	var j Job
	err = tx.QueryRow(ctx, selectQuery).Scan(
		&j.ID,
		&j.Kind,
		&j.Args,
		&j.IdempotencyKey,
		&j.Priority,
		&j.AvailableAt,
		&j.Attempts,
		&j.MaxAttempts,
		&j.CreatedAt,
		&j.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, uuid.Nil, nil
		}
		return nil, uuid.Nil, err
	}

	jobLeaseToken := uuid.New()
	j.Attempts++
	j.Status = StatusRunning

	const updateQuery = `
		UPDATE app_jobs
		SET status = 'running',
		    lease_token = $2,
		    lease_expires_at = now() + $3::interval,
		    attempts = attempts + 1,
		    updated_at = now()
		WHERE id = $1 AND status = 'available'
	`
	_, err = tx.Exec(ctx, updateQuery, j.ID, jobLeaseToken, leaseInterval)
	if err != nil {
		return nil, uuid.Nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, uuid.Nil, err
	}

	return &j, jobLeaseToken, nil
}

func (d *Drain) markJobCompleted(ctx context.Context, jobID uuid.UUID, leaseToken uuid.UUID) error {
	const query = `
		UPDATE app_jobs
		SET status = 'completed',
		    completed_at = now(),
		    lease_token = NULL,
		    updated_at = now()
		WHERE id = $1 AND status = 'running' AND lease_token = $2
	`
	_, err := d.pool.Exec(ctx, query, jobID, leaseToken)
	return err
}

func (d *Drain) markJobDiscarded(ctx context.Context, jobID uuid.UUID, leaseToken uuid.UUID, lastErr string) error {
	const query = `
		UPDATE app_jobs
		SET status = 'discarded',
		    last_error = $3,
		    completed_at = now(),
		    lease_token = NULL,
		    updated_at = now()
		WHERE id = $1 AND status = 'running' AND lease_token = $2
	`
	_, err := d.pool.Exec(ctx, query, jobID, leaseToken, lastErr)
	return err
}

func (d *Drain) markJobRetry(ctx context.Context, jobID uuid.UUID, leaseToken uuid.UUID, backoff time.Duration, lastErr string) error {
	const query = `
		UPDATE app_jobs
		SET status = 'available',
		    available_at = now() + $3::interval,
		    last_error = $4,
		    lease_token = NULL,
		    updated_at = now()
		WHERE id = $1 AND status = 'running' AND lease_token = $2
	`
	backoffInterval := fmt.Sprintf("%d milliseconds", backoff.Milliseconds())
	_, err := d.pool.Exec(ctx, query, jobID, leaseToken, backoffInterval, lastErr)
	return err
}

func (d *Drain) releaseSlotAndWave(ctx context.Context, req DrainRequest) error {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// 1. Giải phóng slot
	const freeSlotQuery = `
		UPDATE job_drain_slots
		SET state = 'free', lease_token = NULL, updated_at = now()
		WHERE slot_no = $1 AND lease_token = $2
	`
	_, _ = tx.Exec(ctx, freeSlotQuery, req.SlotNo, req.SlotToken)

	// 2. Giảm wave_outstanding
	const decWaveQuery = `
		UPDATE job_wakeup_state
		SET wave_outstanding = GREATEST(0, wave_outstanding - 1),
		    updated_at = now()
		WHERE id = 1 AND wave_id = $1
		RETURNING wave_outstanding, requested_generation, dispatched_generation
	`
	var waveOutstanding int16
	var reqGen, dispGen int64
	err = tx.QueryRow(ctx, decWaveQuery, req.WaveID).Scan(&waveOutstanding, &reqGen, &dispGen)
	if err != nil {
		_ = tx.Commit(ctx)
		return nil
	}

	// 3. Nếu là drain cuối cùng trong wave (wave_outstanding == 0)
	if waveOutstanding == 0 {
		var dueJobs int64
		_ = tx.QueryRow(ctx, `SELECT count(*) FROM app_jobs WHERE status = 'available' AND available_at <= now()`).Scan(&dueJobs)

		if reqGen > dispGen || dueJobs > 0 {
			// Còn việc hoặc có generation mới -> re-arm dispatcher
			_, _ = tx.Exec(ctx, `UPDATE job_wakeup_state SET dispatcher_requested = true, updated_at = now() WHERE id = 1`)

			// Kích hoạt dispatch tiếp theo qua pg_net nếu có
			if d.dispatchURL != "" {
				pgNetTimeout := int(d.cfg.PGNetDispatchTimeout.Milliseconds())
				if pgNetTimeout <= 0 {
					pgNetTimeout = 10000
				}
				const triggerDispatchQuery = `
					SELECT net.http_post(
						url := $1,
						headers := jsonb_build_object(
							'Content-Type', 'application/json',
							'Authorization', 'Bearer ' || $2,
							'Cache-Control', 'no-store'
						),
						body := '{}'::jsonb,
						timeout_milliseconds := $3
					)
				`
				_, _ = tx.Exec(ctx, triggerDispatchQuery, d.dispatchURL, d.cfg.TriggerSecret, pgNetTimeout)
			}
		} else {
			// Hoàn tất đợt việc -> ghi nhận acknowledged_generation
			_, _ = tx.Exec(ctx, `UPDATE job_wakeup_state SET acknowledged_generation = $1, updated_at = now() WHERE id = 1`, dispGen)
		}
	}

	return tx.Commit(ctx)
}

func calculateBackoff(attempts int) time.Duration {
	if attempts <= 0 {
		attempts = 1
	}
	base := 30 * time.Second
	multiplier := 1 << (attempts - 1)
	backoff := time.Duration(multiplier) * base
	if backoff > time.Hour {
		backoff = time.Hour
	}
	// Bounded jitter +-20%
	jitterRange := float64(backoff) * 0.2
	jitter := time.Duration((rand.Float64()*2 - 1) * jitterRange)
	return backoff + jitter
}
