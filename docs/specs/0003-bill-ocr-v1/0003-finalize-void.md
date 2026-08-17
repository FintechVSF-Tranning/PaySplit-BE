# 0003. Finalize and void

## Summary

This child spec covers the boundary where an editable bill becomes immutable financial history. Captain finalize writes exact member share snapshots, positive debts, activity, and notification jobs atomically. Safe void preserves that history and is allowed only before any payment starts.

## Requirements

This child owns **AC-9** through **AC-11**, finalized reads in **AC-12**, and finalize operations in **AC-14** from [the umbrella spec](index.md).

## Decision

Finalize is one synchronous PostgreSQL transaction guarded by the bill row, current version, Captain permission, and an idempotency key. It reruns the same pure allocation function used by preview and persists immutable per member components. Void is a terminal transition that marks unpaid debts voided rather than deleting them.

## Feature design

### Finalize preconditions

1. The actor is the active Captain for the path group.
2. The bill is draft, its version matches, and review points to that version.
3. Every assignee is an active member, each item ratio sums to one, and bill totals reconcile.
4. The Creditor still maps to an active member whose user profile has a complete valid bank account. Bank code must exist in the embedded VietQR directory. Account number and holder name follow spec 0001 constraints.
5. The request carries an idempotency key. A completed matching request replays its stored response.

### Finalize transaction

1. Lock the bill first, then relevant membership and assignment rows in canonical UUID byte order.
2. Rerun allocation with checked `int64` arithmetic. Reject overflow before any financial insert.
3. Insert one `bill_member_shares` row for every assigned member plus the Creditor. Keep zero rows for participant history.
4. Insert one `debts` row for each non Creditor share whose final amount is positive. Map group and bill from the bill, debtor from the share, creditor from the bill, amount from final amount, status to `awaiting`, and payment and settlement fields to null. Do not create self debt, zero debt, or a due date.
5. Set bill status and finalize fields. Insert exactly one `finalized_bill` activity and one River notification job per share snapshot, including the Creditor, in the same database transaction. Each job contains group ID, bill ID, member ID, final amount, Creditor ID, and activity ID.
6. Store the idempotent response and commit. A matching key replays it. A different key after finalization returns `409 BILL_IMMUTABLE` and does not add activity or notifications. No external notification or provider call happens under locks.

### Snapshot components

| Field | Meaning |
|---|---|
| `item_subtotal` | Sum of the member item allocations after item level Hamilton rounding |
| `service_charge_share` | Member service allocation, or all service charge for the Creditor when subtotal is zero |
| `vat_share` | Member VAT allocation, or all VAT for the Creditor when subtotal is zero |
| `discount_share` | Member discount allocation |
| `rounding_adjustment` | Sum of component Hamilton results minus each exact component truncated toward zero, with discount contribution subtracted |
| `final_amount` | Exact immutable amount attributed to the member |

The sum of `final_amount` is bill total. The sum of positive non Creditor final amounts is the sum of debts for the bill. These are different invariants because the Creditor can consume part of the bill.

### Void

1. Only the Captain may void a finalized bill. The request carries a trimmed reason from 1 to 500 characters and an idempotency key.
2. Lock the bill, then all bill debts in canonical UUID byte order. Module 4 payment start must use the same debt lock order before it changes status or `payment_id`.
3. Reject when any debt has a nonnull payment or a state other than `awaiting`.
4. Set the bill and its debts to `voided`, record actor, cleaned reason, and PostgreSQL time, insert one activity, store the idempotent response, and commit.
5. Images, items, assignments, OCR jobs, share snapshots, and debts stay readable and immutable.
6. A later draft may carry the voided bill ID in `replaces_bill_id`. A unique same group reference permits linear replacement chains, prevents branches, and a locked chain walk prevents cycles.
7. A matching idempotency key replays the void result. A different key after void returns `409 BILL_ALREADY_VOIDED` and writes nothing.

### Failure contract

| Failure | Result |
|---|---|
| Ordinary member finalizes or voids | `403 CAPTAIN_REQUIRED` |
| Draft changed after review | `409 VERSION_CONFLICT` or `409 REVIEW_REQUIRED` |
| Invalid ratios, totals, or assignee | `422 BILL_NOT_READY` with field level blockers |
| Creditor bank incomplete | `422 BANK_ACCOUNT_REQUIRED` |
| Concurrent finalize | one commit, later request replays when key matches or returns `409 BILL_IMMUTABLE` |
| Share or debt write fails | full rollback, bill remains draft |
| Notification delivery fails after commit | River retry, finalized bill remains valid |
| Void after payment starts | `409 PAYMENT_ALREADY_STARTED` |

## Build plan

1. Add share snapshot, bill state, debt state, replacement, activity, idempotency, and supporting index migration changes, satisfies **AC-9** through **AC-11**.
2. Build transactional finalize with ordered locks, pure allocation reuse, bank validation, idempotent response replay, activities, and River notification inserts, satisfies **AC-9**, **AC-10**, and **AC-14**.
3. Build immutable detail reads and verify snapshot to debt traceability, satisfies **AC-10** and **AC-12**.
4. Build void and replacement checks with payment boundary and concurrent request coverage, satisfies **AC-11**.
5. Complete failure injection, OpenAPI, metrics, redaction, and end to end tests, satisfies **AC-9** through **AC-14**.

## Rationale

Synchronous finalization is bounded by the prototype limits and gives the mobile client a definitive result. Storing final shares is justified because changing historical calculations is unacceptable. Marking debts voided retains auditability and prevents a correction flow from deleting financial evidence.
