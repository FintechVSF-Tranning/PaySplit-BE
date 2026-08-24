# Review, split-settlement-v1 feature slice round 4 (f43f7ac^ → working tree), 2026-08-22

**Reviewed by**: ox-alpha (author on a different model)
**Scope**: ~35 hand-written files, feature-slice diff `git diff f43f7ac^` (committed work in f43f7ac..2e87f2e plus all uncommitted working-tree fixes; generated `*/sqlc/*`, docs-only changes, and the untracked out-of-scope migration 000010 excluded from the counted scope)
**Verdict**: Approve with nits

## Summary

Fourth pass over split and settlement v1, after a fix pass aimed squarely at the round-three major and the carried minors. The fix pass is real: the settlement integration suite now skips unless `TEST_DATABASE_URL` is set (verified live — tests skip without it and pass against the isolated `paysplit_test` database), debt-scan loops check `rows.Err()`, the six new indexes build `CONCURRENTLY`, the cleanup worker no longer strands rows past ten attempts (capped backoff plus a failure metric, pinned by a new integration test), VietQR rejects TLV values over 99 bytes instead of corrupting them, OpenAPI documents the 422/404 surface with concrete schemas, HEIC detection sniffs the ISOBMFF brand directly, and the hand-written queries now name `awaiting`/`pending_confirmation` explicitly with zero drift against the regenerated sqlc output and migration 000009. No blockers or majors remain. What is left are two carried minors — the hardcoded `Retry-After` literal and the slice's schema coupling to the untracked migration 000010 for `media_cleanup_jobs.reason` — plus nits.

## Disposition of prior findings (rounds 1–3)

Every distinct finding from the three previous reviews, judged against current code:

| Prior finding | Status |
|---|---|
| 🔴 R1: Migration 000009 drops `media_cleanup_jobs` owned by 000001 | **Fixed.** The committed version had re-added the table block; the working tree deletes it entirely (`db/migrations/000009_split_settlement_v1.sql`). Up/Down touch only objects 000009 owns; Down restores the pre-feature view definition correctly. |
| 🔴 R1: Concurrent proofs under one key share an object key; loser deletes winner's image | **Fixed.** In-progress guard returns `ErrIdempotencyInProgress` (`repository.go:602-612`); per-attempt operation-scoped keys; post-upload DB failure rotates the operation ID (`service.go:168`, `repository.go:659-680`) so a compensated key can never be resurrected; concurrent double-resume closed via the idempotency-row `FOR UPDATE`. Pinned by unit and integration tests. |
| 🟠 R1: AC-11 in-progress 409 unreachable for proof | **Fixed** (same branch as above), extended by the abandoned-attempt resume path. |
| 🟠 R1: Stored `response_code` never replayed | **Fixed.** `beginIdempotency` returns the stored code, `CreatePayment` maps it to `created` (`repository.go:338-343`), handler emits 201/200 accordingly (`handler.go:114-117`). |
| 🟠 R1: `PAYMENT_REMINDER_MAX_COUNT != 3` fails startup | **Fixed.** Range validation 1–3 with named messages (`config.go`), value threaded service → repository → workers, unit-tested. |
| 🟠 R1: Usecase imports `net/http` | **Fixed.** No transport imports anywhere in `usecase`; sniffing lives behind `DetectProofContentType` (pure) called from the handler. |
| 🟠 R1: Any DB error loading creditor bank profile becomes 422 | **Fixed.** All sites scan into `pgtype.Text`, distinguishing NULL/no-rows from wrapped internal errors (`repository.go:356-364, 527-535, 639-648, 938-947`). |
| 🟠 R1 → 🟠 R2 → 🟡 R3: Proof images force-converted (JPEG, then WebP) | **Partially fixed.** Proof has a dedicated adapter, upload and signed-URL formats agree, the decision is documented in the module README, and an end-to-end test asserts WebP output through the signed URL. The adapter still discards the source format unconditionally, and the README/verify claim of "lossless" is inaccurate (see Nits). |
| 🟠 R1: Notification payloads carry no identifiers | **Fixed.** Every call site passes `group_id` + `payment_id`/`debt_id` through `BeforeCommit` into the stored payload (`repository.go:460, 967, 1120, 1233, 1291, 1340`); unit and integration tests pin it. |
| 🟡 R1: QR canonical hash over un-normalized debt IDs | **Fixed.** IDs normalized via `uuid.Parse(...).String()` before sort/hash (`service.go:90-104`), tested with uppercase variants. |
| 🟡 R1/R2/R3: `Retry-After` hardcoded to 1 | **Still open.** `handler.go:288`. |
| 🟡 R1: 10 MB limit duplicated as handler literal | **Fixed.** Handler receives the configured maximum via constructor and panics on non-positive values (`handler.go:27-37`); usecase still enforces its own bound. |
| 🟡 R1/R2: `uuid.MustParse` on request-supplied/DB-derived values | **Fixed.** Replaced by `storedUUID` returning wrapped errors (`repository.go:287-293`); remaining `uuid.Must` calls are only fresh `NewV7()` generation. |
| 🟡 R1: `covered_debt_ids` serializes as null | **Fixed.** Initialized to `[]string{}` at the boundary (`response.go:116-119`). |
| 🟡 R1/R2: `item_subtotal` string round-trip in response layer | **Fixed.** int64 accumulation keyed by bill, formatted once (`response.go:43, 59-60`). |
| 🟡 R1: Over-long proof note silently truncated | **Fixed.** Reads to 2001 bytes, rejects >2000 bytes or invalid UTF-8 explicitly (`handler.go:165-171`); a valid ≤500-rune note can never falsely reject. |
| 🟡 R1: `QueueMediaCleanup` discards its reason | **Fixed** as written — but see Minor 2 for the column-provenance problem it introduced. |
| ⚪ R1: VietQR `%02d` length overflow | **Fixed.** `tlvFields` errors above 99 bytes (`generator.go:68-70`) with a dedicated test. |
| ⚪ R1/R3: `NOT IN ('settled','voided')` predicates | **Fixed.** Queries and the view migration now name `awaiting` + `pending_confirmation` explicitly. |
| ⚪ R1/R3: Matrix `debt_count` sums both directions while amount nets | **Still open.** `queries/settlement.sql:98`. |
| ⚪ R1/R3: Automated reminders hold one transaction across up to 100 debts | **Still open**, unchanged, acceptable at current scale. |
| ⚪ R1: Dead generated queries (`GetPaymentRow` etc.) | **Fixed.** Removed from `queries/settlement.sql`; sqlc regenerated without them. |
| 🟡 R2: Failed proof burns its idempotency key until expiry | **Fixed.** `ResetProofAttempt` lease marker plus resume-with-same-operation (upload failures) or rotation (post-upload failures); integration test walks reset → resume → claim → 409. |
| 🟡 R2: QR creation checks bank before idempotency replay | **Fixed.** `beginIdempotency` precedes the bank lookup (`repository.go:338` vs `:354`). |
| 🟡 R2/R3: Slice depends on out-of-scope migration 000010 for `reason` | **Partially fixed / effectively still open.** See Minor 2. |
| 🟡 R2/R3: OpenAPI omits 422s/proof 404 and uses bare list schemas | **Fixed.** All seven routes documented with concrete `Payment`/`ExpenseBill`/`Debt`/matrix schemas, shared `UnprocessableEntity` response, proof 404/409/503 present; reference-code pattern matches the generator alphabet. |
| 🟡 R2/R3: HEIC depends on client-declared content type | **Fixed.** `DetectProofContentType` sniffs the `ftyp` brand list including `mif1`/`msf1` (`service.go:197-220`); missing/octet-stream declarations fall back to detection; MP4 rejected with a test. |
| 🟠 R3: Integration tests fall back to `DATABASE_URL` and run global workers against any configured DB | **Fixed.** `settlementTestPool` loads `.env` but reads only `TEST_DATABASE_URL` and skips when absent (`repository_integration_test.go:24-27`); no `DATABASE_URL` fallback. Verified: suite skips without the variable and passes against the isolated test database with it. |
| 🟡 R3: Debt-scan loops drop iteration errors | **Fixed.** `rows.Err()` checked after every scan loop (`repository.go:395, 749, 911, 1054, 1276, 1321, 1388`). |
| 🟡 R3: Six indexes build non-concurrently | **Fixed.** All six plus `uq_payments_pending_proof_pair` now build `CONCURRENTLY` under the file's existing `NO TRANSACTION`; Down drops plainly. |
| 🟡 R3: Cleanup jobs stranded forever at 10 attempts | **Fixed.** Claim query no longer filters on the attempt ceiling; increments cap at the CHECK-constrained 10, backoff caps at 2⁸×5 min, failures record `last_error_code` and invoke the `RecordMediaCleanupFailure` metric (`repository.go:1359-1404`, wired at `workers.go:58`). Pinned by `TestSettlementMediaCleanupRetriesPastAttemptLimitAndRecordsFailure`. |
| ⚪ R3: Config validation message unnamed | **Fixed.** Each settlement variable has its own named error; tested. |
| ⚪ R3: Settlement validation gated on `VietQRServiceBaseURL != ""` | **Fixed.** Validation is unconditional. |
| ⚪ R3: Oversized image rejected as `VALIDATION_FAILED` vs `INVALID_IMAGE` inconsistency | **Fixed.** Oversized image now maps to `INVALID_IMAGE` (`handler.go:180-183`). |

## Minor

### 🟡 `media_cleanup_jobs.reason` only exists via the out-of-scope migration 000010; committed generated code already depends on it, `internal/modules/settlement/repository/postgres/repository.go:986`
**Problem**: Repeat of rounds two and three, partially fixed. `QueueMediaCleanup` persists `reason`, a column added only by the untracked migration 000010 that this review is told belongs to another feature; 000009 does not create it. The committed sqlc models across *all five* modules include `MediaCleanupJob.Reason`, so they were generated against a migration set containing an untracked file — a fresh checkout without 000010 regenerates different output, and on any database migrated only through tracked migrations every compensation enqueue fails with "column reason does not exist". The error is at least surfaced now (`errors.Join` in `usecase/service.go:166-169`) instead of swallowed, but the durable-cleanup guarantee of AC-6 then silently degrades on those environments, and 000001's `idx_media_cleanup_jobs_due` no longer matches the new claim predicate either.
**Why it matters**: This slice cannot deploy independently of the other feature despite being scoped as self-contained; deploy ordering is implicit and the failure mode hides inside a joined error.
**Suggested fix**: Land the column addition within this slice's own migration story (a one-line `ALTER TABLE ... ADD COLUMN IF NOT EXISTS reason TEXT NOT NULL DEFAULT ''` would do), or document the hard sequencing dependency in the spec's data-model section, and regenerate sqlc only from tracked migrations.

### 🟡 `Retry-After` is still a hardcoded literal, `internal/modules/settlement/delivery/http/handler.go:288`
**Problem**: Fourth consecutive round. `payment_idempotency_keys.retry_after` is now genuinely written (as the abandoned-attempt lease marker) and read (as the resumability flag), but the HTTP header remains the constant `"1"`.
**Why it matters**: With the resume design, a 409 means "first attempt still in flight"; a computed hint would be trivial, and the fixed second invites tight retry loops. Low urgency because the in-progress window is short-lived by construction.
**Suggested fix**: Set the header from elapsed time or a per-operation hint persisted alongside the idempotency row instead of the literal.

## Nits

- ⚪ `internal/modules/settlement/README.md` and `docs/specs/0004-split-settlement-v1/verify.md` describe the WebP conversion as "lossless q_100"; `q_100` is maximum-quality lossy encoding, not lossless (`webp:lossless` would be). The evidence claim should match what the adapter does.
- ⚪ `internal/modules/settlement/delivery/http/handler.go:158`, the declared multipart `Content-Type` is captured and then unconditionally overwritten by detection at line 189 — dead assignment, and `validProofImage`'s declared-vs-detected equality branch is unreachable given the caller always passes the detected value. Simplify to detection-only.
- ⚪ `internal/modules/settlement/repository/repository.go:36`, `CreatePaymentInput.QRPayload` is never read anywhere.
- ⚪ `internal/modules/settlement/repository/postgres/repository.go:1401`, the full `err.Error()` text is stored into `last_error_code`, a column named for codes; fine functionally, misleading semantically.
- ⚪ `queries/settlement.sql:98`, matrix `debt_count` sums both directions while `total_amount` nets, so count and amount describe different populations. Repeat nit; worth a confirming comment.
- ⚪ `repository.go:1252-1297`, automated reminders hold one transaction across up to 100 debts spanning many groups. Fine today; repeat nit.
- ⚪ Notification payloads omit `amount`, which the activity metadata carries; adding it would let clients render richer bodies without another payload contract change. Repeat nit.
- ⚪ `repository.go:1215` compares `last_reminded_at` against app-server `time.Now()` while the automated path compares inside SQL against `now()` (`repository.go:1258`) — mixed clocks on one rate limit. Harmless skew today.

## Strengths

- The round-three major was fixed exactly as prescribed and verified honestly: the guard skips cleanly without `TEST_DATABASE_URL`, keeps the `.env` load for convenience, and the suite demonstrably passes against the isolated database rather than merely compiling.
- The idempotency-resume machinery matured well: prepare/submit/reset separation, operation rotation on post-upload failure versus retention on upload failure, and the `FOR UPDATE`-closed double-resume race together satisfy invariant 16 with no cross-attempt deletion hazard — all pinned by tests at both layers.
- Query/code/migration consistency is now exact: hand-written `settlement.sql` matches the regenerated sqlc byte-for-byte in intent, dead queries are gone, predicates match AC-1's statuses, and migration 000009 owns precisely its own objects with correctly `CONCURRENTLY`-built indexes.
- Lock discipline holds across module boundaries: bill void joins group → debts → payments ordering with an explanatory comment, superseding pending QR intents before voiding debts, matching spec invariant 13.
- Money handling stays int64 internally and formatted strings at the boundary everywhere, the golden VietQR test pins full TLV+CRC16 bytes, and the oversized-TLV corruption path is now an error instead of silent payload damage.

## Test coverage

Unit coverage (always gating): canonical hash normalization including uppercase sets, duplicate/empty/non-UUID rejection, JPEG/PNG/HEIC magic-byte acceptance and MP4 rejection, note bounds and trimming, replay-without-upload, in-progress producing zero storage mutations, release-on-upload-failure preserving the operation ID, compensation queuing the exact key with rotation, configured reminder maximum threading through service and workers, worker fail-fast ordering and expiry-before-cleanup sequencing, notifier identifier preservation and rollback, response grouping/superseded hiding/error mapping including `Retry-After` presence, golden VietQR bytes plus oversize rejection, metrics counters, and config defaults/range/named-error acceptance.

Integration coverage (gated strictly on `TEST_DATABASE_URL`): full lifecycle create → prepare → in-progress 409 → reset → resume → submit → confirm with payload assertions, bank-profile removal/replay interplay, conflict and completed-replay cases, operation rotation, rejection resetting debts, eight-way reminder race with exactly one success, automated/stalled double-run claims, expired-key deletion, cleanup queue→process round trip with persisted reason, retry-past-attempt-limit with failure recording, constraint rejection probes, and outsider/inactive denial. Cloudinary conversion is verified end to end through the signed download URL.

Remaining gaps (unchanged risk assessment, none blocking): no concurrency test for proof vs. bill void in both winning orders (spec critical scenario 10); `CreatePayment`'s supersession/exact-set branch untested; multi-bill expense pagination boundaries and multipart handler edge cases (duplicate fields, oversized bodies) untested.
