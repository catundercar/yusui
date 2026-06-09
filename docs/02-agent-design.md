# 02 · YuSui Agent 设计

## 2.1 角色与边界

Agent 是项目的"边界守门人"，同时承担三件事：

1. **NetBird Peer + daemon 监管**：纯 Go 单二进制（**draft10：Windows 原生** `agent.exe`），作为该项目唯一的普通 NetBird Peer 拿到 overlay IP；**管理**（非内嵌）官方 NetBird daemon。**不广播 Network Route**。
2. **per-ticket 用户态 L4 转发器（即 ACL）**：每张工单一个 listener，绑在 overlay IP 上，把字节转给资产 `ip:port`；listener 的存在/目标/源白名单就是访问控制本身。
3. **可观测探针**：心跳、转发器状态、连接计数、(v0.2) 连接日志。

> **v0.1-draft10（重大转向：Agent 平台 + 执行机制 + 路由）**
> - **平台**：运维 Agent 主机基本是 **Windows**。Agent 是**纯 Go `agent.exe`**，跨平台编译（`CGO_ENABLED=0`）。
> - **NetBird 集成方式**：**管理官方 daemon**（内核 WireGuard / wintun），经 daemon 本地 gRPC API 入网/起停/读状态——不内嵌 SDK、不 fork，最忠实于「NetBird 是上游依赖」。一次性安装（wintun 驱动 + 服务）由**独立 installer** 完成（非 agent 自举、非 MDM）。
> - **执行机制**：放弃 nftables（Linux-only，Windows 无对应）。改为 **agent 进程内的 per-ticket 用户态 L4 转发器**（纯 Go `net.Listen`/`net.Dial`），跨平台、不依赖任何 OS 包过滤。Agent 仍只做 L4 字节搬运，不终结 SSH、不解析应用层（录像/审计仍在 Server Web Shell）。
> - **路由**：不广播项目 CIDR 为 Route；Server 直连 Agent overlay IP，Agent 转发到资产。跨项目重叠私网由此天然消解（[04 §4.1](04-netbird-adapter.md)）。
> - **Server 侧**：Server 在容器内以 **NetBird sidecar 容器**入网（不走本「管理 daemon」模式）。
> - 历史:draft1~9 的「Routing Peer 广播 CIDR + nftables(filter/nat) TTL set」模型在 draft10 被取代；§2.3 的 nftables/eBPF 内容降为**可选 Linux 实现**（仅在有内核转发性能诉求时），不再是默认事实源。

**Agent 不做的事**：
- 不解析 SQL / SSH 命令（应用层审计交 JumpServer）
- 不存业务数据
- 不直接面向最终用户（用户只与 Server 交互）
- 不自治：所有规则来自 Server，本地不提供 CLI 改规则

## 2.2 进程模型

单二进制 `yusui-agent`，内部 goroutine 划分：

```
yusui-agent
├─ supervisor              （管协程生命周期，崩溃重启）
├─ netbird-supervisor      （管理官方 NetBird daemon：经其本地 gRPC API 入网/起停/读状态）
├─ control-plane           （gRPC 客户端，长连接到 Server）
│   ├─ stream sender       （上行 events）
│   └─ stream receiver     （下行 commands）
├─ forwarder               （per-ticket 用户态 L4 代理：每工单一个 overlay listener → 资产 ip:port）
├─ asset-prober (v0.2)     （被动 ARP/扫描，候选资产上报）
├─ metrics                 （Prometheus exporter 端口）
└─ local-store             （BoltDB / SQLite，规则与凭证持久化）
```

进程崩溃时 supervisor 重启子协程；整个二进制崩溃由 systemd 拉起。

## 2.3 转发器 / 规则引擎

### v0.1（draft10）：用户态 per-ticket L4 转发器（默认，跨平台）

每张工单 = Agent 进程内一个 TCP 转发器：

- `ApplyRule` 到达 → 在 **overlay IP : 合成端口** 上 `net.Listen`，把连接转发到工单的 `资产 ip:port`（`net.Dial`）。监听地址回传给 Server（[03 §3.2 `AckCommand.forward_addr`](03-agent-protocol.md)），Server Web Shell 用它替代直连资产 IP。
- **ACL 即转发器本身**：listener 只接受源 = Server overlay peer IP（`src_peer_ips`）的连接，且只转发到那一个 `ip:port`。「listener 存在 + 固定目标 + 源白名单」就是唯一的「谁能进哪里」事实表。
- **TTL**：agent 进程内定时器在 `expires_at` 关闭 listener 并断开活动连接（Server 侧 `ForceCloseByTicket` 双保险）。
- **跨平台**：纯 Go，不依赖 nftables/WFP/portproxy；Windows/Linux 同一套。Agent 只搬 L4 字节，不终结 SSH（端到端加密在 Server SSH 客户端 ↔ 资产 sshd 之间）。
- **无需 SNAT**：转发器以 **Agent 自身 LAN IP** 主动 `Dial` 资产，资产天然只看到 Agent IP（满足 §7.4「资产只接受 Agent 入站」），不再需要 nftables masquerade。
- `rule_id`/`ticket_id` 仍是幂等键；BoltDB 缓存活动转发器，崩溃重启按缓存重建 listener。

### 可选（Linux，draft10 起非默认）：nftables 内核转发

> 仅当 Agent 跑在 Linux 且有内核级转发性能诉求时考虑，藏在 `Enforcer` 接口后，与上面的用户态转发器二选一。下面是 draft1~9 的默认实现细节，draft10 起不再是默认事实源。

YuSui 在每个 Agent 上独占两个 nftables 表，结构固定。

#### filter 表（ACL）

```
table inet yusui {
    # element 自描述：comment 字段烤 rule_id；BoltDB 退化为索引缓存
    set allowed_v4 { type ipv4_addr . ipv4_addr . inet_service . inet_proto;
                     flags interval, timeout;
                     comment "yusui-rules"; }
    chain forward {
        type filter hook forward priority 0;
        ct state established,related accept;
        ip saddr . ip daddr . tcp dport . meta l4proto @allowed_v4 accept;
        log prefix "yusui-drop: " drop;
    }
}
```

- `allowed_v4` 是带 timeout 的 set。下发规则：
  ```
  nft add element inet yusui allowed_v4 \
    { 100.92.1.5 . 10.20.3.7 . 3306 . tcp \
      timeout 2h comment "yusui:tk:42" }
  ```
- **rule_id 写进 element 的 `comment`**（nft ≥ 0.9.4 / Ubuntu 22+ / Rocky 9 支持），让 nftables 自描述；BoltDB 仅做查询索引（损坏可重建），不再是单一事实源。对账时直接 `nft -j list set` 把元素+comment 拉回来比对 DB。
- **多源 IP 支持（draft7 新增）**：v0.3 多副本 Server 时，每个 Server 副本是独立 NetBird Peer，IP 不同。Apply 时下发**一组** server-peer-ip → 同一个 rule_id 在 set 里展开为多条 element（每 src 一条），同时 Apply、同时 Revoke。看 `ApplyRule.src_peer_ips repeated` 字段（[03 §3.2](03-agent-protocol.md)）。
- 规则只放行 forward 链，本机入站策略由系统默认 firewall 管。

#### nat 表（masquerade，draft7 新增）

资产位于 10.x/172.x 项目私网，**不**装 NetBird，不知道 100.x overlay 网段。从 Server 经 Agent 到资产的 SSH 连接如果保留 100.x source IP，资产回包无法路由——必须由 Agent 做 SNAT。同时 §7.4 要求"资产 OS 防火墙拒绝非 Agent IP" 也只有 SNAT 后才能自洽。

```
table inet yusui-nat {
    chain postrouting {
        type nat hook postrouting priority srcnat;
        # 来自 server-peer 网段（100.92.0.0/16 等 NetBird overlay）出口走 masquerade
        ip saddr @server_peer_set oifname $project_iface masquerade;
    }
    set server_peer_set { type ipv4_addr; flags interval; }
}
```

**重要后果（必须写进文档）**：资产侧 sshd / last / auditd / 应用日志看到的 source IP **永远是 Agent IP**，看不到真实运维人员。这意味着"谁敲了这条命令"在资产自身的日志里得不到答案——需要靠 §7.12 的 session sentinel 在资产 syslog 里写入工单/用户的关联标签来反查。客户侧 SIEM 接入时按 sentinel 关联。

`server_peer_set` 同样由 Server 下发并维护，复用 Apply/Revoke 流程。Agent 启动期对账确保它至少包含 NetBird overlay 网段（fallback 防误删）。

### v0.3：eBPF 升级

切换到 eBPF（基于 cilium/ebpf）：
- 规则存在 BPF map（LPM trie），更新无锁，O(1)。
- 命中事件 perf ring buffer → 异步上报 → Server，作为流量审计补充。
- 仍保留 nftables 作为 fallback；启动时探测内核版本（≥5.10）决定使用模式。

### 规则状态机（Rule 在 Agent 本地视角）

```
                  ApplyRule
   ┌─────────┐   ──────────▶  ┌─────────┐
   │ Absent  │                │ Active  │
   └─────────┘   ◀──────────  └─────────┘
                  RevokeRule        │
                  / 到期 / 失联熔断  │
                                    ▼
                              ┌─────────────┐
                              │ Drained     │（关 listener，留 BoltDB tombstone 1h）
                              └─────────────┘
```

`Drained` 状态用于对账：Server 询问时，Agent 能区分"从未存在"和"刚撤销"。

## 2.4 与 NetBird 的协作

Agent 启动流程：

```
1. 读本地配置 (agent.yaml)：core_addr / project_id / setup_key / wg_iface
2. 若 netbird 未运行：netbird up --setup-key XXX --management-url ...
3. 等待 netbird status → 拿到 own peer_id
4. 调 Server gRPC: Register(project_id, peer_id, hostname, version)
5. Server 回 Token；后续 gRPC 调用都带 Token + mTLS 客户端证书
6. 进入 Reconcile：Server 推全量规则 → 本地比对**活动转发器 listener**状态 → 补齐 / 删多
7. 进入正常态：双向流 + 心跳
```

> draft10：第 2 步的「确保 netbird 已 up（`--setup-key`）」由 `netbird-supervisor` 经 daemon 本地 gRPC API 完成，不再 fork CLI。

**不再注册 Network Route（draft10）**：Agent 只作普通 Peer 拿 overlay IP，**不广播**项目 CIDR。Server 直连 Agent overlay IP，访问的细粒度由 Agent 转发器控制（见 §2.3、[04 §4.1](04-netbird-adapter.md)）。Server 仍是唯一控制点。

## 2.5 高可用（v0.3+）

每项目 ≥ 2 Agent，标识为 `primary` / `secondary`。

**NetBird 侧（draft10）**：
不再依赖 NetBird HA Route（已无 Route）。两个 Agent 各是独立 Peer、各有 overlay IP。HA 由 **Server 侧**实现：Apply 时 Server 让两个 Agent 都开转发器，得到两个 `forward_addr`；Server 连接时择一、失败切换另一个（健康探测 + 重连）。

**YuSui 侧**：
Server 把规则**同时下发到两个 Agent**，状态机要求两个都 ack。任一 Agent 失联 → 标记该项目"降级"，告警但不阻断。两个都失联 → 阻断新工单批准。

**为什么不主备而是双活**：
主备需要 failover 检测延迟（秒级），双活的规则一致性靠 Server 双写保证。代价是 Agent 故障时仍可能有几秒内 ACL 不同步——但只影响极少数刚下发的规则，已建立的连接不受影响。

## 2.6 失联熔断

Agent 维护两个时钟：
- `last_core_recv_at`：最近一次收到 Server 任何消息
- `freeze_after`：默认 60s

当 `now - last_core_recv_at > freeze_after`：
- 进入 **Frozen** 模式
- 拒绝新建转发器（即使运维人员手动尝试）
- 已下发的转发器 listener 仍按 `expires_at` 由 agent 进程内定时器到期自动关闭
- 已建立 TCP 连接不影响
- netbird daemon 报告 overlay 断开，也触发 Frozen
- 持续重连，重连成功后强制全量对账

恢复条件：与 Server 任意消息往返成功 + 完整对账通过。

## 2.7 持久化

本地 BoltDB（单文件）存：
- `meta`：peer_id、上次 Server endpoint、token
- `rules`：每个 Active / Drained 工单的转发器记录（`rule_id`、资产 `ip:port`、overlay 合成端口、`expires_at`、`src_peer_ips` 白名单）
- `events_outbox`：未上报成功的 events（断网期间累积，重连后回放）

不存任何业务数据、不存用户密码、不存 NetBird 密钥（密钥由 NetBird 官方 daemon 自管）。

**draft10：转发器活动 listener 是运行期事实，BoltDB 是崩溃恢复源**。用户态转发器没有 OS 侧持久表（不像 nftables），所以进程崩溃重启后据 BoltDB 重建 listener，并立即与 Server 全量对账纠偏。（draft1~9：事实源是 nftables element 的 comment，BoltDB 仅查询缓存。）

## 2.8 资源占用预期

| 资源 | v0.1 目标 |
|---|---|
| RAM | < 50MB |
| CPU 空闲 | < 1% (1 vCPU) |
| 规则容量 | 10k 条无明显抖动 |
| 网络上行 | 心跳 ≈ 200B/10s ≈ 60KB/h |

## 2.9 安装与升级

> **draft10：标准 Agent 是 Windows 原生 `agent.exe`，由独立 installer 安装。** installer 负责一次性把 **NetBird 官方 daemon（wintun 驱动 + Windows 服务）**装上并以 setup_key 入网，再装 `agent.exe`（也作为 Windows 服务），用 `YUSUI_REGISTER_TOKEN` 向 Server 注册。两个 token 含义见下。Agent 运行期通过 NetBird daemon 的本地 gRPC API（Windows 命名管道）管理它，不 fork CLI。下面的 Linux `install.sh` 作为可选 Linux Agent 的参考保留。

```bash
# v0.1 一键安装（Linux 可选 Agent；Windows 走独立 installer）
curl -fsSL https://yusui.example/install.sh | \
  YUSUI_SERVER=https://yusui.example \
  YUSUI_PROJECT=alpha \
  NETBIRD_SETUP_KEY=xxx \             # 给 NetBird client 加入 overlay 用
  YUSUI_REGISTER_TOKEN=yyy \           # 一次性，向 YuSui Server 注册 Agent 用
  bash
```

**两个 token 概念不要混淆**（draft7 澄清）：
- `NETBIRD_SETUP_KEY`：NetBird 自己的 enrollment key，由 NetBird Mgmt 颁发，用于把 Agent 主机加入 overlay 拿到 100.x IP。
- `YUSUI_REGISTER_TOKEN`：YuSui Server 颁发的一次性 token（1h 有效），用于 Agent 完成 `Register(project, peer_id, ...)` gRPC 调用并换取 mTLS 证书。

两者都必需，缺一不可。安装脚本会先 `netbird up --setup-key $NETBIRD_SETUP_KEY` 入网，再用 `$YUSUI_REGISTER_TOKEN` 调 YuSui Server。

脚本做：
1. 检测 OS / 内核
2. 安装 NetBird (官方脚本)
3. 下载 yusui-agent 二进制
4. 生成 systemd unit
5. 启动并轮询健康直到 Registered

升级：Server 推送可用版本号，Agent 自校 → 下载新二进制到 `.next` → systemd Restart。失败回滚到上版本。

## 2.10 未决问题

- **（draft10 已决）Agent 平台**：Windows 原生 `agent.exe`（纯 Go，用户态转发器），不再「只支持 Linux」。Linux Agent 降为可选。
- **（draft10 已决）Agent 与 NetBird 的关系**：**管理官方 daemon（经其本地 gRPC API），不内嵌、不 fork**——保持 NetBird 升级独立、最忠实于上游依赖。曾考虑的内嵌 SDK（`client/embed`，单文件/用户态/免驱动）因 SDK 较新、用户态 WG 性能与依赖体量被放弃。
- agent↔daemon 接口的版本耦合：pin NetBird 版本，daemon gRPC API 变更要测；CLI 仅作兜底。
- 用户态转发器对大吞吐（DB dump、文件传输）的 CPU/延迟代价：SSH 运维会话无感；大文件强制走独立通道（与 §8 风险表一致）。
- 是否允许 Agent 跑在容器里？K8s 场景下需 `hostNetwork: true` + `NET_ADMIN`，可以但要单独 Helm chart（仅 Linux 可选实现相关）。
- 可选 Linux nftables 实现：timeout set 在某些发行版（CentOS 7）不支持 → 若启用该实现，要求 ≥ Ubuntu 22.04 / Debian 12 / Rocky 9。
