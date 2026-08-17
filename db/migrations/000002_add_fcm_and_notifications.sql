-- +goose Up
-- +goose StatementBegin
-- 1. Thêm cột fcm_token vào bảng sessions
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS fcm_token TEXT;

-- 2. Bổ sung các cột title, body vào bảng notifications
ALTER TABLE notifications 
    ADD COLUMN IF NOT EXISTS title TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS body TEXT NOT NULL DEFAULT '';

-- Bỏ default '' sau khi đã cập nhật giá trị cho các record cũ (nếu có)
ALTER TABLE notifications 
    ALTER COLUMN title DROP DEFAULT,
    ALTER COLUMN body DROP DEFAULT;

-- 3. Bổ sung các CHECK constraints cho bảng notifications
ALTER TABLE notifications
    ADD CONSTRAINT notifications_type_length CHECK (char_length(btrim(type)) BETWEEN 1 AND 60),
    ADD CONSTRAINT notifications_title_length CHECK (char_length(btrim(title)) BETWEEN 1 AND 255),
    ADD CONSTRAINT notifications_body_length CHECK (char_length(btrim(body)) BETWEEN 1 AND 1000);

-- 4. Bổ sung index tối ưu truy vấn phân trang lịch sử thông báo
CREATE INDEX IF NOT EXISTS idx_notifications_user_created ON notifications(user_id, created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_notifications_user_created;

ALTER TABLE notifications
    DROP CONSTRAINT IF EXISTS notifications_body_length,
    DROP CONSTRAINT IF EXISTS notifications_title_length,
    DROP CONSTRAINT IF EXISTS notifications_type_length,
    DROP COLUMN IF EXISTS body,
    DROP COLUMN IF EXISTS title;

ALTER TABLE sessions
    DROP COLUMN IF EXISTS fcm_token;
-- +goose StatementEnd
