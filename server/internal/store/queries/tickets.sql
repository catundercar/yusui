-- name: CreateTicket :one
INSERT INTO yusui.tickets
  (pub_id, requester_id, project_id, target_selector, ports, protocol, access_kind, reason, duration_sec, status)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'pending')
RETURNING *;

-- name: GetTicketByID :one
SELECT * FROM yusui.tickets WHERE id = $1;

-- name: GetTicketByPubID :one
SELECT * FROM yusui.tickets WHERE pub_id = $1;

-- name: GetTicketForUpdate :one
SELECT * FROM yusui.tickets WHERE id = $1 FOR UPDATE;

-- name: ListTickets :many
SELECT * FROM yusui.tickets ORDER BY id DESC;

-- name: ListTicketsByRequester :many
SELECT * FROM yusui.tickets WHERE requester_id = $1 ORDER BY id DESC;

-- name: SetTicketApproved :exec
UPDATE yusui.tickets
SET approver_id = $2, approved_at = now(), frozen_asset_ids = $3, status = 'approved', updated_at = now()
WHERE id = $1;

-- name: SetTicketActive :exec
UPDATE yusui.tickets
SET status = 'active', activated_at = now(), expires_at = $2, updated_at = now()
WHERE id = $1;

-- name: SetTicketStatus :exec
UPDATE yusui.tickets SET status = $2, updated_at = now() WHERE id = $1;

-- name: CloseTicket :exec
UPDATE yusui.tickets SET status = $2, closed_at = now(), updated_at = now() WHERE id = $1;

-- name: ListExpiredActiveTickets :many
SELECT * FROM yusui.tickets
WHERE status = 'active' AND expires_at IS NOT NULL AND expires_at <= now()
ORDER BY id;

-- name: ListActiveTickets :many
-- Not-yet-expired active tickets — used to rebuild the in-memory forward map
-- after a Server restart (docs/10).
SELECT * FROM yusui.tickets
WHERE status = 'active' AND (expires_at IS NULL OR expires_at > now())
ORDER BY id;
