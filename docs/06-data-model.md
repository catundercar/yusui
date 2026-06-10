# 06 · 数据模型

## 6.1 总览

Postgres 16+ 单库，全部表在 schema `yusui`。约束硬性写在 DDL 里，不依赖应用层校验。

v0.1 共 13 张表：
- **主表**：`users` `projects` `agents` `assets` `asset_credentials` `tickets` `policy_bindings` `audit_logs` `netbird_global_settings`
- **Web Shell 相关**（详见 [09](09-web-shell.md)）：`sessions` `session_attachers` `command_filter_events` `command_policies`

```
projects ◀──┬── agents ◀──┬── policy_bindings ──▶ tickets ──▶ users (requester/approver)
            │              │                          │
            └── assets ◀───┘                          ├──▶ sessions ──┬──▶ session_attachers
                  │                                   │               └──▶ command_filter_events
                  └────▶ command_policies ◀───────────┘
                                                      │
                                                      └──▶ audit_logs
```

## 6.2 完整 DDL

```sql
CREATE SCHEMA yusui;
SET search_path TO yusui;

-- 用户
-- v0.1 全部为本地账号；external_id 预留给 v0.3 OIDC（NULL 表示本地）
CREATE TABLE users (
  id              BIGSERIAL PRIMARY KEY,
  username        TEXT NOT NULL UNIQUE,            -- 登录名
  display_name    TEXT,
  email           TEXT UNIQUE,                     -- 后续 OIDC 绑定时按邮箱对账
  role            TEXT NOT NULL CHECK (role IN ('requester','approver','admin')),
  -- 本地账号字段（v0.1）
  password_hash   TEXT,                            -- bcrypt；OIDC 用户为 NULL
  password_alg    TEXT NOT NULL DEFAULT 'bcrypt',
  password_changed_at TIMESTAMPTZ,
  mfa_secret_enc  BYTEA,                           -- 可选 TOTP，KMS 加密
  mfa_enabled     BOOLEAN NOT NULL DEFAULT FALSE,
  failed_login_count INT NOT NULL DEFAULT 0,
  locked_until    TIMESTAMPTZ,
  last_login_at   TIMESTAMPTZ,
  -- OIDC 字段（v0.3+，nullable）
  external_id     TEXT UNIQUE,                     -- OIDC sub；NULL = 本地账号
  external_issuer TEXT,                            -- IdP URL
  -- 通用
  -- 注意：v0.1-draft6 起取消运维端 NetBird 客户端，用户不再是 Peer，不留 netbird_peer_id 字段
  is_active       BOOLEAN NOT NULL DEFAULT TRUE,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  -- 本地账号必须有 password_hash；OIDC 账号必须有 external_id；两者择一
  CHECK ((password_hash IS NOT NULL) OR (external_id IS NOT NULL))
);
CREATE INDEX ON users(is_active);
CREATE INDEX ON users(external_id) WHERE external_id IS NOT NULL;

-- 项目
CREATE TABLE projects (
  id           BIGSERIAL PRIMARY KEY,
  code         TEXT NOT NULL UNIQUE,
  name         TEXT NOT NULL,
  cidrs        CIDR[] NOT NULL,
  netbird_group_id TEXT NOT NULL UNIQUE,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (array_length(cidrs, 1) >= 1)
);

-- Agent（项目级 sub-router）
CREATE TABLE agents (
  id                BIGSERIAL PRIMARY KEY,
  project_id        BIGINT NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
  role              TEXT NOT NULL CHECK (role IN ('primary','secondary')),
  hostname          TEXT NOT NULL,
  netbird_peer_id   TEXT NOT NULL UNIQUE,
  netbird_route_id  TEXT,
  agent_version     TEXT,
  cert_fingerprint  TEXT,
  status            TEXT NOT NULL DEFAULT 'unknown'
                       CHECK (status IN ('unknown','online','offline','degraded','frozen')),
  -- draft12(迁移 0002,docs/11):准入门控,与运行期 status 正交。
  -- pending=自动注册待审;approved=可下发规则;rejected=拒绝。
  -- admin 手建默认 approved(向后兼容);自动注册写 pending。
  enrollment        TEXT NOT NULL DEFAULT 'approved'
                       CHECK (enrollment IN ('pending','approved','rejected')),
  netbird_setup_key TEXT,   -- 审核通过时绑定,下发给 daemon 入网;敏感,API 响应脱敏为 has_setup_key
  last_seen_at      TIMESTAMPTZ,
  registered_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (project_id, role)   -- v0.1 限制：每项目最多一个 primary 一个 secondary
);
CREATE INDEX ON agents(project_id);
CREATE INDEX ON agents(status);

-- 资产
CREATE TABLE assets (
  id           BIGSERIAL PRIMARY KEY,
  project_id   BIGINT NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
  name         TEXT NOT NULL,
  ip_internal  INET NOT NULL,
  ports        INT[] NOT NULL DEFAULT '{}',
  os           TEXT,
  tags         JSONB NOT NULL DEFAULT '{}',
  source       TEXT NOT NULL DEFAULT 'manual' CHECK (source IN ('manual','agent_probe','import')),
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (project_id, ip_internal)
);
CREATE INDEX ON assets USING GIN(tags);
CREATE INDEX ON assets(project_id);

-- 资产 SSH 凭证（v0.1 自研 Web SSH 必需；v0.2 起 jumpserver 协议的资产可不填）
CREATE TABLE asset_credentials (
  id              BIGSERIAL PRIMARY KEY,
  asset_id        BIGINT NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
  ssh_user        TEXT NOT NULL,                  -- e.g. 'ops-yusui'
  auth_kind       TEXT NOT NULL CHECK (auth_kind IN ('key','password')),
  secret_enc      BYTEA NOT NULL,                 -- KMS envelope encrypted
  secret_kms_key_id TEXT NOT NULL,                -- 引用 KMS 中的 key id 便于轮换
  fingerprint     TEXT,                            -- 公钥指纹（key 模式），便于审计与对比
  description     TEXT,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  rotated_at      TIMESTAMPTZ,
  is_active       BOOLEAN NOT NULL DEFAULT TRUE,
  UNIQUE (asset_id, ssh_user, is_active)
);
CREATE INDEX ON asset_credentials(asset_id) WHERE is_active = TRUE;
-- 解密只发生在 Web Shell Service 进程内，读取动作必须落 audit_logs:
--   action='credential.decrypt', actor=session_id, target=asset_id

-- 工单
-- v0.1 只支持 SSH（access_kind=web_shell）；v0.2 加 jumpserver 模式覆盖 RDP/DB/etc
CREATE TABLE tickets (
  id                BIGSERIAL PRIMARY KEY,
  pub_id            TEXT NOT NULL UNIQUE,            -- ULID，UI 用
  requester_id      BIGINT NOT NULL REFERENCES users(id),
  approver_id       BIGINT REFERENCES users(id),
  project_id        BIGINT NOT NULL REFERENCES projects(id),
  target_selector   JSONB NOT NULL,                  -- {"asset_ids":[1,2]} OR {"tags":{"app":"mysql"}}
  -- draft7：审批时把 target_selector 冻结展开为具体资产清单，存进 frozen_asset_ids 字段
  -- 审批 UI 必须把这个清单显式展示给审批人，避免"批了 tag 实际放行 N 台"的盲签
  frozen_asset_ids  BIGINT[],                         -- 仅 status >= 'approved' 时填
  ports             INT[] NOT NULL,
  protocol          TEXT NOT NULL DEFAULT 'tcp' CHECK (protocol IN ('tcp','udp','any')),
  access_kind       TEXT NOT NULL DEFAULT 'web_shell'
                       CHECK (access_kind IN ('web_shell','jumpserver')),  -- v0.1 仅 web_shell
  reason            TEXT NOT NULL,
  duration_sec      INT NOT NULL CHECK (duration_sec BETWEEN 60 AND 86400),
  status            TEXT NOT NULL CHECK (status IN
                       ('draft','pending','approved','active','revoking',
                        'closed','rejected','expired','apply_failed','revoke_pending')),
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  approved_at       TIMESTAMPTZ,
  activated_at      TIMESTAMPTZ,
  expires_at        TIMESTAMPTZ,
  closed_at         TIMESTAMPTZ,
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (requester_id <> approver_id OR approver_id IS NULL),
  CHECK ((status='approved' AND approved_at IS NOT NULL) OR status<>'approved')
);
CREATE INDEX ON tickets(status);
CREATE INDEX ON tickets(requester_id);
CREATE INDEX ON tickets(expires_at) WHERE status='active';

-- 临时策略绑定（v0.1-draft6 起单层：仅 Agent）
CREATE TABLE policy_bindings (
  ticket_id              BIGINT PRIMARY KEY REFERENCES tickets(id) ON DELETE CASCADE,
  agent_id               BIGINT NOT NULL REFERENCES agents(id),
  agent_rule_id          TEXT NOT NULL,                -- "yusui:tk:<id>"，与 nftables element comment 一致
  -- draft7：下发时使用的源 IP 列表（v0.1 通常 1 个；v0.3 Server 水平扩展时多个）
  -- 用 INET[] 而非 TEXT 便于按 IP 反查；为空表示"用当前 server_peer_set" - Apply 时展开
  src_peer_ips           INET[] NOT NULL DEFAULT '{}',
  agent_applied_at       TIMESTAMPTZ,
  apply_attempts         INT NOT NULL DEFAULT 0,
  revoke_attempts        INT NOT NULL DEFAULT 0,
  last_error             TEXT,
  updated_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ON policy_bindings(agent_id);
-- 历史背景：v0.1-draft1~5 此处有 netbird_policy_id / netbird_src_group_id 等字段，
-- draft6 取消运维端 NetBird 后这些字段不再使用；NetBird 一侧只有一条常驻策略，
-- 由系统启动期写入 netbird_global_settings（如下），无需 per-ticket 绑定。

-- NetBird 全局常驻策略（v0.1-draft6 新增）
CREATE TABLE netbird_global_settings (
  id                       SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),  -- 单行
  server_peer_id           TEXT NOT NULL UNIQUE,
  server_peer_group_id     TEXT NOT NULL,
  builtin_policy_id        TEXT NOT NULL,                  -- "yusui:builtin:server-to-agents"
  installed_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_reconciled_at       TIMESTAMPTZ
);

-- 审计（append-only，仅 INSERT 权限的数据库角色）
CREATE TABLE audit_logs (
  id          BIGSERIAL PRIMARY KEY,
  ts          TIMESTAMPTZ NOT NULL DEFAULT now(),
  actor_type  TEXT NOT NULL CHECK (actor_type IN ('user','system','agent','cron')),
  actor_id    TEXT,
  action      TEXT NOT NULL,       -- e.g. 'ticket.approve', 'policy.apply'
  target_type TEXT,                 -- e.g. 'ticket', 'agent'
  target_id   TEXT,
  payload     JSONB NOT NULL DEFAULT '{}',
  prev_hash   BYTEA,                -- v0.3：链式哈希
  hash        BYTEA                  -- v0.3
);
CREATE INDEX ON audit_logs(ts DESC);
CREATE INDEX ON audit_logs(target_type, target_id);
CREATE INDEX ON audit_logs(action);

-- river 作业表（由 river 库自动创建，此处仅示意，不在 yusui schema）
-- SCHEMA river: river_job, ...
```

### Web Shell 相关表

完整 DDL 在 [09 · Web Shell §9.8](09-web-shell.md#98-数据模型增量)。结构概览：

| 表 | 作用 |
|---|---|
| `sessions` | 一次终端会话：ticket、asset、agent、起止时间、录像 URI、规则快照 |
| `session_attachers` | 会话历史挂接者：user / source（web/api/observer）/ role / 时间窗 |
| `command_filter_events` | 命令拦截事件：rule_id、severity、action_taken、raw_line（脱敏后）|
| `command_policies` | admin 配置的命令规则集；项目/资产可分别引用 |

附加列：
- `projects.command_policy_id BIGINT REFERENCES command_policies(id)`
- `assets.command_policy_id BIGINT REFERENCES command_policies(id)`

## 6.3 关键约束的设计理由

| 约束 | 理由 |
|---|---|
| `agents.project_id ON DELETE RESTRICT` | 项目不能直接删，必须先撤所有 Agent，避免悬挂引用 |
| `UNIQUE (project_id, role)` | v0.1 拓扑简单：每项目最多 1 primary + 1 secondary |
| `assets.UNIQUE (project_id, ip_internal)` | 同一项目内 IP 唯一；不同项目可重复（私网会重叠） |
| `tickets.duration_sec BETWEEN 60..86400` | 1 分钟到 24 小时，超 24 小时必须分单审批 |
| `tickets.access_kind CHECK ('web_shell','jumpserver')` | v0.1 仅允许 `web_shell`；v0.2 解锁 `jumpserver` |
| `requester_id <> approver_id` | 不允许自审批 |
| `policy_bindings ON DELETE CASCADE` | ticket 删除（仅测试场景）连带清绑定 |
| `netbird_global_settings.id=1` | 单例表，确保只有一组常驻 NetBird 设置 |

## 6.4 索引策略

- 高频路径：`tickets(status)` + `tickets(expires_at) WHERE status='active'` → 过期扫描快。
- `audit_logs(ts DESC)` 倒序常查最近。
- JSONB `assets.tags` GIN 索引 → 标签筛选。
- 不为审计加更多索引，避免拖慢写入。

## 6.5 迁移策略

- 使用 **goose** 管 migration（`migrations/0001_init.sql` 开始）。
- 所有 ALTER 必须 backward-compatible（先加可空列，应用兼容两版本，再加 NOT NULL）。
- 禁止生产环境跑破坏性 DOWN migration；rollback 通过新 UP 实现。

## 6.6 数据保留

| 表 | 策略 |
|---|---|
| `tickets` | 永久保留（合规需要） |
| `policy_bindings` | 与 ticket 同寿命 |
| `audit_logs` | 永久保留；v0.3 起冷数据归档到 S3/对象存储 |
| `users` | 软删（is_active=false），永不物理删 |
| `assets` | 软删（v0.2 加 deleted_at 列） |
| `asset_credentials` | 与 asset 同寿命；轮换时 `is_active=false`，保留 30 天用于取证 |

## 6.6.1 鉴权字段说明（v0.1 → v0.3 演进）

- **v0.1**：所有用户为本地账号，`password_hash` 必填，`external_id` 为 NULL。Admin 在 UI/CLI 创建用户并分配初始密码，用户首次登录强制改密。
- **v0.2**：不变。JumpServer 集成不引入 OIDC，YuSui 直接调 JumpServer API 创建对应账号并下发一次性密码。
- **v0.3**：增加 OIDC provider。已有本地账号可"绑定 OIDC"：录入 OIDC sub 后该账号变成混合账号（两种登录方式都可，但 admin 可强制只走 OIDC）。新增 OIDC 用户首次登录时自动按 email 匹配本地账号；未匹配则按规则创建（默认 role=requester，需 admin 审批激活）。

## 6.7 多租户预留

v0.1 不做多租户，但所有主表预留 `tenant_id BIGINT NULL` 列在 v1.0 加。提前在主键策略上避免使用全局自增 ID 暴露给 UI——v0.1 起 UI 用 ULID（`pub_id TEXT UNIQUE`），内部仍用 BIGSERIAL。这样未来加 tenant 不需要改 URL。

## 6.8 备份

- 物理：Postgres 物理流复制 + 每日 base backup 到对象存储。
- 逻辑：每日 `pg_dump --format=custom`，保留 30 天。
- 关键表（tickets / policy_bindings / audit_logs）启用 logical replication 到只读副本，BI 与导出走副本。

## 6.8.1 target_selector 的展开与冻结（draft7 新增）

工单创建时允许两种 target_selector：
- `{"asset_ids":[1,2]}` — 直接指定
- `{"tags":{"app":"mysql"}}` — 标签筛选

**冻结时机**：审批人点"批准"那一刻，server 立即把 selector 解析为**当时**匹配的资产集合 → 写入 `tickets.frozen_asset_ids`。Agent 规则按这个清单下发。审批后再加资产即使匹配 tag 也不会自动放行——避免"批了 tag → 后续新加资产意外可达"。

**UI 必须展示**：审批人页面把展开结果显式列出（资产名 + IP + 端口），审批人确认后才能点批准。否则是盲签——批的是 selector，实际放行可能 30 台。

## 6.9 未决问题

- 审计链式哈希（v0.3）：算法（SHA-256 链）+ 锚定（每小时把最新 hash 写一份到只读 S3）。
- `policy_bindings.last_error` 可能存敏感信息（Agent 错误体）→ 落库前做脱敏白名单。
- 是否需要单独的 `events` 表存 Agent 上报的命中事件？v0.2 评估，可能改用时间序列库。
- `command_filter_events.raw_line` 的保留期：长保留有助合规调查，但含原始命令文本（可能含口令）。倾向 90 天滚动 + 敏感字段脱敏。
- 多张 ticket 共享 asset 的并发 sessions：是否在 `sessions` 表加 `(asset_id, status='running')` 唯一约束？v0.1 不加（同一资产允许多并发会话）。
