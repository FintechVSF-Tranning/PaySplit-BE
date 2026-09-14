-- name: CreateUser :one
INSERT INTO users (email, phone_number, display_name, password_hash)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetUserByEmail :one
SELECT *
FROM users
WHERE email = $1
LIMIT 1;

-- name: GetUserByID :one
SELECT *
FROM users
WHERE id = $1
LIMIT 1;

-- FCM token không còn nằm trên bảng sessions. Việc ghi nó vào `device_tokens`
-- được viết tay trong repository (UpdateSessionFCMToken) vì cần hai bước trong
-- một transaction: tra (user_id, device_id) từ phiên rồi mới upsert.

