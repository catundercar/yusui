# 10 · TODO / 未实现项(按规划记录)

> 这篇是**实现欠账的单一事实表**:已设计或已规划、但代码里还没做(或做了简化版)的东西。
> 每条给:**现状 → 不做会怎样 → 规划版本 → 关联文件**。修一条就把它从这里划掉并补到对应模块文档。
> 口吻同其它文档:做什么 + 为什么 + 不这样会怎样。

状态图例:`未实现` / `部分(MVP 简化)` / `占位(门控报错)` / `偏离(实现与文档不符)`。

---

## 10.1 draft10 收尾(本期设计、尚未全部落地)

draft10(见 [02](02-agent-design.md)/[03](03-agent-protocol.md)/[04](04-netbird-adapter.md) + DESIGN 变更日志)的执行模型——用户态 L4 转发器、`forward_addr`、Web Shell 连转发器、Windows 原生 agent.exe——已实现并有 CI 守门(`e2e-grpc`)。剩下这些:

| 项 | 状态 | 现状 / 不做会怎样 | 规划 | 关联 |
|---|---|---|---|---|
| **NetBird daemon 管理(`overlay.Netbird`)** | 部分(已落地 + 已验证,gRPC daemon API 待补) | `overlay.New(Config{Kind:"netbird"})` 已实现:设了 setup key 就 `netbird up`,然后从 WireGuard 接口(默认 `wt0`)自动发现 overlay IP 作 forwarder `ListenHost()`、上报 connected/down。**本机已验证**:netbird daemon 容器入网后,agent `YUSUI_OVERLAY=netbird` 读到真实 overlay IP(`listen_host=100.118.119.171 status=connected`,`deploy/netbird/`)。**v1 用 `netbird` CLI(本身就是 daemon 本地 API 的客户端)+ 读接口**;draft10 偏好的「直连 daemon 本地 gRPC API」是后续细化。 | draft10 收尾 | `agent/internal/overlay/overlay.go`、[deploy/netbird](../deploy/netbird) |
| **Windows installer** | 未实现 | draft10 定的「独立 installer 装 wintun 驱动 + netbird 服务 + agent.exe 服务」还没有。**不做 → Windows 上无法一键部署 agent**。 | draft10 收尾 | [02 §2.9](02-agent-design.md) |
| ~~**服务器重启后 `forward_addr` 重建**~~ | ✅ 已修复 | 引擎新增 `RebuildForwards`:调度器启动即跑一趟 + 每 5s,对 active 工单按 `rule_id` **幂等 re-Apply** 回填内存 `forward_addr`(agent 转发器对同 `rule_id` 幂等,不扰动在途连接);map 的 key 存在性=本进程已应用,避免重复下发。配套修了 `GracefulStop` 无界阻塞——agent 长连 Control 流会让每次 SIGTERM(部署/重启)挂住并占住端口,改为「优雅 5s 后强制 `Stop()`」。`e2e-grpc` 加重启相位守门(重启→重连→重建→Web Shell 仍走转发器)。 | — | `server/internal/policy/engine.go`、`cmd/yusui-server/main.go` |
| **多 target 的 `forward_addr`** | 部分(MVP 简化) | `agentgw.Gateway.ApplyRule` 只回传**第一个 target** 的转发地址;一张工单多资产/多端口时,其余 target 的转发地址拿不到。**不做 → 多资产工单只有第一个能经转发器连**(MVP 工单基本单 target,暂不影响)。需要 per-target 地址映射(`map[ip:port]addr`)+ Web Shell 按 (asset,port) 解析。 | v0.2 | `server/internal/agentgw/`、`controller.go`、`engine.go` |
| **Agent 本地持久化(BoltDB)** | 部分(已用服务端自愈替代) | [02 §2.7](02-agent-design.md) 写「BoltDB 缓存活动规则,崩溃可重建」,但 `forward`/`nft` 仍是**纯内存 map**,无 bbolt。**agent 重启自愈已做**:controller 在 agent 重连(新 Control 流)时 `OnAgentReconnect` 清掉该 agent 的 forward 条目 + 立即 `RebuildForwards`,**新 Web Shell 重连到新转发器**(`e2e-grpc` agent-restart 相位守门)。**注意**:agent 重启会断掉**已建立**的连接(TCP 随转发器死,无法迁移),这是固有限制。BoltDB 本地持久化现降为可选(服务端 re-Apply 已覆盖重建)。 | v0.3(可选) | `agent/internal/forward/`、`server/internal/policy/engine.go`、`agentctl` |
| **docs 残留旧表述清扫** | 偏离 | `docs/01/05/06/07` 仍有 `nftables` / Network Route / Routing Peer 等 draft1~9 表述,与 draft10 冲突(draft10 只扫了 02/03/04 + CLAUDE.md + DESIGN)。**不做 → 文档自相矛盾,误导后来者**。 | 文档债 | `docs/01,05,06,07` |

---

## 10.2 安全硬化

| 项 | 状态 | 现状 / 不做会怎样 | 规划 | 关联 |
|---|---|---|---|---|
| **Agent↔Server gRPC mTLS** | 偏离 | [03 §3.6](03-agent-protocol.md) 要求私 CA mTLS(server 签 agent 证书);代码用 `insecure.NewCredentials()`。**不做 → 控制面无双向认证**(目前靠「只走 overlay + register token」兜底,但与设计不符)。需要:Register 阶段签发证书、Control 阶段 mTLS。 | v0.2 | `controller.go`、`agent/internal/control/client.go` |
| **MFA / TOTP** | 占位(fail-closed) | `users.mfa_enabled` 有列,但 `LocalProvider.Login`/`StepUp` 遇到 mfa_enabled 直接 `ErrMFAUnsupported`(故意 fail-closed,不放行)。**不做 → 无法给账号开第二因子**;`pquerna/otp` 已规划未接。 | v0.1 收尾 / v0.3 | `server/internal/auth/identity.go` |
| **后端 validation 错误 i18n** | 部分 | 带 code 的错误已本地化([03/前端 errText](../web/src/api.ts));但 `validation` 这类携带具体英文 message 的错误(如 `reason is required`),中文环境仍显示英文细节。**不做 → 中文 UI 偶现英文报错**。需要:后端把字段名参数化进 code + 前端模板。 | v0.2 | `server/internal/policy/engine.go`、`web/src/i18n` |
| **Audit 哈希链** | 未实现(列预留) | `audit_logs.prev_hash`/`hash` 列存在但恒为 NULL([06](06-data-model.md) 标注 v0.3:SHA-256 链 + 每小时锚定只读存储)。**不做 → 审计可读但非防篡改**(append-only DB 角色已是第一道)。 | v0.3 | [06](06-data-model.md)、[07 §7](07-security.md) |
| **资产侧 session sentinel** | 未实现 | [07 §7.12](07-security.md):因 agent SNAT/转发,资产日志只见 agent 来源;需在资产 syslog 写工单/用户关联标签反查。**不做 → 资产自身日志无法定位「谁敲的」**。 | v0.2 | [07 §7.12](07-security.md) |

---

## 10.3 可靠性 / 运维

| 项 | 状态 | 现状 / 不做会怎样 | 规划 | 关联 |
|---|---|---|---|---|
| **river 调度(替代进程内 ticker)** | 偏离 | 计划用 river(Postgres-backed)做到期回收;实现是 `engine.RunScheduler` 进程内 `time.Ticker` + `ExpireDue`。**不做 → 单 server 够用,但多副本(v0.3)会重复跑到期、且无持久任务队列**。 | v0.3 | `server/internal/policy/engine.go`、CLAUDE.md 技术栈 |
| **Agent 高可用(primary/secondary 双活)** | 部分(占位) | schema 有 `role IN (primary,secondary)` + `UNIQUE(project_id,role)`,但注册与下发只认 `GetPrimaryAgentForProject`;**建 `secondary` 能上线但不在数据路径里**([02 §2.5](02-agent-design.md) 的双活下发 + 故障切换未实现)。**不做 → 单 agent 故障即项目不可用**。 | v0.3 | `engine.go`、`controller.go`、[02 §2.5](02-agent-design.md) |
| **cert rotation(RenewCert)** | 未实现 | [03 §3.6](03-agent-protocol.md):到期前 7 天 agent 主动续证(proto 字段 v0.2 加)。**不做 → 90 天证书到期后 agent 掉线**(目前无 mTLS,尚不触发)。 | v0.2 | [03 §3.6](03-agent-protocol.md) |
| **Web Shell 进程隔离** | 部分(MVP 同进程) | v0.1 是 server 内独立 goroutine 池 + 接口边界;v0.2 拆独立进程/容器(最小权限 namespace)。**不做 → Web Shell 与控制面同进程,§7.8.2 的隔离缓解不生效**。 | v0.2 | [09](09-web-shell.md)、CLAUDE.md 组件边界 |
| **录像对象存储** | 部分(本地 FS) | 录像写本地 `RECORDINGS_DIR`;v0.2+ 对象存储 + WORM。**不做 → 多副本/重启录像不集中、不抗篡改**。 | v0.2+ | [09 §9.5](09-web-shell.md) |
| **eBPF enforcer** | 未实现(可选) | [02 §2.3](02-agent-design.md):Linux 上可选 eBPF 替代 nftables(连接级日志、更细粒度)。draft10 默认已是用户态转发器,这条优先级降低。 | v0.3(可选) | [02 §2.3](02-agent-design.md) |
| **Helm chart** | 未实现 | v0.1 是 Docker Compose([deploy](../deploy));v0.3 出 Helm。 | v0.3 | DESIGN §6 |

---

## 10.4 已规划功能扩展(版本已定)

| 项 | 状态 | 现状 | 规划 | 关联 |
|---|---|---|---|---|
| **OIDC / Identity Adapter** | 占位(接口预留) | v0.1 本地账号(bcrypt);`IdentityAdapter` 接口已抽,OIDC(Keycloak/Authentik)未接。 | v0.3 | `server/internal/auth/`、CLAUDE.md |
| **JumpServer 集成** | 未实现 | 覆盖 SSH 以外协议(RDP/DB 等)的 v0.2 可选扩展,整篇设计在 [08](08-jumpserver-integration.md),无代码。 | v0.2 | [08](08-jumpserver-integration.md) |
| **资产自动发现(asset-prober)** | 未实现 | [02](02-agent-design.md)/[03 `AssetReport`](03-agent-protocol.md):agent 被动 ARP/扫描上报候选资产;v0.1 仅手动录入。 | v0.2 | [02](02-agent-design.md)、[03](03-agent-protocol.md) |
| **项目级 approver 作用域** | 未实现 | v0.1 approver 是全局(诚实化已记);v0.2 加 `user_project_memberships`,approver 仅审批所属项目。 | v0.2 | DESIGN 路线图、[05](05-policy-engine.md) |
| **多租户(tenant_id)** | 未实现 | UI 用 ULID `pub_id` 为 v1.0 多租户预留 URL 空间;`tenant_id` 未加。 | v1.0 | [06](06-data-model.md) |

---

## 10.5 测试覆盖缺口

| 项 | 状态 | 现状 / 不做会怎样 |
|---|---|---|
| ~~**审批 + 自审批拦截 的 e2e**~~ | ✅ 已修复(step-up 提示除外) | `ticket-approve.spec`:审批人 UI 审批申请人的工单→active;**自审批被拦**(`approver_eq_requester` 错误提示、工单保持 pending)。`agent-approve.spec` 另覆盖 agent 注册审核。**step-up 的密码提示未覆盖**——新登录已满足窗口、e2e 内无法让其过期;后端强制 + 前端 `withStepUp` 包裹。 |
| ~~**TS 类型检查进 CI**~~ | ✅ 已修复 | `web` 加 `npm run typecheck`(`vue-tsc --noEmit`,识别 `.vue` SFC),CI `e2e` job 在 playwright 前跑;`src/shims.d.ts` 声明无类型的 fontsource 副作用包。 |
| **NetBird Adapter 契约测试** | 未实现 | [04 §4.12](04-netbird-adapter.md):CI 起真实 NetBird Mgmt 跑 Adapter 全流程。现仅 mock 单测。 |
| **compose 栈无自动化测试** | 已知 | CI 用 `e2e-stack.sh`/`e2e-grpc.sh` 直跑进程,不起 docker-compose;compose 编排(如 restart 策略)靠人工 + 注释。 |

---

## 未决问题

- `overlay.Netbird` 用 NetBird 官方 daemon 的本地 gRPC API,还是退回内嵌 SDK(`client/embed`)?当前定的是「管理 daemon」,但没有 NetBird 环境无法落地验证——需要先确认一个带 NetBird 的测试环境。
- `secondary` agent:在 UI 上先标注「v0.3 预留、当前不参与转发」,还是 v0.1 直接隐藏该选项?
- river vs 进程内 ticker:v0.1 单 server 下 ticker 够用,是否值得在 v0.1 就引入 river 以避免 v0.3 重写?
