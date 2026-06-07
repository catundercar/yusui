-- name: InsertAudit :exec
-- Append-only (yusui_app has INSERT only on this table — invariant #7).
INSERT INTO yusui.audit_logs (actor_type, actor_id, action, target_type, target_id, payload)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: ListAuditByTarget :many
SELECT * FROM yusui.audit_logs
WHERE target_type = $1 AND target_id = $2
ORDER BY ts DESC, id DESC
LIMIT $3;

-- name: ListRecentAudit :many
SELECT * FROM yusui.audit_logs ORDER BY ts DESC, id DESC LIMIT $1;
