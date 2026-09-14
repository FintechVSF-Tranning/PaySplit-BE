-- +goose Up
-- ---------------------------------------------------------------------------
-- Gỡ bỏ refresh token rotation (spec 0011, giai đoạn 4)
-- ---------------------------------------------------------------------------
-- Từ giai đoạn 3, credential duy nhất là session ID đục lưu trên Redis, tự gia
-- hạn bằng TTL trượt. Không còn access token ngắn hạn nên cũng không còn thứ gì
-- để xoay vòng: bảng này đã ngừng được đọc và ghi.
--
-- Cột sessions.fcm_token cũng được gỡ ở đây. Nó đã ngừng dùng từ migration
-- 000018 (chuyển sang device_tokens) và được giữ lại một nhịp để rollback an
-- toàn; nhịp đó đã qua.
-- +goose StatementBegin
DROP TABLE IF EXISTS session_refresh_tokens;

ALTER TABLE sessions DROP COLUMN IF EXISTS fcm_token;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS fcm_token TEXT;

UPDATE sessions s
SET fcm_token = d.fcm_token
FROM device_tokens d
WHERE s.user_id = d.user_id AND s.device_id = d.device_id AND s.revoked_at IS NULL;

CREATE TABLE IF NOT EXISTS session_refresh_tokens (
    id          UUID PRIMARY KEY DEFAULT uuidv7(),
    session_id  UUID NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    token_hash  BYTEA NOT NULL UNIQUE,
    issued_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL,
    used_at     TIMESTAMPTZ,
    revoked_at  TIMESTAMPTZ,
    CONSTRAINT session_refresh_token_hash_size CHECK (octet_length(token_hash) = 32),
    CONSTRAINT session_refresh_token_expiry_after_issue CHECK (expires_at > issued_at)
);
CREATE INDEX IF NOT EXISTS idx_session_refresh_tokens_session_id ON session_refresh_tokens(session_id);
CREATE INDEX IF NOT EXISTS idx_session_refresh_tokens_live_lookup ON session_refresh_tokens(token_hash) WHERE used_at IS NULL AND revoked_at IS NULL;
-- Dữ liệu refresh token không khôi phục được: chúng là chuỗi ngẫu nhiên đã băm và
-- không tồn tại ở đâu khác. Rollback về cơ chế cũ đồng nghĩa mọi người dùng phải
-- đăng nhập lại.
-- +goose StatementEnd
