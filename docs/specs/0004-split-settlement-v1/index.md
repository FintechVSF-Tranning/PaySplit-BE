# 0004. Split and settlement v1

**Date**: 2026-08-17
**Status**: Proposed

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

1. **AC-1**: An authenticated active member can view their personal allocated expenses in the group through `GET /api/v1/groups/{groupId}/expenses/me`. The response returns their aggregate financial summary (total owed, total settled, total receivable) and item level allocations across bills (item name, quantity, unit price, line total, the item's own discount amount and final price, always present and equal to `0`/`line_total` when no promotion applies, assigned share ratio, computed item share, proportional service charge, VAT, the member's share of the bill's general discount, rounding adjustment, creditor profile, and current debt status).
2. **AC-2**: An authenticated active member can view group debts through `GET /api/v1/groups/{groupId}/debts`. The endpoint supports cursor pagination, filtering by `debtor_id`, `creditor_id`, and `status` (`awaiting`, `pending_confirmation`, `settled`), and returns individual bill debt records, the caller total payable and receivable amounts, and the aggregated debt matrix across all group members.
3. **AC-3**: A Payer can generate a payment QR for a specific Creditor in the group through `POST /api/v1/groups/{groupId}/payments/qr`. By default, the system selects all `awaiting` debts owed to that Creditor in the group. If `debt_ids` is provided, the system validates that every specified debt belongs to the same group, is in `awaiting` status, and shares the same debtor and creditor.
4. **AC-4**: Payment QR generation returns a `201` response with a new payment record in `pending_proof` status, or returns `200` replaying an existing unsubmitted payment for the exact same debt set. The generated `reference_code` uses the prefix `PAY` followed by eight unguessable uppercase alphanumeric characters, unique across the entire system. The response returns both the EMVCo TLV payload string and a Sepay/vietqr.app image URL encoding the Creditor bank code, account number, account holder name, integer VND amount, and reference code.
5. **AC-5**: If the Creditor has no valid bank account configured in their profile, payment QR generation is rejected with `422 BANK_ACCOUNT_REQUIRED`. If the Creditor updates their bank profile while a payment remains in `pending_proof`, subsequent reads of that payment dynamically display the Creditor latest bank account and updated QR URL.
6. **AC-6**: A Payer can submit transfer proof through `POST /api/v1/groups/{groupId}/payments/{paymentId}/proof` with a JPEG, PNG, or HEIC image of at most 10 MB and an optional text note up to 500 characters. The transaction validates that the payment is in `pending_proof` status and that the caller is the debtor. It snapshots the Creditor current bank details onto the payment row, uploads the image as a private Cloudinary asset, transitions the payment and all covered debts to `pending_confirmation`, and enqueues a notification job for the Creditor.
7. **AC-7**: A Creditor can confirm a pending payment through `POST /api/v1/groups/{groupId}/payments/{paymentId}/confirm`. In one database transaction, the system verifies that the caller is the creditor on the payment, transitions the payment to `confirmed` with `confirmed_at = now()`, marks every covered debt as `settled` with `settled_at = now()`, writes a `payment_confirmed` group activity, and enqueues a settlement notification for the Payer. Settlement is all or nothing for the full payment amount.
8. **AC-8**: A Creditor can reject a pending payment through `POST /api/v1/groups/{groupId}/payments/{paymentId}/reject` by supplying a non empty reason between 1 and 500 characters. In one database transaction, the system verifies that the caller is the creditor on the payment, transitions the payment to `rejected` with `rejected_at = now()` and `rejection_reason`, returns all covered debts to `awaiting` status with `payment_id = NULL`, writes a `payment_rejected` group activity, and enqueues a rejection notification for the Payer.
9. **AC-9**: A Creditor or Captain can manually trigger a debt reminder for an individual `awaiting` debt through `POST /api/v1/groups/{groupId}/debts/{debtId}/remind`. The endpoint enforces a rate limit of at most one reminder per debt per 24 hours, increments `reminder_count`, logs an activity, and sends a notification to the debtor.
10. **AC-10**: A background River scheduled worker runs periodically to scan unsettled obligations. It enqueues automated reminder notifications for `awaiting` debts older than 72 hours (capped at three total reminders per debt), and enqueues warning alerts for Creditors when a payment remains in `pending_confirmation` for more than 48 hours without confirmation or rejection.
11. **AC-11**: All payment mutations (QR creation, proof upload, confirmation, rejection, and manual reminder) require an `Idempotency-Key` header and execute under strict lock ordering. The transaction locks the `groups` row, then locks all targeted `debts` rows in ascending UUID byte order, and then locks or inserts the `payments` row. If a concurrent bill void attempts to lock the same debts, void is rejected with `409 PAYMENT_ALREADY_STARTED` if any debt is in `pending_confirmation` or `settled`.
12. **AC-12**: Inactive members or users outside the group cannot access any debt, expense, or payment endpoints. Responses expose five minute signed Cloudinary URLs for payment proof images, and logs redact payment proof URLs, reference codes, bank account numbers, and member notes.

## Decision

**Chosen option**: Transactionally coordinated peer to peer settlement with dynamic VietQR generation, ordered row locking, and durable proof handling.

PaySplit coordinates payments directly between debtor and creditor bank accounts without holding funds. PostgreSQL enforces strong consistency and strict lock ordering for debt state transitions. Cloudinary stores private transfer proofs with short lived signed URLs, and River workers handle automated reminders and stalled payment alerts. (basis: project PRD requirements, PostgreSQL foreign key and locking conventions, and VietQR NAPAS standards)

**Implementation skills**: `supabase-postgres-best-practices` (`supabase/agent-skills`, `.agents/skills/supabase-postgres-best-practices/`)

## Feature design

### Data model sketch

| Entity | Required fields | Nullable fields | Relations and constraints |
|---|---|---|---|
| `debts` | `id uuid`, `group_id uuid`, `bill_id uuid`, `debtor_member_id uuid`, `creditor_member_id uuid`, `amount bigint`, `status debt_status`, `reminder_count int`, `created_at`, `updated_at` | `payment_id uuid`, `settled_at timestamptz`, `voided_at timestamptz` | Unique `(id, group_id)`. Unique `(bill_id, debtor_member_id, creditor_member_id)`. Foreign keys to `groups`, `bills`, `group_members`, and `payments`. `amount > 0`. Statuses: `awaiting`, `pending_confirmation`, `settled`, `voided`. |
| `payments` | `id uuid`, `group_id uuid`, `debtor_member_id uuid`, `creditor_member_id uuid`, `amount bigint`, `reference_code text`, `status payment_status`, `created_at`, `updated_at` | `qr_payload text`, `recipient_bank_code text`, `recipient_bank_name text`, `recipient_account_number text`, `recipient_account_holder text`, `image_object_key text`, `note text`, `rejection_reason text`, `submitted_at timestamptz`, `confirmed_at timestamptz`, `rejected_at timestamptz` | Unique `(id, group_id)`. Unique `reference_code`. Foreign keys to `groups` and `group_members`. Statuses: `pending_proof`, `pending_confirmation`, `confirmed`, `rejected`, `superseded` (an unsubmitted payment automatically retired when the Payer regenerates a QR for a different debt set, AC-4). Check constraints enforce status consistency with timestamps, the four bank snapshot columns arriving together, and rejection reason. |
| `group_activities` | `id uuid`, `group_id uuid`, `actor_member_id uuid`, `action_type activity_type`, `description text`, `metadata jsonb`, `created_at timestamptz` | none | Composite foreign keys to `groups` and `group_members`. Action types include `payment_created`, `payment_submitted`, `payment_confirmed`, `payment_rejected`, `debt_reminded`, `payment_stalled_confirmation`. `description` is a server generated Vietnamese sentence built from `action_type` plus the actor and target display names, matching the pattern already used by the `group` and `bill` modules. |
| `payment_idempotency_keys` | `actor_user_id uuid`, `operation text`, `key_hash text`, `canonical_request_hash text`, `operation_id uuid`, `state idempotency_state`, `expires_at timestamptz`, `created_at timestamptz` | `response_code int`, `response_body jsonb`, `retry_after timestamptz` | Unique `(actor_user_id, operation, key_hash)`. 24 hour time to live for payment operations. |
| `v_member_balances` | `group_id uuid`, `member_id uuid`, `net_balance bigint` | none | Derived PostgreSQL view aggregating unsettled debts where `status NOT IN ('settled', 'voided')`. |

The `payments` and `debts` tables already exist from the initial schema migration (`000001_init_schema.up.sql`), so this feature extends them rather than creating them fresh. `payments` currently has no `status` column (state is inferred from `submitted_at`/`confirmed_at`/`rejected_at`) and none of the four `recipient_bank_*` snapshot columns AC-6 needs. `debt_status` already carries two legacy values, `stalled_confirmation` and `rejected`, that predate this design; they stay in the enum unused (Postgres cannot cheaply drop an enum value) rather than being removed. `activity_type` already carries payment related values under different names (`submitted_proof`, `confirmed_payment`, `rejected_payment`, `stalled_payment_reminder`); this feature renames them to the names this spec's Activity contract uses, since no code has written a row under the old names yet. The schema change adds the new `payment_status` type and query indexes for debt filtering and payment lookup:

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
ALTER TABLE payments ADD CONSTRAINT chk_payments_status_confirmed_at
    CHECK ((status = 'confirmed') = (confirmed_at IS NOT NULL)) NOT VALID;
ALTER TABLE payments VALIDATE CONSTRAINT chk_payments_status_confirmed_at;
ALTER TABLE payments ADD CONSTRAINT chk_payments_status_rejected_at
    CHECK ((status = 'rejected') = (rejected_at IS NOT NULL)) NOT VALID;
ALTER TABLE payments VALIDATE CONSTRAINT chk_payments_status_rejected_at;

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

CREATE TYPE idempotency_state AS ENUM ('in_progress', 'completed');

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
    ON debts(group_id, debtor_member_id, status)
    INCLUDE (amount, creditor_member_id);

CREATE INDEX idx_debts_creditor_status
    ON debts(group_id, creditor_member_id, status)
    INCLUDE (amount, debtor_member_id);

CREATE INDEX idx_debts_reminders
    ON debts(status, created_at)
    WHERE status = 'awaiting' AND reminder_count < 3;

CREATE INDEX idx_payments_group_status
    ON payments(group_id, status, created_at DESC, id DESC);

CREATE INDEX idx_payments_stalled_confirmation
    ON payments(status, submitted_at)
    WHERE status = 'pending_confirmation';
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
```

### API surface

All routes require a valid bearer session and are mounted under `/api/v1/groups/{groupId}`.

| Endpoint | Method | Key inputs | Key outputs | Auth | Key errors |
|---|---|---|---|---|---|
| `/expenses/me` | `GET` | `cursor`: string optional, `limit`: int optional | financial summary, item allocations list, `next_cursor` | active group member | `400 INVALID_CURSOR`, `404 GROUP_NOT_FOUND` |
| `/debts` | `GET` | `debtor_id`: UUID optional, `creditor_id`: UUID optional, `status`: string optional, `cursor`: string optional, `limit`: int optional | debts list, summary totals, net debt matrix, `next_cursor` | active group member | `400 INVALID_CURSOR`, `404 GROUP_NOT_FOUND` |
| `/debts/{debtId}/remind` | `POST` | `debtId`: UUID path, `Idempotency-Key` header | reminder count, reminded timestamp | active Creditor or Captain | `403 FORBIDDEN`, `404 DEBT_NOT_FOUND`, `409 DEBT_NOT_AWAITING`, `429 REMINDER_RATE_LIMITED` |
| `/payments/qr` | `POST` | `Idempotency-Key` header, `creditor_member_id`: UUID required, `debt_ids`: UUID array optional | payment object, QR payload string, QR image URL, recipient bank info, covered debts list | active debtor member | `400 VALIDATION_FAILED`, `404 CREDITOR_NOT_FOUND`, `409 DEBTS_NOT_AWAITING`, `422 BANK_ACCOUNT_REQUIRED` |
| `/payments/{paymentId}` | `GET` | `paymentId`: UUID path | payment detail, current status, recipient bank info, QR URLs, signed proof URL, covered debts | active debtor, creditor, or Captain | `403 FORBIDDEN`, `404 PAYMENT_NOT_FOUND` |
| `/payments/{paymentId}/proof` | `POST` | `Idempotency-Key` header, multipart `image`: file required, `note`: string optional | updated payment, status `pending_confirmation`, signed image URL, covered debts | active debtor on payment | `400 INVALID_IMAGE`, `403 FORBIDDEN`, `404 PAYMENT_NOT_FOUND`, `409 PAYMENT_NOT_PENDING_PROOF`, `503 STORAGE_UNAVAILABLE` |
| `/payments/{paymentId}/confirm` | `POST` | `Idempotency-Key` header, `paymentId`: UUID path | updated payment, status `confirmed`, `confirmed_at`, settled debt IDs | active creditor on payment | `403 FORBIDDEN`, `404 PAYMENT_NOT_FOUND`, `409 PAYMENT_NOT_PENDING_CONFIRMATION` |
| `/payments/{paymentId}/reject` | `POST` | `Idempotency-Key` header, `paymentId`: UUID path, `reason`: string required | updated payment, status `rejected`, `rejected_at`, `rejection_reason`, reset debt IDs | active creditor on payment | `400 VALIDATION_FAILED`, `403 FORBIDDEN`, `404 PAYMENT_NOT_FOUND`, `409 PAYMENT_NOT_PENDING_CONFIRMATION` |

### HTTP contract and Public response schemas

All money values are represented as base 10 JSON strings in VND. Lists default to 20 items and accept up to 100. Cursors are opaque base64 encodings of `(created_at, id)`.

| Object | Exact public fields |
|---|---|
| `expense_summary` | `total_owed`: string, `total_settled`: string, `total_receivable`: string, `net_balance`: string |
| `expense_item` | `bill_id`: UUID, `bill_date`: string, `merchant_name`: string, `item_name`: string, `quantity`: string, `unit_price`: string, `line_total`: string, `item_discount_amount`: string, `item_final_price`: string, `share_ratio`: string, `item_share`: string, `service_charge_share`: string, `vat_share`: string, `discount_share`: string, `rounding_adjustment`: string, `final_amount`: string, `creditor_member_id`: UUID, `creditor_display_name`: string, `debt_status`: string |
| `debt_item` | `id`: UUID, `bill_id`: UUID, `bill_date`: string, `merchant_name`: string, `debtor_member_id`: UUID, `debtor_display_name`: string, `debtor_avatar_url`: string or null, `creditor_member_id`: UUID, `creditor_display_name`: string, `creditor_avatar_url`: string or null, `amount`: string, `status`: string, `reminder_count`: int, `payment_id`: UUID or null, `created_at`: string, `settled_at`: string or null |
| `debt_matrix_entry` | `debtor_member_id`: UUID, `creditor_member_id`: UUID, `total_amount`: string, `debt_count`: int. One entry per unordered member pair with any unsettled debt between them, netted to the direction that currently owes; a pair that nets to zero is omitted. |
| `recipient_bank_info` | `bank_code`: string, `bank_name`: string, `account_number`: string, `account_holder`: string |
| `payment_detail` | `id`: UUID, `group_id`: UUID, `debtor_member_id`: UUID, `creditor_member_id`: UUID, `amount`: string, `reference_code`: string, `status`: string, `qr_payload`: string, `qr_image_url`: string, `recipient`: `recipient_bank_info`, `image_url`: string or null, `note`: string or null, `rejection_reason`: string or null, `covered_debt_ids`: UUID array, `created_at`: string, `submitted_at`: string or null, `confirmed_at`: string or null, `rejected_at`: string or null |

| Endpoint result | Exact response envelope |
|---|---|
| Get my expenses | `{ "summary": expense_summary, "items": [expense_item], "next_cursor": string or null }` |
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
| Get my expenses | Breakdown amounts, ratios, rounding | Joined `bill_item_assignments`, `bill_items`, `bill_shares`, `bills`, and `debts` rows |
| Get my expenses | `item_discount_amount`, `item_final_price` | `bill_items.discount_amount`, `bill_items.final_price` directly (always present, `NOT NULL DEFAULT 0`; `0` and equal to `line_total` for an item with no promotion, set by either OCR folding, spec 0003's 0004, or a manual edit, spec 0003's 0005) |
| Get my expenses | `share_ratio` | `bill_item_assignments.weight` for this member on this item, divided by the sum of `weight` across every assignment on that same item |
| Get my expenses | `item_share` | `bill_items.final_price` (the net, post item discount price), not `line_total`, weighted by `share_ratio` above, floor rounded the same way `CalculateFloorAllocation` computes each item's per member split |
| Get my expenses | `discount_share` | The member's proportional share of `bills.general_discount` only; an item's own targeted promotion is already netted into `item_final_price`/`item_share` above and never counted again here |
| Get my expenses | `service_charge_share`, `vat_share`, `discount_share`, `rounding_adjustment`, `final_amount` | These are computed once per member per bill, not per item: they are that member's single `bill_shares` row for this bill, repeated on every `expense_item` row belonging to that bill. `Σ item_share` across a bill's item rows equals that same `bill_shares.item_subtotal` exactly, by construction. |
| List debts | Individual debts and aggregate matrix | `debts` rows filtered by query params, combined with `users` profiles via `group_members` |
| Generate QR | Payment ID, reference code | PostgreSQL UUID v7, and a reference code built from `PAY` plus 8 cryptographically random characters drawn from the uppercase alphanumeric set with `0`, `O`, `1`, `I` excluded (avoids misreads when a Creditor searches their bank statement) |
| Generate QR | Grouped payment amount | Sum of `debts.amount` for all targeted awaiting debts |
| Generate QR | Recipient bank details | Active Creditor profile bank fields verified against the embedded VietQR directory |
| Generate QR | QR image URL | `https://vietqr.app/img` format configured with Creditor bank code, account number, amount, reference code, holder name, and compact template |
| Generate QR | EMVCo QR payload | Locally generated TLV EMVCo standard payload with CRC16-CCITT checksum |
| Submit proof | Bank snapshot on payment | Creditor bank fields copied onto `payments` row at submission time |
| Submit proof | Proof image object key and URL | Cloudinary private upload response key, signed at response time with five minute expiry |
| Confirm payment | Settled timestamp and activity | PostgreSQL `now()`, authenticated creditor member ID, and `payment_confirmed` activity |
| Reject payment | Rejection timestamp and reason | PostgreSQL `now()`, validated request text reason, and `payment_rejected` activity |
| Remind debt | Incremented count and notification | PostgreSQL `reminder_count + 1`, rate limit timestamp, and River notification insert |
| Stalled confirmation alert | Hours pending and notification | `now() - payments.submitted_at`, floored to whole hours, and River notification insert to the Creditor |
| Any notification insert | `notifications.type` literal | A fixed string per action, one per new `activity_type` value added by this feature (`payment_created`, `payment_submitted`, `payment_confirmed`, `payment_rejected`, `debt_reminded`, `payment_stalled_confirmation`), matching the existing convention of reusing the activity type name as the notification type |

### Key invariants

1. PaySplit never holds, routes, or pools user money. All funds transfer peer to peer directly between bank accounts.
2. Every mutation transaction acquires the `groups` lock, then acquires locks on all targeted `debts` rows in ascending UUID byte order, and then locks or creates the `payments` row.
3. A payment in `pending_proof` always displays the Creditor current active bank account. Upon proof submission (`pending_confirmation`), the recipient bank details become an immutable snapshot on the payment row.
4. When a payment is rejected, every covered debt returns to `awaiting` with `payment_id = NULL`. The rejected payment record is preserved permanently with its rejection reason for auditability.
5. Settling a payment is strictly all or nothing. Every covered debt transitions to `settled` with `settled_at` in the same transaction as payment confirmation.
6. A bill cannot be voided if any of its debts have entered `pending_confirmation` or `settled` status.
7. Manual reminders are throttled to at most once per 24 hours per debt. Automated reminders stop immediately once a debt enters `pending_confirmation`.
8. Payment proof images are private assets. Mobile clients receive only five minute signed URLs.
9. `debt_status` retains two legacy values, `stalled_confirmation` and `rejected`, left over from the initial schema scaffold. This feature never assigns them to a `debts` row; a payment's own submitted or declined state lives on `payments.status`, not `debts.status`.
10. This feature depended on spec 0003 relaxing the existing `debts` check constraint `CHECK ((status = 'awaiting') = (payment_id IS NULL))` (`000001_init_schema.up.sql`) to also allow `voided` with a null `payment_id`; spec 0003 has shipped this (`debts_check1` now reads `CHECK ((status = ANY (ARRAY['awaiting'::debt_status, 'voided'::debt_status])) = (payment_id IS NULL))`, confirmed live 2026-08-21), so `v_member_balances` and the debt list here can rely on it.
11. Item level discounts (spec 0003's children 0004 and 0005) are already netted into `bill_items.final_price` before `bill_shares` is computed at finalize (`bill_item_assignments` are set earlier, at draft time). This feature's `expense_item.item_share` reads that net `final_price`, never the gross `line_total`; `expense_item.discount_share` reflects only the bill's `general_discount`, so a member never sees an item's own targeted promotion counted twice, once inside `item_share` and again inside `discount_share`.

### Activity contract

| Action type | Actor | Required metadata |
|---|---|---|
| `payment_created` | Payer | `payment_id`, `creditor_member_id`, `amount`, `covered_debt_count` |
| `payment_submitted` | Payer | `payment_id`, `creditor_member_id`, `amount`, `has_note` |
| `payment_confirmed` | Creditor | `payment_id`, `debtor_member_id`, `amount`, `settled_debt_count` |
| `payment_rejected` | Creditor | `payment_id`, `debtor_member_id`, `amount`, `rejection_reason` |
| `debt_reminded` | Creditor or Captain (manual), or the River scheduled worker (automated) | `debt_id`, `debtor_member_id`, `amount`, `reminder_count` |
| `payment_stalled_confirmation` | River scheduled worker | `payment_id`, `creditor_member_id`, `debtor_member_id`, `hours_pending` |

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
| `VIETQR_SERVICE_BASE_URL` | Base URL for QR image generation, defaults to `https://vietqr.app/img` |
| `VIETQR_TEMPLATE` | VietQR display template, defaults to `compact` |
| `PAYMENT_PROOF_MAX_BYTES` | Maximum byte size for transfer proof uploads, defaults to 10485760 (10 MB) |
| `PAYMENT_PROOF_SIGNED_URL_TTL` | Lifetime for signed proof image URLs, defaults to 300 seconds (5 minutes) |
| `PAYMENT_REMINDER_STALE_HOURS` | Minimum age before automated reminder triggers for awaiting debts, defaults to 72 hours |
| `PAYMENT_REMINDER_MAX_COUNT` | Maximum automated reminders per debt, defaults to 3 |
| `STALLED_CONFIRMATION_HOURS` | Age before alerting creditor of unconfirmed payment proof, defaults to 48 hours |

### Critical test scenarios

1. A Payer views their personal allocated expenses and verifies exact item shares, taxes, and rounding adjustments across multiple bills, verifies **AC-1**.
2. An active member lists group debts with cursor pagination and verifies the aggregated net balance matrix, verifies **AC-2**.
3. A Payer generates a VietQR payment covering three awaiting debts to one Creditor, verifies amount summation, reference code format, and QR URL construction, verifies **AC-3** and **AC-4**.
4. Generating QR for a Creditor without a bank account fails with `422 BANK_ACCOUNT_REQUIRED`, and updating the profile while unpaid updates the displayed QR, verifies **AC-5**.
5. A Payer uploads a valid proof image, snapshotting bank details and moving debts to `pending_confirmation`, verifies **AC-6**.
6. A Creditor confirms payment, atomically settling all covered debts and logging activity, verifies **AC-7**.
7. A Creditor rejects payment with a reason, returning debts to `awaiting` with audit trail preserved, verifies **AC-8**.
8. A Creditor triggers manual debt reminder, verifying the 24 hour rate limit, verifies **AC-9**.
9. River scheduled workers process automated reminders for 3 day old debts and alert creditors for 48 hour stalled payments, verifies **AC-10**.
10. Concurrent payment submission and bill void execute under strict lock ordering without deadlock or race conditions, verifies **AC-11**.
11. Inactive members are denied access and logs redact sensitive financial and image data, verifies **AC-12**.

## Build plan

The project uses Tracer Bullet. Each slice crosses database schema, SQLC queries, repository, usecase, HTTP handler, integration tests, and OpenAPI documentation before the next slice begins.

1. Build the expense and debt query slice. Add query indexes, redefine `v_member_balances` to exclude `voided` debts (requires spec 0003's `debt_status` migration to have landed first), SQLC queries for personal expense breakdown and group debt matrix, cursor pagination, usecase logic, HTTP endpoints `GET /expenses/me` and `GET /debts`, and real PostgreSQL coverage, satisfies **AC-1** and **AC-2**.
2. Build the VietQR payment generation slice. Extend the existing `payments` table (`000001_init_schema.up.sql`) with the `status` column, the four `recipient_bank_*` snapshot columns, and the renamed/added `activity_type` values, plus cryptographic reference code generator, VietQR URL builder and EMVCo payload generator, bank account validation, dynamic profile lookup, idempotency keys, and `POST /payments/qr` endpoint, satisfies **AC-3**, **AC-4**, **AC-5**, and **AC-11**.
3. Build the transfer proof submission slice. Add multipart image upload handling, Cloudinary private asset adapter with signed URL generation, bank snapshot persistence, atomic state transition to `pending_confirmation`, notification enqueueing, and `POST /payments/{paymentId}/proof`, satisfies **AC-6**, **AC-11**, and **AC-12**.
4. Build the creditor confirmation and rejection slice. Add creditor authorization, atomic all or nothing settlement transaction, rejection handling with debt reset, activity logging, notifications, and endpoints `POST /confirm` and `POST /reject`, satisfies **AC-7**, **AC-8**, and **AC-11**.
5. Build the debt reminder and background job slice. Add manual reminder rate limiting, `POST /debts/{debtId}/remind`, and River workers for 72h automated reminders (writing `debt_reminded`) and 48h stalled confirmation alerts (writing `payment_stalled_confirmation`), satisfies **AC-9** and **AC-10**.
6. Complete operational hardening and end to end verification. Add metrics, structured redaction, concurrency and lock contention tests against PostgreSQL, OpenAPI spec updates, and module documentation, satisfies **AC-1** through **AC-12**.

## Consequences

**Positive**:

1. PaySplit remains strictly a payment coordinator with zero fund custody, avoiding financial licensing hurdles.
2. Grouping debts into a single VietQR allows payers to clear multiple bills in a single bank transfer.
3. Ordered row locking prevents deadlocks and eliminates race conditions between bill voiding and debt payments.
4. Dynamic bank profile lookup during unpaid state ensures payers always transfer to the latest active bank account, while proof submission freezes the account snapshot for historical audit.

**Negative and tradeoffs**:

1. Creditors must manually verify their actual bank accounts before confirming payments inside the app.
2. Partial payment confirmation is not supported in v1; a rejected payment resets all covered debts to awaiting status.
3. Private proof storage in Cloudinary requires generating time limited signed URLs on every read request.

**Neutral**:

1. `v_member_balances` calculates real time balances dynamically from unsettled debts rather than maintaining an expensive double entry ledger.
2. The `debts` table retains composite foreign keys ensuring debts never reference payments or members in another group.

## Follow-up

- [ ] Add `supabase-postgres-best-practices` to the root or database area `AGENTS.md` context file.
- [ ] Align mobile application deep link handling for VietQR app opening when payer scans on a single device.
- [x] Reconcile the Split and Settlement feature row in `docs/scope/scope.md` once spec review is complete (already tracked as in-progress row 4).
- [x] This feature's migration must run strictly after spec 0003's `debt_status` migration that adds `voided`. Resolved: spec 0003 shipped (`000006_bill_and_ocr_v1.sql`), `debt_status` already has `voided`, confirmed live 2026-08-21. `v_member_balances`'s redefinition still lands in this feature's own later migration file.
- [x] Spec 0003 also needed to relax `debts`' existing check constraint `CHECK ((status = 'awaiting') = (payment_id IS NULL))` so a `voided` debt with a null `payment_id` is allowed. Resolved: `debts_check1` already reads the relaxed form, confirmed live 2026-08-21.
- [ ] When this feature is built, the personal expense breakdown (`GET /expenses/me`) must expose `item_discount_amount`/`item_final_price` per item and compute `item_share` from `final_price`, not `line_total`, per the Key invariants and Value sourcing updates added 2026-08-21 after spec 0003's item discount children (0004, 0005) shipped.
- [ ] `docs/api-change-request.md`'s Disband Group precondition (`SELECT count(*) FROM debts WHERE group_id = $1 AND status <> 'settled'` must be `0`) has the same bug `v_member_balances` had before this spec's fix: it counts a `voided` debt (spec 0003) as still blocking disbandment, when a voided debt carries no real obligation. That document is outside this spec's ownership (it's the Group module's change request, not `docs/specs/`), but flagging it here since this spec is where the `debts` lifecycle expertise lives: whoever builds Disband Group should change that query to `status NOT IN ('settled', 'voided')`, matching this spec's own `v_member_balances` definition.
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
