-- +goose Up

-- Mỗi dòng = một biến động số dư SePay báo về qua webhook. Bảng này chỉ ghi
-- nhận (append-only); việc đối soát với payments nằm ở bước xử lý sau.
CREATE TABLE IF NOT EXISTS sepay_transactions (
    id                BIGINT PRIMARY KEY,                     -- id giao dịch phía SePay; SePay retry tối đa 7 lần nên đây là khóa chống trùng
    gateway           TEXT NOT NULL,                          -- Tên ngân hàng (Vietcombank, MBBank...)
    transaction_date  TIMESTAMPTZ NOT NULL,                   -- SePay gửi giờ Việt Nam không kèm múi giờ, đã quy đổi về UTC+7
    account_number    TEXT NOT NULL,
    sub_account       TEXT,                                   -- Tài khoản ảo (VA), nếu có
    code              TEXT,                                   -- Mã thanh toán SePay tự nhận diện theo tiền tố cấu hình
    content           TEXT NOT NULL DEFAULT '',               -- Nội dung chuyển khoản
    transfer_type     TEXT NOT NULL,
    transfer_amount   BIGINT NOT NULL,
    accumulated       BIGINT NOT NULL DEFAULT 0,              -- Số dư lũy kế sau giao dịch
    reference_code    TEXT,                                   -- Mã tham chiếu phía ngân hàng
    description       TEXT NOT NULL DEFAULT '',
    payment_reference TEXT,                                   -- Mã PAYxxxxxxxx trích từ code/content, đối chiếu với payments.reference_code
    raw_payload       JSONB NOT NULL,
    received_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT chk_sepay_transactions_transfer_type CHECK (transfer_type IN ('in', 'out')),
    CONSTRAINT chk_sepay_transactions_amount CHECK (transfer_amount >= 0)
);

CREATE INDEX IF NOT EXISTS idx_sepay_transactions_payment_reference
    ON sepay_transactions(payment_reference)
    WHERE payment_reference IS NOT NULL;

-- +goose Down

DROP TABLE IF EXISTS sepay_transactions;
