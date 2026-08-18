-- +goose Up
-- ---------------------------------------------------------------------------
-- Admin v1 (spec 0005 / Module 5)
-- ---------------------------------------------------------------------------
-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_admin_audit_admin ON admin_audit_logs(admin_id, created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_admin_audit_admin;
-- +goose StatementEnd
