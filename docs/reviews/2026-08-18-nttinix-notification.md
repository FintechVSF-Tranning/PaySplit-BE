# Review, nttinix/notification, 2026-08-18

**Reviewed by**: claude-opus-5 (author on Codex)
**Scope**: 33 files, branch vs main (feature-scoped: `internal/modules/notification`, `internal/platform/notification/fcm`, `internal/platform/queue/river`, migration 000003, bootstrap, openapi, auth jobs)
**Verdict**: Blocked

## Summary

The change adds a River-on-Postgres background queue, an FCM push client with Vietnamese message builders, an in-app notification module (repository/usecase/handlers), and wires both into `bootstrap.App` with a graceful-shutdown ordering that matches `docs/notification-module.md`. Layering is clean and idiomatic for this repo, the worker's happy/dead-token/transient-error paths are unit tested, and `go build`, `go vet` and `go test` all pass.

The headline problems are: FCM `INVALID_ARGUMENT` responses are misclassified as dead tokens and silently delete a valid user's push token; the notification row and the River job are **not** inserted in one transaction and the job carries the whole message instead of `NotificationID`, both of which AC-3 states explicitly; migration 000003 adds CHECK constraints over backfilled empty columns with no UPDATE, so `migrate-up` aborts on any database that already has `notifications` rows; and River is started with a hardcoded 20 workers against a pool whose default `DB_MAX_CONNS` is 10.

## Blockers

### 🔴 `INVALID_ARGUMENT` from FCM deletes a valid device token, `internal/platform/notification/fcm/client.go:82`

**Problem**: `SendToDevice` maps both `messaging.IsRegistrationTokenNotRegistered(err)` **and** `messaging.IsInvalidArgument(err)` to `ErrInvalidToken`, and `IsInvalidTokenError` (client.go:116) repeats the same conflation. The worker (`internal/modules/notification/jobs/send_notification.go:63`) then calls `ClearFCMToken` and completes the job. FCM returns `INVALID_ARGUMENT` for malformed *messages*, not only malformed tokens: reserved data keys (`from`, `gcm.*`, `google.*`), an oversized data payload (>4KB), or an invalid APNs/Android config all produce it. Spec AC-4 names only `messaging.IsRegistrationTokenNotRegistered`.

**Why it matters**: A bug in any future message builder (a long `rejection_reason`, a new data key) makes every affected send return `INVALID_ARGUMENT`, and the worker responds by wiping `sessions.fcm_token` for those users. The token is unrecoverable server-side — push stays dead for that user until they sign out and back in — and the job completes successfully, so nothing surfaces in logs or metrics. This is silent data loss triggered by a server-side mistake, not by the client.

**Suggested fix**: Treat only `IsRegistrationTokenNotRegistered` (and optionally `IsSenderIDMismatch`) as a dead token. Map `IsInvalidArgument` to a distinct non-retryable error that is logged loudly and completes the job **without** touching the token, so a payload bug is visible instead of destructive. Add a unit test asserting an `INVALID_ARGUMENT`-shaped error does not reach `ClearFCMToken`.

## Major

### 🟠 Notification row and River job are not enqueued transactionally, `internal/modules/notification/usecase/service.go:72`

**Problem**: `SendToUser` does `repo.CreateNotification` on the pool (line 72), then `enqueuer.EnqueueNotification` (line 78), which calls `client.Insert` — also on the pool — with no shared transaction. AC-3 requires both writes "in the same database transaction", and the spec's key invariants call this out as the mechanism "preventing lost or orphan notifications". River's `riverpgxv5` driver supports `InsertTx(ctx, tx, ...)` precisely for this; the client is even parameterised as `*river.Client[pgx.Tx]`, so the transactional path was designed for and then not used.

**Why it matters**: Three concrete failures. (1) Crash or connection loss between line 72 and line 78 leaves an in-app record that never pushes. (2) `EnqueueNotification` failing returns an error to the caller after the row is already committed; the caller's natural retry writes a second `notifications` row, so the user sees the same item twice. (3) The inverse is worse once bills/payments call this: `SendToUser` is not enlisted in the caller's business transaction, so if the bill-finalize transaction rolls back after `SendToUser` returned, the notification row and the push both survive and the user is told about an event that never happened — exactly the phantom state the spec's invariant forbids.

**Suggested fix**: Give the repository and the enqueuer transaction-aware variants (`CreateNotificationTx` / `EnqueueNotificationTx(ctx, tx, ...)` using `river.Client.InsertTx`), and have `SendToUser` either open its own transaction or accept a caller-supplied `pgx.Tx` so business modules can enlist it. Keep the non-transactional path only for callers with no surrounding write.

### 🟠 Job args carry the whole `PushMessage` instead of `NotificationID`, `internal/modules/notification/jobs/send_notification.go:25`

**Problem**: `NotificationJobArgs` is `{UserID, Message}`. AC-3 is explicit: "The job payload carries `NotificationID uuid` only; the worker reads title, body, and payload from the stored notification record."

**Why it matters**: Beyond the spec deviation, this has real consequences. The title/body/payload are duplicated into `river_job.args`, so an already-persisted notification is stored twice and the two copies can drift. More importantly the job has no reference back to the notification row, which removes any handle for idempotency or delivery bookkeeping: River is at-least-once, so a crash after `client.Send` succeeds but before the job is marked complete replays the job and the user gets a duplicate push, with no notification ID to dedupe on and no place to record "already delivered". It also blocks the obvious future features (delivery status, retry-with-updated-content).

**Suggested fix**: Reduce the args to `{NotificationID string}`, have the worker load the notification (which also yields `user_id`), and use the notification ID as a natural dedupe/`collapse_key` handle. If the job's notification row is gone (user deleted), complete without error.

### 🟠 Migration 000003 adds CHECK constraints over backfilled empty columns without backfilling, `db/migrations/000003_add_fcm_and_notifications.sql:10`

**Problem**: Lines 10-12 add `title`/`body` as `NOT NULL DEFAULT ''`, line 15-17 drop the default, then lines 20-23 add `CHECK (char_length(btrim(title)) BETWEEN 1 AND 255)` and the equivalent for `body`. Any pre-existing row in `notifications` has `title = ''` and `body = ''` and fails validation, so `ADD CONSTRAINT` errors and the whole migration rolls back. The inline comment ("Bỏ default '' sau khi đã cập nhật giá trị cho các record cũ (nếu có)") claims old records were updated, but no `UPDATE` statement exists. Migration 000002 gets this right (`UPDATE groups SET name = 'Unnamed Group' WHERE char_length(btrim(name)) = 0;` before the check) — the same pattern is simply missing here.

**Why it matters**: The `notifications` table exists since 000001. Any environment where a row was ever inserted — a shared staging DB, a seeded dev DB, a re-run after a partial rollout — fails `make migrate-up` with a constraint-violation error, blocking the deploy. It is only safe today because nothing has written to the table yet, which is a coincidence, not a guarantee.

**Suggested fix**: Add `UPDATE notifications SET title = '(no title)' WHERE btrim(title) = '';` and the equivalent for `body` immediately before the `ADD CONSTRAINT` block, mirroring 000002. Consider `ADD CONSTRAINT ... NOT VALID` + `VALIDATE CONSTRAINT` if the table is ever expected to be large.

### 🟠 River is started with 20 workers against a 10-connection pool, `internal/bootstrap/app.go:104`

**Problem**: `riverpkg.NewClient(db, riverWorkers, riverpkg.Config{MaxWorkers: 20})` hardcodes 20 on the *same* `pgxpool.Pool` the HTTP handlers use, whose `DB_MAX_CONNS` defaults to 10 (`internal/config/config.go:104`). Separately, the spec's "Configuration required" section calls for `RIVER_WORKER_COUNT` (default 5) and `RIVER_FETCH_COOLDOWN_MS` (default 100); neither exists anywhere in `internal/config` (`grep RIVER_` returns nothing), and the build plan asks for "configurable concurrency". `river/client.go:47` also falls back to 50 when unset, ten times the spec's default.

**Why it matters**: A burst of notification jobs lets River hold every connection in the pool (each running worker plus River's own producer/maintenance connections), starving inbound HTTP requests. Those requests then queue on `pool.Acquire` until the 15s timeout middleware fires, so a push backlog degrades into user-visible 500s on unrelated endpoints. The absence of an env knob means the only remedy in production is a code change and redeploy.

**Suggested fix**: Add `RIVER_WORKER_COUNT` (default 5) and `RIVER_FETCH_COOLDOWN_MS` (default 100) to `config.Load`, pass them through `riverpkg.Config`, lower the `client.go:47` fallback to match, and either validate `worker count < DB_MAX_CONNS` at startup or give River its own small pool.

### 🟠 No validation of title/body/type before insert; a long rejection reason breaks the caller, `internal/modules/notification/usecase/service.go:64`

**Problem**: `SendToUser` builds the `domain.Notification` straight from `msg.Title`/`msg.Body` and inserts it. Nothing checks non-emptiness or the 60/255/1000-character limits the new CHECK constraints enforce. The repository wraps the failure as `fmt.Errorf("create notification: %w", ...)`, which matches neither `ErrInvalidInput` nor `ErrNotificationNotFound`, so `writeDomainError` (handler.go:107) renders it as a 500 `INTERNAL_ERROR`.

**Why it matters**: This is reachable, not hypothetical. `NewPaymentRejectedMessage` (payload.go:83) interpolates `rejection_reason`, which is uncapped `TEXT` in the schema (000001, payments table) — a creditor pasting a >1000-character reason makes `CreateNotification` fail. Once bills/payments call `SendToUser` inside their own flow, that error propagates and fails the *business* operation: the payment rejection itself 500s because the notification could not be stored.

**Suggested fix**: Validate and clamp in the usecase before insert — trim, reject empty title/body/type with `ErrInvalidInput`, and truncate body/title to the constraint limits (with an ellipsis) rather than letting a DB CHECK violation escape. Add a unit test with an over-long body.

### 🟠 Marking an already-read notification returns 404, `internal/modules/notification/repository/postgres/queries/notification.sql:26`

**Problem**: `MarkNotificationAsRead` filters `WHERE id = $1 AND user_id = $2 AND read_at IS NULL`, and `repository.go:113` maps `affected == 0` to `ErrNotificationNotFound`, which the handler renders as 404. The query cannot distinguish "does not exist / belongs to someone else" from "exists, is yours, already read".

**Why it matters**: AC-6 specifies 404 only for cross-user access ("to avoid leaking notification existence"). In practice a user taps a notification twice, or the Flutter app retries the PATCH after a flaky network, and gets a 404 for a notification that is sitting in their own list — the client has to special-case a 404 that means success. `PATCH .../read` should be idempotent.

**Suggested fix**: Drop `AND read_at IS NULL` from the WHERE clause (`SET read_at = COALESCE(read_at, now())` preserves the original read timestamp), so 0 rows affected unambiguously means not-found-or-not-yours and a repeat call returns 200.

### 🟠 FCM-disabled mode is not honored; nil guards are dead code, `internal/bootstrap/app.go:88`

**Problem**: `fcm.New` returns a typed `(*fcm.Notifier)(nil)` when no credentials are configured (client.go:38). That nil pointer is passed to `NewNotificationWorker` (app.go:102) and `NewService` (app.go:111), where the parameters are **interfaces** — so `w.pushNotifier == nil` (send_notification.go:49) and `s.pushNotifier != nil` (service.go:85) are both false/true respectively for a nil pointer, and every disabled-mode guard in the module never fires. Meanwhile the spec's key invariant is explicit: "When FCM is disabled ... the notification usecase creates the in app record but does not enqueue a push delivery job. The notification worker is not registered with River." Here the worker is always registered and the enqueuer is always non-nil, so jobs are always enqueued.

**Why it matters**: Today it degrades quietly — every notification in a credential-less environment (all local dev) enqueues a River job that does a `GetActiveFCMTokenByUserID` query and then no-ops inside the nil-receiver guard on `SendToDevice`. That is pointless DB and queue load, and it means the disabled path is exercised in a way that looks healthy while doing nothing. The real hazard is the typed-nil footgun: the next person who adds a genuine `if pushNotifier == nil { skip }` branch will find it silently ineffective.

**Suggested fix**: Have `fcm.New` return an explicit `(nil, nil)` handled at the call site — in `bootstrap.New`, branch on `fcmNotifier == nil` (a concrete pointer comparison, done *before* it is placed in any interface) and skip both `river.AddWorker` and the enqueuer, passing `nil` interfaces to `NewService`. Alternatively return a `noopNotifier` value so the disabled case is a real type rather than a nil pointer.

## Minor

### 🟡 `ClearFCMToken` is not scoped to the user, `internal/modules/notification/repository/postgres/queries/notification.sql:43`

**Problem**: `UPDATE sessions SET fcm_token = NULL WHERE fcm_token = $1` matches every session holding that token value, across all users. FCM registration tokens are per app-install, so when user A signs out and user B signs in on the same phone, both rows can legitimately carry the same string.

**Suggested fix**: Scope the update to the user whose job is running (`AND user_id = $2`), and pass `job.Args.UserID` from the worker. Clearing a genuinely dead token everywhere is defensible, but it should be a deliberate choice, not a side effect of the WHERE clause.

### 🟡 `Shutdown` leaks River, workers and the pool when the HTTP drain times out, `internal/bootstrap/app.go:170`

**Problem**: `if err := a.server.Shutdown(ctx); err != nil { return ... }` returns early, so `riverClient.Stop`, `cancelWorkers()`, `workers.Wait()` and `db.Close()` are all skipped whenever the shutdown context deadline is exceeded — which is precisely the case where an orderly drain matters most. AC-7 requires the drain-then-close sequence to run.

**Suggested fix**: Collect the HTTP shutdown error into a variable and run the remaining teardown unconditionally (defer or explicit sequence), returning the joined error at the end.

### 🟡 Page-size cap is 100, spec says 50, `internal/modules/notification/delivery/http/handler.go:35`

**Problem**: `helpers.ParseOffsetPager` uses `pagination.NewOffsetPager` with library defaults (`MaxPageSize = 100`). AC-6 specifies max `page_size` 50; `docs/openapi.yaml:465` documents 100, matching the code but contradicting the spec.

**Suggested fix**: Either pass `pagination.WithMaxPageSize(50)` at this call site, or update the spec to 100 to match the repo-wide convention. Right now three sources disagree.

### 🟡 Notification `type` vocabulary and payload shape diverge from the spec, `internal/platform/notification/fcm/payload.go:9`

**Problem**: The spec's initial type set is `bill_finalized`, `debt_reminder`, `payment_proof_submitted`, `payment_confirmed`, `payment_rejected`; the code defines `payment_reminder`, `new_bill`, `payment_confirmed`, `payment_rejected`, `group_invitation`, `bill_updated`, `system_announcement`. Only two overlap. The spec's JSONB payload contract also types `amount` as `int64` and includes `debt_id`; the code emits `amount` as a decimal string (`strconv.FormatInt`, payload.go:30) because `PushMessage.Data` is `map[string]string`, and never emits `debt_id`.

**Why it matters**: These strings are the Flutter deep-link contract. Whichever side the FE codes against will be wrong for the other.

**Suggested fix**: Reconcile — update spec 0006's type table and payload contract to the implemented names/shapes, or rename the constants. If `amount` stays a string in the FCM `data` map (which it must), say so in the spec so the FE parses it as a string.

### 🟡 Fallback direct-send swallows the error and skips dead-token cleanup, `internal/modules/notification/usecase/service.go:88`

**Problem**: `_ = s.pushNotifier.SendToDevice(ctx, token, msg)` discards the result entirely — no logging, no `IsInvalidTokenError` check, no `ClearFCMToken`. The `repo.GetActiveFCMTokenByUserID` error is also swallowed by `if err == nil`.

**Suggested fix**: Either apply the same dead-token handling as the worker, or delete the fallback branch. Since the enqueuer is always non-nil in `bootstrap` (see the disabled-mode finding), this path is currently only reachable from tests, which makes it a maintenance liability rather than a feature.

### 🟡 Notification list ordering has no tiebreaker, `internal/modules/notification/repository/postgres/queries/notification.sql:10`

**Problem**: `ORDER BY created_at DESC` alone. Two notifications sharing a `created_at` (same statement, or the same `now()` inside one transaction — common when a bill fan-outs to several members) have an undefined relative order, so an item can be repeated or skipped across offset pages.

**Suggested fix**: `ORDER BY created_at DESC, id DESC`. `id` is uuidv7 so it is already time-ordered; the covering index `(user_id, created_at DESC)` still applies.

### 🟡 Cleanup worker logs discard the error, `internal/modules/auth/jobs/workers.go:47`

**Problem**: `log.Printf("event=auth_cleanup_failed")` and `log.Printf("event=media_cleanup_claim_failed")` are called inside `if err != nil` blocks but never include `err`. The failure is recorded, its cause is not.

**Suggested fix**: Append `err=%v`. Also note this goroutine has no `recover()`, so a panic anywhere in `cleanupMedia` takes the whole process down.

## Nits

- ⚪ `internal/modules/auth/jobs/workers.go:82`, defines `func min(a, b int) int`, shadowing the Go 1.21 builtin. Delete it.
- ⚪ `db/migrations/000003_add_fcm_and_notifications.sql:5`, wraps four plain DDL statements in one `+goose StatementBegin/End` block; 000001 uses that directive only for the function body containing semicolons. Plain DDL does not need it.
- ⚪ `internal/modules/notification/usecase/service.go:54`, `if err == nil { rawPayload = b }` silently drops the payload on marshal failure. `map[string]string` cannot fail to marshal, so this is unreachable defensive code that hides a bug if the type ever changes; prefer returning the error.
- ⚪ `internal/modules/notification/usecase/service.go:59`, derives the notification `type` from `msg.Data["type"]` — a stringly-typed round-trip through the push payload. A dedicated field on `PushMessage` would be clearer and would survive a builder forgetting the key.
- ⚪ `internal/modules/notification/repository/postgres/repository.go:61`, calls `dbgen.New(r.db)` twice in `ListByUserID` (and once per method elsewhere); constructing the querier once in `New` would be tidier.
- ⚪ `internal/platform/notification/fcm/client.go:16`, `SendToAllUsers` publishes to a hardcoded `all_users` topic that nothing in this change subscribes clients to, and `Service.SendToAllUsers` has no caller and no test. Consider deferring it to the feature that needs it.

## Strengths

- Layering is faithful to the repo's clean-architecture rules: `domain` is dependency-free, the usecase declares its own `PushNotifier`/`JobEnqueuer` ports and never imports pgx or chi, and all wiring lands in `bootstrap/app.go`. The queue and FCM adapters sit correctly under `internal/platform/`.
- Shutdown ordering (HTTP drain → River stop → cancel cleanup workers → pool close) matches `docs/notification-module.md` exactly, and River's `Stop(ctx)` is given the caller's deadline rather than a fresh one.
- `GetActiveFCMTokenByUserID` correctly folds `pgx.ErrNoRows` into `("", nil)` so a user with no active session completes the job rather than retrying forever — exactly what AC-4 asks for, and it is unit tested.
- The worker's error taxonomy is right in shape: dead token → complete, DB/transport error → return for River backoff, with a test for each. The `formatMoney` integer implementation is allocation-light and correct including the negative case.
- The repository integration test creates two users and asserts cross-user isolation rather than only testing the happy path, and `notification.sql` uses `:execrows` to detect the not-found case instead of a separate SELECT.
- `docs/notification-module.md` is a genuinely useful module doc, and `openapi.yaml` was updated with all five endpoints including the 404 on `PATCH /{id}/read`.

## Test coverage

Coverage is above average for this repo and all suites pass (`go build ./...`, `go vet`, `go test` on the touched packages are clean). Unit tests cover the worker's four branches (success, dead token cleared, DB error propagated, no active token), `IsInvalidTokenError`, the nil-notifier/empty-token safe paths, the message builders, the usecase's enqueue and fallback paths, and all four handlers including the 404. A gated River integration test proves migrate → register → start → insert → work end-to-end, and the repository integration test covers create/list/pagination/unread-count/mark-read/mark-all plus user isolation.

Gaps worth closing, in priority order:

1. **No test asserts that a non-`NOT_REGISTERED` FCM error leaves the token alone.** `mockWorkerNotifier` only ever returns `fcm.ErrInvalidToken` or a generic error, so the `IsInvalidArgument` conflation (the blocker) is invisible to the suite. A test feeding an `INVALID_ARGUMENT`-shaped error and asserting `clearedToken == ""` would have caught it.
2. **The enqueue-failure path is untested.** `mockJobEnqueuer.errToReturn` exists in `service_test.go:82` but no test sets it, so the branch where a notification row is committed and the job insert then fails — the one that produces duplicate rows on retry — is never exercised.
3. **No test for over-long or empty title/body**, so the CHECK-constraint 500 (Major above) is unguarded. A repository integration test inserting a 1200-character body would pin the behavior.
4. **Retry/backoff semantics are asserted only at the unit level** (an error is returned). The River integration test covers a job that succeeds first try; it never observes a job that fails, retries, and eventually succeeds, so nothing verifies River's retry configuration actually applies to `send_notification`.
5. No test covers `SendToAllUsers` on the service, or `MarkAsRead` against an already-read notification (which is currently a 404 — see Major).
