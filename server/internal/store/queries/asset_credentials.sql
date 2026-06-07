-- name: CreateAssetCredential :one
INSERT INTO yusui.asset_credentials
  (asset_id, ssh_user, auth_kind, secret_enc, secret_kms_key_id, fingerprint, description)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, asset_id, ssh_user, auth_kind, secret_kms_key_id, fingerprint, description, created_at, rotated_at, is_active;

-- name: GetActiveCredentialForAsset :one
-- Returns the full row including secret_enc (Web Shell decrypts in-process).
SELECT * FROM yusui.asset_credentials
WHERE asset_id = $1 AND is_active = TRUE
ORDER BY id DESC
LIMIT 1;

-- name: ListCredentialsForAsset :many
-- Excludes secret_enc — never list secrets.
SELECT id, asset_id, ssh_user, auth_kind, secret_kms_key_id, fingerprint, description, created_at, rotated_at, is_active
FROM yusui.asset_credentials
WHERE asset_id = $1
ORDER BY id;
