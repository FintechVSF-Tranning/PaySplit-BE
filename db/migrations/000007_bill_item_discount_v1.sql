-- +goose Up
-- ---------------------------------------------------------------------------
-- Item Discount OCR Parsing and Mapping (Spec 0003, child 0004)
-- ---------------------------------------------------------------------------
-- +goose StatementBegin

-- 1. Bổ sung discount_amount, final_price vào bill_items (Spec 3 AC-15, AC-17)
ALTER TABLE bill_items
    ADD COLUMN IF NOT EXISTS discount_amount BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS final_price BIGINT NOT NULL DEFAULT 0;

-- Backfill các dòng cũ (trước tính năng này): không có khuyến mãi riêng theo món,
-- nên final_price bằng đúng line_total.
UPDATE bill_items SET final_price = line_total WHERE discount_amount = 0;

ALTER TABLE bill_items DROP CONSTRAINT IF EXISTS check_bill_items_discount;
ALTER TABLE bill_items ADD CONSTRAINT check_bill_items_discount
    CHECK (discount_amount >= 0 AND final_price >= 0 AND final_price = line_total - discount_amount);

-- 2. Bổ sung total_item_discount, general_discount vào bills (Spec 3 AC-17)
ALTER TABLE bills
    ADD COLUMN IF NOT EXISTS total_item_discount BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS general_discount BIGINT NOT NULL DEFAULT 0;

-- Backfill: hóa đơn cũ không có khuyến mãi theo món, toàn bộ discount hiện có là giảm giá chung.
UPDATE bills SET general_discount = discount WHERE total_item_discount = 0 AND general_discount = 0;

ALTER TABLE bills DROP CONSTRAINT IF EXISTS check_bills_discount_composition;
ALTER TABLE bills ADD CONSTRAINT check_bills_discount_composition
    CHECK (total_item_discount >= 0 AND general_discount >= 0 AND discount = total_item_discount + general_discount);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE bills
    DROP CONSTRAINT IF EXISTS check_bills_discount_composition,
    DROP COLUMN IF EXISTS general_discount,
    DROP COLUMN IF EXISTS total_item_discount;

ALTER TABLE bill_items
    DROP CONSTRAINT IF EXISTS check_bill_items_discount,
    DROP COLUMN IF EXISTS final_price,
    DROP COLUMN IF EXISTS discount_amount;
-- +goose StatementEnd
