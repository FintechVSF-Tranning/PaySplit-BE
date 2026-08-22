# Review, split-settlement-v1 feature slice (f43f7ac^ → working tree), 2026-08-22

**Reviewed by**: ox-alpha (author on a different model)
**Scope**: 31 hand-written files in the reviewed path set, feature-slice diff `git diff f43f7ac^` (commit f43f7ac plus the uncommitted working-tree fixes on top; generated `*/sqlc/*`, docs, and the untracked out-of-scope migration 000010 excluded from the counted scope)
**Verdict**: Changes requested

## Summary

This lands spec 0004 end to end: migration 000009, a new `settlement` module (domain → usecase → repository/postgres → delivery/http), a golden-tested local NAPAS VietQR encoder, River workers, metrics, OpenAPI updates, and bill-void supersession wired through the same group lock. The uncommitted working-tree changes genuinely fix both prior blockers and five of seven prior majors: the migration no longer touches `media_cleanup_jobs`, concurrent proof attempts can no longer share an object key, idempotency replays carry their original 201/200, DB faults are no longer reported as 422, and the reminder cap is configurable. Two majors from the previous review are untouched — proof images are still force-converted to JPEG by the shared Cloudinary adapter, and notification payloads still carry no identifiers — so the change is not mergeable as-is.

## Disposition of the previous review's blockers and majors

| Previous finding | Status |
|---|---|
| 🔴 Migration 000009 drops `media_cleanup_jobs` owned by 000001 | **Fixed.** The `CREATE TABLE`/index block and the `DROP TABLE` are gone (`db/migrations/000009_split_settlement_v1.sql`); Down now only drops objects 000009 owns. See Minor 7 for a new coupling introduced by the fix. |
| 🔴 Concurrent proofs under one key share an object key; loser deletes winner's image | **Fixed.** `PrepareProof` now returns `ErrIdempotencyInProgress` for an existing in-progress record (`repository.go:576-578`), so a second attempt never uploads; each attempt keeps its own operation-scoped key, and compensation deletes only its own object. Pinned by unit test `TestSubmitProof_AC6AndAC11InProgressDoesNotUploadOrDelete` and an integration assertion. Introduces Minor 4 below. |
| 🟠 AC-11 `409 IDEMPOTENCY_IN_PROGRESS` unreachable for proof | **Fixed** (same branch as above). |
| 🟠 Stored `response_code` written but never replayed | **Fixed.** `beginIdempotency` returns the stored code, `CreatePayment` maps it to the `created` flag, handler emits 201/200 accordingly (`repository.go:345-351`, `handler.go:114-117`). Integration test pins replayed `created=true`. Confirm/reject/remind discard the code, but those operations are always 200, so nothing is lost. |
| 🟠 `PAYMENT_REMINDER_MAX_COUNT != 3` fails startup | **Fixed.** Validation is now a 1–3 range (`config.go:493-496`), the configured value flows service → `RemindInput.MaxCount` → repository and into the workers. The DB constraint still hardcodes 0–3, but config can no longer disagree with it. |
| 🟠 Usecase imports `net/http` | **Fixed.** Content-type sniffing moved to the handler (`handler.go:185-187`); usecase has no transport imports. |
| 🟠 Any DB error loading creditor bank profile becomes `422 BANK_ACCOUNT_REQUIRED` | **Fixed.** All four sites scan into `pgtype.Text` and distinguish `ErrNoRows`/NULL (422) from real failures (wrapped 500s): `repository.go:332-343, 501-511, 595-606, 862-874`. |
| 🟠 Proof images force-converted to JPEG | **Still open.** See Major 1. |
| 🟠 Notification payloads carry no identifiers | **Still open.** See Major 2. |

## Major

### 🟠 Proof images are force-converted to JPEG by the reused bill storage adapter, `internal/platform/storage/cloudinary/bill.go:62`
**Problem**: Settlement's `ProofStorage` is wired to `BillStorage` (`bootstrap/app.go:176`), whose `Upload` hardcodes `Format: "jpg"` (and whose `SignedURL` hardcodes `format=jpg`, `bill.go:91`). AC-6 accepts JPEG, PNG, and HEIC, and the validation layer happily accepts all three before upload.
**Why it matters**: PNG screenshots of banking apps — arguably the dominant proof format — are re-encoded lossily on a money-verification artifact, degrading exactly the text the creditor must read to confirm a transfer. The stored asset format also silently diverges from what the client sent.
**Suggested fix**: Give settlement its own storage adapter that preserves the source format (derive the extension from the validated content type), or parameterise `Upload`/`SignedURL` with the format and thread the validated content type from the usecase through `repository.SubmitProofInput`.

### 🟠 Notification payloads carry no identifiers, `internal/modules/settlement/usecase/service.go:43`
**Problem**: `s.notify` sends only `map[string]string{"type": kind}`; `Notifier.NotifyTx` persists exactly that map as the payload (`integration/notification.go:30-34`). Payment ID, debt ID, amount, and group are all available at every call site and are already written into the `group_activities` metadata, but dropped from notifications.
**Why it matters**: A push titled "Your payment was confirmed" gives the mobile client nothing to deep-link to, so AC-7/AC-8's "enqueues a settlement notification for the Payer" is satisfied only in the weakest sense. Adding identifiers later is a client-visible payload change, so it gets more expensive every release.
**Suggested fix**: Pass a per-operation payload (payment_id/debt_id/amount/group_id, mirroring the activity metadata) from each call site through `BeforeCommit` into `NotifyTx`.

## Minor

### 🟡 A failed proof submission burns its idempotency key until the 24-hour expiry, `internal/modules/settlement/repository/postgres/repository.go:551-579` with `usecase/service.go:146-165`
**Problem**: `PrepareProof` commits the `in_progress` row as soon as pre-checks pass, before the client uploads. If anything after that fails terminally (storage outage → `ErrStorageUnavailable`, or the locked recheck losing to a bill void → `ErrDebtsNotAwaiting`), the row stays `in_progress`; nothing ever marks it failed. Any retry with the same key now hits the new in-progress guard and receives `409 IDEMPOTENCY_IN_PROGRESS` until expiry, even though no operation is running.
**Why it matters**: The fix for the old blocker traded one problem for another: spec invariant 16 says the operation ID is "reused by its retries", but retries can no longer resume — `resumeProofIdempotency`'s in-progress branch is unreachable from a fresh request because `PrepareProof` refuses to hand back the stored operation ID. The client also cannot distinguish "wait and replay" from "this key is dead, use another", and `Retry-After: 1` actively suggests waiting when waiting will never succeed.
**Suggested fix**: On terminal failures (state conflicts, storage errors) transition the row to a failed state or delete it so the key is reusable; alternatively let `PrepareProof` return the stored operation ID once a lease/heartbeat shows the first attempt abandoned. At minimum document the burn-on-failure semantics in the module README.

### 🟡 QR creation checks creditor bank validity before the idempotency replay check, `internal/modules/settlement/repository/postgres/repository.go:332-351`
**Problem**: The bank-profile lookup and `422 BANK_ACCOUNT_REQUIRED` gate run before `beginIdempotency`, so an exact-key, exact-hash retry of a successful creation returns 422 instead of the stored response whenever the creditor's bank became invalid after the original call. AC-11 promises unconditional replay of the stored status/body for 24 hours.
**Why it matters**: A client retrying after a network timeout gets a *different* error than the original success and cannot recover its payment/reference code from the retry; AC-11 and AC-5 conflict here and the resolution is implicit. (Proof, confirm, and reject check idempotency first and replay correctly.)
**Suggested fix**: Move `beginIdempotency` ahead of the bank lookup in `CreatePayment` (as `finishPayment` already does) so completed records always replay, and keep the 422 for genuinely new creations.

### 🟡 QueueMediaCleanup now writes `reason`, a column created by the out-of-scope migration, `internal/modules/settlement/repository/postgres/repository.go:911-913`
**Problem**: The fixed `QueueMediaCleanup` persists its `reason` argument, and sqlc models were regenerated to include it — but `media_cleanup_jobs.reason` is added by the untracked migration 000010, which this review is told belongs to a different feature. On any database where that migration has not run, every cleanup enqueue fails with "column reason does not exist", and `service.go:161-162` discards that error, so durable proof cleanup silently stops working while the request succeeds.
**Why it matters**: This slice is not deployable independently of the other feature's migration despite being scoped as a self-contained slice; the failure mode is silent loss of the exact compensation path AC-6 relies on.
**Suggested fix**: Either land the column addition as part of this slice's own migration story (or explicitly sequence the dependency in docs), and stop swallowing the `QueueMediaCleanup` error at the call site — log it at minimum.

### 🟡 `Retry-After` is hardcoded to `1`, `internal/modules/settlement/delivery/http/handler.go:286`
**Problem**: The `payment_idempotency_keys.retry_after` column exists for this and is never written or read; the header is a constant.
**Why it matters**: Combined with Minor 1, a fixed 1-second hint invites tight retry loops against an operation that may be permanently stuck. Repeat finding from the previous review.
**Suggested fix**: Persist a per-operation hint on insert (or compute from elapsed time) and read it in `writeError`'s source path, instead of a literal.

### 🟡 `item_subtotal` money arithmetic happens in the HTTP response layer, `internal/modules/settlement/delivery/http/response.go:58-59`
**Problem**: The per-bill subtotal is accumulated by parsing the previously formatted string back to int64 on every item row. Financial aggregation belongs in the query or usecase; the string round-trip is needless work per row and an easy place to introduce a formatting bug. Repeat finding.
**Why it matters**: Spec invariant says `Σ item_share` must equal `allocation.item_subtotal`; computing that sum via formatted strings is fragile and untestable at the right layer.
**Suggested fix**: Accumulate int64 subtotals keyed by bill ID in the usecase (or select `SUM(item_share)` per bill in `ListExpenseRows`) and format once.

### 🟡 `uuid.MustParse` on DB-derived values in money paths, `internal/modules/settlement/repository/postgres/repository.go:491,586,596,864,998`
**Problem**: These parse values read back from `payments`/`group_members` rows in the same function; they are safe today only because of query guarantees, and any future schema/reordering change turns a malformed value into a panic. Repeat finding, unchanged.
**Why it matters**: A panic inside an open transaction aborts the request with a 500 and rolls back, but panics in request paths bypass the error mapping entirely.
**Suggested fix**: Keep the parsed UUIDs alongside the loaded entity (or parse once with error handling) instead of re-parsing strings mid-transaction.

### 🟡 OpenAPI contract omits the 422 responses the handlers really produce, `docs/openapi.yaml:1054-1113`
**Problem**: `POST /payments/qr`, `GET /payments/{paymentId}`, and `POST /payments/{paymentId}/proof` can all return `422 BANK_ACCOUNT_REQUIRED` (implemented and tested in `response_test.go`), but none documents a 422 response; the proof endpoint also omits its documented 404. Additionally `ExpensePage.bills` and `DebtPage.debts` are typed as bare `{type: object}` arrays, dropping the detailed `expense_bill`/`debt_item` field contracts the spec defines.
**Why it matters**: The FE generates expectations from this contract; undocumented 422s will surface as unhandled error branches in the app, and the loose schemas hide breaking changes.
**Suggested fix**: Add the 422 (and proof 404) responses referencing the shared error envelope, and spell out the item/allocation/debt object schemas.

### 🟡 HEIC proofs depend entirely on the client declaring a content type, `internal/modules/settlement/delivery/http/handler.go:185-187`
**Problem**: When a multipart part lacks `Content-Type`, the fallback sniffs with `http.DetectContentType`, which can never return `image/heic`/`image/heif` — such uploads become `application/octet-stream` and fail `validProofImage` as `INVALID_IMAGE`, despite AC-6 explicitly accepting HEIC.
**Why it matters**: iOS clients that omit the part header (not unusual for raw byte uploads) get a confusing rejection for a format the product supports.
**Suggested fix**: Sniff the ISOBMFF `ftyp` brand directly (the usecase validator already checks bytes 4–8) instead of relying on `http.DetectContentType` for the HEIC family.

## Nits

- ⚪ `internal/platform/vietqr/generator.go:47`, `tlv` formats length with `%02d`, which silently emits three digits and corrupts the payload above 99 bytes. All current values are short; a length guard returning an error would make that guarantee explicit. Repeat nit.
- ⚪ `repository/postgres/queries/settlement.sql:108-129`, `GetPaymentRow`, `ListPaymentDebtIDs`, `ListAutomatedReminderCandidates`, `ListStalledPaymentCandidates` remain generated-but-unused dead code. Repeat nit.
- ⚪ `queries/settlement.sql:98`, matrix `debt_count` sums both directions while `total_amount` is netted, so count and amount describe different populations. Repeat nit; worth a confirming comment.
- ⚪ `queries/settlement.sql:57-59,84-85,92`, predicates use `NOT IN ('settled','voided')` where AC-1 names `awaiting`+`pending_confirmation`; equivalent only via invariant 9. Repeat nit.
- ⚪ `repository.go:1170-1213`, `ProcessAutomatedReminders` holds one transaction across up to 100 debts spanning many groups with two statements per row. Fine at current scale; batch before it grows. Repeat nit.
- ⚪ `internal/config/config.go:496`, the validation failure message "settlement settings are invalid" still does not name the offending variable, which cost debugging time once already.

## Strengths

- The two previous blockers were fixed properly rather than patched around: the migration now only owns its own objects, and the in-progress idempotency guard kills the cross-attempt deletion hazard at its root — with regression tests pinning both (`TestSubmitProof_AC6AndAC11InProgressDoesNotUploadOrDelete`, integration replay/conflict/in-progress assertions).
- Lock discipline remains exemplary: group → debts ascending id → payment is applied identically in `CreatePayment`, `SubmitProof`, `finishPayment`, `RemindDebt`, and now bill void joins the same protocol via the group lock (`bill/repository/postgres/repository.go:809-813`) with an explanatory comment tying it to the settlement invariant.
- The integration suite grew the exact tests the previous review called missing: create replay + hash conflict, proof in-progress and completed replay, an eight-way reminder race asserting exactly one success, claim-once automated reminders and stalled alerts, expiry cleanup, media-cleanup queue *and* process, and constraint rejection probes.
- The VietQR golden test continues to pin the full TLV byte string including CRC16, and the rewritten Cloudinary signed-URL implementation is tested down to signature presence and expiry window.
- Money semantics stay string-typed at the boundary and int64 everywhere else; no floats touch amounts anywhere in the module.

## Test coverage

Unit coverage (always gating): debt-ID canonicalization including uppercase normalization, all three accepted image formats plus rejection cases, note bounds, in-progress proof doing zero storage mutations, configured reminder maximum threading, compensation queueing the exact object key when delete fails, reject-reason trimming/bounds, worker eligibility windows and fail-fast ordering, response grouping/superseded hiding/error mapping (including `Retry-After` presence), golden VietQR bytes, notifier rollback, metrics counters, and config acceptance of the widened range.

Integration coverage (gated on `TEST_DATABASE_URL`): full QR → proof → confirm lifecycle with settled-debt assertions, rejection resetting debts to awaiting with null `payment_id`, idempotency replay/conflict/in-progress/completed-replay, reminder race, automated/stalled worker double-run claims, expired-key deletion, media cleanup queue→process round trip, database constraint rejections, and outsider/inactive denial.

Remaining gaps, in rough risk order: no concurrency test for proof vs. bill void in both winning orders (spec critical scenario 10 — the void-side supersession SQL is untested anywhere); `CreatePayment`'s supersession/exact-set branch (`repository.go:386-413`) has no test; expenses pagination is untested beyond a single-bill page (multi-bill pages, cursor boundaries); the multipart handler parsing (note truncation edge, duplicate fields, oversized bodies) has no test. None of these block, but scenario 10 and the supersession branch sit directly on top of this slice's hardest logic and would be the first things to regress silently.
