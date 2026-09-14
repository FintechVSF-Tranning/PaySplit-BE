-- +goose Up
-- ---------------------------------------------------------------------------
-- Tách FCM token khỏi bảng sessions (spec 0011, giai đoạn 0)
-- ---------------------------------------------------------------------------
-- FCM registration token mô tả THIẾT BỊ, không mô tả PHIÊN: nó do Google cấp cho
-- một bản cài app và sống tới khi người dùng gỡ app, độc lập hoàn toàn với việc
-- ai đang đăng nhập. Đặt nó trên `sessions` khiến mọi câu hỏi về thiết bị phải đi
-- qua một điều kiện về phiên — cụ thể là `expires_at > now()` trong
-- GetActiveFCMTokenByUserID — nên người dùng lâu không mở app sẽ KHÔNG nhận được
-- thông báo nhắc nợ, đúng nhóm mà tính năng đó sinh ra để phục vụ.
--
-- Sau migration này, `sessions.fcm_token` không còn được đọc/ghi. Cột được giữ lại
-- ở bản Up để backfill an toàn và để rollback không mất dữ liệu; migration sau sẽ
-- gỡ hẳn khi đã chạy ổn định.
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS device_tokens (
    id         UUID PRIMARY KEY DEFAULT uuidv7(),
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id  UUID NOT NULL,
    fcm_token  TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT device_tokens_fcm_token_not_blank CHECK (btrim(fcm_token) <> ''),
    CONSTRAINT uq_device_tokens_user_device UNIQUE (user_id, device_id)
);

-- Tra cứu chính: "user này có những địa chỉ push nào".
CREATE INDEX IF NOT EXISTS idx_device_tokens_user_id ON device_tokens(user_id);

-- Backfill. Bỏ qua điều kiện revoked_at/expires_at — token vẫn dùng được kể cả
-- khi phiên đã chết; đó chính là bug đang được sửa.
--
-- DISTINCT ON cần thiết vì lịch sử `sessions` có nhiều hàng đã thu hồi cho cùng
-- một thiết bị: giữ token của phiên mới nhất.
--
-- Cố tình KHÔNG khử trùng lặp theo `fcm_token`: hai tài khoản được phép cùng giữ
-- một chuỗi token (cùng một máy, người dùng A đăng xuất rồi B đăng nhập). Ràng
-- buộc đúng nằm ở chỗ khác — mọi thao tác dọn token phải giới hạn theo user, xem
-- ClearFCMToken.
INSERT INTO device_tokens (user_id, device_id, fcm_token)
SELECT DISTINCT ON (user_id, device_id) user_id, device_id, fcm_token
FROM sessions
WHERE fcm_token IS NOT NULL AND btrim(fcm_token) <> ''
ORDER BY user_id, device_id, issued_at DESC;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Trả token về sessions để không mất khả năng gửi push khi rollback.
UPDATE sessions s
SET fcm_token = d.fcm_token
FROM device_tokens d
WHERE s.user_id = d.user_id
  AND s.device_id = d.device_id
  AND s.revoked_at IS NULL;

DROP TABLE IF EXISTS device_tokens;
-- +goose StatementEnd
