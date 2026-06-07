-- name: CreateProject :one
INSERT INTO yusui.projects (code, name, cidrs)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetProjectByID :one
SELECT * FROM yusui.projects WHERE id = $1;

-- name: GetProjectByCode :one
SELECT * FROM yusui.projects WHERE code = $1;

-- name: ListProjects :many
SELECT * FROM yusui.projects ORDER BY id;
