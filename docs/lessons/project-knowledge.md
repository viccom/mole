# moleAgent 项目开发经验与工程知识

> 基于 2026-04-14 ~ 2026-05-09 共 25 天、60+ 次提交的开发实践总结。
> 涵盖服务端 (moleAgent_Serv) 和客户端 (moleAgent_client) 两个仓库。

---

## 一、踩过的坑与修复

### 1.1 smux 参数调优：三连翻车

**问题**：TCP 隧道下 RDP 场景严重卡顿，操作延迟数秒。

**第一次尝试** (`8a31126`)：增大 biCopy buffer 和 smux 会话参数。未解决根本问题。

**第二次尝试** (`04fcf01`)：发现 smux `MaxFrameSize` 超出 uint16 范围，将 65536 改为 65535。仍然卡顿。

**第三次尝试** (`233b3df`)：**回退 `MaxFrameSize` 为 32768**。smux 库在运行时对帧大小有内部限制，65535 虽然不溢出 uint16，但超出库的实际处理能力。改为 32768 后问题彻底解决。

**教训**：
- smux `MaxFrameSize` 必须为 32768，不是 65535，也不是 65536
- 修改第三方库参数时，先看库的源码常量定义，不要想当然
- 一次只改一个变量，不要同时调多个参数再一起测试

### 1.2 TCP_NODELAY 与内核缓冲区

**问题**：TCP 隧道转发延迟高，小包场景（如 SSH 交互）明显。

**根因**：Nagle 算法积累小包、内核 TCP 缓冲区默认值太小。

**修复** (`67984d9`)：
- 对所有 TCP 连接设置 `TCP_NODELAY`
- 增大内核 `SO_RCVBUF` / `SO_SNDBUF`
- 客户端同步修复 (`30c6dfa`)

**教训**：交互式协议（SSH、RDP、串口）必须禁用 Nagle，否则延迟随包大小非线性增长。

### 1.3 biCopy buffer 大小

**演进过程**：
- 初始：小 buffer（4KB-32KB）
- `8e1083f`：增到 256KB，解决视频/大文件场景
- `ffd5803`：增到 512KB
- `29b21e0`：最终定在 1MB

**教训**：内网穿透场景下，数据在内存中直通，大 buffer 没有拷贝开销（io.Copy 本身用固定 buffer 循环）。真正影响吞吐量的是单次 syscall 的数据量。1MB buffer 在 RDP/文件传输等高吞吐场景下表现最优。

### 1.4 notifyManagers 的 cfg.Enable 同步

**问题** (`f297de9`)：客户端本地 API 启停隧道后，ser2mq/vpn Manager 收到的配置中 `Enable` 始终为 false。

**根因**：服务端存储的 `Para` JSON 可能不含 `enable` 字段。Go 的 `json.Unmarshal` 对 bool 默认零值 false。`notifyManagers()` 中直接使用反序列化结果，没有同步隧道的 `Enabled` 状态。

**修复**：在 `notifyManagers()` 中，反序列化 Para 后必须执行 `cfg.Enable = t.IsEnabled()`。`IsEnabled()` 将 `Enabled == nil` 视为启用（向后兼容旧数据）。

**教训**：JSON 反序列化的零值和"未设置"不可区分。当业务语义上"未设置"≠"零值"时，必须在反序列化后显式同步。

### 1.5 客户端隧道变更不通知 Manager

**问题** (`b20eab8`)：客户端通过本地 REST API 添加/删除隧道后，ser2mq 和 vpn Manager 没有响应——隧道配置在内存中更新了，但处理器/进程没启停。

**根因**：`processTunnelUpdates()` 在服务端同步成功后更新内存 `tunnels[]`，但**没有调用 `notifyManagers()`**。

**修复**：成功更新内存后立即调用 `notifyManagers()`。

**教训**：配置更新的完整链路是 `存储 → 内存 → Manager → 运行时`。漏掉任何一环都会导致"配置改了但没生效"。

### 1.6 隧道 toggle 丢失字段

**问题** (`f6917c1`)：前端启用/禁用隧道时，只传 `enabled` 字段，导致 `domain`、`listen_port`、`Para` 等字段被覆盖为空。

**修复**：`applyTunnelMutation` 的替换逻辑中，必须保留原有隧道的所有字段，只更新变更的部分。

**教训**：全量替换的 API 设计中，前端必须原样传回所有字段。后端也应做防御性处理——对已知未变更的字段保留原值。

### 1.7 同名隧道统计串台

**问题** (`773b38b`)：不同节点的同名隧道统计数据互相覆盖。

**根因**：统计 key 仅用隧道名称，未包含节点 ID。

**修复**：统计 key 格式改为 `nodeID/tunnelName`。

**教训**：在多租户/多节点系统中，所有运行时数据的 key 必须包含租户/节点标识。

### 1.8 健康检查误删预配置节点

**问题** (`773b38b`)：健康检查发现节点离线后，直接删除节点记录。但预配置节点（通过 REST API 提前创建的离线节点）也被删除了。

**修复**：健康检查只修改节点状态为 `offline`，不删除记录。预配置节点的生命周期由管理员管理。

**教训**：健康检查和垃圾回收的边界要清晰——健康检查负责状态标记，不负责资源回收。

### 1.9 KCP bufferedConn 与 WS 溢出

**问题** (`a34c9fc`)：添加 KCP 传输后，连接数据丢失和缓冲区溢出。

**根因**：KCP 是 UDP-based，数据报文可能一次性读到多个包，`bufio.Reader` 的默认缓冲区不够用。

**修复**：增大 `bufferedConn` 的缓冲区大小，WebSocket 同理。

**教训**：不同传输协议的读写模式差异很大。TCP 是流式的，KCP/UDP 是报文式的。缓冲区设计必须匹配协议特性。

### 1.10 文档记录了不存在的环境变量

**问题**：CLAUDE.md 中记录了 `MA_WS_PORT` 和 `MA_KCP_PORT` 环境变量，但 `config.go` 的 `applyEnvOverrides()` 中并没有实现。

**修复**：移除这两个不存在的环境变量文档，添加说明：`ws_port` 和 `kcp_port` 仅支持 YAML 配置文件。

**教训**：文档与代码必须同步。修改配置逻辑时，同步更新所有引用该配置的文档（CLAUDE.md、README.md）。

---

## 二、架构设计经验

### 2.1 隧道配置真相源模式

**核心原则**：服务端持久化是唯一真相源，客户端仅持有内存快照。

```
管理后台修改
  → TunnelConfigService (统一入口)
    → 持久化到 Redka (落库)
    → 推送到在线节点 (tunnel_push)
    → 更新内存路由索引 (RebuildIndex)
```

**关键约束**：
- **不绕过 TunnelConfigService**：直接调 `nodeRepo.Update` 会导致持久化与运行态不一致
- **在线节点先推后更新**：先推送配置到客户端，客户端确认后再更新服务端索引。颠倒顺序会导致路由把流量切到未准备好的隧道
- **客户端上报的配置不覆盖服务端持久化**：节点重连时，以服务端持久化配置为准

### 2.2 Session 与 Node 模型分离

**设计**：smux Session 存储在 `ShardedNodeManager.sessions` map 中，`core.Node` 是纯领域模型，两者分离。

**好处**：
- Node 可以序列化到 Redka，不含 smux Session（不可序列化）
- API 返回 Node 数据时不暴露传输层细节
- 健康检查、断开清理等操作独立于领域模型

**获取 Session 的唯一途径**：`nodeMgr.GetSession(nodeID)`，不存在于 Node 结构体上。

### 2.3 分片节点管理器

**设计**：256 个分片（FNV-1a hash），每个分片独立 `sync.RWMutex`。

**理由**：单锁在节点数多时成为瓶颈。分片后，不同节点的操作可以并行执行。

**教训**：分片数选 2 的幂次（256），可以用位与替代取模运算。FNV-1a 分布均匀性足够。

### 2.4 依赖注入顺序

**规则**：`main.go` 中的组件创建顺序必须满足依赖关系，通过构造函数注入。

```
config → logging → storage → repos → auth → nodeMgr → gateway
  → transport → controlSrv → tunnelSvc → disconnectHandler → apiRouter
```

**关键**：回调函数（`onNodeChange`、`onNodeDisconnect`）在组件创建后、启动前设置，避免循环依赖。

### 2.5 RBAC + 资源归属双层控制

**设计**：
- RBAC 管资源类型权限（`tunnels:read`、`nodes:admin`）
- `resource_scope.go` 管资源实例归属（`OwnerUserID`）
- Handler 层做过滤，Repo 层不感知归属

**教训**：权限检查放在 Handler 层而非 Repo 层，保持数据读写纯粹。但这也意味着新增 Handler 容易忘记加权限检查——需要 code review 重点关注。

### 2.6 多传输协议抽象

**设计**：`Transport` 接口抽象，当前实现 TCP/TLS、WebSocket、KCP 三种。

**关键**：支持多协议同时监听——`control_port` 为 TCP 主监听，`ws_port` 和 `kcp_port` 配置额外监听。三种协议共用 worker pool 和认证流程。

**教训**：`AddTransport()` 动态添加 listener，而不是在配置中静态选择。这允许一个服务端同时服务不同网络环境的客户端。

---

## 三、性能优化经验

### 3.1 数据透传的 buffer 选择

| 场景 | 推荐 buffer | 理由 |
|------|-----------|------|
| HTTP 代理响应体 | 1MB | 大文件/视频流场景 |
| WebSocket relay | 1MB | 双向实时数据流 |
| TCP/UDP 透传 | 1MB | RDP/SSH 等交互式协议 |
| 串口读取 | 256B-1KB | 串口波特率有限，大 buffer 浪费内存 |

### 3.2 路由索引设计

**双索引**：`domainIdx`（域名匹配）+ `tunnelIdx`（隧道名匹配）+ 全量扫描兜底。

**匹配优先级**：精确域名 > 路径前缀 > 虚拟主机约定 > 兜底全量扫描。

**重建时机**：每次节点变更（注册、断开、隧道更新）全量重建 `RebuildIndex`。

**当前瓶颈**：节点数多时全量重建可能慢。未来需考虑增量索引。

### 3.3 串口 copy-and-release 模式

**设计**：串口读写使用"复制数据后立即释放锁"模式，不持锁跨 I/O。

**理由**：串口 I/O 可能阻塞（等待数据），持锁会导致其他操作（如关闭串口）长时间等待。

---

## 四、前端工程经验

### 4.1 React SPA 全量重写

**教训**：从零搭建 React + Vite + Tailwind 项目时，按以下顺序分批提交：

1. 项目脚手架 + 配置对齐 (`f9a861b`)
2. API client、types、utils、hooks (`63953bc`)
3. 通用 UI 组件 (`429eb81`)
4. 页面逐个实现：login → dashboard → nodes → tunnels → users → tokens (`50df9e5` ~ `5706dc8`)
5. 构建产物部署 (`0d9e17e`)

**好处**：每步可独立验证，出问题容易定位。不要一次性提交整个前端。

### 4.2 前端表单的 buildPara 陷阱

**问题**：编辑 ser2mq/vpn 隧道时，如果表单没有显示某些字段（如 `secret`、`qos`、`stopbits`），提交时这些字段丢失，服务端存储被覆盖。

**规则**：`buildPara` 必须包含该类型 Para 的所有字段，未在表单中显示的字段原样传回。

### 4.3 ES Module 模块化前端

**设计**：客户端内置前端使用原生 ES Module，每个页面一个模块，`init()` 函数模式。

**好处**：无构建步骤，`go:embed` 直接嵌入，适合嵌入式设备的轻量管理界面。

---

## 五、协作与工程规范经验

### 5.1 CLAUDE.md 作为 AI 协作契约

**实践**：CLAUDE.md 不是普通文档，是 AI 编码助手的"行为规范"。包含：
- 项目结构和技术栈（帮助 AI 理解上下文）
- 关键约定和易出错点（防止 AI 重复犯错）
- 依赖注入顺序（引导 AI 在正确位置添加新组件）
- 开发约束（如"修改前先 git pull"）

**维护规则**：
- 每次 AI 犯错后，将错误模式添加到"注意事项"中
- 代码结构变更时同步更新
- 环境变量/配置项必须与实际代码一致

### 5.2 渐进式功能开发

**项目演进轨迹**（按时间线）：

| 阶段 | 时间 | 核心工作 |
|------|------|---------|
| 基础架构 | 04-14 | 并发修复、隧道配置持久化、TunnelConfigService 引入 |
| 安全增强 | 04-15 | 用户级 AccessToken、RBAC、资源归属、管理后台 SPA |
| 运行时能力 | 04-15~16 | TCP/UDP 代理头、运行时统计、隧道启停 |
| 性能调优 | 04-27 | smux buffer 三连调优、TCP_NODELAY、1MB buffer |
| 特殊隧道 | 04-22 | ser2mq、vpn-manager 类型支持 |
| 多协议 | 05-02~04 | Transport 接口抽象、WebSocket、KCP |
| 客户端重构 | 04-27~05-08 | ser2mq 协议兼容、SSE 实时流、vnt-cli 集成 |
| 文档完善 | 05-09 | 设计文档更新、控制指令扩展计划 |

**经验**：先做核心路径（注册→隧道→转发），再做运维能力（统计、启停），最后做扩展（多协议、特殊类型）。每一步都确保现有功能不回退。

### 5.3 提交信息规范

**格式**：`type(scope): description`

- `feat` 新功能、`fix` 修复、`refactor` 重构、`perf` 性能、`docs` 文档
- scope 标明影响范围：`smux`、`tcp`、`transport`、`ser2mq`、`admin` 等
- 关联修复链：如 smux 调优的三个提交 `8a31126` → `04fcf01` → `233b3df` 可清晰看到尝试路径

---

## 六、可复用的设计模式

### 6.1 控制协议模式

单连接多路复用（smux）+ JSON 命令：
- 所有命令通过 smux stream 传输，格式：单行 JSON + `\n`
- 请求-响应对：stream 生命周期 = 一个请求-响应周期
- 双向推送：服务端通过 `OpenStream` 主动发起（如 tunnel_push）

### 6.2 配置变更三步曲

任何配置变更必须走：**持久化 → 推送 → 运行时更新**

```
1. 写持久化存储 (Redka)
2. 推送到在线客户端 (tunnel_push via smux)
3. 更新服务端运行时索引 (RebuildIndex)
```

缺任何一步都会导致状态不一致。

### 6.3 资源回收策略

**原则**：断开时只清理运行态，不修改持久化。

- 清理 TCP/UDP 监听器
- 清理路由索引条目
- 清理统计数据条目
- **保留**持久化配置（节点重连时通过 `applyRuntimeTunnels` 重新激活）

### 6.4 向后兼容的 JSON 字段扩展

- 新增字段使用 `omitempty`
- 零值（`nil`、`false`、`0`、`""`）等于"未设置"，旧版本忽略
- 示例：`Tunnel.Enabled` 用 `*bool` + `omitempty`，`nil` 视为启用

---

## 七、给未来开发的建议

1. **改配置先看代码**：不要凭文档修改配置逻辑。先 `grep` 确认配置字段在 `config.go` 中是否存在。

2. **改 tunnel 走 TunnelConfigService**：任何隧道增删改必须通过统一入口，不要直接操作 `nodeRepo` 或 `nodeMgr`。

3. **改协议先查兼容性**：smux/控制协议的任何字段变更，考虑旧客户端/服务端的行为。`omitempty` 是好朋友。

4. **新 Manager 别忘 notifyManagers**：新增隧道类型时，`notifyManagers()` 必须添加对应分支，否则配置变更不会触发运行时更新。

5. **测试真实场景**：smux 参数调优、buffer 大小等性能问题，只有真实场景（RDP、大文件传输）才能暴露。单元测试无法覆盖。

6. **文档即代码**：CLAUDE.md 和 README.md 是项目的"活文档"，每次功能变更必须同步更新。过时的文档比没有文档更危险。
