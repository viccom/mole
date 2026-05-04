# 分片式多实例部署方案 — 最小改动 + 数据一致性

## Context

moleAgent_Serv 当前使用 redka + SQLite，单实例运行。用户期望实现多实例分片部署：
- 各实例数据面独立（流量路由只在本机节点）
- 控制面统一（共享认证、配置、全局状态可见）
- 改动最小且安全

**关键发现**：redka v1.0.1 **原生支持 PostgreSQL 后端 + 事务**。存储层只需改配置即可切换，无需重写任何 repo。

## 一、方案核心思路

```
redka SQLite (单机)  ──切换──▶  redka PostgreSQL (多实例共享)
                                    │
                              ┌─────┼─────┐
                              │     │     │
                           实例A  实例B  实例C
                           (各自本地内存管理在线节点)
```

- **持久化层**：redka 切换到 PostgreSQL 驱动 → 所有 repo 代码不变
- **唯一性保证**：利用 redka 事务 `db.Update()` + PG 的行级锁
- **实时状态**：复用 PG（redka Hash 存储），新增 `serv:instance:*` 和 `serv:node_status:*` hash key
- **无需新增基础设施**：不引入 Redis，PG 兼任持久化和状态共享

## 二、存储层改造（改动最小化）

### 2.1 redka 切换 PostgreSQL

**仅修改 `internal/storage/db.go` 的 Init 函数**：

```go
// 当前代码
opts := redka.Options{
    DriverName: "sqlite",
}
db, err = redka.Open(cfg.Path, &opts)

// 改为（根据配置自动选择）
if cfg.Type == "postgres" {
    db, err = redka.Open(cfg.PostgresDSN, &redka.Options{
        DriverName: "postgres",
    })
} else {
    db, err = redka.Open(cfg.Path, &redka.Options{
        DriverName: "sqlite",
    })
}
```

**所有 repo 代码（user_repo, role_repo, node_repo, access_token_repo）完全不变。**

### 2.2 配置扩展

```yaml
# config.yaml
database:
  type: "sqlite"              # "sqlite" 或 "postgres"
  path: "data/config.db"      # sqlite 模式
  # PostgreSQL 模式
  postgres_dsn: "postgres://user:pass@pg.example.com:5432/moleagent?sslmode=require"
```

```bash
# 环境变量
MA_DB_TYPE=postgres
MA_DB_POSTGRES_DSN=postgres://user:pass@pg.example.com:5432/moleagent
```

## 三、数据唯一性保证（核心问题）

### 3.1 当前问题分析

| 操作 | 当前保护方式 | 多实例并发风险 |
|------|-------------|---------------|
| 用户创建 | `GetByUsername` → 检查 → `Create`（非原子） | 两个实例同时检查"不存在"→ 都写入 |
| 用户更新 | Read → Modify → Write（无锁） | 并发更新丢失 |
| 节点创建 | 内存 Map 检查 + Repo 盲写 | 实例 A 不知道实例 B 已创建 |
| 角色创建 | 无检查，直接覆盖 | 静默覆盖丢失数据 |
| Token 创建 | 多步操作（主记录+索引），手动回滚 | 中间状态不一致 |
| Token 更新 | 多步操作（旧索引删除+新索引写入） | 索引丢失或重复 |

### 3.2 解决方案：redka 事务 + 唯一性检查原子化

**redka 提供 `db.Update(func(tx *Tx) error)` 事务**，整个函数在单个 SQL 事务中执行。

#### 用户创建（原子化 check-then-create）

```go
// 当前：非原子的 check-then-create
_, err := h.userRepo.GetByUsername(req.Username)  // 步骤1：查询
if err == nil { return 409 }
h.userRepo.Create(...)                            // 步骤2：创建（与步骤1之间可能被另一个实例插入）

// 改为：在事务内原子完成
func (r *userRepo) Create(user *User, passwordHash string) error {
    return r.db.Update(func(tx *redka.Tx) error {
        // 在同一事务内检查 + 创建
        _, err := tx.Hash().Get("usernames", user.Username)
        if err == nil {
            return core.ErrUserExists  // 事务内发现重复，回滚
        }
        // 写入主记录 + 密码 + 索引，全部在同一事务
        data, _ := json.Marshal(user)
        tx.Hash().Set("users", user.ID, string(data))
        tx.Hash().Set("passwords", user.ID, passwordHash)
        tx.Hash().Set("usernames", user.Username, user.ID)
        return nil  // 事务提交
    })
}
```

#### 节点创建（跨实例安全）

```go
func (r *nodeRepo) Create(node *Node) error {
    return r.db.Update(func(tx *redka.Tx) error {
        // 检查 PG 中是否已存在
        _, err := tx.Hash().Get("nodes", node.ID)
        if err == nil {
            return core.ErrNodeExists
        }
        data, _ := json.Marshal(node)
        _, err = tx.Hash().Set("nodes", node.ID, string(data))
        return err
    })
}
```

#### Token 创建/更新（多步操作事务化）

```go
func (r *accessTokenRepo) Create(token *AccessToken) error {
    return r.db.Update(func(tx *redka.Tx) error {
        // 检查 hash 索引是否已存在
        _, err := tx.Hash().Get("access_token_hash_index", token.TokenHash)
        if err == nil {
            return core.ErrConflict  // hash 重复
        }
        // 主记录 + 索引在同一事务
        data, _ := json.Marshal(token)
        tx.Hash().Set("access_tokens", token.ID, string(data))
        tx.Hash().Set("access_token_hash_index", token.TokenHash, token.ID)
        return nil  // 要么全部成功，要么全部回滚
    })
}
```

#### 用户/节点更新（乐观锁）

在 JSON 数据中添加 `version` 字段：

```go
func (r *userRepo) Update(user *User) error {
    return r.db.Update(func(tx *redka.Tx) error {
        val, err := tx.Hash().Get("users", user.ID)
        if err != nil {
            return core.ErrUserNotFound
        }
        var existing User
        json.Unmarshal([]byte(val.String()), &existing)

        // 乐观锁检查
        if user.Version > 0 && user.Version != existing.Version {
            return core.ErrConflict  // 版本不匹配，提示刷新重试
        }

        user.Version = existing.Version + 1
        user.UpdatedAt = time.Now().UTC()

        data, _ := json.Marshal(user)
        tx.Hash().Set("users", user.ID, string(data))

        // 更新 username 索引
        if existing.Username != user.Username {
            tx.Hash().Delete("usernames", existing.Username)
        }
        tx.Hash().Set("usernames", user.Username, user.ID)
        return nil
    })
}
```

### 3.3 改动范围汇总

| 文件 | 改动 |
|------|------|
| `internal/storage/db.go` | Init 函数增加 PG 模式分支 |
| `internal/storage/user_repo.go` | Create/Update 改用 `db.Update()` 事务 + 乐观锁 |
| `internal/storage/role_repo.go` | Create 改用事务，增加存在性检查 |
| `internal/storage/node_repo.go` | Create 改用事务，增加存在性检查 |
| `internal/storage/access_token_repo.go` | Create/Update 改用事务，消除手动回滚 |
| `internal/core/domain.go` | User/Node 结构体增加 Version 字段 |
| `internal/config/config.go` | 增加 DB type 和 PG DSN 配置 |

**repo 接口（core/ports.go）完全不变。**

## 四、实时状态同步（复用 PostgreSQL）

### 4.1 数据结构

在 redka（PG 后端）中新增 hash key 存储：

```
# 实例注册信息
serv:instances                        Hash
  ├── srv-a    → {"id":"srv-a","addr":"10.0.0.1","control_ports":{"tcp":":9981","ws":":9988"},
  │               "gateway_domain":"*.a.example.com","started_at":"...","node_count":5}
  ├── srv-b    → {"id":"srv-b",...}
  └── srv-c    → {"id":"srv-c",...}

# 节点在线状态
serv:node_status                      Hash
  ├── prodSrv01 → {"status":"online","instance_id":"srv-a","remote_addr":"10.0.0.5:43210",
  │               "transport":"tcp","tunnels":["web","db"],"connected_at":"..."}
  ├── prodSrv02 → {"status":"online","instance_id":"srv-b",...}
  └── devNode01 → {"status":"offline","instance_id":"","disconnected_at":"..."}

# 实例心跳时间戳
serv:heartbeat                        Hash
  ├── srv-a    → "2026-05-04T10:05:00Z"
  ├── srv-b    → "2026-05-04T10:05:01Z"
  └── srv-c    → "2026-05-04T10:04:58Z"
```

### 4.2 状态更新时机

| 事件 | 更新操作 |
|------|---------|
| 实例启动 | `HSET serv:instances {instanceID} {info}` |
| 实例心跳（每 30s） | `HSET serv:heartbeat {instanceID} {timestamp}` |
| 节点注册 | `HSET serv:node_status {nodeID} {online info}` |
| 节点心跳 | 更新 node_status 的 last_heartbeat |
| 节点断开 | `HSET serv:node_status {nodeID} {offline info}` |
| 实例关闭 | `HDEL serv:instances {instanceID}` + 清理该实例的 node_status |

### 4.3 API 调整

```go
// GET /api/v1/nodes — 全局视图
// 1. 从 PG (redka) 获取所有节点持久化配置 (hash: nodes)
// 2. 从 PG (redka) 获取所有节点在线状态 (hash: serv:node_status)
// 3. 合并：持久化配置 + 在线状态 + 所属实例

// GET /api/v1/servers — 新增：实例列表
// 从 PG (redka) 获取 serv:instances + serv:heartbeat
```

### 4.4 心跳过期清理

- 每个实例定期扫描 `serv:heartbeat`，超过 90s 未更新的实例标记为离线
- 离线实例的 node_status 全部标记为 offline
- 由存活实例之一负责清理（使用 PG 的原子操作避免重复清理）

## 五、实例标识与配置

### 5.1 每个实例的配置

```yaml
database:
  type: "postgres"
  postgres_dsn: "postgres://user:pass@pg.example.com:5432/moleagent"

server:
  instance_id: "srv-a"          # 实例唯一标识（不设置则自动生成）
  gateway_domain: "*.a.example.com"  # 本实例绑定的泛域名
  control_port: ":9981"
  ws_port: ":9988"
```

### 5.2 种子数据保护

多个实例启动时都会执行 `seedData()`，需要确保幂等性（当前实现已是幂等的：先检查是否存在再创建）。

## 六、跨实例操作通知（关键遗漏补充）

这是计划中**最大的遗漏**。以下操作在当前架构中仅在本机内存/Session 中执行，多实例时需要跨实例路由。

### 6.1 问题场景

| 操作 | 触发方式 | 问题 |
|------|---------|------|
| **隧道推送** | 管理员在实例 B 调用 `POST /api/v1/tunnels`，为目标节点创建/修改隧道 | `PushTunnelUpdate` 需要 smux Session，但 Session 在实例 A 上 → 推送失败 |
| **节点断开** | 管理员在实例 B 调用 `DELETE /api/v1/nodes/{id}` | `Disconnect` 操作找不到本地 Session → 无法断开 |
| **用户删除级联** | 管理员在实例 B 删除用户，需更新在线节点的 `OwnerUserID` | 实例 A 的 `ShardedNodeManager` 内存状态未更新 |
| **隧道配置同步** | `ApplyTunnel/RemoveTunnel` 需要刷新本机路由索引 + 推送到客户端 | 只有持有 Session 的实例才能推送 |
| **Access Token 使用** | 节点在实例 A 接入，需更新 `last_used_at` | 写 PG 即可，无跨实例问题 |

### 6.2 解决方案：PG NOTIFY/LISTEN 跨实例事件通知

PostgreSQL 内置 `NOTIFY/LISTEN` 机制，无需引入额外中间件即可实现跨实例事件通知。

```
实例 A (LISTEN)  ←──PG NOTIFY──→  实例 B (LISTEN)
        │                               │
   收到事件 → 执行本地操作        收到事件 → 执行本地操作
```

#### 事件通道设计

```sql
-- 实例启动时注册监听
LISTEN moleagent_events;
```

#### 事件消息格式

```json
// 隧道配置变更事件
{"type":"tunnel_change","instance_id":"srv-b","node_id":"prodSrv01","action":"apply","tunnel_name":"web"}

// 节点断开事件
{"type":"node_disconnect","instance_id":"srv-b","node_id":"prodSrv01"}

// 用户删除级联事件
{"type":"user_delete_cascade","instance_id":"srv-b","user_id":"alice"}

// 节点归属变更事件
{"type":"node_owner_change","instance_id":"srv-b","node_id":"prodSrv01","owner_user_id":"system"}
```

#### 跨实例操作流程（以隧道推送为例）

```
1. 管理员在实例 B 调用 POST /api/v1/tunnels (node_id=prodSrv01)
2. 实例 B：
   a. 持久化隧道配置到 PG（事务内）
   b. 查询 PG：serv:node_status:prodSrv01 → instance_id="srv-a"
   c. 发现节点不在本实例 → NOTIFY 'moleagent_events', '{"type":"tunnel_change","node_id":"prodSrv01"}'
   d. 如果节点在本实例 → 直接 pushToClient（无需通知）
3. 实例 A 收到 NOTIFY：
   a. 重新加载节点的持久化隧道配置
   b. 调用 pushToClient → 通过 smux Session 推送到客户端
   c. 刷新本机路由索引
```

### 6.3 各操作的跨实例处理

| 操作 | 发起实例动作 | 目标实例动作 |
|------|------------|------------|
| **ApplyTunnel** | 写 PG + NOTIFY | 收到通知 → 从 PG 重新加载配置 → pushToClient + RebuildIndex |
| **RemoveTunnel** | 写 PG + NOTIFY | 收到通知 → 从 PG 重新加载配置 → pushToClient + RebuildIndex |
| **Delete Node** | 写 PG + NOTIFY | 收到通知 → 断开本机 smux Session + 清理资源 |
| **User Delete** | 写 PG（级联 Token/NodeOwner）+ NOTIFY | 收到通知 → 更新本机 ShardedNodeManager 内存态 |
| **Node Disconnect** | 查 PG 定位实例 + NOTIFY | 收到通知 → 关闭 smux Session |

### 6.4 NOTIFY 的注意事项

- **消息不持久化**：PG NOTIFY 只发给当前连接的监听者，不排队。实例重启后错过的事件通过**定期全量同步**补偿
- **定期全量同步**（每 60s）：从 PG 重新加载所有节点配置，与本地内存态对比，确保一致性
- **网络分区恢复**：分区恢复后，PG LISTEN 自动重连，全量同步补齐期间遗漏的变更
- **消息大小限制**：PG NOTIFY payload 限制 8000 字节，足够传递事件元数据（不含完整隧道配置，接收方从 PG 读取）

### 6.5 实现方式

```go
// internal/state/notify.go

type EventBus struct {
    db      *redka.DB
    sqlDB   *sql.DB       // 原始 sql.DB，用于 LISTEN/NOTIFY
    handler func(event Event)
}

func (bus *EventBus) Start(ctx context.Context) error {
    // 1. LISTEN moleagent_events
    // 2. goroutine 循环接收通知 → 解析 → 调用 handler
}

func (bus *EventBus) Publish(event Event) error {
    // NOTIFY 'moleagent_events', '{"type":"tunnel_change",...}'
}
```

> **注意**：PG NOTIFY/LISTEN 需要使用原始 `*sql.DB` 或 `*sql.Conn`，redka 本身不暴露此功能。在 `storage/db.go` 中额外暴露原始 `*sql.DB` 即可。

## 七、其他遗漏补充

### 7.1 MQTT 跨实例

当前每个实例内嵌独立的 MQTT Broker。多实例部署时：

- **方案 A（推荐）：MQTT 独立部署**，各实例通过 MQTT 互不干扰
- **方案 B：MQTT 保持内嵌**，各实例的 MQTT 相互独立，客户端需连到对应实例的 MQTT 端口

由于 MQTT 消息本身有 topic 路由，跨实例 MQTT 共享会引入较大复杂度。建议 MQTT 保持独立，不作为多实例同步的一部分。

### 7.2 实例崩溃恢复

实例崩溃（无 graceful shutdown）时：
- `serv:instances` 中的注册信息未清理
- `serv:node_status` 中节点状态仍为 online
- **恢复机制**：其他实例的心跳清理 goroutine 检测到心跳超时（90s）后：
  1. 标记该实例离线
  2. 该实例下所有 node_status 改为 offline
  3. 释放相关隧道运行时资源（TCP/UDP 监听器无法远程释放，需等实例恢复后自行清理）

### 7.3 TCP/UDP 监听器跨实例冲突

当两个实例上的节点配置了相同的 `listen_port`（如都配置了 `:20001`）：
- **当前无跨实例校验**：每个实例独立绑定端口，不同服务器上不冲突
- **API 层需提示**：创建隧道时，应告知管理员该端口属于哪个实例，避免混淆
- 跨实例 listen_port 唯一性无法在 PG 层保证（不同服务器可以绑定相同端口），这属于管理规范问题

### 7.4 PG 驱动依赖

需新增 PostgreSQL 驱动 import：
```go
import _ "github.com/lib/pq"  // 或 github.com/jackc/pgx/v5/stdlib
```

### 7.5 PG 连接池配置

多实例共享 PG 时需合理配置连接池：
```yaml
database:
  postgres_dsn: "postgres://..."
  max_open_conns: 25       # 每个实例的最大连接数
  max_idle_conns: 10
  conn_max_lifetime: "5m"
```

需在 `db.go` 中通过 `sql.DB.SetMaxOpenConns()` 等方法设置。

### 7.6 日志增强

多实例部署时日志应包含 `instance_id`：
```go
slog.Info("Node registered", "instance", instanceID, "nodeId", nodeID, ...)
```

### 7.7 API 响应增强

节点列表 API 应返回节点所属实例信息：
```json
{
    "id": "prodSrv01",
    "status": "online",
    "instance_id": "srv-a",
    "instance_addr": "10.0.0.1:9981",
    ...
}
```

## 八、客户端自动择优（moleAgent_client）

### 8.1 配置格式

```json
{
    "servers": [
        {"addr": "a.example.com:9981", "transport": "tcp"},
        {"addr": "b.example.com:9988", "transport": "ws", "path": "/ws"},
        {"addr": "c.example.com:9981", "transport": "kcp"}
    ],
    "server_select": "auto"
}
```

### 8.2 择优策略

1. 启动时并发 TCP connect 探测所有服务器延迟
2. 选择最低延迟的服务器建立连接
3. 断开后自动切换到次优服务器
4. 后台定期探测（60s），下次重连时使用更优服务器

## 九、实施阶段

### 阶段 1：PostgreSQL 支持 + 数据唯一性（后端核心）
- `storage/db.go` — PG 初始化分支
- `storage/*_repo.go` — 事务化改造（Create/Update）
- `core/domain.go` — 增加 Version 字段
- `config/config.go` — 新增 PG 配置
- **验证**：启动两个 API 实例连同一 PG，并发创建同 username，验证唯一一个成功

### 阶段 2：实时状态同步 + 跨实例通知 + 全局 API
- `internal/state/instance.go` — 实例注册与心跳
- `internal/state/node_status.go` — 节点状态同步
- `internal/state/notify.go` — PG NOTIFY/LISTEN 事件总线（**新增**）
- `tunnel/control.go` — 节点连接/断开时同步状态
- `service/tunnel_service.go` — 跨实例隧道推送通知（**新增**）
- `api/node_handler.go` — 列表接口合并在线状态、跨实例 Delete/Disconnect
- `api/user_handler.go` — 用户删除级联发布跨实例通知
- `api/system_handler.go` — 新增 /servers 端点
- `storage/db.go` — 暴露原始 *sql.DB 供 NOTIFY/LISTEN 使用
- **验证**：两个实例，客户端连实例 A，实例 B 的 API 创建隧道 → 实例 A 收到通知并推送到客户端

### 阶段 3：客户端自动择优（moleAgent_client）
- 多服务器配置支持
- 延迟探测 + 自动选择
- 故障转移
- **验证**：配置 3 个服务器地址，模拟故障验证自动切换

## 十、关键文件清单

### 修改文件（阶段 1）
- `internal/storage/db.go` — PG 初始化
- `internal/storage/user_repo.go` — 事务化
- `internal/storage/role_repo.go` — 事务化
- `internal/storage/node_repo.go` — 事务化
- `internal/storage/access_token_repo.go` — 事务化
- `internal/core/domain.go` — Version 字段
- `internal/config/config.go` — PG 配置
- `configs/config.example.yaml` — 配置示例

### 新增文件（阶段 2）
- `internal/state/instance.go` — 实例注册与心跳
- `internal/state/node_status.go` — 节点状态同步
- `internal/state/notify.go` — PG NOTIFY/LISTEN 事件总线

### 修改文件（阶段 2）
- `internal/tunnel/control.go` — 节点状态同步钩子
- `internal/service/tunnel_service.go` — 跨实例隧道推送（NOTIFY 替代直接 push）
- `internal/api/node_handler.go` — 全局节点视图、跨实例 Delete/Disconnect
- `internal/api/user_handler.go` — 用户删除级联发布跨实例通知
- `internal/api/system_handler.go` — /servers 端点
- `internal/storage/db.go` — 暴露原始 *sql.DB 供 NOTIFY/LISTEN
- `cmd/moleagent-serv/main.go` — EventBus 初始化与事件注册

### moleAgent_client 修改（阶段 3）
- 配置文件解析 — 多服务器支持
- 连接管理 — 探测、选择、故障转移

## 十一、验证计划

1. **单元测试**：事务化 repo 的 CRUD + 并发冲突测试
2. **并发测试**：两个 API 实例并发创建同 username/node_id
3. **状态同步测试**：节点连实例 A → 实例 B 的 API 查到在线
4. **跨实例推送测试**（**新增**）：实例 B API 创建隧道 → NOTIFY → 实例 A 收到并推送到客户端
5. **跨实例断开测试**（**新增**）：实例 B API 删除节点 → NOTIFY → 实例 A 断开 smux Session
6. **用户级联测试**（**新增**）：实例 B 删除用户 → NOTIFY → 实例 A 更新内存态节点归属
7. **故障转移测试**：停止实例 A → 节点状态标记 offline → 客户端重连实例 B
8. **兼容性测试**：不配 PG 时仍可用 SQLite 单机模式
