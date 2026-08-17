-- name: CreateGroup :one
INSERT INTO groups (name, currency, created_by)
VALUES ($1, $2, $3)
RETURNING *;

-- name: CreateInitialCaptainMembership :one
INSERT INTO group_members (group_id, user_id, role, status)
VALUES ($1, $2, 'captain', 'active')
RETURNING *;

-- name: GetUserDisplayName :one
SELECT display_name FROM users WHERE id = $1;

-- name: ListActiveGroupsForUser :many
SELECT g.id, g.name, g.currency, g.created_by, g.created_at,
       m.id AS membership_id, m.role,
       (SELECT count(*) FROM group_members am WHERE am.group_id = g.id AND am.status = 'active') AS active_member_count
FROM group_members m
JOIN groups g ON g.id = m.group_id
WHERE m.user_id = $1
  AND m.status = 'active'
  AND (sqlc.narg('cursor_created_at')::timestamptz IS NULL
       OR (g.created_at, g.id) < (sqlc.narg('cursor_created_at')::timestamptz, sqlc.narg('cursor_id')::uuid))
ORDER BY g.created_at DESC, g.id DESC
LIMIT $2;

-- name: GetGroupByID :one
SELECT * FROM groups WHERE id = $1;

-- name: GetActiveMembership :one
SELECT * FROM group_members WHERE group_id = $1 AND user_id = $2 AND status = 'active';

-- name: ListActiveMembers :many
SELECT m.id AS membership_id, m.user_id, u.display_name, u.avatar_object_key, m.role, m.joined_at
FROM group_members m
JOIN users u ON u.id = m.user_id
WHERE m.group_id = $1 AND m.status = 'active'
ORDER BY m.joined_at ASC, m.id ASC;

-- name: ListGroupBalances :many
SELECT member_id, net_balance::bigint AS net_balance FROM v_member_balances WHERE group_id = $1;

-- name: LockGroup :one
SELECT id FROM groups WHERE id = $1 FOR UPDATE;

-- name: FindAvailableInvite :one
SELECT * FROM group_invites
WHERE group_id = $1
  AND revoked_at IS NULL
  AND expires_at > $2
  AND (max_uses IS NULL OR use_count < max_uses)
ORDER BY created_at DESC, id DESC
LIMIT 1
FOR UPDATE;

-- name: RevokeAvailableInvites :many
UPDATE group_invites
SET revoked_at = $2
WHERE group_id = $1
  AND revoked_at IS NULL
  AND expires_at > $2
  AND (max_uses IS NULL OR use_count < max_uses)
RETURNING *;

-- name: CreateInvite :one
INSERT INTO group_invites (group_id, code, created_by, expires_at, max_uses)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetInviteByIDForGroup :one
SELECT * FROM group_invites WHERE id = $1 AND group_id = $2 FOR UPDATE;

-- name: RevokeInviteByID :exec
UPDATE group_invites SET revoked_at = $2 WHERE id = $1;

-- name: GetInviteGroupIDByCode :one
SELECT group_id FROM group_invites WHERE code = $1;

-- name: GetInviteByCode :one
SELECT * FROM group_invites WHERE code = $1;

-- name: GetInviteByCodeForUpdate :one
SELECT * FROM group_invites WHERE code = $1 FOR UPDATE;

-- name: CountActiveMembers :one
SELECT count(*) FROM group_members WHERE group_id = $1 AND status = 'active';

-- name: GetActiveCaptainDisplayName :one
SELECT u.display_name
FROM group_members m
JOIN users u ON u.id = m.user_id
WHERE m.group_id = $1 AND m.role = 'captain' AND m.status = 'active';

-- name: GetMembershipByUserForUpdate :one
SELECT * FROM group_members WHERE group_id = $1 AND user_id = $2 FOR UPDATE;

-- name: IncrementInviteUse :exec
UPDATE group_invites SET use_count = use_count + 1 WHERE id = $1;

-- name: InsertMembership :one
INSERT INTO group_members (group_id, user_id, role, status)
VALUES ($1, $2, 'member', 'active')
RETURNING *;

-- name: ReactivateMembership :one
UPDATE group_members
SET status = 'active', role = 'member', left_at = NULL, joined_at = $2
WHERE id = $1
RETURNING *;

-- name: LockMembership :one
SELECT * FROM group_members WHERE id = $1 AND group_id = $2 FOR UPDATE;

-- name: SumOpenDebtorTotal :one
SELECT COALESCE(SUM(amount), 0)::bigint AS total
FROM debts
WHERE group_id = $1 AND debtor_member_id = $2 AND status <> 'settled';

-- name: SumOpenCreditorTotal :one
SELECT COALESCE(SUM(amount), 0)::bigint AS total
FROM debts
WHERE group_id = $1 AND creditor_member_id = $2 AND status <> 'settled';

-- name: MarkMembershipInactive :exec
UPDATE group_members SET status = 'inactive', left_at = $2 WHERE id = $1;

-- name: DemoteToMember :exec
UPDATE group_members SET role = 'member' WHERE id = $1;

-- name: PromoteToCaptain :exec
UPDATE group_members SET role = 'captain' WHERE id = $1;

-- name: ListGroupActivities :many
SELECT a.id, a.action_type, a.description, a.metadata, a.created_at,
       m.id AS actor_membership_id, m.user_id, u.display_name, u.avatar_object_key
FROM group_activities a
JOIN group_members m ON m.id = a.actor_member_id
JOIN users u ON u.id = m.user_id
WHERE a.group_id = $1
  AND (sqlc.narg('cursor_created_at')::timestamptz IS NULL
       OR (a.created_at, a.id) < (sqlc.narg('cursor_created_at')::timestamptz, sqlc.narg('cursor_id')::uuid))
ORDER BY a.created_at DESC, a.id DESC
LIMIT $2;
