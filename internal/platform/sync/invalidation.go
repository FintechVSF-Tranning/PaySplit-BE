package sync

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// RecordBillInvalidation ghi nhận invalidation cho bill trong cùng transaction với thay đổi dữ liệu (Spec 0010 AC-8)
func RecordBillInvalidation(ctx context.Context, tx pgx.Tx, groupID, billID uuid.UUID, version int64) error {
	if tx == nil {
		return nil
	}

	// 1. Khóa lock timeout ngắn 2s
	_, _ = tx.Exec(ctx, `SET LOCAL lock_timeout = '2s'`)

	// 2. Tăng counter tuần tự sync_sequence_state
	var nextSeq int64
	const seqQuery = `UPDATE sync_sequence_state SET value = value + 1, updated_at = now() WHERE id = 1 RETURNING value`
	if err := tx.QueryRow(ctx, seqQuery).Scan(&nextSeq); err != nil {
		return fmt.Errorf("increment sync sequence: %w", err)
	}

	// 3. Chèn bản ghi realtime_invalidations
	const insertQuery = `
		INSERT INTO realtime_invalidations (sequence, group_id, aggregate_type, aggregate_id, version)
		VALUES ($1, $2, 'bill', $3, $4)
		ON CONFLICT (aggregate_type, aggregate_id, version) DO NOTHING
	`
	if _, err := tx.Exec(ctx, insertQuery, nextSeq, groupID, billID, version); err != nil {
		return fmt.Errorf("insert realtime invalidation: %w", err)
	}

	return nil
}

// RecordGroupInvalidation ghi nhận invalidation cho group và cập nhật membership_sync_version cho các users (Spec 0010 AC-7, AC-8)
func RecordGroupInvalidation(ctx context.Context, tx pgx.Tx, groupID uuid.UUID, affectedUserIDs []uuid.UUID) error {
	if tx == nil {
		return nil
	}

	_, _ = tx.Exec(ctx, `SET LOCAL lock_timeout = '2s'`)

	var nextSeq int64
	const seqQuery = `UPDATE sync_sequence_state SET value = value + 1, updated_at = now() WHERE id = 1 RETURNING value`
	if err := tx.QueryRow(ctx, seqQuery).Scan(&nextSeq); err != nil {
		return fmt.Errorf("increment sync sequence: %w", err)
	}

	// Cập nhật membership_sync_version cho affected users
	if len(affectedUserIDs) > 0 {
		const updateMemQuery = `
			UPDATE users
			SET membership_sync_version = membership_sync_version + 1,
			    updated_at = now()
			WHERE id = ANY($1)
		`
		_, _ = tx.Exec(ctx, updateMemQuery, affectedUserIDs)
	}

	nowVersion := time.Now().UnixMilli()
	const insertQuery = `
		INSERT INTO realtime_invalidations (sequence, group_id, aggregate_type, aggregate_id, version)
		VALUES ($1, $2, 'group', $2, $3)
		ON CONFLICT (aggregate_type, aggregate_id, version) DO NOTHING
	`
	if _, err := tx.Exec(ctx, insertQuery, nextSeq, groupID, nowVersion); err != nil {
		return fmt.Errorf("insert realtime invalidation: %w", err)
	}

	return nil
}

// RecordSettlementInvalidation ghi nhận invalidation cho settlement (Spec 0010 AC-8)
func RecordSettlementInvalidation(ctx context.Context, tx pgx.Tx, groupID, settlementID uuid.UUID, version int64) error {
	if tx == nil {
		return nil
	}

	_, _ = tx.Exec(ctx, `SET LOCAL lock_timeout = '2s'`)

	var nextSeq int64
	const seqQuery = `UPDATE sync_sequence_state SET value = value + 1, updated_at = now() WHERE id = 1 RETURNING value`
	if err := tx.QueryRow(ctx, seqQuery).Scan(&nextSeq); err != nil {
		return fmt.Errorf("increment sync sequence: %w", err)
	}

	const insertQuery = `
		INSERT INTO realtime_invalidations (sequence, group_id, aggregate_type, aggregate_id, version)
		VALUES ($1, $2, 'settlement', $3, $4)
		ON CONFLICT (aggregate_type, aggregate_id, version) DO NOTHING
	`
	if _, err := tx.Exec(ctx, insertQuery, nextSeq, groupID, settlementID, version); err != nil {
		return fmt.Errorf("insert realtime invalidation: %w", err)
	}

	return nil
}
