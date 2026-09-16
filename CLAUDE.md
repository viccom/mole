# moleAgent\_Serv

代理隧道服务端，负责节点管理、隧道转发、认证鉴权和 MQTT Broker。

## 技术栈

**后端**: Go 1.25, 标准库为主
- smux (github.com/xtaci/smux) — TCP 多路复用
- redka + SQLite — 持久化（用户/角色/配置/接入 Token）
- mochi-mqtt — 内嵌 MQTT Broker
- bcrypt + JWT — 认证
- sha256 + crypto/rand — 接入 Token 哈希存储
- lumberjack — 日志轮转

**前端 (admin/)**: React 18 + TypeScript + Vite + TailwindCSS + Headless UI

**手机端 (mobile/)**: React 18 + TypeScript + Vite + TailwindCSS（独立前端，底部 Tab 导航）

## 项目结构

```
cmd/moleagent-serv/     入口
internal/
  config/               配置加载 (YAML + 环境变量)
  core/                 领域模型、接口、错误、事件总线
  storage/              redka 仓储层 (UserRepo, RoleRepo, NodeRepo, AccessTokenRepo, FeishuBindingRepo)
  auth/                 JWT、bcrypt、RBAC、中间件
  api/                  HTTP API 路由与处理器
  feishu/               飞书开放平台 API 客户端（tenant_access_token、用户信息）
  tunnel/               隧道核心 (control, http, tcp, udp, registry, stats, transport)
  node/                 分片节点管理器 + 健康检查
  service/              业务服务（接入 Token 认证、隧道配置）
  mqtt/                 内嵌 MQTT Broker（含 p2p 信令 authHook）
  stun/                 内嵌 STUN 服务 (:3478，p2p 打洞地址探测兜底)
  logging/              日志初始化
configs/                配置文件模板
admin/                  PC 前端 React SPA（构建产物部署在 admin/dist/）
mobile/                 手机端独立前端（底部 Tab 导航，构建产物部署在 mobile/dist/）
```

## 架构要点

**节点连接**: 节点通过控制端口连接 → Challenge-Response 认证 → 建立 smux 会话 → 注册隧道配置。传输层通过 `Transport` 接口抽象（`tunnel/transport.go`），当前实现 `TCPTransport`（TCP/TLS）、`WSTransport`（WS/WSS）和 `KCPTransport`（KCP，支持 FEC 和 AES 加密）。支持**多协议同时监听**：`control_port` 为 TCP 主监听，`ws_port` 和 `kcp_port` 配置额外 WS/KCP 监听（空=禁用），三种协议共用 worker pool 和认证流程。单协议模式仍通过 `server.transport` 选择（`tcp`/`ws`/`kcp`）。`ControlServer.AddTransport()` 动态添加额外 listener。

**节点接入认证**: 支持两级 Token — 用户级 AccessToken（优先）和旧全局 nodetoken（兼容）。接入后自动绑定 `Node.OwnerUserID`。

**隧道转发**: 网关收到外部请求 → 路由匹配(域名/路径/端口) → 通过节点的 smux 会话 OpenStream 转发数据。`enabled=false` 的隧道不参与路由。双向透传使用 1MB buffer 优化大流量场景（如 RDP）。

**节点管理**: ShardedNodeManager(256分片) + 健康检查(30s间隔, 90s超时)。节点断开时由 `ReleaseNodeResources` 清理监听器和统计。

**认证流程**: JWT token (Bearer/Cookie) → AuthMiddleware → RBAC 权限检查 + 资源归属过滤。

**资源归属**: RBAC 管资源类型权限，`resource_scope.go` 管资源实例归属（`OwnerUserID`）。管理员全局可见，普通用户仅看自己的节点/隧道。

## 数据流

```
外部请求 → Gateway 端口(:9980) → TunnelGateway.ServeHTTP
  → 路由匹配(泛域名/路径/精确域名, 跳过 enabled=false) → nodeMgr.GetSession() → smux.OpenStream()
  → biCopy 双向透传(1MB buffer) → counting wrapper 采集运行时统计

节点连接 → Control 端口(:9981) → Challenge-Response
  → NodeAccessAuthenticator 统一认证（用户级 Token 优先 → 旧全局 Token 兜底）
  → smux.Server() → AcceptStream() → register/ping/tunnel_update 控制命令
  → handleRegister 写入 OwnerUserID + AccessTokenID
  → TunnelConfigService.LoadPersisted() 下发持久化隧道配置
```

## 依赖注入流程

`cmd/moleagent-serv/main.go` 按以下顺序初始化组件（后面的依赖前面的）：

```
config.Load() → logging.Init() → storage.Init()
  → userRepo, roleRepo, nodeRepo, accessTokenRepo (仓储层)
  → jwtMgr → rbacEngine → authSvc → authMW (认证链)
  → nodeMgr (分片节点管理器)
  → gateway (TunnelGateway) — 引用 nodeMgr
  → transport (TCP/WS/KCP) → controlSrv — 引用 nodeMgr, nodeRepo
  → controlSrv.AddTransport() (额外 WS/KCP listener)
  → tunnelSvc (TunnelConfigService) — 引用 nodeMgr, nodeRepo, gateway, controlSrv
  → controlSrv.SetTunnelConfigManager(tunnelSvc)
  → disconnectHandler → controlSrv.SetOnNodeDisconnect + health check
  → nodeAccessAuth → controlSrv.SetAuthenticator
  → mqttBroker (可选)
  → apiRouter → buildAPIRouter(所有 handler) — 引用上述所有依赖
```

新增组件时注意：
- 在 `main.go` 中按依赖顺序创建
- 通过构造函数注入依赖，不要在包内使用全局变量
- 回调函数（`onNodeChange`, `onNodeDisconnect`）在组件创建后、启动前设置

## 关键约定

- `UserStatus` / `NodeStatus` / `AccessTokenStatus` 是类型化常量，不使用原始字符串
- `core.UserRepo` / `core.RoleRepo` / `core.AccessTokenRepo` 是接口，storage 包提供实现
- `core.NodeAccessAuthenticator` 是接入认证接口，service 包提供实现
- `core.TunnelConfigManager` 是隧道配置变更接口（ApplyTunnel/RemoveTunnel/ReplaceTunnels/SyncFromClient/LoadPersisted），service.TunnelConfigService 提供实现
- smux Session 存储在 `ShardedNodeManager` 内部 `sessions` map 中，与 `core.Node` 领域模型分离
- API 路由注册在 `cmd/moleagent-serv/main.go` 的 `buildAPIRouter`
- 种子数据在 `storage/db.go` 的 `seedData()`
- 修复BUG，改进功能，都要保证原来的业务功能正确。
- **开发规范**：详见 [开发规范.md](开发规范.md)，包含编码、API、日志和架构约束。
- **隧道配置真相源规则**：
  - **持久化配置是管理真相源**（NodeRepo/Redka 持久化层）
  - **在线节点内存态是运行副本**（ShardedNodeManager 内存）
  - **客户端注册带来的配置更新**，必须经过统一入口落库
  - 节点重连时，优先使用持久化配置覆盖客户端上报的空配置
  - REST API 增删隧道时，同时更新内存态和持久化
- `TunnelHandler.Create()` 对非管理员校验 node_id 归属，非 owner 返回 404
- `TunnelHandler.Stats()` 按归属过滤，非管理员只能看到自己节点的隧道统计
- `NodeHandler.Create()` 自动绑定 OwnerUserID：管理员创建 → system，普通用户 → 自己
- `UserHandler.Delete()` 级联处理同时更新持久化层（nodeRepo）和运行态（nodeMgr），保证在线节点归属同步
- `AccessTokenRepo.Update()` 维护 hash 索引一致性：hash 变更时删除旧索引、建立新索引，操作失败有回滚保护
- 修改代码前必须先  git pull  同步最新远程代码

## 节点模型语义

- **预配置节点**：通过 REST API 预先创建的离线节点，Status=offline，无 session
- **在线节点**：通过控制端口注册并建立 smux 会话的节点，Status=online，有 session
- **运行态连接**：smux Session 存储在 ShardedNodeManager.sessions map 中，与 Node 结构体分离
- **持久化隧道配置**：存储在 Redka 的 `nodes` hash 中，重启后可恢复
- **客户端注册与服务端持久化配置的优先关系**：节点注册时，服务端以持久化配置优先（覆盖客户端上报的空配置）

## 用户级接入 Token

### 核心概念

- **AccessToken**：归属于用户的节点接入凭据，仅存 sha256 hash，明文仅创建/轮换时返回一次
- **Node.OwnerUserID**：节点归属用户，接入时自动绑定；用于资源可见性过滤
- **Node.AccessTokenID**：审计用，记录节点由哪个 Token 接入
- **Tunnel.Enabled**：`*bool` + `omitempty`，nil 视为启用（向后兼容旧数据）

### 接入认证流程

1. 客户端发送 raw token
2. `ControlServer` 统一委托给 `NodeAccessAuthenticator`
3. `AccessTokenAuthService` 先尝试 sha256 hash 查找用户级 Token
4. 找到且 active → 返回 `NodeAccessGrant{UserID, AccessTokenID, LegacyGlobal=false}`
5. 未找到 → 用 `subtle.ConstantTimeCompare` 比对旧全局 Token
6. 匹配 → 返回 `NodeAccessGrant{UserID:"system", LegacyGlobal=true}`
7. 都不匹配 → 认证失败
8. 无 `authenticator` 注入时回退到直接比对旧全局 Token（兼容未注入场景）

### 资源归属过滤

- `api/resource_scope.go` 提供 `IsAdmin`、`CanAccessNode`、`FilterNodes`、`checkNodeOwnership`
- Handler 层过滤，Repo 层不感知归属（保持数据读写纯粹）
- `checkNodeOwnership` 在 claims 为 nil 时放行（测试兼容）

### 存储结构（Redka）

| Hash | Key | Value | 用途 |
|------|-----|-------|------|
| `access_tokens` | tokenID | AccessToken JSON | Token 数据 |
| `access_token_hash_index` | sha256(rawToken) | tokenID | O(1) Token 查找 |

**索引一致性**：Create/Update/Delete 均维护 hash 索引。Update 时若 TokenHash 变更（轮换），自动删除旧索引并建立新索引，操作失败有回滚保护。

### 级联处理

- 删除用户时：该用户 AccessToken 标记 disabled；归属节点 OwnerUserID 改为 "system"
- 级联同时更新持久化层（nodeRepo）和运行态（nodeMgr），保证在线节点归属立即生效
- 已在线节点不强制断开

### 迁移

- 服务启动时 `migrateNodeOwnership()` 将 `OwnerUserID==""` 的节点设为 "system"
- 旧全局 Token 保留兼容，文档中标记为待弃用

### Tunnel.Enabled 链路

- **索引层**：`RebuildIndex` 跳过 `!IsEnabled()` 的隧道
- **HTTP 网关**：`findNodeTunnel` + 全量扫描兜底均检查 `IsEnabled()`
- **TCP 转发**：`findNodeForTunnel` 检查 + 运行时二次检查
- **UDP 转发**：共享 `findNodeForTunnel` + 运行时二次检查
- **统计 API**：区分 `total_tunnels` / `enabled_tunnels` / `active_tunnels`（仅 online+enabled）

### 个人 Access Token API

- `GET/POST/DELETE /api/v1/me/access-tokens` + `POST .../rotate`
- 使用 `RegisterAuth`（仅认证，无 RBAC 资源）
- 从 JWT claims 直接取 UserID，用户只能管理自己的 Token


### 飞书企业自建应用 SSO

**配置**: `feishu.app_id` + `feishu.app_secret`（YAML 或 `MA_FEISHU_APP_ID` / `MA_FEISHU_APP_SECRET` 环境变量）

**认证流程**:
1. 前端检测飞书环境 → JSAPI `requestAuthCode(appId)` 获取 auth_code
2. `POST /api/v1/auth/feishu/callback { code }` → 后端用 tenant_access_token + auth_code 换用户信息
3. 已绑定 → 签发 JWT 一键登录；未绑定 → 返回 `need_bind` + 临时 `feishu_token`（5 分钟有效）
4. `POST /api/v1/auth/feishu/bind { feishu_token, username, password }` → 验证密码 → 写绑定 → 签发 JWT

**绑定存储**（Redka）:
- `feishu_bindings` hash: `open_id → { user_id, feishu_name, avatar_url, bound_at }`
- `feishu_user_bindings` hash: `user_id → open_id`（反向索引）
- `feishu_bind_tokens` string: 临时令牌，5 分钟 TTL

**前端**:
- PC 端（admin/）：LoginPage 检测飞书环境自动 SSO，非飞书环境显示「飞书登录」按钮；FeishuBindPage 处理首次绑定
- 手机端（mobile/）：独立前端，底部 Tab 导航（节点/隧道/设置），自动触发飞书 SSO，仅节点管理和隧道管理

**关键约定**:
- 一个飞书 open_id 只能绑定一个 moleAgent 账户
- 一个 moleAgent 账户也只绑一个飞书身份
- `GET /api/v1/auth/feishu/config` 公开端点返回 `app_id`（供前端 JSAPI 调用）
- `GET/DELETE /api/v1/me/feishu-bindings` 需认证端点（查看/解绑）
- `internal/feishu/client.go` 封装飞书 API，tenant_access_token 缓存 2 小时
- 飞书未配置（app_id 为空）时所有飞书端点返回 503

### 隧道创建归属校验

- `TunnelHandler.Create()` 在执行隧道配置变更前校验 `node_id` 归属
- 非管理员只能给自己的节点添加/修改隧道
- 管理员不受限制

### 隧道类型扩展

- 标准类型 `http/https/tcp/udp`：REST API 和客户端注册均支持，服务端校验 target 格式为 `host:port`
- 客户端本地类型 `ser2mq/vpn-manager/ser2tcp/ser2udp/webssh`：仅客户端注册时通过，服务端不校验 target 格式，配置在 `Tunnel.Para` (json.RawMessage) 中
- `p2p`：服务端只做 para 校验与持久化，**不参与数据转发**（两端打洞直连）。REST API 同样接受（`tunnel_handler.go` 的 `clientLocalTypes` 含 `p2p`）
- REST API (`TunnelHandler.Create`) 接受标准四种类型 + 客户端本地类型，客户端本地类型由节点自行注册

### p2p 隧道（两层契约）

- **Para 两层结构**（契约 §0.2，与客户端逐字一致）：连接参数（`room/modes/relay_server/mqtt_brokers/stun_servers`，两端对称）+ `mappings[]`（仅访问发起端配置，随 `TUNNEL:OPEN` 在线传对端）。校验在 `core/validate_para.go` 的 `ValidateP2PPara`（§0.3 规则，含同 para 内 local_port 去重；旧顶层单组映射字段已废弃）
- **room 配对不变量**：`service/tunnel_service.go` 的 `validateP2PRoomPairing`——同 room 全局最多 2 条记录且分属 2 个不同节点（p2punch 假设 room 内恰两端，第三端持同 room 入场等于把流量隧穿给陌生节点，必须挡在落库前）
- **信令凭据**：C→S 控制命令 `p2p_signal_token` → `P2PSignalTokenService` 签发（TTL 24h，仅存 hash）；内嵌 MQTT Broker 的 authHook 校验 `p2p-signal:<tokenID>` 用户名。客户端信令默认**公共 broker 优先（同 p2punch）+ 本服务端 broker 兜底**
- **内嵌 STUN**：`:3478`（`stun/server.go`），作客户端打洞地址探测的兜底之一
- **部署顺序（关键）**：para 契约变更必须 ①服务端二进制 → ②admin 前端 → ③客户端，顺序不可颠倒（先上前端而后端未换，用户建 p2p 隧道会 500）

### 隧道运行时统计

- `tunnel/counting.go` 包装连接，采集每条隧道的流量(bytes_in/out)、连接数、活跃状态
- `core.TunnelStatsReader` 接口解耦 api 层与 tunnel 层
- `GET /api/v1/tunnels/usage` 汇总统计，支持 `node_id`/`type`/`status` 查询参数过滤
- 统计 key 格式：`nodeID/tunnelName`，节点断开时通过 `ReleaseNodeResources` 清理

### 节点断开资源回收

- `TunnelConfigService.ReleaseNodeResources()` 负责清理离线节点的 TCP/UDP 监听器、路由索引和统计条目
- 不修改持久化配置，节点重连时通过 `applyRuntimeTunnels` 重新激活
- 由 `controlSrv.SetOnNodeDisconnect` 和 `health check` 两处触发

### 节点故障重连快速恢复

**三层防护机制**（所有传输协议共用 1、2）：

1. **goroutine 置换保护**：`setupSmuxAndAccept` 的 defer 检查 displacement 状态——旧 goroutine 发现新连接已接管时跳过 Remove，防止误删新注册节点
2. **probeOldSession 探测**：`handleRegister` 发现 nodeID 已存在时，3 秒超时探测旧 session（OpenStream → 写 tunnel_push → 等响应）。真活→拒绝重连；假死→置换，新连接接管
3. **传输层差异**：
   - **TCP/WS**：新连接始终可达（新 socket/upgrade），无需额外处理，重连总耗时约 5-8 秒
   - **KCP**：需要特殊处理（见下文）

**KCP 重连特殊机制**：

- smux keepalive **5s/15s**（TCP/WS 为 30s/90s），加速假死 session 检测
- 客户端发送 probe byte `\x00` 触发服务端 kcp-go Accept，服务端 `handleConnection` 必须先读取丢弃，否则 `\x00` 残留污染 auth JSON 读取
- `kcpListener` 按 remote address 追踪连接，新连接到达时主动关闭旧连接（stale connection tracking）
- kcp-go listener 内部 sessions map 按 conv+sn 处理路由：conv 匹配→喂给旧 session；conv 不匹配+sn==0→替换旧 session；conv 不匹配+sn!=0→静默丢弃

## 构建 & 运行

```bash
# 后端
go build -o moleagent-serv ./cmd/moleagent-serv
./moleagent-serv -config configs/config.example.yaml

# 前端
cd admin && npm install && npm run build    # 产物在 admin/dist/

# 前端开发模式
cd admin && npm run dev
```

环境变量覆盖配置:

| 环境变量 | 用途 |
|---------|------|
| `MA_JWT_SECRET` | JWT 签名密钥（未设则自动生成，适合开发） |
| `MA_ADMIN_USER` / `MA_ADMIN_PASS` | 种子管理员凭据 |
| `MA_NODE_TOKEN` | 旧全局节点接入 Token |
| `MA_DB_PATH` | SQLite 数据库路径 |
| `MA_CONTROL_PORT` / `MA_GATEWAY_PORT` / `MA_API_PORT` | 端口覆盖 |
| `MA_TRANSPORT` | 传输协议（tcp, ws, kcp） |
| `MA_LOG_LEVEL` | 日志级别 |
| `MA_TLS_ENABLED` / `MA_TLS_CERT` / `MA_TLS_KEY` | TLS 配置 |

> **注意**：`ws_port` 和 `kcp_port` 仅支持 YAML 配置文件设置，暂无对应环境变量。

## 测试

```bash
# 后端全量测试
go test ./...

# 单个包测试
go test ./internal/tunnel/...

# 运行单个测试函数
go test ./internal/api/ -run TestTunnelHandler_Create -v

# 前端测试
cd admin && npm test
```

测试辅助：`auth.SetClaims(ctx, claims)` 用于 handler 测试注入认证上下文。

## 注意事项（AI 编码陷阱）

1. **隧道配置变更必须走 TunnelConfigService**：不要直接调用 nodeMgr.Update 或 nodeRepo.Update 修改隧道，必须通过 `TunnelConfigService.ApplyTunnel/RemoveTunnel/ReplaceTunnels` 统一处理持久化、路由刷新和客户端同步。直接修改会导致持久化与运行态不一致。

2. **在线节点先推后更新**：`ApplyTunnel` 对在线节点先推送配置到客户端（`pushToClient`），客户端确认后再更新服务端运行态索引。不要颠倒顺序——如果先更新服务端索引，路由会把流量切到新隧道，但客户端可能还未准备好。

3. **smux Session 不在 Node 结构体中**：Session 存储在 `ShardedNodeManager.sessions` map 中，与 `core.Node` 分离。获取 Session 必须调用 `nodeMgr.GetSession()`，不要尝试从 Node 结构体获取。

4. **Tunnel.Enabled 是 `*bool`**：使用 `tunnel.IsEnabled()` 判断，不要直接解引用 `Enabled` 字段（nil 表示启用）。新增隧道时，默认不设置 `Enabled` 字段（nil = 启用），而不是显式设为 `true`。

5. **节点 ID 格式**：固定 8 字符，首字符字母，其余字母或数字。由 `isValidNodeID()` 校验。修改校验规则时需同步更新客户端。

6. **TCP 代理头格式**：服务端通过 smux stream 转发 TCP/UDP 流量时，先发送 `\x00<tunnel-name>\n` 标识头，客户端据此路由到正确目标。新增隧道转发逻辑时必须保留此协议。

7. **资源归属过滤在 Handler 层**：不要在 Repo 层做归属过滤。Handler 通过 `resource_scope.go` 的 `IsAdmin/CanAccessNode/FilterNodes/checkNodeOwnership` 过滤，Repo 层保持纯粹的数据读写。

8. **ReleaseNodeResources 不修改持久化**：节点断开时只清理运行态资源（监听器、路由索引、统计条目），不修改 Redka 持久化配置。节点重连时通过 `applyRuntimeTunnels` 重新激活。

9. **RebuildIndex 触发时机**：每次节点变更（注册、断开、隧道更新）都会调用 `gateway.RebuildIndex`，这是全量重建。如果未来节点数很大，需考虑增量索引。

10. **validateTunnel 的类型分支**：服务端校验中，`ser2mq/vpn-manager/ser2tcp/ser2udp` 走 `default` 分支直接返回错误（未知类型）。这些客户端本地类型仅在客户端注册时通过 `SyncFromClient` 入口接受，REST API 的 `ApplyTunnel` 会拒绝它们。新增客户端本地类型时需同步更新 `service/tunnel_service.go` 的 `validateTunnel`。
