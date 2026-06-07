-- name: CreateAsset :one
INSERT INTO yusui.assets (project_id, name, ip_internal, ports, os, tags)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetAssetByID :one
SELECT * FROM yusui.assets WHERE id = $1;

-- name: ListAssets :many
SELECT * FROM yusui.assets ORDER BY id;

-- name: ListAssetsByProject :many
SELECT * FROM yusui.assets WHERE project_id = $1 ORDER BY id;

-- name: ListAssetsByIDs :many
SELECT * FROM yusui.assets WHERE id = ANY(@ids::bigint[]) ORDER BY id;
