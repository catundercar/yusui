# 诊断记录:Web 终端回显卡顿 = OrbStack 的 host↔VM 虚拟网络

> **一句话结论**:卡顿不在 YuSui(server / overlay / forwarder / 录像),而在
> **回显路径上有 OrbStack VM**。OrbStack 的 **host↔VM 虚拟网卡会偶发地拖住小交互包
> (实测约 5% 的小包,尾延迟冲到 ~1.9s)**;浏览器在 host 上,每次按键来回都要
> `host → … → 资产 → host`,只要这条路上**有任何一个 VM**,就会被它拖卡。
> **彻底修复 = 路径上不放任何 VM**:agent 也跑成容器。改完后回显 1–3ms、零卡顿。

⚠️ **本文修正了一个早期错误结论。** 第一版只把"资产"从 VM 换成容器,以为就好了
(见 §6 Stage 1);但那只修好了 `agent→资产` 这一跳,`server→agent VM` 那一跳仍跨
VM 边界、仍卡(见 §7–§8)。完整修复见 §9。这份记录把**两段**都如实写下来。

这是一份事后排查记录(decision-record 口吻)。

---

## 1. 现象

- 在浏览器 Web 终端里打字,回显**一卡一卡**;DevTools 的 WS 帧里偶见单次回显 ~2.3s,
  后来又抓到一次**命令输出延迟 ~6s**(回车 28.451s → 输出 34.427s)。
- 第一直觉(用户提出):"是不是没走 P2P、全是本地栈代理,不该这么卡。"——方向对,
  "本地栈"里藏着一个非 YuSui 的虚拟化网络瓶颈。

## 2. 链路拓扑(定位时延来自哪一跳)

```
浏览器(host) ──ws──► demo-web ──► yusui-server ──┐
                                                 │ leg3: server → agent  (overlay, WireGuard)
                                          agent forwarder
                                                 │ leg4: agent → 资产
                                              资产 sshd
回显沿原路返回。一次按键回显 = 走两遍 leg3+leg4。
```

早期形态:server 是 **host 容器**,agent 是 **OrbStack VM**,资产是 **OrbStack VM**。
于是 leg4(agent VM ↔ 资产 VM)是 VM↔VM,leg3(server 容器 ↔ agent VM)跨 host↔VM。

## 3. 测量方法

- 用 `server/cmd/wstest` / 临时探针**逐次按键打点回显往返**,不靠"整体感觉"。
- 一次只改一个变量,其余(server、overlay、forwarder、录像、命令、时段)钉死。
- 看**分布**不看均值——卡顿是双峰的(多数快、少数卡),均值会抹平。

## 4. 被排除的假设

| # | 假设 | 验证 | 结论 |
|---|---|---|---|
| 1 | **Nagle**(小包攒批) | 查 Go `net` 默认值 | **排除**。Go TCP 默认 `SetNoDelay(true)`。 |
| 2 | **MOTD / shell 启动脚本** | 关掉再测 | **排除**。无变化。 |
| 3 | **录像写盘 I/O** | 录像目录指到 tmpfs | **排除**。无变化。 |
| 4 | **回车闸控 / 命令过滤阻塞** | 读 `webshell.go`/`filter.go` | **排除**。过滤是纯正则;回车走 default 分支**立即** `WriteStdin("\r")`,无等待。confirm 才有 10s 定时器,普通命令不碰。 |
| 5 | **agent forwarder 缓冲** | 读 `forward.go` | **排除**。双向裸 `io.Copy`,无缓冲无超时。 |
| 6 | **wstest 测出的 "4.2s"** | 读 wstest 源码 | **测量假象**。里面有 3.9s 是硬编码 `time.Sleep`。先校准尺子。 |

代码全路径无任何 6s 量级定时器 → 卡顿是**传输层**的、"攒批后一次性吐"的特征。

## 5. （第一段）A/B:资产 VM vs 容器,其余全等

只改"资产是 VM 还是容器"这一个变量,各跑 300 次按键回显:

| 资产形态 | leg4 | 回显分布(300 次) |
|---|---|---|
| 容器 | VM↔容器 | **298/300 < 10ms** |
| VM | VM↔VM | **64/300 ≈ 200ms** |

于是把资产换成容器,修好了 **leg4**。**早期结论(错):"换容器就跟手了。"**

## 6. 复发:换容器后仍卡

用户随后用 DevTools 抓到一帧:逐键回显 `d→d` **7ms(快)**,但**回车 `pwd` → 输出
差了 ~6s**。逐键打点探针证实(完整 SSH 路径,资产已是容器):

```
round 1: keystroke=432ms  cmd=216ms
round 2: keystroke=212ms  cmd=3ms
round 3: keystroke=3ms    cmd=215ms
round 4: keystroke=213ms  cmd=4ms
round 5: keystroke=1757ms cmd=6ms
```

**~50% 的往返仍卡 ~200ms,偶发 1757ms。** 敲键和命令都卡,不是命令专属。说明 leg4
修好了,但完整路径里**还有一跳在卡**——剩下的就是 **leg3(server 容器 ↔ agent VM,
走 overlay)**,它仍跨 host↔VM 边界。

## 7. （第二段）单跳隔离:把 leg3 钉死

突发小包 RTT 探针,对比两条路(其余不变):

| 路径 | p50 | p90 | max | 卡顿(>50ms) |
|---|---|---|---|---|
| **docker↔docker(对照)** | 0.2ms | 0.3ms | 0.4ms | **0/60** |
| **server 容器 → agent VM overlay IP(跨 host↔VM)** | 0.6ms | 1.0ms | **1870ms** | **4/80 (~5%)** |

纯容器↔容器零卡顿;一旦跨进 OrbStack VM,约 **5% 小包卡顿、尾延迟 1.87s**。SSH 每次
回显跨 leg3 好几个包,只要一个卡整次就卡 → ~5% 单包卡顿放大成 ~50% 按键卡顿,偶尔叠成
数秒。`ping` 看着干净(0.56ms)不矛盾:ping 是稀疏单包,不触发突发小包的攒批。

## 8. 根因(完整)

**(测得)** OrbStack 在 **host/容器 ↔ VM** 这个虚拟化边界上,会偶发地拖住小交互包
(实测 ~5%,尾巴 1.9s)。VM↔VM、host↔VM 都中招;容器↔容器干净。

**(关键推论)** 浏览器在 macOS host 上,按键回显来回必经 `host → … → 资产 → host`。
**只要这条路上还有任何一个 OrbStack VM,就会跨这条边界、就会卡。** 换资产容器只去掉了
leg4 的 VM;agent 本身是 VM,leg3 仍跨边界,所以还卡。

**(推断机制)** ~200ms 是 TCP delayed-ACK 的经典计时器值;最可能是 host↔VM 虚拟路径上
小包 ACK 被延迟/合并。机制是推断,**实测**的是 ~5% 单包卡顿、1.9s 尾延迟、以及"路径里
去掉 VM 即消失"。

## 9. 修复(完整)

把 **agent 也从 VM 换成容器**:`demo-agent` 是 netbird peer 容器(挂在 `assetnet`,
照搬 server peer 的模式),`demo-yusui-agent` 跑在它的 netns 里读 `wt0`。这样回显路径
`browser → server 容器 → agent 容器 → 资产容器` **全程容器、零 VM 边界**;overlay 仍是
两个 peer 容器之间的真 WireGuard。改完后实测(完整 SSH 路径):

```
round 1: keystroke=11ms  cmd=2ms     ← 首次冷启动
round 2: keystroke=1ms   cmd=2ms
round 3: keystroke=2ms   cmd=2ms
round 4: keystroke=2ms   cmd=3ms
round 5: keystroke=3ms   cmd=1ms
round 6: keystroke=3ms   cmd=3ms
```

**1–3ms,零 ~200ms 卡顿,零数秒尾延迟。** 关键路径不变:`rm -rf /` 拦截、`shutdown`
确认、`whoami`=`ops-yusui` 全部正常。

> **取舍(诚实)**:agent *容器* 不如 agent *VM* 像"真实网关机器"。但它演示的架构完全
> 相同——agent 是唯一 peer、资产在它后面、server 只能经 per-ticket forwarder + 真
> overlay 到资产。要"真 VM 承载 agent"的保真度,就得接受 OrbStack 的这个回显卡顿
> (真实网络不会这样)。我们选了跟手。见 `up.sh` / `README.md`。

## 10. 如何复现这次测量(可验证)

```bash
cd deploy/demo && ./up.sh                     # 全容器:回显应 ~全部 <10ms
# 逐键打点(完整 SSH 路径):
go run ./server/cmd/wstest "ws://localhost:8091/api/v1/ws/tickets/<id>/terminal" "<token>"
# 要复现"卡"的一侧:把 agent 或资产临时改回 OrbStack VM,重测 → ~50% 卡 200ms、尾巴数秒
# 单跳直测:docker↔docker vs 容器→VM-overlay-IP 的突发小包 RTT → 前者干净、后者 ~5% 卡 1.9s
```

## 11. 诚实声明

- **早期结论"换容器就跟手"是错的、过度乐观**——只修了 leg4。完整结论是"路径里不能有
  任何 VM",所以 agent 也得是容器(§9)。这份记录保留了这个纠错过程。
- DevTools 里 ~2.3s / 6s 是**长尾**,不是常态;常态是 ~50% 按键卡 ~200ms。
- wstest 最初的 "4.2s" 是测量桩里的硬编码 sleep 污染,先校准再下结论。
- "delayed-ACK / host↔VM 虚拟网卡攒批"是机制**推断**;**实测**的是卡顿分布与"去掉 VM 即消失"。

## 未决问题

1. 是否需要在某处保留一个"真 VM 承载 agent"的高保真变体(接受卡顿)用于演示网络隔离?
   目前默认全容器、跟手。
2. 若以后必须在 OrbStack 上跑 VM 且要交互流畅,需确认能否调小 host↔VM 的 delayed-ACK /
   关掉虚拟网卡攒批(目前未投入,因为全容器已满足目标)。
