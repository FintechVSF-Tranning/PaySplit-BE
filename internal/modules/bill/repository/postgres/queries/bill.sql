-- name: CreateBill :one
INSERT INTO bills (
    id,
    group_id,
    creditor_member_id,
    status,
    merchant_name,
    bill_date,
    subtotal,
    service_charge,
    vat,
    discount,
    total,
    split_method,
    mismatch_codes,
    replaces_bill_id,
    version,
    created_at,
    updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, 1, now(), now()
) RETURNING *;

-- name: GetBillByID :one
SELECT * FROM bills
WHERE id = $1 AND group_id = $2;

-- name: GetBillByIDForUpdate :one
SELECT * FROM bills
WHERE id = $1 AND group_id = $2
FOR UPDATE;

-- name: ListBillsByGroup :many
SELECT * FROM bills
WHERE group_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: UpdateDraftBill :one
UPDATE bills
SET merchant_name = $3,
    bill_date = $4,
    subtotal = $5,
    service_charge = $6,
    vat = $7,
    discount = $8,
    total = $9,
    split_method = $10,
    mismatch_codes = $11,
    version = version + 1,
    updated_at = now()
WHERE id = $1 AND group_id = $2 AND status = 'draft' AND version = $12
RETURNING *;

-- name: ReviewBill :one
UPDATE bills
SET status = 'reviewed',
    version = version + 1,
    updated_at = now()
WHERE id = $1 AND group_id = $2 AND status = 'draft' AND version = $3
RETURNING *;

-- name: FinalizeBill :one
UPDATE bills
SET status = 'finalized',
    finalized_at = now(),
    version = version + 1,
    updated_at = now()
WHERE id = $1 AND group_id = $2 AND status IN ('draft', 'reviewed') AND version = $3
RETURNING *;

-- name: VoidBill :one
UPDATE bills
SET status = 'voided',
    voided_at = now(),
    version = version + 1,
    updated_at = now()
WHERE id = $1 AND group_id = $2 AND status = 'finalized' AND version = $3
RETURNING *;

-- name: DeleteDraftBill :exec
DELETE FROM bills
WHERE id = $1 AND group_id = $2 AND status = 'draft';

-- ============================================================================
-- BILL IMAGES
-- ============================================================================

-- name: CreateBillImage :one
INSERT INTO bill_images (
    id,
    bill_id,
    group_id,
    image_key,
    position,
    created_at
) VALUES (
    $1, $2, $3, $4, $5, now()
) RETURNING *;

-- name: ListBillImages :many
SELECT * FROM bill_images
WHERE bill_id = $1
ORDER BY position ASC;

-- name: DeleteBillImages :exec
DELETE FROM bill_images
WHERE bill_id = $1;

-- ============================================================================
-- BILL ITEMS & ASSIGNMENTS
-- ============================================================================

-- name: CreateBillItem :one
INSERT INTO bill_items (
    id,
    bill_id,
    group_id,
    name,
    quantity,
    unit_price,
    line_total,
    position,
    created_at,
    updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, now(), now()
) RETURNING *;

-- name: ListBillItems :many
SELECT * FROM bill_items
WHERE bill_id = $1
ORDER BY position ASC;

-- name: DeleteBillItems :exec
DELETE FROM bill_items
WHERE bill_id = $1;

-- name: CreateBillItemAssignment :one
INSERT INTO bill_item_assignments (
    id,
    bill_item_id,
    group_id,
    member_id,
    weight,
    created_at
) VALUES (
    $1, $2, $3, $4, $5, now()
) RETURNING *;

-- name: ListBillItemAssignmentsByItem :many
SELECT * FROM bill_item_assignments
WHERE bill_item_id = $1;

-- name: ListBillItemAssignmentsByBill :many
SELECT a.* 
FROM bill_item_assignments a
JOIN bill_items bi ON a.bill_item_id = bi.id
WHERE bi.bill_id = $1;

-- name: DeleteBillItemAssignmentsByItem :exec
DELETE FROM bill_item_assignments
WHERE bill_item_id = $1;

-- ============================================================================
-- BILL SHARES (Hamilton Finalized Snapshot)
-- ============================================================================

-- name: CreateBillShare :one
INSERT INTO bill_shares (
    id,
    bill_id,
    group_id,
    member_id,
    computed_amount,
    created_at
) VALUES (
    $1, $2, $3, $4, $5, now()
) RETURNING *;

-- name: ListBillShares :many
SELECT * FROM bill_shares
WHERE bill_id = $1;

-- name: DeleteBillShares :exec
DELETE FROM bill_shares
WHERE bill_id = $1;

-- ============================================================================
-- OCR JOBS
-- ============================================================================

-- name: CreateOCRJob :one
INSERT INTO ocr_jobs (
    id,
    bill_id,
    status,
    provider,
    attempts,
    raw_response,
    candidate,
    version,
    created_at,
    updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, 1, now(), now()
) RETURNING *;

-- name: GetOCRJobByID :one
SELECT * FROM ocr_jobs
WHERE id = $1;

-- name: GetActiveOCRJobByBillID :one
SELECT * FROM ocr_jobs
WHERE bill_id = $1 AND status IN ('queued', 'processing')
LIMIT 1;

-- name: GetLatestOCRJobByBillID :one
SELECT * FROM ocr_jobs
WHERE bill_id = $1
ORDER BY created_at DESC
LIMIT 1;

-- name: UpdateOCRJobProcessing :one
UPDATE ocr_jobs
SET status = 'processing',
    attempts = attempts + 1,
    version = version + 1,
    updated_at = now()
WHERE id = $1 AND version = $2
RETURNING *;

-- name: UpdateOCRJobSuccess :one
UPDATE ocr_jobs
SET status = 'succeeded',
    candidate = $3,
    raw_response = $4,
    version = version + 1,
    updated_at = now(),
    completed_at = now()
WHERE id = $1 AND version = $2
RETURNING *;

-- name: UpdateOCRJobFailed :one
UPDATE ocr_jobs
SET status = 'failed',
    error_message = $3,
    version = version + 1,
    updated_at = now(),
    completed_at = now()
WHERE id = $1 AND version = $2
RETURNING *;

-- name: CountManualOCRAttemptsInWindow :one
SELECT COUNT(*) FROM ocr_jobs
WHERE bill_id = $1 AND created_at >= $2;

-- name: GetGroupMember :one
SELECT id, group_id, user_id, role, status, joined_at, left_at
FROM group_members
WHERE group_id = $1 AND user_id = $2;

-- name: ListActiveGroupMembers :many
SELECT id, group_id, user_id, role, status, joined_at, left_at
FROM group_members
WHERE group_id = $1 AND status = 'active';

-- name: CreateDebt :one
INSERT INTO debts (
    id,
    group_id,
    bill_id,
    debtor_member_id,
    creditor_member_id,
    amount,
    status,
    reminder_count,
    created_at,
    updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6, 'awaiting', 0, now(), now()
) RETURNING *;

-- name: VoidDebtsByBillID :exec
UPDATE debts
SET status = 'voided',
    updated_at = now()
WHERE bill_id = $1 AND status = 'awaiting';
