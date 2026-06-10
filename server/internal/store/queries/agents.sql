-- name: CreateAgent :one
-- Admin-created agent; enrollment defaults to 'approved' (trusted, docs/11 §11.2).
INSERT INTO yusui.agents (project_id, role, hostname)
VALUES ($1, $2, $3)
RETURNING *;

-- name: CreatePendingAgent :one
-- Auto-registered (unknown) agent; explicitly 'pending' until admin approval.
INSERT INTO yusui.agents (project_id, role, hostname, enrollment)
VALUES ($1, $2, $3, 'pending')
RETURNING *;

-- name: GetAgentByID :one
SELECT * FROM yusui.agents WHERE id = $1;

-- name: GetAgentByProjectAndHostname :one
-- Register lookup: find this daemon's row regardless of enrollment state.
SELECT * FROM yusui.agents WHERE project_id = $1 AND hostname = $2;

-- name: ListAgents :many
SELECT * FROM yusui.agents ORDER BY id;

-- name: ListAgentsByProject :many
SELECT * FROM yusui.agents WHERE project_id = $1 ORDER BY id;

-- name: GetPrimaryAgentForProject :one
-- Used by the Policy Engine to pick the rule target: only an APPROVED primary
-- ever receives per-ticket rules (pending/rejected agents get nothing, docs/11).
SELECT * FROM yusui.agents WHERE project_id = $1 AND role = 'primary' AND enrollment = 'approved';

-- name: ApproveAgent :one
-- Admin approval: flip to approved and bind the NetBird setup key (P2 fills it;
-- P1 may pass NULL). Idempotent on an already-approved row.
UPDATE yusui.agents
SET enrollment = 'approved', netbird_setup_key = $2
WHERE id = $1
RETURNING *;

-- name: RejectAgent :one
UPDATE yusui.agents SET enrollment = 'rejected' WHERE id = $1 RETURNING *;

-- name: SetAgentStatus :exec
UPDATE yusui.agents SET status = $2, last_seen_at = now() WHERE id = $1;
