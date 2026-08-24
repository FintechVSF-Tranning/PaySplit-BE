-- +goose NO TRANSACTION
-- +goose Up

DO $$ BEGIN CREATE TYPE payment_status AS ENUM ('pending_proof', 'pending_confirmation', 'confirmed', 'rejected', 'superseded'); EXCEPTION WHEN duplicate_object THEN null; END $$;

ALTER TABLE payments ADD COLUMN IF NOT EXISTS status payment_status NOT NULL DEFAULT 'pending_proof';
ALTER TABLE payments ADD COLUMN IF NOT EXISTS recipient_bank_code TEXT;
ALTER TABLE payments ADD COLUMN IF NOT EXISTS recipient_bank_name TEXT;
ALTER TABLE payments ADD COLUMN IF NOT EXISTS recipient_account_number TEXT;
ALTER TABLE payments ADD COLUMN IF NOT EXISTS recipient_account_holder TEXT;
ALTER TABLE payments ADD COLUMN IF NOT EXISTS stalled_alerted_at TIMESTAMPTZ;
ALTER TABLE debts ADD COLUMN IF NOT EXISTS last_reminded_at TIMESTAMPTZ;

UPDATE payments
SET status = CASE
    WHEN confirmed_at IS NOT NULL THEN 'confirmed'::payment_status
    WHEN rejected_at IS NOT NULL THEN 'rejected'::payment_status
    WHEN submitted_at IS NOT NULL THEN 'pending_confirmation'::payment_status
    ELSE 'pending_proof'::payment_status
END;

ALTER TABLE payments DROP CONSTRAINT IF EXISTS chk_payments_snapshot_with_submission;
ALTER TABLE payments ADD CONSTRAINT chk_payments_snapshot_with_submission CHECK (
    (submitted_at IS NULL) = (recipient_bank_code IS NULL)
    AND (submitted_at IS NULL) = (recipient_bank_name IS NULL)
    AND (submitted_at IS NULL) = (recipient_account_number IS NULL)
    AND (submitted_at IS NULL) = (recipient_account_holder IS NULL)
) NOT VALID;
ALTER TABLE payments VALIDATE CONSTRAINT chk_payments_snapshot_with_submission;

ALTER TABLE payments DROP CONSTRAINT IF EXISTS chk_payments_status_confirmed_at;
ALTER TABLE payments DROP CONSTRAINT IF EXISTS chk_payments_status_rejected_at;
ALTER TABLE payments DROP CONSTRAINT IF EXISTS chk_payments_status_submission;
ALTER TABLE payments DROP CONSTRAINT IF EXISTS chk_payments_state_matrix;
ALTER TABLE payments ADD CONSTRAINT chk_payments_state_matrix CHECK (
    (status IN ('pending_proof', 'superseded')
        AND submitted_at IS NULL AND image_object_key IS NULL
        AND recipient_bank_code IS NULL AND recipient_bank_name IS NULL
        AND recipient_account_number IS NULL AND recipient_account_holder IS NULL
        AND confirmed_at IS NULL AND rejected_at IS NULL AND rejection_reason IS NULL
        AND stalled_alerted_at IS NULL)
 OR (status = 'pending_confirmation'
        AND submitted_at IS NOT NULL AND image_object_key IS NOT NULL
        AND recipient_bank_code IS NOT NULL AND recipient_bank_name IS NOT NULL
        AND recipient_account_number IS NOT NULL AND recipient_account_holder IS NOT NULL
        AND confirmed_at IS NULL AND rejected_at IS NULL AND rejection_reason IS NULL)
 OR (status = 'confirmed'
        AND submitted_at IS NOT NULL AND image_object_key IS NOT NULL
        AND recipient_bank_code IS NOT NULL AND recipient_bank_name IS NOT NULL
        AND recipient_account_number IS NOT NULL AND recipient_account_holder IS NOT NULL
        AND confirmed_at IS NOT NULL AND rejected_at IS NULL AND rejection_reason IS NULL)
 OR (status = 'rejected'
        AND submitted_at IS NOT NULL AND image_object_key IS NOT NULL
        AND recipient_bank_code IS NOT NULL AND recipient_bank_name IS NOT NULL
        AND recipient_account_number IS NOT NULL AND recipient_account_holder IS NOT NULL
        AND confirmed_at IS NULL AND rejected_at IS NOT NULL
        AND rejection_reason IS NOT NULL AND length(btrim(rejection_reason)) BETWEEN 1 AND 500)
) NOT VALID;
ALTER TABLE payments VALIDATE CONSTRAINT chk_payments_state_matrix;

ALTER TABLE payments DROP CONSTRAINT IF EXISTS uq_payments_payment_debt_identity;
ALTER TABLE payments ADD CONSTRAINT uq_payments_payment_debt_identity
    UNIQUE (id, group_id, debtor_member_id, creditor_member_id);
ALTER TABLE debts DROP CONSTRAINT IF EXISTS uq_debts_payment_debt_identity;
ALTER TABLE debts ADD CONSTRAINT uq_debts_payment_debt_identity
    UNIQUE (id, group_id, debtor_member_id, creditor_member_id);
ALTER TABLE debts DROP CONSTRAINT IF EXISTS chk_debts_reminder_count;
ALTER TABLE debts ADD CONSTRAINT chk_debts_reminder_count
    CHECK (reminder_count BETWEEN 0 AND 3) NOT VALID;
ALTER TABLE debts VALIDATE CONSTRAINT chk_debts_reminder_count;

CREATE TABLE IF NOT EXISTS payment_debts (
    payment_id UUID NOT NULL,
    debt_id UUID NOT NULL,
    group_id UUID NOT NULL,
    debtor_member_id UUID NOT NULL,
    creditor_member_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (payment_id, debt_id),
    FOREIGN KEY (payment_id, group_id, debtor_member_id, creditor_member_id)
        REFERENCES payments(id, group_id, debtor_member_id, creditor_member_id) ON DELETE CASCADE,
    FOREIGN KEY (debt_id, group_id, debtor_member_id, creditor_member_id)
        REFERENCES debts(id, group_id, debtor_member_id, creditor_member_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_payment_debts_debt ON payment_debts(debt_id, payment_id);
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS uq_payments_pending_proof_pair
    ON payments(group_id, debtor_member_id, creditor_member_id)
    WHERE status = 'pending_proof';

DO $$ BEGIN EXECUTE 'ALTER TYPE activity_type RENAME VALUE ''submitted_proof'' TO ''payment_submitted'''; EXCEPTION WHEN invalid_parameter_value THEN null; END $$;
DO $$ BEGIN EXECUTE 'ALTER TYPE activity_type RENAME VALUE ''confirmed_payment'' TO ''payment_confirmed'''; EXCEPTION WHEN invalid_parameter_value THEN null; END $$;
DO $$ BEGIN EXECUTE 'ALTER TYPE activity_type RENAME VALUE ''rejected_payment'' TO ''payment_rejected'''; EXCEPTION WHEN invalid_parameter_value THEN null; END $$;
DO $$ BEGIN EXECUTE 'ALTER TYPE activity_type RENAME VALUE ''stalled_payment_reminder'' TO ''payment_stalled_confirmation'''; EXCEPTION WHEN invalid_parameter_value THEN null; END $$;
DO $$ BEGIN ALTER TYPE activity_type ADD VALUE IF NOT EXISTS 'payment_created'; EXCEPTION WHEN duplicate_object THEN null; END $$;
DO $$ BEGIN ALTER TYPE activity_type ADD VALUE IF NOT EXISTS 'debt_reminded'; EXCEPTION WHEN duplicate_object THEN null; END $$;

DO $$ BEGIN CREATE TYPE activity_actor_kind AS ENUM ('member', 'system'); EXCEPTION WHEN duplicate_object THEN null; END $$;
ALTER TABLE group_activities ALTER COLUMN actor_member_id DROP NOT NULL;
ALTER TABLE group_activities ADD COLUMN IF NOT EXISTS actor_kind activity_actor_kind NOT NULL DEFAULT 'member';
ALTER TABLE group_activities DROP CONSTRAINT IF EXISTS chk_group_activities_actor;
ALTER TABLE group_activities ADD CONSTRAINT chk_group_activities_actor CHECK (
    (actor_kind = 'member' AND actor_member_id IS NOT NULL)
 OR (actor_kind = 'system' AND actor_member_id IS NULL)
);

CREATE TABLE IF NOT EXISTS payment_idempotency_keys (
    actor_user_id          UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    operation              TEXT NOT NULL,
    key_hash               TEXT NOT NULL,
    canonical_request_hash TEXT NOT NULL,
    operation_id           UUID,
    state                  idempotency_state NOT NULL DEFAULT 'in_progress',
    response_code          INT,
    response_body          JSONB,
    retry_after            TIMESTAMPTZ,
    expires_at             TIMESTAMPTZ NOT NULL DEFAULT (now() + INTERVAL '24 hours'),
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (actor_user_id, operation, key_hash)
);

CREATE INDEX IF NOT EXISTS idx_payment_idempotency_keys_expiry ON payment_idempotency_keys(expires_at);

CREATE OR REPLACE VIEW v_member_balances AS
SELECT m.group_id,
       m.id AS member_id,
       COALESCE(cr.total, 0) - COALESCE(dr.total, 0) AS net_balance
FROM group_members m
LEFT JOIN (
    SELECT creditor_member_id AS mid, SUM(amount) AS total
    FROM debts WHERE status IN ('awaiting', 'pending_confirmation') GROUP BY 1
) cr ON cr.mid = m.id
LEFT JOIN (
    SELECT debtor_member_id AS mid, SUM(amount) AS total
    FROM debts WHERE status IN ('awaiting', 'pending_confirmation') GROUP BY 1
) dr ON dr.mid = m.id;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_debts_group_status
    ON debts(group_id, status, created_at DESC, id DESC);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_debts_debtor_status
    ON debts(group_id, debtor_member_id, status, created_at DESC, id DESC) INCLUDE (amount, creditor_member_id);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_debts_creditor_status
    ON debts(group_id, creditor_member_id, status, created_at DESC, id DESC) INCLUDE (amount, debtor_member_id);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_debts_reminders
    ON debts(status, created_at, last_reminded_at) WHERE status = 'awaiting' AND reminder_count < 3;
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_payments_group_status
    ON payments(group_id, status, created_at DESC, id DESC);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_payments_stalled_confirmation
    ON payments(status, submitted_at) WHERE status = 'pending_confirmation' AND stalled_alerted_at IS NULL;

-- +goose Down

DROP TABLE IF EXISTS payment_idempotency_keys;
DROP TABLE IF EXISTS payment_debts;
DROP INDEX IF EXISTS uq_payments_pending_proof_pair;
DROP INDEX IF EXISTS idx_payments_stalled_confirmation;
DROP INDEX IF EXISTS idx_payments_group_status;
DROP INDEX IF EXISTS idx_debts_reminders;
DROP INDEX IF EXISTS idx_debts_creditor_status;
DROP INDEX IF EXISTS idx_debts_debtor_status;
DROP INDEX IF EXISTS idx_debts_group_status;

CREATE OR REPLACE VIEW v_member_balances AS
SELECT m.group_id,
       m.id AS member_id,
       COALESCE(cr.total, 0) - COALESCE(dr.total, 0) AS net_balance
FROM group_members m
LEFT JOIN (SELECT creditor_member_id AS mid, SUM(amount) AS total FROM debts WHERE status <> 'settled' GROUP BY 1) cr ON cr.mid = m.id
LEFT JOIN (SELECT debtor_member_id AS mid, SUM(amount) AS total FROM debts WHERE status <> 'settled' GROUP BY 1) dr ON dr.mid = m.id;

ALTER TABLE payments
    DROP CONSTRAINT IF EXISTS uq_payments_payment_debt_identity,
    DROP CONSTRAINT IF EXISTS chk_payments_state_matrix,
    DROP CONSTRAINT IF EXISTS chk_payments_snapshot_with_submission,
    DROP COLUMN IF EXISTS stalled_alerted_at,
    DROP COLUMN IF EXISTS recipient_account_holder,
    DROP COLUMN IF EXISTS recipient_account_number,
    DROP COLUMN IF EXISTS recipient_bank_name,
    DROP COLUMN IF EXISTS recipient_bank_code,
    DROP COLUMN IF EXISTS status;

ALTER TABLE debts
    DROP CONSTRAINT IF EXISTS uq_debts_payment_debt_identity,
    DROP CONSTRAINT IF EXISTS chk_debts_reminder_count,
    DROP COLUMN IF EXISTS last_reminded_at;

DELETE FROM group_activities WHERE actor_kind = 'system';
ALTER TABLE group_activities
    DROP CONSTRAINT IF EXISTS chk_group_activities_actor,
    DROP COLUMN IF EXISTS actor_kind;
ALTER TABLE group_activities ALTER COLUMN actor_member_id SET NOT NULL;

DO $$ BEGIN EXECUTE 'ALTER TYPE activity_type RENAME VALUE ''payment_submitted'' TO ''submitted_proof'''; EXCEPTION WHEN invalid_parameter_value THEN null; END $$;
DO $$ BEGIN EXECUTE 'ALTER TYPE activity_type RENAME VALUE ''payment_confirmed'' TO ''confirmed_payment'''; EXCEPTION WHEN invalid_parameter_value THEN null; END $$;
DO $$ BEGIN EXECUTE 'ALTER TYPE activity_type RENAME VALUE ''payment_rejected'' TO ''rejected_payment'''; EXCEPTION WHEN invalid_parameter_value THEN null; END $$;
DO $$ BEGIN EXECUTE 'ALTER TYPE activity_type RENAME VALUE ''payment_stalled_confirmation'' TO ''stalled_payment_reminder'''; EXCEPTION WHEN invalid_parameter_value THEN null; END $$;
DROP TYPE IF EXISTS payment_status;
DROP TYPE IF EXISTS activity_actor_kind;
