-- +goose Up
-- ---------------------------------------------------------------------------
-- Auth OTP v1: add attempt_count to user_tokens for brute force protection
-- ---------------------------------------------------------------------------
-- +goose StatementBegin
ALTER TABLE user_tokens 
    ADD COLUMN IF NOT EXISTS attempt_count INT NOT NULL DEFAULT 0,
    ADD CONSTRAINT user_token_attempt_count_non_negative CHECK (attempt_count >= 0);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE user_tokens 
    DROP CONSTRAINT IF EXISTS user_token_attempt_count_non_negative,
    DROP COLUMN IF EXISTS attempt_count;
-- +goose StatementEnd
