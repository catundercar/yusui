-- +goose Up
-- draft11/12: agent enrollment (auto-register + admin approval, docs/11).
-- `enrollment` is ORTHOGONAL to the runtime `status` liveness column: status =
-- "is it up?", enrollment = "is it allowed in?". Additive + backward-compatible.
ALTER TABLE yusui.agents
  ADD COLUMN enrollment TEXT NOT NULL DEFAULT 'approved'
    CHECK (enrollment IN ('pending','approved','rejected'));
-- Default 'approved' keeps existing (admin-created) agents working; auto-
-- registered agents are inserted explicitly as 'pending'.

ALTER TABLE yusui.agents
  ADD COLUMN netbird_setup_key TEXT;  -- bound on approval, handed to the daemon to join NetBird; sensitive.

-- +goose Down
-- Dev/test only (docs/06 §6.5: no destructive DOWN in prod).
ALTER TABLE yusui.agents DROP COLUMN IF EXISTS netbird_setup_key;
ALTER TABLE yusui.agents DROP COLUMN IF EXISTS enrollment;
