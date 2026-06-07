-- name: CreateAgent :one
INSERT INTO yusui.agents (project_id, role, hostname)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetAgentByID :one
SELECT * FROM yusui.agents WHERE id = $1;

-- name: ListAgents :many
SELECT * FROM yusui.agents ORDER BY id;

-- name: ListAgentsByProject :many
SELECT * FROM yusui.agents WHERE project_id = $1 ORDER BY id;

-- name: GetPrimaryAgentForProject :one
SELECT * FROM yusui.agents WHERE project_id = $1 AND role = 'primary';

-- name: SetAgentStatus :exec
UPDATE yusui.agents SET status = $2, last_seen_at = now() WHERE id = $1;
