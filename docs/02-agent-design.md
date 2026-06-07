# 02 · YuSui Agent 设计

## 2.1 角色与边界

Agent 是项目的"边界守门人"，同时承担三件事：

1. **NetBird Routing Peer**：作为该项目唯一的 NetBird Peer，把项目私网 CIDR 注册为 Network Route。
2. **本地 ACL 执行点**：在 nftables（v0.1）或 eBPF（v0.3+）中维护 YuSui 下发的临时规则集合。
3. **可观测探针**：心跳、规则状态、命中计数、(v0.2) 连接日志。

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
├─ netbird-client          （fork/exec netbird CLI，监控进程）
├─ control-plane           （gRPC 客户端，长连接到 Server）
│   ├─ stream sender       （上行 events）
│   └─ stream receiver     （下行 commands）
├─ rule-engine             （nftables/eBPF 适配，规则状态机）
├─ asset-prober (v0.2)     （被动 ARP/扫描，候选资产上报）
├─ metrics                 （Prometheus exporter 端口）
└─ local-store             （BoltDB / SQLite，规则与凭证持久化）
```

进程崩溃时 supervisor 重启子协程；整个二进制崩溃由 systemd 拉起。

## 2.3 规则引擎（Rule Engine）

### v0.1：nftables 适配（filter + nat 两表）

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
                              │ Drained     │（删 nft 元素，留 BoltDB tombstone 1h）
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
6. 进入 Reconcile：Server 推全量规则 → 本地比对 nftables 实际状态 → 补齐 / 删多
7. 进入正常态：双向流 + 心跳
```

**Network Route 的注册**：Agent 自己不注册路由，由 Server 通过 NetBird API 在 Agent 的 peer_id 上挂 Route。这样 Server 是唯一控制点，避免双写。

## 2.5 高可用（v0.3+）

每项目 ≥ 2 Agent，标识为 `primary` / `secondary`。

**NetBird 侧**：
两个 Agent 同时注册同一 Network Route，NetBird 支持 routing peers 优先级 + failover。具体配置交给 NetBird 自身的 HA Route 能力（NetBird `--routing-group` + `priority`）。

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
- 拒绝任何本地 CLI 添加规则（即使运维人员手动尝试）
- nftables set 的 timeout 仍然有效，到期规则照常清理
- 已建立 TCP 连接不影响
- 持续重连，重连成功后强制全量对账

恢复条件：与 Server 任意消息往返成功 + 完整对账通过。

## 2.7 持久化

本地 BoltDB（单文件）存：
- `meta`：peer_id、上次 Server endpoint、token
- `rules`：所有 Active / Drained 规则索引缓存（用于快速查询；事实源是 nftables element 的 comment，BoltDB 损坏可从 nft 重建）
- `events_outbox`：未上报成功的 events（断网期间累积，重连后回放）

不存任何业务数据、不存用户密码、不存 NetBird 密钥（密钥由 netbird client 自己管在 `/etc/netbird/`）。

**为什么 nft element 是事实源而 BoltDB 是缓存**：早期 draft 把 BoltDB 作为唯一映射表，BoltDB 损坏会导致对账把全项目规则误判为孤儿全部撤掉。draft7 起统一 rule_id 写进 nft comment，自描述；BoltDB 退化为查询加速。

## 2.8 资源占用预期

| 资源 | v0.1 目标 |
|---|---|
| RAM | < 50MB |
| CPU 空闲 | < 1% (1 vCPU) |
| 规则容量 | 10k 条无明显抖动 |
| 网络上行 | 心跳 ≈ 200B/10s ≈ 60KB/h |

## 2.9 安装与升级

```bash
# v0.1 一键安装
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

- nftables timeout set 在某些发行版（CentOS 7）不支持 → 需要 fallback 到 ipset？v0.1 直接要求 ≥ Ubuntu 22.04 / Debian 12 / Rocky 9。
- Agent 与 NetBird client 是否合并成同一进程（fork netbird 代码内嵌）？倾向不合并，保持 NetBird 升级独立。
- 是否允许 Agent 跑在容器里？K8s 场景下需 `hostNetwork: true` + `NET_ADMIN`，可以但要单独 Helm chart。
- Windows Server 项目场景：Agent 装 Linux VM 旁路 vs Windows 原生二进制？v1.0 前只支持 Linux Agent。
