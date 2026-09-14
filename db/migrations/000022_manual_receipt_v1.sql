-- +goose Up

-- Thanh toán giờ chỉ được xác nhận bởi ngân hàng (SePay) hoặc bởi chính người
-- nhận bấm "Tôi đã nhận tiền"; luồng ảnh minh chứng đã bị gỡ. Nhánh 'confirmed'
-- không còn bắt buộc image_object_key (dòng cũ vẫn giữ ảnh của chúng).
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

-- +goose Down

-- Payment xác nhận thủ công không có ảnh không thỏa ràng buộc cũ: khôi phục ở
-- dạng NOT VALID để rollback không bị chặn bởi dữ liệu đã có.
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
) NOT VALID;
