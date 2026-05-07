# moleAgent_Serv 使用指南

## 目录

- [快速开始](#快速开始)
- [服务架构](#服务架构)
- [配置文件](#配置文件)
- [认证与授权](#认证与授权)
- [节点接入认证](#节点接入认证)
- [节点归属与资源可见性](#节点归属与资源可见性)
- [REST API](#rest-api)
- [节点连接协议](#节点连接协议)
- [隧道启停控制](#隧道启停控制)
- [MQTT Broker](#mqtt-broker)
- [管理页面](#管理页面)
- [日志](#日志)
- [常见问题](#常见问题)

---

## 快速开始

### 编译

```bash
git clone <repo>
cd moleAgent_Serv
go build -o moleagent-serv ./cmd/moleagent-serv
```

### 启动

```bash
# 必须设置 JWT_SECRET（最小 16 字符）
export MA_JWT_SECRET="your-super-secret-key-at-least-16-chars"
export MA_ADMIN_USER="admin"
export MA_ADMIN_PASS="your-admin-password"

./moleagent-serv -nodetoken "your-node-token"
```

> **注意**：`-nodetoken` 是旧全局接入凭据，所有节点共用一个 Token。推荐使用**用户级接入 Token**（见[节点接入认证](#节点接入认证)），`-nodetoken` 仅作兼容过渡保留。

服务默认监听端口：

| 端口 | 用途 |
|------|------|
| `:9981` | 节点控制端口（Node 连接） |
| `:9980` | 网关端口（外部请求） |
| `:9983` | REST API + 管理页面 |

### Docker 部署

```bash
docker run -d \
  -e MA_JWT_SECRET="your-secret-key" \
  -e MA_ADMIN_USER="admin" \
  -e MA_ADMIN_PASS="admin" \
  -p 9981:9981 -p 9980:9980 -p 9983:9983 \
  moleagent-serv
```

---

## 服务架构

```
                                    ┌─────────────┐
  [浏览器/客户端]  ── HTTP/HTTPS ──▶  │  Gateway    │
                                    │  :9980      │
                                    └──────┬──────┘
                                           │ smux 流
                                    ┌──────▼──────┐
[节点客户端]  ── TCP/WS/KCP + smux ──▶  │  Control     │
                                    │  :9981       │
                                    └──────┬──────┘
                                           │
         ┌────────────────────────────────┼────────────────────────────────┐
         │          moleAgent_Serv         │                                │
         │  ┌─────────────┐   ┌───────────▼───┐   ┌──────────────┐        │
         │  │ REST API    │   │  MQTT Broker  │   │ Node Manager │        │
         │  │ :9983        │   │  :1883/:1882  │   │ (分片)       │        │
         │  └─────────────┘   └───────────────┘   └──────────────┘        │
         │         │                                                  │        │
         │         └──────────┬──────────────────────────────┘          │
         │                    │                                             │
         │              ┌─────▼─────┐                                     │
         │              │  Redka DB  │                                     │
         │              │ (SQLite)   │                                     │
         │              └────────────┘                                     │
         └─────────────────────────────────────────────────────────────────┘
```

### 三大核心端口

1. **Control Port (9981)** — 节点注册、心跳、隧道控制
2. **Gateway Port (9980)** — 接收外部请求，转发到对应节点
3. **API Port (9983)** — REST API + 管理页面

### 传输协议

Control Port 支持三种底层传输协议，可**同时启用**多种协议：

| 协议 | 主配置 | 额外监听 | 加密方式 | 适用场景 |
|------|--------|---------|---------|---------|
| TCP | `transport: "tcp"` | — | TLS（可选） | 默认选择，稳定可靠 |
| WebSocket | `transport: "ws"` | `ws_port: ":9988"` | TLS → `wss` | 穿透 HTTP 代理/防火墙 |
| KCP (UDP) | `transport: "kcp"` | `kcp_port: ":9981"` | AES-256（可选） | 高延迟/弱网，FEC 纠错 |

**单协议模式**：设置 `transport` 为 `tcp`/`ws`/`kcp`，仅启用一种协议。

**多协议同时监听**（推荐）：`transport` 设为 `tcp`（默认），再配置 `ws_port` 和 `kcp_port` 启用额外协议：

```yaml
server:
  control_port: ":9981"    # TCP（始终启用）
  ws_port: ":9988"         # WebSocket（空=禁用）
  kcp_port: ":9981"        # KCP/UDP（空=禁用，可与 TCP 共用端口号）
```

三种协议的节点共用同一套认证、smux 会话、注册流程，完全互通。

**TLS 加密**：仅适用于 `tcp` 和 `ws` 协议。`ws` + TLS 即 `wss`。

**KCP 加密**：通过 `server.kcp.key` 设置共享密钥，使用 SHA-256 派生 AES-256 密钥。

**KCP FEC 纠错**：配置 `data_shards` 和 `parity_shards` 启用前向纠错，适合丢包率高的网络。

> **注意**：KCP 不支持 TLS（UDP 协议），配置校验会拒绝 `transport: kcp` + `tls.enabled: true`。

### Nginx 反向代理 WebSocket

WS 端口可通过 nginx 反向代理，实现 TLS 终结和统一入口。WS transport 的升级路径固定为 `/ws`。

```nginx
server {
    listen 443 ssl;
    server_name tunnel.example.com;

    ssl_certificate     /etc/ssl/certs/tunnel.example.com.pem;
    ssl_certificate_key /etc/ssl/private/tunnel.example.com.key;

    location /ws {
        proxy_pass http://127.0.0.1:9988;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
    }
}
```

客户端连接地址改为 `wss://tunnel.example.com/ws`，由 nginx 终结 TLS 后转发到内部 WS 端口。

> **提示**：`proxy_read_timeout` 建议设为 3600s 或更大。smux 心跳间隔 30s，但隧道空闲时连接可能长时间无数据，nginx 默认 60s 超时会断开长连接。

---

## 配置文件

配置文件 `config.yaml` 示例：

```yaml
server:
  control_port: ":9981"    # 节点控制端口（TCP，始终启用）
  gateway_port: ":9980"    # 网关端口
  api_port: ":9983"        # API + 管理页面端口
  transport: "tcp"         # 主传输协议: tcp, ws, kcp
  ws_port: ""              # WebSocket 额外监听（空=禁用, 如 ":9988"）
  kcp_port: ""             # KCP/UDP 额外监听（空=禁用, 如 ":9981"）
  max_concurrent: 50000    # 最大并发连接数
  tls:
    enabled: false         # TLS 加密（仅 tcp/ws，KCP 使用独立加密）
    cert_file: "certs/server.crt"
    key_file: "certs/server.key"
  kcp:                     # KCP 协议调优（transport: kcp 时生效）
    key: ""                # 加密密钥（空=不加密）
    data_shards: 10        # FEC 数据分片（0=禁用，推荐 10）
    parity_shards: 3       # FEC 校验分片（推荐 3）
    nodelay: 1             # 低延迟模式（推荐 1）
    interval: 10           # ACK 间隔 ms（推荐 10）
    resend: 2              # 快速重传阈值（推荐 2）
    no_congestion: 1       # 禁用拥塞控制（隧道场景推荐 1）
    send_window: 0         # 发送窗口（0=默认）
    recv_window: 0         # 接收窗口（0=默认）

mqtt:
  enabled: true
  tcp_port: ":1883"        # MQTT TCP
  ws_port: ":1882"         # MQTT WebSocket

auth:
  jwt_secret: "your-secret-key-here"   # 必须设置
  jwt_expiry: "24h"        # Token 有效期
  bcrypt_cost: 12          # 密码哈希强度

database:
  path: "data/config.db"   # SQLite 数据库路径

logging:
  level: "info"           # debug/info/warn/error
  format: "text"           # text/json
  file:
    enabled: true
    path: "logs/moleagent.log"
    max_size_mb: 100
    max_backups: 10
    max_age_days: 30
    compress: true
  console:
    enabled: true
    color: true
```

### 环境变量覆盖

| 环境变量 | 对应配置 | 说明 |
|----------|---------|------|
| `MA_JWT_SECRET` | `auth.jwt_secret` | JWT 签名密钥 |
| `MA_ADMIN_USER` | — | 管理员用户名，默认 `admin` |
| `MA_ADMIN_PASS` | — | 管理员密码，默认 `admin` |
| `MA_DB_PATH` | `database.path` | 数据库路径 |
| `MA_CONTROL_PORT` | `server.control_port` | 控制端口 |
| `MA_GATEWAY_PORT` | `server.gateway_port` | 网关端口 |
| `MA_API_PORT` | `server.api_port` | API 端口 |
| `MA_TRANSPORT` | `server.transport` | 传输协议（tcp, ws, kcp） |
| `MA_TLS_ENABLED` | `server.tls.enabled` | 启用 TLS |
| `MA_TLS_CERT` | `server.tls.cert_file` | TLS 证书路径 |
| `MA_TLS_KEY` | `server.tls.key_file` | TLS 私钥路径 |
| `MA_LOG_LEVEL` | `logging.level` | 日志等级 |

---

## 认证与授权

### 用户系统

默认创建超级管理员账户（可通过 `MA_ADMIN_USER` / `MA_ADMIN_PASS` 自定义）。

内置两种角色：

| 角色 | 权限 | 说明 |
|------|------|------|
| `admin` | `*:*` | 完全控制 |
| `operator` | `*:read` | 仅读权限 |

### 授权模型（RBAC）

用户属于一个或多个角色，角色拥有一组权限。

**权限格式**：`resource:action`

| 资源 | 可用操作 |
|------|---------|
| `users` | `read`, `write`, `delete`, `admin` |
| `roles` | `read`, `write`, `delete` |
| `nodes` | `read`, `write`, `delete`, `admin` |
| `tunnels` | `read`, `write`, `delete` |
| `mqtt` | `read`, `write` |
| `system` | `read`, `admin` |
| `accesskey` | `read`, `admin` |
| `*` | `*`（通配） |

### 三种认证方式

#### 1. Bearer Token（最常用）

```bash
# 登录获取 Token
curl -X POST http://localhost:9983/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"admin"}'

# 后续请求携带 Token
curl http://localhost:9983/api/v1/users \
  -H 'Authorization: Bearer eyJhbGciOiJIUzI1NiIs...'
```

#### 2. AccessKey（服务间调用）

设置全局 AccessKey 后，服务间调用无需 JWT：

```bash
# 设置 AccessKey（需要 admin 权限）
curl -X PUT http://localhost:9983/api/v1/accesskey \
  -H 'Authorization: Bearer <token>' \
  -H 'Content-Type: application/json' \
  -d '{"key":"your-access-key"}'

# 使用 AccessKey（绕过 RBAC）
curl http://localhost:9983/api/v1/metrics \
  -H 'W-Access-Key: your-access-key'
```

#### 3. Cookie（浏览器管理页面）

登录管理页面后，Token 存储在 Cookie 中。

---

## 节点接入认证

节点接入服务端时需要通过认证，系统支持两种接入凭据：

### 用户级接入 Token（推荐）

每个用户可以创建多个接入 Token，客户端使用某个用户的 Token 接入后，节点自动归属该用户。

**接入流程**：

1. 用户通过 API 创建接入 Token（`POST /api/v1/me/access-tokens`）
2. 将返回的 `mat_xxxx...` 格式 Token 配置到客户端
3. 客户端连接服务端控制端口时发送该 Token
4. 服务端校验通过后，节点自动绑定 `OwnerUserID` 为该用户

**Token 特点**：

- 格式：`mat_` 前缀 + 32 字节随机 hex（共 68 字符）
- 服务端仅存 sha256 hash，不存明文
- 明文仅在创建/轮换时返回一次
- 支持 `active` / `disabled` 两种状态
- 支持轮换（rotate），旧 Token 立即失效

### 旧全局 nodetoken（兼容保留）

通过 `-nodetoken` 参数或 `MA_NODE_TOKEN` 环境变量设置的全局 Token。

- 所有节点共用同一凭据，无法区分接入者
- 兼容期内保留，使用旧 Token 接入的节点归属 `system`
- 不推荐新增使用，后续版本将弃用

### 认证优先级

服务端按以下顺序校验：

1. 先尝试用户级 Token（sha256 hash 查找）
2. 找到且 active → 认证成功，绑定用户归属
3. 未找到 → 尝试旧全局 Token（常量时间比较）
4. 匹配 → 认证成功，归属标记为 `system`（legacy）
5. 都不匹配 → 认证失败

> **实现细节**：ControlServer 统一委托给 `NodeAccessAuthenticator` 处理认证，不直接比对旧全局 Token，保证优先级语义一致。仅在未注入认证服务时回退到直接比对模式。

---

## 节点归属与资源可见性

### 归属模型

每个节点有 `OwnerUserID` 字段，标识节点归属用户：

- **用户级 Token 接入**：自动绑定为 Token 所属用户
- **旧全局 Token 接入**：归属 `system`
- **预配置节点**：通过 REST API 创建，管理员创建归 system，普通用户自动归自己

### 可见性规则

| 角色 | 节点/隧道可见范围 |
|------|------------------|
| `admin` | 全部节点和隧道 |
| 普通用户 | 仅 `OwnerUserID == 自己` 的节点及其隧道 |

**访问行为**：

- 非管理员 `List` 只返回自己的节点/隧道
- 非管理员 `Get/Update/Delete` 访问他人资源返回 `404`
- Handler 层做归属过滤，Repo 层不感知归属
- 非管理员创建/修改隧道时校验目标节点归属，只能操作自己的节点

### 级联处理

删除用户时：

1. 该用户所有 AccessToken 标记为 `disabled`
2. 归属节点的 `OwnerUserID` 改为 `system`（持久化层和运行态同步更新）
3. 已在线节点不强制断开（但重连时 Token 已失效）

---

## REST API

### 基础信息

- **Base URL**: `http://localhost:9983/api/v1`
- **认证**: Bearer Token / AccessKey / Cookie
- **响应格式**: 统一 JSON

```json
{
  "code": 0,
  "msg": "success",
  "data": { ... }
}
```

**业务错误码**：

| code | 说明 |
|------|------|
| `0` | 成功 |
| `400` | 参数错误 |
| `401` | 未授权 |
| `403` | 权限不足 |
| `404` | 资源不存在 |
| `409` | 冲突（如用户名已存在） |
| `422` | 验证失败 |
| `500` | 服务器内部错误 |

### 认证接口

| 方法 | 路径 | 说明 | 认证 |
|------|------|------|------|
| POST | `/auth/login` | 登录 | 否 |
| POST | `/auth/logout` | 登出 | 是 |
| POST | `/auth/refresh` | 刷新 Token | 是 |
| GET | `/auth/me` | 当前用户信息 | 是 |
| POST | `/auth/changepass` | 修改密码 | 是 |

**登录示例**：

```bash
curl -X POST http://localhost:9983/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"admin"}'
```

响应：

```json
{
  "code": 0,
  "msg": "success",
  "data": {
    "token": "eyJhbGciOiJIUzI1NiIs...",
    "expires_at": "2026-04-10T10:00:00Z",
    "user": { "id": "admin", "username": "admin" }
  }
}
```

### 用户管理

| 方法 | 路径 | 说明 | 权限 |
|------|------|------|------|
| GET | `/users` | 用户列表 | `users:read` |
| POST | `/users` | 创建用户 | `users:write` |
| GET | `/users/{id}` | 用户详情 | `users:read` |
| PUT | `/users/{id}` | 更新用户 | `users:write` |
| DELETE | `/users/{id}` | 删除用户 | `users:delete` |
| PUT | `/users/{id}/status` | 启用/禁用 | `users:admin` |
| PUT | `/users/{id}/password` | 重置密码 | `users:admin` |
| GET | `/users/{id}/roles` | 用户角色 | `users:read` |
| POST | `/users/{id}/roles/{roleId}` | 分配角色 | `users:admin` |
| DELETE | `/users/{id}/roles/{roleId}` | 移除角色 | `users:admin` |

**创建用户**：

```bash
curl -X POST http://localhost:9983/api/v1/users \
  -H 'Authorization: Bearer <token>' \
  -H 'Content-Type: application/json' \
  -d '{
    "username": "operator1",
    "password": "StrongPass123!",
    "role_ids": ["operator"]
  }'
```

### 角色管理

| 方法 | 路径 | 说明 | 权限 |
|------|------|------|------|
| GET | `/roles` | 角色列表 | `roles:read` |
| POST | `/roles` | 创建角色 | `roles:write` |
| GET | `/roles/{id}` | 角色详情 | `roles:read` |
| PUT | `/roles/{id}` | 更新角色 | `roles:write` |
| DELETE | `/roles/{id}` | 删除角色 | `roles:delete` |
| GET | `/permissions` | 可用权限列表 | `roles:read` |

**创建角色**：

```bash
curl -X POST http://localhost:9983/api/v1/roles \
  -H 'Authorization: Bearer <token>' \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "tunnel-manager",
    "description": "隧道管理员",
    "permissions": [
      {"resource": "nodes", "action": "read"},
      {"resource": "tunnels", "action": "read"},
      {"resource": "tunnels", "action": "write"}
    ]
  }'
```

### 个人 Access Token 管理

用户自助管理节点接入凭据，仅需要登录认证，无需额外 RBAC 权限。

| 方法 | 路径 | 说明 | 认证 |
|------|------|------|------|
| GET | `/me/access-tokens` | 列出我的 Token | 是 |
| POST | `/me/access-tokens` | 创建 Token | 是 |
| DELETE | `/me/access-tokens/{id}` | 删除 Token | 是 |
| POST | `/me/access-tokens/{id}/rotate` | 轮换 Token | 是 |

**创建 Token**：

```bash
curl -X POST http://localhost:9983/api/v1/me/access-tokens \
  -H 'Authorization: Bearer <token>' \
  -H 'Content-Type: application/json' \
  -d '{"name": "办公室网关"}'
```

响应：

```json
{
  "code": 0,
  "msg": "success",
  "data": {
    "id": "atk_1744617600000000000",
    "name": "办公室网关",
    "token": "mat_a1b2c3d4e5f6...（完整明文，仅此一次）",
    "token_prefix": "mat_a1b2",
    "created_at": "2026-04-14T12:00:00Z"
  }
}
```

> **重要**：完整 Token 仅在创建和轮换时返回一次，后续无法再查看。

**列出 Token**：

```bash
curl http://localhost:9983/api/v1/me/access-tokens \
  -H 'Authorization: Bearer <token>'
```

响应：

```json
{
  "code": 0,
  "msg": "success",
  "data": {
    "items": [
      {
        "id": "atk_1744617600000000000",
        "name": "办公室网关",
        "token_prefix": "mat_a1b2",
        "status": "active",
        "last_used_at": "2026-04-15T08:30:00Z",
        "created_at": "2026-04-14T12:00:00Z"
      }
    ],
    "total": 1
  }
}
```

**轮换 Token**：

```bash
curl -X POST http://localhost:9983/api/v1/me/access-tokens/atk_xxx/rotate \
  -H 'Authorization: Bearer <token>'
```

响应：

```json
{
  "code": 0,
  "msg": "success",
  "data": {
    "id": "atk_1744617600000000000",
    "name": "办公室网关",
    "token": "mat_new_token_value...（新的完整明文）",
    "token_prefix": "mat_new1"
  }
}
```

轮换后旧 Token 立即失效，使用新 Token 连接即可。已在线节点不会被断开。

**删除 Token**：

```bash
curl -X DELETE http://localhost:9983/api/v1/me/access-tokens/atk_xxx \
  -H 'Authorization: Bearer <token>'
```

删除后该 Token 不可用于新连接，已在线节点不会被断开。

### 节点管理

| 方法 | 路径 | 说明 | 权限 |
|------|------|------|------|
| GET | `/nodes` | 节点列表 | `nodes:read` |
| POST | `/nodes` | 预配置节点 | `nodes:write` |
| GET | `/nodes/{id}` | 节点详情 | `nodes:read` |
| PUT | `/nodes/{id}` | 更新节点 | `nodes:write` |
| DELETE | `/nodes/{id}` | 删除节点 | `nodes:delete` |
| GET | `/nodes/{id}/tunnels` | 节点隧道 | `nodes:read` |
| DELETE | `/nodes/{id}/connection` | 断开连接 | `nodes:admin` |

**预配置节点**（节点离线时也可创建配置）：

```bash
curl -X POST http://localhost:9983/api/v1/nodes \
  -H 'Authorization: Bearer <token>' \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "prod-server-01",
    "token": "node-auth-token",
    "tunnels": [
      {"name": "web", "type": "http", "target": "http://127.0.0.1:8080", "domain": "app.example.com"},
      {"name": "db", "type": "tcp", "target": "127.0.0.1:3306", "listen_port": 20001},
      {"name": "admin", "type": "http", "target": "http://127.0.0.1:9090", "domain": "admin.example.com", "enabled": false}
    ]
  }'
```

> **归属**：管理员创建的预配置节点自动归属 `system`；普通用户创建的节点自动归属当前用户。

> **可见性**：管理员可查看全部节点；普通用户只能看到 `OwnerUserID` 为自己的节点。

### 隧道管理

| 方法 | 路径 | 说明 | 权限 |
|------|------|------|------|
| GET | `/tunnels` | 活跃隧道列表 | `tunnels:read` |
| GET | `/tunnels/stats` | 隧道统计 | `tunnels:read` |
| POST | `/tunnels` | 创建动态隧道 | `tunnels:write` |
| DELETE | `/tunnels/{name}` | 删除隧道 | `tunnels:delete` |

**隧道统计**：

```bash
curl http://localhost:9983/api/v1/tunnels/stats \
  -H 'Authorization: Bearer <token>'
```

响应：

```json
{
  "code": 0,
  "msg": "success",
  "data": {
    "total_tunnels": 10,
    "enabled_tunnels": 8,
    "active_tunnels": 5
  }
}
```

- `total_tunnels`：所有隧道总数（含禁用）
- `enabled_tunnels`：`enabled=true` 或未设置的隧道数
- `active_tunnels`：在线节点上 `enabled=true` 的隧道数（正在服务中）

> **可见性**：管理员可查看全部隧道；普通用户只能看到自己节点上的隧道。

### MQTT 管理

| 方法 | 路径 | 说明 | 权限 |
|------|------|------|------|
| GET | `/mqtt/clients` | 已连接客户端 | `mqtt:read` |
| GET | `/mqtt/topics` | 活跃订阅 | `mqtt:read` |
| GET | `/mqtt/stats` | Broker 统计 | `mqtt:read` |
| POST | `/mqtt/publish` | 内联发布 | `mqtt:write` |
| GET | `/mqtt/health` | 健康检查 | `mqtt:read` |

**发布 MQTT 消息**：

```bash
curl -X POST http://localhost:9983/api/v1/mqtt/publish \
  -H 'Authorization: Bearer <token>' \
  -H 'Content-Type: application/json' \
  -d '{"topic":"devices/sensor1/data","payload":"{\"temp\":25.5}","qos":1}'
```

### 系统管理

| 方法 | 路径 | 说明 | 权限 |
|------|------|------|------|
| GET | `/health` | 健康检查 | **否** |
| GET | `/version` | 版本与系统信息 | **否** |
| GET | `/metrics` | 系统指标 | `system:read` |
| GET | `/config` | 运行时配置 | `system:admin` |
| GET | `/accesskey` | AccessKey 状态 | `accesskey:read` |
| PUT | `/accesskey` | 设置 AccessKey | `accesskey:admin` |
| DELETE | `/accesskey` | 禁用 AccessKey | `accesskey:admin` |

---

## 节点连接协议

### 连接流程

```
[Node 客户端]                     [moleAgent_Serv]
     │                                   │
     │──── TCP/WS/KCP 连接 ────────────▶│
     │                                   │
     │◀────── 32 字节 Challenge ─────────│
     │                                   │
     │── JSON {"token":"mat_xxx"} ──────▶│ 验证 Token（用户级优先）
     │                                   │
     │◀──── {"cmd":"ok"} ────────────────│ 认证成功 + 绑定归属用户
     │                                   │
     │◀════════ smux session ═══════════▶│ 复用同一 TCP
     │                                   │
     │── smux stream: ──────────────────▶│
     │   {"cmd":"register","node_id":"..."│
     │    "tunnels":[...]}               │
     │                                   │
     │◀── smux stream: {"cmd":"ok"} ─────│
     │                                   │
     │── smux stream: {"cmd":"ping"} ──▶│  心跳（每 10s）
     │◀── smux stream: {"cmd":"pong"} ◀─│
```

### 节点注册消息

```json
{
  "cmd": "register",
  "node_id": "prod-server-01",
  "name": "生产服务器 01",
  "token": "mat_a1b2c3d4...",
  "tunnels": [
    {
      "name": "web",
      "type": "http",
      "target": "http://127.0.0.1:8080",
      "domain": "app.example.com"
    },
    {
      "name": "db",
      "type": "tcp",
      "target": "127.0.0.1:3306",
      "listen_port": 20001
    },
    {
      "name": "dns",
      "type": "udp",
      "target": "127.0.0.1:53",
      "listen_port": 20002
    },
    {
      "name": "admin",
      "type": "http",
      "target": "http://127.0.0.1:9090",
      "domain": "admin.example.com",
      "enabled": false
    }
  ]
}
```

### 隧道类型说明

| 类型 | 用途 | 配置字段 |
|------|------|---------|
| `http` | HTTP 反向代理 | `domain`（按 Host 匹配） |
| `tcp` | TCP 端口映射 | `listen_port` |
| `udp` | UDP 端口映射 | `listen_port` |

---

## 隧道启停控制

每个隧道配置支持 `enabled` 字段，可控制隧道是否对外生效。

### 字段语义

| 值 | 行为 |
|----|------|
| 未设置（nil） | 启用（向后兼容旧数据） |
| `true` | 启用 |
| `false` | 禁用：配置保留，但不参与服务端路由和客户端转发 |

### 使用方式

在隧道配置中设置 `enabled` 字段：

```json
{
  "name": "admin-panel",
  "type": "http",
  "target": "http://127.0.0.1:9090",
  "domain": "admin.example.com",
  "enabled": false
}
```

禁用后：

- 配置仍保留在服务端持久化存储中
- 隧道出现在列表 API 中（带 `enabled: false` 标记）
- 服务端路由不匹配该隧道（HTTP 域名索引跳过、TCP/UDP 请求分发跳过）
- 客户端收到配置推送后，后续转发也忽略该隧道
- 重新启用只需将 `enabled` 设为 `true`

### 生效范围

`enabled=false` 的过滤在以下位置生效：

- **HTTP 网关**：域名索引构建、请求路由匹配均跳过
- **TCP 转发**：`findNodeForTunnel` 查找 + 运行时二次检查
- **UDP 转发**：同 TCP 模式
- **统计 API**：`active_tunnels` 仅统计 online + enabled 的隧道

---

## MQTT Broker

内置 mochi-mqtt 嵌入式 Broker，支持 TCP 和 WebSocket。

### MQTT 客户端连接

```bash
# 使用 mosquitto_clients
mosquitto_pub -h localhost -p 1883 -u username -P password \
  -t "devices/sensor1/data" -m '{"temp":25.5}'

# 订阅主题
mosquitto_sub -h localhost -p 1883 -u username -P password \
  -t "devices/+/data"
```

### MQTT 认证

用户名/密码通过 moleAgent 用户系统验证（`mqtt_users` 表）。

### WebSocket 连接

```javascript
const client = mqtt.connect('ws://localhost:1882', {
  clientId: 'web-client-01',
  username: 'your-user',
  password: 'your-pass'
})
```

---

## 管理页面

访问 `http://localhost:9983/admin/` 打开管理后台。

登录步骤：
1. 访问 `/admin`
2. 通过 POST `/api/v1/auth/login` 获取 Token
3. 浏览器存储 Token 后即可访问管理页面

---

## 日志

### 日志等级

| 等级 | 使用场景 |
|------|---------|
| `debug` | 开发调试 |
| `info` | 正常运行信息 |
| `warn` | 警告（可恢复异常） |
| `error` | 错误（需处理） |

### 输出格式

**文本格式（开发）**：
```
2026-04-09T10:30:45.123+0800  INFO  node/manager.go:85  Node registered  {"nodeId": "node-001", "name": "生产节点"}
```

**JSON 格式（生产）**：
```json
{"ts":"2026-04-09T10:30:45.123+0800","level":"INFO","msg":"Node registered","nodeId":"node-001"}
```

### 日志文件轮转

- 单文件最大 100MB
- 保留 10 个备份
- 保留 30 天
- 旧日志 gzip 压缩

---

## 常见问题

### Q: 启动失败 "auth.jwt_secret is required"

必须设置 `MA_JWT_SECRET` 环境变量或配置文件中的 `auth.jwt_secret`：

```bash
export MA_JWT_SECRET="your-secret-key-at-least-16-characters"
```

### Q: 节点连接被拒绝

检查节点使用的接入凭据：

```bash
# 方式一（推荐）：使用用户级接入 Token
# 1. 登录 API 获取 JWT Token
# 2. 创建接入 Token
curl -X POST http://localhost:9983/api/v1/me/access-tokens \
  -H 'Authorization: Bearer <jwt>' \
  -H 'Content-Type: application/json' \
  -d '{"name": "my-node"}'
# 3. 将返回的 mat_xxx... 配置到客户端

# 方式二（兼容）：使用旧全局 nodetoken
./moleagent-serv -nodetoken "your-node-token"
# 客户端连接时使用相同 Token
```

### Q: HTTP 隧道 502 Bad Gateway

- 确认节点在线：`GET /api/v1/nodes`
- 确认节点已注册隧道：`GET /api/v1/nodes/{id}/tunnels`
- 确认隧道未被禁用（`enabled` 不为 `false`）
- 检查节点后端服务是否正常运行

### Q: 如何让隧道暂时不对外生效

在隧道配置中设置 `"enabled": false`：

```bash
curl -X POST http://localhost:9983/api/v1/tunnels \
  -H 'Authorization: Bearer <token>' \
  -H 'Content-Type: application/json' \
  -d '{"name": "web", "type": "http", "target": "http://127.0.0.1:8080", "domain": "app.example.com", "enabled": false}'
```

禁用后配置保留，重新启用只需将 `enabled` 改为 `true`。

### Q: 如何轮换节点接入 Token

```bash
# 轮换 Token（需要登录后的 JWT）
curl -X POST http://localhost:9983/api/v1/me/access-tokens/atk_xxx/rotate \
  -H 'Authorization: Bearer <jwt>'
```

轮换后旧 Token 立即失效，新 Token 在响应中返回。已在线节点不会被断开，但重连时需使用新 Token。

### Q: 普通用户看不到节点

普通用户只能看到 `OwnerUserID` 等于自己的节点。确认：

1. 节点是使用该用户的 Access Token 接入的（而非旧全局 nodetoken）
2. 使用旧全局 Token 接入的节点归属 `system`，仅管理员可见

### Q: 删除用户后其节点怎么办

删除用户时系统自动处理：

- 该用户的接入 Token 全部标记为 `disabled`（不可用于新连接）
- 归属节点的 `OwnerUserID` 改为 `system`（持久化和运行态同步更新，管理员可重新分配）
- 已在线节点不会被断开

### Q: 如何查看实时日志

```bash
# 直接输出到终端
tail -f logs/moleagent.log

# 搜索错误
grep "ERROR" logs/moleagent.log

# JSON 格式查看
cat logs/moleagent.log | jq
```

### Q: 如何选择传输协议

| 场景 | 推荐 | 原因 |
|------|------|------|
| 内网/专线 | `tcp` | 延迟最低，最稳定 |
| 需穿透 HTTP 代理/防火墙 | `ws` | WebSocket 走 HTTP 升级，兼容性好 |
| 公网高延迟/弱网 | `kcp` | UDP 底层 + FEC 纠错 + 低延迟模式 |

客户端和服务端必须使用相同的 `transport` 配置。

### Q: KCP 加密如何配置

服务端和客户端设置相同的 `kcp.key`，系统使用 SHA-256 派生 AES-256 密钥加密所有 KCP 流量：

```yaml
# 服务端 config.yaml
server:
  transport: "kcp"
  kcp:
    key: "my-secret-key"
    data_shards: 10
    parity_shards: 3
```

```json
// 客户端 config.json
{
  "transport": "kcp",
  "kcp": {
    "key": "my-secret-key",
    "data_shards": 10,
    "parity_shards": 3
  }
}
```

### Q: 升级后旧节点看不到

从旧版本升级后，已有节点的 `OwnerUserID` 为空。系统启动时自动迁移：将 `OwnerUserID == ""` 的节点设为 `"system"`，管理员可在管理页面重新分配归属。
