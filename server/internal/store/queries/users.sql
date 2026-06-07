-- name: CreateUser :one
INSERT INTO yusui.users (username, display_name, email, role, password_hash, mfa_secret_enc, mfa_enabled)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetUserByUsername :one
SELECT * FROM yusui.users WHERE username = $1;

-- name: GetUserByID :one
SELECT * FROM yusui.users WHERE id = $1;

-- name: CountUsers :one
SELECT count(*) FROM yusui.users;

-- name: ListUsers :many
-- Excludes password_hash / mfa_secret_enc — never list secrets.
SELECT id, username, display_name, email, role, mfa_enabled, is_active, last_login_at, created_at
FROM yusui.users ORDER BY id;

-- name: MarkLoginSuccess :exec
UPDATE yusui.users
SET last_login_at = now(), failed_login_count = 0, locked_until = NULL, updated_at = now()
WHERE id = $1;

-- name: MarkLoginFailure :one
UPDATE yusui.users
SET failed_login_count = failed_login_count + 1,
    locked_until = CASE WHEN failed_login_count + 1 >= 5 THEN now() + interval '15 minutes' ELSE locked_until END,
    updated_at = now()
WHERE id = $1
RETURNING failed_login_count, locked_until;
