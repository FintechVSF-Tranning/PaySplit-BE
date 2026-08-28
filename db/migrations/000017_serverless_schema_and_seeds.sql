-- +goose Up
-- Migration 000017: Serverless runtime schema, job queue tables, coordination seeds, and sync versions.

-- 1. Bổ sung sync_version cho bảng bills và membership_sync_version cho users
ALTER TABLE bills ADD COLUMN IF NOT EXISTS sync_version bigint NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN IF NOT EXISTS membership_sync_version bigint NOT NULL DEFAULT 0;

-- 2. Hàng đợi durable app_jobs thay thế River Queue
CREATE TABLE IF NOT EXISTS app_jobs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kind text NOT NULL,
    args jsonb NOT NULL DEFAULT '{}'::jsonb,
    idempotency_key text NOT NULL,
    status text NOT NULL DEFAULT 'available',
    priority int NOT NULL DEFAULT 0,
    available_at timestamptz NOT NULL DEFAULT now(),
    attempts int NOT NULL DEFAULT 0,
    max_attempts int NOT NULL DEFAULT 5,
    lease_token uuid,
    lease_expires_at timestamptz,
    last_error text,
    completed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_app_jobs_kind_idempotency UNIQUE (kind, idempotency_key),
    CONSTRAINT chk_app_jobs_status CHECK (status IN ('available', 'running', 'completed', 'discarded'))
);

CREATE INDEX IF NOT EXISTS idx_app_jobs_claimable 
    ON app_jobs (priority DESC, available_at ASC, created_at ASC) 
    WHERE status = 'available';

CREATE INDEX IF NOT EXISTS idx_app_jobs_running_lease 
    ON app_jobs (lease_expires_at) 
    WHERE status = 'running';

-- 3. Bảng quản lý 10 drain slots toàn cục
CREATE TABLE IF NOT EXISTS job_drain_slots (
    slot_no smallint PRIMARY KEY,
    state text NOT NULL DEFAULT 'free',
    lease_token uuid,
    lease_expires_at timestamptz,
    dispatch_generation bigint NOT NULL DEFAULT 0,
    wave_id bigint NOT NULL DEFAULT 0,
    holder text,
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT chk_job_drain_slots_state CHECK (state IN ('free', 'reserved', 'leased')),
    CONSTRAINT chk_job_drain_slots_no CHECK (slot_no BETWEEN 1 AND 10)
);

-- Seed đúng 10 slot rows
INSERT INTO job_drain_slots (slot_no, state)
SELECT s, 'free' FROM generate_series(1, 10) AS s
ON CONFLICT (slot_no) DO NOTHING;

-- 4. Singleton quản lý điều phối wakeup và wave dispatch
CREATE TABLE IF NOT EXISTS job_wakeup_state (
    id int PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    requested_generation bigint NOT NULL DEFAULT 0,
    dispatched_generation bigint NOT NULL DEFAULT 0,
    acknowledged_generation bigint NOT NULL DEFAULT 0,
    dispatcher_requested boolean NOT NULL DEFAULT false,
    dispatcher_token uuid,
    dispatcher_lease_expires_at timestamptz,
    wave_id bigint NOT NULL DEFAULT 0,
    wave_outstanding smallint NOT NULL DEFAULT 0,
    next_dispatch_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO job_wakeup_state (id) VALUES (1) ON CONFLICT (id) DO NOTHING;

-- 5. Singleton bộ đếm commit-ordered invalidation cursor
CREATE TABLE IF NOT EXISTS sync_sequence_state (
    id int PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    value bigint NOT NULL DEFAULT 0,
    updated_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO sync_sequence_state (id, value) VALUES (1, 0) ON CONFLICT (id) DO NOTHING;

-- 6. Bảng lưu trữ durable metadata invalidation (không đưa vào supabase_realtime publication)
CREATE TABLE IF NOT EXISTS realtime_invalidations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    sequence bigint NOT NULL UNIQUE,
    group_id uuid NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    aggregate_type text NOT NULL,
    aggregate_id uuid NOT NULL,
    version bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_realtime_invalidations_agg_ver UNIQUE (aggregate_type, aggregate_id, version)
);

CREATE INDEX IF NOT EXISTS idx_realtime_invalidations_group_seq 
    ON realtime_invalidations (group_id, sequence);

CREATE INDEX IF NOT EXISTS idx_realtime_invalidations_seq 
    ON realtime_invalidations (sequence);

-- +goose Down
DROP TABLE IF EXISTS realtime_invalidations;
DROP TABLE IF EXISTS sync_sequence_state;
DROP TABLE IF EXISTS job_wakeup_state;
DROP TABLE IF EXISTS job_drain_slots;
DROP TABLE IF EXISTS app_jobs;
ALTER TABLE users DROP COLUMN IF EXISTS membership_sync_version;
ALTER TABLE bills DROP COLUMN IF EXISTS sync_version;
