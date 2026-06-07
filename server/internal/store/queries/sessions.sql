-- name: CreateSession :one
INSERT INTO yusui.sessions (pub_id, ticket_id, asset_id, agent_id, ssh_user, status, command_policy_snapshot)
VALUES ($1, $2, $3, $4, $5, 'allocated', $6)
RETURNING *;

-- name: SetSessionRunning :exec
UPDATE yusui.sessions SET status = 'running', opened_at = now() WHERE id = $1;

-- name: CloseSession :exec
UPDATE yusui.sessions
SET status = 'closed', closed_at = now(), closed_reason = $2, recording_uri = $3
WHERE id = $1 AND status <> 'closed';

-- name: GetSession :one
SELECT * FROM yusui.sessions WHERE id = $1;

-- name: ListSessionsByTicket :many
SELECT * FROM yusui.sessions WHERE ticket_id = $1 ORDER BY id DESC;

-- name: AddAttacher :one
INSERT INTO yusui.session_attachers (session_id, user_id, source, label, role, attached_at)
VALUES ($1, $2, $3, $4, $5, now())
RETURNING id;

-- name: DetachAttacher :exec
UPDATE yusui.session_attachers SET detached_at = now() WHERE id = $1;

-- name: InsertCommandFilterEvent :exec
INSERT INTO yusui.command_filter_events
  (session_id, rule_id, severity, action_taken, source, attacher_label, raw_line)
VALUES ($1, $2, $3, $4, $5, $6, $7);
