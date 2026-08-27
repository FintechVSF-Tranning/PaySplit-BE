# Verify: bill and OCR v1 (spec 0003)

## Pending verification for the 2026 08 27 allocation revision

- [x] Two items of 400000 VND and 800000 VND shared equally by six members produce 200000 VND for every member.
- [x] A 100000 VND total shared equally by three members awards 33334 VND to the lowest canonical member UUID and 33333 VND to the other two when all fractions tie.
- [x] Mixed item participation, private items, fee, VAT, general discount, zero subtotal, large values, and discount cap cases preserve every final invariant.
- [x] Reordering members, items, participants, and Go map insertion produces identical output.
- [x] Nested item shares sum to `ItemSubtotal`, and all member final amounts sum to the computed bill total.

The checked Creditor absorption cases below are historical evidence for the previous allocator. They do not verify revised **AC-6** or **AC-10**.

## Commands and runtime evidence
- [x] `go test -v ./internal/modules/bill/repository/postgres/...` (PostgreSQL integration test with real database transactions) (satisfies AC-1, AC-5, AC-7, AC-9, AC-10, AC-11)
- [x] `go test -v ./internal/modules/bill/jobs/...` (River queue OCR background worker test, plus retention job registration) (satisfies AC-2, AC-3, AC-11, AC-13)
- [x] `go test -v ./internal/modules/bill/delivery/http/...` (SSE hub and HTTP handler streaming test) (satisfies AC-2, AC-8, AC-12)
- [x] `go test -v ./internal/modules/bill/usecase/...` (floor allocation and usecase business logic test, including a brute force sweep over 4260 input combinations) (satisfies AC-4, AC-5, AC-6, AC-7, AC-9, AC-10, AC-13)
- [x] `curl -s -i http://localhost:8080/api/v1/bills` (unauthenticated read returns 401 AUTHENTICATION_REQUIRED) (satisfies AC-8)

## Runtime evidence, 2026 08 20

Server started with `go run ./cmd/api`, bound to `localhost:8080`, against the PostgreSQL 18 container on port 5433.

- [x] Both retention jobs run at startup. `SELECT kind, state, args FROM river_job WHERE kind IN ('ocr_raw_retention_cleanup','bill_idempotency_key_cleanup')` returns two rows in state `completed`, with `{"older_than_hours": 720}` read from config. (satisfies AC-11, AC-13)
- [x] Floor allocation with Creditor remainder absorption. `POST /api/v1/bills` with a 100001 VND item split evenly between two members, then `GET /api/v1/bills/{id}`. Breakdown: Creditor 50001 with `rounding_adjustment` 1, other member 50000 with `rounding_adjustment` 0, sum exactly 100001. (satisfies AC-6, AC-10)
- [x] Finalize writes the same numbers immutably. `POST /api/v1/bills/{id}/finalize` returns 200. `bill_shares` holds 50001 and 50000, `sum(final_amount)` equals `bills.total` exactly, and `debts` holds one row of 50000 `awaiting` for the non Creditor member only. (satisfies AC-9, AC-10)
- [x] Expired idempotency key is reclaimed, not a crash. A key row was aged past `expires_at` in the database, then the same `Idempotency-Key` was replayed through `POST /api/v1/bills`. Result 201, server still healthy, zero panics in the log. This is the exact case that used to dereference a nil record. (satisfies AC-1, AC-9)
- [x] Idempotency replay and reuse. Same key with the same body returns the same bill ID twice. Same key with a different body returns 409 `IDEMPOTENCY_KEY_REUSED`. (satisfies AC-1)
- [x] Unauthenticated read returns 401 `AUTHENTICATION_REQUIRED`. (satisfies AC-8)
- [x] Review blocks an unreconciled draft. A bill with discount 200000 against subtotal 100000 returns 422 `BILL_NOT_READY` on review. (satisfies AC-7)
- [x] The read path surfaces blocker codes. Reading, review, and finalize now share one reconciliation function, and a bill detail read of a draft or reviewed bill recomputes the blocker list. The bill whose discount is 200000 against a subtotal of 100000 now returns `["DISCOUNT_EXCEEDS_BILL","TOTAL_MISMATCH"]` where it previously returned an empty list with no breakdown and no explanation. A bill with an unassigned item returns `["ITEM_UNASSIGNED"]`, a bill whose declared subtotal does not match its items returns `["SUBTOTAL_MISMATCH"]`, and a clean bill returns `[]` with a two row breakdown. (satisfies AC-6)
- [x] `mismatch_codes` stays an array. An early cut of the shared function returned nil for a clean bill, which serializes as `null` and would break a client calling `isEmpty` on it. Caught by reading the live response, fixed, and locked in with a test. Clean drafts, finalized bills, and voided bills all return `[]`. (satisfies AC-12)
- [x] Review and finalize still behave after the rewiring. A clean 100001 VND bill reviews with 200, finalizes with 200, and its `bill_shares` sum to exactly 100001 with adjustments of 1 and 0. A bill with an unassigned item is refused at review with 422 `BILL_NOT_READY`. (satisfies AC-7, AC-9, AC-10)

### OCR, Cloudinary, and SSE, run with a real receipt and a real provider key

Fixture: `testdata/bills/image.png`, a 425 by 470 PNG of a restaurant receipt.

- [x] LlamaExtract extracts a real receipt. `go test -run TestIntegration_LlamaExtract ./internal/platform/ocr/llamaextract/` passes in 26.3 seconds against the live provider. Four items returned, and the raw `24/03/2024` is normalized to `2024-03-24`, which is the locale rule the spec asks for. (satisfies AC-3)
- [x] Image draft creation returns 202 with a private Cloudinary asset. `POST /api/v1/bills` as multipart with the PNG returns `202`, one `bill_images` row at position 0 with key `bills/{uuid}/0`, and one `ocr_jobs` row in state `queued`. (satisfies AC-1, AC-2)
- [x] The stored asset is genuinely private. The five minute signed URL returns `200`; the same object requested on the plain public Cloudinary path returns `404`. (satisfies AC-1, AC-8)
- [x] SSE reports the job through to completion. `GET /api/v1/bills/{id}/events` opens with a `snapshot` event, then `ocr.updated` events moving `processing` to `succeeded`, then `heartbeat` every 15 seconds. (satisfies AC-2, AC-12, AC-14)
- [x] OCR never edits the draft by itself. After the job succeeded, the draft still read `subtotal` 0, `total` 0, `version` 1, zero items. The candidate sat on the job row only. (satisfies AC-4)
- [x] Applying the candidate needs the current version. `POST /api/v1/bills/{id}/apply-candidate` with a wrong version returns `409 VERSION_CONFLICT`. With the current version it returns 200 and writes `subtotal` 1795000 and the four extracted items into the draft. (satisfies AC-4, AC-5)
- [x] Running OCR again preserves earlier candidates and manual edits. `POST /api/v1/bills/{id}/ocr-retry` returns 202 and produces a second succeeded job with its own candidate. The draft kept the manual `vat` of 1, `total` of 1795001, and the four items with their assignments. Both candidates remain on `ocr_jobs`. (satisfies AC-4)
- [x] Floor allocation on real OCR numbers. With `subtotal` 1795000 and `vat` 1 split evenly between two members, neither member floor can claim the single VAT dong, so the Creditor takes it: Creditor 897501 with `rounding_adjustment` 1, other member 897500 with 0, sum exactly 1795001. (satisfies AC-6, AC-10)
- [x] Raw provider content never reaches the API. The bill detail response contains no `raw_response` field while the column is populated in the database. (satisfies AC-12)
- [x] The 30 day raw OCR purge really deletes. Both job rows were aged 40 days past `completed_at`, the server restarted so the periodic job fired, and afterwards `raw_response` is NULL on both rows while `candidate` is untouched. Five expired idempotency keys were deleted in the same pass. This is the worker that was never wired up before. (satisfies AC-11, AC-13)
- [x] Void keeps history. `POST /api/v1/bills/{id}/void` with the current version returns 200. The bill and its debt both move to `voided`, and the two `bill_shares` rows survive with their sum still exactly 100001. A void without the version returns `409 VERSION_CONFLICT`. (satisfies AC-11)

### Item discount OCR parsing (spec 0004), live provider run 2026 08 21

Fixture: `testdata/bills/anh2.png`, a real VinCommerce (VM Royal City) supermarket receipt with three interleaved `KM` promotion lines, the exact real world pattern spec 0004 was designed against.

- [x] `go test -run TestIntegration_LlamaExtract ./internal/platform/ocr/llamaextract/` passes in 22.7 seconds against the live LlamaExtract provider (not a synthetic fixture). Raw JSON returned three separate `{"name":"KM","line_total":-64125}`-style lines interleaved with five real items and `"subtotal":null`. (satisfies AC-3)
- [x] The normalizer folds all three real `KM` lines into their preceding item correctly: 5 clean items in the candidate, zero orphan lines, each with `discount_amount`/`final_price` matching `line_total` minus its immediately following `KM` line (e.g. `149625 - 64125 = 85500`). (satisfies AC-15)
- [x] Aggregate reconciliation on real provider output, not synthetic numbers: `subtotal` (derived from item sum, since the provider returned `subtotal: null`) `545500`, `total_item_discount` `192200`, `general_discount` `0`, `discount` `192200`, `total` `353300`, exactly matching the raw provider's own `total: 353300` with no `mismatch_warning`. (satisfies AC-17, AC-18)

## Not exercised in this run
- Multi image drafts of two to five files. Only a single image draft was run.
- Concurrent finalize and concurrent edit races. Covered by the integration tests, not by a live run.
- OCR provider failure and the retry ladder. The live provider succeeded, so the failure branch was never taken.

## Acceptance criteria coverage
- [x] AC-1: Manual draft creation returns 201 Created and image draft creation returns 202 Accepted with private Cloudinary storage.
- [x] AC-2: River background worker processes OCR jobs and broadcasts events to SSE streams.
- [x] AC-3: LlamaExtract client extracts structured candidate with nonnegative integer VND monetary values.
- [x] AC-4: Explicit candidate application updates draft bill without automatic silent overwrites.
- [x] AC-5: Full draft replacement with optimistic locking version check and up to 100 items.
- [x] AC-6: Exact item shares are aggregated before rounding, largest remainder uses stable UUID tie breaking, and the read path returns the stable blocker codes that explain an absent breakdown.
- [x] AC-7: Explicit review checks that subtotal and total reconcile before finalizing.
- [x] AC-8: Group member authorization blocks unauthorized callers and generates short lived signed URLs.
- [x] AC-9: Synchronous transactional finalization creates immutable bill shares and positive awaiting debts.
- [x] AC-10: Sum of final shares equals bill total, every remainder is distributed without Creditor priority, and no member final amount is negative.
- [x] AC-11: Safe void transitions bill and debts to voided while preserving history for replacement.
- [x] AC-12: List and detail reads expose bill state, breakdown, and signed image URLs.
- [x] AC-13: Draft deletion removes bill records and enqueues Cloudinary media cleanup jobs.
- [x] AC-14: In memory calculation and short database transactions protect performance and safety.
- [x] AC-19: `PUT /bills/{id}` accepts `discount_amount` per item, validates it against the derived `line_total`, and item level discounts survive a manual edit.
- [x] AC-20: `POST /bills` accepts the same `discount_amount` field with the same validation and derivation as `PUT`.
- [x] AC-21: `CreateBill` rejects a negative `discount` and no longer violates `check_bills_discount_composition` when `discount > 0` with no item level discount.

## Manual edit item discount (spec 0005), runtime evidence 2026 08 21

Server started with `go run ./cmd/api`, bound to `localhost:8080`, against the PostgreSQL 18 container on port 5433.

- [x] `POST /bills` with `discount: 20000` and no item level discount returns `201` (previously `500`, `check_bills_discount_composition` violation): response carries `total_item_discount: 0`, `general_discount: 20000`, `discount: 20000`. (satisfies AC-21)
- [x] `PUT /bills/{id}` with one item carrying `discount_amount: 15000` on a `line_total: 50000` item returns `200`: response carries `item.discount_amount: 15000`, `item.final_price: 35000`, `bill.total_item_discount: 15000`. (satisfies AC-19)
- [x] `PUT /bills/{id}` with `discount_amount: 999999` on a `line_total: 50000` item returns `400 VALIDATION_FAILED`, `"item 0: discount_amount must be between 0 and line_total"`, and does not write anything (bill version unchanged). (satisfies AC-19)
- [x] `go test ./internal/modules/bill/usecase/...` covers `discount_amount` exceeding the **derived** `line_total` (`unit_price × quantity` fallback, not the raw request value), the item discount round trip through `UpdateDraftBill` reassignment, and the `CreateBill` discount composition regression. (satisfies AC-19, AC-20, AC-21)
- [x] `go test ./internal/modules/bill/repository/postgres/...` against the live database, migrations 1 through 7 applied, all pass. (satisfies AC-19, AC-20, AC-21)
