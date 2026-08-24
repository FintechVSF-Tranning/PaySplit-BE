-- +goose NO TRANSACTION
-- +goose Up

ALTER TABLE media_cleanup_jobs
    ADD COLUMN IF NOT EXISTS reason TEXT NOT NULL DEFAULT '';
DROP INDEX CONCURRENTLY IF EXISTS idx_media_cleanup_jobs_due;
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_media_cleanup_jobs_due
    ON media_cleanup_jobs(next_attempt_at, id) WHERE completed_at IS NULL;

-- +goose Down

ALTER TABLE media_cleanup_jobs
    DROP COLUMN IF EXISTS reason;
DROP INDEX CONCURRENTLY IF EXISTS idx_media_cleanup_jobs_due;
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_media_cleanup_jobs_due
    ON media_cleanup_jobs(next_attempt_at, id) WHERE completed_at IS NULL AND attempt_count < 10;
