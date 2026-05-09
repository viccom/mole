# 用户级接入 Token 改进方案

## 1. 背景与目标

当前 `moleAgent_Serv` 已具备多用户、角色、RBAC 的基础能力，但节点接入仍使用进程级唯一 `-nodetoken` / `MA_NODE_TOKEN`：

- 所有客户端节点共用一个接入凭据，无法区分"是谁接入的"。
- 节点与用户之间没有稳定归属关系，RBAC 只能判断"有没有 `nodes/tunnels` 权限"，不能判断"能不能看这个节点"。
- 现状下只有管理员适合查看和管理节点，普通用户即使具备读写权限，也缺少资源归属边界。

本次改进目标：

- 为每个用户提供多个"节点接入 Token"。
- 客户端使用某个用户的接入 Token 成功接入后，节点自动归属到该用户。
- 默认只有节点归属用户和管理员可查看、管理该节点及其隧道。
- 在节点下的隧道配置中增加 `enabled` 字段，用于启用/禁用隧道，服务端负责持久化、同步，并在运行时忽略已禁用隧道。
- 改动尽量轻量级、小步演进、松耦合，不做大规模架构重写。

## 2. 设计原则

### 2.1 最小改动

- 不重写现有 `JWT`、`RBAC`、`NodeManager`、`TunnelConfigService` 主干。
- 不改变网关转发、MQTT、隧道推送等核心数据面逻辑。
- 只在"控制端接入认证"和"管理 API 资源过滤"两个关键入口收口。

### 2.2 松耦合

- 新增"接入 Token 认证服务"接口，由 `ControlServer` 依赖接口而不是直接依赖存储实现。
- 新增"节点可见性/归属判断"辅助层，避免把归属判断散落到所有 Repo。
- 保持 `RBAC` 继续负责"是否有资源权限"，新增归属校验仅负责"是否是该资源所有者"。

### 2.3 兼容迁移

- 第一阶段保留旧全局 token 兼容通道，避免现有客户端一次性全部失效。
- 新用户优先走"用户级接入 Token"，旧 token 作为过渡能力，最终可关闭。

## 3. 现状问题定位

### 3.1 接入认证问题

当前控制端口在 [main.go](file:///root/gitme/refactoring/moleAgent/moleAgent_Serv/cmd/moleagent-serv/main.go#L28-L30) 通过命令行读取唯一 `nodetoken`，并在 [control.go](file:///root/gitme/refactoring/moleAgent/moleAgent_Serv/internal/tunnel/control.go#L197-L203) 用常量时间比较完成认证。

这会带来 3 个问题：

- 认证成功后无法识别接入者属于哪个用户。
- token 泄露影响全体节点，而不是单个用户范围。
- 后续无法做"用户自助创建/吊销接入 token"。

### 3.2 资源归属问题

当前 [Node](file:///root/gitme/refactoring/moleAgent/moleAgent_Serv/internal/core/domain.go#L20-L29) 没有 `OwnerUserID` 一类字段，因此：

- `NodeHandler.List/Get/Update/Delete` 只能按全量节点处理。
- [TunnelHandler.List/Delete](file:///root/gitme/refactoring/moleAgent/moleAgent_Serv/internal/api/tunnel_handler.go#L24-L58) 也是扫描所有节点。
- `RBAC` 只判断资源类型，不判断资源属于谁。

### 3.3 API 能力缺口

当前系统只有全局 `accesskey` 管理 API，没有"用户自己的节点接入 token 管理 API"。这与"多用户独立管理自己节点"的目标不匹配。

## 4. 轻量级目标方案

本方案不引入复杂的资源 ACL、组织树、策略引擎，只补三个核心概念：

1. `AccessToken`：归属于某个用户的节点接入凭据。
2. `Node.OwnerUserID`：节点归属用户。
3. `Tunnel.Enabled`：隧道配置级启用开关。

只要这三个点建立起来，现有 `RBAC + Handler + Service` 架构就能以较小改动实现用户隔离和隧道开关控制。

## 5. 数据模型改动

### 5.1 Node 增加归属字段

在 `core.Node` 中新增：

```go
OwnerUserID   string `json:"owner_user_id,omitempty"`
AccessTokenID string `json:"access_token_id,omitempty"`
```

说明：

- `OwnerUserID` 是最关键字段，用于节点和隧道的资源归属判断。
- `AccessTokenID` 用于审计和后续轮换排查，不参与主业务判断。
- 保留现有 `Token` 字段兼容旧客户端，不建议继续作为长期管理字段使用。

### 5.2 新增 AccessToken 模型

建议新增 `core.AccessToken`：

```go
type AccessToken struct {
	ID          string    `json:"id"`
	UserID      string    `json:"user_id"`
	Name        string    `json:"name"`
	TokenPrefix string    `json:"token_prefix"`
	TokenHash   string    `json:"-"`
	Status      string    `json:"status"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}
```

建议：

- 服务端只存 `TokenHash`，不存明文。
- 创建时返回一次完整明文 token，之后只展示 `TokenPrefix`。
- `Status` 先支持 `active` / `disabled` 即可，不做复杂状态机。

### 5.3 存储结构

为保持和现有 `redka` 用法一致，新增两个 hash：

- `access_tokens`：`tokenID -> AccessToken(JSON)`
- `access_token_hash_index`：`sha256(rawToken) -> tokenID`

这样可以在控制端接入时完成 O(1) 级别查找，避免扫描所有 token。

### 5.4 Tunnel 增加启用字段

建议在 `core.Tunnel` 中新增：

```go
Enabled bool `json:"enabled"`
```

建议语义：

- 默认值视为 `true`，保证兼容旧数据和旧客户端。
- `enabled=false` 表示该隧道配置被保留，但当前不对外生效。
- 服务端主要负责持久化和同步到客户端，不引入复杂的"禁用原因""定时启停""灰度状态"等扩展字段。

轻量实现建议：

- 持久化层保存原始隧道配置，包含 `enabled`。
- 控制面同步给客户端时原样携带 `enabled`。
- 服务端运行时在构建生效路由、统计 active tunnel、查找可删除目标时忽略 `enabled=false` 的隧道。

这样可以把"禁用"实现为一个纯配置能力，而不是引入新的运行态子系统。

## 6. 核心流程改造

### 6.1 节点接入认证

当前 `ControlServer` 直接比较全局 token。改造后建议：

1. 抽象接口 `NodeAccessAuthenticator`。
2. `ControlServer` 只依赖该接口。
3. 默认实现 `AccessTokenAuthService` 负责：
   - 校验原始 token。
   - 查到 `AccessToken`。
   - 取到 `userID`。
   - 记录 `last_used_at`。
   - 返回认证结果。

建议接口：

```go
type NodeAccessAuthenticator interface {
    AuthenticateNodeToken(ctx context.Context, rawToken string) (*NodeAccessGrant, error)
}
```

```go
type NodeAccessGrant struct {
    UserID        string
    AccessTokenID string
    LegacyGlobal  bool
}
```

这样 `ControlServer` 不需要知道 token 存在哪里，也不需要依赖 `UserRepo` / `AccessTokenRepo` 细节。

### 6.2 注册成功后的归属绑定

在 `handleRegister` 里，节点注册成功后写入：

- `node.OwnerUserID = grant.UserID`
- `node.AccessTokenID = grant.AccessTokenID`

这样后续：

- 持久化层已有归属数据。
- `NodeHandler` / `TunnelHandler` 只需按 `OwnerUserID` 过滤即可。

### 6.2.1 隧道配置变化的通知链路

对于隧道配置的任意变化，都必须沿用现有统一链路：

1. 服务端管理入口修改隧道配置。
2. `TunnelConfigService` 先持久化。
3. 在线节点通过 `PushTunnelUpdate` 下发完整隧道列表。
4. 客户端收到 `tunnel_push` 后替换本地配置。
5. 服务端仅在客户端确认接收成功后，再更新运行态并刷新索引。

这条链路对 `Tunnel.Enabled` 同样适用，不应另起一套"仅字段补丁"机制。

原因：

- 当前系统已经具备"完整隧道列表推送"的能力，复用它改动最小。
- `enabled` 与 `target/domain/listen_port` 一样，本质上也是隧道配置的一部分。
- 统一走全量替换，能减少服务端与客户端状态不一致风险。

### 6.2.2 客户端对配置变化的动作要求

客户端对服务端推送的隧道配置变化，当前已经具备"接收并替换本地隧道列表"的基础能力，但本次需要把行为要求明确化：

- 任何隧道配置变化，包括 `Tunnel.Enabled` 的变化，都通过 `tunnel_push` 感知。
- 客户端收到 `tunnel_push` 后，必须用新配置原子替换本地 `tunnels` 快照。
- 后续新进入的流量，按最新配置决策转发目标。
- 对 `enabled=false` 的隧道，客户端在本地匹配和转发时应忽略。

这里的"客户端进行相应动作"，轻量实现下不要求增加额外后台 watcher 或复杂启停编排，重点是：

- 检测到配置变化。
- 更新内存配置。
- 后续按新配置工作。

这与当前客户端"按收到的流和当前隧道快照实时做转发决策"的架构是一致的。

### 6.3 全局 token 兼容策略

为了轻量迁移，建议第一版保留兼容：

- 若用户级 token 校验成功，则走新逻辑。
- 若校验失败，再判断是否命中旧全局 token。
- 命中旧全局 token 时，将节点标记为：
  - `OwnerUserID = "admin"` 或空值加"legacy"标记。

更推荐：

- 兼容期内把旧全局 token 归属到管理员。
- 文档中明确它只用于迁移，不再推荐新增使用。

这样老客户端无需立即修改，但新能力已可上线。

## 7. API 设计方案

为减少 RBAC 和路径设计复杂度，建议新增"当前用户自助 API"，而不是直接做"管理员代管所有用户 token"全套接口。

### 7.1 推荐路径

使用：

- `GET /api/v1/me/access-tokens`
- `POST /api/v1/me/access-tokens`
- `DELETE /api/v1/me/access-tokens/{id}`
- `POST /api/v1/me/access-tokens/{id}/rotate`

这是最轻量的选择，原因：

- 直接从 JWT claims 获取当前 `userID`。
- 不需要新增复杂的"用户 A 是否可管理用户 B token"的授权判断。
- 与当前 `/api/v1/auth/me` 风格一致。

### 7.2 返回格式

继续沿用现有 `ApiResponse`：

```json
{
  "code": 0,
  "msg": "success",
  "data": {
    "items": []
  }
}
```

### 7.3 创建 token

`POST /api/v1/me/access-tokens`

请求：

```json
{
  "name": "办公室网关"
}
```

响应建议：

```json
{
  "code": 0,
  "msg": "success",
  "data": {
    "id": "atk_xxx",
    "name": "办公室网关",
    "token": "mat_xxxxxxxxx",
    "token_prefix": "mat_xxxx",
    "created_at": "2026-04-14T12:00:00Z"
  }
}
```

注意：

- 完整 token 只在创建/轮换成功时返回一次。
- 列表接口不返回完整 token。

### 7.4 列表 token

`GET /api/v1/me/access-tokens`

返回字段建议：

- `id`
- `name`
- `token_prefix`
- `status`
- `last_used_at`
- `created_at`

### 7.5 删除 token

`DELETE /api/v1/me/access-tokens/{id}`

行为建议：

- 只允许删除自己的 token。
- 若有在线节点正使用该 token，不强制踢下线。
- 删除后该 token 不允许新连接。

这样逻辑简单，且不会引入额外在线会话管理复杂度。

### 7.6 轮换 token

`POST /api/v1/me/access-tokens/{id}/rotate`

轮换策略建议：

- 生成新 token。
- 更新 hash 和 prefix。
- 返回新明文 token。
- 不强制断开旧连接；新连接开始使用新 token。

这是运维上最温和的行为，也最适合轻量实施。

### 7.7 隧道 API 的兼容扩展

隧道相关请求体建议增加：

```json
{
  "name": "web-a",
  "type": "http",
  "target": "127.0.0.1:8080",
  "domain": "a.example.com",
  "enabled": true
}
```

兼容策略：

- 旧客户端或旧前端未传 `enabled` 时，服务端按启用处理。
- 更新节点隧道列表时，`enabled` 跟随完整配置一起持久化和同步。
- 第一版不必单独新增 `/enable`、`/disable` 子接口，继续复用现有创建、更新隧道配置入口即可。
- 不引入字段级增量同步协议，继续复用完整 `tunnel_push` 配置推送。

## 8. 节点与隧道的可见性控制

### 8.1 设计思路

不重写 `RBACEngine`。保留现有：

- `RBAC` 负责"有没有 `nodes/tunnels` 的 read/write/delete 权限"。
- Handler 负责"这个资源是不是你的"。

建议新增一个小型辅助组件，例如：

```go
type ResourceScope struct {}
```

提供：

- `IsAdmin(claims *core.Claims) bool`
- `CanAccessNode(claims *core.Claims, node *core.Node) bool`
- `FilterNodes(claims *core.Claims, nodes []*core.Node) []*core.Node`

### 8.2 访问规则

- `admin`：可查看和管理全部节点、隧道、token。
- 普通用户：
  - 仅可查看 `OwnerUserID == claims.UserID` 的节点。
  - 仅可修改自己的节点。
  - 仅可查看和删除自己节点上的隧道。

隧道启用状态规则：

- `enabled=true`：隧道参与服务端生效路由与客户端下发后的实际工作。
- `enabled=false`：隧道配置仍保留、仍可被所有者查看和编辑，但不参与服务端对外生效。

### 8.3 最小改造位置

建议只改以下 Handler：

- `NodeHandler`
- `TunnelHandler`

其中：

- `NodeHandler.List` 先过滤后返回。
- `NodeHandler.Get/Update/Delete` 若不是自己的节点，返回 `404`。
- `TunnelHandler.List/Stats/Delete/Create` 仅在当前用户可见节点范围内操作。
- `TunnelGateway`、隧道路由匹配和统计逻辑在读取节点隧道时过滤 `enabled=false`。

这样无需改 Repo，无需改 `NodeManager`，侵入面最小。

## 9. 对现有权限模型的影响

### 9.1 RBAC 基本保持不变

现有路由注册和资源权限可以继续保留，例如 [main.go](file:///root/gitme/refactoring/moleAgent/moleAgent_Serv/cmd/moleagent-serv/main.go#L253-L278) 的 `nodes/tunnels` 权限注册无需推倒重来。

### 9.2 仅补一个新资源

若希望 token API 也纳入资源分类，建议新增资源名：

- `node_tokens`

但为了尽量轻量，我更建议：

- `/api/v1/me/access-tokens` 使用 `RegisterAuth` 即可。
- 因为这是"当前用户自助接口"，不需要复杂 RBAC。

如果未来要做管理员代管其他用户 token，再补 `node_tokens` 资源也不迟。

## 10. 开发实施计划

建议采用 5 个小阶段，每个阶段都可单独回归。

### 阶段 1：数据模型与存储

目标：

- 新增 `core.AccessToken`
- 新增 `AccessTokenRepo`
- `Node` 增加 `OwnerUserID`、`AccessTokenID`
- `Tunnel` 增加 `Enabled`
- redka 增加 `access_tokens` 和 `access_token_hash_index`

改动点：

- `internal/core/domain.go`
- `internal/core/ports.go`
- `internal/storage/`

输出：

- 仓储 CRUD 可用
- token 明文只在创建时返回，库内只存 hash
- 旧隧道配置在未显式设置时默认按启用处理

### 阶段 2：控制端接入认证改造

目标：

- `ControlServer` 从"比对单一字符串"改为"依赖认证服务"
- 节点注册后自动绑定 `OwnerUserID`
- 保留旧全局 token 兼容

改动点：

- `internal/tunnel/control.go`
- `cmd/moleagent-serv/main.go`
- 新增 `internal/service/node_access_auth.go`

输出：

- 新 token 可接入
- 旧 token 仍可接入
- 节点持久化后带归属用户

### 阶段 3：隧道配置同步链路补强

目标：

- 明确 `Tunnel.Enabled` 与其他隧道字段一样，走统一的持久化和客户端推送链路
- 客户端收到 `tunnel_push` 后能按新配置立即生效

改动点：

- `internal/service/tunnel_service.go`
- `internal/tunnel/control.go`
- `moleAgent_client/internal/protocol/types.go`
- `moleAgent_client/client.go`
- `moleAgent_client/tunnel.go`

输出：

- 配置变更统一走"持久化 -> 推送客户端 -> 更新运行态"
- `Tunnel.Enabled` 变化能被客户端感知并立即用于后续转发决策

### 阶段 4：用户自助 Token API

目标：

- 提供创建、列表、删除、轮换 API

改动点：

- 新增 `internal/api/access_token_handler.go`
- `cmd/moleagent-serv/main.go` 路由注册

输出：

- 用户登录后可管理自己的接入 token
- 响应格式与现有 API 保持一致

### 阶段 5：节点/隧道归属过滤

目标：

- 普通用户只能看到并管理自己的节点和隧道
- 管理员保持全局可见
- 禁用隧道不参与服务端对外生效和 active 统计

改动点：

- `internal/api/node_handler.go`
- `internal/api/tunnel_handler.go`
- 新增 `internal/api/resource_scope.go` 或等价辅助代码
- `internal/tunnel/http.go`
- 与隧道路由、统计相关的最小必要位置

输出：

- 非管理员看不到他人节点
- 非管理员无法误删、误改他人隧道
- 已禁用隧道仍保留配置，但不会继续对外提供服务

### 阶段 6：迁移与收尾

目标：

- 补迁移脚本/初始化逻辑
- 完善日志和测试
- 更新文档

改动点：

- `internal/storage/db.go`
- `moleAgent_Serv/CLAUDE.md`
- 配置模板和部署文档

输出：

- 升级后旧库可直接运行
- 管理员知道如何逐步弃用旧全局 token

## 11. 测试计划

### 11.1 单元测试

至少补以下测试：

- `AccessTokenRepo` 的 create/get/delete/rotate
- `NodeAccessAuthenticator` 的成功、禁用、无效 token、兼容旧 token
- `NodeHandler` 的 owner 过滤
- `TunnelHandler` 的 owner 过滤
- `Tunnel.Enabled` 的默认值兼容和过滤行为
- 服务端 `tunnel_push` 下发后，客户端本地配置替换与事件通知行为

### 11.2 集成测试

至少覆盖：

1. 用户 A 创建 token，客户端用该 token 接入，节点归属 A。
2. 用户 B 登录后看不到 A 的节点。
3. 管理员能看到 A、B 全部节点。
4. 删除 token 后，新接入失败，旧在线连接不受影响。
5. 轮换 token 后，新 token 可接入，旧 token 不再可用于新连接。
6. 隧道被禁用后，配置仍存在且会同步到客户端，但不再参与服务端对外生效。
7. 客户端收到 `enabled=false` 的配置推送后，后续 HTTP/TCP/UDP 转发不再使用该隧道。

### 11.3 回归重点

重点确认以下原功能不被破坏：

- 原有 JWT 登录/刷新/改密不变。
- 原有 RBAC 权限判断不变。
- 隧道配置下发和持久化不变。
- 新增 `enabled=false` 时，隧道应停留在"已配置但不生效"的轻量语义。
- 客户端收到全量隧道配置推送后的替换逻辑不应退化为部分更新或脏状态叠加。
- 网关转发、MQTT、健康检查不变。
- 兼容期内旧全局 token 仍可接入。

## 12. 补充：关键细节明确

### 12.0.1 Token 格式与哈希算法

- **Token 明文格式**：`mat_` 前缀 + 32 字节随机 hex 编码（共 68 字符）。
- **TokenHash 算法**：`sha256(rawToken)` 的 hex 编码，存入 `access_token_hash_index`。
- **TokenPrefix**：取明文前 8 字符（如 `mat_xxxx`），用于列表展示区分。
- **Token 生成**：使用 `crypto/rand`，不使用 `math/rand`。

### 12.0.2 现有节点迁移策略

升级后数据库中已存在的节点没有 `OwnerUserID` 字段（JSON 反序列化为零值空字符串）。

**迁移规则**：

- 服务启动时扫描所有 `OwnerUserID == ""` 的节点。
- 将其 `OwnerUserID` 设为 `"system"`（或 `"admin"`），并写回持久化。
- 同时在日志中记录迁移数量，便于管理员审计。

这样避免"无归属节点"在后续归属过滤中被所有用户都看不到的尴尬局面。

### 12.0.3 删除用户的级联处理

当管理员删除用户时，需要处理该用户名下的资源：

- 该用户所有 `AccessToken` 标记为 `status: disabled`。
- 该用户归属节点的 `OwnerUserID` 改为 `"system"`。
- 已在线节点不强制断开，但后续重连将因 token 失效而拒绝。

**原因**：轻量实现，不做复杂会话管理。节点改为系统级后，管理员可重新分配。

### 12.0.4 版本兼容关键约束

`Tunnel.Enabled` 字段使用 Go 零值 `false`。如果服务端先发送包含 `enabled` 的推送，而客户端尚未升级：

- 旧客户端 JSON 解码时**忽略未知字段**，`Enabled` 零值为 `false`。
- 但由于旧客户端代码根本不读取 `Enabled` 字段，不会影响其转发行为。
- 旧客户端只是单纯地把未知字段丢弃，不会把隧道误判为禁用。

**结论**：`Enabled` 字段的新增对旧客户端**无影响**，前提是旧客户端不依赖 `Enabled` 字段做逻辑判断。但需要确保旧客户端的 `Tunnel` 结构体没有同名字段冲突。

**风险点**：如果旧客户端恰好也有一个 `Enabled` 字段（不同语义），则会冲突。当前代码确认旧客户端无此字段，安全。

### 12.0.5 TCP/UDP 隧道禁用的实现策略

当前 TCP/UDP 隧道的请求处理链路：

1. 外部请求到达网关端口
2. `TunnelGateway` 根据隧道名查找节点（`findNodeForTunnel`）
3. 通过节点的 smux 会话转发

**禁用策略**：

- **不在索引重建时创建/销毁监听器**（端口映射场景下端口绑定是固定的，频繁开关不现实）。
- **在请求到来时查找目标阶段跳过 `enabled=false` 的隧道**（`findNodeForTunnel`、`findByTunnelName`、全量扫描兜底）。
- 这样端口仍然在监听，但到达的请求找不到有效隧道，返回连接拒绝或 502。
- 与 HTTP 隧道处理方式保持一致：索引层过滤 + 运行时过滤。

### 12.5.1 风险：把"归属控制"做进 Repo

不建议在 Repo 层按 userID 过滤数据，否则会把鉴权逻辑污染到底层存储，耦合上升。

规避：

- Repo 只做数据读写。
- 归属过滤放在 Handler/小型 Scope 辅助层。

### 12.5.2 风险：直接重写 RBAC

不建议把 RBAC 改成"资源实例级策略引擎"，这会显著放大改造面。

规避：

- 保持"RBAC 管资源类型，归属逻辑管资源实例"的双层结构。

### 12.5.3 风险：删除 token 立即踢掉在线节点

这会引入在线会话定位、强制断线和状态同步复杂度。

规避：

- 第一版只限制"后续新连接"，不主动踢现有连接。

### 12.5.4 风险：仅存储 enabled 但服务端仍按启用处理

如果只把 `enabled` 当作普通字段持久化，而不在服务端生效路径做过滤，那么"禁用"会名存实亡。

规避：

- 保持实现边界轻量，但必须在最小必要的服务端生效路径中过滤 `enabled=false`。
- 不做复杂调度，只做"是否参与生效"的布尔判断。

## 13. 推荐落地顺序

若按"最小风险上线"排序，推荐：

1. 先做数据模型和仓储。
2. 再做控制端接入认证兼容。
3. 然后开放用户自助 token API。
4. 最后补节点/隧道归属过滤。

原因：

- 前两步先建立"归属数据"。
- 第三步提供新入口给用户使用。
- 第四步再收紧可见性，避免出现"新 token 已发，但旧数据未绑定归属"导致的误判。

## 14. 最终结论

这是一个适合"小步快跑"的改进项，不建议做成大重构。

最合适的落地方式是：

- 新增 `AccessToken` 作为用户级接入凭据。
- 新增 `Node.OwnerUserID` 作为资源归属基础。
- 新增 `Tunnel.Enabled` 作为隧道级配置开关。
- 控制端接入时绑定归属。
- API 层按归属过滤。
- 服务端与客户端同步完整隧道配置，运行时忽略已禁用隧道。
- 兼容保留旧全局 token，分阶段淘汰。

这样可以在较小改动范围内，补齐"多用户系统真正按用户隔离节点"的核心能力，并增加实用的隧道启停开关，同时不破坏当前隧道、认证和运维主链路。

---

## 15. 审查 v3 修复记录（2026-04-15）

审查报告 v3 发现 6 项偏差，已全部修复并通过测试验证。以下为修复细节：

### P0-1：AccessTokenRepo.Update() hash 索引一致性

**问题**：Token 轮换后，`Update()` 不维护 `access_token_hash_index`，导致旧 Token 通过旧索引仍可查到、新 Token 通过新索引查不到。

**修复**：`Update()` 现在先获取旧 Token 记录，若 `TokenHash` 变更则按序执行：建立新索引 → 更新记录 → 删除旧索引。每步失败均有回滚保护（`access_token_repo.go`）。

### P0-2：TunnelHandler.Create() 跨用户写权限缺口

**问题**：非管理员可通过 `POST /tunnels` 指定任意 `node_id` 修改他人节点的隧道配置。

**修复**：`Create()` 在执行隧道配置变更前校验 `node_id` 归属。非管理员只能给自己的节点添加/修改隧道（`tunnel_handler.go`）。

### P0-3：删除用户后的在线运行态未同步

**问题**：`UserHandler.Delete()` 只更新 `nodeRepo`（持久化层），不同步 `nodeMgr`（运行态），导致在线节点归属过滤失真。

**修复**：`UserHandler` 新增 `NodeManager` 依赖（`core.NodeManager` 接口），Delete 时同时更新持久化层和运行态中的 `OwnerUserID`（`user_handler.go`、`main.go`）。

### P1-4：TunnelHandler.Stats() 泄露全局统计

**问题**：`Stats()` 统计所有节点的隧道数据，非管理员能看到全局统计信息。

**修复**：`Stats()` 增加归属过滤，非管理员只能看到自己节点上隧道的统计数据（`tunnel_handler.go`）。

### P1-5：预配置节点未绑定 owner

**问题**：`NodeHandler.Create()` 新建节点时不写 `OwnerUserID`，预配置节点成为无主节点。

**修复**：`Create()` 从 JWT claims 自动绑定归属。管理员创建 → `system`，普通用户创建 → 当前用户 ID（`node_handler.go`）。

### P1-6：Token 优先级实现分叉

**问题**：`control.go` 先比对旧全局 Token（legacy 优先），再走用户级 Token 认证，与设计方案要求的"用户级 Token 优先"不一致。

**修复**：`ControlServer.handleConnection()` 统一委托给 `NodeAccessAuthenticator` 处理认证。`authenticator` 内部实现"用户级 Token 优先 → legacy 兜底"的优先级语义。仅在未注入 `authenticator` 时回退到直接比对旧全局 Token（`control.go`）。

### 新增测试

| 测试文件 | 新增用例 | 验证点 |
|---------|---------|--------|
| `storage/access_token_repo_test.go` | `TestAccessTokenRepo_Update_MaintainsHashIndex` | 轮换后旧 hash 索引不可查、新 hash 索引可查 |
| `api/tunnel_handler_test.go` | `TestTunnelHandler_Create_NonOwner_404` | 非管理员给他人节点创建隧道返回 404 |
| `api/tunnel_handler_test.go` | `TestTunnelHandler_Stats_NonAdminOnlyOwnTunnels` | Stats 按归属过滤只统计自己的隧道 |
| `api/user_handler_test.go` | `TestUserHandler_Delete_SyncsRuntimeNodeOwner` | 删除用户后在线运行态 owner 同步更新 |
| `api/node_handler_test.go` | `TestNodeHandler_Create_BindsOwnerUserID` | 创建预配置节点自动绑定归属 |

### 最终完成标准复核

| # | 完成标准 | v3 修复后判定 |
|---|----------|-------------|
| 1 | 用户可以自助创建多个接入 Token | ✅ |
| 2 | 节点接入后自动归属用户 | ✅ |
| 3 | 普通用户只能查看和管理自己的节点/隧道 | ✅ |
| 4 | `Tunnel.Enabled` 可持久化、可同步、可生效 | ✅ |
| 5 | 隧道配置变化统一通过 `TunnelConfigService → tunnel_push → 客户端替换` | ✅ |
| 6 | 客户端能检测配置变化并按新配置工作 | ✅ |
| 7 | 升级后旧节点自动迁移为系统归属 | ✅ |
| 8 | 删除用户后其资源有明确级联处理策略 | ✅ |
| 9 | HTTP/TCP/UDP 三种隧道 `enabled=false` 过滤行为一致 | ✅ |
