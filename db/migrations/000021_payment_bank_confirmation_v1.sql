-- +goose Up

-- Thanh toán được xác nhận tự động khi ngân hàng (qua SePay) báo tiền đã vào
-- đúng tài khoản người nhận. Khi đó không có ảnh minh chứng: bằng chứng là
-- giao dịch ngân hàng, lưu ở bank_transaction_id.
ALTER TABLE payments ADD COLUMN IF NOT EXISTS confirmation_source TEXT NOT NULL DEFAULT 'manual';
ALTER TABLE payments ADD COLUMN IF NOT EXISTS bank_transaction_id BIGINT;

ALTER TABLE payments DROP CONSTRAINT IF EXISTS chk_payments_confirmation_source;
ALTER TABLE payments ADD CONSTRAINT chk_payments_confirmation_source CHECK (
    (confirmation_source = 'manual' AND bank_transaction_id IS NULL)
 OR (confirmation_source = 'bank_transfer' AND status = 'confirmed' AND bank_transaction_id IS NOT NULL)
);

-- Một giao dịch ngân hàng chỉ gạch được đúng một payment.
CREATE UNIQUE INDEX IF NOT EXISTS uq_payments_bank_transaction_id
    ON payments(bank_transaction_id)
    WHERE bank_transaction_id IS NOT NULL;

-- Giống 000009, chỉ nới nhánh 'confirmed': ảnh minh chứng bắt buộc với xác
-- nhận thủ công, không bắt buộc khi nguồn xác nhận là ngân hàng.
ALTER TABLE payments DROP CONSTRAINT IF EXISTS chk_payments_state_matrix;
ALTER TABLE payments ADD CONSTRAINT chk_payments_state_matrix CHECK (
    (status IN ('pending_proof', 'superseded')
        AND submitted_at IS NULL AND image_object_key IS NULL
        AND recipient_bank_code IS NULL AND recipient_bank_name IS NULL
        AND recipient_account_number IS NULL AND recipient_account_holder IS NULL
        AND confirmed_at IS NULL AND rejected_at IS NULL AND rejection_reason IS NULL
        AND stalled_alerted_at IS NULL)
 OR (status = 'pending_confirmation'
        AND submitted_at IS NOT NULL AND image_object_key IS NOT NULL
        AND recipient_bank_code IS NOT NULL AND recipient_bank_name IS NOT NULL
        AND recipient_account_number IS NOT NULL AND recipient_account_holder IS NOT NULL
        AND confirmed_at IS NULL AND rejected_at IS NULL AND rejection_reason IS NULL)
 OR (status = 'confirmed'
        AND submitted_at IS NOT NULL
        AND (image_object_key IS NOT NULL OR confirmation_source = 'bank_transfer')
        AND recipient_bank_code IS NOT NULL AND recipient_bank_name IS NOT NULL
        AND recipient_account_number IS NOT NULL AND recipient_account_holder IS NOT NULL
        AND confirmed_at IS NOT NULL AND rejected_at IS NULL AND rejection_reason IS NULL)
 OR (status = 'rejected'
        AND submitted_at IS NOT NULL AND image_object_key IS NOT NULL
        AND recipient_bank_code IS NOT NULL AND recipient_bank_name IS NOT NULL
        AND recipient_account_number IS NOT NULL AND recipient_account_holder IS NOT NULL
        AND confirmed_at IS NULL AND rejected_at IS NOT NULL
        AND rejection_reason IS NOT NULL AND length(btrim(rejection_reason)) BETWEEN 1 AND 500)
);

-- Kết quả đối soát của từng giao dịch SePay. match_status NULL nghĩa là chưa
-- xử lý xong (ví dụ lỗi DB giữa chừng): lần SePay retry sẽ xử lý lại.
ALTER TABLE sepay_transactions ADD COLUMN IF NOT EXISTS match_status TEXT;
ALTER TABLE sepay_transactions ADD COLUMN IF NOT EXISTS payment_id UUID REFERENCES payments(id) ON DELETE SET NULL;
ALTER TABLE sepay_transactions ADD COLUMN IF NOT EXISTS processed_at TIMESTAMPTZ;

-- +goose Down

ALTER TABLE sepay_transactions
    DROP COLUMN IF EXISTS processed_at,
    DROP COLUMN IF EXISTS payment_id,
    DROP COLUMN IF EXISTS match_status;

-- Payment xác nhận qua ngân hàng không có ảnh nên không thỏa ràng buộc cũ:
-- khôi phục ở dạng NOT VALID để rollback không bị chặn bởi dữ liệu đã có.
ALTER TABLE payments DROP CONSTRAINT IF EXISTS chk_payments_state_matrix;
ALTER TABLE payments ADD CONSTRAINT chk_payments_state_matrix CHECK (
    (status IN ('pending_proof', 'superseded')
        AND submitted_at IS NULL AND image_object_key IS NULL
        AND recipient_bank_code IS NULL AND recipient_bank_name IS NULL
        AND recipient_account_number IS NULL AND recipient_account_holder IS NULL
        AND confirmed_at IS NULL AND rejected_at IS NULL AND rejection_reason IS NULL
        AND stalled_alerted_at IS NULL)
 OR (status = 'pending_confirmation'
        AND submitted_at IS NOT NULL AND image_object_key IS NOT NULL
        AND recipient_bank_code IS NOT NULL AND recipient_bank_name IS NOT NULL
        AND recipient_account_number IS NOT NULL AND recipient_account_holder IS NOT NULL
        AND confirmed_at IS NULL AND rejected_at IS NULL AND rejection_reason IS NULL)
 OR (status = 'confirmed'
        AND submitted_at IS NOT NULL AND image_object_key IS NOT NULL
        AND recipient_bank_code IS NOT NULL AND recipient_bank_name IS NOT NULL
        AND recipient_account_number IS NOT NULL AND recipient_account_holder IS NOT NULL
        AND confirmed_at IS NOT NULL AND rejected_at IS NULL AND rejection_reason IS NULL)
 OR (status = 'rejected'
        AND submitted_at IS NOT NULL AND image_object_key IS NOT NULL
        AND recipient_bank_code IS NOT NULL AND recipient_bank_name IS NOT NULL
        AND recipient_account_number IS NOT NULL AND recipient_account_holder IS NOT NULL
        AND confirmed_at IS NULL AND rejected_at IS NOT NULL
        AND rejection_reason IS NOT NULL AND length(btrim(rejection_reason)) BETWEEN 1 AND 500)
) NOT VALID;

DROP INDEX IF EXISTS uq_payments_bank_transaction_id;
ALTER TABLE payments DROP CONSTRAINT IF EXISTS chk_payments_confirmation_source;
ALTER TABLE payments
    DROP COLUMN IF EXISTS bank_transaction_id,
    DROP COLUMN IF EXISTS confirmation_source;
