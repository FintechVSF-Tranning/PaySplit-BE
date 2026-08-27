# 0004. Item Discount OCR Parsing and Mapping

## Summary

This child spec covers the normalization and mapping of item specific discounts from OCR receipts. In real receipts from supermarkets and food merchants, item promotions appear as separate child lines named KM or with negative values right under their parent items. The OCR normalizer merges these promotion lines into their parent items as positive discount amounts, computes net final prices, isolates bill level general discounts, and prevents double counting during group expense allocation.

## Requirements

This child adds acceptance criteria **AC-15** through **AC-18** to [the umbrella spec](index.md).

**User stories**:

1. As a group member reviewing an OCR receipt, I want promotions on specific items to apply directly to those items so that whoever eats or buys the item gets the discount.
2. As a Creditor, I want the system to distinguish item promotions from bill wide vouchers so that general discounts are shared fairly across all members while item discounts stay with item assignees.
3. As a group member, I want clean bill items without negative placeholder rows like KM so that assigning items to members is clear and error free.

**Acceptance criteria**:

- **AC-15 (Item Promotion Line Detection and Preprocessing)**: The OCR normalizer scans the raw items array in sequence. Any raw entry whose name contains promotion markers (such as `KM`, `Khuyen mai`, `Chiet khau`, `Giam gia`), or whose `line_total` is negative, or whose `quantity` is null or zero with a negative or zero price, is classified as an item promotion line. The normalizer converts its negative total to a positive amount, adds it to `discount_amount` of the immediately preceding real item, and computes `final_price = line_total - discount_amount`. The promotion entry itself is pruned from the resulting candidate items array.
- **AC-16 (Orphan and Multi Promotion Robustness)**: If a promotion line appears before any valid item or has no preceding item, the normalizer falls back to treating its absolute value as a bill level general discount with an informative warning code `OCR_ORPHAN_ITEM_DISCOUNT`. If multiple promotion lines appear under the same parent item, their amounts accumulate into that parent item's `discount_amount`. If `discount_amount` exceeds `line_total`, `final_price` is clamped to zero and the excess amount is moved to general discount with warning code `OCR_ITEM_DISCOUNT_EXCEEDED`.
- **AC-17 (Separation of Item Discounts and General Discounts)**: The normalizer computes `total_item_discount = sum(item.discount_amount)` across all preserved items. It computes the bill level general discount as `general_discount = max(0, payload.discount - total_item_discount)`. The overall bill discount is `total_discount = total_item_discount + general_discount`.
- **AC-18 (Mathematical Reconciliation and Invariant Preservation)**: Gross subtotal is `subtotal = sum(item.line_total)`. Net items total is `net_items_total = sum(item.final_price) = subtotal - total_item_discount`. Computed total is `computed_total = subtotal - total_discount + service_charge + vat = net_items_total - general_discount + service_charge + vat`. If `computed_total` matches `payload.total`, the receipt is marked fully reconciled with `mismatch_warning = false` and `mismatch_delta = 0`.

## Decision

**Chosen option**: Sequential item promotion folding with explicit item discount fields and residual general discount calculation

We process raw OCR items sequentially in the LlamaExtract normalizer platform layer before candidate persistence. We store `discount_amount` and `final_price` on each normalized item candidate and database item row. The bill entity and database schema store `total_item_discount` and `general_discount` alongside total discount. Group allocation assigns shares using `item.final_price`, and allocates only `general_discount` proportionally across members.

**Implementation skills**: `supabase-postgres-best-practices` (`supabase/agent-skills`, `.agents/skills/supabase-postgres-best-practices/`)

## Feature design

### Normalization pipeline

The normalization algorithm runs in `internal/platform/ocr/llamaextract/normalizer.go` as follows:

```text
Raw Receipt Payload from Provider
               ↓
1. Scan Raw Items Array in Order:
   ├── If RawItem is a normal product line:
   │     Create new ItemCandidate with:
   │       name, quantity, unit_price, line_total = item.line_total,
   │       discount_amount = 0, final_price = item.line_total
   │     Append to clean items list
   │
   └── If RawItem is a promotion line (matches KM, negative total, null quantity):
         Extract positive discount: promo_value = abs(RawItem.line_total)
         ├── Case A (Preceding item exists in clean items list):
         │     preceding_item.discount_amount += promo_value
         │     preceding_item.final_price = preceding_item.line_total - preceding_item.discount_amount
         │     Do NOT add promo line to clean items list
         │
         └── Case B (Orphan promo line with no preceding item):
               orphan_discount_accumulator += promo_value
               Add warning "OCR_ORPHAN_ITEM_DISCOUNT"

2. Aggregate Totals:
   gross_subtotal = sum(clean_items.line_total)
   total_item_discount = sum(clean_items.discount_amount)
   net_items_total = sum(clean_items.final_price)

3. Compute General and Total Discounts:
   raw_discount = max(0, payload.discount) + orphan_discount_accumulator
   general_discount = max(0, raw_discount - total_item_discount)
   total_discount = total_item_discount + general_discount

4. Verify Reconciliation:
   computed_total = gross_subtotal - total_discount + service_charge + vat
   mismatch_delta = computed_total - payload.total
   mismatch_warning = (mismatch_delta != 0)
```

### Data model sketch

#### Go Structs (`internal/platform/ocr/llamaextract/schema.go`)

```go
type ReceiptCandidate struct {
	MerchantName      *string         `json:"merchant_name"`
	BillDate          *string         `json:"bill_date"`
	Subtotal          int64           `json:"subtotal"`            // Gross subtotal before item discounts
	TotalItemDiscount int64           `json:"total_item_discount"` // Sum of item discounts
	GeneralDiscount   int64           `json:"general_discount"`   // Bill wide voucher discount
	TotalDiscount     int64           `json:"total_discount"`     // TotalItemDiscount + GeneralDiscount
	ServiceCharge     int64           `json:"service_charge"`
	VAT               int64           `json:"vat"`
	ReportedTotal     int64           `json:"reported_total"`
	ComputedTotal     int64           `json:"computed_total"`
	MismatchWarning   bool            `json:"mismatch_warning"`
	MismatchDelta     int64           `json:"mismatch_delta"`
	Warnings          []string        `json:"warnings,omitempty"`
	Items             []ItemCandidate `json:"items"`
}

type ItemCandidate struct {
	Position       int16   `json:"position"`
	Name           string  `json:"name"`
	Quantity       float64 `json:"quantity"`
	UnitPrice      int64   `json:"unit_price"`      // Base unit price
	LineTotal      int64   `json:"line_total"`      // Gross line total
	DiscountAmount int64   `json:"discount_amount"` // Promotion discount applied to this item
	FinalPrice     int64   `json:"final_price"`     // Net price = LineTotal - DiscountAmount
}
```

#### Domain Entities (`internal/modules/bill/domain/entity.go`)

```go
type BillItem struct {
	ID             uuid.UUID
	BillID         uuid.UUID
	GroupID        uuid.UUID
	Name           string
	Position       int16
	Quantity       float64
	UnitPrice      int64
	LineTotal      int64 // Gross amount
	DiscountAmount int64 // Item discount amount
	FinalPrice     int64 // Net price to be split among assignees
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type Bill struct {
	ID                uuid.UUID
	GroupID           uuid.UUID
	CreditorMemberID  uuid.UUID
	Status            BillStatus
	MerchantName      *string
	BillDate          *time.Time
	ImageObjectKey    *string
	Subtotal          int64 // Gross subtotal
	TotalItemDiscount int64 // Sum of all item discounts
	GeneralDiscount   int64 // General bill level discount
	Discount          int64 // Total discount = TotalItemDiscount + GeneralDiscount
	ServiceCharge     int64
	VAT               int64
	Total             int64 // Final bill total
	MismatchWarning   bool
	Version           int32
	// other metadata fields...
}
```

#### Database Schema (`db/migrations/`)

```sql
-- Migration: add item discount fields to bill_items and bills
ALTER TABLE bill_items
    ADD COLUMN IF NOT EXISTS discount_amount BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS final_price BIGINT NOT NULL DEFAULT 0;

ALTER TABLE bill_items
    ADD CONSTRAINT check_bill_items_discount
    CHECK (discount_amount >= 0 AND final_price >= 0 AND final_price = line_total - discount_amount);

ALTER TABLE bills
    ADD COLUMN IF NOT EXISTS total_item_discount BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS general_discount BIGINT NOT NULL DEFAULT 0;

ALTER TABLE bills
    ADD CONSTRAINT check_bills_discount_composition
    CHECK (total_item_discount >= 0 AND general_discount >= 0 AND discount = total_item_discount + general_discount);
```

### Proportional allocation with net item prices

When calculating group member expenses in `internal/modules/bill/usecase/allocation.go`:

1. **Item Share Calculation**: Each member assigned to an item receives an exact rational share of `item.FinalPrice` (the net price after item promotion), rather than the gross price:
   $$\text{member\_item\_share} = \text{item.FinalPrice} \times \text{integer weight} / \text{total item weight}$$
2. **Member Item Subtotal**: $\text{member\_subtotal} = \sum \text{exact member\_item\_share}$. Money is rounded only after this aggregation under [Allocation and review](0002-allocation-review.md).
3. **General Discount Allocation**: Only `Bill.GeneralDiscount` is allocated proportionally across members based on their $\text{member\_subtotal} / \text{net\_items\_total}$ ratio. Item specific discounts stay entirely with the members who consumed those specific items.

### Value sourcing

| Action | Value produced or displayed | Source |
|---|---|---|
| Parse raw item | `item.discount_amount` | Accumulated absolute values of subsequent promotion lines matching `KM` or negative total |
| Parse raw item | `item.final_price` | `item.line_total - item.discount_amount` |
| Prune promotion line | Filtered clean items list | Promotion lines removed from candidate items list |
| Parse receipt totals | `bill.total_item_discount` | `sum(item.discount_amount)` across clean items |
| Parse receipt totals | `bill.general_discount` | `max(0, payload.discount - total_item_discount)` |
| Parse receipt totals | `bill.discount` | `total_item_discount + general_discount` |
| Member allocation | `member.item_subtotal` | Sum of member shares based on `item.final_price` |
| Member allocation | `member.discount_share` | Proportional share of `bill.general_discount` |

### Critical test scenarios

- **VM Royal City benchmark payload**: 5 product items with 3 interleaving `KM` discount lines (`-64.125`, `-68.175`, `-59.900`), gross subtotal `545.500 đ`, total item discount `192.200 đ`, raw discount `192.200 đ`, general discount `0 đ`, reported total `353.300 đ`. Result produces 5 clean items, 0 orphan lines, exact total match `353.300 đ`, verifies **AC-15**, **AC-17**, **AC-18**.
- **Combined item discount plus voucher payload**: Bill with `50.000 đ` item discount on steak and `30.000 đ` store voucher. `payload.discount = 80000`. Result produces `total_item_discount = 50000`, `general_discount = 30000`, and `total_discount = 80000`, verifies **AC-17**.
- **Orphan promotion line at index 0**: Receipt starts with a discount voucher line before any product. Result converts amount into `general_discount`, attaches warning `OCR_ORPHAN_ITEM_DISCOUNT`, and keeps clean items intact, verifies **AC-16**.
- **Promotion discount exceeds line item price**: Promotion line indicates `-150.000 đ` on a `100.000 đ` item. Result clamps `final_price = 0`, sets `discount_amount = 100000`, and transfers excess `50.000 đ` to `general_discount` with warning `OCR_ITEM_DISCOUNT_EXCEEDED`, verifies **AC-16**.

## Build plan

1. Update `ReceiptCandidate` and `ItemCandidate` schemas in `internal/platform/ocr/llamaextract/schema.go`, satisfies **AC-15**, **AC-17**.
2. Implement sequential item discount normalizer and folding logic in `internal/platform/ocr/llamaextract/normalizer.go` with unit tests for single promo, multi promo, orphan promo, and mixed voucher scenarios, satisfies **AC-15**, **AC-16**, **AC-17**, **AC-18**.
3. Create database migration adding `discount_amount`, `final_price` to `bill_items` and `total_item_discount`, `general_discount` to `bills`, satisfies **AC-15**, **AC-17**.
4. Update domain entities in `internal/modules/bill/domain/entity.go` and repository mappers, satisfies **AC-15**.
5. Update `allocation.go` and `reconciliation.go` in `internal/modules/bill/usecase/` to split `final_price` and allocate only `general_discount`, satisfies **AC-17**, **AC-18**.
6. Update candidate review presentation in frontend mobile app and prototype to show original price, discount badge, and final price per line item, satisfies **AC-15**.

## Consequences

**Positive**:
- Eliminates ugly negative `KM` lines from the user interface and database.
- Guarantees financial math reconciliation on receipts with item level promotions without triggering false positive mismatch alerts.
- Ensures item specific promotions benefit only the members assigned to those items.

**Negative / Tradeoffs**:
- Requires adding columns to `bill_items` and `bills` with a database migration.
- Requires updating both backend allocation usecase and frontend bill breakdown UI to understand net versus gross item prices.

**Neutral**:
- Fully backward compatible with manual bill creation and standard receipts that have no item level discounts (`discount_amount = 0`, `final_price = line_total`).

## Follow-up

- [ ] Run `make sqlc` after applying the database migration to regenerate type safe query wrappers.
- [ ] Ensure mobile Flutter app models map `discountAmount` and `finalPrice` for bill items.
