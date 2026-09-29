# P2P Tunnel Integration Plan（v2，2026-09-13 复核修订）

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 把 p2punch 作为 moleAgent_client 的新隧道类型 `TunnelTypeP2P` 集成。两端都是 moleAgent_client，借用 moleAgent_Serv 的中心管理（tunnel 配置持久化、tunnel_push 分发）、内嵌 MQTT broker（:1883）做 P2P 信令、内嵌 STUN server（:3478，server 端新增）做 NAT 探测兜底。P2P 数据流量跳过 moleAgent_Serv，**不引入独立二进制**。

**Architecture:**
- fork p2punch 整包到 `moleAgent_client/internal/p2p/`（**11 个子包**，`//go:build p2p` 隔离），新增 `moleAgent_client/internal/proxy/p2p/` Manager 接入现有 tunnel 框架。
- **P2P 隧道默认不编译**：默认 `go build ./...` 不含任何 P2P 代码；仅 `-tags p2p` 显式启用。无 `nop2p` 反向 tag。
- moleAgent_Serv 协议层接受 `TunnelTypeP2P`（与 ser2mq/vpn-manager 同类「客户端本地类型」），新增内嵌 STUN server（`internal/stun/`，**无 build tag**，配置门控默认关）。
- MQTT broker 复用现有 :1883（mochi-mqtt）；**信令鉴权走 P2PSignalToken**（见「信令鉴权」节；开发调试可临时用公共 broker 解耦）。

**Tech Stack:**
- `github.com/hashicorp/yamux v0.1.2`（P2P session 多路复用）
- `github.com/pion/dtls/v3 v3.1.5`（client UDP 链路加密）
- `github.com/pion/stun/v3 v3.1.6`（client 探测 + server 内嵌 STUN）
- 复用现有：`eclipse/paho.mqtt.golang v1.5.1`、`xtaci/kcp-go/v5 v5.6.72`（两端版本已对齐）
- client go.mod 现状基线：**直接依赖 9 个**；p2p 引入后 +3 直接依赖（yamux/dtls/stun）

---

## v2 修订说明（2026-09-13 三仓库核查后回落）

本次修订基于对 moleAgent_client / moleAgent_Serv / p2punch 的逐行核查，主要变更：

1. **build tag 决策（用户裁定）**：p2p 隧道**默认不编译**，`-tags p2p` 显式启用。删除 v1 的「默认开 / `-tags nop2p`」表述。跨 tag 调用用**成对文件**（`p2p` / `!p2p` 各一份同名函数）实现，常量与数据结构不加 tag。
2. **room 语义与中心分发定位（用户澄清）**：p2punch 中只有加入相同 room 的两端才可能建连；moleAgent 集成的核心价值是**中心生成唯一 room 并把配置对分发到两个节点**。建连机制本身与 p2punch 原生完全一致，中心不参与打洞。见「room 与中心分发」节。
3. **服务端引用纠错**（v1 引用了不存在的符号）：
   - `moleAgent_Serv/internal/protocol/types.go` **不存在** → 删除该步骤；server 侧常量落 `internal/core/domain.go`。
   - `isValidTunnelType` **不存在** → 类型校验在 `internal/service/tunnel_service.go:64-101` 的 `validateTunnel` switch。
   - `handleTunnelAction` **不存在** → 实际是 `TriggerTunnelAction`（`internal/tunnel/control.go:751-766`，S→C 出站、无类型概念）。
   - `core.TunnelConfigService` → 实际接口 `TunnelConfigManager`（`internal/core/ports.go:26`）。
   - `internal/api/auth/middleware.go` → 实际 `internal/auth/middleware.go`；handler 命名惯例是 `*_handler.go`（非 `handler_*.go`）。
4. **删除 v1 阶段 8（tunnel_action ack）**：与「不做」清单（远程 action 不做）矛盾，且方向理解反了——`tunnel_action` 是 server→client 出站命令，server 端不存在「收到并 ack」。p2p 与 http/tcp/udp 同类：不支持远程 action，`TriggerTunnelAction` 无需任何改动。
5. **信令鉴权整合进正式阶段**：v1 的三次增订（方案 D → 变体 B+ → P2PSignalToken）散落文末、与阶段 0-9 两张皮。v2 收敛为单一方案并编入阶段 6；且 **client→server 的 REST 调用在现有代码中零先例**，拉取凭据改走**现有 smux 控制通道**（新 C→S 命令 `p2p_signal_token`），不新开 HTTP 通道。
6. **fork 范围修正**：`internal/` 实际 **13 个包**，fork 11 个（剔除 `update`——带 go-selfupdater 依赖与硬编码升级 URL；`version`）。v1 的「10 个子包」及名单均不准确。
7. **Handler 职责明确**：从「room+modes」到「打洞→secure upgrade→yamux→Session」的编排代码在 p2punch 的 `cmd/cli/connection.go`，**不在 fork 的 internal/ 里**。Handler 必须吸收这段 glue（engine 参数、按传输分叉的 secure 配置、role 消费、退避循环——退避可照抄 `cmd/cli/connection.go:66-70` 及其单测 `cmd/cli/events_test.go:69-87`）。注意 `Outcome` 的**组装**在 `internal/engine/mode_*.go`（随 engine 包整体 fork，v2.2 第 22 条），connection.go 仅消费——Handler 调 `engine.Registry[mode]` 即得 Outcome。
8. **MQTT 凭据事实纠错**：p2punch **当前没有任何** SetUsername/SetPassword 配置点（全仓库 grep 零命中，`easyp2p/mqtt_signal.go:170-197` 仅匿名）。v1 前文「p2punch 本身支持 SetUsername/SetPassword」是错的；p2punch 上游 PR 为**硬前置**。
9. **状态上报两处**：`collectTunnelStatuses()`（client.go:661，喂 `tunnel_status` 命令）**和** `AllTunnelStatus()/buildTunnelStatus()`（client.go:1303/1246，喂本地 REST `/api/tunnels`）都要加 P2P 分支，缺一则管理界面看不到状态。
10. **常量归位**：client `TunnelTypeP2P` 落根包 `tunnel.go`（与 ser2mq 同款——`internal/protocol/types.go` 只有 http/tcp/udp，ser2mq/vpn-manager 从不在协议层）；server 落 `core/domain.go`。v1「两端 4 处常量」不成立，实际 **2 处**，protocol 层零改动（`Type` 是命名类型 `protocol.TunnelType`（types.go:17，底层 string），"p2p" 值自然透传）。
11. **server STUN 去 build tag**：改配置门控（`stun.enabled` 默认 false）。理由：tag 会让 main.go 无条件引用一个默认不编译的包而构建失败，需双侧接线；pion/stun 依赖很小，运行时门控等效且简单。
12. **精度修正**：Manager 模板改为 ser2mq 风格（v1 引 vpn 为模板但自拟接口是单 map，互斥；vpn 实际是双参 `OnTunnelUpdate(names, configs)`，client.go:422）；体积表「顶层依赖 57」口径错误（直接依赖 9 个）；client `Validate()` 需为 p2p 增加**空 Target 豁免**（tunnel.go:56 目前只豁免 vpn-manager）；`notifyManagers` 中 p2p 分支必须同步 `Enable`（同 ser2mq/ser2net/webssh，client.go:370-409）。

### v2.1 二轮审核补强（2026-09-13）

13. **tunnel 不对称语义修正**（v2 实质性错误）：核实 `p2punch/internal/tunnel/{tunnel.go,tcp.go}` 后确认——发起端监听 `LocalPort`，**target 由发起端在 `TUNNEL:OPEN` 里指定、由接受端 dial**（tunnel.go:24-29,187-268；tcp.go:15,99-103），接受端无 target 概念、无 allowlist。v2 的「两端配置对称、同 target」不成立，改为访问端/纯会话端模型（见「P2PConfig」节）。
14. **room 配对不变量**：server 新增跨记录配对校验（room ≤2 条、异节点、排除自身计数）——第三端持同 room 会与陌生节点完成 ECDHE 建连（对陌生对端照样成功），属数据外泄面，必须挡在落库前。
15. **Manager 构造注入 `serverHost`**：默认 `mqtt_brokers`/`stun_servers` 需从 server 地址派生（对齐 ser2mq `NewManager(ctx, nodeID)` 的注入模式）。
16. **字节统计来源钉死**：`session.ListTunnels()` 的 `TunnelInfo` 自带 `BytesIn/BytesOut`（session.go:60-69，普通 uint64 字段；atomic 计数器在 session_secure.go 内部、Load 后填入，导出结构无 32 位对齐问题），无需自写 conn 计数包装。
17. **fork 文件既有 build tag 合并**：netx 两文件现有 `!windows` → 合并为 `p2p && !windows`，脚本不得覆盖。
18. **信令鉴权细化**：authHook 前缀分支置于 `VerifyMQTTCredentials` 之前；aclHook 拒 MQTT 通配符 topic（防 `nat-exchange/#` 全域订阅）；`p2p_signal_token` 响应用 ad-hoc JSON（不扩 `ControlResponse`）；token 增加「删除隧道即吊销」与 node 归属校验；仅 server 默认 broker 时拉取 token。
19. **配置变更重启**：Manager 增量逻辑补「同名 Para 变更 → 重启 handler」（对齐 vpn 的变更重启惯例）。
20. `TunnelStatusByName()`（client.go:1320）纳入状态接线清单；运维速查改为访问端/纯会话端两条记录的示例，并补 :3478/UDP 防火放行提示。

### v2.2 开工前复核（2026-09-13，三仓库逐项核查后修订）

21. **vpn 重启惯例位置纠错**：`client.go:200-225` 实为 `Run()` 重连循环主体；「配置变更→Stop→NewProcessMgr→Start」的重启惯例实际在 `internal/proxy/vpn/manager.go:199-225`（阶段 5.4 已改引）。
22. **Outcome 组装位置纠错**：`Outcome{Mux, IsClient, Local, Remote}` 的字面量组装在 `internal/engine/mode_*.go`（mode_lan.go:37、mode_v4_tcp.go:36、mode_v4_udp.go:42、mode_v6.go:104、mode_v6_tcp.go:98、mode_v4_relay.go:68 共 6 处），随 engine 包整体 fork 获得；`cmd/cli/connection.go` 仅消费（126 行 `modeFn(ctx, deps)` 返回 Outcome）。第 7 条与阶段 5.6 已改写。
23. **protocol.Tunnel.Type 是命名类型**：types.go:17 `Type TunnelType`（底层 string），非裸 string；"p2p" 透传结论不变（第 10 条已修正表述）。
24. **auth/middleware.go 行号对调**：W-Access-Key 直通在 authenticate() **75-79**；55-65 是 RequirePermission 的 RBAC 绕过检查（`claims.UserID != "access_key"`）。旁路事实不变，安全模型节引用已修正。
25. **TunnelInfo 字节字段口径修正**：`BytesIn/BytesOut` 是普通 `uint64`（session.go:67-68）；atomic 计数器在 session_secure.go 内部、Load 后填入 TunnelInfo——导出结构无 32 位对齐问题，第 16 条结论（无需自写计数包装）不变。
26. **离线测试分布修正**：`net.Pipe` 系离线测试在 engine/netx/secure/session；crypto 与 heartbeat 的测试为纯函数离线测试（无 net.Pipe）。阶段 4.1 已改写。
27. **行号微漂收录**：collectTunnelStatuses 实为 client.go:661；`TestReconnectBackoff` 实为 events_test.go:69-87；`readResponse` 定义于 client.go:1174、sendTunnelUpdate 内调用点 533；`tunnel_test.go` 在 client 根包尚不存在（阶段 0.1 为新建）；server 侧 `TunnelConfigManager` 接口在 ports.go:26、`validateTunnel` 在 tunnel_service.go:64-101、`TriggerTunnelAction` 在 control.go:752-766。

---

## 目标边界

- 把 p2punch 1:1 P2P 协议作为 moleAgent_client 的一种新隧道类型。**连接（session）两端对称**——同 room 各自起 engine，保留 p2punch 原生 SelectRole 决定机制；**隧道（tunnel）不对称**——发起端 CreateTunnel 监听，接受端被动 dial（见「P2PConfig」节语义）
- **不引入独立二进制**：p2punch 核心包作为子包嵌入 moleAgent_client，p2punchd/desktop/relaysrv 完全废弃
- **默认不编译**：`//go:build p2p` 隔离，`-tags p2p` 显式启用；不进入发布矩阵，仅供开发期显式构建（Makefile 不变）
- **TunnelType 命名**：`"p2p"`；常量位置 client 根 `tunnel.go` / server `core/domain.go` 各一处
- **P2P 流量跳过 moleAgent_Serv**：服务端只管配置存/分发，不参与流量；`dispatchStream()` 零改动（P2P session 独立于 smux 数据面）
- **P2P session 独立 lifecycle**：不挂 moleAgent_Serv 的 smux session ctx，server 断线不影响 P2P，P2P 断不影响 smux
- **MQTT 信令默认走 server :1883**（`mqtt_brokers` 默认填 server 地址）；生产启用 server broker 信令需先完成阶段 6（信令鉴权）；开发调试可临时指向公共 broker（paho 匿名连接公共 broker 不受 server authHook 约束）
- **STUN 公共为主（miwifi/hitv/bilibili 优先，同 p2punch），server :3478 内嵌兜底**（Para `stun_servers` 末尾追加）
- **room 由服务端流程生成并分发**（见下节），两端配置一致即建连

## 不做（明确范围外）

- admin/ 前端 P2P 表单（curl POST 即可）
- mobile/ 前端、desktop wails GUI 集成（后续单独 PR）
- v4-relay 兜底（MVP 跑默认链 lan,tcp-v6,udp-v6,udp-v4；`relay_server` 字段预留）
- **远程 tunnel_action**：p2p 不支持远程 start/stop/restart，server `TriggerTunnelAction` 零改动；client 本地 builtin API 的 start/stop 后续可选
- p2p tunnel 详细 metrics（MVP 只报 running + connected + bytes_in/out）

---

## room 与中心分发（v2 新增，用户澄清）

**p2punch 原生语义**：room 即信令 sessionUid。只有加入**相同 room** 的两个实例才可能建连；room 同时是信令 topic 盐（`nat-exchange/ + hex(sha256(salt‖hex(md5(room))))[:16]`，easyp2p/p2p.go:162-167,257-259）、信令 payload AES-GCM key 派生输入（`deriveKey("mqtt-exchange-gonc-v2.2.0", room)`，固定盐 "nc-p2p-tool"）与 E2E 上下文。**room 不同 → 永远连不上；room 相同 → 建连机制与 p2punch 单机使用完全一致**（SelectRole 定角色、中心不参与）。

**moleAgent 集成的定位**：中心管理的价值不在打洞，而在 **room 的生成与配置分发**：

1. 管理者（或后续 admin 端点）创建 P2P 隧道时，由**服务端生成强随机 room**（crypto/rand，base62，≥16 字符，满足 8-32 `[a-zA-Z0-9_-]` 校验）。**room 是共享密钥**（知道 room 即可加入信令并推导 payload key），不可用自增 ID/时间戳；日志不回显完整 room。
2. 服务端创建**一对 Tunnel 记录**（node A、node B 各一条，同 name、同 room；**Para 按角色不对称**——访问端带 `local_port`+`target_*`，服务端只有 room 等会话字段，见「P2PConfig」节）。
3. 既有 `tunnel_push` 全量分发机制把配置推到两端——**零新增协议**，两端各自 `notifyManagers()` 收到相同 room 即开始建连。

**配对不变量（server 校验，防串扰）**：同一 room 在全部 p2p 隧道中最多出现于 **2 条记录且分属 2 个不同节点**（排除自身后计数，`ApplyTunnel`/`SyncFromClient` 落库前检查）。依据：p2punch 假设 room 内恰两端（MQTT 首个响应者即配对），第三个客户端持同 room 入场会与错误对端完成 ECDHE 建连——等于把流量隧穿给陌生节点。同节点重复记录同样拒绝。

**MVP 实现方式**：不经 admin 表单，用 curl 两次 POST `/api/v1/tunnels`（room 字段由创建侧脚本随机生成填入，server 只做格式校验）。「一键配对端点 + 自动 room 生成」列为后续增强（见遗留）。

---

## 关键设计

### build tag 与跨 tag 调用（hook 模式）

默认构建不含 P2P 代码，但 client 主流程（New/Close/notifyManagers/状态收集）需要调用 P2P 能力。采用**无 tag 接口 + 成对实现文件**：

```go
// p2p_hook.go —— 无 build tag
// p2pController 抽象 P2P 管理器；默认构建下为 nil，所有调用点 nil-safe。
type p2pController interface {
    Notify(tunnels []Tunnel)                                          // 自行解析 Para + IsEnabled()
    StatusByName(name string) (P2PRuntime, error)                     // 不存在返 ErrNotFound
    Close()
}

// P2PRuntime 无 tag（状态数据结构，供两处状态收集共用）
type P2PRuntime struct {
    Running   bool
    Connected bool
    BytesIn   uint64
    BytesOut  uint64
    Error     string
}
```

- `client_p2p.go`（`//go:build p2p`）：真实现，内部持有 `*p2p.Manager`；`newP2PController()` 返回真实例。
- `client_p2p_stub.go`（`//go:build !p2p`）：空实现 / `newP2PController()` 返回 nil。
- `TunnelTypeP2P` 常量与 `Validate()` 的 case / Target 豁免**不加 tag**（字符串常量零成本；默认构建可识别、存储、透传 p2p 配置，只是不运行）。
- fork 的 `internal/p2p/` 11 个子包 + `internal/proxy/p2p/` 全部 `//go:build p2p`；tag 行用脚本批量插入（每个 .go 顶部、package 前），并计入上游同步噪声（见「上游同步」）。
- server `internal/stun/` **不加 tag**，由 `stun.enabled` 配置门控（默认 false）。

### client 端 P2P 配置（internal/proxy/p2p/config.go，`//go:build p2p`）

```go
type P2PConfig struct {
    Room        string   `json:"room"`        // 8-32 字符 [a-zA-Z0-9_-]，FromPara 校验（p2punch 的 validateRoom 在 cmd 层，fork 不带，需自实现）
    Modes       []string `json:"modes"`       // 默认 DefaultModes：lan,tcp-v6,udp-v6,udp-v4（engine/dep.go:32）
    RelayServer string   `json:"relay_server,omitempty"` // v4-relay 预留
    MQTTBrokers []string `json:"mqtt_brokers"`  // 空 = 默认 tcp://<serverHost>:1883（serverHost 由 Manager 构造注入）
    STUNServers []string `json:"stun_servers"`  // 空 = p2punch 公共列表（国内优先）+ serverHost:3478 末尾
    Protocol    string   `json:"protocol"`      // "tcp" / "udp"，恒填
    LocalPort   int      `json:"local_port"`    // 仅发起端有意义：本端监听端口，必须 1-65535
    TargetHost  string   `json:"target_host"`   // 仅发起端填写；空 = 纯会话端
    TargetPort  int      `json:"target_port"`
}
```

**tunnel 不对称语义**（源码核实：`p2punch/internal/tunnel/tunnel.go:24-29,187-268` + `tcp.go:15,99-103`）：
- **发起端**（Para 带 `target_host`）：session 建立后调 `session.CreateTunnel(Params{Protocol, LocalPort, TargetHost, TargetPort})`——内部同步发 `TUNNEL:OPEN`、等对端 OK（10s 超时）、起本地监听。用户连 **发起端:LocalPort**，流量经 P2P 到对端，由**对端** dial `target_host:target_port`。
- **纯会话端**（`target_host` 为空）：只建 session 供对端 OPEN，自身不 CreateTunnel。OPEN 消息携带全部 Params，**target 由发起端指定**（可以是对端的 127.0.0.1）。
- 双向访问 = 两端都填 target（各自监听、各自指定对端目标），合法但 MVP 推荐单向。

**信任模型（必须写进运维认知）**：接受端 `AcceptRemote` **无 target allowlist**（tunnel.go:216-268 直接 dial OPEN 里的任意地址）——**完成配对即等于授权对端访问本机 loopback 与所在内网**。room 配对校验（见「room 与中心分发」）防第三方误入；接受端 allowlist 加固列为上游演进（见遗留）。

`FromPara` / `ToPara`；`Validate` 校验项与 server `validateP2PPara` 一致：room 格式、modes ⊂ `engine.AllModes`、protocol ∈ {tcp,udp} 恒校验；`target_host != ""` 时要求 `local_port` 1-65535 且 `target_port` 1-65535（纯会话端 `target_port` 必须为 0）。Enable 不入结构体，Manager 以「配置从 map 消失」为停机信号（同 ser2mq/ser2net/webssh 语义），`Notify` 内用 `t.IsEnabled()` 过滤。

### client 端 Manager / Handler（internal/proxy/p2p/，`//go:build p2p`）

Manager 签名取 **ser2mq 风格**（单 map 分发 + 单隧道操作；仓库 4 个现有 Manager 签名本就不统一，vpn 是双参特例，勿引为模板）：

```go
type Manager struct {                    // 独立 ctx，不挂 client smux session ctx
    ctx    context.Context
    cancel context.CancelFunc
    mu      sync.RWMutex
    configs  map[string]P2PConfig
    handlers map[string]*Handler
}
func NewManager(serverHost string) *Manager   // serverHost 用于派生默认 mqtt_brokers/stun_servers（对齐 ser2mq.NewManager(ctx, nodeID) 的注入模式）
func (m *Manager) Notify(tunnels []client.Tunnel)   // 对齐 p2pController 接口；增量启停；同名 Para 变更（JSON 不等）→ 重启 handler
func (m *Manager) Status(name string) (P2PRuntime, error)
func (m *Manager) Start(name string) error          // 幂等
func (m *Manager) Stop(name string) error           // 幂等
func (m *Manager) Close()                           // 关闭全部 handler + ctx

type Handler struct {
    cfg      P2PConfig
    sess     p2psession.Session   // interface（session/session.go:16）
    signal   SignalCredentials    // MQTT 凭据（阶段 6 注入；开发期为空=匿名）
}
func (h *Handler) Run(ctx context.Context) error   // 退避 5/10/20/40/60s（照抄 cmd/cli/connection.go:66-70）；session 建立后若 cfg.TargetHost != "" 则 CreateTunnel
func (h *Handler) Status() P2PRuntime              // Connected = sess 已建立；Bytes 取 sess.ListTunnels() 聚合（TunnelInfo 自带 BytesIn/Out，session.go:60-69）——无需自写 conn 计数包装
func (h *Handler) Close() error                    // 幂等：先 CloseTunnel 再 sess.Close()（secure 层级联关底层 socket，§3.2 第 6 条）
```

**Handler 必须吸收的 cmd 层 glue**（fork 不含，参考 `p2punch/cmd/cli/connection.go`）：
- engine 模式函数调用：`engine.Registry[modeName](ctx, deps)` 返回 `Outcome{Mux, IsClient, Local, Remote}`（定义在 session/consts.go:18，**组装在 engine/mode_*.go**、随 fork 获得；connection.go 仅消费，见 v2.2 第 22 条）
- secure 配置按传输分叉：UDP → `dtls + KcpWithUDP=true + KcpEncryption=false`；TCP → `tls13 + KeepAlive=30`；`KeyType="PSK"`、`Key=hex(sharedKey)`、`InsecureSkipVerify=true`（p2punch CLAUDE.md §3.2 第 2 条）
- yamux Client/Server 与 `info.IsClient` 配对（§3.2 第 3 条；role 来自 `easyp2p.SelectRole`，v6 双向 probe 用地址字典序）
- 打洞失败退避循环 + 连上后 3s 快速重连
- 注入 MQTT broker 列表（`engine.SetServers`）与凭据（依赖 p2punch 上游 PR，见阶段 6）

### server 端 STUN（moleAgent_Serv/internal/stun/，无 build tag）

```go
type Server struct { conn *net.UDPConn }
func NewServer(bindAddr string) (*Server, error)
func (s *Server) Close() error
func (s *Server) ListenAndServe() error // 阻塞
```

用 `pion/stun/v3` 实现 Binding Request → Binding Response。main.go 在 MQTT broker 初始化（main.go:240-251）同位置按 `cfg.Stun.Enabled` 门控启动。配置段：

```yaml
stun:
  enabled: false
  bind_addr: ":3478"
```

### server 端 Para 校验（internal/core/validate_para.go，新建）

```go
func validateP2PPara(para json.RawMessage) error
```

校验项：room 8-32 `[a-zA-Z0-9_-]`；modes ⊂ AllModes（lan/tcp-v6/udp-v6/udp-v4/tcp-v4/v4-relay，`engine/dep.go:36-39` 同款集合硬编码）；protocol ∈ {tcp,udp}——恒校验；`target_host != ""` 时 `local_port` 1-65535 且 `target_port` 1-65535，否则 `target_port` 必须为 0（纯会话端，与 client `Validate` 豁免一致）。

**配对校验**（跨记录，`TunnelConfigManager` 实现内、`ApplyTunnel`/`SyncFromClient` 落库前）：同一 room 排除自身后已存在于其他记录时，仅当恰好 1 条且节点不同则放行（组成一对），否则拒绝；同节点重复 room 拒绝。防「第三端持同 room 误配到陌生节点」（p2punch 假设 room 内恰两端，MQTT 首响即配对，ECDHE 对陌生对端照样成功）。

接入点：`internal/service/tunnel_service.go` `validateTunnel` switch 加 `case core.TunnelTypeP2P` → 调 `validateP2PPara(t.Para)`，不校验 Target。注意：现状 ser2mq/vpn 的 Para 在 server **零校验**（唯一有 Para 校验的是 webssh，在 `api/tunnel_handler.go:586`），p2p 比同类严格是有意为之（room 是密钥材料，格式必须把关）。`applyRuntimeTunnels` 零改动（只为 tcp/udp 起监听）。

### 信令鉴权（P2PSignalToken，最终方案）

**演进结论**（三次增订收敛，前置事实）：server authHook 对空用户名直接拒绝（broker.go:177-179），匿名连不上 :1883；p2punch 无凭据配置点；client→server REST 调用零先例；server AccessToken 明文仅 Create/Rotate 响应出现一次（sha256 落库），不可二次拉取。**用户凭据不能进 Para（admin 越权风险），方案 D 已废弃。**

**最终方案**：服务端派生 P2P 专用 token，经 **smux 控制通道**下发（不新增 REST）：

1. **p2punch 上游 PR（硬前置，独立仓库独立 review）**：`easyp2p` 的 `NewMQTTSignalSession` 增加可选 username/password 参数，缺省行为不变（匿名）。若上游暂未合并，允许临时改 fork 内部（最小改动），上游合并后从 fork 同步掉。
2. **server：新表 `p2p_signal_tokens`**（redka hash，同 access_tokens 结构：tokenID/secretHash/nodeID/tunnelName/expiresAt）。**不绑定 userID**——与用户体系彻底隔离。
3. **server：派生接口**。创建 P2P tunnel 时（`TunnelConfigManager` 实现，`tunnel_service.go`）按 nodeID+tunnelName 派生：随机 secret，sha256 落库，TTL 24h，未过期则幂等复用。**不写回 Para**。
4. **server：控制通道命令**。`internal/tunnel/control.go` `handleStream` switch（509-553，现处理 register/ping/sysinfo/tunnel_status/tunnel_update）加 C→S 命令 `p2p_signal_token`：校验连接已认证 + node 归属，返回 `{cmd:"p2p_signal_token", username:"p2p-signal:<tokenID>", password:<secret>, expires_at}`。
5. **server：authHook 分支**（broker.go:173-194）：username 前缀 `p2p-signal:` 走 P2P token 校验（sha256 比对 p2p_signal_tokens），通过后 **`cl.Properties.Username` 置为该哨兵字符串本身**（保持「Username 即身份载体」契约，GetClients/aclHook 语义不变；不注入 userID，与普通用户路径分叉）。
6. **server：aclHook 分支**（broker.go:210-236）：检测到 `p2p-signal:` 前缀 → **仅**放行 topic 前缀 `nat-exchange/`（read/write），其余拒绝；**该检查必须在 `rbac == nil` 兜底放行（222-224）之前**，否则 rbac 未配置时 P2P 客户端可订阅任意 topic。普通用户路径不变。
7. **client：拉取与注入**。P2P handler 启动 session 前，经现有已认证 smux 控制流发 `p2p_signal_token` 命令（复用 `readResponse()` 模式），凭据只存内存，过期/重启重拉；注入 paho Options（依赖上游 PR 的配置点）。topic 前缀 `nat-exchange/` 覆盖 p2punch 全部 5 种 salt 用途（address/sync/wait/v6/relay-keyx 均经 `TopicExchange` 拼接）。

**安全模型**：
- P2P token = 单一用途凭据，仅能读写 `nat-exchange/*`；即使泄露也不能登 admin（REST JWT 流程查 claims.UserID，`internal/auth/middleware.go:73-108`，与 broker 的 `cl.Properties` 载体互不相通）、不能触达其他用户的节点。
- E2E 加密（ECDHE P-256 + HKDF 派生 PSK → 派生 ECDSA 证书做 DTLS/TLS1.3 pinning）不依赖 broker 信任，broker 仅协议层中转。
- **配对信任模型**：接受端 `AcceptRemote` 无 allowlist，发起端可指定对端任意目标（含 127.0.0.1）——完成配对即互相授权访问对方 loopback/内网。信任边界 = room 保密性 + server 配对校验；对外只暴露「访问端:LocalPort」一个口。
- **既有旁路提示**（本次核查发现，设计需绕开而非依赖）：REST 的 `W-Access-Key` 全局密钥直通（authenticate() middleware.go:75-79）且绕过 RBAC（RequirePermission 的 `claims.UserID != "access_key"` 检查，middleware.go:55-65）；aclHook `rbac==nil` 全放行（broker.go:222-224，P2P 哨兵检查已置于其前）。

---

## 实施阶段

每步遵循 TDD：先写失败测试，再写实现，最后跑测试确认通过。

### 阶段 0: 类型常量与 Validate（两端 2 处，protocol 层零改动）

**0.1** client 失败测试：`tunnel_test.go` 断言 `TunnelTypeP2P = "p2p"`；`Validate()` 接受 p2p；**空 Target + p2p 不报错**（对齐 tunnel.go:56 的 vpn-manager 豁免写法）。
**0.2** client `tunnel.go`：加 `TunnelTypeP2P TunnelType = "p2p"` 常量（无 tag）；`Validate()` case 列表加 `TunnelTypeP2P`；Target 豁免加 p2p。
**0.3** server 失败测试：`internal/core/domain_test.go` 断言 `core.TunnelTypeP2P = "p2p"`。
**0.4** server `internal/core/domain.go`：加 `TunnelTypeP2P TunnelType = "p2p"`（TunnelType 常量现有 http/https/tcp/udp/webssh 五个；ser2mq 等是裸字符串 case，p2p 用常量即可，顺手收敛裸字符串不在本计划范围）。
**0.5** 跑两端 `go test ./...`。

### 阶段 1: 服务端内嵌 STUN

**1.1** 失败测试：`internal/stun/stun_test.go` 起 Server → 发 Binding Request → 收 Binding Response（回环 UDP）。
**1.2** 新建 `internal/stun/`（server.go + 测试），`pion/stun/v3` 实现，**无 build tag**。
**1.3** `internal/config/config.go` 加 `StunConfig{Enabled bool; BindAddr string}`（默认 `:3478`）；`configs/config.example.yaml` 加 `stun:` 段（enabled: false）。
**1.4** `cmd/moleagent-serv/main.go`：`cfg.Stun.Enabled` 时 `stun.NewServer` + goroutine（放 MQTT broker 初始化同区域，main.go:240-251 附近）。
**1.5** `go.mod` 加 `github.com/pion/stun/v3`；跑 `go test ./internal/stun/`。

### 阶段 2: 服务端 Para 校验 + 配对校验

**2.1** 失败测试：`internal/core/validate_para_test.go`（新建），表驱动：room 太短/太长/非法字符；modes 未知模式；protocol 非 tcp/udp；发起端（target_host 非空）local_port/target_port 越界；纯会话端（target_host 空）target_port 非 0。
**2.2** 新建 `internal/core/validate_para.go` 实现 `validateP2PPara`。
**2.3** `internal/service/tunnel_service.go` `validateTunnel` 加 `case core.TunnelTypeP2P` → `validateP2PPara(t.Para)`（不校验根级 Target）。
**2.4** 失败测试（service 层）：room 配对校验——合法成对（1+1 异节点）/ 第三条同 room 拒绝 / 同节点两条同 room 拒绝 / 更新自身不误判（排除自身计数）。
**2.5** 实现 `ApplyTunnel`/`SyncFromClient` 落库前的配对校验（扫描既有 p2p 隧道 Para 的 room）。
**2.6** 跑 `go test ./internal/core/ ./internal/service/`。

### 阶段 3: fork p2punch 子包（11 个）

**3.1** 新建 `moleAgent_client/internal/p2p/`，子包：`crypto, easyp2p, engine, heartbeat, misc, netutil, netx, secure, session, transport, tunnel`（**剔除 `update`——go-selfupdater 依赖 + 硬编码升级 URL；剔除 `version`**）。
**3.2** 从 `p2punch/internal/<子包>/` 复制全部 .go（含可离线测试，见阶段 4）。
**3.3** 脚本化改写：import path `s|p2punch/internal|moleAgent_client/internal/p2p|g`；每个 .go 顶部插 `//go:build p2p`。⚠️ 已有 build tag 的文件**不得覆盖、要合并条件**：`internal/netx/control_unix.go` 与 `internal/netx/udp_bridge_test.go` 现有 `//go:build !windows` → 合并为 `//go:build p2p && !windows`。sed 会命中注释里的 "p2punch/internal" 字样，review diff 时一并接受（注释无副作用）。fork 包名沿用原名（crypto/secure/session/tunnel/...），`internal/proxy/p2p` 引用时统一加别名（`p2psession`/`p2pengine`/`p2psecure`/`p2pcrypto`/`p2ptunnel`）避免与 stdlib crypto 及根包 `Tunnel` 混淆。
**3.4** client `go.mod` 加 `hashicorp/yamux`、`pion/dtls/v3`、`pion/stun/v3`（版本对齐 p2punch：v0.1.2 / v3.1.5 / v3.1.6）；`go mod tidy`。
**3.5** ⚠️ 工具链检查：p2punch `go 1.26`，client `go 1.25.3`——若 fork 代码用了 1.26 新 API，此处编译会暴露；先跑 `go build -tags p2p ./internal/p2p/...` 确认。
**3.6** 构建矩阵验证：`go build ./...`（默认，无 p2p）、`go build -tags p2p ./...`、`go vet -tags p2p ./...`。

### 阶段 4: fork 测试

**4.1** 复制可离线测试：secure / netx / session / engine（`net.Pipe` 系）+ crypto / heartbeat（纯函数离线测试，无 net.Pipe）+ easyp2p 离线子集（mqtt_signal mock、loopback STUN/组播）。同样处理 import path + tag。
**4.2** easyp2p 网络依赖测试（`TestExchangeRelayKey` 等，现状靠 `-short` skip）加 `//go:build p2p_integration` 排除；同步记录到交付说明（对齐 p2punch CLAUDE.md §4.1「公共 MQTT 测试失败已知非阻断」约定）。
**4.3** 跑 `go test -tags p2p ./internal/p2p/...` 验证离线子集通过。

### 阶段 5: proxy/p2p Manager + Handler

**5.1** 失败测试 `config_test.go`：FromPara 合法/非法 room/缺 target；ToPara 往返。
**5.2** 实现 `config.go`（含 room 校验——**自实现**，p2punch 的 validateRoom 在 cmd 层 fork 不带）。
**5.3** 失败测试 `manager_test.go`：Notify 增量启停（新加启/删除停/Enable=false 停/同名 Para 变更重启）；Status 不存在返 ErrNotFound；Start/Stop 幂等；Close 关全部。真实 p2p session 用接口 mock 注入。
**5.4** 实现 `manager.go`（`NewManager(serverHost string)`；变更检测用 Para JSON 比较，参照 vpn Manager 的重启惯例 `internal/proxy/vpn/manager.go:199-225`——v2.2 第 21 条纠错，勿引 client.go:200-225，那是 Run() 重连循环）。
**5.5** 失败测试 `handler_test.go`：Run 起 session；带 target 配置 CreateTunnel（mock engine/session）；纯会话端不 CreateTunnel；断开后按 5/10/20/40/60s 退避重连；Status 聚合 ListTunnels 字节；Close 取消。
**5.6** 实现 `handler.go`：吸收「关键设计·Handler 必须吸收的 cmd 层 glue」全部四项；MQTT 凭据经 `SignalCredentials` 注入点预留（阶段 6 激活，此前为空=匿名）。

### 阶段 6: 信令鉴权（P2P 走 server :1883 的前置；开发期可先跳过，用公共 broker 调试）

依赖顺序**不可并行**：

**6.1 p2punch 上游 PR**：`easyp2p.NewMQTTSignalSession` 加可选 MQTT username/password，缺省匿名行为不变。
**6.2 server**：`p2p_signal_tokens` 存储（redka hash + sha256 索引，照 `internal/storage/access_token_repo.go` 模式）+ 派生/校验函数（TTL 24h，未过期幂等复用；**删除对应 p2p tunnel 时吊销**）+ `TunnelConfigManager` 实现（tunnel_service.go）创建 p2p tunnel 时自动派生。测试覆盖派生幂等、过期、吊销、格式。
**6.3 server**：`internal/tunnel/control.go` `handleStream` 加 `p2p_signal_token` 命令。请求 `{"cmd":"p2p_signal_token","name":"<tunnel name>"}`，响应 `{"cmd":"p2p_signal_token","ok":true,"username":"p2p-signal:<tokenID>","password":"<secret>","expires_at":<unix>}`——两端 **ad-hoc JSON 各自解码**（与 pong 携带 ts 同款做法；协议层无共享 types 文件，**不要**为此扩 `ControlResponse` 结构）。校验：连接已认证；`name` 在该节点持久化配置中存在且 type==p2p（纵深防御，token 本身只授 nat-exchange/*）。**不写 Para**。测试加 `control_test.go`（已存在 444 行，追加）。
**6.4 server**：authHook（broker.go:173-194）**在 `VerifyMQTTCredentials` 之前**加 `p2p-signal:` 前缀分支——先查前缀，命中走 sha256 比对 `p2p_signal_tokens` 表、`cl.Properties.Username` 置哨兵字符串本身；未命中走原用户路径不变。aclHook（210-236）加哨兵分支：**置于 `rbac == nil` 兜底（222-224）之前**；topic 含 `+`/`#` 通配符一律拒绝（防 `nat-exchange/#` 全域订阅）；其余仅放行 `nat-exchange/` 前缀。测试覆盖：P2P token 可订阅 nat-exchange/* 精确 topic、拒通配、拒其他 topic、拒匿名、拒伪造前缀用户名、不影响普通用户路径、GetClients 显示正常。
**6.5 client**：`client.go` 加 `requestP2PSignalToken(name string)`（复用 `sendTunnelUpdate` 的 OpenStream→写→读一响应模式——sendTunnelUpdate 定义于 client.go:509、readResponse 调用于 533、定义于 1174）。**拉取策略**：仅当使用默认 server broker（`mqtt_brokers` 为空或等于派生默认值）时拉取——自定义/公共 broker 直接匿名，不依赖控制面；控制面未连接时 Handler 按既有退避等待重试。凭据只存内存，进程重启/过期重拉（broker 在连接时鉴权，长连接跨过期不受影响）。经 `SignalCredentials` 注入 paho Options（`-tags p2p`）。
**6.6** 两端全量测试。

### 阶段 7: client 接线（hook 模式）

**7.1** 失败测试（`//go:build p2p`）：`client_p2p.go` 提供 `newP2PController()` 非 nil；`!p2p` 构建下调用点 nil-safe。
**7.2** 新建无 tag 的 `p2p_hook.go`（`p2pController` 接口 + `P2PRuntime`）；`client_p2p.go`（p2p 真实现）/ `client_p2p_stub.go`（!p2p 空实现）。
**7.3** `client.go` 改动（全部经 hook，无 tag diff 以外零 P2P 符号）：
- `New()`（client.go:89）调 `newP2PController()`
- `Close()`（client.go:235）nil-safe 调 `c.p2p.Close()`
- `notifyManagers()`（client.go:365-427）加 `c.p2p.Notify(tunnels)`（p2p 的 Enable 过滤在 Notify 内部做，同 ser2mq 的 `cfg.Enable = t.IsEnabled()` 惯例）
- `collectTunnelStatuses()`（client.go:661）**和** `buildTunnelStatus()`（client.go:1246）都加 P2P 分支（后者漏改则本地 REST `/api/tunnels` 看不到 P2P）；单隧道端点走的 `TunnelStatusByName()`（client.go:1320）一并覆盖
- `dispatchStream()` **零改动**（P2P 不走 smux 数据面）
**7.4** 构建矩阵 + 全量测试：`go test ./...`、`go test -tags p2p ./...`。

### 阶段 8: room 配对分发工作流（文档 + curl 验证）

**8.1** 编写操作说明（落到本文件末尾「运维速查」）：server 侧随机生成 room（`openssl rand -base64 24 | tr -dc 'a-zA-Z0-9' | head -c 24`）→ curl POST 两条 Tunnel（node A/B，同 room 同 name；访问端带 `local_port`+`target_*`，纯会话端仅会话字段）→ `tunnel_push` 自动分发 → 两端 `-tags p2p` 构建的 client 建连。
**8.2** 集成验证（本机两进程）：两个 moleAgent_client（`-tags p2p`，不同 nodeID）+ moleAgent_Serv：
- server 日志确认信令走 :1883（或阶段 6 已完成时用 P2P token；开发期可指公共 broker）
- STUN 探测覆盖 server :3478
- 建连后两端 `tunnel_status` 上报 connected；本地 REST `/api/tunnels` 可见
**8.3** 全量回归：client `go test ./...` + `go test -tags p2p ./...` + `go vet -tags p2p ./...`；server `go test ./...`。

---

## 已知遗留（明确列出）

- **admin/mobile 前端 P2P 表单、desktop GUI**（后续单独 PR）
- **「一键配对」服务端端点 + 自动 room 生成**（MVP 用 curl，room 由创建侧脚本生成）
- **v4-relay 模式未启用**（server 不扮演 relay；`relay_server` 字段预留）
- **远程 tunnel_action**（p2p 不支持，与 http/tcp/udp 同类）
- **接受端 target allowlist 加固**（p2punch 原生 `AcceptRemote` 无 allowlist，配对即全权授权——上游演进项，见信任模型）
- **多 P2P tunnel 性能**：每 tunnel 独立 session（p2punch 原生设计），未做复用（MVP 接受）
- **P2P 公共 MQTT 集成测试不可达**（`p2p_integration` tag 排除；对齐 p2punch CLAUDE.md §4.1）
- **上游文档漂移**（引用 p2punch CLAUDE.md 时勿照抄）：「5 模式」实为 6；`AllModes/DefaultModes` 在 `engine/dep.go` 不在 registry.go；`TestE2E_V4Relay` 在 `cmd/relaysrv/` 不在 easyp2p
- **工作区 CLAUDE.md 滞后**：称「ApplyTunnel 会拒绝 ser2mq 等本地类型」——代码已不成立（tunnel_handler.go:205,237 显式接受），建议另行更新

---

## 关键文件清单

**client 修改**：
- `tunnel.go` — `TunnelTypeP2P` 常量 + `Validate()` case 与 Target 豁免（均无 tag）
- `client.go` — New/Close/notifyManagers/collectTunnelStatuses/buildTunnelStatus 接 hook（无 P2P 符号直引）
- `go.mod` — + yamux / pion/dtls/v3 / pion/stun/v3

**client 新增**：
- `internal/p2p/{crypto,easyp2p,engine,heartbeat,misc,netutil,netx,secure,session,transport,tunnel}/`（fork 11 包，全 `//go:build p2p`）
- `internal/proxy/p2p/{config,manager,handler}.go` + 测试（`//go:build p2p`）
- `p2p_hook.go`（无 tag）、`client_p2p.go`（p2p）/ `client_p2p_stub.go`（!p2p）

**server 修改**：
- `internal/core/domain.go` — `TunnelTypeP2P`
- `internal/service/tunnel_service.go` — `validateTunnel` P2P case；`TunnelConfigManager` 实现派生 P2P token
- `internal/tunnel/control.go` — `handleStream` 加 `p2p_signal_token`；authHook/aclHook 不在此文件（见下）
- `internal/mqtt/broker.go` — authHook（173-194）P2P 分支、aclHook（210-236）P2P marker 分支
- `cmd/moleagent-serv/main.go` — STUN 启动（配置门控）
- `internal/config/config.go` + `configs/config.example.yaml` — `stun:` 段
- `go.mod` — + pion/stun/v3

**server 新增**：
- `internal/stun/{server,stun_test}.go`（无 tag）
- `internal/core/{validate_para.go,validate_para_test.go}`
- `internal/storage/p2p_signal_token_repo.go` + `internal/auth/p2p_signal_token.go`（存储与校验，命名对齐现有 access_token 模式）

**参考（不改）**：
- `p2punch/internal/session/session.go` — Session 接口；`cmd/cli/connection.go` — Handler 编排与退避参考实现；`internal/engine/mode_*.go` — Outcome 组装（随 fork 获得）
- `p2punch/CLAUDE.md` §3.2 8 条硬约束（迁移时全部保留）
- `moleAgent_client/internal/proxy/ser2mq/manager.go` — Manager 签名模板
- `moleAgent_Serv/internal/storage/access_token_repo.go` — P2P token 存储模板
- `moleAgent_Serv/internal/mqtt/broker.go` + main.go:240-251 — STUN 初始化同款位置

---

## 决策记录（v1 多层增订收敛，结论保留）

- **smux vs yamux：故意不统一**。协议不兼容（smux 8 字节头 2016 自研 MIT / yamux 12 字节头 hashicorp MPL）；smux 替换 = 现网部署断裂；两者正交（smux 跑控制面到 server，yamux 跑 P2P 端到端）。yamux 库 < 50KB。
- **MQTT 鉴权演进**：方案 D（ACL bypass + 用户凭据入 Para）废弃——凭据类型不匹配 + admin 越权风险。变体 B+（REST 拉取）修正——client→server REST 零先例，改走 smux 控制通道。最终 P2PSignalToken + 哨兵 Username + nat-exchange/* ACL 限定（见「信令鉴权」节）。
- **fork 边界**：p2punch 是用户私人仓库可演进，但**集成时严格不动 fork 内部**（上游 PR 先行，fork 同步采用）；唯一例外——上游 PR 未合并期间允许 fork 内最小改动，合并后同步掉。
- **体积**：默认不编译后体积问题消解；`-tags p2p` 构建增量仍按 +3-5MB 预期（yamux/dtls/stun + pion 间接依赖），不进发布矩阵。
- **上游同步**：跟踪文档 `docs/plans/2026-09-13-p2p-upstream-sync.md`（新建）：记录 upstream commit hash ↔ fork commit、sed 与 tag 行噪声、累计落后数。因 import path 与 tag 行改动，无法纯文本 diff 校验，不做 pre-commit 强制。

## 运维速查（阶段 8.1 产物，已经本机三进程集成验证实测）

```bash
# 0. server 需开启 mqtt + stun（stun.enabled: true），防火墙放行 :3478/UDP；
#    client 必须 -tags p2p 构建，默认构建只透传配置不运行 P2P
go build -tags p2p -o moleagent-client ./cmd/moleagent-client

# 1. 生成 room（创建侧，勿入日志）
ROOM=$(openssl rand -base64 24 | tr -dc 'a-zA-Z0-9' | head -c 24)
# 2a. 访问端（node A）：带 local_port + target_*（target 是 B 侧要访问的地址，可为 B 的 127.0.0.1）
curl -X POST https://<serv>/api/v1/tunnels -H 'Authorization: Bearer <jwt>' -d '{
  "node_id":"<nodeA>", "name":"p2p-a-b", "type":"p2p", "enabled":true,
  "para":{"room":"'$ROOM'","modes":["lan","tcp-v6","udp-v6","udp-v4"],
          "protocol":"tcp","local_port":18080,
          "target_host":"127.0.0.1","target_port":8080}}'
# 2b. 纯会话端（node B）：仅 room 等会话字段（target_host 空、target_port 0）
curl -X POST https://<serv>/api/v1/tunnels -H 'Authorization: Bearer <jwt>' -d '{
  "node_id":"<nodeB>", "name":"p2p-a-b", "type":"p2p", "enabled":true,
  "para":{"room":"'$ROOM'","modes":["lan","tcp-v6","udp-v6","udp-v4"],"protocol":"tcp"}}'
# 3. tunnel_push 自动分发；访问侧用户连 nodeA:18080 → B 的 127.0.0.1:8080
# 4. 验证：client 本地 GET /api/tunnels（connected/bytes）；server 节点详情
#    client_statuses 可见 running+connected；server 日志出现
#    "MQTT p2p signal client authenticated"（信令走 :1883 且已过 P2PSignalToken 校验）
```

运维注意：
- server 的 `:3478/UDP` 需防火墙放行（STUN 兜底才可达）；`mqtt_brokers`/`stun_servers` 留空即默认指向 server（serverHost 从 client 配置的 ServerAddr 派生）。
- 生产启用 server broker 信令无需手工配置凭据：client 每次连接尝试前经控制通道拉取 P2PSignalToken（默认 broker 场景自动启用）；指向公共/自定义 broker 时自动匿名。
- 同 room 第三条记录会被 server 拒绝（错误信息中 room 打码），这是防串扰的预期行为。
- 已知环境限制：本机集成验证走 lan 模式命中（同子网），udp-v4 打洞 + STUN 兜底链路未在真实 NAT 环境实测（STUN 服务本身有回环单测覆盖）。
