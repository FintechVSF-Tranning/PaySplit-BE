# Review, split-settlement-v1 feature slice round 3 (f43f7ac^ → working tree), 2026-08-22

**Reviewed by**: ox-alpha (author on a different model)
**Scope**: 33 hand-written files, feature-slice diff `git diff f43f7ac^` (committed feature work plus all uncommitted working-tree fixes; generated `*/sqlc/*`, docs, and the untracked out-of-scope migration 000010 excluded from the counted scope)
**Verdict**: Changes requested

## Summary

Third pass over split and settlement v1. The fresh fix surface is substantive, not cosmetic: failed proof attempts no longer burn their idempotency key (a `ResetProofAttempt` lease plus operation-ID rotation lets retries resume per invariant 16), QR creation now checks idempotency before bank validity so completed records always replay their original 201/200, notification payloads finally carry `group_id` + `payment_id`/`debt_id` at every call site, and proof storage moved to a dedicated adapter with a consistent upload/signed-URL format verified end to end against real Cloudinary. Both prior majors are resolved or downgraded (details below). What keeps this at Changes requested is one new safety major: the settlement integration suite falls back to `DATABASE_URL`, so running the documented test command against a shared database executes *global* worker logic — automated reminders to real users, stalled alerts, and Cloudinary deletions of unrelated pending objects.

## Disposition of prior findings (rounds 1–2)

| Prior finding | Status |
|---|---|
| 🔴 R1: Migration 000009 drops `media_cleanup_jobs` owned by 000001 | **Fixed.** The table DDL/DROP is gone; Up/Down only touch objects 000009 owns (`db/migrations/000009_split_settlement_v1.sql`). Down restores the pre-feature view definition correctly. |
| 🔴 R1: Concurrent proofs under one key share an object key; loser deletes winner's image | **Fixed.** In-progress guard returns `ErrIdempotencyInProgress` (`repository.go:576-586`); compensation deletes only its own key; on DB-level failure `ResetProofAttempt(..., true)` rotates the operation ID so the dead key can never be reused (`service.go:161-168`, `repository.go:625-646`). Pinned by unit tests and integration reset/rotation assertions. |
| 🟠 R1: AC-11 in-progress 409 unreachable for proof | **Fixed** (same branch as above), plus a retryable-resume path for abandoned attempts that also resolves R2 Minor 1. |
| 🟠 R1: Stored `response_code` never replayed | **Fixed.** `beginIdempotency` returns the stored code; `CreatePayment` maps it to `created`; handler emits 201/200 accordingly (`repository.go:324-330`, `handler.go:114-117`). |
| 🟠 R1: `PAYMENT_REMINDER_MAX_COUNT != 3` fails startup | **Fixed.** Range validation 1–3 (`config.go:496`), value threaded service → repository → workers, unit-tested. |
| 🟠 R1: Usecase imports `net/http` | **Fixed.** Sniffing lives in the handler (`handler.go:185-187`). |
| 🟠 R1: DB faults become `422 BANK_ACCOUNT_REQUIRED` | **Fixed.** All four sites scan into `pgtype.Text`, distinguish NULL/no-rows from wrapped internal errors (`repository.go:342-351, 501-510, 604-615, 894-906`). |
| 🟠 R2: Proof images force-converted to JPEG by the bill adapter | **Partially fixed → downgraded to Minor 1.** Proof now has its own adapter, but still transcodes everything — to WebP this time. |
| 🟠 R2: Notification payloads carry no identifiers | **Fixed.** Every call site passes `group_id` + `payment_id`/`debt_id` through `BeforeCommit` into the stored payload (`repository.go:442, 925, 1070, 1183, 1237, 1282`); unit (`TestNotifyPreservesRoutingIdentifiers`) and integration tests pin it. Payload is two short strings — no leak, no FCM size concern. Amount is still absent (nit). |
| 🟡 R2 Min 1: Failed proof burns its idempotency key until expiry | **Fixed.** `ResetProofAttempt` writes `retry_after=now()` as an abandoned marker; `PrepareProof` resumes such attempts with the stored operation ID and clears the marker (`repository.go:554-587, 625-646`). Upload-failure retries keep the same key (no object was created); DB-failure retries rotate it (old key queued for cleanup). The concurrent double-resume race is closed by the idempotency-row `FOR UPDATE`: the second transaction re-reads `retry_after=NULL` under EvalPlanQual and gets 409. Integration test walks reset → resume → claim → 409. |
| 🟡 R2 Min 2: QR creation checks bank before idempotency replay | **Fixed.** `beginIdempotency` now precedes the bank lookup (`repository.go:324` vs `:342`). |
| 🟡 R2 Min 3: `QueueMediaCleanup` writes `reason` owned by out-of-scope migration | **Partially fixed → Minor 2.** The error is no longer swallowed (`errors.Join` at `service.go:159, 168`), but the slice still cannot deploy independently of untracked 000010, and committed sqlc models include `MediaCleanupJob.Reason` generated only because that file exists locally. |
| 🟡 R2 Min 4: `Retry-After` hardcoded to 1 | **Still open** (`handler.go:286`). |
| 🟡 R2 Min 5: `item_subtotal` string round-trip in response layer | **Still open** (`response.go:58-59`). |
| 🟡 R2 Min 6: `uuid.MustParse` on DB-derived values in money paths | **Still open** (`repository.go:110, 155, 176, 490, 595, 605, 896, 1030, 1179`). |
| 🟡 R2 Min 7: OpenAPI omits settlement 422s/404 and uses bare item schemas | **Still open.** The only 422s in the spec belong to bill endpoints; `/payments/*` documents no 422 and proof lacks its 404; `ExpensePage.bills` / `DebtPage.debts` remain `{type: object}` arrays. |
| 🟡 R2 Min 8: HEIC proofs depend on client-declared content type | **Still open** (`handler.go:185-187`); `http.DetectContentType` can never return HEIC. |
| ⚪ R2 nits (VietQR `%02d`, dead generated queries, matrix `debt_count`, `NOT IN` predicates, reminder single-transaction scope, unnamed config error) | **All still open**, unchanged. |

## Major

### 🟠 Settlement integration tests fall back to `DATABASE_URL` and execute global worker operations against whatever database is configured, `internal/modules/settlement/repository/postgres/repository_integration_test.go:21-30`

**Problem**: `settlementTestPool` skips only when neither variable is set: it loads the repo `.env`, falls back to `DATABASE_URL`, and runs anyway. The project convention (CLAUDE.md, and auth/group/admin/notification modules) is to skip unless `TEST_DATABASE_URL` is set. This matters more here than in the bill module (which already has the same fallback): `TestSettlementPaymentLifecyclePostgres` invokes `ProcessAutomatedReminders`, `ProcessStalledPayments`, and `ProcessMediaCleanup`, which are deliberately unscoped queries over the *entire* database (`ORDER BY id ... LIMIT 100`). Run against any shared/staging database, `go test ./...` increments real debts' reminder counts and enqueues notifications to real users, alerts real creditors, and deletes unrelated pending Cloudinary objects via the cleanup worker.

**Why it matters**: The documented merge gate (`make test` → `go test ./...`, with the Makefile exporting `.env`) silently becomes a destructive operation the moment `DATABASE_URL` points anywhere but an isolated local instance. A convention violation that sends push notifications and deletes media as a side effect of running tests will eventually bite someone's staging environment.

**Suggested fix**: Gate strictly on `TEST_DATABASE_URL` like the other modules (keep the `.env` load if desired, but skip when the dedicated variable is absent). Longer term, the worker functions could accept a scope or the tests could assert eligibility counts before claiming, but the gate alone removes the hazard.

## Minor

### 🟡 Proof images are still transcoded — to WebP now — discarding the client's format, `internal/platform/storage/cloudinary/proof.go:27-33`

**Problem**: The prior major (forced JPEG through the bill adapter) is half-addressed: proof has a dedicated adapter and `SignedURL` now matches the stored format, but `Upload` hardcodes `format=webp`, so PNG screenshots and HEIC captures are lossily re-encoded just like before, only to a better codec. AC-6 accepts JPEG/PNG/HEIC; nothing in the spec mandates conversion.

**Why it matters**: This is evidence of a money transfer, and the stored artifact silently diverges from what the payer submitted. WebP at Cloudinary's default quality is materially kinder to screenshot text than JPEG, which is why this is downgraded from Major — but it remains an undocumented product decision embedded in an adapter.

**Suggested fix**: Either preserve the source extension (derive it from the validated content type and thread it into the storage call), or convert losslessly (`webp:lossless` transformation) so the verification artifact never loses fidelity. At minimum record the decision (and its quality setting) in the module README alongside the existing "private assets" paragraph. Note the end-to-end test added for the conversion path (`bill_integration_test.go`, asserting RIFF/WEBP bytes through the signed download URL) is genuinely good — extend it to HEIC input when convenient.

### 🟡 Slice depends on the out-of-scope migration 000010 for `media_cleanup_jobs.reason`, `internal/modules/settlement/repository/postgres/repository.go:943-945`

**Problem**: Repeat of round-two Minor 3, partially fixed. `QueueMediaCleanup` persists `reason`, a column created by the untracked migration 000010 that belongs to another feature; committed generated code (`sqlc/models.go`, `MediaCleanupJob.Reason`) reflects that file's presence. On any database without 000010 every compensation enqueue fails at runtime. The failure is now surfaced instead of swallowed (`errors.Join` in `usecase/service.go`), but the slice is still not independently deployable and a fresh checkout regenerates different sqlc output.

**Why it matters**: Silent schema coupling between features makes deploy ordering implicit and will produce a confusing 503-with-hidden-causes on environments that applied only migrations 000001–000009.

**Suggested fix**: Land the column addition within this slice's own migration story (or explicitly document the hard dependency and sequencing in the spec's data-model section), and regenerate sqlc only from tracked migrations.

### 🟡 Debt-scan loops drop iteration errors, turning query failures into fake 409 state conflicts, `internal/modules/settlement/repository/postgres/repository.go:371-381, 867-877, 1001-1012`

**Problem**: All three `FOR UPDATE` scans (`CreatePayment`, `SubmitProof`, `finishPayment`) iterate `rows.Next()`, then call `rows.Close()` without ever checking `rows.Err()`. If iteration terminates due to a connection or context error, the partial set flows into the `len(selected) == 0 || len(ids) != len(debtIDs)` guards and is reported as `DEBTS_NOT_AWAITING` / `PAYMENT_NOT_PENDING_CONFIRMATION`.

**Why it matters**: A transient DB fault during a money mutation is misreported as a business-state conflict, exactly the class of bug the earlier 422-vs-500 fix eliminated for bank profiles. It also corrupts metrics (`recordOperation` logs a normal error outcome).

**Suggested fix**: Check `rows.Err()` after each loop (after `Close()`) and return the wrapped error; keep the emptiness guards for genuine empty sets.

### 🟡 `Retry-After` is still a hardcoded literal, `internal/modules/settlement/delivery/http/handler.go:286`

Repeat finding, unchanged. `payment_idempotency_keys.retry_after` is written by `ResetProofAttempt` but never read into responses; the header is constant `"1"` even though the resume design means a 409 now genuinely means "first attempt still in flight", where a computed hint would be trivial.

### 🟡 `item_subtotal` aggregation still happens via formatted-string round-trip in the response layer, `internal/modules/settlement/delivery/http/response.go:58-59`

Repeat finding, unchanged. Financial accumulation by `strconv.ParseInt` of the previously formatted value per item row remains fragile and untestable at the right layer; accumulate int64 per bill in the usecase or select `SUM(item_share)` per bill.

### 🟡 `uuid.MustParse` on DB-derived strings throughout the payment paths, `internal/modules/settlement/repository/postgres/repository.go:490, 595, 605, 896, 1030, 1179`

Repeat finding, unchanged. These parse values read back from rows in the same transaction; a panic bypasses the domain-error mapping entirely.

### 🟡 OpenAPI contract still omits the settlement error surface and detailed list schemas, `docs/openapi.yaml` (settlement paths)

Repeat finding, unchanged. `POST /payments/qr`, `GET /payments/{paymentId}`, and the proof endpoint implement and return `422 BANK_ACCOUNT_REQUIRED` (tested in `response_test.go`), none documents it; proof lacks its 404; `bills`/`debts`/`net_matrix` items are bare `{type: object}` despite the spec defining exact field lists.

### 🟡 HEIC acceptance still hinges on the multipart part declaring a content type, `internal/modules/settlement/delivery/http/handler.go:185-187`

Repeat finding, unchanged. The `http.DetectContentType` fallback cannot produce `image/heic`; sniff the ISOBMFF `ftyp` brand directly instead.

### 🟡 Six new indexes build non-concurrently on live tables, `db/migrations/000009_split_settlement_v1.sql` (index block)

**Problem**: `idx_debts_*`, `idx_payments_*` are plain `CREATE INDEX IF NOT EXISTS` on `debts`/`payments`, tables that have held production data since 000001. The file starts with `-- +goose NO TRANSACTION`, which is precisely the precondition that makes `CREATE INDEX CONCURRENTLY` legal — and the repo's own precedent (000008, praised in round one) used `CONCURRENTLY` for exactly this reason.

**Why it matters**: On a deployed environment each index build takes a lock that blocks writes to debts/payments for the build duration; six of them serialize into a noticeable deploy stall.

**Suggested fix**: Build the four indexes on pre-existing large tables with `CONCURRENTLY` (keeping plain creates for `payment_debts`, which 000009 itself creates). Drop-and-recreate in Down stays as-is.

### 🟡 Cleanup jobs that exhaust 10 attempts are stranded forever with no metric, `internal/modules/settlement/repository/postgres/repository.go:1301-1312`

**Problem**: `ProcessMediaCleanup` selects `attempt_count < 10`; after ten failed deletions (e.g., a prolonged Cloudinary outage) the row is never selected again, never marked terminal, and `RecordMediaCleanupFailure` — which exists and is used by the auth module — is never called here.

**Why it matters**: An orphaned private proof image persists indefinitely and the only trace is a `last_error_code` nobody surfaces; the durable-compensation guarantee of AC-6 quietly ends after ~50 minutes of backoff.

**Suggested fix**: Record the failure counter on each errored attempt, and either alert on rows exceeding the attempt ceiling or let the ceiling grow unbounded with exponential backoff.

## Nits

- ⚪ `internal/platform/vietqr/generator.go:47`, `tlv` still formats length with `%02d`, silently emitting three digits above 99 bytes. Repeat nit.
- ⚪ `queries/settlement.sql:108-129`, `GetPaymentRow`, `ListPaymentDebtIDs`, `ListAutomatedReminderCandidates`, `ListStalledPaymentCandidates` remain generated-but-unused. Repeat nit.
- ⚪ `queries/settlement.sql:98`, matrix `debt_count` sums both directions while `total_amount` nets. Repeat nit.
- ⚪ `queries/settlement.sql:57-59,84-85,92`, `NOT IN ('settled','voided')` instead of naming `awaiting`+`pending_confirmation`. Repeat nit.
- ⚪ `repository.go:1202-1243`, automated reminders hold one transaction across up to 100 debts spanning groups. Fine today; repeat nit.
- ⚪ `config.go:497`, validation message still doesn't name the offending settlement variable. Repeat nit.
- ⚪ `config.go:496`, the whole settlement validation block is gated on `VietQRServiceBaseURL != ""`; explicitly setting the base URL env to empty disables every settlement check (including `ProofMaxBytes > 0`), surfacing later as a constructor panic instead of a config error.
- ⚪ `handler.go:176-178`, an oversized image is rejected as `VALIDATION_FAILED` while the usecase reports the same condition as `INVALID_IMAGE`; pick one public code.
- ⚪ Notification payloads omit `amount`, which the activity metadata carries; adding it would let clients render richer bodies without another payload contract change.

## Strengths

- The idempotency-resume redesign is the best work in this round: the distinction between upload failure (same operation ID retained — no object existed, key safely reusable) and post-upload failure (operation rotated so the compensated key is never resurrected) resolves both the burned-key minor and the old cross-attempt deletion blocker with one mechanism, and the concurrency argument closes cleanly via the idempotency-row `FOR UPDATE`.
- Bill void joining the group-lock protocol with an explanatory comment (`bill/repository/postgres/repository.go:809-813`) keeps the slice's hardest invariant enforceable across module boundaries.
- The rewritten Cloudinary signed URL was not just swapped in but verified: SDK signing semantics match (`SignParametersUsingAlgoAndVersion` injects `timestamp`; defaults resolve to the standard download endpoint), and the new integration test downloads the converted asset and asserts RIFF/WEBP magic bytes through the signed URL end to end.
- Handler note handling now provably rejects rather than truncates, and the 2000-byte cap cannot falsely reject any valid ≤500-rune note (max 4 bytes/rune).
- Money semantics stay int64 internally and formatted strings at the boundary everywhere; the golden VietQR TLV+CRC16 test continues to pin the wire format.

## Test coverage

Unit coverage (always gating): debt-ID normalization feeding identical canonical hashes, JPEG/PNG/HEIC magic-byte acceptance and rejection including oversized images and long notes, replay-without-upload, in-progress producing zero storage mutations, release-on-upload-failure preserving the operation ID, compensation queuing the exact key and rotating the operation, configured reminder maximum threading through service and workers, worker fail-fast ordering and expiry-before-cleanup sequencing, notifier identifier preservation, response grouping/superseded hiding/error mapping including the `Retry-After` header, golden VietQR bytes, metrics counters, and config range acceptance.

Integration coverage (gated — see the Major about the fallback): full create → prepare → reset → resume → submit → confirm lifecycle with payload assertions, rejection resetting debts, rotation producing a fresh operation ID, reminder race with exactly one success, automated/stalled double-run claims, expired-key deletion, cleanup queue→process round trip including persisted reason, constraint rejection probes, and outsider/inactive denial.

Remaining gaps, in risk order: no concurrency test for proof vs. bill void in both winning orders (spec critical scenario 10 — the void-side supersession SQL added in this slice has no test exercising it against a live race); `CreatePayment`'s supersession/exact-set branch (`repository.go:386-412`) still untested; HEIC→WebP conversion untested end to end; multi-bill expense pagination boundaries untested; multipart handler edge cases (duplicate fields, oversized bodies) untested. None block beyond the items already listed.
