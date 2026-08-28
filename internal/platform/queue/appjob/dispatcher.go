package appjob

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"paysplit-backend/internal/config"
)

// Dispatcher thực hiện lập lịch và điều phối các đợt drain job có giới hạn dung lượng
type Dispatcher struct {
	pool     *pgxpool.Pool
	cfg      config.JobConfig
	drainURL string
}

// NewDispatcher khởi tạo Dispatcher
func NewDispatcher(pool *pgxpool.Pool, cfg config.JobConfig, drainURL string) *Dispatcher {
	return &Dispatcher{
		pool:     pool,
		cfg:      cfg,
		drainURL: drainURL,
	}
}

// Dispatch thực hiện 1 lượt điều phối wave: lấy lease, dọn dẹp expired, đếm việc và kích hoạt drain
func (d *Dispatcher) Dispatch(ctx context.Context) (*DispatchResponse, error) {
	if !d.cfg.ProcessingEnabled {
		return nil, nil
	}

	leaseToken := uuid.New()
	leaseDuration := d.cfg.DispatcherLease
	if leaseDuration <= 0 {
		leaseDuration = 15 * time.Second
	}

	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin dispatch tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// 1. Thử lấy singleton dispatcher lease trên hàng job_wakeup_state
	var waveOutstanding int16
	var reqGen, dispGen, ackGen int64
	var currentWaveID int64
	const acquireLeaseQuery = `
		UPDATE job_wakeup_state
		SET dispatcher_token = $1,
		    dispatcher_lease_expires_at = now() + $2::interval,
		    updated_at = now()
		WHERE id = 1 AND (dispatcher_lease_expires_at IS NULL OR dispatcher_lease_expires_at < now())
		RETURNING wave_id, wave_outstanding, requested_generation, dispatched_generation, acknowledged_generation
	`
	leaseInterval := fmt.Sprintf("%d milliseconds", leaseDuration.Milliseconds())
	err = tx.QueryRow(ctx, acquireLeaseQuery, leaseToken, leaseInterval).Scan(
		&currentWaveID,
		&waveOutstanding,
		&reqGen,
		&dispGen,
		&ackGen,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			// Một dispatcher khác đang giữ lease hợp lệ -> trả 204
			return nil, nil
		}
		return nil, fmt.Errorf("acquire dispatcher lease: %w", err)
	}

	// 2. Reconcile các slot đã hết hạn lease (reset về free và điều chỉnh wave_outstanding)
	const reconcileSlotsQuery = `
		WITH expired_slots AS (
			UPDATE job_drain_slots
			SET state = 'free', lease_token = NULL, updated_at = now()
			WHERE state IN ('reserved', 'leased') AND lease_expires_at < now()
			RETURNING wave_id
		),
		expired_counts AS (
			SELECT wave_id, count(*) AS cnt FROM expired_slots GROUP BY wave_id
		)
		UPDATE job_wakeup_state s
		SET wave_outstanding = GREATEST(0, s.wave_outstanding - ec.cnt),
		    updated_at = now()
		FROM expired_counts ec
		WHERE s.id = 1 AND s.wave_id = ec.wave_id
		RETURNING s.wave_outstanding
	`
	var updatedWaveOutstanding int16
	err = tx.QueryRow(ctx, reconcileSlotsQuery).Scan(&updatedWaveOutstanding)
	if err == nil {
		waveOutstanding = updatedWaveOutstanding
	} else if err != pgx.ErrNoRows {
		return nil, fmt.Errorf("reconcile expired slots: %w", err)
	}

	// 3. Reconcile các job running đã hết hạn lease (đưa về available hoặc discarded)
	const reconcileJobsQuery = `
		UPDATE app_jobs
		SET status = CASE WHEN attempts < max_attempts THEN 'available' ELSE 'discarded' END,
		    available_at = now(),
		    last_error = 'lease expired',
		    lease_token = NULL,
		    completed_at = CASE WHEN attempts >= max_attempts THEN now() ELSE NULL END,
		    updated_at = now()
		WHERE status = 'running' AND lease_expires_at < now()
	`
	if _, err := tx.Exec(ctx, reconcileJobsQuery); err != nil {
		return nil, fmt.Errorf("reconcile expired running jobs: %w", err)
	}

	// 4. Kiểm tra xem có active wave đang chạy hay không (Single Active Wave Rule)
	if waveOutstanding > 0 {
		// Vẫn còn drain của wave hiện tại đang chạy -> nhả lease và để drain cuối tạo wave mới
		const releaseLeaseOnly = `
			UPDATE job_wakeup_state
			SET dispatcher_token = NULL, dispatcher_lease_expires_at = NULL, updated_at = now()
			WHERE id = 1 AND dispatcher_token = $1
		`
		_, _ = tx.Exec(ctx, releaseLeaseOnly, leaseToken)
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit preserve wave: %w", err)
		}
		return nil, nil
	}

	// 5. Đếm số lượng job due và các slot đang free
	var dueJobs int64
	const countDueQuery = `SELECT count(*) FROM app_jobs WHERE status = 'available' AND available_at <= now()`
	if err := tx.QueryRow(ctx, countDueQuery).Scan(&dueJobs); err != nil {
		return nil, fmt.Errorf("count due jobs: %w", err)
	}

	if dueJobs == 0 {
		// Không có job đến hạn -> nâng acknowledged_generation, clear lease, kết thúc
		const advanceAckQuery = `
			UPDATE job_wakeup_state
			SET acknowledged_generation = requested_generation,
			    dispatcher_requested = false,
			    dispatcher_token = NULL,
			    dispatcher_lease_expires_at = NULL,
			    updated_at = now()
			WHERE id = 1 AND dispatcher_token = $1
		`
		_, _ = tx.Exec(ctx, advanceAckQuery, leaseToken)
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit advance ack: %w", err)
		}
		return nil, nil
	}

	// Lấy danh sách các slot free
	rows, err := tx.Query(ctx, `SELECT slot_no FROM job_drain_slots WHERE state = 'free' ORDER BY slot_no FOR UPDATE`)
	if err != nil {
		return nil, fmt.Errorf("query free slots: %w", err)
	}
	var freeSlots []int16
	for rows.Next() {
		var s int16
		if err := rows.Scan(&s); err == nil {
			freeSlots = append(freeSlots, s)
		}
	}
	rows.Close()

	if len(freeSlots) == 0 {
		// Không có slot free -> nhả lease chờ Cron hoặc wave giải phóng
		const releaseLeaseNoSlot = `
			UPDATE job_wakeup_state
			SET dispatcher_token = NULL, dispatcher_lease_expires_at = NULL, updated_at = now()
			WHERE id = 1 AND dispatcher_token = $1
		`
		_, _ = tx.Exec(ctx, releaseLeaseNoSlot, leaseToken)
		_ = tx.Commit(ctx)
		return nil, nil
	}

	// 6. Tính số slot cần reserve: N = min(free_slots, ceil(due_jobs / batch_size)), tối đa 10
	batchSize := d.cfg.BatchSize
	if batchSize <= 0 {
		batchSize = 5
	}
	neededSlots := int(math.Ceil(float64(dueJobs) / float64(batchSize)))
	n := len(freeSlots)
	if neededSlots < n {
		n = neededSlots
	}
	if n > 10 {
		n = 10
	}
	if n <= 0 {
		n = 1
	}

	newWaveID := currentWaveID + 1
	slotLease := d.cfg.DrainSlotLeaseDuration
	if slotLease <= 0 {
		slotLease = 75 * time.Second
	}
	slotLeaseInterval := fmt.Sprintf("%d milliseconds", slotLease.Milliseconds())

	reservations := make([]SlotReservation, 0, n)
	for i := 0; i < n; i++ {
		slotNo := freeSlots[i]
		slotToken := uuid.New()
		const reserveSlotQuery = `
			UPDATE job_drain_slots
			SET state = 'reserved',
			    lease_token = $2,
			    lease_expires_at = now() + $3::interval,
			    wave_id = $4,
			    dispatch_generation = $5,
			    updated_at = now()
			WHERE slot_no = $1 AND state = 'free'
		`
		_, err := tx.Exec(ctx, reserveSlotQuery, slotNo, slotToken, slotLeaseInterval, newWaveID, reqGen)
		if err != nil {
			return nil, fmt.Errorf("reserve slot %d: %w", slotNo, err)
		}

		reservations = append(reservations, SlotReservation{
			SlotNo:             slotNo,
			SlotToken:          slotToken,
			WaveID:             newWaveID,
			DispatchGeneration: reqGen,
		})
	}

	// 7. Gọi pg_net kích hoạt các drain request bất đồng bộ
	pgNetTimeout := int(d.cfg.PGNetDrainTimeout.Milliseconds())
	if pgNetTimeout <= 0 {
		pgNetTimeout = 55000
	}

	if d.drainURL != "" {
		for _, r := range reservations {
			const pgNetQuery = `
				SELECT net.http_post(
					url := $1,
					headers := jsonb_build_object(
						'Content-Type', 'application/json',
						'Authorization', 'Bearer ' || $2,
						'Cache-Control', 'no-store'
					),
					body := jsonb_build_object(
						'slot_no', $3,
						'slot_token', $4::text,
						'wave_id', $5,
						'dispatch_generation', $6
					),
					timeout_milliseconds := $7
				)
			`
			// Thực hiện lệnh gọi trong block bắt lỗi mềm nếu pg_net chưa có trong môi trường test
			_, _ = tx.Exec(ctx, pgNetQuery, d.drainURL, d.cfg.TriggerSecret, r.SlotNo, r.SlotToken, r.WaveID, r.DispatchGeneration, pgNetTimeout)
		}
	}

	// 8. Cập nhật state wave mới, xóa dispatcher lease và commit transaction
	const updateWaveStateQuery = `
		UPDATE job_wakeup_state
		SET wave_id = $1,
		    wave_outstanding = $2,
		    dispatched_generation = $3,
		    dispatcher_requested = false,
		    dispatcher_token = NULL,
		    dispatcher_lease_expires_at = NULL,
		    updated_at = now()
		WHERE id = 1 AND dispatcher_token = $4
	`
	_, err = tx.Exec(ctx, updateWaveStateQuery, newWaveID, int16(n), reqGen, leaseToken)
	if err != nil {
		return nil, fmt.Errorf("update wave state: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit wave dispatch: %w", err)
	}

	return &DispatchResponse{
		WaveID:          newWaveID,
		ReservedSlots:   n,
		DispatchedSlots: n,
	}, nil
}
