-- name: CreateUser :one
INSERT INTO users (id, email, display_name, password_hash)
VALUES ($1, $2, $3, $4)
RETURNING id, email, display_name, password_hash, role, created_at;

-- name: GetUserByEmail :one
SELECT id, email, display_name, password_hash, role, created_at
FROM users
WHERE email = $1
LIMIT 1;
