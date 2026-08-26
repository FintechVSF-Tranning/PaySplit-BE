-- +goose Up
-- Group realtime sync v1 (spec 0009): con trỏ phiên bản cho mỗi nhóm + nhật ký sự kiện.
--
-- roster_version là nguồn thứ tự duy nhất của một nhóm. Nó KHÔNG dùng sequence
-- toàn cục: hai transaction lấy số từ một sequence có thể commit ngược thứ tự,
-- khiến client đọc "WHERE version > N" bỏ sót vĩnh viễn một sự kiện. Bump bằng
-- UPDATE trên chính dòng groups thì transaction sau phải chờ transaction trước
-- commit (mọi mutation của nhóm đã giữ sẵn khóa này qua LockActiveGroup), nên
-- dãy version của mỗi nhóm luôn liền mạch và đúng thứ tự commit.
ALTER TABLE groups ADD COLUMN IF NOT EXISTS roster_version BIGINT NOT NULL DEFAULT 0;

-- Nhật ký sự kiện là nguồn sự thật cho catch-up; SSE chỉ là lớp tăng tốc.
CREATE TABLE IF NOT EXISTS group_events (
    group_id   UUID        NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    version    BIGINT      NOT NULL,
    event_type TEXT        NOT NULL,
    payload    JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (group_id, version),
    CHECK (version > 0),
    CHECK (event_type <> '')
);

-- Phục vụ job dọn nhật ký cũ; client tụt xa hơn ngưỡng giữ log sẽ nhận snapshot.
CREATE INDEX IF NOT EXISTS idx_group_events_retention ON group_events(created_at);

-- +goose Down
DROP INDEX IF EXISTS idx_group_events_retention;
DROP TABLE IF EXISTS group_events;
ALTER TABLE groups DROP COLUMN IF EXISTS roster_version;
