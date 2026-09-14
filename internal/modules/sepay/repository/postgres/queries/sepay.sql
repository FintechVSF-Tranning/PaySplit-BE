-- name: InsertTransaction :execrows
INSERT INTO sepay_transactions (
    id, gateway, transaction_date, account_number, sub_account, code, content,
    transfer_type, transfer_amount, accumulated, reference_code, description,
    payment_reference, raw_payload
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14
)
ON CONFLICT (id) DO NOTHING;

-- name: GetTransactionMatchStatus :one
SELECT match_status, payment_id FROM sepay_transactions WHERE id = $1;

-- name: MarkTransactionProcessed :execrows
-- Chỉ lần xử lý đầu tiên được ghi: hai lần SePay gửi trùng chạy song song thì
-- kết quả của lần gạch nợ thật (confirmed) không bị already_confirmed đè lên.
UPDATE sepay_transactions
SET match_status = $2, payment_id = $3, processed_at = now()
WHERE id = $1 AND match_status IS NULL;
