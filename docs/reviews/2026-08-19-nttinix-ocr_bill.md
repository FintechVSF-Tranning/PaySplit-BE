# Review, nttinix/ocr_bill (Bill and OCR v1, spec 0003), 2026-08-19

**Reviewed by**: claude-opus-5 (author on a different model)
**Scope**: 48 files (46 in the scoped branch diff vs `c49a854` + 2 untracked new test files), branch vs `main`
**Verdict**: Blocked

## Summary

This is a large, genuinely well-structured feature: a full bill lifecycle (manual/image draft → durable River OCR → candidate apply → allocation/review → transactional finalize → safe void) with clean layering, an honest Hamilton allocator, real optimistic locking, ordered lock acquisition on void, and a broad unit-test suite. The recent metrics-wiring follow-up does what it claims at the call-site level and is directly tested.

However, several things block merge. The most serious is that `ApplyCandidate` never checks that the OCR job it applies belongs to the bill in the URL — a missing ownership check that lets one group's extracted receipt content be written into another group's draft. Second, the shared media-cleanup worker is wired with the *avatar* Cloudinary client, which deletes `type=upload` assets, while receipt images are stored as `type=private`; receipt cleanup therefore silently no-ops and reports success, so private receipt images are never actually removed. Third, raw provider/DB error strings flow unfiltered into HTTP 500 bodies, into SSE events, and into a Prometheus label value — all three explicitly forbidden by the spec's security model and AC-14, and the metric case is also an unbounded-cardinality bug.

Beyond that, the queue-depth gauge is structurally the wrong mechanism for a durable queue, several validated config values (`BILL_IMAGE_MAX_BYTES`, `BILL_IMAGE_MAX_COUNT`, `BILL_OCR_MAX_ATTEMPTS`, `BILL_OCR_RAW_RETENTION`) are never used, and the 30-day raw-OCR purge worker is written but never registered.

## Blockers

### 🔴 `ApplyCandidate` does not verify the OCR job belongs to the bill, `internal/modules/bill/usecase/service.go:488-501`

**Problem**: The job is fetched with `s.repo.GetOCRJobByID(ctx, jobID)` — an unscoped lookup by primary key (`repository/repository.go:193`; the SQL has no `bill_id`/`group_id` predicate). Nothing afterwards compares `ocrJob.BillID` to `billID` or its group to `groupID`. The only gates are `ocrJob.Status == succeeded`, `bill.Version == expectedVersion`, and `bill.Version == ocrJob.Version` — and the last is trivially satisfiable because a fresh draft and a fresh job both sit at version `1`.

**Why it matters**: A user who belongs to two groups (or otherwise learns a job ID — job IDs are returned in bill detail and in the SSE snapshot to every active member) can apply group A's candidate onto group B's draft. That writes another bill's merchant name, bill date, item names, quantities and every money field into the target draft, and returns them in the response. It is a cross-tenant data disclosure and it silently corrupts a financial draft. AC-4 and the security model both require candidate application to be bill-scoped.

**Suggested fix**: Add an explicit `ocrJob.BillID != billID` check returning `ErrOcrJobNotFound` (not a distinguishable error, to avoid confirming the job exists), and change the repository port to `GetOCRJobByID(ctx, id, billID)` with `WHERE id = $1 AND bill_id = $2` so the scope is enforced in SQL rather than by convention. Add a unit test for the cross-bill job ID case.

### 🔴 Receipt image cleanup silently never deletes anything, `internal/bootstrap/app.go:224` + `internal/platform/storage/cloudinary/avatar.go:42-53`

**Problem**: Bill receipts are uploaded with `Type: api.Private` (`cloudinary/bill.go:58`) and correctly destroyed with `Type: api.Private` by `BillStorage.Delete` (`bill.go:136`). But the durable cleanup path — `DeleteDraftBill` → `media_cleanup_jobs` → `authjobs.Workers.cleanupMedia` — is constructed with `avatarStore`, i.e. `AvatarStorage.Delete`, which calls `uploader.Destroy` **without** a `Type`, defaulting to `upload`. It will not find a private asset. Worse, `AvatarStorage.Delete` only errors on a transport failure or a `nil` result; it never inspects `result.Result`, so Cloudinary's `{"result":"not found"}` is treated as success, `CompleteMediaCleanup` marks the row done, and the job is never retried.

**Why it matters**: Every receipt image for a deleted draft stays in Cloudinary forever, and the system reports the cleanup as successful. This breaks AC-13 outright and violates the spec's security model for private receipt assets. It also makes the newly-wired `paysplit_media_cleanup_failures_total` metric dead for exactly the case it was added to observe — the failure it is supposed to count is swallowed one layer below.

**Suggested fix**: Give the cleanup worker a storage implementation that knows the asset's delivery type — either record the provider/type alongside `object_key` in `media_cleanup_jobs` and dispatch, or inject a composite deleter. Independently, make `AvatarStorage.Delete` (and `BillStorage.Delete`) treat a non-`ok` `result.Result` as an error so failures surface instead of being marked complete. Add a test that a bill object key routes to a private destroy.

### 🔴 Internal error text is returned to clients, streamed over SSE, and used as a Prometheus label, `internal/modules/bill/delivery/http/handler.go:592`, `internal/modules/bill/jobs/ocr_worker.go:179,211,218`

**Problem**: Three instances of the same root cause — provider/DB error strings are never reduced to a cleaned code.

1. `writeDomainError` sets `msg := err.Error()` and falls through to `500 INTERNAL_ERROR` with that message verbatim. The usecase wraps aggressively (`"upload image %d to storage: %w"`, `"create bill in repo: %w"`, `"list active group members: %w"`), so clients receive pgx/Cloudinary internals — table names, constraint names, Cloudinary API messages.
2. `failJob` builds `reason` with `fmt.Sprintf("ocr provider failed after %d attempts: %v", job.Attempt, err)` and the llamaextract client wraps raw provider output (`client.go:85,94,303`). That `reason` is persisted to `ocr_jobs.error_message`, broadcast as the SSE `error` field (`ocr_worker.go:218`), and surfaced in the SSE snapshot (`sse_handler.go:130`).
3. The same `reason` is passed as the `error_code` **label value** of `paysplit_ocr_jobs_total` (`ocr_worker.go:211`).

**Why it matters**: The spec is explicit — "Raw provider content never appears in API responses or logs" (security model), AC-14.4 requires "counters with bounded reason labels", and AC-14.7 requires that "metric labels never contain object keys, signed URLs, item text, raw OCR, API keys". Case 3 is also a straightforward Prometheus cardinality bomb: every distinct provider error string (which routinely embeds a URL, a request ID, and a timestamp) creates a new permanent time series, and `/metrics` will grow without bound. Case 1 is standard internals leakage. Case 2 puts unfiltered upstream text in front of every group member.

**Suggested fix**: Introduce a small closed set of error codes (`provider_timeout`, `provider_unavailable`, `schema_invalid`, `download_failed`, `no_images`, `bill_not_found`) derived from the sentinel errors already in `domain/errors.go`. Use that code for the metric label, for the SSE payload, and for `ocr_jobs.error_message`; keep the full error only in a server-side log line. Separately, in `writeDomainError`, emit a fixed message for the unmapped `500` branch and log the real error instead of returning it.

## Major

### 🟠 `paysplit_ocr_queue_depth` is an in-process counter for a cross-process durable queue, `internal/modules/bill/jobs/ocr_worker.go:112-114,279,296` + `internal/platform/metrics/prometheus.go:223-229`

**Problem**: `IncOCRQueueDepth()` fires in the enqueuer, `DecOCRQueueDepth()` in the worker, both against a process-local gauge. Concrete imbalances:
- `EnqueueOCRJobTx` increments on `InsertTx` success, which is *before* the surrounding bill transaction commits. Any rollback (a later `CreateBill` step failing, or a commit error) leaves a permanent +1.
- The decrement is skipped whenever `Work` returns early before the transition: unparseable args (`:76-87`), `ErrOcrJobNotFound` (`:92-94`, e.g. the draft was deleted while queued), and an already `succeeded`/`failed` job (`:99-101`). Each is a permanent +1.
- The gauge resets to 0 on restart, after which decrements drive it negative.
- With more than one replica, the API pod increments and the worker pod decrements, so neither number means anything.

**Why it matters**: AC-14.1 names this gauge as the queue-depth signal. As written it drifts monotonically upward in normal operation and can go negative after a restart, so any alert built on it is untrustworthy — arguably worse than the stale zero the follow-up was fixing.

**Suggested fix**: Replace the inc/dec pair with a `prometheus.NewGaugeFunc` that runs `SELECT count(*) FROM ocr_jobs WHERE status IN ('queued','processing')` (bounded by a short timeout, or refreshed by a ticker into a plain gauge). That is correct across restarts, rollbacks, replicas, and every early-return path, and it removes the need to reason about balance at all. If the inc/dec approach is kept, at minimum move the increment to after commit and decrement on every terminal early-return.

### 🟠 `BILL_OCR_MAX_ATTEMPTS` and `BILL_OCR_RETRY_BASE_DELAY` are never applied to River jobs, `internal/modules/bill/jobs/ocr_worker.go:273-277,290-294`

**Problem**: Both `InsertTx` and `Insert` pass `nil` for `*river.InsertOpts`, so jobs use River's defaults (max attempts 25). The worker's own guard is `job.Attempt >= job.MaxAttempts`, which reads River's value, so the configured `3` never takes effect. `cfg.OCR.MaxAttempts` and `cfg.OCR.RetryBaseDelay` are loaded and validated (`config.go:446`) and then used nowhere.

**Why it matters**: AC-3 requires provider retries to use "configured timeout and retry values". In practice a hard provider outage produces up to 25 paid LlamaExtract attempts per bill instead of 3, and the draft stays stuck in `processing` far longer than intended (the partial unique index blocks any manual retry meanwhile).

**Suggested fix**: Pass `&river.InsertOpts{MaxAttempts: cfg.OCR.MaxAttempts}` from the enqueuer (thread the config into `NewEnqueuer`), and configure the retry policy on the River client from `RetryBaseDelay`.

### 🟠 The 30-day raw-OCR retention worker is written but never registered or scheduled, `internal/modules/bill/jobs/ocr_worker.go:232-255`, `internal/bootstrap/app.go:167`

**Problem**: `OCRRetentionWorker` and `PurgeExpiredRawOCRResponses` exist and are correct-looking, but `river.AddWorker` is only called for `NewOCRWorker`. No `OCRRetentionJobArgs` is ever inserted, and there is no periodic job configured. `cfg.OCR.RawRetentionDays` is validated and then unused.

**Why it matters**: The spec states raw provider responses are "removed 30 days after job completion even when a draft remains open" (security model, AC-3, data model row for `ocr_jobs`). As shipped, raw receipt extraction payloads are retained indefinitely. This is a stated data-retention commitment, not an optimization.

**Suggested fix**: Register the worker and add a River periodic job (`river.NewPeriodicJob`) that inserts `OCRRetentionJobArgs{OlderThanHours: int(cfg.OCR.RawRetentionDays.Hours())}` daily.

### 🟠 Multipart upload is unbounded before the 10 MB check, `internal/modules/bill/delivery/http/handler.go:73-116`

**Problem**: `r.ParseMultipartForm(50 << 20)` only sets the in-memory threshold; it does not cap the request body — the remainder spools to disk. Each part is then fully materialized with `io.ReadAll(f)` with no size check. The 10 MB limit only exists inside `receipt.Processor.Process` (`processor.go:60`), which runs after all files are already in memory. There is no `http.MaxBytesReader`, no per-file size check, and no `Content-Type` check at the handler.

**Why it matters**: An authenticated group member can POST a few multi-gigabyte parts and drive both disk and heap exhaustion before any validation runs. AC-1's "at most 10 MB" per file is enforced too late to protect the server. Note this endpoint bypasses `helpers.ReadJSON`, so the JSON path is unbounded too.

**Suggested fix**: Wrap `r.Body` in `http.MaxBytesReader` sized from `cfg.BillImage.MaxBytes * cfg.BillImage.MaxCount` plus slack before parsing, reject any `fh.Size > cfg.BillImage.MaxBytes` before opening it, and reject `len(fileHeaders) > cfg.BillImage.MaxCount`. Use `io.LimitReader` on each part as a belt-and-braces measure.

### 🟠 A failed mutation wedges its idempotency key `in_progress` for 24 hours, `internal/modules/bill/delivery/http/handler.go:559-581` + `usecase/service.go:1033-1067`

**Problem**: `checkIdempotency` reserves the key before calling the usecase. On every error path the handler calls `writeDomainError` and returns without releasing the reservation. The row stays `in_progress` until `expires_at` (24 h). A retry with the same key then matches `rec.State == "in_progress" && rec.OperationID != opID` and returns `409 IDEMPOTENCY_IN_PROGRESS` forever.

**Why it matters**: The natural client behavior after a transient `503 STORAGE_UNAVAILABLE` or a `500` is to retry with the same `Idempotency-Key` — that is the entire point of the header. Instead the operation becomes permanently unretryable for a day, and the user's only escape is generating a new key, which defeats the deduplication guarantee for the original attempt.

**Suggested fix**: Release or mark-failed the reservation when the usecase returns an error (a `defer` that deletes the `in_progress` row unless `CompleteIdempotency` ran), or set a short `retry_after` on reservation and let `CheckOrReserveIdempotency` reclaim a reservation whose `retry_after` has passed. Also stop discarding the `CompleteIdempotency` error with `_ =` — a failure there silently breaks replay for a *successful* mutation.

### 🟠 Assignment ratios deviate from the spec contract with no enforcement of the sum-to-one invariant, `db/migrations/000006_bill_and_ocr_v1.sql:63-64`, `usecase/service.go:972-1004`

**Problem**: AC-6 and the data model specify `share_ratio numeric(9,8)` constrained to `(0, 1]` and summing to exactly `1.00000000` per item, checked under the bill lock. The implementation renames the column to `weight numeric(10,4)`, adds no `CHECK` constraint at all, and `toAllocationInput` normalizes arbitrary weights by dividing by their sum. Validation is limited to "each weight parses as a positive finite float" (`service.go:185-192, 594-601`).

**Why it matters**: Two problems. First, it is a silent contract change: `numeric(10,4)` cannot represent the `numeric(9,8)` precision the spec promises, and the OpenAPI/FE contract now speaks of weights rather than ratios. Second, the invariant the spec asks to be enforced (per-item ratios total exactly 1) is replaced by implicit normalization, so a client sending obviously wrong data gets a silently reinterpreted split rather than a validation error. The database has no constraint backing it up either — a direct write of `weight = 0` or a negative value is accepted by the schema.

**Suggested fix**: Decide and record which contract holds. If weights are the intended design, update spec 0003 and the OpenAPI accordingly and add `CHECK (weight > 0)`. If ratios are, restore `numeric(9,8)`, add `CHECK (share_ratio > 0 AND share_ratio <= 1)`, and validate the per-item sum in `ReviewBill`/`finalizeBillImpl` alongside the existing totals check.

### 🟠 The request-timeout exemption can be triggered by any client on any route, `internal/transport/http/middleware/timeout.go:37`

**Problem**: The SSE carve-out added on this branch is `strings.Contains(r.Header.Get("Accept"), "text/event-stream") || strings.HasSuffix(r.URL.Path, "/events")`. The first clause is client-controlled and route-independent.

**Why it matters**: Any caller can append `Accept: text/event-stream` to a bill-create multipart upload, a finalize, or any other endpoint and escape the 15 s bound entirely. Combined with the unbounded upload above, that is a cheap way to pin request goroutines and connections indefinitely. The project states "All requests are bounded by a 15s timeout middleware", and this quietly makes that opt-out.

**Suggested fix**: Drop the `Accept`-header clause and key the exemption purely on the route — either the path suffix alone, or (better) register the SSE route outside the timeout middleware group so the exemption is structural rather than string-matched.

### 🟠 Hamilton allocation uses float64 where the spec requires integer arithmetic, `internal/modules/bill/usecase/allocation.go:105-107,122,260-262,277`

**Problem**: `exact := float64(it.LineTotal) * a.Ratio` and `float64(targetTotal) * (float64(base) / float64(totalBase))` do the core money math in binary floating point, and the tie-break comparison uses an `1e-9` epsilon. `Ratio` itself is already a lossy `w / totalWeight` division.

**Why it matters**: AC-6 says the preview "allocates item amounts, service charge, VAT, and discount with **integer VND arithmetic**", and invariant 4 says the same function must reproduce preview and final shares deterministically. Float rounding makes the remainder ordering sensitive to representation: two members with mathematically identical shares can land on either side of the `1e-9` threshold depending on magnitude, so the UUID tie-break is not reliably reached, and results are not reproducible against an independent (e.g. PostgreSQL `numeric`) recomputation.

**Suggested fix**: Represent the ratio as a scaled integer (e.g. `ratioScaled` in 1e-8 units), then `exact = LineTotal * ratioScaled`, `floor = exact / 1e8`, `remainder = exact % 1e8` — all `int64`, exact, and directly comparable. `LineTotal` up to ~9.2e10 VND stays well inside `int64`. Do the same in `runHamiltonForTotal` using `targetTotal * base` over `totalBase`.

### 🟠 Preview silently dumps any total discrepancy onto one member, `internal/modules/bill/usecase/allocation.go:203-237`

**Problem**: After allocation, if `computedTotalSum != in.Total` the whole difference is added to (or subtracted from) whichever member currently has the largest `FinalAmount`, and folded into their `RoundingAdjustment`.

**Why it matters**: On the finalize path this is harmless, because `finalizeBillImpl` has already proven `Total == Subtotal + ServiceCharge + VAT - Discount` and `Subtotal == sum(line_total)`. On the **preview** path (`GetBillDetail`, `service.go:346-356`) no such check runs — drafts are explicitly allowed to be mismatched (AC-5). So a draft whose reported total is off by, say, 500,000 VND shows one arbitrary participant carrying that entire 500,000 with no signal, which is precisely the "extraction errors become financial records silently" outcome the spec is designed to prevent. The `FinalAmount < 0 → 0` clamp on line 195 compounds this by discarding value before reconciliation.

**Suggested fix**: Have `CalculateHamiltonAllocation` allocate against the *computed* total and report the residual explicitly (a returned `Residual int64` or a mismatch code the response surfaces), rather than absorbing it into an arbitrary member. Callers that require exactness (finalize) already validate beforehand; callers that don't (preview) should show the discrepancy rather than hide it.

### 🟠 `RetryOCR` can permanently lock a bill out of OCR, `internal/modules/bill/usecase/service.go:450-459`

**Problem**: `CreateOCRJob` commits a `queued` row, and only then is `EnqueueOCRJob` called outside any transaction. If the River insert fails, the handler returns an error but the `queued` row survives. The partial unique index `uq_ocr_jobs_active_bill` then makes `GetActiveOCRJobByBillID` return non-nil forever, so every subsequent retry gets `409 OCR_ALREADY_RUNNING` and no worker will ever pick the orphan up.

**Why it matters**: A single transient queue error leaves the bill's OCR permanently wedged with no operator-free recovery, and SSE will report `queued` indefinitely. `CreateBill` already solves this correctly via the `BeforeCommit` hook — the retry path just doesn't use it.

**Suggested fix**: Add a `CreateOCRJobTx`-style repository method (or reuse the `BeforeCommit` pattern) so the row insert and the River insert commit together, mirroring `CreateBill`.

## Minor

### 🟡 The OCR manual-retry limit is skipped whenever the count query errors, `internal/modules/bill/usecase/service.go:436-439`

`attempts, err := s.repo.CountManualOCRAttemptsInWindow(...)`; the guard is `if err == nil && int(attempts) >= limit`. A database error therefore fails *open* — the rate limit is bypassed rather than the request rejected. For a limit that exists to cap paid provider calls, fail closed (or at least return the error) instead.

### 🟡 The manual-retry count includes the automatic job, `internal/modules/bill/repository/postgres/queries/bill.sql:289-291`

`SELECT COUNT(*) FROM ocr_jobs WHERE bill_id = $1 AND created_at >= $2` counts every job, including the one created automatically with the image draft. The spec says "counts *user initiated* `ocr_jobs` rows for the bill during the configured window", so users effectively get `limit - 1` manual retries. Add a `created_by_member_id`/origin predicate to distinguish them.

### 🟡 `BILL_IMAGE_MAX_BYTES`, `BILL_IMAGE_MAX_COUNT`, and `BILL_IMAGE_SIGNED_URL_TTL` are validated but never read

The values are loaded (`config.go:268-272,361-366`) and validated and then nowhere used. The limits are hardcoded instead: `5` in `handler.go:99` and `service.go:163`, `maxReceiptBytes = 10 * 1024 * 1024` in `processor.go:31`, `5*time.Minute` in `service.go:339`, and `position < 5` in the migration's `CHECK`. Setting the env vars changes nothing, which is worse than not offering them. Thread the config through, and relax the migration `CHECK` if the count is meant to be tunable.

### 🟡 `ReviewBill` returns `BILL_IMMUTABLE` where `VERSION_CONFLICT` is correct, `internal/modules/bill/usecase/service.go:665-679`

When the bill is already `reviewed` and `bill.Version != expectedVersion`, control falls out of the inner `if` to `return nil, domain.ErrBillImmutable` → `409 BILL_IMMUTABLE`. The bill is not immutable; the caller simply holds a stale version. AC-7 and the API table list `409 VERSION_CONFLICT` for this case, and the wrong code sends clients down a "give up" path instead of a "refetch and retry" path.

### 🟡 The multipart idempotency hash ignores the image bytes, `internal/modules/bill/delivery/http/handler.go:118-123`

The canonical request hash is built from `group_id`, `merchant`, `items_count`, `files_count` only. The spec requires "normalized metadata plus SHA 256 hashes of original bytes in zero based order". As written, two genuinely different uploads with the same merchant and file count reuse the key and replay the first bill instead of returning `409 IDEMPOTENCY_KEY_REUSED` — the exact confusion the reuse check exists to catch.

### 🟡 Partial-upload orphans have no durable recovery, `internal/modules/bill/usecase/service.go:199-205`

The cleanup on failure is a `defer` issuing best-effort `s.storage.Delete` calls with `context.Background()`. If the process dies between the first Cloudinary upload and the commit, nothing records the orphaned objects. AC-13 calls for recovery "through the reserved operation prefix", and `BillStorage.DeleteByPrefix` was written for exactly that — but it is never called anywhere. Enqueue a durable `media_cleanup_jobs` row keyed on the `bills/{operationID}/` prefix at reservation time, or write the prefix into the idempotency reservation so a sweeper can find it.

### 🟡 `ErrOcrAlreadyApplied` is defined and mapped but never returned, `domain/errors.go`, `handler.go:646-648`

AC-4 requires `409 OCR_ALREADY_APPLIED`. In practice a second apply of the same job hits `OCR_RESULT_STALE` instead (because the first apply bumped the version). That is a defensible outcome but not the documented one, and the dead sentinel suggests the case was intended to be handled explicitly. Track the applied actor/time on `ocr_jobs` as the data model describes, and return the specific error.

### 🟡 The migration's enum additions and their immediate use sit in one transaction, `db/migrations/000006_bill_and_ocr_v1.sql:8-34`

`ALTER TYPE bill_status ADD VALUE ... 'voided'` is followed in the same goose statement block by `UPDATE bills ... WHERE status = 'voided'` and `CHECK (status <> 'voided' ...)`. PostgreSQL forbids using an enum value added in the current transaction. This only works today because `'voided'` already exists from migration `000001`, making the `ADD VALUE` a no-op — i.e. it passes by accident, and would fail on a database where the value is genuinely new. Split the enum additions into their own migration (or their own non-transactional statement block).

### 🟡 Void reason and bill IDs are written verbatim into activity descriptions, `repository/postgres/repository.go:861,924`

`desc := fmt.Sprintf("voided bill: %s", p.Reason)` stores up to 500 characters of free user text, and the delete activity description embeds the full bill ID. The spec calls for a "cleaned reason" and, for draft delete, "a group activity with a **redacted** bill ID". Move the reason into the structured metadata (where it is already recorded) and keep the description to a fixed string.

### 🟡 Metrics endpoint token compared non-constant-time, `internal/platform/metrics/prometheus.go:270`

`parts[1] != bearerToken` is a length-and-content-dependent comparison. Use `crypto/subtle.ConstantTimeCompare`. Low practical risk given the endpoint, but it is a one-line fix on a new file.

### 🟡 `cleanupMedia` records the failure metric but never logs the underlying error, `internal/modules/auth/jobs/workers.go:66-81`

The `err` from `w.storage.Delete` is dropped entirely; only the counter moves and the retry is scheduled. When the counter starts climbing there will be nothing to diagnose from. Log the error (redacted) alongside the metric.

### 🟡 SSE connections are uncapped per user, `internal/modules/bill/delivery/http/sse_handler.go`

Nothing limits how many concurrent `/events` streams one user or one bill can hold. Each holds a goroutine, a hub subscription, and (via the exemption above) escapes the request timeout for up to `maxConnectionAge`. A per-user connection cap would bound this cheaply.

## Nits

- ⚪ `internal/modules/bill/jobs/ocr_worker.go:188` — `RecordOCRJob("succeeded", "none")` fires *before* `UpdateOCRJobSuccess`; if that write fails and River retries, the success is double-counted. Move it after the successful commit.
- ⚪ `internal/modules/bill/jobs/ocr_worker.go:170,187` — the provider label is hardcoded `"llamaextract"` rather than taken from the configured adapter name, so it will drift if a second provider is added.
- ⚪ `internal/modules/bill/jobs/ocr_worker.go:154-159` — a `stitchReceiptImages` failure is silently ignored and only the first page is sent to OCR. At minimum log which pages were dropped.
- ⚪ `internal/modules/bill/usecase/service.go:1017-1030` — `computeLineTotal` does `float64(unitPrice) * qty` then `math.Round`, losing exactness above 2^53; parse the quantity as a scaled integer instead.
- ⚪ `internal/modules/bill/usecase/service.go:338-342` — `SignedURL` errors are swallowed, so a bill silently renders with missing image URLs and no server-side trace.
- ⚪ `internal/modules/bill/delivery/http/handler.go:51` — `PATCH` is routed to the full-replacement `UpdateDraftBill`; a PATCH that behaves as a PUT is a contract trap for clients. Either drop it or implement real partial semantics.
- ⚪ `internal/modules/auth/jobs/workers.go:73` — `1 << min(job.AttemptCount-1, 10)` panics on a negative shift if `AttemptCount` is ever `0`. It cannot be today (the claim query pre-increments), but a `max(…, 0)` guard costs nothing.
- ⚪ `db/migrations/000006_bill_and_ocr_v1.sql:82` — `bill_shares` has a composite FK on `(member_id, group_id)` with no supporting index, contrary to the spec's "every foreign key column or matching composite prefix has an index".
- ⚪ `db/migrations/000006_bill_and_ocr_v1.sql:144-152` — the Down section drops `bills_voided_check` and `bills_split_method_check` but leaves `bills_finalized_check` in place and never restores `bills_check1`, so the rollback is not symmetric.
- ⚪ `internal/platform/metrics/prometheus_test.go:47-57` — `TestSetOCRQueueDepth_SetsAbsoluteValue` writes an absolute value to the shared global gauge and leaves it at `0`. It is safe today only because Go runs the package's tests sequentially in source order; a future `t.Parallel()` would make it and `TestIncDecOCRQueueDepth_TracksNetChange` interfere. Snapshot and restore, or use a private registry.
- ⚪ The implemented routes (`/api/v1/bills`, `/{id}/ocr-retry`, `/{id}/apply-candidate`, `group_id` as a query param) differ from the spec's API surface (`/api/v1/groups/{groupId}/bills`, `/ocr-jobs`, `/ocr-jobs/{jobId}/apply`). `docs/openapi.yaml` was written to match the code, so the FE contract is coherent — but spec 0003 is now stale on this point and should be updated so the two don't silently diverge further.

## Strengths

- The `VoidBill` repository path is the best code in the change: it locks the bill row first, then locks the debt rows in a defined order, checks every debt is `awaiting` with a null `payment_id`, and does the state change, debt voiding and activity insert in one transaction — exactly matching invariant 12 and pre-empting the Module 4 race before Module 4 exists.
- Optimistic concurrency is handled properly and consistently: every mutating SQL statement carries both `version = $n` and a status predicate in its `WHERE`, so the pre-transaction reads are advisory only and a lost update is structurally impossible. `UpdateDraftBill` clearing `reviewed_at`/`reviewed_by_member_id` in the same statement that bumps the version is precisely what AC-7 asks for.
- Cursor pagination is done correctly — keyset on `(created_at DESC, id DESC)` with a matching composite index, `limit + 1` lookahead clamped server-side in the repository, and an opaque encoded cursor. It will not drift under concurrent inserts the way an offset would.
- Test coverage is broad and mostly behavioral rather than mock-shaped: authorization is tested per role and per state, `TestHamilton_DeterministicUUIDTieBreaking` and `TestHamilton_LargeDiscount_NeverProducesNegativeFinalAmount` pin down real edge cases, and the new metric tests use unique per-test label values with before/after deltas, which is the right way to assert against a process-global registry.
- The `FinalizeBill`/`finalizeBillImpl` rename-and-wrap was done cleanly — the wrapper adds only timing, every return path flows through it, and the wrapped body is the original logic unchanged. No behavior change, and both outcomes are covered by tests.

## Test coverage

Coverage is genuinely strong for a change this size: 5 allocation tests, ~35 usecase tests spanning authorization, state machine, version conflicts, reconciliation, bank eligibility, notifications and replacement, 9 worker tests, 8 handler tests, plus SSE hub/handler tests and a PostgreSQL integration suite (correctly skipped without `TEST_DATABASE_URL`). The metrics follow-up is directly tested at both the helper level (`prometheus_test.go`) and the call-site level (`TestOCRWorker_Work_QueuedJob_DecrementsQueueDepth`, `TestFinalizeBill_Success_RecordsFinalizeDurationMetric`, `TestReviewBill_Mismatch_RecordsMismatchBlockMetric`, `TestApplyCandidate_Stale_RecordsStaleApplyMetric`, `TestGetBillDetail_*PreviewDurationMetric`, `TestCleanupMedia_DeleteFails_RecordsMediaCleanupFailureMetric`), and the `DoesNotRecord` negative cases are a nice touch.

Genuine gaps, roughly in priority order:

1. **No test that `ApplyCandidate` rejects a job belonging to a different bill.** This is the first blocker, and the absence of the test is why it survived.
2. **The queue-depth *increment* side is untested.** Only the decrement has coverage. There is no test for enqueue-then-rollback, for `ErrOcrJobNotFound`, or for the already-terminal-job early return — i.e. no test asserts the balance property the follow-up was meant to establish. (Fixing the gauge as suggested makes most of these moot.)
3. **No test that a bill receipt key routes to a *private* Cloudinary destroy.** `bill_test.go` covers `BillStorage` in isolation but nothing exercises the `DeleteDraftBill → media_cleanup_jobs → cleanupMedia` wiring, which is where the second blocker lives.
4. **Idempotency has no handler-level tests** for replay of a completed key, `409 IDEMPOTENCY_KEY_REUSED` on a differing request hash, or `409 IDEMPOTENCY_IN_PROGRESS` — all three are spec'd behaviors, and the wedged-reservation bug above would have shown up in the third.
5. **No multipart size/count/content-type rejection tests.** `TestCreateBill_Multipart_InvalidMetadataJSON_ReturnsBadRequest` is the only multipart case.
6. **AC-14 redaction is asserted nowhere.** The spec explicitly requires tests that "logs and metric labels never contain object keys, signed URLs, item text, raw OCR, API keys, account numbers, or idempotency keys". Such a test would have caught the raw-error-as-metric-label blocker; the current metrics tests pass synthetic labels, which proves the recording works but says nothing about what production code puts in them.
