# moleAgent_Serv

代理隧道服务端，负责节点管理、隧道转发、认证鉴权和 MQTT Broker。

## 技术栈

- Go 1.25, 标准库为主
- smux (github.com/xtaci/smux) — TCP 多路复用
- redka + SQLite — 持久化（用户/角色/配置）
- mochi-mqtt — 内嵌 MQTT Broker
- bcrypt + JWT — 认证

## 项目结构

```
cmd/moleagent-serv/     入口
internal/
  config/               配置加载 (YAML + 环境变量)
  core/                 领域模型、接口、错误、事件总线
  storage/              redka 仓储层 (UserRepo, RoleRepo, NodeRepo)
  auth/                 JWT、bcrypt、RBAC、中间件
  api/                  HTTP API 路由与处理器
  tunnel/               隧道核心 (control, http, tcp, udp)
  node/                 分片节点管理器 + 健康检查
  mqtt/                 内嵌 MQTT Broker
  logging/              日志初始化
configs/                配置文件模板
admin/                  前端静态文件
```

## 架构要点

**节点连接**: 节点通过控制端口连接 → Challenge-Response 认证 → 建立 smux 会话 → 注册隧道配置。

**隧道转发**: 网关收到外部请求 → 路由匹配(域名/路径/端口) → 通过节点的 smux 会话 OpenStream 转发数据。

**节点管理**: ShardedNodeManager(256分片) + 健康检查(30s间隔, 90s超时)。

**认证流程**: JWT token (Bearer/Cookie) → AuthMiddleware → RBAC 权限检查。

## 数据流

```
外部请求 → Gateway 端口(:9980) → TunnelGateway.ServeHTTP
  → 路由匹配(泛域名/路径/精确域名) → node.YamuxSession.OpenStream()
  → 数据透传到节点代理

节点连接 → Control 端口(:9981) → Challenge-Response → smux.Server()
  → AcceptStream() → register/ping 控制命令
```

## 关键约定

- `UserStatus` / `NodeStatus` 是类型化常量，不使用原始字符串
- `core.UserRepo` / `core.RoleRepo` 是接口，storage 包提供实现
- smux Session 存储在 `core.Node.YamuxSession`
- API 路由注册在 `cmd/moleagent-serv/main.go` 的 `buildAPIRouter`
- 种子数据在 `storage/db.go` 的 `seedData()`

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
