# 主服务部署

主服务 = **postgres + yusui-server + web SPA**。有两种形态:

- **本地 mock 栈** —— 不需要 NetBird、不需要 agent,一条命令起,用来看产品/开发。
- **生产形态** —— server 作为一个 NetBird Peer、开 gRPC 网关接 agent。

## 本地体验(mock 栈)

```bash
docker compose -f deploy/docker-compose.yml up --build -d
# 打开 http://localhost:8088 ,登录 admin / Admin12345!@
```

它起了:`postgres` → `migrate`(一次性,以 DDL-owner 角色建表)→ `server`(以最小权限 app 角色运行,`AGENT_GATEWAY=mock`)→ `web`。这套不含真 agent/overlay,工单的放行是 mock 的——足够演示工单/审批/UI。

> 端口:web `:8088`、server `:8080`、postgres `:5433`(宿主 5432 常被占)。用 `deploy/.env` 覆盖,见 [`deploy/.env.example`](https://github.com/catundercar/yusui/blob/main/deploy/.env.example)。

## 生产形态

生产要接真 agent,server 必须:① 是一个 **NetBird Peer**(才能拨 agent 的 overlay IP);② `AGENT_GATEWAY=grpc` 并在 overlay 上开 gRPC 端口给 agent 注册。

### 前置:NetBird 控制面

先把 NetBird 控制面跑起来(自托管见仓库 [`deploy/netbird/`](https://github.com/catundercar/yusui/tree/main/deploy/netbird),或用 NetBird 云),拿到:

- 一个 **PAT**(`nbp_...`)给 Server 适配器(用来装那条常驻策略)。
- 一个 **reusable setup key** 给 server peer(和后面的 agent)入网。

### 让 server 成为 Peer(sidecar 模式)

推荐:一个官方 `netbirdio/netbird` 容器做 peer,`yusui-server` 跑在它的网络命名空间里(`--network container:<peer>`),于是 server 在 peer 的 overlay IP 上监听/拨号。这正是 [`deploy/demo/up.sh`](https://github.com/catundercar/yusui/blob/main/deploy/demo/up.sh) 第 4 步的模式(可直接抄):

```bash
# 1) server peer 入 overlay
docker run -d --name yusui-server-peer --cap-add NET_ADMIN --device /dev/net/tun \
  -e NB_SETUP_KEY=<SETUP_KEY> -e NB_MANAGEMENT_URL=<MGMT_URL> -e NB_HOSTNAME=yusui-server \
  netbirdio/netbird:latest
SRV_OIP=$(docker exec yusui-server-peer sh -c "ip -4 -o addr show wt0 | awk '{print \$4}' | cut -d/ -f1")

# 2) yusui-server 跑在 peer 的 netns 里,开 gRPC 网关
docker run -d --name yusui-server --network container:yusui-server-peer \
  -e DATABASE_URL="postgres://yusui_app:<APP_PW>@<pg-host>:5432/yusui?sslmode=disable" \
  -e HTTP_ADDR=":8080" \
  -e JWT_SECRET="<强随机>" -e CREDENTIAL_KEY="<强随机>" \
  -e ADMIN_USERNAME=admin -e ADMIN_PASSWORD="<强口令>" \
  -e AGENT_GATEWAY=grpc -e AGENT_GRPC_ADDR="0.0.0.0:9091" \
  -e AGENT_REGISTER_TOKEN="<YuSui 接入 token>" \
  -e SERVER_PEER_IPS="$SRV_OIP" \
  -e NETBIRD_ENABLED=true -e NETBIRD_MGMT_URL="<MGMT_URL>" -e NETBIRD_TOKEN="<PAT nbp_...>" \
  -e RECORDINGS_DIR=/home/nonroot/recordings \
  yusui-server:latest serve
```

`SERVER_PEER_IPS` 是 server 的 overlay IP,也是单层 ACL 的**源**;`AGENT_REGISTER_TOKEN` 是发给 agent 的 YuSui 接入口令(见 [Agent 部署](/deploy/agent))。

### 配置参考

| 环境变量 | 说明 |
|---|---|
| `DATABASE_URL` | 运行期用**最小权限** `yusui_app` 角色;建表用 `yusui_migrate`(一次性 `migrate` 子命令)。两个角色由 `deploy/postgres/init` 建,`audit_logs` 对 app 角色仅 INSERT。 |
| `JWT_SECRET` / `CREDENTIAL_KEY` | **必须强随机**;弱值 server 会打告警。`CREDENTIAL_KEY` 解密资产 SSH 凭据。 |
| `ADMIN_USERNAME` / `ADMIN_PASSWORD` | 首次启动(users 表为空)时种入管理员。 |
| `AGENT_GATEWAY` | `mock`(本地)/ `grpc`(生产,接真 agent)。 |
| `AGENT_GRPC_ADDR` | gRPC 网关监听地址,`0.0.0.0:9091` → 在 overlay 上可达(Server 不暴露公网 gRPC)。 |
| `AGENT_REGISTER_TOKEN` | agent 注册口令,需与 agent 的 `YUSUI_REGISTER_TOKEN` 一致。 |
| `SERVER_PEER_IPS` | server 的 overlay IP(ACL 源,逗号分隔可多个)。 |
| `NETBIRD_ENABLED`/`_MGMT_URL`/`_TOKEN` | 启用 NetBird 适配器并装常驻策略 `yusui:builtin:server-to-agents`;失败仅阻断新接入,不停机。 |
| `RECORDINGS_DIR` | 录像落盘目录(distroless 下用 nonroot 可写路径)。 |

### web SPA

`web` 镜像是静态 SPA + 同源 `/api` 反代(REST + WebSocket)到 server。把它放在 server 前面(同源),浏览器只访问 web 的地址。

## 数据库与运维

- **迁移**:`yusui-server migrate`(goose,内嵌)。所有 ALTER 向后兼容,生产无破坏性 DOWN。
- **备份**:`pgdata` 卷 + 常规 `pg_dump`;`audit_logs`/录像元数据是合规关键。
- **重启韧性**:postgres 用 `restart: unless-stopped`,否则 daemon 重启后 server 先起会因 DNS 失败 502。

部署完 server,下一步:[把一台机器接入 →](/deploy/agent)。
