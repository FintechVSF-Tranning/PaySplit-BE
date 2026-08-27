-- +goose Up

-- Spec 0003, revised AC-6 and AC-10.
-- A reviewed draft was approved against the previous Creditor absorption amounts.
-- Clear that approval before the new exact allocator can finalize a different breakdown.
UPDATE bills
SET status = 'draft',
    reviewed_at = NULL,
    reviewed_by_member_id = NULL,
    updated_at = now()
WHERE status = 'reviewed';

-- +goose Down

-- Review approval cannot be restored safely because the old actor and timestamp would approve
-- amounts produced by another algorithm. Rollback intentionally leaves these bills as drafts.
SELECT 1;
