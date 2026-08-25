# Review, uncommitted, 2026-08-25

**Reviewed by**: GPT 5.6 Sol (author on Ox Alpha)
**Scope**: 47 files, uncommitted
**Verdict**: Changes requested

## Summary

This change adds the group submission lock, bulk finalize storage and worker, batch APIs, group navigation, metrics, and broad tests. The core happy path is clear and the relevant Go packages pass. Before merge, you should close five production gaps around completion recovery, idempotency, transaction lock order, durable image cleanup, and the group list contract.

## Major

### 🟠 A final completion error can leave the batch active forever, `internal/modules/bill/usecase/bill_close.go:377`

**Problem**: Each item commits its terminal state before `completeBatchIfDone` runs. If `TryCompleteBatch` or completion notification enqueue fails, this function only logs the error and returns. The comment assumes another item worker will retry, but after the last item there is no pending item or job left. A repeated delivery also returns early for a terminal item at line 184 and never tries completion again.

**Why it matters**: One transient database or River error at the final step leaves the batch in `processing` forever even though every item is terminal. That permanently blocks another batch and group archive, and the Captain never receives the completion result.

**Suggested fix**: Give completion a durable retry path. You could enqueue a dedicated completion job, or make repeated item delivery attempt completion for terminal items and return a retryable error when completion fails. Add a test where the last item commits, the first completion attempt fails, and a later delivery completes the same batch exactly once.

### 🟠 Successful bulk start is not atomic with its idempotency result, `internal/modules/bill/delivery/http/close_handler.go:69`

**Problem**: `StartBulkFinalize` commits the lock, batch, items, activities, and River jobs before `CompleteIdempotency` runs in a separate transaction. The completion error at line 80 is ignored. A process crash or database error in this gap leaves the key in `in_progress` even though the batch exists.

**Why it matters**: Retrying the same key can return `IDEMPOTENCY_IN_PROGRESS` for up to 24 hours instead of replaying the original `202` and batch ID. This breaks AC 7 at exactly the failure boundary idempotency is meant to protect.

**Suggested fix**: Complete the reserved idempotency row in the same database transaction that creates the batch, or recover an existing in progress record from its committed resource and replay it. Cover the commit gap with a PostgreSQL test, not only the in memory handler mock.

### 🟠 The item transaction skips the required active group lock, `internal/modules/bill/usecase/bill_close.go:153`

**Problem**: The function says it locks group, batch, item, then bill, but it begins the transaction and immediately locks the batch at line 162. No active group row is ever locked. This contradicts spec 0008 invariant 11 and the existing group transaction contract. It also leaves the current Captain lookup unsynchronized with Captain transfer.

**Why it matters**: A transfer can commit while an item uses the former Captain for finalization activity and notification planning. More broadly, this creates a second lock order beside every existing group scoped mutation, which makes later consent and settlement work unsafe to compose.

**Suggested fix**: Move this transaction orchestration into the PostgreSQL adapter, acquire the active group row first, verify that the batch belongs to that group, then lock batch, item, and bill. Keep `pgx.Tx` out of the usecase boundary while making this change. Add real concurrency coverage for Captain transfer, edit, individual finalize, delete, and disband races.

### 🟠 Locked image attempts can escape durable cleanup, `internal/modules/bill/usecase/service.go:350`

**Problem**: When the transaction recheck detects a newly locked group, the code inserts cleanup rows for uploaded receipt objects but discards every insert error. The deferred direct Cloudinary delete is also best effort. If both operations fail, no durable cleanup record remains.

**Why it matters**: Private receipt images from a rejected request can remain stored indefinitely. This is a resource leak and a privacy failure, and it violates AC 2, which requires attempt objects to enter the durable cleanup flow.

**Suggested fix**: Treat durable cleanup insertion as required work and surface or retry failures until each uploaded key is either deleted or recorded. Add a test where the transaction loses the lock race, cleanup insertion fails, and direct deletion also fails.

### 🟠 Group list always reports locked groups as open, `internal/modules/group/repository/postgres/queries/groups.sql:15`

**Problem**: `ListActiveGroupsForUser` does not select `g.bill_submission_locked_at`, and the list mapper in `repository.go` never sets `BillSubmissionLockedAt`. `newGroupResponse` therefore emits `bill_submission_locked: false` for every list item, even after the group is locked.

**Why it matters**: The public `Group` contract now requires this server policy field. Home or list driven create controls can show a locked group as open, then fail only after the user submits a bill.

**Suggested fix**: Select and map the stored lock timestamp in the list query, regenerate sqlc output, and add a repository or HTTP test that lists a locked group and asserts both lock fields.

## Minor

### 🟡 Lock time comes from the application clock, `internal/modules/bill/repository/postgres/bill_close.go:89`

**Problem**: Both lock paths call `time.Now()` and pass the value into SQL. The value sourcing contract says the stored time comes from PostgreSQL `now()`.

**Why it matters**: Clock skew between application replicas can make audit order and client timestamps disagree with the database transaction that actually won the lock.

**Suggested fix**: Set `bill_submission_locked_at` with PostgreSQL `COALESCE(bill_submission_locked_at, now())` and return the stored value.

### 🟡 Deleted bill display name is omitted instead of returned as null, `internal/modules/bill/domain/batch.go:76`

**Problem**: `omitempty` removes `bill_display_name` when the bill was hard deleted. The API and value sourcing text say that field is returned as null. The current test indexes a map, so it cannot distinguish an absent key from a null value.

**Why it matters**: Strict generated clients can distinguish a missing optional field from a present nullable field, so the response does not match the documented contract.

**Suggested fix**: Remove `omitempty` from the response field and make the HTTP test assert that the key exists with a null value.

## Strengths

- The migration uses constrained batch and item state matrices, a partial unique index for one active batch, and no hard bill foreign key, which preserves the required hard delete behavior.
- The create path rechecks the submission lock while holding the group row, and the tests cover the basic lock versus create race and the main mixed batch result.

## Test coverage

`go test ./internal/modules/bill/... ./internal/modules/group/... ./internal/platform/metrics` passes. The full suite reaches unrelated tests that need local sockets and Cloudinary network access, which the review sandbox blocks. PostgreSQL integration tests require `TEST_DATABASE_URL`, and the checked in verification file still leaves the idempotency replay, Captain transfer, image cleanup failure, and several mutation races unchecked. Those gaps align with the findings above.

Scope: ticked `Review it`.
