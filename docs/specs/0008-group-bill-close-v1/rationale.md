# Rationale: 0008 Group bill close v1

## Context

V1 needs a group closing control without adding debtor consent. Today every active member can create a manual or image bill while the group remains active. Finalize exists only for one reviewed draft version at a time and already writes immutable shares and debts in one short transaction.

The submission policy must win safely against image upload and concurrent bill creation. Bulk finalize must not keep the group row or many bill rows locked while it processes unrelated bills. A failed draft must stay correct and explainable instead of forcing partial financial data into the ledger.

## Current system evidence

1. `database.LockActiveGroup` is already the shared coordination boundary for group scoped writes.
2. Bill create, review, edit, finalize, void, and delete already use exact status and version checks.
3. Individual finalize already requires the current Captain and creates immutable shares, debts, activity, and River notifications atomically. Creditor ownership and review do not grant finalize permission.
4. `idx_bills_group_status` already supports capture by group and bill status.
5. Image bill create already has attempt scoped storage cleanup for a database race after upload.
6. Group Hub has settings, a bill list, a create bill sheet, and per bill detail, but no group submission policy or bulk operation surface.

## Options considered

### Option 1: Durable asynchronous batch, chosen

Lock the group and snapshot bill versions in one short transaction. Store batch items and let River review and finalize each captured bill independently when its current data is valid.

**Pros**:

1. Keeps transactions short.
2. Survives app, API, and worker restarts.
3. Gives exact per bill progress and failure audit.
4. Reuses the current finalize transaction.

**Cons**:

1. Adds batch tables and worker handling.
2. Completion is asynchronous.

### Option 2: One synchronous transaction for every bill

Lock the group and all target bills, then finalize everything before returning.

**Pros**:

1. Gives one immediate all or nothing result.
2. Needs no batch status API.

**Cons**:

1. Lock time grows with bill, member, share, debt, activity, and notification counts.
2. One invalid bill rolls back useful work.
3. It increases deadlock and request timeout risk.

### Option 3: Flutter loops the existing finalize endpoint

The app first locks submissions, lists bills, then calls finalize once per bill.

**Pros**:

1. Small backend change.
2. Reuses the existing public endpoint directly.

**Cons**:

1. Closing progress depends on the phone staying online.
2. Retry can target a different bill set.
3. The server has no durable group level result or completion event.

## Rationale

Option 1 keeps PostgreSQL as the source of truth while respecting the existing lock order. The start transaction makes the lock and target snapshot one decision. Per bill workers keep the financial boundary unchanged and isolate stable validation failures.

The V1 lock is deliberately one way because the request only defines closing intake. Adding unlock would require separate rules for accidental reopen, new bill races, notifications, and audit. Existing drafts stay editable because the requested policy is about new submissions, and this gives the Captain a recovery path for bills that bulk finalize reports as not ready.

Only one batch may be active per group. A second Captain action resumes that batch instead of duplicating work, and group disband waits for terminal completion so archived groups never strand inaccessible jobs. Batch items retain a captured bill ID without a hard bill foreign key because the existing V1 contract hard deletes drafts. This preserves a redacted batch outcome without retaining deleted bill content or silently disabling draft deletion.

## References

**Project sources**:

1. `docs/specs/0002-group-management-v1/index.md`, group coordination and Captain authorization.
2. `docs/specs/0003-bill-ocr-v1/index.md`, bill create, review, finalize, immutable state, and cleanup.
3. `docs/specs/0006-notification-queue-v1/index.md`, River and notification delivery.
4. `internal/platform/database/group_lock.go`, shared active group lock.
5. `db/migrations/000001_init_schema.up.sql`, existing group and bill indexes.
6. `PaySplit-UI/index.html`, current Group Hub, create bill, and Bill Detail surfaces.

**Practices and standards**:

1. PostgreSQL short transactions.
2. Consistent group then bill lock ordering.
3. Durable asynchronous work for unbounded batch size.
4. Idempotency for financial mutations.
