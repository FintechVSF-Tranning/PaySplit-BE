-- name: CreateNotification :one
INSERT INTO notifications (user_id, type, title, body, payload)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetNotificationByID :one
SELECT *
FROM notifications
WHERE id = $1;

-- name: ListNotificationsByUserID :many
SELECT *
FROM notifications
WHERE user_id = $1
ORDER BY created_at DESC, id DESC
LIMIT $2 OFFSET $3;

-- name: CountNotificationsByUserID :one
SELECT COUNT(*)
FROM notifications
WHERE user_id = $1;

-- name: CountUnreadNotifications :one
SELECT COUNT(*)
FROM notifications
WHERE user_id = $1 AND read_at IS NULL;

-- name: MarkNotificationAsRead :execrows
UPDATE notifications
SET read_at = COALESCE(read_at, now())
WHERE id = $1 AND user_id = $2;

-- name: MarkAllNotificationsAsRead :exec
UPDATE notifications
SET read_at = now()
WHERE user_id = $1 AND read_at IS NULL;

-- name: GetActiveFCMTokenByUserID :one
-- Địa chỉ push thuộc về thiết bị, không thuộc về phiên đăng nhập: không lọc theo
-- trạng thái phiên ở đây, nếu không thì đúng nhóm người dùng lâu không mở app —
-- nhóm cần nhắc nợ nhất — sẽ không bao giờ nhận được thông báo.
SELECT fcm_token
FROM device_tokens
WHERE user_id = $1
ORDER BY updated_at DESC
LIMIT 1;

-- name: ClearFCMToken :exec
-- Gọi khi Firebase báo token không còn hợp lệ (người dùng gỡ app). Xoá hẳn hàng
-- thay vì set NULL: hàng không có token thì không còn ý nghĩa gì, và cột
-- fcm_token là NOT NULL.
DELETE FROM device_tokens
WHERE fcm_token = $1 AND user_id = $2;


