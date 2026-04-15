# moleAgent\_Serv

代理隧道服务端，负责节点管理、隧道转发、认证鉴权和 MQTT Broker。

## 技术栈

- Go 1.25, 标准库为主
- smux (github.com/xtaci/smux) — TCP 多路复用
- redka + SQLite — 持久化（用户/角色/配置/接入 Token）
- mochi-mqtt — 内嵌 MQTT Broker
- bcrypt + JWT — 认证
- sha256 + crypto/rand — 接入 Token 哈希存储

## 项目结构

```
cmd/moleagent-serv/     入口
internal/
  config/               配置加载 (YAML + 环境变量)
  core/                 领域模型、接口、错误、事件总线
  storage/              redka 仓储层 (UserRepo, RoleRepo, NodeRepo, AccessTokenRepo)
  auth/                 JWT、bcrypt、RBAC、中间件
  api/                  HTTP API 路由与处理器
  tunnel/               隧道核心 (control, http, tcp, udp, registry)
  node/                 分片节点管理器 + 健康检查
  service/              业务服务（接入 Token 认证、隧道配置）
  mqtt/                 内嵌 MQTT Broker
  logging/              日志初始化
configs/                配置文件模板
admin/                  前端静态文件
```

## 架构要点

**节点连接**: 节点通过控制端口连接 → Challenge-Response 认证 → 建立 smux 会话 → 注册隧道配置。

**节点接入认证**: 支持两级 Token — 用户级 AccessToken（优先）和旧全局 nodetoken（兼容）。接入后自动绑定 `Node.OwnerUserID`。

**隧道转发**: 网关收到外部请求 → 路由匹配(域名/路径/端口) → 通过节点的 smux 会话 OpenStream 转发数据。`enabled=false` 的隧道不参与路由。

**节点管理**: ShardedNodeManager(256分片) + 健康检查(30s间隔, 90s超时)。

**认证流程**: JWT token (Bearer/Cookie) → AuthMiddleware → RBAC 权限检查 + 资源归属过滤。

**资源归属**: RBAC 管资源类型权限，`resource_scope.go` 管资源实例归属（`OwnerUserID`）。管理员全局可见，普通用户仅看自己的节点/隧道。

## 数据流

```
外部请求 → Gateway 端口(:9980) → TunnelGateway.ServeHTTP
  → 路由匹配(泛域名/路径/精确域名, 跳过 enabled=false) → nodeMgr.GetSession() → smux.OpenStream()
  → 数据透传到节点代理

节点连接 → Control 端口(:9981) → Challenge-Response
  → NodeAccessAuthenticator 统一认证（用户级 Token 优先 → 旧全局 Token 兜底）
  → smux.Server() → AcceptStream() → register/ping 控制命令
  → handleRegister 写入 OwnerUserID + AccessTokenID
```

## 关键约定

- `UserStatus` / `NodeStatus` / `AccessTokenStatus` 是类型化常量，不使用原始字符串
- `core.UserRepo` / `core.RoleRepo` / `core.AccessTokenRepo` 是接口，storage 包提供实现
- `core.NodeAccessAuthenticator` 是接入认证接口，service 包提供实现
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

### 隧道创建归属校验

- `TunnelHandler.Create()` 在执行隧道配置变更前校验 `node_id` 归属
- 非管理员只能给自己的节点添加/修改隧道
- 管理员不受限制

## 构建 & 运行

```bash
go build -o moleagent-serv ./cmd/moleagent-serv
./moleagent-serv -config configs/config.example.yaml
```

环境变量可覆盖配置: `MA_JWT_SECRET`, `MA_ADMIN_USER`, `MA_ADMIN_PASS`

## 测试

```bash
go test ./...
```

测试辅助：`auth.SetClaims(ctx, claims)` 用于 handler 测试注入认证上下文。
