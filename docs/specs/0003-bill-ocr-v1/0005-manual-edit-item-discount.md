# 0005. Manual Edit Preserves Item Level Discount

## Summary

This child spec fixes a gap found while verifying spec 0004: the manual edit endpoint (`PUT /bills/{id}`) has no way to receive `discount_amount` per item, so any manual edit after an OCR apply (even just reassigning who owes what, the normal next step) silently wipes item level discounts and turns the whole amount into a general discount. This spec adds `discount_amount` to the item request body of both `PUT /bills/{id}` and `POST /bills`, so item level promotions survive an edit, matching what OCR already persists.

## Context

Spec 0004 taught the OCR normalizer to fold `KM` style promotion lines into `bill_items.discount_amount` and `final_price`, and taught the allocator to keep that discount with the item's assignees rather than spreading it across the group. Both `bill_items.discount_amount` and `bills.total_item_discount` / `general_discount` already exist in the database (migration `000007_bill_item_discount_v1.sql`) and already round trip correctly through `POST /bills/{id}/apply-candidate`, confirmed live during `/check verify`.

The gap is in the manual edit path. `UpdateDraftBill` (`internal/modules/bill/usecase/service.go`) replaces the entire item list on every call (`DeleteBillItems` then `CreateBillItem` per item), and its request DTO (`CreateBillItemRequest`, shared with `POST /bills`) has no `discount_amount` field. So the normal workflow, OCR applies with an even split across every active member, then the Creditor reassigns each item to whoever actually consumed it, calls this endpoint and, because the shared DTO carries no per item discount, the fix already lands with `total_item_discount` reset to 0 and the whole `discount` folded into `general_discount`. The promotion still shows up in the bill total, but it is no longer tied to the item, so AC-17's guarantee (item promotions benefit only the members assigned to that item) breaks the moment a user does the one thing they are expected to do after OCR.

The consequence of not deciding: the item discount feature works only until the first manual touch, which is close to always, since OCR always needs reassignment to real members.

## Requirements

This child adds acceptance criteria **AC-19** through **AC-21** to [the umbrella spec](index.md).

**User stories**:

1. As a Creditor who just reassigned OCR extracted items to the right members, I want the item level promotions to stay attached to those items so that reassigning ownership does not silently turn a targeted discount into a shared one.
2. As a group member entering a bill manually with a running promotion on one item, I want to record that promotion on the item itself so that only whoever is assigned that item benefits from it.

**Acceptance criteria**:

- **AC-19 (item discount round trips through manual edit)**: `PUT /bills/{id}` accepts `discount_amount` (int64, VND, optional, defaults to 0 when omitted) on each item in its request body. Validation runs per item, against that item's **derived** `line_total` (after the existing `unit_price × quantity` fallback, not the raw request value), in this order within the existing checks: the 100 item limit, then `bill.discount < 0`, then each item's `0 <= discount_amount <= line_total` (first failing item's index returned in the error `details`), then `general_discount >= 0`, then assignment weights. The server computes `final_price = line_total - discount_amount`, recomputes `total_item_discount = sum(item.discount_amount)` server side, and derives `general_discount = discount - total_item_discount` (the bill level `discount` field keeps its existing meaning: item plus general combined, unchanged contract). Any failing check rejects the whole request with `400 VALIDATION_FAILED` and writes nothing.
- **AC-20 (symmetry with manual creation)**: `POST /bills` accepts the same `discount_amount` field on each item, with the same validation order and derivation as AC-19, since it shares the same item request DTO.
- **AC-21 (fix the existing `POST /bills` discount bug)**: today, `CreateBill` never validates `req.Discount < 0` and never sets `bills.total_item_discount` / `general_discount`, so any manual bill created with `discount > 0` already violates the database's `check_bills_discount_composition` constraint and fails with an unhandled `500`. `CreateBill` gains the same `req.Discount < 0` guard `UpdateDraftBill` already has, and both endpoints reject `line_total < 0` with `400 VALIDATION_FAILED` before it can produce a negative `final_price` and fail at the database layer instead.

## Options considered

### Option 1: Add `discount_amount` to the shared item request DTO, client round trips it

Add the field to `CreateBillItemRequest` (already shared by `POST /bills` and `PUT /bills/{id}`). The client must resend each item's current `discount_amount` on every edit, the same way it already must resend `unit_price` and `line_total`, since the endpoint replaces the whole item list.

**Pros**:
- Matches the endpoint's existing "resend the full item" contract exactly, no new mental model for the client
- One field, no new endpoint, no change to the delete and recreate mechanics that already work
- Same DTO serves both create and edit, so behavior cannot drift between the two entry points

**Cons**:
- A client that forgets to resend `discount_amount` silently loses it, exactly today's failure mode, just now opt in instead of guaranteed. The mobile app must be updated before this is safe in practice.

### Option 2: Redesign `PUT /bills/{id}` as a partial patch (only sent fields change, unlisted items are left alone)

Stop deleting and recreating every item on every edit; instead, update only the items and fields present in the request.

**Pros**:
- Fixes the same class of data loss for every field, not just `discount_amount`; an edit to the merchant name would no longer require resending the entire item list

**Cons**:
- A materially larger change than the question asked: rewrites `UpdateDraftBill`'s core mechanics, its concurrency story (the delete and recreate under one `bills.version` check), and the API contract every existing client already relies on
- Introduces new ambiguity (how does a client delete an item, versus not mentioning it?) that needs its own design pass

### Option 3: Keep the current design, accept that manual edit converts item discounts to general

**Pros**:
- Zero backend change

**Cons**:
- Directly contradicts AC-17's stated goal the moment a user does the expected next step after OCR (reassigning items), which is not an edge case, it is the normal path

## Decision

**Chosen option**: Option 1: Add `discount_amount` to the shared item request DTO, client round trips it

## Rationale

Option 2 solves a real, broader problem (the endpoint's replace everything mechanics) but is a materially different, larger decision than what was asked, and this spec's job is the smallest change that closes the AC-17 gap, not a redesign of the edit endpoint's concurrency model. Option 3 was rejected because it leaves the feature broken for the workflow it was built for. Option 1 costs one field and matches a pattern (resend the full state) the client already implements correctly for every other item field; the cost of forgetting to resend it is real but is a mobile app implementation detail to get right when it adopts the field, not a backend design flaw, and does not need this endpoint's replace semantics to change.

## Feature design

**Data model sketch**: No new columns. `bill_items.discount_amount` and `bill_items.final_price` (from migration `000007_bill_item_discount_v1.sql`) already exist and already carry the `CHECK (final_price = line_total - discount_amount)` constraint that enforces this at the database layer.

**API surface**:

| Endpoint | Method | Key inputs | Key outputs | Auth | Key errors |
|---|---|---|---|---|---|
| /bills | POST | `items[].discount_amount` (int64, optional, default 0) | `bill.items[].discount_amount`, `.final_price`, `bill.total_item_discount`, `.general_discount` | bearer, Captain or group member | 400 VALIDATION_FAILED |
| /bills/{id} | PUT | `items[].discount_amount` (int64, optional, default 0) | same as above | bearer, Captain or Creditor | 400 VALIDATION_FAILED, 409 VERSION_CONFLICT |

**Value sourcing**:

| Action | Value produced / displayed | Source |
|---|---|---|
| POST /bills, PUT /bills/{id} | `item.discount_amount` | request body `items[].discount_amount`, defaults to 0 when omitted |
| POST /bills, PUT /bills/{id} | `item.line_total` (used to validate `discount_amount` and compute `final_price`) | the **derived** value: request `line_total` if nonzero, else `unit_price × quantity` (existing fallback), never the raw request field directly |
| POST /bills, PUT /bills/{id} | `item.final_price` | derived, `item.line_total - item.discount_amount`, never accepted from the client |
| POST /bills, PUT /bills/{id} | `bill.total_item_discount` | derived, `sum(item.discount_amount)` across the request's items |
| POST /bills, PUT /bills/{id} | `bill.general_discount` | derived, `request.discount - total_item_discount` (existing `discount` field keeps its current meaning: the combined total, gross subtotal in, combined discount out, exactly today's contract) |

**Contract carried over unchanged**: `bill.subtotal` in the request must be the **gross** sum of item `line_total`s (before any discount), and `bill.discount` must be the **combined** total (item plus general), exactly as today. This spec does not change or re-validate that contract at write time; a client that violates it still surfaces as `SUBTOTAL_MISMATCH` / `TOTAL_MISMATCH` at `/bills/{id}/review` via the existing `evaluateAllocation` reconciliation, unchanged.

**Key invariants**:
- `item.line_total >= 0` for every item, else `400 VALIDATION_FAILED` (new: today a negative `line_total` reaches the database and fails the `bill_items_check` / `check_bill_items_discount` constraints as an unhandled `500` instead of a clean `400`)
- `0 <= item.discount_amount <= item.line_total` (the derived value above) for every item, else `400 VALIDATION_FAILED` naming the failing item's index
- `item.final_price = item.line_total - item.discount_amount` always, computed server side, never accepted as client input
- `bill.discount >= 0` for both endpoints (today only `UpdateDraftBill` checks this; `CreateBill` gains the same guard, see AC-21), and `bill.general_discount >= 0`, else `400 VALIDATION_FAILED`
- `bill.discount = bill.total_item_discount + bill.general_discount`, matching the `check_bills_discount_composition` constraint already enforced at the database layer

**Security model**: Unchanged. Same authorization as the rest of `PUT /bills/{id}` and `POST /bills` (Captain, or the Creditor for edit; any active group member for create): no new roles, no new ownership rule.

**Critical test scenarios**:
- Happy path: apply an OCR candidate carrying an item discount, then `PUT` the bill reassigning items to distinct members while resending the same `discount_amount`; `total_item_discount` and each item's `discount_amount`/`final_price` are unchanged after the edit, verifies **AC-19**.
- Failure case: `PUT` an item with `discount_amount` greater than its **derived** `line_total` (send `line_total: 0, unit_price: 100000, quantity: "2"` with `discount_amount: 250000`, which must validate against the derived `200000`, not the literal `0`); the request is rejected with `400 VALIDATION_FAILED` and no partial write occurs, verifies **AC-19**.
- Symmetry: `POST /bills` with one item carrying `discount_amount`; the created item's `final_price` and the bill's `total_item_discount` reflect it exactly as `PUT` would, verifies **AC-20**.
- Regression: `POST /bills` with `discount: 20000` and no per item discount (today's bug); the bill is created successfully with `total_item_discount: 0`, `general_discount: 20000`, no database constraint error, verifies **AC-21**.

## Build plan

1. Add `DiscountAmount int64` to `CreateBillItemRequest` in `internal/modules/bill/usecase/service.go` (shared by both endpoints), satisfies **AC-19**, **AC-20**.
2. Add the missing `req.Discount < 0` guard to `CreateBill` (parity with `UpdateDraftBill`) and an `item.LineTotal >= 0` guard to both endpoints' item loops, both `400 VALIDATION_FAILED`, satisfies **AC-21**.
3. In both endpoints' item construction loop, after `lineTotal` is derived (the existing `unit_price × quantity` fallback), validate `0 <= discount_amount <= lineTotal` (naming the failing item's index in the error), then compute `final_price = lineTotal - discount_amount` and accumulate `total_item_discount`, satisfies **AC-19**, **AC-20**.
4. After the item loop, derive `general_discount = discount - total_item_discount`, reject with `400 VALIDATION_FAILED` if negative, and set `domain.Bill.TotalItemDiscount` / `.GeneralDiscount` from it, replacing the current `GeneralDiscount = req.Discount` fallback in both usecase paths, satisfies **AC-19**, **AC-20**, **AC-21**.
5. Update `docs/openapi.yaml` for both endpoints' request schemas, satisfies **AC-19**, **AC-20**.
6. Add the four critical test scenarios above as usecase tests (mirroring the existing `TestUpdateDraftBill_*` / `TestCreateBill_*` table in `internal/modules/bill/usecase/service_test.go`), satisfies **AC-19**, **AC-20**, **AC-21**.

No migration: both database columns already exist from `000007_bill_item_discount_v1.sql`; this is an additive, backward compatible JSON field (omitted means 0, identical to today's behavior).

## Consequences

**Positive**:
- Item level promotions survive the normal OCR reassignment workflow instead of only surviving until the first manual touch
- `POST` and `PUT` stay behaviorally identical for discount handling, since they share one DTO and one validation path
- Fixes a live bug: `POST /bills` with `discount > 0` currently fails with an unhandled `500` (AC-21)

**Negative / tradeoffs**:
- The mobile app (`PaySplit-FE`) must start sending `discount_amount` on every item edit once it wants to preserve discounts; until it does, the observed behavior is unchanged from today (discount silently becomes general on edit), it is opt in, not automatically fixed by this backend change alone
- One more field for every future client of these two endpoints to get right on every write
- Rollout ordering risk: a client that starts sending per item `discount_amount` but stops keeping `discount` as the correct combined (item plus general) total will get a new `400 VALIDATION_FAILED` (`general_discount < 0`) on a request that used to succeed. The client contract for `discount` does not change, only becomes enforced; call this out to the mobile team before rollout, not as a breaking API change but as a now visible violation of a rule that already existed

**Neutral**:
- Does not touch the "replace all items on every edit" mechanics; a client that only ever means to change one field must still resend the full item list, unchanged from today

## Follow-up

- [ ] Update `PaySplit-FE`'s bill edit screen and its request models to send `discount_amount` when reassigning OCR extracted items, otherwise this fix has no user facing effect
- [ ] Spec 0004's build plan step 6 (candidate review UI showing original price, discount badge, final price) should also cover the edit screen sending this field back, not only displaying it
- [ ] Confirm with the mobile team that `PaySplit-FE` already sends `discount` as the gross combined total (not just the general portion) before this ships, per the rollout ordering risk above
