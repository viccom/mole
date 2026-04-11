# moleAgent_Serv 使用指南

## 目录

- [快速开始](#快速开始)
- [服务架构](#服务架构)
- [配置文件](#配置文件)
- [认证与授权](#认证与授权)
- [REST API](#rest-api)
- [节点连接协议](#节点连接协议)
- [MQTT Broker](#mqtt-broker)
- [管理页面](#管理页面)

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
[节点客户端]  ── TCP + smux ──▶  │  Control     │
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

---

## 配置文件

配置文件 `config.yaml` 示例：

```yaml
server:
  control_port: ":9981"    # 节点控制端口
  gateway_port: ":9980"    # 网关端口
  api_port: ":9983"        # API + 管理页面端口
  max_concurrent: 50000    # 最大并发连接数

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
      {"name": "db", "type": "tcp", "target": "127.0.0.1:3306", "listen_port": 20001}
    ]
  }'
```

### 隧道管理

| 方法 | 路径 | 说明 | 权限 |
|------|------|------|------|
| GET | `/tunnels` | 活跃隧道列表 | `tunnels:read` |
| GET | `/tunnels/stats` | 隧道统计 | `tunnels:read` |
| POST | `/tunnels` | 创建动态隧道 | `tunnels:write` |
| DELETE | `/tunnels/{name}` | 删除隧道 | `tunnels:delete` |

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
     │──────── TCP 连接 ─────────────────▶│
     │                                   │
     │◀────── 32 字节 Challenge ─────────│
     │                                   │
     │── JSON {"token":"xxx"} ──────────▶│ 验证 Token
     │                                   │
     │◀──── {"cmd":"ok"} ────────────────│ 认证成功
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
  "token": "节点认证令牌",
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

检查节点 Token 是否与服务端配置一致：

```bash
# 服务端启动时指定
./moleagent-serv -nodetoken "your-node-token"

# 节点客户端连接时使用相同 Token
```

### Q: HTTP 隧道 502 Bad Gateway

- 确认节点在线：`GET /api/v1/nodes`
- 确认节点已注册隧道：`GET /api/v1/nodes/{id}/tunnels`
- 检查节点后端服务是否正常运行

### Q: 如何查看实时日志

```bash
# 直接输出到终端
tail -f logs/moleagent.log

# 搜索错误
grep "ERROR" logs/moleagent.log

# JSON 格式查看
cat logs/moleagent.log | jq
```
