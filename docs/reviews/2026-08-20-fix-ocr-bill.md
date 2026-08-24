# Review, fix/ocr-bill, 2026-08-20

**Reviewed by**: Claude Sonnet 5 (author on Claude Opus)
**Scope**: 44 files, branch vs `main` (scoped to bill + OCR v1: `internal/modules/bill`, `internal/platform/ocr`, `internal/platform/image/receipt`, `internal/platform/storage/cloudinary/bill*.go`, `db/migrations/000006_bill_and_ocr_v1.sql`)
**Verdict**: Blocked

## Summary

This is a substantial, mostly well-built implementation of bill creation, OCR ingestion via LlamaExtract, draft editing, review, Hamilton-method allocation, finalize, and void, with solid idempotency-key plumbing, image validation, and error redaction. The "not yet complete" commit (`9d80c4c`) closed real gaps (idempotency release-on-failure, size-limit enforcement before reading multipart bodies, internal-error redaction), and the follow-up commit (`a1b4368`) correctly replaced float-based Hamilton rounding with exact integer arithmetic. However, that same follow-up commit also deleted the old code's final reconciliation step that forced `sum(FinalAmount) == bill.Total`, and the exact-integer version can still produce a per-member negative share that gets clamped to zero — silently manufacturing money in the aggregate debt total. Separately, expired idempotency keys hit a code path that returns `(nil, nil)` and crashes the request with a nil pointer dereference, permanently poisoning that key. A third finding: the OCR raw-response retention worker is fully implemented but never registered with River, so the spec's mandated 30-day purge of raw OCR payloads never runs.

## Blockers

### 🔴 Hamilton allocation can silently overcharge members in aggregate when a share is clamped, `internal/modules/bill/usecase/allocation.go:217-220`

**Problem**: `FinalAmount = ItemSubtotal + ServiceChargeShare + VATShare - DiscountShare`, then clamped to `0` if negative (line 218-220). Service charge, VAT, and discount are each allocated by independent Hamilton runs proportional to `ItemSubtotal` (same weights, same tie-break), so in the continuous/ideal case every member's components cancel out to a non-negative `FinalAmount`. But each of the (up to) 4 independent Hamilton allocations (item, SC, VAT, discount) can round a *different* member up or down by ±1 unit. When a low-`ItemSubtotal` member's discount rounds up by 1 more than their SC/VAT rounds up, their `FinalAmount` goes negative and gets clamped to `0` — but nothing is subtracted from any other member to compensate. The result: `sum(FinalAmount)` across all members can **exceed** the real bill total (`subtotal + SC + VAT - discount`), and that surplus becomes debt that doesn't correspond to any real money.

I verified this is reachable with realistic finalize-time inputs (not just synthetic allocation-package calls) — reviewed and finalized bills must satisfy `bill.Total == bill.Subtotal + bill.ServiceCharge + bill.VAT - bill.Discount` and `bill.Discount <= bill.Subtotal + bill.ServiceCharge + bill.VAT` (`internal/modules/bill/usecase/service.go:719-723` and `:780-784`), both of which the failing case below satisfies:

```
2 members, item weights 1/1 (subtotal=2), ServiceCharge=1, VAT=1, Discount=4, Total=0
  (Discount(4) == Subtotal+SC+VAT(4), passes the "discount <= ..." check)
member A: item=1, sc=1, vat=1, disc=2 -> final = 1+1+1-2 = 1
member B: item=1, sc=0, vat=0, disc=2 -> final = 1+0+0-2 = -1 -> clamped to 0
sum(FinalAmount) = 1, but expected total = 0
```

I confirmed this with a brute-force search over the allocation function (base weights 1-6, SC/VAT 0-20, discount 0-40): the mismatch above was the first hit. The previous version of this file (before `a1b4368`) explicitly guarded against exactly this by forcibly adjusting the largest member's `FinalAmount`/`RoundingAdjustment` so `sum(FinalAmount) == in.Total` (see `git show a1b4368` diff, removed block starting `// Nếu có độ lệch do discount lớn hơn tổng thành phần...`). The "exact integer arithmetic" refactor removed that safety net without actually eliminating the underlying edge case it was compensating for — it only eliminated the *floating-point* imprecision, not the *cross-component clamping* imprecision.

**Why it matters**: `FinalizeBill` (`internal/modules/bill/usecase/service.go:824-855`) persists these `FinalAmount` values directly into immutable `bill_shares` rows and creates `debts` from them (`amount > 0` → real debt owed to the creditor). If the sum of shares exceeds the bill's actual total, the app is recording debts that in aggregate exceed what was actually paid/owed — a real, if usually small (1-3 VND), money-correctness bug in a bill-splitting app whose entire value proposition (per `docs/specs/0003-bill-ocr-v1/rationale.md`) is "Integer Hamilton calculation prevents money loss." It's also silent: no test in `allocation_test.go` currently exercises a case where SC/VAT/discount weights diverge enough to trigger the clamp with a positive contribution from another member (the existing `TestHamilton_LargeDiscount_NeverProducesNegativeFinalAmount` test happens not to hit it).

**Suggested fix**: Either (a) reinstate a bounded, deterministic reconciliation pass after computing all `FinalAmount`s — using the same UUID-ordered tie-break as the rest of the algorithm — that redistributes any clamped shortfall/surplus so `sum(FinalAmount) == totalItemSubtotal + SC + VAT - Discount` exactly, or (b) prove and test that clamping cannot happen for valid inputs (it can, per the reproduction above) and otherwise reject/flag such bills before finalize rather than silently manufacturing debt. Add a property-based or brute-force test asserting `sum(FinalAmount) == subtotal+SC+VAT-Discount` for all valid inputs, not just the three hand-picked cases currently in `allocation_test.go`.

### 🔴 Idempotency key reservation panics (nil pointer) and permanently poisons the key once it expires, `internal/modules/bill/repository/postgres/repository.go:1396-1404` and `internal/modules/bill/usecase/service.go:1061`

**Problem**: `ReserveIdempotencyKey` inserts with `ON CONFLICT (actor_user_id, operation, key_hash) DO NOTHING` (repository.go:1366). This conflicts on the **primary key alone**, with no regard for `expires_at`. So once any row exists for `(actor, operation, key_hash)` — even one that is long past its 24h `expires_at` — every future insert attempt for that exact key is a no-op forever. On conflict, the code falls into:

```go
if errors.Is(err, pgx.ErrNoRows) {
    existing, err := r.GetIdempotencyKey(ctx, p.ActorUserID, p.Operation, p.KeyHash)
    if err != nil { return nil, err }
    return existing, nil
}
```

`GetIdempotencyKey` filters `WHERE ... AND expires_at > now()` (repository.go:1447) and returns `(nil, nil)` when no row matches (repository.go:1460-1462) — which is exactly the case for an expired row. So `ReserveIdempotencyKey` returns `(nil, nil)`: no error, but a nil record. Back in `CheckOrReserveIdempotency` (service.go:1049-1063):

```go
rec, err := s.repo.ReserveIdempotencyKey(ctx, ...)
if err != nil { return nil, err }
if rec.CanonicalRequestHash != reqHash {   // <- nil pointer dereference
```

This dereferences `rec` unconditionally, panicking. `chi.Recoverer` is registered in the router (`internal/transport/http/router/router.go:36`) so the process doesn't crash, but the request that hit it gets an opaque 500, and — critically — **the underlying row is never replaced or deleted**, so every subsequent request reusing that exact `Idempotency-Key` value will hit this same code path and panic again, indefinitely. There is also no cleanup job for `bill_idempotency_keys` anywhere in the codebase (see Major finding below), so expired rows never get removed and this state is permanent in practice.

**Why it matters**: This directly contradicts the governing spec, which states expired idempotency rows must be "cleaned durably" and that "Idempotency rows live for 24 hours" (`docs/specs/0003-bill-ocr-v1/index.md:66,108`) — implying a retried request past 24h should be treated as fresh, not permanently broken. A mobile client that reuses an `Idempotency-Key` (e.g., a client-generated UUID tied to a local draft) across a session that spans more than 24 hours — entirely plausible for a bill left in draft overnight — will get a 500 on every mutation for that resource going forward, with no recovery path short of a DB operator manually deleting the row.

**Suggested fix**: Either add expiry to the conflict target (e.g., `ON CONFLICT ... DO UPDATE SET ... WHERE bill_idempotency_keys.expires_at <= now()`, treating an expired row as absent) and re-fetch/re-check the returned row, or have `ReserveIdempotencyKey` explicitly delete-then-insert (or `DELETE WHERE expires_at <= now()` before the insert) inside the same transaction. At minimum, guard the nil case in `CheckOrReserveIdempotency` so it fails safe (treats a nil record as "no prior key" and proceeds) instead of panicking, and add a regression test for "same Idempotency-Key reused after expiry."

## Major

### 🟠 OCR raw-response retention worker is fully implemented but never wired up — spec's 30-day purge never runs, `internal/modules/bill/jobs/ocr_worker.go:281-337`, `internal/bootstrap/app.go:145-172`

**Problem**: `OCRRetentionWorker` and `OCRRetentionJobArgs` (ocr_worker.go:274-337) implement the periodic purge of `ocr_jobs.raw_response` after 30 days, matching `docs/specs/0003-bill-ocr-v1/index.md:176` / `0001-bill-draft-ocr.md:50`. But it is never registered: `river.AddWorker(riverWorkers, ...)` is called for the notification worker and `ocrWorker` (app.go:150, 165) but not for `OCRRetentionWorker`, and no `river.PeriodicJob` is configured anywhere (`riverpkg.NewClient` at app.go:172 passes no `PeriodicJobs`, even though the shared wrapper in `internal/platform/queue/river/client.go:18,63` supports it). The `bill_idempotency_keys` table has the identical gap: nothing anywhere in the codebase ever deletes expired rows from it (confirmed via `grep -rln "bill_idempotency_keys"`, which only shows the migration and the repository CRUD methods).

**Why it matters**: Raw OCR provider responses (which can contain merchant names, item text, and other receipt content) are retained indefinitely instead of being purged after 30 days as the spec requires — a real data-retention/privacy gap, not just an unused code path. `bill_idempotency_keys` also grows without bound, and (per the Blocker above) an expired-but-unpurged row there is actively harmful, not just wasted space.

**Suggested fix**: Register `OCRRetentionWorker` with `river.AddWorker` and schedule it via `river.PeriodicJob` (e.g., daily) in `internal/bootstrap/app.go`, mirroring how `authjobs.New(...)` schedules the auth module's own periodic cleanup. Add an equivalent periodic purge for expired `bill_idempotency_keys` rows (a `DELETE WHERE expires_at <= now()` on the same cadence resolves both this gap and reduces exposure to the Blocker above).

## Minor

### 🟡 Redundant double-clamp of `FinalAmount`, `internal/modules/bill/usecase/service.go:825-828`

`CalculateHamiltonAllocation` already clamps `FinalAmount` to `>= 0` (allocation.go:218-220), so the `if amount < 0 { amount = 0 }` in `finalizeBillImpl` is dead code. Harmless, but worth removing once the Blocker above is fixed, since the real fix likely touches this exact clamping logic.

### 🟡 `getWeight()` mixes weight scales inconsistently for missing input, `internal/modules/bill/usecase/allocation.go:38-49`

Explicit `Weight` values are used as-is, `Ratio` is scaled by `1e8`, and the fallback (neither set) is a hardcoded `10000`. In practice `service.go`'s `toAllocationInput`/`parseWeightToScaledInt` always scales by `1e4` before calling into `allocation.go`, so this inconsistency isn't reachable through the current call path — but the exported `ItemAssignmentInput` type is part of the package's public surface (used directly in tests), and a future caller mixing `Weight` and unset assignments in the same item would get wildly skewed proportions (a `Weight: 1` assignee competing against a fallback-defaulted `10000` assignee is a 1:10000 split, not roughly equal). Consider documenting the expected scale explicitly or unifying the fallback with the `Ratio` scale.

## Strengths

- The idempotency-release-on-failure fix (commit `9d80c4c`) is correct and well-tested (`TestVoidBill_FailedMutation_ReleasesIdempotencyKeyForRetry`), closing a real "wedged in-progress key" gap.
- Multipart body size enforcement is layered correctly: `http.MaxBytesReader` before `ParseMultipartForm`, a declared-`Size` check before opening each file, and a second `io.LimitReader` bound as defense against a lying `Content-Length`/`Size` — genuinely careful.
- Internal error redaction (`writeDomainError`'s `mapped`/fallback path) correctly prevents raw pgx/Cloudinary errors from leaking to clients, with a test proving it (`TestGetBillDetail_UnmappedRepoError_ReturnsRedactedInternalError`).
- Lock ordering in `VoidBill` (bill row first, then debts in UUID order, `repository.go:803-830`) is explicit and matches the documented invariant about avoiding deadlocks with the payment module.
- `CalculateHamiltonAllocation`'s per-item and per-component conservation (sum of floors + largest-remainder distribution) is correct and deterministic; the switch from float to exact integer arithmetic for the core rounding logic is a genuine improvement over the previous version.

## Test coverage

`allocation_test.go` covers equal split, integer weights, SC/VAT/discount together, zero-subtotal creditor-bears-fees, UUID tie-breaking, a large-discount clamp case, draft-mismatch non-dumping, large-amount overflow safety, and the legacy `Ratio` fallback — a solid set, but none of them happen to exercise the cross-component clamp-without-compensation scenario in the first Blocker above (the existing "never produces negative" test's specific numbers don't trigger it). The idempotency nil-pointer path (second Blocker) has no test at all — `handler_test.go`'s idempotency tests use an in-memory mock repository whose `ReserveIdempotencyKey`/`GetIdempotencyKey` never reproduce the real Postgres adapter's expired-row behavior, so this gap wouldn't be caught by the current suite even at the handler layer. The retention-worker wiring gap (Major) is also untested, unsurprisingly since it's a bootstrap/wiring omission rather than a logic bug.
