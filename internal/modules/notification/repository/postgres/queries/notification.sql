-- name: CreateNotification :one
INSERT INTO notifications (user_id, type, title, body, payload)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListNotificationsByUserID :many
SELECT *
FROM notifications
WHERE user_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: CountUnreadNotifications :one
SELECT COUNT(*)
FROM notifications
WHERE user_id = $1 AND read_at IS NULL;

-- name: MarkNotificationAsRead :execrows
UPDATE notifications
SET read_at = now()
WHERE id = $1 AND user_id = $2 AND read_at IS NULL;

-- name: MarkAllNotificationsAsRead :exec
UPDATE notifications
SET read_at = now()
WHERE user_id = $1 AND read_at IS NULL;

-- name: GetActiveFCMTokenByUserID :one
SELECT fcm_token
FROM sessions
WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > now() AND fcm_token IS NOT NULL AND fcm_token <> ''
ORDER BY issued_at DESC
LIMIT 1;

-- name: ClearFCMToken :exec
UPDATE sessions
SET fcm_token = NULL
WHERE fcm_token = $1;

