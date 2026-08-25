-- ============================================================================
-- GROUP BILL CLOSE V1 (Spec 0008)
-- Khóa gửi hóa đơn một chiều, batch chốt toàn bộ và item xử lý từng bill.
-- ============================================================================

-- name: GetGroupSubmissionLock :one
SELECT bill_submission_locked_at FROM groups
WHERE id = $1 AND status = 'active';

-- name: SetGroupSubmissionLockedAt :one
UPDATE groups
SET bill_submission_locked_at = COALESCE(bill_submission_locked_at, now())
WHERE id = $1 AND status = 'active'
RETURNING bill_submission_locked_at;

-- name: CaptureOpenBills :many
SELECT id, version, (status = 'reviewed') AS captured_reviewed
FROM bills
WHERE group_id = $1 AND status IN ('draft', 'reviewed')
ORDER BY id;

-- name: InsertFinalizeBatch :one
INSERT INTO group_bill_finalize_batches (
    id, group_id, requested_by_member_id, status,
    target_count, finalized_count, failed_count,
    created_at, updated_at, started_at, completed_at
) VALUES (
    $1, $2, $3, $4, $5, 0, 0, now(), now(), $6, $7
) RETURNING *;

-- name: GetActiveFinalizeBatch :many
SELECT * FROM group_bill_finalize_batches
WHERE group_id = $1 AND status IN ('queued', 'processing')
ORDER BY created_at ASC;

-- name: GetFinalizeBatchByID :one
SELECT * FROM group_bill_finalize_batches
WHERE id = $1 AND group_id = $2;

-- name: GetLatestFinalizeBatch :one
SELECT * FROM group_bill_finalize_batches
WHERE id = (
    SELECT b.id FROM group_bill_finalize_batches b
    WHERE b.group_id = $1
    ORDER BY b.created_at DESC, b.id DESC
    LIMIT 1
);

-- name: PromoteBatchToProcessing :execrows
UPDATE group_bill_finalize_batches
SET status = 'processing', started_at = now(), updated_at = now()
WHERE id = $1 AND status = 'queued';

-- name: CountPendingBatchItems :one
SELECT count(*) FROM group_bill_finalize_items
WHERE batch_id = $1 AND status = 'pending';

-- name: CompleteFinalizeBatch :execrows
UPDATE group_bill_finalize_batches
SET status = 'completed',
    finalized_count = $2,
    failed_count = $3,
    started_at = COALESCE(started_at, now()),
    completed_at = now(),
    updated_at = now()
WHERE id = $1 AND status IN ('queued', 'processing')
  AND finalized_count + failed_count = $4;

-- name: GetActiveCaptainMembership :one
SELECT id, group_id, user_id, role, status, joined_at, left_at
FROM group_members gm
WHERE group_id = $1 AND status = 'active' AND role = 'captain'
  AND EXISTS (SELECT 1 FROM groups g WHERE g.id = gm.group_id AND g.status = 'active');

-- name: IncrementBatchFinalizedCount :execrows
UPDATE group_bill_finalize_batches
SET finalized_count = finalized_count + 1, updated_at = now()
WHERE id = $1;

-- name: IncrementBatchFailedCount :execrows
UPDATE group_bill_finalize_batches
SET failed_count = failed_count + 1, updated_at = now()
WHERE id = $1;

-- name: InsertFinalizeItem :execrows
INSERT INTO group_bill_finalize_items (
    batch_id, bill_id, bill_version, captured_reviewed, status,
    created_at, updated_at
) VALUES (
    $1, $2, $3, $4, 'pending', now(), now()
);

-- name: LockBatchRowForUpdate :one
SELECT * FROM group_bill_finalize_batches
WHERE id = $1
FOR UPDATE;

-- name: LockBatchItemForUpdate :one
SELECT * FROM group_bill_finalize_items
WHERE batch_id = $1 AND bill_id = $2
FOR UPDATE;

-- name: MarkBatchItemFinalized :execrows
UPDATE group_bill_finalize_items
SET status = 'finalized', error_code = NULL, processed_at = now(), updated_at = now()
WHERE batch_id = $1 AND bill_id = $2 AND status = 'pending';

-- name: MarkBatchItemFailed :execrows
UPDATE group_bill_finalize_items
SET status = 'failed', error_code = $3, processed_at = now(), updated_at = now()
WHERE batch_id = $1 AND bill_id = $2 AND status = 'pending';

-- name: ListBatchItemsPage :many
SELECT i.bill_id, i.bill_version, i.captured_reviewed, i.status,
       i.error_code, i.processed_at, i.created_at,
       b.merchant_name AS bill_display_name
FROM group_bill_finalize_items i
LEFT JOIN bills b ON b.id = i.bill_id
WHERE i.batch_id = $1
  AND (
    $2::timestamptz IS NULL
    OR i.created_at > $2
    OR (i.created_at = $2 AND i.bill_id > $3::uuid)
  )
ORDER BY i.created_at ASC, i.bill_id ASC
LIMIT $4;

-- name: CountActiveBatchesForGroup :one
SELECT count(*) FROM group_bill_finalize_batches
WHERE group_id = $1 AND status IN ('queued', 'processing');

-- Rào chắn archive với batch đang chạy được kiểm tra trong code (DisbandGroup
-- của module group) ngay sau khi giữ khóa nhóm, không dùng trigger.

-- ============================================================================
-- BATCH ITEM WORKER SUPPORT (đọc trong transaction của item)
-- ============================================================================

-- name: GetBillStateForBatch :one
SELECT id, group_id, creditor_member_id, status, version
FROM bills
WHERE id = $1 AND group_id = $2
FOR UPDATE;
