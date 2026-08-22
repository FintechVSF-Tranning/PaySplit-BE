-- +goose Up

-- Group governance and member shareable invites (spec 0002 AC-3, AC-9 to AC-12).
-- This migration is deliberately transactional. Traffic and workers stay stopped
-- until the compatible API has been deployed because legacy codes are rotated.

-- +goose StatementBegin
DO $$
DECLARE
    invalid_group_ids uuid[];
BEGIN
    SELECT array_agg(group_id ORDER BY group_id)
    INTO invalid_group_ids
    FROM (
        SELECT g.id AS group_id
        FROM groups g
        LEFT JOIN group_members gm
          ON gm.group_id = g.id
         AND gm.status = 'active'
         AND gm.role = 'captain'
        GROUP BY g.id
        HAVING count(gm.id) <> 1
    ) malformed;

    IF invalid_group_ids IS NOT NULL THEN
        RAISE EXCEPTION 'group governance preflight failed; expected exactly one active Captain for groups: %',
            array_to_string(invalid_group_ids, ', ');
    END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    CREATE TYPE group_status AS ENUM ('active', 'archived');
EXCEPTION
    WHEN duplicate_object THEN NULL;
END $$;
-- +goose StatementEnd

ALTER TABLE groups
    ADD COLUMN status group_status NOT NULL DEFAULT 'active';

-- +goose StatementBegin
DO $$ BEGIN ALTER TYPE activity_type ADD VALUE IF NOT EXISTS 'group_renamed'; EXCEPTION WHEN duplicate_object THEN NULL; END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN ALTER TYPE activity_type ADD VALUE IF NOT EXISTS 'group_archived'; EXCEPTION WHEN duplicate_object THEN NULL; END $$;
-- +goose StatementEnd

-- Old links are intentionally invalidated. Move every code outside the final
-- namespace first so deterministic replacements cannot collide with legacy data.
UPDATE group_invites
SET revoked_at = COALESCE(revoked_at, transaction_timestamp()),
    code = 'legacy-' || replace(id::text, '-', '');

-- +goose StatementBegin
DO $$
DECLARE
    invite_row record;
    alphabet constant text := 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789';
    ordinal_value bigint := 0;
    remaining bigint;
    encoded text;
    digit_position integer;
BEGIN
    IF (SELECT count(*) FROM group_invites) >= 218340105584896 THEN
        RAISE EXCEPTION 'too many legacy group invites for the eight character Base62 namespace';
    END IF;

    FOR invite_row IN SELECT id FROM group_invites ORDER BY id LOOP
        remaining := ordinal_value;
        encoded := '';
        FOR digit_position IN 1..8 LOOP
            encoded := substr(alphabet, (remaining % 62)::integer + 1, 1) || encoded;
            remaining := remaining / 62;
        END LOOP;

        UPDATE group_invites SET code = encoded WHERE id = invite_row.id;
        ordinal_value := ordinal_value + 1;
    END LOOP;
END $$;
-- +goose StatementEnd

ALTER TABLE group_invites
    ADD CONSTRAINT group_invites_code_base62_check
    CHECK (code ~ '^[A-Za-z0-9]{8}$') NOT VALID;
ALTER TABLE group_invites
    VALIDATE CONSTRAINT group_invites_code_base62_check;

-- +goose Down

ALTER TABLE group_invites
    DROP CONSTRAINT IF EXISTS group_invites_code_base62_check;

ALTER TABLE groups
    DROP COLUMN IF EXISTS status;

DROP TYPE IF EXISTS group_status;

-- group_renamed and group_archived intentionally remain in activity_type.
-- PostgreSQL enum values cannot be removed safely, and rotated codes cannot be
-- reconstructed. Production rollout is therefore forward fix only.
