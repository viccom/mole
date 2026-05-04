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

## 六、客户端自动择优（moleAgent_client）

### 6.1 配置格式

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

### 6.2 择优策略

1. 启动时并发 TCP connect 探测所有服务器延迟
2. 选择最低延迟的服务器建立连接
3. 断开后自动切换到次优服务器
4. 后台定期探测（60s），下次重连时使用更优服务器

## 七、实施阶段

### 阶段 1：PostgreSQL 支持 + 数据唯一性（后端核心）
- `storage/db.go` — PG 初始化分支
- `storage/*_repo.go` — 事务化改造（Create/Update）
- `core/domain.go` — 增加 Version 字段
- `config/config.go` — 新增 PG 配置
- **验证**：启动两个 API 实例连同一 PG，并发创建同 username，验证唯一一个成功

### 阶段 2：实时状态同步 + 全局 API
- `internal/state/` — 实例注册、节点状态发布（新建）
- `tunnel/control.go` — 节点连接/断开时同步状态
- `api/node_handler.go` — 列表接口合并在线状态
- `api/system_handler.go` — 新增 /servers 端点
- **验证**：两个实例，客户端连实例 A，实例 B 的 API 可查到节点在线

### 阶段 3：客户端自动择优（moleAgent_client）
- 多服务器配置支持
- 延迟探测 + 自动选择
- 故障转移
- **验证**：配置 3 个服务器地址，模拟故障验证自动切换

## 八、关键文件清单

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

### 修改文件（阶段 2）
- `internal/tunnel/control.go` — 节点状态同步钩子
- `internal/api/node_handler.go` — 全局节点视图
- `internal/api/system_handler.go` — /servers 端点
- `cmd/moleagent-serv/main.go` — 状态同步初始化

### moleAgent_client 修改（阶段 3）
- 配置文件解析 — 多服务器支持
- 连接管理 — 探测、选择、故障转移

## 九、验证计划

1. **单元测试**：事务化 repo 的 CRUD + 并发冲突测试
2. **并发测试**：两个 API 实例并发创建同 username/node_id
3. **状态同步测试**：节点连实例 A → 实例 B 的 API 查到在线
4. **故障转移测试**：停止实例 A → 节点状态标记 offline → 客户端重连实例 B
5. **兼容性测试**：不配 PG 时仍可用 SQLite 单机模式
