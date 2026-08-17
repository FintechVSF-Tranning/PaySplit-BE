-- +goose Up

-- ---------------------------------------------------------------------------
-- Group management v1 (spec 0002): input checks, activity types, indexes
-- ---------------------------------------------------------------------------

-- AC-1: name is 1 to 100 Unicode code points after strings.TrimSpace, currency
-- is exactly VND in v1. Backfill existing rows to satisfy both checks before
-- adding them: 000001 only enforced CHECK (name <> ''), with no trim and no
-- currency check at all, so an untrimmed name or a non-VND currency is legal
-- under the old schema and would otherwise abort this migration outright.
UPDATE groups SET name = btrim(name) WHERE name <> btrim(name);
-- A pre-existing all-whitespace or over-length name would fail the trimmed
-- CHECK below and abort VALIDATE CONSTRAINT, so normalize those cases too.
UPDATE groups SET name = 'Unnamed Group' WHERE char_length(btrim(name)) = 0;
UPDATE groups SET name = substring(btrim(name) from 1 for 100) WHERE char_length(btrim(name)) > 100;
-- v1 has no other currency support and the API has never accepted a value
-- other than VND (usecase.CreateGroup rejects it before the repository is
-- reached), so normalize any stray legacy row to the only supported value
-- rather than let it block the migration.
UPDATE groups SET currency = 'VND' WHERE currency <> 'VND';

ALTER TABLE groups DROP CONSTRAINT IF EXISTS groups_name_check;
ALTER TABLE groups ADD CONSTRAINT groups_name_check
    CHECK (char_length(name) BETWEEN 1 AND 100 AND name = btrim(name)) NOT VALID;
ALTER TABLE groups DROP CONSTRAINT IF EXISTS groups_currency_check;
ALTER TABLE groups ADD CONSTRAINT groups_currency_check
    CHECK (currency = 'VND') NOT VALID;
-- Rows were backfilled above, so both validations pass without the long
-- ACCESS EXCLUSIVE table scan a plain CHECK add would take: VALIDATE
-- CONSTRAINT only needs SHARE UPDATE EXCLUSIVE, which does not block reads
-- or writes on a large groups table.
ALTER TABLE groups VALIDATE CONSTRAINT groups_name_check;
ALTER TABLE groups VALIDATE CONSTRAINT groups_currency_check;

-- AC-8: activity event types this feature writes.
DO $$ BEGIN ALTER TYPE activity_type ADD VALUE IF NOT EXISTS 'group_created'; EXCEPTION WHEN duplicate_object THEN null; END $$;
DO $$ BEGIN ALTER TYPE activity_type ADD VALUE IF NOT EXISTS 'invite_created'; EXCEPTION WHEN duplicate_object THEN null; END $$;
DO $$ BEGIN ALTER TYPE activity_type ADD VALUE IF NOT EXISTS 'invite_revoked'; EXCEPTION WHEN duplicate_object THEN null; END $$;
DO $$ BEGIN ALTER TYPE activity_type ADD VALUE IF NOT EXISTS 'member_joined'; EXCEPTION WHEN duplicate_object THEN null; END $$;
DO $$ BEGIN ALTER TYPE activity_type ADD VALUE IF NOT EXISTS 'member_reactivated'; EXCEPTION WHEN duplicate_object THEN null; END $$;
DO $$ BEGIN ALTER TYPE activity_type ADD VALUE IF NOT EXISTS 'member_left'; EXCEPTION WHEN duplicate_object THEN null; END $$;
DO $$ BEGIN ALTER TYPE activity_type ADD VALUE IF NOT EXISTS 'member_removed'; EXCEPTION WHEN duplicate_object THEN null; END $$;
DO $$ BEGIN ALTER TYPE activity_type ADD VALUE IF NOT EXISTS 'captain_transferred'; EXCEPTION WHEN duplicate_object THEN null; END $$;

-- AC-2: list groups selects the caller's active memberships, then seeks groups
-- by (created_at, id).
CREATE INDEX IF NOT EXISTS idx_group_members_user_active
    ON group_members(user_id, group_id)
    WHERE status = 'active';

CREATE INDEX IF NOT EXISTS idx_groups_cursor
    ON groups(created_at DESC, id DESC);

-- AC-3: one available invite per group, most recent first.
DROP INDEX IF EXISTS idx_group_invites_active;
CREATE INDEX IF NOT EXISTS idx_group_invites_candidate
    ON group_invites(group_id, created_at DESC, id DESC)
    WHERE revoked_at IS NULL;

-- AC-8: activity timeline is (group_id, created_at DESC, id DESC).
DROP INDEX IF EXISTS idx_group_activities_timeline;
CREATE INDEX IF NOT EXISTS idx_group_activities_timeline
    ON group_activities(group_id, created_at DESC, id DESC);

-- AC-6: member exit sums open payable/receivable totals per group per member.
DROP INDEX IF EXISTS idx_debts_debtor_unsettled;
DROP INDEX IF EXISTS idx_debts_creditor_unsettled;
CREATE INDEX IF NOT EXISTS idx_debts_group_debtor_unsettled
    ON debts(group_id, debtor_member_id) INCLUDE (amount)
    WHERE status <> 'settled';
CREATE INDEX IF NOT EXISTS idx_debts_group_creditor_unsettled
    ON debts(group_id, creditor_member_id) INCLUDE (amount)
    WHERE status <> 'settled';

-- +goose Down

DROP INDEX IF EXISTS idx_debts_group_creditor_unsettled;
DROP INDEX IF EXISTS idx_debts_group_debtor_unsettled;
CREATE INDEX IF NOT EXISTS idx_debts_debtor_unsettled   ON debts(debtor_member_id)   WHERE status <> 'settled';
CREATE INDEX IF NOT EXISTS idx_debts_creditor_unsettled ON debts(creditor_member_id) WHERE status <> 'settled';

DROP INDEX IF EXISTS idx_group_activities_timeline;
CREATE INDEX IF NOT EXISTS idx_group_activities_timeline ON group_activities(group_id, created_at DESC);

DROP INDEX IF EXISTS idx_group_invites_candidate;
CREATE INDEX IF NOT EXISTS idx_group_invites_active ON group_invites(group_id) WHERE revoked_at IS NULL;

DROP INDEX IF EXISTS idx_groups_cursor;
DROP INDEX IF EXISTS idx_group_members_user_active;

ALTER TABLE groups DROP CONSTRAINT IF EXISTS groups_currency_check;
ALTER TABLE groups DROP CONSTRAINT IF EXISTS groups_name_check;
ALTER TABLE groups ADD CONSTRAINT groups_name_check CHECK (name <> '');

-- activity_type enum values added above are intentionally not removed: Postgres
-- cannot drop enum values, and goose down here only needs to restore indexes/checks.
