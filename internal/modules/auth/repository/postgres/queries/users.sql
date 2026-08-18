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

-- name: UpdateSessionFCMToken :execrows
UPDATE sessions
SET fcm_token = $2
WHERE id = $1 AND revoked_at IS NULL;

