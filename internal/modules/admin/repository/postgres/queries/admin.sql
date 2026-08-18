-- name: ListAccounts :many
SELECT
    id,
    email,
    display_name,
    avatar_object_key,
    phone_number,
    role,
    status,
    email_verified_at,
    created_at,
    updated_at
FROM users
WHERE
    (sqlc.narg('search')::text IS NULL OR sqlc.narg('search')::text = '' OR email ILIKE '%' || sqlc.narg('search')::text || '%' OR display_name ILIKE '%' || sqlc.narg('search')::text || '%' OR phone_number ILIKE '%' || sqlc.narg('search')::text || '%')
    AND (sqlc.narg('status')::text IS NULL OR sqlc.narg('status')::text = '' OR status = sqlc.narg('status')::account_status)
    AND (sqlc.narg('role')::text IS NULL OR sqlc.narg('role')::text = '' OR role = sqlc.narg('role')::user_role)
ORDER BY
    CASE WHEN @sort_by::text = 'email' AND @sort_order::text = 'asc' THEN email END ASC,
    CASE WHEN @sort_by::text = 'email' AND @sort_order::text = 'desc' THEN email END DESC,
    CASE WHEN @sort_by::text = 'display_name' AND @sort_order::text = 'asc' THEN display_name END ASC,
    CASE WHEN @sort_by::text = 'display_name' AND @sort_order::text = 'desc' THEN display_name END DESC,
    CASE WHEN @sort_by::text = 'created_at' AND @sort_order::text = 'asc' THEN created_at END ASC,
    CASE WHEN (@sort_by::text = 'created_at' OR @sort_by::text = '' OR @sort_by::text IS NULL) AND (@sort_order::text = 'desc' OR @sort_order::text = '' OR @sort_order::text IS NULL) THEN created_at END DESC,
    created_at DESC
LIMIT @page_limit OFFSET @page_offset;

-- name: CountAccounts :one
SELECT count(*)::bigint AS total
FROM users
WHERE
    (sqlc.narg('search')::text IS NULL OR sqlc.narg('search')::text = '' OR email ILIKE '%' || sqlc.narg('search')::text || '%' OR display_name ILIKE '%' || sqlc.narg('search')::text || '%' OR phone_number ILIKE '%' || sqlc.narg('search')::text || '%')
    AND (sqlc.narg('status')::text IS NULL OR sqlc.narg('status')::text = '' OR status = sqlc.narg('status')::account_status)
    AND (sqlc.narg('role')::text IS NULL OR sqlc.narg('role')::text = '' OR role = sqlc.narg('role')::user_role);

-- name: GetAccountByID :one
SELECT
    id,
    email,
    display_name,
    avatar_object_key,
    phone_number,
    default_bank_code,
    default_bank_account_number,
    default_bank_account_holder,
    role,
    status,
    email_verified_at,
    created_at,
    updated_at,
    failed_login_count,
    login_blocked_until
FROM users
WHERE id = @id;

-- name: CountActiveSessionsByUserID :one
SELECT count(*)::bigint AS active_sessions_count
FROM sessions
WHERE user_id = @user_id AND revoked_at IS NULL AND expires_at > now();

-- name: ListGroupMembershipsByUserID :many
SELECT
    gm.group_id,
    g.name AS group_name,
    gm.role AS member_role,
    gm.status AS member_status,
    gm.joined_at
FROM group_members gm
JOIN groups g ON g.id = gm.group_id
WHERE gm.user_id = @user_id
ORDER BY gm.joined_at DESC;

-- name: GetOutstandingDebtsByUserID :one
SELECT
    COALESCE(count(d.id), 0)::bigint AS outstanding_debts_count,
    COALESCE(sum(d.amount), 0)::bigint AS total_debt_amount_vnd
FROM debts d
JOIN group_members gm ON gm.id = d.debtor_member_id
WHERE gm.user_id = @user_id AND d.status <> 'settled';

-- name: GetOutstandingCreditsByUserID :one
SELECT
    COALESCE(count(d.id), 0)::bigint AS outstanding_credits_count,
    COALESCE(sum(d.amount), 0)::bigint AS total_credit_amount_vnd
FROM debts d
JOIN group_members gm ON gm.id = d.creditor_member_id
WHERE gm.user_id = @user_id AND d.status <> 'settled';

-- name: ListRecentAuditLogsByTargetUserID :many
SELECT
    al.id,
    al.admin_id,
    u.email AS admin_email,
    al.target_user_id,
    al.action,
    al.reason,
    al.created_at
FROM admin_audit_logs al
JOIN users u ON u.id = al.admin_id
WHERE al.target_user_id = @target_user_id
ORDER BY al.created_at DESC
LIMIT 10;

-- name: UpdateUserStatus :one
UPDATE users
SET status = @status, updated_at = now()
WHERE id = @id
RETURNING
    id,
    email,
    display_name,
    avatar_object_key,
    phone_number,
    role,
    status,
    email_verified_at,
    created_at,
    updated_at;

-- name: RevokeSessionsByUserID :exec
UPDATE sessions
SET revoked_at = now(), revoked_reason = @revoked_reason
WHERE user_id = @user_id AND revoked_at IS NULL;

-- name: RevokeRefreshTokensByUserID :exec
UPDATE session_refresh_tokens
SET revoked_at = now()
WHERE session_id IN (SELECT id FROM sessions WHERE user_id = @user_id) AND revoked_at IS NULL;

-- name: CreateAdminAuditLog :one
INSERT INTO admin_audit_logs (admin_id, target_user_id, action, reason)
VALUES (@admin_id, @target_user_id, @action, @reason)
RETURNING id, admin_id, target_user_id, action, reason, created_at;

-- name: GetSystemUsersOverview :one
SELECT
    count(*)::bigint AS total_users,
    count(*) FILTER (WHERE status = 'active')::bigint AS active_users,
    count(*) FILTER (WHERE status = 'pending_verification')::bigint AS pending_verification_users,
    count(*) FILTER (WHERE status = 'suspended')::bigint AS suspended_users,
    count(*) FILTER (WHERE status = 'locked')::bigint AS locked_users
FROM users;

-- name: GetSystemGroupsOverview :one
SELECT count(*)::bigint AS total_groups FROM groups;

-- name: GetSystemBillsOverview :one
SELECT
    count(*) FILTER (WHERE status = 'finalized')::bigint AS finalized_bills,
    count(*) FILTER (WHERE status = 'draft')::bigint AS draft_bills
FROM bills;

-- name: GetSystemDebtsOverview :one
SELECT
    count(*) FILTER (WHERE status = 'awaiting')::bigint AS awaiting_debts,
    count(*) FILTER (WHERE status = 'pending_confirmation')::bigint AS pending_confirmation_debts,
    count(*) FILTER (WHERE status = 'stalled_confirmation')::bigint AS stalled_confirmation_debts,
    count(*) FILTER (WHERE status = 'rejected')::bigint AS rejected_debts,
    count(*) FILTER (WHERE status = 'settled')::bigint AS settled_debts
FROM debts;

-- name: GetSystemMediaCleanupOverview :one
SELECT count(*)::bigint AS pending_cleanup_jobs FROM media_cleanup_jobs WHERE completed_at IS NULL;

-- name: GetSystemOCRJobsOverview :one
SELECT
    count(*) FILTER (WHERE status = 'queued')::bigint AS queued_jobs,
    count(*) FILTER (WHERE status = 'processing')::bigint AS processing_jobs,
    count(*) FILTER (WHERE status = 'succeeded')::bigint AS succeeded_jobs,
    count(*) FILTER (WHERE status = 'failed')::bigint AS failed_jobs
FROM ocr_jobs;
