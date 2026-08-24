# Review, split-settlement-v1 (8a049ee..f43f7ac), 2026-08-21

**Reviewed by**: claude-opus-5 (author on a different model)
**Scope**: 45 files in the reviewed path set, commit range `8a049ee..f43f7ac` (4 commits: spec, schema, debt-status fix, settlement implementation)
**Verdict**: Blocked

## Summary

This lands spec 0004 end to end: migrations 000008/000009, a new `settlement` module (domain → usecase → repository/postgres → delivery/http), a local NAPAS VietQR encoder, River workers for reminders/stalled alerts/cleanup, and OpenAPI updates. The lock discipline (group → debts in ascending id → payment) is applied consistently in all four mutating paths, the VietQR encoder is golden tested, and the database state matrix plus composite foreign keys genuinely enforce the invariants the spec asks for. Two things block merge: migration 000009 re-declares and, on rollback, **drops** the `media_cleanup_jobs` table that migration 000001 already owns; and two concurrent proof submissions under the same idempotency key share one Cloudinary object key, so the losing attempt deletes the winning attempt's committed proof image. Several AC-11 idempotency behaviours (in-progress 409, stored status replay) are specified but not implemented.

## Blockers

### 🔴 Migration 000009 drops a table owned by 000001, and its own DDL for that table never applies, `db/migrations/000009_split_settlement_v1.sql:123-135,166`

**Problem**: `media_cleanup_jobs` already exists from `000001_init_schema.up.sql:134-151` with columns `next_attempt_at`, `last_error_code` and no `reason`. 000009 re-declares it with `CREATE TABLE IF NOT EXISTS` and a *different* shape (`reason TEXT NOT NULL`, `last_error`, no `next_attempt_at`). Because the table exists, 000009's definition is silently a no-op — the live table stays in the 000001 shape, which is what `ProcessMediaCleanup` (`internal/modules/settlement/repository/postgres/repository.go:1251-1292`, querying `next_attempt_at`/`last_error_code`) actually depends on. The integration test name `TestSettlementQueuesMediaCleanupUsingSharedSchema` confirms the author knew this. The Down section then runs `DROP TABLE IF EXISTS media_cleanup_jobs`, destroying a table (and any pending cleanup rows) that 000009 did not create. It also creates a second, redundant partial unique index (`uq_media_cleanup_jobs_active_object`) duplicating 000001's `uq_media_cleanup_jobs_open_object`.

**Why it matters**: Rolling back 000009 — a routine deploy-recovery action — silently deletes production cleanup state and leaves the bill module's cleanup worker broken with no migration that recreates the table. Meanwhile the migration file documents a schema that does not exist anywhere, which will mislead the next person who reads it or bootstraps a DB from the spec.

**Suggested fix**: Delete the `CREATE TABLE media_cleanup_jobs` / `CREATE UNIQUE INDEX uq_media_cleanup_jobs_active_object` blocks and the `DROP TABLE media_cleanup_jobs` from 000009 entirely; the table already exists and is shared. If settlement genuinely needs a `reason` column, add it as an `ALTER TABLE ... ADD COLUMN IF NOT EXISTS reason TEXT` (nullable, or with a default) and drop only that column in Down. Update the spec's data-model table to match.

### 🔴 Concurrent proof attempts under one idempotency key share an object key; the loser deletes the winner's proof image, `internal/modules/settlement/usecase/service.go:149-159` with `repository/postgres/repository.go:549-575`

**Problem**: `PrepareProof` inserts the idempotency row `ON CONFLICT DO NOTHING`; when the row already exists and is `in_progress`, it does not return `ErrIdempotencyInProgress` — it reuses the stored `operation_id` (line 574) and returns normally. Both requests then compute the identical key `payments/{paymentId}/proofs/{operationId}` (service.go:149) and both upload to it (`BillStorage.Upload` sets `Overwrite: true`, `internal/platform/storage/cloudinary/bill.go:63`). One wins the DB transaction; the other fails at the `payment.Status != pending_proof` recheck and runs `s.storage.Delete(ctx, uploaded)` on **the same key the winner just committed** (service.go:156).

**Why it matters**: The payment row ends up in `pending_confirmation` with `image_object_key` pointing at a deleted asset. The creditor sees a broken proof image and cannot verify a transfer that actually happened; the state matrix constraint still passes so nothing surfaces the corruption. This is exactly the scenario spec invariant 16 forbids ("never the key committed by another request") and AC-6's "a losing or failed attempt deletes only its own object". The window is precisely the retry-under-the-same-key case that idempotency keys exist to serve.

**Suggested fix**: Make `PrepareProof` return `domain.ErrIdempotencyInProgress` when it finds an existing row in `in_progress` state (this is also required by AC-11), so a second concurrent attempt never reaches the upload. Independently, harden the compensation path so it only deletes when the attempt provably did not commit — e.g. delete only after confirming the payment's stored `image_object_key` is not this key, or make the object key unique per attempt rather than per idempotency record.

## Major

### 🟠 AC-11's `409 IDEMPOTENCY_IN_PROGRESS` is unreachable for proof submission, `internal/modules/settlement/repository/postgres/repository.go:553-575`

**Problem**: `beginIdempotency` (used by create/confirm/reject/remind) does return `ErrIdempotencyInProgress`, but `PrepareProof` — the only path for the proof operation — has no such branch. It falls through and reuses the operation ID.

**Why it matters**: AC-11 and the API surface table both require this response for every mutation. Beyond the spec gap, it is the root cause of the second blocker.

**Suggested fix**: Add the `state == "in_progress"` branch returning `ErrIdempotencyInProgress`, and populate/read the `retry_after` column rather than the hardcoded header value.

### 🟠 Stored idempotency `response_code` is written but never replayed, `repository.go:348-350,733-740` and `usecase/service.go:97`

**Problem**: `completeIdempotency` persists `response_code` (201 for a fresh QR, 200 otherwise), but every replay path discards it: `CreatePayment` returns `replay, false, nil`, and the handler maps `created == false` to `http.StatusOK` (`delivery/http/handler.go:109-113`).

**Why it matters**: AC-11 states explicitly that a completed match "replays its stored status code and response body, including an original `201` or `200`". A client that retries a QR creation after a network timeout now sees 200 where the original was 201 and may conclude nothing was created.

**Suggested fix**: Have the repository return the stored status alongside the replayed payment and thread it through to the handler, rather than deriving status from the `created` boolean.

### 🟠 `PAYMENT_REMINDER_MAX_COUNT` is validated to reject any value except 3, `internal/config/config.go:601`

**Problem**: The validation reads `... || c.Settlement.ReminderMaxCount != 3 || ...`, so setting the documented env var to anything but 3 fails startup with `settlement settings are invalid`. Separately, `RemindDebt` hardcodes `count >= 3` (`repository.go:1111`) instead of consulting the config, and the DB constraint `chk_debts_reminder_count` hardcodes `BETWEEN 0 AND 3`.

**Why it matters**: A documented configuration knob that crashes the process when configured is worse than no knob, and the failure message does not name the offending variable. Raising the cap later requires touching three places including a migration.

**Suggested fix**: Validate a range (e.g. `< 1 || > 3`, matching the DB constraint) instead of equality, and have `RemindDebt` and `ProcessAutomatedReminders` share the configured value. If 3 is truly fixed by the schema, drop the env var and say so in the spec.

### 🟠 Usecase layer imports `net/http`, violating the project's layering rule, `internal/modules/settlement/usecase/service.go:8,125`

**Problem**: `CLAUDE.md` states the usecase layer "Never imports `pgx`, `chi`, or `net/http`". `service.go` imports `net/http` solely for `http.DetectContentType`.

**Why it matters**: This is an explicit project convention, and content-type sniffing is a transport concern; the handler already has the multipart part header available.

**Suggested fix**: Sniff the content type in `delivery/http/handler.go` (or in a small `internal/platform` helper) and pass the resolved value into the usecase input.

### 🟠 Any database error while loading the creditor's bank profile is reported as `422 BANK_ACCOUNT_REQUIRED`, `repository.go:334-338,502-505,591-594,852-855`

**Problem**: All four sites do `... .Scan(&code, &account, &holder); if e != nil { return ErrBankAccountRequired }`. That catches connection failures, query errors, and scan-type mismatches identically to the intended "columns are NULL" case.

**Why it matters**: A transient DB fault during payment creation tells the payer their creditor has no bank account. The user is sent to fix something that is not broken, and the real error never reaches logs or metrics (`recordOperation` records it as an ordinary error outcome).

**Suggested fix**: Scan into nullable types, return `ErrBankAccountRequired` only when the values are NULL/blank, and wrap anything else as an internal error.

### 🟠 Proof images are force-converted to JPEG by the reused bill storage adapter, `internal/platform/storage/cloudinary/bill.go:58-65`

**Problem**: `SubmitProof` is wired to `billStorage` (`internal/bootstrap/app.go:181`), whose `Upload` hardcodes `Format: "jpg"` and `Overwrite: true`. AC-6 accepts JPEG, PNG, and HEIC.

**Why it matters**: PNG screenshots of banking apps — the dominant proof format — are re-encoded lossily, degrading exactly the text a creditor needs to read. `Overwrite: true` also removes the last safety net against the blocker above.

**Suggested fix**: Either give settlement its own storage adapter that preserves the source format, or parameterise `BillStorage.Upload` with the format and pass the validated content type through.

### 🟠 Notification payloads carry no identifiers, `internal/modules/settlement/usecase/service.go:43`

**Problem**: `s.notify` builds `map[string]string{"type": kind}` for every notification. The `payment_id`, `debt_id`, `amount`, and group are all available at the call sites but are dropped; the `group_activities` rows get proper metadata, the notifications do not.

**Why it matters**: A push notification saying "Your payment was confirmed" gives the mobile client nothing to deep-link to, so AC-7/AC-8's "enqueue a notification for the Payer" is satisfied only in the weakest sense. Adding it later is a client-visible payload change.

**Suggested fix**: Pass the relevant identifiers from each repository call site into `BeforeCommit` and into the notification payload.

## Minor

### 🟡 QR canonical hash is computed over un-normalized debt ID strings, `usecase/service.go:81-95`

Duplicate detection normalizes via `uuid.Parse(...).String()`, but `ids` (which is sorted and hashed) keeps the caller's raw casing. The same debt set submitted with upper-case UUIDs produces a different canonical hash, so a legitimate retry can return `409 IDEMPOTENCY_KEY_REUSED`. Hash the normalized forms.

### 🟡 `Retry-After` is hardcoded to `1`, `delivery/http/handler.go:275`

The `payment_idempotency_keys.retry_after` column exists for this and is never written or read. A fixed 1-second hint invites retry storms on a slow operation.

### 🟡 The 10 MB proof limit is duplicated as a literal in the handler, `delivery/http/handler.go:126`

`const max = 10 << 20` shadows the configured `PAYMENT_PROOF_MAX_BYTES` that the service enforces (`service.go:127`). Raising the config value silently has no effect because the handler truncates first. Thread the configured value into the handler.

### 🟡 `uuid.MustParse` on request-supplied values, `repository.go:110,155,176,490,581,852,983`

These are safe today only because an earlier `uuid.Parse` in the same function already succeeded. Any future reordering turns a malformed ID into a panic in a money path. Reuse the already-parsed values.

### 🟡 `covered_debt_ids` serializes as `null`, not `[]`, `delivery/http/response.go:115`

`domain.Payment.CoveredDebtIDs` is appended to from nil, so a payment with no links emits JSON `null` where the spec's `payment_detail` declares a UUID array. Initialize the slice or normalize at the boundary.

### 🟡 `item_subtotal` money arithmetic happens in the HTTP response layer, `delivery/http/response.go:58-59`

The per-bill subtotal is accumulated by parsing and re-formatting a string on every item row. Financial aggregation belongs in the query or the usecase; the string round-trip is also needless work per row.

### 🟡 An over-long proof note is silently truncated before validation, `delivery/http/handler.go:160`

`io.LimitReader(part, 2001)` caps the note at 2001 bytes with no error. A 500-rune multibyte note can exceed 2001 bytes and be truncated mid-character, and the truncated value then feeds the canonical request hash. Read up to the limit plus one and reject explicitly when exceeded.

### 🟡 `QueueMediaCleanup` discards its `reason` argument, `repository.go:896`

The signature takes a reason (`_ string`) and never stores it, so the durable cleanup rows carry no diagnostic context — the very thing the spec's `reason` column was for. Either persist it or remove the parameter.

## Nits

- ⚪ `internal/platform/vietqr/generator.go:47`, `tlv` formats length with `%02d`, which silently emits three digits and corrupts the payload for any value over 99 bytes. All current values are short, but a length guard returning an error would make that guarantee explicit.
- ⚪ `repository/postgres/queries/settlement.sql:57-59`, `total_owed`/`total_receivable` use `NOT IN ('settled','voided')` while AC-1 specifies `awaiting` plus `pending_confirmation`. Equivalent only because invariant 9 promises the two legacy statuses are never written; stating the two statuses explicitly would be self-documenting.
- ⚪ `repository/postgres/queries/settlement.sql:98`, `debt_count` sums both directions of a pair while `total_amount` is netted, so the count does not correspond to the amount shown. The spec does not pin this down; worth confirming it is intended.
- ⚪ `repository.go:1154-1190`, `ProcessAutomatedReminders` holds one transaction across up to 100 debts spanning many groups, issuing 2 statements per row. Fine at current scale; batch the updates before it grows.
- ⚪ `repository/postgres/queries/settlement.sql:108-129`, `GetPaymentRow`, `ListPaymentDebtIDs`, `ListAutomatedReminderCandidates`, and `ListStalledPaymentCandidates` are generated but the repository uses hand-written equivalents instead. Dead generated code.

## Strengths

- Migration 000008 is exactly right and well commented: it recognizes that changing the query predicate to `NOT IN ('settled','voided')` without matching the partial index predicates would silently lose index usage, and it uses `CONCURRENTLY` with `NO TRANSACTION` correctly for a live table.
- Lock ordering (group → debts by ascending id → payment) is applied identically in `CreatePayment`, `SubmitProof`, `finishPayment`, and `RemindDebt`. This is the single most important correctness property in the feature and it was not cut corners on.
- The `chk_payments_state_matrix` constraint plus the four-column composite foreign keys on `payment_debts` push the hard invariants into the database, and the integration test actively probes them with cross-pair and invalid-state inserts rather than only testing the happy path.
- `TestGeneratorBuildGolden` pins the exact TLV byte string including the CRC16, which is the correct way to test a wire-format encoder.
- `ProcessStalledPayments` re-asserts `stalled_alerted_at IS NULL` in the UPDATE and skips on zero rows affected, so the "exactly one alert" guarantee holds even if the SKIP LOCKED select races.

## Test coverage

Unit coverage of the pure logic is decent: image magic-byte validation across all three formats, reason trimming and bounds, debt-ID canonicalization, response mapping, worker orchestration, and notification rollback are all exercised, and the Postgres integration test walks a full QR → proof → confirm lifecycle plus constraint rejection.

The gaps line up with the riskiest code:

- **Idempotency (AC-11) is essentially untested.** No test covers a conflicting request hash, an in-progress record, replay of a stored status code, or expiry cleanup. The `response_code`-not-replayed bug and the missing in-progress branch would both have been caught by the spec's own critical scenario 12.
- **No concurrency tests at all**, despite the spec calling for four (scenarios 5, 8, 9, 10: concurrent proof attempts, reminder races, concurrent workers, proof vs. bill void). The proof-deletion blocker lives exactly in that untested space.
- **`CreatePayment` supersession and exact-set replay** — the branch at `repository.go:386-412` that decides between replaying a payment and superseding it — has no test in either the unit or integration suite.
- **`RemindDebt`'s rate limit** (3-send cap, 24-hour interval, captain-vs-creditor authorization) is untested.
- **`ProcessMediaCleanup`** is never called by any test; only `QueueMediaCleanup` is. Given that the migration and the code disagree about that table's schema, a test that actually runs the cleanup query would be valuable.
