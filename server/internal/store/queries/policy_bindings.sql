-- name: UpsertBinding :exec
INSERT INTO yusui.policy_bindings (ticket_id, agent_id, agent_rule_id, src_peer_ips)
VALUES ($1, $2, $3, $4)
ON CONFLICT (ticket_id) DO UPDATE
SET agent_id = excluded.agent_id,
    agent_rule_id = excluded.agent_rule_id,
    src_peer_ips = excluded.src_peer_ips,
    updated_at = now();

-- name: SetBindingApplied :exec
UPDATE yusui.policy_bindings
SET agent_applied_at = now(), apply_attempts = apply_attempts + 1, updated_at = now()
WHERE ticket_id = $1;

-- name: GetBinding :one
SELECT * FROM yusui.policy_bindings WHERE ticket_id = $1;
