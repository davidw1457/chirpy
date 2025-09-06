-- name: CreateUser :one
INSERT INTO users (id, created_at, updated_at, email, hashed_password)
VALUES (gen_random_uuid(), NOW(), NOW(), $1, $2)
RETURNING *;

-- name: ResetUsers :exec
DELETE
FROM users;

-- name: GetUserByEmail :one
SELECT *
FROM users
WHERE email = $1;

-- name: CreateRefreshToken :one
INSERT INTO refresh_tokens (token, created_at, updated_at, user_id, expires_at)
VALUES ($1, NOW(), NOW(), $2, NOW() + INTERVAL '60 DAYS')
RETURNING *;

-- name: GetRefreshToken :one
SELECT *
FROM refresh_tokens
WHERE token = $1 AND revoked_at IS NULL AND expires_at > NOW();

-- name: RevokeRefreshToken :exec
UPDATE refresh_tokens
SET revoked_at = NOW(), updated_at = NOW()
WHERE token = $1;

-- name: UpdateUser :one
UPDATE users
SET email = $1, hashed_password = $2
WHERE id = $3
RETURNING users.*;

-- name: UpdateToChirpyRed :one
UPDATE users
SET is_chirpy_red = TRUE
WHERE id = $1
RETURNING users.*;

-- name: GetUserByID :one
SELECT *
FROM users
WHERE id = $1;
