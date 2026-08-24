# Review, namplh/split-bill (bill and OCR v1), 2026-08-22

**Reviewed by**: claude-opus-5 (author on an unspecified model, prior reviews suggest Sonnet/Opus family)
**Scope**: 53 files, branch vs `main` (merge base `c49a854`), scoped to the bill + OCR v1 feature only
**Verdict**: Changes requested

## Summary

This slice adds the whole bill lifecycle (manual/image draft, LlamaExtract OCR through River, candidate apply, allocation preview, review, finalize into immutable shares and debts, void, draft delete) plus an SSE hub, receipt image normalization and private Cloudinary storage. The core money path is the strongest part of the change: `CalculateFloorAllocation` is genuinely well reasoned, the item-discount folding into `final_price` is arithmetically consistent end to end (reconciliation, allocation and the DB CHECK constraints all agree), and optimistic locking is enforced in SQL (`WHERE status = ... AND version = $n`) rather than only in Go, so the read-then-write windows are safe.

The problems are on the edges, not in the arithmetic: the SSE stream authorizes the caller against a caller-supplied `group_id` without binding the bill to that group; an OCR job can be permanently stranded in `processing` with no reaper, which permanently blocks retries for that bill; image and storage failures surface as `500 INTERNAL_ERROR` instead of the `400 INVALID_IMAGE` / `503 STORAGE_UNAVAILABLE` the spec defines; and the provider timeout budget covers the entire upload → create → poll cycle, so the documented 8 s default is very likely to make live OCR fail. Test coverage is unusually good for a change this size, but every issue below sits in an untested branch.

## Major

### 🟠 SSE stream authorizes against a caller-supplied group, not the bill's group, `internal/modules/bill/delivery/http/sse_handler.go:73-95`
**Problem**: `group_id` is read from the query string and membership is checked against *that* group (`h.repo.GetGroupMember(ctx, groupID, callerUserID)`). Nothing verifies the bill actually belongs to it. `GetBillByID(billID, groupID)` is called at line 110 with the error deliberately discarded (`bill, _ :=`), so a mismatch just yields an empty snapshot — and the code then proceeds to `h.hub.Subscribe(billID)`. The safe path (resolving the group from the bill at line 82) only runs when `group_id` is omitted.
**Why it matters**: Any authenticated user who is an active member of *any* group can pass their own `group_id` plus an arbitrary bill UUID and receive that bill's live `ocr.updated` events (job id, status, attempt state, cleaned error code, OCR warnings) for a group they do not belong to. It is a cross-tenant authorization hole on a stream; the leaked payload is small today but grows with every field added to a broadcast.
**Suggested fix**: Always resolve the bill first via `GetBillOnlyByID`, and if a `group_id` was supplied, reject with `404 BILL_NOT_FOUND` unless it equals `bill.GroupID`. Check membership only against the bill's own group, and treat the `GetBillByID` error as fatal rather than discarding it.

### 🟠 An OCR job can be stranded in `processing` forever, permanently blocking that bill, `internal/modules/bill/jobs/ocr_worker.go:154-256`, `internal/modules/bill/jobs/retention.go:20`
**Problem**: `UpdateOCRJobProcessing` flips the row to `processing` before the provider call. Several paths return an error to River instead of calling `failJob`: `get bill by id` (non-not-found), `update ocr job success`, and any process kill or lost lease. When River exhausts `MaxAttempts` on those paths it discards its own job, but `ocr_jobs.status` stays `processing`. Nothing ever reaps it — `RegisterRetentionJobs` only registers raw-response and idempotency cleanup.
**Why it matters**: `uq_ocr_jobs_active_bill` and `GetActiveOCRJobByBillID` both treat `processing` as active, so `RetryOCR` returns `409 OCR_ALREADY_RUNNING` for that bill forever, and the bill detail/SSE reports a job that will never complete. The user's only recovery is deleting the draft and re-uploading all receipts. This directly contradicts AC-3's promise that the draft "is left available for manual entry when all retries fail".
**Suggested fix**: Add a periodic reaper that marks `processing` jobs whose `updated_at` is older than a bounded lease (e.g. a few multiples of `BILL_OCR_PROVIDER_TIMEOUT`) as `failed` with a cleaned code, and/or use River's `Timeout`/final-error hook so the last attempt always lands in `failJob`.

### 🟠 Image and storage failures return `500 INTERNAL_ERROR` instead of the specified error codes, `internal/modules/bill/usecase/service.go:209-218`, `internal/modules/bill/delivery/http/handler.go:728-737`
**Problem**: `CreateBill` wraps processor and upload failures in bare `fmt.Errorf("process image %d: %w", ...)` / `("upload image %d to storage: %w", ...)` with no domain sentinel. `writeDomainError` therefore falls through to `default`, logs `event=bill_internal_error` and returns `500 INTERNAL_ERROR`. `ReceiptProcessor.IsUnsupported` exists on the interface (`service.go:38-41`) and is implemented (`processor.go:54`) but is never called anywhere.
**Why it matters**: AC-1 and the API surface table specify `400 INVALID_IMAGE` for a bad photo and `503 STORAGE_UNAVAILABLE` for Cloudinary. Today a user photographing a receipt in an unsupported format gets an opaque 500, clients cannot distinguish "your photo is bad" from "our server is broken", and every ordinary user error pollutes the internal-error log channel used for alerting.
**Suggested fix**: Map `receipt.ErrUnsupportedFormat` / `receipt.ErrInvalidImage` (via the already-present `IsUnsupported`) to a `domain.ErrInvalidImage` sentinel handled as `400 INVALID_IMAGE`, and wrap storage failures in `domain.ErrStorageUnavailable` handled as `503`. Delete `IsUnsupported` if you choose a different mechanism — dead interface methods invite the next reader to assume the check happens.

### 🟠 Provider timeout budget covers the whole upload + create + poll cycle, `internal/platform/ocr/llamaextract/client.go:36-104`, `:242-313`
**Problem**: `c.timeout` (default 8 s, from `BILL_OCR_PROVIDER_TIMEOUT_SECONDS`) is applied both as the `http.Client.Timeout` for each individual call *and* as the deadline for `reqCtx`, which spans `uploadFile` → `createExtractionJob` → `pollExtractionResult`. The poller ticks every 400 ms inside that same 8 s window, so the entire asynchronous extraction must finish within roughly 8 seconds or the call returns `ErrOcrTimeout`.
**Why it matters**: A document-extraction job that includes an image upload realistically takes longer than 8 s end to end. With the documented default, OCR would burn all three River attempts on timeouts and land in `failed` for most real receipts, while the spec's own AC-14 budgets 10 s from worker start to committed success. The worker's fallback default (20 s, `ocr_worker.go:108-110`) hints the author already sensed the mismatch, but config always supplies a value so the fallback never applies.
**Suggested fix**: Separate the two budgets — keep `BILL_OCR_PROVIDER_TIMEOUT` as the per-HTTP-request timeout, and give the overall extraction (poll loop included) its own larger deadline derived from the worker's job timeout. Verify against the real provider before merge; `client_integration_test.go` is the right place to prove it.

### 🟠 Orphaned Cloudinary uploads are cleaned up best-effort, not durably, `internal/modules/bill/usecase/service.go:196-204`
**Problem**: The rollback path is a `defer` that calls `s.storage.Delete(context.Background(), key)` and discards the error. If the delete call itself fails (Cloudinary down — which is exactly the situation that made the create fail), or the process dies between upload and commit, the assets are orphaned with no record anywhere. `EnqueueMediaCleanup` and the `media_cleanup_jobs` table exist and are used correctly by `DeleteDraftBill` (`repository.go:939-944`), but not here.
**Why it matters**: AC-13 requires partial uploads and failed bill transactions to be recoverable through the reserved operation prefix, and the spec dedicates critical test scenario 8 to exactly this case. As written, private receipt images can be retained indefinitely with no pointer to them — a data-retention problem, not just a storage-cost one.
**Suggested fix**: On the failure path, write the object keys (or the `bills/{operation_id}/` prefix) into `media_cleanup_jobs` through the existing durable flow instead of, or in addition to, the inline delete. That also lets `DeleteByPrefix` clean up uploads that never produced a `bill_images` row.

## Minor

### 🟡 Manual OCR retry limit is not enforced when the count query fails, and counts the automatic job, `internal/modules/bill/usecase/service.go:471-474`, `queries/bill.sql:310-312`
`attempts, err := ...; if err == nil && int(attempts) >= limit` silently skips the limit on any DB error — the failure-open direction on a rate limit that guards a paid provider. Separately, `CountManualOCRAttemptsInWindow` counts every `ocr_jobs` row for the bill, including the one auto-created at bill creation, while value sourcing says "the manual limit counts user initiated `ocr_jobs` rows". Users effectively get 4 manual retries, not 5. Also `activeJob, _ := s.repo.GetActiveOCRJobByBillID(...)` (line 457) discards a real DB error and treats it as "no job running".

### 🟡 `getWeight` mixes two weight scales, `internal/modules/bill/usecase/allocation.go:49-61`
The comment claims every branch is on `weightScale` (1e8), but every production caller goes through `parseWeightToScaledInt` (`service.go:1023-1033`), which scales by 1e4. If a single assignment ever arrives with `Weight == 0` and `Ratio == 0`, it falls back to 1e8 and receives 10 000× the weight of its siblings on the same item. Unreachable today only by accident; `CalculateFloorAllocation` is exported and the two scales will collide the first time someone calls it directly. Make the fallback relative to the other weights on the item, or normalize on one constant.

### 🟡 Multi-page stitching failures silently drop pages 2..n, `internal/modules/bill/jobs/ocr_worker.go:200-206`
`if err == nil && len(stitched) > 0` — if any page fails to decode, `imageBytes` stays as page 1 and OCR runs on a fraction of the receipt with no warning on the candidate and nothing in the log. A candidate that silently represents one page of five is worse than a failed job. Either fail the job with a cleaned code or attach a warning code to the candidate.

### 🟡 `bill.updated` SSE event is never emitted, `internal/modules/bill/delivery/http/sse_hub.go`, `internal/modules/bill/usecase/service.go`
The spec's SSE contract is `snapshot`, `ocr.updated`, `bill.updated`, `heartbeat`. `Broadcast` is only ever called from the OCR worker; a grep for `bill.updated` across `internal/` finds nothing. Clients watching a bill someone else is editing learn nothing until they reconnect and get a fresh snapshot.

### 🟡 Multipart idempotency hash omits the images and the items, `internal/modules/bill/delivery/http/handler.go:161-166`
The canonical request hash for a multipart create is `{group_id, merchant, items_count, files_count}`. Value sourcing requires "normalized metadata plus SHA 256 hashes of original bytes in zero based order". Two genuinely different receipt sets with the same merchant and counts hash identically, so a client bug that reuses a key returns a stale 202 for the wrong bill instead of `409 IDEMPOTENCY_KEY_REUSED`.

### 🟡 Client-supplied item IDs are discarded on draft replace, `internal/modules/bill/usecase/service.go:643`
`UpdateDraftBill` mints a fresh `uuid.NewV7()` for every item on every save, while the API surface specifies "complete items with client UUIDs". Every edit therefore invalidates any client-side reference to an item (and rewrites all assignment rows), which will bite as soon as the app tries to preserve per-item UI state or diff a draft.

### 🟡 Creditor's active membership is not re-checked at finalize, `internal/modules/bill/usecase/service.go:806-830`
`activeSet` is only used to validate assignment members; the Creditor is added unconditionally by `CalculateFloorAllocation`. `GetGroupMemberUser` joins `group_members` without filtering `status = 'active'`. AC-6 states allocation "requires a Creditor who is an active group member". As written, a bill whose Creditor has left the group can still be finalized, creating `awaiting` debts payable to a departed member.

### 🟡 OCR success metric is recorded before the DB write commits, `internal/modules/bill/jobs/ocr_worker.go:237-245`
`RecordOCRJob("succeeded", "none")` fires before `UpdateOCRJobSuccess`. If that write fails the worker returns an error and River retries, so one logical job can increment the success counter several times while never reaching `succeeded` in the database. Move the counter after the successful commit.

### 🟡 `bill_images` hard-codes the max count that config makes tunable, `db/migrations/000006_bill_and_ocr_v1.sql:63`
`CHECK (position >= 0 AND position < 5)` versus `BILL_IMAGE_MAX_COUNT` (`handler.go:50-57`). Raising the env var above 5 turns a legitimate upload into a constraint violation surfaced as a 500 after the images are already in Cloudinary. Either derive the check from the same limit or document that 5 is fixed by the schema.

### 🟡 Raw provider response bodies end up inside error strings that get logged, `internal/platform/ocr/llamaextract/client.go:161`, `:219`, `:270`, `internal/modules/bill/jobs/ocr_worker.go:191`, `:220`, `:227`
`fmt.Errorf("poll failed with status %d: %s", resp.StatusCode, string(respBytes))` embeds the entire provider payload, and the worker logs those errors verbatim with `err=%v`. On a non-2xx response that still carries partial extraction output, receipt text lands in application logs. The security model says raw provider content never appears in logs, and AC-14 asks for redaction tests. Log the cleaned code (which `classifyProviderError` already produces) and keep the body out of the error string.

### 🟡 Group activity descriptions carry merchant name and the raw void reason, `internal/modules/bill/repository/postgres/repository.go:212-222`, `:899`
`created bill draft %q` embeds the merchant name (receipt text) and `voided bill: %s` embeds the user-supplied reason verbatim. Value sourcing specifies activity metadata is identifiers, amounts, counts and a *cleaned* reason. The `metadata` JSON is correctly redacted; only `description` leaks.

### 🟡 Version mismatch on an already-reviewed bill reports `BILL_IMMUTABLE`, `internal/modules/bill/usecase/service.go:725-739`
When status is `reviewed` and `bill.Version != expectedVersion`, the branch falls through to `ErrBillImmutable` → `409 BILL_IMMUTABLE`, which tells the client the bill can never be changed. The truthful answer is `409 VERSION_CONFLICT` (refetch and retry).

### 🟡 Spec `index.md` no longer describes what was built
`docs/specs/0003-bill-ocr-v1/index.md` still specifies routes under `/api/v1/groups/{groupId}/bills` with `/ocr-jobs` and `/ocr-jobs/{jobId}/apply`, `share_ratio numeric(9,8)` summing to `1.00000000`, and a three-value `bill_status`. The implementation ships `/api/v1/bills?group_id=`, `/ocr-retry`, `/apply-candidate`, `weight numeric(10,4)`, and a fourth `reviewed` status. `docs/openapi.yaml` matches the implementation, so the contract itself is documented — but the governing spec now contradicts both, and follow-up items 5 and 6 show the drift is already known. Worth a `/sync` pass so the next reader is not misled about which document is authoritative.

## Nits

- ⚪ `internal/platform/ocr/llamaextract/normalizer.go:411-419`, `isPromotionMarker` matches the bare substring `km` anywhere in a folded item name; a genuine item whose name happens to contain those letters is silently converted into a discount on the preceding line.
- ⚪ `internal/platform/ocr/llamaextract/normalizer.go:132`, duplicate `OCR_ITEM_DISCOUNT_EXCEEDED` warnings accumulate one per offending line; only deduplicated later by `mergeMismatchCodes`.
- ⚪ `internal/modules/bill/usecase/service.go:167-171`, `5` and `100` are hard-coded here while the handler takes the same limits from config; the service should receive them too so there is one source of truth.
- ⚪ `internal/modules/bill/repository/postgres/repository.go:309`, `:621`, `:1237`, decoding enum columns with `fmt.Sprintf("%v", ...)` works but silently produces garbage strings if the sqlc type ever changes; a typed conversion would fail loudly instead.
- ⚪ `internal/modules/bill/usecase/service.go:1043-1047`, `computeLineTotal` goes through `float64`, so quantities/prices near `MaxInt64` round rather than erroring; harmless for VND receipts, inconsistent with invariant 1 ("every multiplication checks overflow").

## Strengths

- `CalculateFloorAllocation` is the best code in the change: the two-pass structure (everyone else floors independently, Creditor takes the remainder by subtraction) makes "shares sum to the bill total" true *by construction* rather than by rounding luck, the closing invariant check fails loudly instead of silently minting a debt, and `ErrDiscountNotAllocatable` correctly rejects rather than clamps. `TestFloorAllocation_BruteForce_Invariants` backs it properly.
- The item-discount model is consistent across all four layers: `final_price = line_total - discount_amount` and `discount = total_item_discount + general_discount` are enforced by DB CHECK constraints, computed in the usecase, folded by the normalizer, and consumed by `toAllocationInput` — and the algebra genuinely reduces back to `subtotal + service + vat - discount`.
- Optimistic locking lives in SQL (`WHERE status IN (...) AND version = $n`), not just in Go, so the unlocked read in the service layer cannot produce a lost update; `VoidBill` additionally locks the bill row then debt rows in UUID order, matching the documented lock hierarchy.
- `evaluateAllocation` collapsing read, review and finalize onto one reconciliation function is exactly the right call — it makes "no surprises at finalize" a structural property.
- Test coverage is genuinely strong for a change this size: ~130 tests including brute-force allocation invariants, metric assertions, idempotency reclaim against a live database, and regression tests that name the bug they lock down.

## Test coverage

Coverage is well above what this repo's other modules carry. The allocation engine, reconciliation blockers, item-discount round trips, idempotency reserve/reclaim/purge, retention job registration, the OCR worker's success/failure/retry branches, and the multipart size limits are all exercised, with `TEST_DATABASE_URL`-gated integration tests for the real PostgreSQL paths.

The untested branches are exactly where the findings above sit:

- No test asserts the SSE handler binds the bill to the group — `TestStreamBillEvents_Snapshot` uses the happy path only. A cross-group case would have caught the Major.
- No test covers an `ocr_jobs` row left in `processing` after River gives up, nor any recovery from it.
- No test covers `CreateBill` when `processor.Process` or `storage.Upload` fails, so the 500-instead-of-400 mapping is invisible.
- No test covers the failed-upload cleanup path (spec critical scenario 8) — the `defer` block has no assertion behind it.
- Spec critical scenario 5 (concurrent edit vs finalize, exactly one winner, no partial rows) has no integration test; the SQL looks correct, but the claim is unproven.
- `stitchReceiptImages` is tested for success (`TestOCRWorker_MultipleImagesStitched_Success`) but not for the partial-decode fallback that silently drops pages.
