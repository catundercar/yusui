# 03 · Agent ↔ Server 协议

## 3.1 选型

**gRPC over mTLS over NetBird Overlay**。

| 维度 | 选择 | 理由 |
|---|---|---|
| 传输 | gRPC（HTTP/2） | 双向流、强类型、生态好 |
| 序列化 | Protobuf | 版本兼容性好 |
| 加密 | mTLS（私 CA） | 双向认证，不依赖外部证书机构 |
| 网络 | 走 NetBird Overlay | Agent 本来就是 Peer，零额外暴露 |
| 长连接 | bi-directional stream + keepalive 10s | 命令低延迟 |

走 Overlay 的好处：Server 不需要暴露公网 gRPC 端口；Agent 出去的 NetBird 隧道天然就能到 Server。

## 3.2 服务定义

```proto
syntax = "proto3";
package yusui.agent.v1;

service AgentControl {
  // 注册：Agent 启动时调一次，拿到 session token
  rpc Register(RegisterRequest) returns (RegisterResponse);

  // 双向流：注册后保持
  // 上行：心跳、事件、对账响应
  // 下行：规则下发/撤销、对账请求、配置更新
  rpc Control(stream AgentToServer) returns (stream ServerToAgent);
}

message RegisterRequest {
  string project_code   = 1;
  string netbird_peer_id = 2;
  string hostname       = 3;
  string agent_version  = 4;
  string setup_token    = 5;  // YuSui 注册令牌(=安装脚本 YUSUI_REGISTER_TOKEN,1h)；勿与 NetBird setup_key 混淆
  bytes  csr            = 6;  // mTLS 客户端证书签名请求
}

message RegisterResponse {
  string agent_id       = 1;
  bytes  signed_cert    = 2;  // Server CA 签发的 mTLS 证书，有效期 90d
  bytes  ca_cert        = 3;
  string session_token  = 4;  // 在 Control stream 的 metadata 中携带
  ControlConfig config  = 5;
}

message ControlConfig {
  uint32 heartbeat_sec  = 1;  // 默认 10
  uint32 freeze_after_sec = 2; // 默认 60
  uint32 reconcile_interval_sec = 3; // 默认 300
}

// ---- 上行 ----
message AgentToServer {
  oneof msg {
    Heartbeat    heartbeat    = 1;
    AckCommand   ack          = 2;
    RuleEvent    rule_event   = 3;
    AssetReport  asset_report = 4;
    ReconcileResponse reconcile_resp = 5;
  }
}

message Heartbeat {
  google.protobuf.Timestamp ts = 1;
  AgentStatus status = 2;
  uint64 active_rules = 3;
  string netbird_status = 4;  // "connected" / "disconnected"
}

enum AgentStatus { READY = 0; DEGRADED = 1; FROZEN = 2; }

message AckCommand {
  string command_id = 1;
  AckResult result  = 2;
  string error_msg  = 3;  // 非 OK 时
}

enum AckResult { OK = 0; FAILED = 1; PARTIAL = 2; SKIPPED = 3; }

message RuleEvent {
  string rule_id = 1;
  RuleEventKind kind = 2;
  uint64 packet_count = 3;
  google.protobuf.Timestamp at = 4;
}

enum RuleEventKind {
  RULE_APPLIED = 0;
  RULE_REVOKED = 1;
  RULE_EXPIRED_LOCAL = 2;  // nftables timeout 触发
  RULE_HIT_FIRST    = 3;   // 第一次命中
  RULE_HIT_LAST     = 4;   // 撤销时上报最终计数
}

message AssetReport {  // v0.2+
  repeated AssetCandidate candidates = 1;
}
message AssetCandidate {
  string ip = 1;
  string mac = 2;
  string hostname_guess = 3;
  repeated uint32 ports_seen = 4;
}

// ---- 下行 ----
message ServerToAgent {
  oneof msg {
    ApplyRule    apply   = 1;
    RevokeRule   revoke  = 2;
    ReconcileRequest reconcile = 3;
    UpdateConfig config  = 4;
    Drain        drain   = 5;  // 准备升级/下线，停止接新规则
  }
}

message ApplyRule {
  string command_id = 1;
  string rule_id    = 2;     // YuSui 全局唯一；写进 nft element comment
  string ticket_id  = 3;     // 反向追溯
  // src_peer_ip 已弃用（draft6）；保留以兼容 N-1 Agent 解析
  string src_peer_ip = 4 [deprecated = true];
  // draft7：多源 IP。v0.1 通常 1 个（Server 单副本）；v0.3 Server 水平扩展时多副本各为独立 Peer，每副本都要能 dial 资产
  // v0.2 access_kind=jumpserver 时再加 jumpserver-peer-ip
  repeated string src_peer_ips = 9;
  string dst_ip     = 5;     // 资产 IP
  uint32 dst_port   = 6;
  Protocol proto    = 7;
  google.protobuf.Timestamp expires_at = 8;
}

// Agent 行为：把 src_peer_ips 展开成 set element（每 src 一条），共用 rule_id（写 nft comment）。
// Revoke 时按 rule_id 一次性删全部展开 element。
// Agent N-1 兼容：若收到老 ApplyRule（仅 src_peer_ip 字段），等价于 src_peer_ips=[src_peer_ip]。

enum Protocol { TCP = 0; UDP = 1; ANY = 2; }

message RevokeRule {
  string command_id = 1;
  string rule_id    = 2;
  string reason     = 3;   // "expired" / "revoked_by_admin" / "ticket_closed"
}

message ReconcileRequest {
  string command_id = 1;
}

message ReconcileResponse {
  string command_id = 1;
  repeated string active_rule_ids = 2;
}

message UpdateConfig {
  string command_id = 1;
  ControlConfig config = 2;
}

message Drain {
  string command_id = 1;
  uint32 graceful_sec = 2;
}
```

## 3.3 命令语义

**幂等性**：所有 `ApplyRule` / `RevokeRule` 携带 `rule_id`。Agent 内部按 rule_id 去重，重复 Apply 等价于"确保存在"，重复 Revoke 等价于"确保不存在"。Server 可放心重试。

**ack 时机**：
- `ApplyRule` 必须在 nftables 真正写入后 ack。失败要立即上报 FAILED + 错误信息，不重试由 Server 决定。
- `RevokeRule` 删除 nft 元素后 ack。如果元素已不存在，返回 OK（幂等）。
- `ReconcileRequest` 收到后立即返回完整规则清单，不分页（10k 规模够用）。

**顺序保证**：双向流内消息严格有序。Server 不会在同一 stream 上对同一 rule_id 发出乱序命令（例如先 Revoke 再 Apply）。

## 3.4 对账协议

**触发时机**：
1. Agent Register 之后立即一次
2. 任一侧检测到 N 秒无消息后重连之后
3. Server 周期触发（默认 5 min）

**流程**：

```
Server ──ReconcileRequest(cmd_42)──▶ Agent
Agent ──ReconcileResponse(cmd_42, [r1, r2, r3])──▶ Server
Server 比对 DB 与 Response：
   set(DB.expected) - set(Agent.actual) = 缺失 → 发 ApplyRule
   set(Agent.actual) - set(DB.expected) = 孤儿 → 发 RevokeRule
   交集 → OK
```

对账时**仅持 `agent:<id>` 锁**（draft7 修正：不再全局暂停审批）；新工单 target 是其他 Agent 不受影响。

## 3.5 错误处理与重试

| 场景 | 处理 |
|---|---|
| gRPC stream 断 | Agent 立即重连（退避 1s → 30s 上限）；Server 把对应 Agent 标记 offline，下发指令进队列 |
| Apply 后 ack 丢 | Server 超时（默认 5s）→ 重发同 cmd_id；Agent 已应用则直接返 OK |
| Apply 失败 | Server 重试 3 次，仍失败 → 标 ticket=apply_failed，告警 |
| Revoke 失败 | Server 标 binding=revoke_pending，下次心跳重发；超 5 min 触发告警 |
| Agent 上报规则 DB 没有 | Server 发 Revoke；如果反复出现，告警（疑似手动改动） |

## 3.6 认证与授权

**Register 阶段**：
- Agent 拿 `setup_token`（即安装脚本的 `YUSUI_REGISTER_TOKEN`，Server 一次性颁发，有效期 1h；**与 NetBird `setup_key` 不是一回事**）
- Agent 生成本地 keypair，提交 CSR
- Server 验证 token → 用内置 CA 签证书（90 天）→ 返回

**Control 阶段**：
- 每次 gRPC 调用走 mTLS，客户端证书 CN = agent_id
- metadata 携带 session_token（Register 时分配，重启失效）
- Server 校验：mTLS CN 与 token 中的 agent_id 一致

**证书轮换**：
到期前 7 天 Agent 主动发 RenewCert（未在上面 proto 中，v0.2 加）。
紧急吊销：Server 维护 CRL，下次 Control 请求时拒绝。

## 3.7 版本兼容

- proto 字段只增不删，已废弃字段保留 `[deprecated=true]`。
- Agent 上报 `agent_version`，Server 维护"最低支持版本"。
- Server 与 Agent 跨 N-1 版本必须互通；N-2 警告但不阻断。
- 协议大版本（v2）通过新 service path `/yusui.agent.v2.AgentControl/...` 引入，老 Agent 走 v1。

## 3.8 未决问题

- gRPC 走 NetBird Overlay 的话，NetBird Mgmt 挂了 Agent 也连不上 Server → 是否需要一条 fallback 公网路径？倾向"故意不要"，简化威胁模型。
- 心跳 10s 对 1k+ Agent 时 Server 的 CPU 开销 → 监控后再调整为分桶心跳。
- 是否暴露 Agent → Server 的 reverse-tunnel 用于 ad-hoc 调试？v1.0 之前不做，避免攻击面。
