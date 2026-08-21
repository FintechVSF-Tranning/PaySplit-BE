# 0004. Split and settlement v1: rationale

## Context

Shared group activities like meals, trips, and shared living expenses frequently generate multiple debts between the same members across different bills. For example, member A may owe member B 50,000 VND for lunch, 120,000 VND for dinner, and 30,000 VND for drinks. Forcing member A to make three separate banking transactions is tedious, error prone, and clutters the banking history for both payer and creditor.

At the same time, Vietnam digital payment regulations (Decree 52/2024/ND-CP) impose strict licensing requirements on payment intermediaries that hold or pool user funds. PaySplit deliberately avoids holding funds or automating bank transfers. Money flows peer to peer directly between user bank accounts using the national VietQR standard.

This architectural decision introduces several key technical challenges:
1. PaySplit cannot automatically verify whether money was received in the creditor bank account. It must rely on a secure workflow where payers submit proof and creditors manually confirm or reject.
2. Generating a VietQR requires the creditor latest valid bank account. If an unpaid QR is generated and the creditor later changes their bank account, the unpaid QR must reflect the new bank account, while an already submitted payment must freeze the bank snapshot to keep audit history accurate.
3. Concurrent group actions, such as a Captain voiding an unpaid bill while a debtor is submitting payment proof for that bill debt, can cause data inconsistencies or deadlocks if lock ordering is not strictly enforced.
4. Without automated reminders and stalled payment alerts, pending debts and confirmations can stall indefinitely.
5. A QR must remember its exact selected debts before proof submission, but the shipped `debts_check1` requires every `awaiting` debt to keep `payment_id = NULL`. Without a separate relation, the system cannot replay an exact debt set, expose `covered_debt_ids`, or audit a superseded QR without changing the meaning of the debt lifecycle.

## Pending proof debt set decision

### Option A: Add immutable `payment_debts` links (Chosen)

Create one link row per selected debt when the QR is created. Keep `debts.status = 'awaiting'` and `debts.payment_id = NULL` until proof submission.

**Pros**:

1. Preserves the shipped debt constraint and the meaning of `awaiting`.
2. Supports exact set replay, superseded payment audit, and covered debt reads directly.
3. Lets bill void proceed before proof while giving it a precise set of pending payments to supersede.

**Cons**:

1. Adds a table, indexes, and a join to payment reads.
2. Requires both the immutable link and active `debts.payment_id` pointer to be kept conceptually distinct.

### Option B: Attach debts at QR creation and relax `debts_check1`

Set `debts.payment_id` while the debt remains `awaiting`, then allow awaiting debts to carry a payment pointer.

**Pros**:

1. Reuses the existing foreign key without a new relation.
2. Makes covered debt lookup a direct filter on `debts.payment_id`.

**Cons**:

1. Overloads `payment_id` with both a tentative QR selection and an active submitted settlement.
2. Forces bill void, reminders, QR expiry, supersession, and abandoned QR cleanup to distinguish two meanings of the same pointer.
3. Weakens a shipped database invariant that currently makes invalid debt state visible immediately.

### Option C: Store debt IDs as an array or JSON on `payments`

Store the selected UUID values in one denormalized column.

**Pros**:

1. Keeps payment reads simple.
2. Avoids a join table.

**Cons**:

1. Cannot enforce composite foreign keys or same group ownership for every element.
2. Makes reverse lookup from a debt to pending payments harder and less index friendly.
3. Duplicates relational data in a format that is easier to drift.

`payment_debts` is chosen because the selected set is a real many to many historical relation, not payment metadata. The added join is a small operational cost compared with weakening `debts_check1` or losing foreign key enforcement. The table uses composite group foreign keys and an index beginning with `debt_id`, which supports both cross group safety and the bill void lookup. The unique pending payment index plus the existing group lock serializes QR regeneration for one debtor and creditor pair.

A `pending_proof` link is intentionally not a reservation. If bill void locks an awaiting debt first, it voids the debt and supersedes linked pending payments in the same transaction. If proof submission locks first, it moves the full linked set to `pending_confirmation`, after which bill void rejects the operation. This preserves short transactions and produces a deterministic result without adding another state to debts.

## Cross check decisions

The independent architecture cross check found several contracts that needed to be made executable rather than left to implementation judgment. The following decisions are part of the chosen design.

**Reminder durability.** `debts.last_reminded_at` is the shared clock for manual and automated reminders, and `reminder_count` has a combined cap of three. `payments.stalled_alerted_at` makes a stalled confirmation alert exactly once. Conditional updates under row lock prevent duplicate workers.

**System activity actor.** River work cannot impersonate a member. `group_activities.actor_member_id` becomes nullable and a constrained `actor_kind` distinguishes `member` from `system`.

**Read model boundaries.** Personal expense totals have explicit status formulas. Bill level allocation amounts appear once per bill rather than being repeated on item rows. Debt list filters affect only the list; whole group unsettled totals and the net matrix remain independent of those filters.

**Idempotency.** A canonical request hash distinguishes safe replay from key reuse. Path identifiers, normalized bodies, and sorted debt IDs participate in the hash. Proof upload also hashes raw image bytes and the note. Completed status and response bodies replay for 24 hours, an unfinished request returns a retry response, and River removes expired records.

**Database enforcement.** The payment status matrix is a check constraint, not only usecase logic. `payment_debts` duplicates group, debtor, and creditor identifiers so composite foreign keys prove that both sides describe the same member pair. This duplication is accepted because it turns a critical financial invariant into a database guarantee.

**Proof upload compensation.** Cloudinary upload happens after a preliminary check and before the locked database transition because network I/O must not extend the transaction. The deterministic key `payments/{paymentId}/proofs/{operationId}` uses the operation ID created with the idempotency record, making retries converge while different idempotency attempts remain isolated even when their files are identical. Only the winning key is stored. A failed or losing attempt deletes only its own key, with `media_cleanup_jobs` as the durable fallback if deletion also fails, so it cannot overwrite or delete another attempt's committed proof.

**Bills without caller debt.** Personal expenses include every finalized bill with an allocation for the caller, including a bill where the caller is the Creditor and no self debt exists. The response represents this honestly with nullable `debt_id` and `debt_status` rather than inventing a synthetic debt state or silently omitting the allocation.

**Bank profile drift.** A missing or invalid bank profile blocks pending proof reads and submission with `BANK_ACCOUNT_REQUIRED` but does not destroy the selected debt set. Once proof is submitted, the immutable snapshot is authoritative.

**Debt selection edge cases.** Omission means all eligible debts to the Creditor. A supplied collection contains 1 through 100 unique UUIDs. Empty or duplicate input is validation failure; any selected debt that is no longer eligible produces one state conflict response.

**VietQR interoperability.** The backend owns a local NAPAS account transfer encoder using AID `A000000727`, service `QRIBFTTA`, VND currency `704`, purpose subtag `62.08`, and CRC16 CCITT FALSE. The image URL follows the `img.vietqr.io/image/{bank}-{account}-{template}.png` contract with encoded query values. Golden payload and URL fixtures prevent silent changes in tag nesting, lengths, checksum, or escaping.

## Schema baseline and drift found during cross check

The `payments` and `debts` tables, plus the `debt_status` and `activity_type` enums, already exist from the very first schema migration (`000001_init_schema.up.sql`), written before this spec, as scaffolding for a Module 4 that was not yet designed. A cross check against that live schema (2026-08-17) found three places where it drifts from this design, each resolved below and reflected in `index.md`.

**Payments table shape.** The live `payments` table has no `status` column (state was meant to be read off `submitted_at`/`confirmed_at`/`rejected_at`) and none of the four bank snapshot columns AC-6 needs. Deriving status from three nullable timestamps works but hides the state machine in application logic instead of the schema, and gives no place to store the bank snapshot at all. Decision: add an explicit `status payment_status` column and the four `recipient_bank_*` columns via `ALTER TABLE`, keeping the existing timestamp columns for their audit value. This is an additive migration; the table is not yet written to by any code, so there is no backfill risk.

**`debt_status` legacy values.** The live enum already carries `stalled_confirmation` and `rejected`, values from before this design that this feature's state machine (`awaiting`, `pending_confirmation`, `settled`, plus `voided` from spec 0003) does not use; a debt's stalled or rejected state belongs to its payment, not the debt itself. Postgres cannot drop an enum value without rebuilding the type, which is disproportionate for two unused values on an otherwise empty table. Decision: leave them in the enum, unused, and say so explicitly (`index.md`, Key invariants) so a future reader does not assume they are load bearing.

**`activity_type` naming collision.** The live enum already carries `submitted_proof`, `confirmed_payment`, `rejected_payment`, and `stalled_payment_reminder`, the same events this spec's Activity contract names differently (`payment_submitted`, `payment_confirmed`, `payment_rejected`, and the newly added `payment_stalled_confirmation`). Two options: add the new names alongside the old ones (leaving four unused legacy values permanently), or rename the existing values to match. Renaming was chosen because the table is unbuilt and unwritten, so there is no historical row whose meaning a rename would corrupt; adding parallel values would leave dead enum entries with no way to remove them later. `payment_created` and `debt_reminded` are genuinely new events with no prior value to rename.

**`v_member_balances` view.** The live view sums `status <> 'settled'`, so once spec 0003 lands the `voided` debt status, a voided debt would still count toward a member's net balance, silently disagreeing with this spec's own data model table (`status NOT IN ('settled', 'voided')`) and with 0002's member exit invariant. Decision: redefine the view in this feature's migration to exclude `voided` explicitly, sequenced after spec 0003's migration adds that enum value.

## Drift check against the shipped spec 0003 (2026-08-21)

Spec 0003 (`docs/specs/0003-bill-ocr-v1/`) shipped since this spec was written, including two child specs added mid build: `0004-item-discount-ocr-parsing.md` (OCR normalizer folds `KM` style promotion lines into `bill_items.discount_amount`/`final_price`) and `0005-manual-edit-item-discount.md` (the same fields round trip through manual bill edits). A cross check of the live database (2026-08-21) against this spec's assumptions found:

- **The `debt_status`/`debts` dependency this spec flagged as pending is now satisfied.** `debt_status` has `voided`, `debts.voided_at` exists, and `debts_check1` already allows `voided` with a null `payment_id`, exactly as this spec's Data model sketch and Key invariant 10 required. Nothing left to flag to whoever builds spec 0003, it is built. `index.md`'s dependency notes and the two matching Follow-up items were updated to reflect this rather than describe a still open dependency.
- **A real gap: this spec's personal expense breakdown never accounted for item level discounts.** `expense_item` (the `GET /expenses/me` response shape) had `line_total` but no way to show that an item's own promotion already reduced it, and its `item_share`/`discount_share` value sourcing never named `bill_items.final_price` as the base for the member's item share. Since the underlying allocator (spec 0003's `usecase/allocation.go`) already computes shares from `final_price`, not the gross `line_total`, this spec's design would either have under specified the exact number to display or an implementer would have quietly used the wrong base and double counted a member's own item discount inside `discount_share` as well. Decision: add `item_discount_amount`/`item_final_price` to `expense_item`, name `bill_items.final_price` as `item_share`'s source, and state explicitly that `discount_share` covers only `bills.general_discount`. This is additive to an unbuilt feature, no migration or already written code is affected.

Both are reflected in `index.md`: the Data model sketch dependency note, Key invariants 10 and 11, the `expense_item` schema, the Value sourcing table, and the Follow-up list.

## Options considered

### Option 1: Direct single bill debt settlement without aggregation

Each finalized debt row must be paid individually. A separate VietQR is generated for each bill debt, and each payment maps to exactly one `debts` record.

**Pros**:
- Simpler data model and queries because payments map 1 to 1 with debts.
- No need to manage debt grouping or partial debt selection across bills.

**Cons**:
- Poor user experience when multiple bills exist between the same members, requiring multiple bank transfers.
- Higher friction leads to delayed settlements and higher drop off rates.

### Option 2: Transactionally coordinated multi bill aggregation with dynamic VietQR (Chosen option)

A Payer can group multiple awaiting debts owed to the same Creditor into a single payment record with one unique reference code and one VietQR image. Unpaid payments dynamically display the Creditor latest bank account, while submitted payments freeze the bank account snapshot for historical audit. Confirmation and rejection operate atomically across all covered debts under strict row lock ordering.

**Pros**:
- Optimal user experience: clears all outstanding debts to a creditor in one single bank transfer.
- Preserves full auditability by linking the payment record to underlying bill debts.
- Enforces strict concurrency control with ordered locking (`groups` row first, then `debts` in ascending UUID order, then `payments`).
- Dynamic bank lookup prevents misdirected transfers if a creditor updates their account before payment.

**Cons**:
- Slightly more complex transaction logic and state reconciliation when a payment is rejected.
- All or nothing settlement means a dispute on one bill debt in a grouped payment resets all covered debts.

### Option 3: Off ledger peer to peer confirmation without proof storage

The app only calculates amounts and lets users mark debts as paid without uploading image proof or tracking reference codes.

**Pros**:
- Lowest implementation effort; no storage service integration needed.
- No storage costs for screenshot files.

**Cons**:
- High risk of disputes and confusion when a debtor claims payment but a creditor has no transfer proof or reference code to search their bank statement.
- Fails the core trust and coordination value proposition of PaySplit.

## Rationale

Option 2 was chosen because it directly solves the user friction of multiple bank transfers while preserving complete auditability and regulatory compliance.

Grouping debts into a single payment record with a unique `reference_code` (e.g. `PAY8K3M9X2Z`) allows the creditor to search their bank mobile app or bank statement instantly for the exact reference code and aggregated amount.

The two phase bank snapshot strategy balances accuracy with auditability:
- In `pending_proof` status (before money is sent), the app dynamically queries the Creditor active bank profile. If the Creditor updates their receiving account, the Payer immediately sees the updated account number and new QR code, preventing transfers to stale accounts.
- Once the Payer submits transfer proof (`pending_confirmation`), the system copies the Creditor bank code, account number, and holder name onto the `payments` record as an immutable snapshot. This ensures that even if the Creditor changes their bank account later, the dispute record reflects the exact account where money was claimed to be sent.

Strict lock ordering across transactions (`groups` row first, then `debts` in ascending UUID byte order, then `payments`) eliminates deadlocks between concurrent payment confirmations, rejections, and Module 3 bill voids.

## References

**Project sources**:
- `docs/Product_Requirement_Document.md`, sections 4.1.16, 4.1.17, 4.1.18, 4.1.19, 4.2.1
- `docs/screen_flow.md`, Module 4 Split and Settlement screen flows
- `docs/specs/0002-group-management-v1/index.md`, group coordination lock
- `docs/specs/0003-bill-ocr-v1/index.md`, debt creation and voiding invariants

**Practices & standards**:
- Vietnam National Payment Standard VietQR (NAPAS EMVCo QR code specification)
- PostgreSQL ordered row locking for deadlock prevention in financial operations
- Idempotency keys pattern for safe financial state transitions
- Time limited signed URLs for private asset protection in cloud storage
