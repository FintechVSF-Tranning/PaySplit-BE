# 0004. Split and settlement v1

**Date**: 2026-08-17
**Status**: In Progress

## Summary

Split and settlement v1 provides personal expense tracking, group debt visibility, VietQR payment generation, transfer proof submission, and manual creditor confirmation. The system acts as a pure payment coordinator without holding or transferring user funds, keeping operations compliant with payment regulations. Short database transactions, strict lock ordering across groups, debts, and payments, and durable background workers guarantee exact peer to peer debt settlement.

## Rationale

Reasoning and options considered: see [rationale.md](rationale.md).

## Requirements

**User stories**:

1. As a Payer, I want to view my allocated expense breakdown so that I understand exactly what items and surcharges I owe across finalized bills.
2. As an active group member, I want to view the group debt list and balances so that I know who owes whom and what my net balance is.
3. As a Payer, I want to generate a VietQR code for one or multiple debts owed to the same Creditor so that I can transfer money quickly without typing account details manually.
4. As a Payer, I want to submit transfer proof with an optional note so that my Creditor can verify that I sent the money.
5. As a Creditor, I want to confirm or reject a submitted payment with a clear reason so that debt records reflect true bank transfers.
6. As a Creditor or Captain, I want the system to remind debtors automatically and alert me when confirmations remain pending for too long so that debts are settled promptly.

**Acceptance criteria**:

1. **AC-1**: An authenticated active member can view their personal allocated expenses in the group through `GET /api/v1/groups/{groupId}/expenses/me`. The response returns an aggregate financial summary and a paginated list of every finalized bill with an allocation for the caller. Each bill contains its allocation summary exactly once and nests item rows containing only item level values. If the caller is the bill Creditor or otherwise has no debt row, `debt_id` and `debt_status` are null. `total_owed` is the caller's debtor amount in `awaiting` plus `pending_confirmation`, `total_settled` is the caller's debtor amount in `settled`, `total_receivable` is the caller's creditor amount in `awaiting` plus `pending_confirmation`, and `net_balance` comes from `v_member_balances`.
2. **AC-2**: An authenticated active member can view group debts through `GET /api/v1/groups/{groupId}/debts`. The endpoint supports cursor pagination and filters the returned debt list by `debtor_member_id`, `creditor_member_id`, and `status` (`awaiting`, `pending_confirmation`, `settled`). Its caller payable, caller receivable, and net debt matrix always represent the whole group's current unsettled obligations and exclude `settled` and `voided`, independent of list filters.
3. **AC-3**: A Payer can generate a payment QR for a specific Creditor in the group through `POST /api/v1/groups/{groupId}/payments/qr`. Omitted `debt_ids` selects all `awaiting` debts owed by the caller to that Creditor. A provided array must contain 1 through 100 unique UUIDs. An empty array or duplicates return `400 VALIDATION_FAILED`; zero eligible debts or a linked debt with the wrong group, debtor, creditor, or a non awaiting status returns `409 DEBTS_NOT_AWAITING`; and a missing Creditor returns `404 CREDITOR_NOT_FOUND`.
4. **AC-4**: Payment QR generation returns a `201` response with a new payment record in `pending_proof` status and immutable `payment_debts` rows for the selected debt set, or returns `200` replaying the existing unsubmitted payment when that set matches exactly. Creating a QR for a different set supersedes the prior `pending_proof` payment for the same group, Payer, and Creditor. The generated `reference_code` uses the prefix `PAY` followed by eight unguessable uppercase alphanumeric characters, unique across the entire system. The response returns a locally encoded NAPAS account transfer TLV payload and a percent encoded `img.vietqr.io` image URL containing the bank BIN, account number, compact template, integer VND amount, reference code, and account holder display name.
5. **AC-5**: If the Creditor has no valid bank account configured in their profile, payment QR generation is rejected with `422 BANK_ACCOUNT_REQUIRED`. If that account is removed or becomes invalid while a payment remains in `pending_proof`, the payment record is preserved but payment detail reads and proof submission return `422 BANK_ACCOUNT_REQUIRED` until the profile is corrected. Pending proof reads always regenerate recipient information and QR output from the current valid profile. `pending_confirmation`, `confirmed`, and `rejected` payments use their immutable bank snapshot. A `superseded` payment exposes no active recipient or QR output.
6. **AC-6**: A Payer can submit transfer proof through `POST /api/v1/groups/{groupId}/payments/{paymentId}/proof` with a JPEG, PNG, or HEIC image of at most 10 MB and an optional text note up to 500 characters. After a preliminary authorization and state check, the service creates or reuses the idempotency record's `operation_id`, uploads a private Cloudinary object under the per attempt key `payments/{paymentId}/proofs/{operationId}`, opens a database transaction, and rechecks payment, bank, and linked debt state under lock. Success stores that winning object key, snapshots the Creditor current bank details, transitions the payment and every covered debt to `pending_confirmation`, and enqueues a notification. A losing or failed attempt deletes only its own object; failed deletion creates a durable `media_cleanup_jobs` row for that exact key in a separate transaction.
7. **AC-7**: A Creditor can confirm a pending payment through `POST /api/v1/groups/{groupId}/payments/{paymentId}/confirm`. In one database transaction, the system verifies that the caller is the creditor on the payment, transitions the payment to `confirmed` with `confirmed_at = now()`, marks every covered debt as `settled` with `settled_at = now()`, writes a `payment_confirmed` group activity, and enqueues a settlement notification for the Payer. Settlement is all or nothing for the full payment amount.
8. **AC-8**: A Creditor can reject a pending payment through `POST /api/v1/groups/{groupId}/payments/{paymentId}/reject` by supplying a non empty reason between 1 and 500 characters. In one database transaction, the system verifies that the caller is the creditor on the payment, transitions the payment to `rejected` with `rejected_at = now()` and `rejection_reason`, returns all covered debts to `awaiting` status with `payment_id = NULL`, writes a `payment_rejected` group activity, and enqueues a rejection notification for the Payer.
9. **AC-9**: A Creditor or Captain can manually trigger a debt reminder for an individual `awaiting` debt through `POST /api/v1/groups/{groupId}/debts/{debtId}/remind`. Manual and automated reminders update the same `reminder_count` and `last_reminded_at` under row lock, allow at most three reminders total, and require at least 24 hours between sends.
10. **AC-10**: A River scheduler may run hourly. It conditionally claims `awaiting` debts at least 72 hours old where `reminder_count < 3` and `last_reminded_at` is null or at least 24 hours old. It also conditionally claims payments that have remained in `pending_confirmation` for more than 48 hours and have `stalled_alerted_at IS NULL`. The first worker sets that timestamp and sends exactly one stalled alert; concurrent workers do no duplicate work.
11. **AC-11**: QR creation, proof upload, confirmation, rejection, and manual reminder require an `Idempotency-Key`. For 24 hours, the same key plus the same canonical request hash replays the stored HTTP status and body; a different hash returns `409 IDEMPOTENCY_KEY_REUSED`; an unfinished operation returns `409 IDEMPOTENCY_IN_PROGRESS` with `Retry-After`. The hash includes path identifiers, a normalized body, and sorted debt IDs; proof submission additionally hashes the image bytes with SHA256 and includes the note. A River worker deletes expired records. Mutations lock the group, targeted debts in ascending UUID byte order, then the payment. If proof wins, concurrent void returns `409 PAYMENT_ALREADY_STARTED`; if void wins, it supersedes linked pending proof payments.
12. **AC-12**: Inactive members or users outside the group cannot access any debt, expense, or payment endpoints. Responses expose five minute signed Cloudinary URLs for payment proof images, and logs redact payment proof URLs, reference codes, bank account numbers, and member notes.

## Decision

**Chosen option**: Transactionally coordinated peer to peer settlement with dynamic VietQR generation, ordered row locking, and durable proof handling.

PaySplit coordinates payments directly between debtor and creditor bank accounts without holding funds. PostgreSQL enforces strong consistency and strict lock ordering for debt state transitions. Cloudinary stores private transfer proofs with short lived signed URLs, and River workers handle automated reminders and stalled payment alerts. (basis: project PRD requirements, PostgreSQL foreign key and locking conventions, and VietQR NAPAS standards)

**Implementation skills**: `supabase-postgres-best-practices` (`supabase/agent-skills`, `.agents/skills/supabase-postgres-best-practices/`)

## Feature design

### Data model sketch

| Entity | Required fields | Nullable fields | Relations and constraints |
|---|---|---|---|
| `debts` | `id uuid`, `group_id uuid`, `bill_id uuid`, `debtor_member_id uuid`, `creditor_member_id uuid`, `amount bigint`, `status debt_status`, `reminder_count int`, `created_at`, `updated_at` | `payment_id uuid`, `settled_at timestamptz`, `voided_at timestamptz`, `last_reminded_at timestamptz` | Unique `(id, group_id)` and `(id, group_id, debtor_member_id, creditor_member_id)`. Unique `(bill_id, debtor_member_id, creditor_member_id)`. Foreign keys to `groups`, `bills`, `group_members`, and `payments`. `amount > 0`; reminder count is between zero and three. Statuses: `awaiting`, `pending_confirmation`, `settled`, `voided`. |
| `payments` | `id uuid`, `group_id uuid`, `debtor_member_id uuid`, `creditor_member_id uuid`, `amount bigint`, `reference_code text`, `status payment_status`, `created_at`, `updated_at` | `qr_payload text`, `recipient_bank_code text`, `recipient_bank_name text`, `recipient_account_number text`, `recipient_account_holder text`, `image_object_key text`, `note text`, `rejection_reason text`, `submitted_at timestamptz`, `confirmed_at timestamptz`, `rejected_at timestamptz`, `stalled_alerted_at timestamptz` | Unique `(id, group_id)` and `(id, group_id, debtor_member_id, creditor_member_id)`. Unique `reference_code`. At most one `pending_proof` row per member pair in a group. Statuses: `pending_proof`, `pending_confirmation`, `confirmed`, `rejected`, `superseded`. A state matrix check constrains timestamps, proof object, snapshots, and rejection reason. |
| `payment_debts` | `payment_id uuid`, `debt_id uuid`, `group_id uuid`, `debtor_member_id uuid`, `creditor_member_id uuid`, `created_at timestamptz` | none | Primary key `(payment_id, debt_id)`. Composite foreign keys to the four column identity keys on both `payments` and `debts` enforce the same group, debtor, and creditor in PostgreSQL. Index `(debt_id, payment_id)` supports reverse lookup. Rows are immutable audit links. |
| `group_activities` | `id uuid`, `group_id uuid`, `actor_kind activity_actor_kind`, `action_type activity_type`, `description text`, `metadata jsonb`, `created_at timestamptz` | `actor_member_id uuid` | `actor_kind` is `member` or `system`. A member actor requires `actor_member_id`; a system actor requires it to be null. Member rows retain the group member composite foreign key. River generated reminder and stalled events use the system actor. |
| `payment_idempotency_keys` | `actor_user_id uuid`, `operation text`, `key_hash text`, `canonical_request_hash text`, `operation_id uuid`, `state idempotency_state`, `expires_at timestamptz`, `created_at timestamptz` | `response_code int`, `response_body jsonb`, `retry_after timestamptz` | Unique `(actor_user_id, operation, key_hash)`. 24 hour time to live for payment operations. |
| `media_cleanup_jobs` | `id uuid`, `provider text`, `object_key text`, `reason text`, `attempt_count int`, `created_at`, `updated_at` | `completed_at timestamptz`, `last_error text` | Unique active cleanup job per provider object. Written in a separate transaction only when immediate compensation deletion fails. River retries until complete. |
| `v_member_balances` | `group_id uuid`, `member_id uuid`, `net_balance bigint` | none | Derived PostgreSQL view aggregating unsettled debts where `status NOT IN ('settled', 'voided')`. |

The `payments` and `debts` tables already exist from the initial schema migration (`000001_init_schema.up.sql`), so this feature extends them rather than creating them fresh. The new `payment_debts` table records the exact selected set from QR creation onward without changing debt lifecycle state. `debts.payment_id` remains null while a debt is `awaiting` and is assigned only when proof submission moves that debt to `pending_confirmation`. `payments` currently has no `status` column (state is inferred from `submitted_at`/`confirmed_at`/`rejected_at`) and none of the four `recipient_bank_*` snapshot columns AC-6 needs. `debt_status` already carries two legacy values, `stalled_confirmation` and `rejected`, that predate this design; they stay in the enum unused (Postgres cannot cheaply drop an enum value) rather than being removed. `activity_type` already carries payment related values under different names (`submitted_proof`, `confirmed_payment`, `rejected_payment`, `stalled_payment_reminder`); this feature renames them to the names this spec's Activity contract uses, since no code has written a row under the old names yet. The schema change adds the new `payment_status` type and query indexes for debt filtering and payment lookup:

Spec 0003 has now shipped (its migrations `000006_bill_and_ocr_v1.sql` and `000007_bill_item_discount_v1.sql` are live) and already carries everything this feature's migration depends on, confirmed against the running database on 2026-08-21: `debt_status` has the `voided` value, `debts.voided_at` exists, and `debts_check1` already reads `CHECK ((status = ANY (ARRAY['awaiting'::debt_status, 'voided'::debt_status])) = (payment_id IS NULL))`, exactly the relaxation this spec needed. `v_member_balances` itself is still the original `status <> 'settled'` definition; that redefinition below remains this feature's own migration to write, and it can now run in its own later file without waiting on anything further from spec 0003.

```sql
-- Extend the existing payments table with explicit status and the creditor
-- bank snapshot columns AC-6 requires (see rationale.md for why the column
-- is explicit rather than derived from the timestamp columns).
CREATE TYPE payment_status AS ENUM (
    'pending_proof',
    'pending_confirmation',
    'confirmed',
    'rejected',
    'superseded'
);

ALTER TABLE payments ADD COLUMN status payment_status NOT NULL DEFAULT 'pending_proof';
ALTER TABLE payments ADD COLUMN recipient_bank_code TEXT;
ALTER TABLE payments ADD COLUMN recipient_bank_name TEXT;
ALTER TABLE payments ADD COLUMN recipient_account_number TEXT;
ALTER TABLE payments ADD COLUMN recipient_account_holder TEXT;
ALTER TABLE payments ADD COLUMN stalled_alerted_at TIMESTAMPTZ;
ALTER TABLE debts ADD COLUMN last_reminded_at TIMESTAMPTZ;

ALTER TABLE payments ADD CONSTRAINT uq_payments_payment_debt_identity
    UNIQUE (id, group_id, debtor_member_id, creditor_member_id);
ALTER TABLE debts ADD CONSTRAINT uq_debts_payment_debt_identity
    UNIQUE (id, group_id, debtor_member_id, creditor_member_id);

CREATE TABLE payment_debts (
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
CREATE INDEX idx_payment_debts_debt ON payment_debts(debt_id, payment_id);
CREATE UNIQUE INDEX uq_payments_pending_proof_pair
    ON payments(group_id, debtor_member_id, creditor_member_id)
    WHERE status = 'pending_proof';

-- All four snapshot columns arrive together or not at all, matching the
-- users.bank_* all-or-none convention in 000001_init_schema.up.sql.
ALTER TABLE payments ADD CONSTRAINT chk_payments_snapshot_with_submission
    CHECK ((submitted_at IS NULL) = (recipient_bank_code IS NULL)
       AND (submitted_at IS NULL) = (recipient_bank_name IS NULL)
       AND (submitted_at IS NULL) = (recipient_account_number IS NULL)
       AND (submitted_at IS NULL) = (recipient_account_holder IS NULL))
    NOT VALID;
ALTER TABLE payments VALIDATE CONSTRAINT chk_payments_snapshot_with_submission;

-- Ties the new explicit status column to the existing timestamp columns, the
-- same pairing 000001_init_schema.up.sql already enforces for rejection.
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
ALTER TABLE debts ADD CONSTRAINT chk_debts_reminder_count
    CHECK (reminder_count BETWEEN 0 AND 3) NOT VALID;
ALTER TABLE debts VALIDATE CONSTRAINT chk_debts_reminder_count;

CREATE TYPE activity_actor_kind AS ENUM ('member', 'system');
ALTER TABLE group_activities ALTER COLUMN actor_member_id DROP NOT NULL;
ALTER TABLE group_activities ADD COLUMN actor_kind activity_actor_kind NOT NULL DEFAULT 'member';
ALTER TABLE group_activities ADD CONSTRAINT chk_group_activities_actor CHECK (
    (actor_kind = 'member' AND actor_member_id IS NOT NULL)
 OR (actor_kind = 'system' AND actor_member_id IS NULL)
);

-- Rename the payment related activity_type values already in the enum to the
-- names this feature's Activity contract uses, and add the two new ones.
-- Safe in one transaction: renames touch existing catalog rows, they do not
-- add new ones, and the two newly added values are never referenced below.
ALTER TYPE activity_type RENAME VALUE 'submitted_proof' TO 'payment_submitted';
ALTER TYPE activity_type RENAME VALUE 'confirmed_payment' TO 'payment_confirmed';
ALTER TYPE activity_type RENAME VALUE 'rejected_payment' TO 'payment_rejected';
ALTER TYPE activity_type RENAME VALUE 'stalled_payment_reminder' TO 'payment_stalled_confirmation';
DO $$ BEGIN ALTER TYPE activity_type ADD VALUE IF NOT EXISTS 'payment_created'; EXCEPTION WHEN duplicate_object THEN null; END $$;
DO $$ BEGIN ALTER TYPE activity_type ADD VALUE IF NOT EXISTS 'debt_reminded'; EXCEPTION WHEN duplicate_object THEN null; END $$;

-- Reuse idempotency_state from 000006_bill_and_ocr_v1.sql. This migration
-- must not recreate it, and its Down section must not drop the shared type.

CREATE TABLE payment_idempotency_keys (
    actor_user_id           UUID NOT NULL REFERENCES users(id),
    operation                TEXT NOT NULL,
    key_hash                 TEXT NOT NULL,
    canonical_request_hash   TEXT NOT NULL,
    operation_id              UUID,
    state                     idempotency_state NOT NULL DEFAULT 'in_progress',
    response_code             INT,
    response_body             JSONB,
    retry_after                TIMESTAMPTZ,
    expires_at                 TIMESTAMPTZ NOT NULL DEFAULT (now() + INTERVAL '24 hours'),
    created_at                 TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (actor_user_id, operation, key_hash)
);
CREATE INDEX idx_payment_idempotency_keys_expiry ON payment_idempotency_keys(expires_at);

CREATE TABLE media_cleanup_jobs (
    id UUID PRIMARY KEY,
    provider TEXT NOT NULL,
    object_key TEXT NOT NULL,
    reason TEXT NOT NULL,
    attempt_count INT NOT NULL DEFAULT 0,
    last_error TEXT,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX uq_media_cleanup_jobs_active_object
    ON media_cleanup_jobs(provider, object_key) WHERE completed_at IS NULL;

-- Spec 0003's migration adding 'voided' to debt_status is already applied
-- (see note above), so this redefinition is safe in its own migration file.
-- Keeps the net balance view accurate: a voided debt must stop counting as
-- still owed.
CREATE OR REPLACE VIEW v_member_balances AS
SELECT m.group_id,
       m.id AS member_id,
       COALESCE(cr.total, 0) - COALESCE(dr.total, 0) AS net_balance
FROM group_members m
LEFT JOIN (SELECT creditor_member_id AS mid, SUM(amount) AS total
           FROM debts WHERE status NOT IN ('settled', 'voided') GROUP BY 1) cr ON cr.mid = m.id
LEFT JOIN (SELECT debtor_member_id AS mid, SUM(amount) AS total
           FROM debts WHERE status NOT IN ('settled', 'voided') GROUP BY 1) dr ON dr.mid = m.id;

CREATE INDEX idx_debts_group_status
    ON debts(group_id, status, created_at DESC, id DESC);

CREATE INDEX idx_debts_debtor_status
    ON debts(group_id, debtor_member_id, status, created_at DESC, id DESC)
    INCLUDE (amount, creditor_member_id);

CREATE INDEX idx_debts_creditor_status
    ON debts(group_id, creditor_member_id, status, created_at DESC, id DESC)
    INCLUDE (amount, debtor_member_id);

CREATE INDEX idx_debts_reminders
    ON debts(status, created_at, last_reminded_at)
    WHERE status = 'awaiting' AND reminder_count < 3;

CREATE INDEX idx_payments_group_status
    ON payments(group_id, status, created_at DESC, id DESC);

CREATE INDEX idx_payments_stalled_confirmation
    ON payments(status, submitted_at)
    WHERE status = 'pending_confirmation' AND stalled_alerted_at IS NULL;
```

### State transitions

```text
debts lifecycle:
awaiting -> pending_confirmation (on payment proof submission)
pending_confirmation -> settled (on creditor confirmation)
pending_confirmation -> awaiting (on creditor rejection, payment_id cleared)
awaiting -> voided (on bill void in Module 3)

payments lifecycle:
pending_proof -> pending_confirmation (on proof image upload)
pending_confirmation -> confirmed (on creditor confirmation)
pending_confirmation -> rejected (on creditor rejection)
pending_proof -> superseded (when the Payer regenerates a QR for a different debt set before submitting proof)

payment debt links:
QR creation inserts one immutable payment_debts row per selected awaiting debt
proof submission locks those linked debts, then sets debts.payment_id and pending_confirmation together
rejection clears debts.payment_id but keeps payment_debts for audit
bill void supersedes any pending_proof payment linked to a debt it voids
```

### API surface

All routes require a valid bearer session and are mounted under `/api/v1/groups/{groupId}`.

| Endpoint | Method | Key inputs | Key outputs | Auth | Key errors |
|---|---|---|---|---|---|
| `/expenses/me` | `GET` | `cursor`: string optional, `limit`: int optional | financial summary, bills with nested item allocations, `next_cursor` | active group member | `400 INVALID_CURSOR`, `404 GROUP_NOT_FOUND` |
| `/debts` | `GET` | `debtor_member_id`: UUID optional, `creditor_member_id`: UUID optional, `status`: string optional, `cursor`: string optional, `limit`: int optional | filtered debts list, unfiltered current group summary totals and net debt matrix, `next_cursor` | active group member | `400 INVALID_CURSOR`, `404 GROUP_NOT_FOUND` |
| `/debts/{debtId}/remind` | `POST` | `debtId`: UUID path, `Idempotency-Key` header | reminder count, reminded timestamp | active Creditor or Captain | `403 FORBIDDEN`, `404 DEBT_NOT_FOUND`, `409 DEBT_NOT_AWAITING`, `429 REMINDER_RATE_LIMITED` |
| `/payments/qr` | `POST` | `Idempotency-Key` header, `creditor_member_id`: UUID required, `debt_ids`: UUID array optional | payment object, QR payload string, QR image URL, recipient bank info, covered debts list | active debtor member | `400 VALIDATION_FAILED`, `404 CREDITOR_NOT_FOUND`, `409 DEBTS_NOT_AWAITING`, `422 BANK_ACCOUNT_REQUIRED` |
| `/payments/{paymentId}` | `GET` | `paymentId`: UUID path | payment detail, current status, recipient bank info, QR URLs, signed proof URL, covered debts | active debtor, creditor, or Captain | `403 FORBIDDEN`, `404 PAYMENT_NOT_FOUND`, `422 BANK_ACCOUNT_REQUIRED` for pending proof only |
| `/payments/{paymentId}/proof` | `POST` | `Idempotency-Key` header, multipart `image`: file required, `note`: string optional | updated payment, status `pending_confirmation`, signed image URL, covered debts | active debtor on payment | `400 INVALID_IMAGE`, `403 FORBIDDEN`, `404 PAYMENT_NOT_FOUND`, `409 PAYMENT_NOT_PENDING_PROOF`, `422 BANK_ACCOUNT_REQUIRED`, `503 STORAGE_UNAVAILABLE` |
| `/payments/{paymentId}/confirm` | `POST` | `Idempotency-Key` header, `paymentId`: UUID path | updated payment, status `confirmed`, `confirmed_at`, settled debt IDs | active creditor on payment | `403 FORBIDDEN`, `404 PAYMENT_NOT_FOUND`, `409 PAYMENT_NOT_PENDING_CONFIRMATION` |
| `/payments/{paymentId}/reject` | `POST` | `Idempotency-Key` header, `paymentId`: UUID path, `reason`: string required | updated payment, status `rejected`, `rejected_at`, `rejection_reason`, reset debt IDs | active creditor on payment | `400 VALIDATION_FAILED`, `403 FORBIDDEN`, `404 PAYMENT_NOT_FOUND`, `409 PAYMENT_NOT_PENDING_CONFIRMATION` |

Every mutation above may also return `409 IDEMPOTENCY_KEY_REUSED` when a key is paired with a different canonical request, or `409 IDEMPOTENCY_IN_PROGRESS` plus `Retry-After` while its first execution is unfinished. A completed match replays its stored status code and response body, including an original `201` or `200`.

### HTTP contract and Public response schemas

All money values are represented as base 10 JSON strings in VND. Lists default to 20 items and accept up to 100. Cursors are opaque base64 encodings of `(created_at, id)`.

| Object | Exact public fields |
|---|---|
| `expense_summary` | `total_owed`: string, `total_settled`: string, `total_receivable`: string, `net_balance`: string |
| `expense_item` | `item_name`: string, `quantity`: string, `unit_price`: string, `line_total`: string, `item_discount_amount`: string, `item_final_price`: string, `share_ratio`: string, `item_share`: string |
| `bill_allocation_summary` | `item_subtotal`: string, `service_charge_share`: string, `vat_share`: string, `discount_share`: string, `rounding_adjustment`: string, `final_amount`: string |
| `expense_bill` | `bill_id`: UUID, `bill_date`: string, `merchant_name`: string, `creditor_member_id`: UUID, `creditor_display_name`: string, `debt_id`: UUID or null, `debt_status`: string or null, `allocation`: `bill_allocation_summary`, `items`: `[expense_item]` |
| `debt_item` | `id`: UUID, `bill_id`: UUID, `bill_date`: string, `merchant_name`: string, `debtor_member_id`: UUID, `debtor_display_name`: string, `debtor_avatar_url`: string or null, `creditor_member_id`: UUID, `creditor_display_name`: string, `creditor_avatar_url`: string or null, `amount`: string, `status`: string, `reminder_count`: int, `payment_id`: UUID or null, `created_at`: string, `settled_at`: string or null |
| `debt_matrix_entry` | `debtor_member_id`: UUID, `creditor_member_id`: UUID, `total_amount`: string, `debt_count`: int. One entry per unordered member pair with any unsettled debt between them, netted to the direction that currently owes; a pair that nets to zero is omitted. |
| `recipient_bank_info` | `bank_code`: string containing the VietQR bank BIN, `bank_name`: string, `account_number`: string, `account_holder`: string |
| `payment_detail` | `id`: UUID, `group_id`: UUID, `debtor_member_id`: UUID, `creditor_member_id`: UUID, `amount`: string, `reference_code`: string, `status`: string, `qr_payload`: string or null, `qr_image_url`: string or null, `recipient`: `recipient_bank_info` or null, `image_url`: string or null, `note`: string or null, `rejection_reason`: string or null, `covered_debt_ids`: UUID array, `created_at`: string, `submitted_at`: string or null, `confirmed_at`: string or null, `rejected_at`: string or null |

| Endpoint result | Exact response envelope |
|---|---|
| Get my expenses | `{ "summary": expense_summary, "bills": [expense_bill], "next_cursor": string or null }` |
| List group debts | `{ "debts": [debt_item], "net_matrix": [debt_matrix_entry], "caller_payable": string, "caller_receivable": string, "next_cursor": string or null }` |
| Generate QR | `{ "payment": payment_detail }` |
| Get payment detail | `{ "payment": payment_detail }` |
| Submit proof | `{ "payment": payment_detail }` |
| Confirm payment | `{ "payment": payment_detail, "settled_debts": [UUID] }` |
| Reject payment | `{ "payment": payment_detail, "reset_debts": [UUID] }` |
| Remind debt | `{ "debt_id": UUID, "reminder_count": int, "reminded_at": string }` |

### Value sourcing

| Action | Value produced or displayed | Source |
|---|---|---|
| Get my expenses | `total_owed`, `total_settled`, `total_receivable`, `net_balance` | Caller debtor sum for `awaiting` plus `pending_confirmation`; caller debtor sum for `settled`; caller creditor sum for `awaiting` plus `pending_confirmation`; and `v_member_balances.net_balance`, respectively |
| Get my expenses | Bill and item breakdown amounts and ratios | Joined `bill_item_assignments`, `bill_items`, `bill_shares`, `bills`, and `debts` rows, grouped into one response object per bill |
| Get my expenses | `item_discount_amount`, `item_final_price` | `bill_items.discount_amount`, `bill_items.final_price` directly (always present, `NOT NULL DEFAULT 0`; `0` and equal to `line_total` for an item with no promotion, set by either OCR folding, spec 0003's 0004, or a manual edit, spec 0003's 0005) |
| Get my expenses | `share_ratio` | `bill_item_assignments.weight` for this member on this item, divided by the sum of `weight` across every assignment on that same item |
| Get my expenses | `item_share` | `bill_items.final_price` (the net, post item discount price), not `line_total`, weighted by `share_ratio` above. Start from each exact item floor, then distribute the member local residual by item fractional remainder and item UUID ascending so nested rows sum to the stored `item_subtotal` |
| Get my expenses | `discount_share` | The member's proportional share of `bills.general_discount` only; an item's own targeted promotion is already netted into `item_final_price`/`item_share` above and never counted again here |
| Get my expenses | `service_charge_share`, `vat_share`, `discount_share`, `rounding_adjustment`, `final_amount` | The member's single `bill_shares` row, emitted once in the bill's `allocation`. Nested item rows never repeat bill level totals. `Σ item_share` across nested items equals `allocation.item_subtotal`. |
| List debts | Individual debts | `debts` rows filtered by query params, combined with `users` profiles via `group_members` |
| List debts | Caller payable, receivable, and net matrix | Whole group current unsettled debts with statuses `awaiting` or `pending_confirmation`; list filters never affect these aggregates |
| Generate QR | Payment ID, reference code | PostgreSQL UUID v7, and a reference code built from `PAY` plus 8 cryptographically random characters drawn from the uppercase alphanumeric set with `0`, `O`, `1`, `I` excluded (avoids misreads when a Creditor searches their bank statement) |
| Generate QR | Grouped payment amount | Sum of `debts.amount` for all targeted awaiting debts |
| Generate QR and get payment detail | `covered_debt_ids` and exact set replay | Immutable `payment_debts.debt_id` rows for the payment, compared as a complete set inside the group transaction |
| Generate QR | Recipient bank details | Active Creditor profile bank fields verified against the embedded VietQR directory |
| Generate QR | QR image URL | `https://img.vietqr.io/image/{BANK_ID}-{ACCOUNT_NO}-{TEMPLATE}.png?amount={AMOUNT}&addInfo={DESCRIPTION}&accountName={ACCOUNT_NAME}`. Path segments and query values are percent encoded; `BANK_ID` uses the embedded directory BIN, amount is positive integer VND, description is the reference code, and template defaults to `compact`. |
| Generate QR | EMVCo QR payload | Local NAPAS account transfer payload: tags `00=01`, `01=12`, `38` containing `00=A000000727`, nested `01` with `00={bank BIN}` and `01={account number}`, and `02=QRIBFTTA`; `53=704`; `54={integer amount}`; `58=VN`; `62` containing `08={reference code}`; and `63` as uppercase four hex digit CRC16 CCITT FALSE over the payload including the `6304` prefix. Tags are emitted in ascending order with two digit decimal byte lengths. |
| Submit proof | Bank snapshot on payment | Creditor bank fields copied onto `payments` row at submission time |
| Submit proof | Proof image object key and URL | Private per attempt key `payments/{paymentId}/proofs/{operationId}`, where `operationId` is stable for retries of one idempotency record and distinct across different keys. Only the winning key is stored on the payment; signed URLs have five minute expiry. |
| Confirm payment | Settled timestamp and activity | PostgreSQL `now()`, authenticated creditor member ID, and `payment_confirmed` activity |
| Reject payment | Rejection timestamp and reason | PostgreSQL `now()`, validated request text reason, and `payment_rejected` activity |
| Remind debt | Incremented count and notification | PostgreSQL `reminder_count + 1`, rate limit timestamp, and River notification insert |
| Stalled confirmation alert | Hours pending and notification | `now() - payments.submitted_at`, floored to whole hours, and River notification insert to the Creditor |
| Any notification insert | `notifications.type` literal | A fixed string per action, one per new `activity_type` value added by this feature (`payment_created`, `payment_submitted`, `payment_confirmed`, `payment_rejected`, `debt_reminded`, `payment_stalled_confirmation`), matching the existing convention of reusing the activity type name as the notification type |

### Key invariants

1. PaySplit never holds, routes, or pools user money. All funds transfer peer to peer directly between bank accounts.
2. Every payment mutation transaction acquires the `groups` lock, then acquires locks on all targeted `debts` rows in ascending UUID byte order, and then locks or creates the `payments` row. `payment_debts` identifies the target set but never replaces the required debt locks.
3. A payment in `pending_proof` displays the Creditor current valid bank account. If none exists, detail reads and proof submission return `422 BANK_ACCOUNT_REQUIRED` without deleting the payment. Upon proof submission, the recipient bank details become an immutable snapshot used by `pending_confirmation`, `confirmed`, and `rejected`. A superseded payment has no active QR or recipient response.
4. When a payment is rejected, every covered debt returns to `awaiting` with `payment_id = NULL`. The rejected payment record is preserved permanently with its rejection reason for auditability.
5. Settling a payment is strictly all or nothing. Every covered debt transitions to `settled` with `settled_at` in the same transaction as payment confirmation.
6. A bill cannot be voided if any of its debts have entered `pending_confirmation` or `settled` status.
7. Manual and automated reminders share `reminder_count` and `last_reminded_at`, permit no more than three total sends and no more than one per 24 hours, and update them through a conditional statement under the debt lock. Automated reminders stop once a debt leaves `awaiting`. A payment receives at most one stalled alert, proven by `stalled_alerted_at`.
8. Payment proof images are private assets. Mobile clients receive only five minute signed URLs.
9. `debt_status` retains two legacy values, `stalled_confirmation` and `rejected`, left over from the initial schema scaffold. This feature never assigns them to a `debts` row; a payment's own submitted or declined state lives on `payments.status`, not `debts.status`.
10. This feature depended on spec 0003 relaxing the existing `debts` check constraint `CHECK ((status = 'awaiting') = (payment_id IS NULL))` (`000001_init_schema.up.sql`) to also allow `voided` with a null `payment_id`; spec 0003 has shipped this (`debts_check1` now reads `CHECK ((status = ANY (ARRAY['awaiting'::debt_status, 'voided'::debt_status])) = (payment_id IS NULL))`, confirmed live 2026-08-21), so `v_member_balances` and the debt list here can rely on it.
11. Item level discounts (spec 0003's children 0004 and 0005) are already netted into `bill_items.final_price` before `bill_shares` is computed at finalize (`bill_item_assignments` are set earlier, at draft time). This feature's `expense_item.item_share` reads that net `final_price`, never the gross `line_total`; `expense_item.discount_share` reflects only the bill's `general_discount`, so a member never sees an item's own targeted promotion counted twice, once inside `item_share` and again inside `discount_share`.
12. `payment_debts` is the immutable audit set for every payment status. `debts.payment_id` is only the active settlement pointer and stays null for `awaiting` and `voided` debts, so `debts_check1` remains unchanged.
13. A `pending_proof` payment does not reserve its linked debts. Bill void may win the locks while they are still `awaiting`; when it does, every affected `pending_proof` payment becomes `superseded` in the same transaction. Proof submission must reject any linked set that is no longer entirely `awaiting`.
14. `pending_proof` and `superseded` have no submission timestamp, proof object, bank snapshot, confirmation, or rejection. `pending_confirmation` has submission, proof, and all four snapshot fields but no terminal timestamp. `confirmed` adds only `confirmed_at`. `rejected` adds only `rejected_at` and a non empty reason. `note` remains optional in all states.
15. Idempotency identity is `(actor_user_id, operation, key_hash)`. Canonical hashes include path identifiers, normalized bodies, and sorted debt IDs; proof requests additionally include SHA256 of raw image bytes and the note. Completed responses replay for 24 hours and expired rows are removed by River.
16. Proof upload identity is deterministic per idempotency attempt, not merely per payment: `payments/{paymentId}/proofs/{operationId}`. The operation ID is created with the idempotency record before upload, reused by its retries, and differs for another idempotency key even when image bytes are identical. The database stores only the winning key; each losing or failed attempt may delete or enqueue cleanup only for its own exact key, never the key committed by another request.

### Activity contract

| Action type | Actor | Required metadata |
|---|---|---|
| `payment_created` | Payer | `payment_id`, `creditor_member_id`, `amount`, `covered_debt_count` |
| `payment_submitted` | Payer | `payment_id`, `creditor_member_id`, `amount`, `has_note` |
| `payment_confirmed` | Creditor | `payment_id`, `debtor_member_id`, `amount`, `settled_debt_count` |
| `payment_rejected` | Creditor | `payment_id`, `debtor_member_id`, `amount`, `rejection_reason` |
| `debt_reminded` | Creditor or Captain with `actor_kind=member`, or River with `actor_kind=system` | `debt_id`, `debtor_member_id`, `amount`, `reminder_count` |
| `payment_stalled_confirmation` | River with `actor_kind=system` | `payment_id`, `creditor_member_id`, `debtor_member_id`, `hours_pending` |

### Security model

1. Every endpoint requires a valid bearer token and an active session.
2. The caller must be an active member of the target group. Inactive members or non members receive `404 GROUP_NOT_FOUND` on group scoped routes.
3. Only the debtor on a payment can submit transfer proof.
4. Only the specific creditor on a payment can confirm or reject receipt of money. A group Captain cannot confirm payments for another creditor.
5. All database operations enforce composite foreign keys (`group_id` paired with member and resource IDs) to prevent cross group access.
6. Access logs and audit trails redact proof image URLs, bank account numbers, reference codes, and member transfer notes.

### Configuration required

| Variable | Purpose |
|---|---|
| `VIETQR_SERVICE_BASE_URL` | Base URL for QR image generation, defaults to `https://img.vietqr.io/image` |
| `VIETQR_TEMPLATE` | VietQR display template, defaults to `compact` |
| `PAYMENT_PROOF_MAX_BYTES` | Maximum byte size for transfer proof uploads, defaults to 10485760 (10 MB) |
| `PAYMENT_PROOF_SIGNED_URL_TTL` | Lifetime for signed proof image URLs, defaults to 300 seconds (5 minutes) |
| `PAYMENT_REMINDER_STALE_HOURS` | Minimum age before automated reminder triggers for awaiting debts, defaults to 72 hours |
| `PAYMENT_REMINDER_MAX_COUNT` | Maximum combined manual and automated reminders per debt, defaults to 3 |
| `STALLED_CONFIRMATION_HOURS` | Age before alerting creditor of unconfirmed payment proof, defaults to 48 hours |

### Critical test scenarios

1. A Payer views personal expenses and verifies the four exact summary formulas, one allocation summary per bill, item only nested values, pagination by bill, and null debt fields for an allocated bill with no caller debt, verifies **AC-1**.
2. An active member filters the debt list and verifies that cursor ordering uses `(created_at DESC, id DESC)` while payable, receivable, and matrix remain whole group and exclude settled and voided, verifies **AC-2**.
3. A Payer generates a VietQR payment covering three awaiting debts, verifies exact composite links, replay and supersession, then tests omitted, empty, duplicate, over 100, wrong group, wrong pair, and non awaiting debt ID cases, verifies **AC-3** and **AC-4**.
4. Golden fixtures verify the exact NAPAS TLV bytes, nested tags, CRC16 CCITT FALSE, percent encoded `img.vietqr.io` URL, and known bank scannable payloads. Missing or later invalid bank profiles return `422`, while corrected pending proof and snapshotted later states behave as specified, verifies **AC-4** and **AC-5**.
5. Concurrent proof tests use distinct request hashes and verify per attempt object isolation, locked state recheck, one winning key, loser cleanup limited to its own key, successful snapshot, immediate compensation delete, and durable cleanup fallback, verifies **AC-6**.
6. A Creditor confirms payment, atomically settling all covered debts and logging activity, verifies **AC-7**.
7. A Creditor rejects payment with a reason, returning debts to `awaiting` with audit trail preserved, verifies **AC-8**.
8. Manual and automated reminder races verify a shared three send cap and 24 hour timestamp under lock, verifies **AC-9**.
9. Concurrent River workers conditionally claim one eligible reminder and exactly one stalled alert using system activity actors, verifies **AC-10**.
10. Concurrent proof submission and bill void execute under strict lock ordering without deadlock. If proof wins, void returns `PAYMENT_ALREADY_STARTED`; if void wins, the payment becomes `superseded` and proof returns a debt state conflict, verifies **AC-11**.
11. Inactive members are denied access and logs redact sensitive financial and image data, verifies **AC-12**.
12. Every mutation verifies completed replay, conflicting hash, in progress retry, proof byte hashing, and expiry cleanup, verifies **AC-11**.
13. Database constraint tests reject every invalid payment state combination and every cross pair `payment_debts` link.

## Build plan

The project uses Tracer Bullet. Each slice crosses database schema, SQLC queries, repository, usecase, HTTP handler, integration tests, and OpenAPI documentation before the next slice begins.

1. Build the expense and debt query slice. Add query indexes, redefine `v_member_balances` to exclude `voided` debts (requires spec 0003's `debt_status` migration to have landed first), SQLC queries for personal expense breakdown and group debt matrix, cursor pagination, usecase logic, HTTP endpoints `GET /expenses/me` and `GET /debts`, and real PostgreSQL coverage, satisfies **AC-1** and **AC-2**.
2. Build the VietQR payment generation slice. Extend `payments`, create `payment_debts` with member pair composite foreign keys and both indexes, keep `debts_check1` unchanged, and add the status matrix and actor kind constraints. Implement exact set validation and replay, supersession, reference codes, local golden tested TLV encoding, encoded VietQR image URLs, dynamic bank validation, the complete idempotency protocol, and `POST /payments/qr`, satisfies **AC-3**, **AC-4**, **AC-5**, and **AC-11**.
3. Build the transfer proof submission slice. Add multipart image upload handling, deterministic per attempt private Cloudinary object keys, signed URLs, attempt isolated compensation and durable `media_cleanup_jobs`, locked bank and debt rechecks, snapshot persistence, atomic transition, notification enqueueing, and `POST /payments/{paymentId}/proof`, satisfies **AC-6**, **AC-11**, and **AC-12**.
4. Build the creditor confirmation and rejection slice. Add creditor authorization, atomic all or nothing settlement transaction, rejection handling with debt reset, activity logging, notifications, and endpoints `POST /confirm` and `POST /reject`, satisfies **AC-7**, **AC-8**, and **AC-11**.
5. Build the debt reminder and background job slice. Add shared reminder count and timestamp claims, `POST /debts/{debtId}/remind`, system activity actors, one time stalled alert claims, idempotency expiry, media cleanup, and River workers, satisfies **AC-9** and **AC-10**.
6. Complete operational hardening and end to end verification. Add metrics, structured redaction, concurrency and lock contention tests against PostgreSQL, OpenAPI spec updates, and module documentation, satisfies **AC-1** through **AC-12**.

## Consequences

**Positive**:

1. PaySplit remains strictly a payment coordinator with zero fund custody, avoiding financial licensing hurdles.
2. Grouping debts into a single VietQR allows payers to clear multiple bills in a single bank transfer.
3. Ordered row locking and immutable payment debt links make the outcome of bill void and proof submission deterministic.
4. Dynamic bank profile lookup during unpaid state ensures payers always transfer to the latest active bank account, while proof submission freezes the account snapshot for historical audit.

**Negative and tradeoffs**:

1. Creditors must manually verify their actual bank accounts before confirming payments inside the app.
2. Partial payment confirmation is not supported in v1; a rejected payment resets all covered debts to awaiting status.
3. Private proof storage in Cloudinary requires generating time limited signed URLs on every read request.
4. `payment_debts` adds one table and one extra join to payment reads. Its rows are retained for audit, so storage grows with payment history.

**Neutral**:

1. `v_member_balances` calculates real time balances dynamically from unsettled debts rather than maintaining an expensive double entry ledger.
2. The `debts` table retains composite foreign keys ensuring debts never reference payments or members in another group.
3. `debts.payment_id` remains the active settlement pointer, while `payment_debts` records historical coverage across rejected and superseded attempts.

## Follow-up

- [ ] Add `supabase-postgres-best-practices` to the root or database area `AGENTS.md` context file.
- [ ] Align mobile application deep link handling for VietQR app opening when payer scans on a single device.
- [x] Reconcile the Split and Settlement feature row in `docs/scope/scope.md` once spec review is complete (already tracked as in-progress row 4).
- [x] This feature's migration must run strictly after spec 0003's `debt_status` migration that adds `voided`. Resolved: spec 0003 shipped (`000006_bill_and_ocr_v1.sql`), `debt_status` already has `voided`, confirmed live 2026-08-21. `v_member_balances`'s redefinition still lands in this feature's own later migration file.
- [x] Spec 0003 also needed to relax `debts`' existing check constraint `CHECK ((status = 'awaiting') = (payment_id IS NULL))` so a `voided` debt with a null `payment_id` is allowed. Resolved: `debts_check1` already reads the relaxed form, confirmed live 2026-08-21.
- [ ] When this feature is built, the personal expense breakdown (`GET /expenses/me`) must expose `item_discount_amount`/`item_final_price` per item and compute `item_share` from `final_price`, not `line_total`, per the Key invariants and Value sourcing updates added 2026-08-21 after spec 0003's item discount children (0004, 0005) shipped.
- [ ] `docs/change-req/api-change-request-01.md`'s Disband Group precondition (`SELECT count(*) FROM debts WHERE group_id = $1 AND status <> 'settled'` must be `0`) has the same bug `v_member_balances` had before this spec's fix: it counts a `voided` debt (spec 0003) as still blocking disbandment, when a voided debt carries no real obligation. That document is outside this spec's ownership (it's the Group module's change request, not `docs/specs/`), but flagging it here since this spec is where the `debts` lifecycle expertise lives: whoever builds Disband Group should change that query to `status NOT IN ('settled', 'voided')`, matching this spec's own `v_member_balances` definition.
- [ ] The `activity_type` renames (`submitted_proof` to `payment_submitted`, `confirmed_payment` to `payment_confirmed`, `rejected_payment` to `payment_rejected`, `stalled_payment_reminder` to `payment_stalled_confirmation`) touch enum values already defined in `000001_init_schema.up.sql`. Confirm no other in-flight branch has started writing rows under the old names before this migration merges.

## References

**Project sources**:
- `docs/Product_Requirement_Document.md`, sections 4.1.16 through 4.1.19, 4.2.1
- `docs/screen_flow.md`, Module 4 Split & Settlement
- `docs/specs/0002-group-management-v1/index.md`, group locking and membership rules
- `docs/specs/0003-bill-ocr-v1/index.md`, debt creation and voiding invariants
- `docs/specs/0003-bill-ocr-v1/0004-item-discount-ocr-parsing.md` and `0005-manual-edit-item-discount.md`, item level versus general discount separation this feature's expense breakdown must not double count

**Practices & standards**:
- Vietnam National Payment Standard VietQR (NAPAS EMVCo QR code specification)
- PostgreSQL ordered row locking for deadlock prevention in financial operations
- Idempotency keys pattern for safe financial state transitions
- Time limited signed URLs for private asset protection in cloud storage
