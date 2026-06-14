# Agent 部署(一条命令)

Agent 是一个项目接入 YuSui 的**唯一入口**:它是该项目唯一的 NetBird Peer,资产藏在它后面,Server 只能经它的按工单转发器到达资产。

目标:把一台机器接入,**像 Tailscale 那样——复制一条命令贴上去即可**。

## TL;DR

```bash
# ① 管理员:为这个项目生成一条接入命令(自动 mint NetBird setup key 并打包)
./deploy/agent/make-enroll.sh \
  --server 100.118.0.5:9091 --project demo --register-token <YuSui register token> \
  --mgmt-url https://netbird.example --pat nbp_xxx

# ② 它会打印出这一条 —— 在目标机器上(root)执行:
curl -fsSL https://raw.githubusercontent.com/catundercar/yusui/main/deploy/agent/install-agent.sh \
  | sudo bash -s -- --enroll <TOKEN>

# ③ 管理员:回 YuSui 页面【资源管理 → Agent → 批准】(step-up 二次验证)
```

完。装好后 agent 自己把 NetBird 拉起来、向 Server 注册成 **pending**,你在页面点一下批准就接入了。

## 这条命令背后做了什么

`install-agent.sh`(目标机器、root、幂等):

1. **装 NetBird 守护进程**(若缺失,走官方 `pkgs.netbird.io/install.sh`)。
2. **装 `yusui-agent` 二进制**(从 GitHub Releases 下载对应架构,或 `--binary <path>` 用本地)。
3. 写 **`/etc/yusui/agent.env`**(`0640`)+ **systemd unit** `yusui-agent.service`。
4. `systemctl enable --now yusui-agent` 启动。

随后 **agent 自己**(不是脚本)执行 `netbird up --setup-key ...` 入网、读 `wt0`,再用 register token 注册。

> **边界**:装 NetBird 守护进程 + 驱动是**安装器**一次性的活(机器没法自举依赖,Windows 还要装 wintun 驱动);而 `netbird up` 入网及之后的生命周期由 **agent** 管(`YUSUI_NB_SETUP_KEY` / `YUSUI_NB_MGMT_URL` 是 agent 的配置)。

## 前置:发布 agent 二进制

`install-agent.sh` 默认从 Releases 下载。先发布一次:

```bash
make agent-dist                      # → dist/yusui-agent-linux-amd64 / -arm64 / windows-amd64.exe
gh release create v0.1.0 dist/yusui-agent-* --title v0.1.0 --notes "agent binaries"
```

没有 Release 也能用:给 `install-agent.sh` 传 `--binary /path/to/yusui-agent-linux-amd64`(先用 `scp`/`orb push` 把二进制拷上去)。

## 两个口令,别搞混

| 口令 | 谁用 | 来源 |
|---|---|---|
| **NetBird setup key**(`--netbird-key`) | agent 入 overlay | 控制面 mint(`make-enroll.sh` 自动做) |
| **YuSui register token**(`--register-token`) | agent 接入 YuSui | = Server 的 `AGENT_REGISTER_TOKEN` |

还有个前提:**项目(`--project` 的 code)必须先在页面建好**,否则 agent 注册会报 "project not found"。

## 手动方式(不想用脚本)

`install-agent.sh` 只是把下面这些固化了。你也可以手动:把二进制放到 `/usr/local/bin/yusui-agent`,写 `/etc/yusui/agent.env`:

| 环境变量 | 说明 | 示例 |
|---|---|---|
| `YUSUI_SERVER_GRPC` | Server 的 overlay gRPC 地址 | `100.118.0.5:9091` |
| `YUSUI_PROJECT` | 项目 code(须先存在) | `demo` |
| `YUSUI_REGISTER_TOKEN` | YuSui 接入口令 | `…` |
| `YUSUI_HOSTNAME` | 显示名 | `web-1` |
| `YUSUI_ENFORCER` | `forward` = 用户态 L4 转发(跨平台,无 nftables) | `forward` |
| `YUSUI_OVERLAY` | `netbird` = 由 agent 管 NetBird | `netbird` |
| `YUSUI_NB_IFACE` | overlay 网卡 | `wt0` |
| `YUSUI_NB_SETUP_KEY` | NetBird 入网 key(agent 用它 `netbird up`) | `…` |
| `YUSUI_NB_MGMT_URL` | NetBird 管理面地址 | `https://netbird.example` |

再用 systemd 拉起(`EnvironmentFile=/etc/yusui/agent.env`、`Restart=always`),单元模板见 [`deploy/agent/install-agent.sh`](https://github.com/catundercar/yusui/blob/main/deploy/agent/install-agent.sh)。

## 验证 + 排错

```bash
journalctl -u yusui-agent -f
```

健康路径的日志关键字:
- `netbird up issued` / `overlay interface up` —— agent 自己把 NetBird 拉起来了。
- `registered enrollment=pending` —— 已向 Server 注册,**等页面批准**。
- 批准后:`control stream open` + `forwarder up`(有工单时)。

常见问题:

| 现象 | 多半是 |
|---|---|
| `project not found` | 项目 code 没在页面先建,或 `YUSUI_PROJECT` 拼错 |
| 注册不上、连不到 Server | `YUSUI_SERVER_GRPC` 的 overlay IP 错,或 agent 没入网(看有没有 `wt0`) |
| 页面看不到这台 agent | register token 与 Server 的 `AGENT_REGISTER_TOKEN` 不一致 |
| 同项目第二台报唯一约束 | 一个项目只能有一个 `primary` agent(`UNIQUE(project_id, role)`)——用新项目或 `secondary` |

## 接入之后

在页面【资源管理】里:**项目 → 资产 → 凭据**(资产挂在哪个项目 = 决定它走哪个 agent),然后 **工单 → 提工单 → 审批 → 打开终端**。

## 路线图:UI 一键

下一步把上面的 ①② 收进产品:管理员在 **资源管理 → Agent → 添加** 选好项目,页面直接给出**那条带 token 的安装命令**(Server 现签 enrollment token、内置安装脚本下载入口),运维复制即用——`make-enroll.sh` 是它的命令行雏形。
