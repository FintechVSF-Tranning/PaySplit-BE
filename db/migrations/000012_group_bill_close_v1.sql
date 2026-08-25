-- +goose Up

-- Group bill close v1 (spec 0008, AC-1 đến AC-10).
-- Migration thuần cộng thêm: mọi nhóm hiện hữu giữ bill_submission_locked_at NULL
-- (nghĩa là vẫn mở), batch và item không cần backfill dữ liệu lịch sử.
-- Khóa gửi hóa đơn là một chiều trong V1: chỉ đi từ NULL sang NOT NULL,
-- không bao giờ trả về NULL trừ khi nhóm bị archive (spec 0008 Feature design).

-- 1. Cột khóa một chiều trên groups (spec 0008 AC-1)
ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS bill_submission_locked_at TIMESTAMPTZ;

-- 2. Enum trạng thái batch và item (spec 0008 Data model)
-- +goose StatementBegin
DO $$ BEGIN CREATE TYPE bulk_finalize_status AS ENUM ('queued', 'processing', 'completed'); EXCEPTION WHEN duplicate_object THEN null; END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN CREATE TYPE bulk_finalize_item_status AS ENUM ('pending', 'finalized', 'failed'); EXCEPTION WHEN duplicate_object THEN null; END $$;
-- +goose StatementEnd

-- 3. Giá trị hoạt động nhóm mới (spec 0008 Activity and notification contract)
-- +goose StatementBegin
DO $$ BEGIN ALTER TYPE activity_type ADD VALUE IF NOT EXISTS 'bill_submission_locked'; EXCEPTION WHEN duplicate_object THEN null; END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN ALTER TYPE activity_type ADD VALUE IF NOT EXISTS 'bill_bulk_finalize_started'; EXCEPTION WHEN duplicate_object THEN null; END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN ALTER TYPE activity_type ADD VALUE IF NOT EXISTS 'bill_bulk_finalize_completed'; EXCEPTION WHEN duplicate_object THEN null; END $$;
-- +goose StatementEnd

-- 4. Bảng batch chốt toàn bộ hóa đơn (spec 0008 Data model, AC-4)
CREATE TABLE IF NOT EXISTS group_bill_finalize_batches (
    id                     UUID PRIMARY KEY DEFAULT uuidv7(),
    group_id               UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    requested_by_member_id UUID NOT NULL REFERENCES group_members(id),
    status                 bulk_finalize_status NOT NULL DEFAULT 'queued',
    target_count           INT NOT NULL DEFAULT 0 CHECK (target_count >= 0),
    finalized_count        INT NOT NULL DEFAULT 0 CHECK (finalized_count >= 0),
    failed_count           INT NOT NULL DEFAULT 0 CHECK (failed_count >= 0),
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at             TIMESTAMPTZ,
    completed_at           TIMESTAMPTZ,
    -- Bộ đếm không âm và không vượt quá số bill đã capture (bất biến 8)
    CHECK (finalized_count + failed_count <= target_count),
    -- Ma trận trạng thái batch: queued chưa có mốc thời gian, processing đã bắt đầu
    -- chưa kết thúc, completed đủ hai mốc và tổng đếm khớp target (bất biến 8)
    CHECK (
        (status = 'queued'     AND started_at IS NULL     AND completed_at IS NULL)
     OR (status = 'processing' AND started_at IS NOT NULL AND completed_at IS NULL)
     OR (status = 'completed'  AND started_at IS NOT NULL AND completed_at IS NOT NULL
                                   AND finalized_count + failed_count = target_count)
    )
);

-- Mỗi nhóm chỉ được tồn tại tối đa một batch đang queued hoặc processing (AC-4, bất biến 5)
CREATE UNIQUE INDEX IF NOT EXISTS uq_bill_finalize_batches_active
    ON group_bill_finalize_batches(group_id) WHERE status IN ('queued', 'processing');

-- Tra cứu batch mới nhất theo nhóm cho điều hướng của Captain (Value sourcing)
CREATE INDEX IF NOT EXISTS idx_bill_finalize_batches_group_created
    ON group_bill_finalize_batches(group_id, created_at DESC, id DESC);

-- Mỗi cột khóa ngoại mới đều có index riêng (Data model)
CREATE INDEX IF NOT EXISTS idx_bill_finalize_batches_requester
    ON group_bill_finalize_batches(requested_by_member_id);

-- 5. Bảng item: mỗi bill được capture đúng một lần trong một batch (bất biến 4).
-- Lưu ý: bill_id cố ý KHÔNG có khóa ngoại cứng tới bills, theo đúng mẫu
-- "redacted draft delete audit" hiện có, để một draft thất bại vẫn có thể bị
-- hard delete mà item không chặn (spec 0008 Data model).
CREATE TABLE IF NOT EXISTS group_bill_finalize_items (
    batch_id           UUID NOT NULL REFERENCES group_bill_finalize_batches(id) ON DELETE CASCADE,
    bill_id            UUID NOT NULL,
    bill_version       INT NOT NULL CHECK (bill_version > 0),
    captured_reviewed  BOOLEAN NOT NULL,
    status             bulk_finalize_item_status NOT NULL DEFAULT 'pending',
    error_code         TEXT,
    processed_at       TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (batch_id, bill_id),
    -- Ma trận trạng thái item: pending chưa có kết quả, finalized có mốc xử lý
    -- và không lỗi, failed có mốc xử lý và một mã lỗi ổn định khác rỗng
    CHECK (
        (status = 'pending'   AND error_code IS NULL     AND processed_at IS NULL)
     OR (status = 'finalized' AND error_code IS NULL     AND processed_at IS NOT NULL)
     OR (status = 'failed'    AND error_code IS NOT NULL AND error_code <> '' AND processed_at IS NOT NULL)
    )
);

-- Đọc item theo batch lọc theo trạng thái (AC-6, Data model)
CREATE INDEX IF NOT EXISTS idx_bill_finalize_items_batch_status
    ON group_bill_finalize_items(batch_id, status, bill_id);

-- +goose Down

DROP TABLE IF EXISTS group_bill_finalize_items;
DROP TABLE IF EXISTS group_bill_finalize_batches;

ALTER TABLE groups
    DROP COLUMN IF EXISTS bill_submission_locked_at;

DROP TYPE IF EXISTS bulk_finalize_item_status;
DROP TYPE IF EXISTS bulk_finalize_status;

-- Các giá trị activity_type mới ('bill_submission_locked',
-- 'bill_bulk_finalize_started', 'bill_bulk_finalize_completed') cố ý giữ nguyên:
-- PostgreSQL không thể xóa an toàn một giá trị enum. Rollout production vì thế
-- chỉ đi về phía trước, giống tiền lệ 000011.
