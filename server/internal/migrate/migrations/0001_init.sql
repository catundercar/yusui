-- +goose Up
-- YuSui v0.1 initial schema — 13 tables (docs/06 §6.2 + docs/09 §9.8).
-- Constraints live in DDL, not the app layer (CLAUDE.md / docs/06 §6.3).
-- All objects are schema-qualified (yusui.*) so both sqlc (compile-time) and the
-- runtime role resolve them without depending on search_path.
--
-- Build-order deviation (documented): docs/06 marks projects.netbird_group_id
-- and agents.netbird_peer_id NOT NULL. NetBird is introduced at M4, so they are
-- nullable here and populated by the NetBird Adapter at M4; a later migration
-- may re-add NOT NULL once the overlay is in place.

CREATE SCHEMA yusui;

-- ---- users ---------------------------------------------------------------
CREATE TABLE yusui.users (
  id                  BIGSERIAL PRIMARY KEY,
  username            TEXT NOT NULL UNIQUE,
  display_name        TEXT,
  email               TEXT UNIQUE,
  role                TEXT NOT NULL CHECK (role IN ('requester','approver','admin')),
  password_hash       TEXT,
  password_alg        TEXT NOT NULL DEFAULT 'bcrypt',
  password_changed_at TIMESTAMPTZ,
  mfa_secret_enc      BYTEA,
  mfa_enabled         BOOLEAN NOT NULL DEFAULT FALSE,
  failed_login_count  INT NOT NULL DEFAULT 0,
  locked_until        TIMESTAMPTZ,
  last_login_at       TIMESTAMPTZ,
  external_id         TEXT UNIQUE,
  external_issuer     TEXT,
  is_active           BOOLEAN NOT NULL DEFAULT TRUE,
  created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK ((password_hash IS NOT NULL) OR (external_id IS NOT NULL))
);
CREATE INDEX ON yusui.users(is_active);
CREATE INDEX ON yusui.users(external_id) WHERE external_id IS NOT NULL;

-- ---- projects ------------------------------------------------------------
CREATE TABLE yusui.projects (
  id               BIGSERIAL PRIMARY KEY,
  code             TEXT NOT NULL UNIQUE,
  name             TEXT NOT NULL,
  cidrs            CIDR[] NOT NULL,
  netbird_group_id TEXT UNIQUE,                 -- M4 populates (docs/06: NOT NULL)
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (array_length(cidrs, 1) >= 1)
);

-- ---- agents --------------------------------------------------------------
CREATE TABLE yusui.agents (
  id               BIGSERIAL PRIMARY KEY,
  project_id       BIGINT NOT NULL REFERENCES yusui.projects(id) ON DELETE RESTRICT,
  role             TEXT NOT NULL CHECK (role IN ('primary','secondary')),
  hostname         TEXT NOT NULL,
  netbird_peer_id  TEXT UNIQUE,                 -- M4 populates (docs/06: NOT NULL)
  netbird_route_id TEXT,
  agent_version    TEXT,
  cert_fingerprint TEXT,
  status           TEXT NOT NULL DEFAULT 'unknown'
                     CHECK (status IN ('unknown','online','offline','degraded','frozen')),
  last_seen_at     TIMESTAMPTZ,
  registered_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (project_id, role)
);
CREATE INDEX ON yusui.agents(project_id);
CREATE INDEX ON yusui.agents(status);

-- ---- assets --------------------------------------------------------------
CREATE TABLE yusui.assets (
  id          BIGSERIAL PRIMARY KEY,
  project_id  BIGINT NOT NULL REFERENCES yusui.projects(id) ON DELETE RESTRICT,
  name        TEXT NOT NULL,
  ip_internal INET NOT NULL,
  ports       INT[] NOT NULL DEFAULT '{}',
  os          TEXT,
  tags        JSONB NOT NULL DEFAULT '{}',
  source      TEXT NOT NULL DEFAULT 'manual' CHECK (source IN ('manual','agent_probe','import')),
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (project_id, ip_internal)
);
CREATE INDEX ON yusui.assets USING GIN(tags);
CREATE INDEX ON yusui.assets(project_id);

-- ---- asset_credentials ---------------------------------------------------
CREATE TABLE yusui.asset_credentials (
  id                BIGSERIAL PRIMARY KEY,
  asset_id          BIGINT NOT NULL REFERENCES yusui.assets(id) ON DELETE CASCADE,
  ssh_user          TEXT NOT NULL,
  auth_kind         TEXT NOT NULL CHECK (auth_kind IN ('key','password')),
  secret_enc        BYTEA NOT NULL,
  secret_kms_key_id TEXT NOT NULL,
  fingerprint       TEXT,
  description       TEXT,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  rotated_at        TIMESTAMPTZ,
  is_active         BOOLEAN NOT NULL DEFAULT TRUE,
  UNIQUE (asset_id, ssh_user, is_active)
);
CREATE INDEX ON yusui.asset_credentials(asset_id) WHERE is_active = TRUE;

-- ---- command_policies ----------------------------------------------------
CREATE TABLE yusui.command_policies (
  id         BIGSERIAL PRIMARY KEY,
  code       TEXT NOT NULL UNIQUE,
  name       TEXT NOT NULL,
  is_builtin BOOLEAN NOT NULL DEFAULT FALSE,
  rules      JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ---- tickets -------------------------------------------------------------
CREATE TABLE yusui.tickets (
  id               BIGSERIAL PRIMARY KEY,
  pub_id           TEXT NOT NULL UNIQUE,             -- ULID, UI-facing
  requester_id     BIGINT NOT NULL REFERENCES yusui.users(id),
  approver_id      BIGINT REFERENCES yusui.users(id),
  project_id       BIGINT NOT NULL REFERENCES yusui.projects(id),
  target_selector  JSONB NOT NULL,                   -- {"asset_ids":[..]} | {"tags":{..}}
  frozen_asset_ids BIGINT[],                         -- frozen at approval (docs/06 §6.8.1)
  ports            INT[] NOT NULL,
  protocol         TEXT NOT NULL DEFAULT 'tcp' CHECK (protocol IN ('tcp','udp','any')),
  access_kind      TEXT NOT NULL DEFAULT 'web_shell'
                     CHECK (access_kind IN ('web_shell','jumpserver')),
  reason           TEXT NOT NULL,
  duration_sec     INT NOT NULL CHECK (duration_sec BETWEEN 60 AND 86400),
  status           TEXT NOT NULL CHECK (status IN
                     ('draft','pending','approved','active','revoking',
                      'closed','rejected','expired','apply_failed','revoke_pending')),
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  approved_at      TIMESTAMPTZ,
  activated_at     TIMESTAMPTZ,
  expires_at       TIMESTAMPTZ,
  closed_at        TIMESTAMPTZ,
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (requester_id <> approver_id OR approver_id IS NULL),
  CHECK ((status = 'approved' AND approved_at IS NOT NULL) OR status <> 'approved')
);
CREATE INDEX ON yusui.tickets(status);
CREATE INDEX ON yusui.tickets(requester_id);
CREATE INDEX ON yusui.tickets(expires_at) WHERE status = 'active';

-- ---- policy_bindings (single-layer: Agent only) --------------------------
CREATE TABLE yusui.policy_bindings (
  ticket_id        BIGINT PRIMARY KEY REFERENCES yusui.tickets(id) ON DELETE CASCADE,
  agent_id         BIGINT NOT NULL REFERENCES yusui.agents(id),
  agent_rule_id    TEXT NOT NULL,                    -- "yusui:tk:<id>" == nft element comment
  src_peer_ips     INET[] NOT NULL DEFAULT '{}',     -- multi-source (docs/03 draft7)
  agent_applied_at TIMESTAMPTZ,
  apply_attempts   INT NOT NULL DEFAULT 0,
  revoke_attempts  INT NOT NULL DEFAULT 0,
  last_error       TEXT,
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ON yusui.policy_bindings(agent_id);

-- ---- netbird_global_settings (single row; written at M4 startup) ---------
CREATE TABLE yusui.netbird_global_settings (
  id                   SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
  server_peer_id       TEXT NOT NULL UNIQUE,
  server_peer_group_id TEXT NOT NULL,
  builtin_policy_id    TEXT NOT NULL,
  installed_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_reconciled_at   TIMESTAMPTZ
);

-- ---- audit_logs (append-only; see GRANTs below) --------------------------
CREATE TABLE yusui.audit_logs (
  id          BIGSERIAL PRIMARY KEY,
  ts          TIMESTAMPTZ NOT NULL DEFAULT now(),
  actor_type  TEXT NOT NULL CHECK (actor_type IN ('user','system','agent','cron')),
  actor_id    TEXT,
  action      TEXT NOT NULL,
  target_type TEXT,
  target_id   TEXT,
  payload     JSONB NOT NULL DEFAULT '{}',
  prev_hash   BYTEA,                                 -- v0.3 chained hash
  hash        BYTEA
);
CREATE INDEX ON yusui.audit_logs(ts DESC);
CREATE INDEX ON yusui.audit_logs(target_type, target_id);
CREATE INDEX ON yusui.audit_logs(action);

-- ---- sessions (Web Shell) ------------------------------------------------
CREATE TABLE yusui.sessions (
  id                      BIGSERIAL PRIMARY KEY,
  pub_id                  TEXT NOT NULL UNIQUE,
  ticket_id               BIGINT NOT NULL REFERENCES yusui.tickets(id),
  asset_id                BIGINT NOT NULL REFERENCES yusui.assets(id),
  agent_id                BIGINT NOT NULL REFERENCES yusui.agents(id),
  ssh_user                TEXT NOT NULL,
  status                  TEXT NOT NULL CHECK (status IN ('allocated','running','closed')),
  opened_at               TIMESTAMPTZ,
  closed_at               TIMESTAMPTZ,
  closed_reason           TEXT,
  recording_uri           TEXT,
  command_policy_snapshot JSONB NOT NULL              -- effective rules at session start
);
CREATE INDEX ON yusui.sessions(ticket_id);
CREATE INDEX ON yusui.sessions(asset_id);

-- ---- session_attachers ---------------------------------------------------
CREATE TABLE yusui.session_attachers (
  id          BIGSERIAL PRIMARY KEY,
  session_id  BIGINT NOT NULL REFERENCES yusui.sessions(id) ON DELETE CASCADE,
  user_id     BIGINT REFERENCES yusui.users(id),     -- NULL for AI tools (source+label identify)
  source      TEXT NOT NULL CHECK (source IN ('web','api','observer','system')),
  label       TEXT,
  role        TEXT NOT NULL CHECK (role IN ('primary','observer')),
  attached_at TIMESTAMPTZ NOT NULL,
  detached_at TIMESTAMPTZ
);

-- ---- command_filter_events (append-only; see GRANTs below) ---------------
CREATE TABLE yusui.command_filter_events (
  id             BIGSERIAL PRIMARY KEY,
  session_id     BIGINT NOT NULL REFERENCES yusui.sessions(id),
  ts             TIMESTAMPTZ NOT NULL DEFAULT now(),
  rule_id        TEXT NOT NULL,
  severity       TEXT NOT NULL,
  action_taken   TEXT NOT NULL CHECK (action_taken IN ('warned','blocked','confirmed','confirm_timeout')),
  source         TEXT NOT NULL,
  attacher_label TEXT,
  raw_line       TEXT NOT NULL                        -- redacted before insert (docs/06 §6.9)
);
CREATE INDEX ON yusui.command_filter_events(session_id);
CREATE INDEX ON yusui.command_filter_events(rule_id);

-- ---- cross-references added after command_policies exists -----------------
ALTER TABLE yusui.projects ADD COLUMN command_policy_id BIGINT REFERENCES yusui.command_policies(id);
ALTER TABLE yusui.assets   ADD COLUMN command_policy_id BIGINT REFERENCES yusui.command_policies(id);

-- ---- least-privilege runtime role ----------------------------------------
-- Roles are created at cluster init (deploy/postgres/init) before migrate runs;
-- if yusui_app is absent the migration fails loudly (correct: fix role setup).
-- yusui_app gets full DML on business tables, but only INSERT+SELECT on the
-- audit / command-filter event tables (append-only — invariant #7).
-- Plain statements only (goose splits on ';'; no DO/$$ block).
GRANT USAGE ON SCHEMA yusui TO yusui_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA yusui TO yusui_app;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA yusui TO yusui_app;
REVOKE UPDATE, DELETE ON yusui.audit_logs            FROM yusui_app;
REVOKE UPDATE, DELETE ON yusui.command_filter_events FROM yusui_app;

-- +goose Down
-- Dev/test only. Production never runs a destructive DOWN (docs/06 §6.5);
-- rollback is done via a new forward migration.
DROP SCHEMA yusui CASCADE;
