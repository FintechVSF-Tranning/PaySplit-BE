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
-- Mỗi hàng mang đủ dữ liệu cho một thẻ nhóm ở FE nên danh sách không cần
-- gọi thêm GET /groups/{id} cho từng nhóm (báo cáo đối chiếu mục 3.2, 3.3).
SELECT g.id, g.name, g.currency, g.created_by, g.created_at,
       g.bill_submission_locked_at,
       m.id AS membership_id, m.role,
       (SELECT count(*) FROM group_members am WHERE am.group_id = g.id AND am.status = 'active') AS active_member_count,
       -- Số dư ròng của chính caller trong nhóm; v_member_balances chỉ có hàng
       -- khi thành viên có công nợ nên COALESCE về 0 cho nhóm chưa phát sinh.
       COALESCE((SELECT b.net_balance FROM v_member_balances b WHERE b.member_id = m.id), 0)::bigint AS caller_net_balance,
       -- Hóa đơn chưa chốt, dùng chung định nghĩa với CountUnfinishedBills.
       (SELECT count(*) FROM bills bi WHERE bi.group_id = g.id AND bi.status NOT IN ('finalized', 'voided')) AS pending_bill_count,
       -- Hoạt động gần nhất, NULL khi nhóm chưa có hoạt động nào. Gói thành
       -- jsonb thay vì hai cột phẳng vì sqlc suy cột phẳng lấy từ subquery
       -- thành NOT NULL (description là NOT NULL trong bảng) và sẽ vỡ khi
       -- scan NULL. Truy vấn dùng idx_group_activities_timeline.
       (SELECT jsonb_build_object('description', a.description, 'created_at', a.created_at)
        FROM group_activities a
        WHERE a.group_id = g.id
        ORDER BY a.created_at DESC, a.id DESC LIMIT 1) AS last_activity
FROM group_members m
JOIN groups g ON g.id = m.group_id
WHERE m.user_id = $1
  AND m.status = 'active'
  AND g.status = 'active'
  AND (sqlc.narg('cursor_created_at')::timestamptz IS NULL
       OR (g.created_at, g.id) < (sqlc.narg('cursor_created_at')::timestamptz, sqlc.narg('cursor_id')::uuid))
ORDER BY g.created_at DESC, g.id DESC
LIMIT $2;

-- name: GetGroupByID :one
SELECT * FROM groups WHERE id = $1 AND status = 'active';

-- name: GetActiveMembership :one
SELECT * FROM group_members WHERE group_id = $1 AND user_id = $2 AND status = 'active';

-- name: ListActiveMembers :many
SELECT m.id AS membership_id, m.user_id, u.display_name, u.avatar_object_key, m.role, m.joined_at
FROM group_members m
JOIN users u ON u.id = m.user_id
WHERE m.group_id = $1 AND m.status = 'active'
ORDER BY m.joined_at ASC, m.id ASC;

-- name: ListGroupBalances :many
SELECT b.member_id, b.net_balance::bigint AS net_balance
FROM v_member_balances b
JOIN group_members m ON m.id = b.member_id AND m.group_id = b.group_id
WHERE b.group_id = $1 AND m.status = 'active'
ORDER BY m.joined_at ASC, m.id ASC;

-- name: LockGroup :one
SELECT id FROM groups WHERE id = $1 AND status = 'active' FOR UPDATE;

-- name: FindAvailableInvite :one
SELECT * FROM group_invites
WHERE group_id = $1
  AND revoked_at IS NULL
  AND expires_at > $2
  AND (max_uses IS NULL OR use_count < max_uses)
ORDER BY created_at DESC, id DESC
LIMIT 1
FOR UPDATE;

-- name: ListAvailableInvites :many
SELECT * FROM group_invites
WHERE group_id = $1
  AND revoked_at IS NULL
  AND expires_at > $2
  AND (max_uses IS NULL OR use_count < max_uses)
ORDER BY created_at DESC, id DESC;

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
SELECT i.group_id
FROM group_invites i
JOIN groups g ON g.id = i.group_id AND g.status = 'active'
WHERE i.code = $1;

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
WHERE group_id = $1 AND debtor_member_id = $2 AND status NOT IN ('settled', 'voided');

-- name: SumOpenCreditorTotal :one
SELECT COALESCE(SUM(amount), 0)::bigint AS total
FROM debts
WHERE group_id = $1 AND creditor_member_id = $2 AND status NOT IN ('settled', 'voided');

-- name: MarkMembershipInactive :exec
UPDATE group_members SET status = 'inactive', left_at = $2 WHERE id = $1;

-- name: DemoteToMember :exec
UPDATE group_members SET role = 'member' WHERE id = $1;

-- name: PromoteToCaptain :exec
UPDATE group_members SET role = 'captain' WHERE id = $1;

-- name: RenameGroup :one
UPDATE groups
SET name = $2
WHERE id = $1 AND status = 'active'
RETURNING *;

-- name: CountUnfinishedBills :one
SELECT count(*) FROM bills
WHERE group_id = $1 AND status NOT IN ('finalized', 'voided');

-- name: CountOpenDebts :one
SELECT count(*) FROM debts
WHERE group_id = $1 AND status NOT IN ('settled', 'voided');

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

-- ============================================================================
-- GROUP BILL CLOSE V1 (Spec 0008): điều hướng batch của Captain và rào chắn archive
-- ============================================================================

-- name: GetBillFinalizeBatchNavigation :one
SELECT
    (SELECT b.id FROM group_bill_finalize_batches b
     WHERE b.group_id = $1 AND b.status IN ('queued', 'processing')
     ORDER BY b.created_at ASC LIMIT 1) AS active_batch_id,
    (SELECT b.id FROM group_bill_finalize_batches b
     WHERE b.group_id = $1
     ORDER BY b.created_at DESC, b.id DESC LIMIT 1) AS latest_batch_id;

-- name: BumpRosterVersion :one
UPDATE groups SET roster_version = roster_version + 1 WHERE id = $1 RETURNING roster_version;

-- name: InsertGroupEvent :exec
INSERT INTO group_events (group_id, version, event_type, payload) VALUES ($1, $2, $3, $4);

-- name: GetGroupSyncCursor :one
SELECT g.roster_version::bigint AS current_version,
       COALESCE((SELECT MIN(e.version) FROM group_events e WHERE e.group_id = g.id), 0)::bigint AS oldest_version
FROM groups g
WHERE g.id = $1;

-- name: ListGroupEventsSince :many
SELECT version, event_type, payload, created_at
FROM group_events
WHERE group_id = $1 AND version > $2
ORDER BY version ASC
LIMIT $3;

-- name: GetMemberSnapshot :one
SELECT m.id AS membership_id, m.user_id, u.display_name, u.avatar_object_key, m.role, m.joined_at
FROM group_members m
JOIN users u ON u.id = m.user_id
WHERE m.id = $1 AND m.group_id = $2;

-- name: DeleteGroupEventsBefore :execrows
DELETE FROM group_events WHERE created_at < $1;
