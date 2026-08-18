-- +goose Up
-- ---------------------------------------------------------------------------
-- Module 3: Bill & OCR v1 (Spec 0003)
-- ---------------------------------------------------------------------------
-- +goose StatementBegin

-- 1. Bổ sung các giá trị 'reviewed', 'voided' vào enum bill_status
DO $$ BEGIN ALTER TYPE bill_status ADD VALUE IF NOT EXISTS 'reviewed' BEFORE 'finalized'; EXCEPTION WHEN duplicate_object THEN null; END $$;
DO $$ BEGIN ALTER TYPE bill_status ADD VALUE IF NOT EXISTS 'voided' AFTER 'finalized'; EXCEPTION WHEN duplicate_object THEN null; END $$;

-- 2. Cập nhật bảng bills: Bổ sung replaces_bill_id, voided_at, split_method, mismatch_codes
ALTER TABLE bills 
    ADD COLUMN IF NOT EXISTS replaces_bill_id UUID REFERENCES bills(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS voided_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS split_method TEXT NOT NULL DEFAULT 'even',
    ADD COLUMN IF NOT EXISTS mismatch_codes TEXT[] NOT NULL DEFAULT '{}';

-- Cập nhật ràng buộc trạng thái finalized/voided (thay thế bills_check1 cũ)
ALTER TABLE bills DROP CONSTRAINT IF EXISTS bills_check1;
ALTER TABLE bills DROP CONSTRAINT IF EXISTS bills_finalized_check;
ALTER TABLE bills ADD CONSTRAINT bills_finalized_check 
    CHECK ((status IN ('finalized', 'voided')) = (finalized_at IS NOT NULL));

-- Bổ sung CHECK constraints và Index cho bills
ALTER TABLE bills 
    ADD CONSTRAINT bills_split_method_check CHECK (split_method IN ('even', 'item_ratio', 'exact', 'shares', 'percentage')),
    ADD CONSTRAINT bills_voided_check CHECK ((status = 'voided') = (voided_at IS NOT NULL));

CREATE UNIQUE INDEX IF NOT EXISTS uq_bills_replacement ON bills(replaces_bill_id) WHERE replaces_bill_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_bills_group_created ON bills(group_id, created_at DESC);

-- 3. Tạo bảng bill_images: Lưu trữ danh sách ảnh hóa đơn theo thứ tự (1-5 ảnh, Spec 3 AC-1)
CREATE TABLE IF NOT EXISTS bill_images (
    id          UUID PRIMARY KEY DEFAULT uuidv7(),
    bill_id     UUID NOT NULL,
    group_id    UUID NOT NULL,
    image_key   TEXT NOT NULL,
    position    SMALLINT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (position >= 0 AND position < 5),
    UNIQUE (bill_id, position),
    FOREIGN KEY (bill_id, group_id) REFERENCES bills(id, group_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_bill_images_bill ON bill_images(bill_id, position);

-- 4. Bổ sung cột position vào bảng bill_items để giữ đúng thứ tự món ăn
ALTER TABLE bill_items ADD COLUMN IF NOT EXISTS position SMALLINT NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_bill_items_bill_pos ON bill_items(bill_id, position);

-- Đảm bảo cột weight trong bill_item_assignments tồn tại (hoặc rename từ share_ratio nếu có)
DO $$ BEGIN ALTER TABLE bill_item_assignments RENAME COLUMN share_ratio TO weight; EXCEPTION WHEN undefined_column THEN null; END $$;
ALTER TABLE bill_item_assignments ADD COLUMN IF NOT EXISTS weight NUMERIC(10,4) NOT NULL DEFAULT 1;

-- 5. Tạo bảng bill_shares: Snapshot phân bổ nợ chi tiết của từng thành viên sau khi Finalize (Spec 3 AC-9)
CREATE TABLE IF NOT EXISTS bill_shares (
    id              UUID PRIMARY KEY DEFAULT uuidv7(),
    bill_id         UUID NOT NULL,
    group_id        UUID NOT NULL,
    member_id       UUID NOT NULL,
    computed_amount BIGINT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (computed_amount >= 0),
    UNIQUE (bill_id, member_id),
    FOREIGN KEY (bill_id, group_id) REFERENCES bills(id, group_id) ON DELETE CASCADE,
    FOREIGN KEY (member_id, group_id) REFERENCES group_members(id, group_id)
);
CREATE INDEX IF NOT EXISTS idx_bill_shares_bill ON bill_shares(bill_id);

-- 6. Cập nhật bảng ocr_jobs: Bổ sung candidate (JSONB), version, đổi default provider sang llamaextract
ALTER TABLE ocr_jobs 
    ADD COLUMN IF NOT EXISTS candidate JSONB,
    ADD COLUMN IF NOT EXISTS version INT NOT NULL DEFAULT 1;

ALTER TABLE ocr_jobs ALTER COLUMN provider SET DEFAULT 'llamaextract';

-- Partial Unique Index: Mỗi bill chỉ được có tối đa 1 OCR Job đang chạy (queued/processing, Spec 3 AC-2)
CREATE UNIQUE INDEX IF NOT EXISTS uq_ocr_jobs_active_bill ON ocr_jobs(bill_id) WHERE status IN ('queued', 'processing');

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS uq_ocr_jobs_active_bill;
ALTER TABLE ocr_jobs 
    DROP COLUMN IF EXISTS version,
    DROP COLUMN IF EXISTS candidate;

DROP TABLE IF EXISTS bill_shares CASCADE;

DROP INDEX IF EXISTS idx_bill_items_bill_pos;
ALTER TABLE bill_items DROP COLUMN IF EXISTS position;

DROP TABLE IF EXISTS bill_images CASCADE;

DROP INDEX IF EXISTS idx_bills_group_created;
DROP INDEX IF EXISTS uq_bills_replacement;
ALTER TABLE bills 
    DROP CONSTRAINT IF EXISTS bills_voided_check,
    DROP CONSTRAINT IF EXISTS bills_split_method_check,
    DROP COLUMN IF EXISTS mismatch_codes,
    DROP COLUMN IF EXISTS split_method,
    DROP COLUMN IF EXISTS voided_at,
    DROP COLUMN IF EXISTS replaces_bill_id;
-- +goose StatementEnd
