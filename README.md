# moleAgent_client

内网穿透客户端，连接 moleAgent_Serv 服务端建立加密隧道，将外部请求转发到本地服务。支持 HTTP/TCP/UDP/ser2mq/vpn-manager 五种隧道类型，以及 TCP/WebSocket/KCP 三种传输协议。

## 程序架构

### 分层设计

```
┌─────────────────────────────────────────────────────────────┐
│                      CLI 入口层 (cmd/)                       │
│  命令行参数解析、配置加载、信号处理、子命令代理               │
├─────────────────────────────────────────────────────────────┤
│                    公共 API 层 (根包)                         │
│  client.go  config.go  tunnel.go  event.go  nodeid.go       │
│  对外暴露 Client 结构体和所有操作方法，协调各子系统           │
├──────────────┬──────────────────────┬────────────────────────┤
│   传输层      │      代理层          │    内置服务层           │
│  transport/  │      proxy/          │    builtin/            │
│  连接/认证/  │  http  tcp  relay   │  HTTP API + Web UI     │
│  smux 会话   │  ser2mq/  vpn/      │  静态文件嵌入           │
├──────────────┴──────────────────────┴────────────────────────┤
│                     协议层 (protocol/)                        │
│  与服务端通信的消息类型定义，前后端共享的 JSON 结构           │
└─────────────────────────────────────────────────────────────┘
```

### 包结构

```
.
├── client.go               Client 公共 API：New(), Run(), AddTunnel()...
├── config.go               Config 加载/验证/默认值/持久化
├── tunnel.go               TunnelType 常量、Tunnel 结构体、校验与转换
├── event.go                EventBus 同步事件总线
├── nodeid.go               节点 ID 生成与校验（硬件指纹 + 随机回退）
│
├── cmd/moleagent-client/
│   └── main.go             CLI 入口：参数解析、配置合并、信号处理
│
└── internal/               内部实现，不对外暴露
    ├── protocol/types.go   协议消息结构（RegisterReq, TunnelPush 等）
    │
    ├── transport/dialer.go 连接管理：拨号、认证、smux 多路复用会话
    │
    ├── proxy/
    │   ├── http.go         HTTP/WebSocket 反向代理
    │   ├── tcp.go          TCP/UDP 原始数据转发
    │   ├── relay.go        双向数据中继 + 流量统计
    │   ├── ser2mq/         串口转 MQTT 子系统
    │   │   ├── ser2mq.go   核心处理器：串口↔MQTT 双向转发
    │   │   ├── crypto.go   ChaCha20-Poly1305-X 加解密
    │   │   ├── mqtt.go     MQTT 客户端封装，每隧道独立连接
    │   │   ├── serial.go   串口抽象层，copy-and-release 模式
    │   │   ├── manager.go  多隧道生命周期管理
    │   │   └── stream.go   SSE 事件发布/订阅中心
    │   └── vpn/            VPN 程序管理子系统
    │       ├── config.go   配置结构 + BuildArgs() 动态参数构建
    │       ├── manager.go  多隧道生命周期管理，自动启停
    │       ├── process.go  进程管理：查找、启停、崩溃检测、日志采集
    │       └── vnt_client.go  vnt-cli REST API 客户端
    │
    └── builtin/
        ├── server.go       内置 HTTP 服务 + REST API 路由注册
        └── static/         前端资源（embed 嵌入）
            ├── index.html
            ├── style.css
            ├── api.js       REST 客户端封装
            ├── app.js       仪表盘/刷新逻辑
            ├── tunnels.js   Web + Stream 隧道管理
            ├── ser2mq.js    串口转 MQTT 管理面板
            └── vpn.js       VPN 进程管理面板
```

### 核心数据流

```
外部请求 → 服务端 → smux 流 → dispatchStream() 路由
                                ├── 控制推送 → processTunnelUpdates()
                                ├── TCP/UDP → HandleRawStream() → 本地目标
                                └── HTTP/WS → HandleHTTPStream() → 本地目标

ser2mq: 串口 ←read/write→ Ser2MQHandler ←publish/subscribe→ MQTT Broker
vpn:    Manager.OnTunnelUpdate() → ProcessMgr.Start() → 子进程 → Watchdog 监控
```

### 隧道配置与数据流

#### 存储原则：服务端为唯一权威源

客户端**不持久化隧道配置**。所有隧道配置存储在 moleAgent_Serv 服务端，客户端仅在连接状态下持有内存中的配置快照。断线重连后，客户端通过注册流程（`register` 命令）重新上报，服务端以 `tunnel_push` 回推最新配置。这保证了多客户端场景下的配置一致性。

#### 双向同步机制

```
┌──────────────────────────────────────────────────────────────┐
│                     moleAgent_Serv（权威源）                  │
│  持久化存储所有节点隧道配置，Web 管理后台 / REST API 操作     │
└─────────────┬────────────────────────────┬───────────────────┘
              │ tunnel_push（服务端推送）   │ tunnel_update（客户端上报）
              │ 管理后台修改配置时触发       │ 客户端本地 API 操作时触发
              ▼                            ▲
┌──────────────────────────────────────────────────────────────┐
│                    moleAgent_client（内存快照）                │
│  handlePossiblePush()        processTunnelUpdates()          │
│    ↓ 更新内存 tunnels[]        ↑ 序列化 mutation 队列        │
│    ↓ notifyManagers()          ↑ sendTunnelUpdate() 写服务端 │
│    ↓                                           ↑             │
│  ser2mq.Manager    vpn.Manager     AddTunnel() / RemoveTunnel()
└──────────────────────────────────────────────────────────────┘
```

**服务端→客户端（tunnel_push）**：

1. 管理员通过 Web 后台或 REST API 修改节点隧道配置
2. 服务端通过已建立的 smux 会话，主动推送 `tunnel_push` JSON 控制消息
3. 客户端 `dispatchStream()` 启发式检测首字节 `{`，识别为控制推送
4. `handlePossiblePush()` 解析 JSON、替换内存中的 `tunnels[]` 快照
5. `notifyManagers()` 将变更分发到 ser2mq/vpn 各 Manager
6. 各 Manager 比对新旧配置，执行增量启停操作

**客户端→服务端（tunnel_update）**：

1. 用户通过客户端本地 API（`POST /api/tunnels`）发起隧道增删
2. `AddTunnel()` / `RemoveTunnel()` 将变更请求封装为 `tunnelMutation`，投入 `tunReqs` 通道
3. `processTunnelUpdates()` goroutine 序列化消费请求队列，保证隧道状态变更的原子性
4. `applyTunnelMutation()` 在当前快照上执行增/删/替换操作
5. `sendTunnelUpdate()` 通过 smux 控制流将新隧道列表发送给服务端持久化
6. 服务端确认后，更新本地 `tunnels[]` 快照，`notifyManagers()` 通知各子系统

#### 序列化保证

所有隧道变更（无论来源是服务端推送还是本地操作）都经过序列化处理：

- 服务端推送：`handlePossiblePush()` 直接更新内存 + 通知 Manager
- 本地操作：`tunReqs` channel → `processTunnelUpdates()` 逐个消费 → 服务端确认后才更新内存
- 两者互不冲突，因为 smux 流的读取是单线程的（`acceptLoop`），而本地操作通过 channel 排队

#### Para 扩展字段的传递

`Tunnel.Para` 使用 `json.RawMessage` 延迟解析：

```
服务端存储（完整 JSON） → tunnel_push 序列化传输 → 客户端内存快照
                                                        ↓
                                          notifyManagers() 按类型分发
                                                        ↓
                                          ser2mq: json.Unmarshal → Ser2MQConfig
                                          vpn:    json.Unmarshal → vpn.Config
```

各隧道类型的 Manager 只关心自己的配置结构体，通过 `json.RawMessage` 实现按需反序列化，新增类型无需修改公共结构。

#### 程序启动时的配置加载与隧道同步

```
程序启动
  │
  ├── 加载配置：配置文件 / 命令行参数 → Config.Tunnels（可能为空）
  │
  ├── New(cfg)：创建 Client 实例，cfg.Tunnels 复制到内存 tunnels[]
  │   同时创建 ser2mq.Manager 和 vpn.Manager（尚无隧道配置）
  │
  ├── Run(ctx)：进入主循环
  │   │
  │   ├── Connect + Authenticate：建立传输连接，完成挑战-响应认证
  │   │
  │   ├── Register：
  │   │   发送 {cmd:"register", node_id, name, tunnels}
  │   │   服务端合并客户端上报的隧道列表与持久化存储
  │   │   返回 {cmd:"ok"}
  │   │
  │   ├── 启动 heartbeat goroutine（定时 ping/pong）
  │   ├── 启动 processTunnelUpdates goroutine（监听本地变更请求）
  │   │
  │   └── acceptLoop：接受服务端发来的 smux 流
  │       │
  │       ├── 首次 tunnel_push（服务端推送完整隧道列表）
  │       │   → handlePossiblePush() 替换内存 tunnels[]
  │       │   → notifyManagers() → 各 Manager.OnTunnelUpdate()
  │       │     - ser2mq.Manager：启动/停止串口↔MQTT 处理器
  │       │     - vpn.Manager：autostart=true 的隧道自动启动子进程
  │       │
  │       ├── 后续 tunnel_push（增量更新）
  │       │   → 同上流程，Manager 比对新旧配置执行增量操作
  │       │
  │       ├── TCP/UDP 数据流 → HandleRawStream() → 本地转发
  │       └── HTTP/WS 数据流 → HandleHTTPStream() → 本地转发
  │
  └── 断线重连：回到 Run 主循环顶部，重新 Connect → Register → acceptLoop
```

关键点：

- `Config.Tunnels` 可在配置文件或命令行中预定义隧道，但**首次 `tunnel_push` 会完全覆盖内存快照**
- 配置文件中的隧道列表主要用于首次注册时上报给服务端，服务端合并后下发权威配置
- 断线重连后，整个流程从头开始，Manager 的旧状态被清理，按新推送的配置重建
- ser2mq/vpn Manager 在 `OnTunnelUpdate()` 时执行增量操作：对比新旧配置 map，停止已删除的隧道、启动新增的隧道

#### 隧道增删改机制

**三种变更类型**（`tunnelMutationKind`）：

| 操作 | 触发方式 | mutation 类型 | 行为 |
|------|---------|---------------|------|
| 新增 | `POST /api/tunnels`（新名称） | `add` | 追加到列表尾部 |
| 修改 | `POST /api/tunnels`（同名替换） | `add` | 按 name 匹配替换整个 Tunnel 对象 |
| 删除 | `DELETE /api/tunnels/:name` | `remove` | 按 name 移除，不存在则报错 |

**变更处理流水线**（以客户端本地 API 为例）：

```
HTTP POST /api/tunnels → builtin/server.go 解析 JSON
  → c.AddTunnel(tunnel) → tunnel.Validate() 校验
    → c.requestTunnelMutation() 封装为 tunnelReq 投入 tunReqs 通道
      → processTunnelUpdates() 消费请求
        → applyTunnelMutation() 在快照上执行变更（add/remove/replace）
        → sendTunnelUpdate() 将新列表发送给服务端持久化
          → 失败：返回错误，内存不更新，Manager 不通知
          → 成功：更新内存 tunnels[]，触发后续流程
        → notifyManagers() 分发到 ser2mq/vpn Manager
        → EventBus.Emit(EventTunnelSynced)
      → req.resp <- err  回复 HTTP 请求
```

**enable/disable 切换**：通过 `POST /api/tunnels` 发送 `enabled: false` 的隧道配置实现。`applyTunnelMutation` 按名称匹配替换整个 Tunnel 对象（保留 name、target 等字段不变），`notifyManagers()` 中被禁用的隧道不会进入 Manager 的配置 map，Manager 在对比时检测到"消失"的隧道并停止之。

**服务端发起的变更**：管理后台操作 → 服务端修改数据库 → 服务端主动 `tunnel_push` → 客户端 `handlePossiblePush()` 直接替换整个 `tunnels[]` → `notifyManagers()`，无需经过 `tunReqs` 队列。

#### Para 扩展字段的传递

`Tunnel.Para` 使用 `json.RawMessage` 延迟解析：

```
服务端存储（完整 JSON） → tunnel_push 序列化传输 → 客户端内存快照
                                                        ↓
                                          notifyManagers() 按类型分发
                                                        ↓
                                          ser2mq: json.Unmarshal → Ser2MQConfig
                                          vpn:    json.Unmarshal → vpn.Config
```

各隧道类型的 Manager 只关心自己的配置结构体，通过 `json.RawMessage` 实现按需反序列化，新增类型无需修改公共结构。

#### 注册流程

客户端每次连接服务端后执行注册：

1. 发送 `register` 控制命令（含 node_id、name、当前 tunnels 快照）
2. 服务端校验 token，将客户端上报的隧道配置与持久化存储合并
3. 服务端返回 `ok`，后续如果有配置差异则主动 `tunnel_push` 同步

## 功能模块详解

### 1. 传输层 (transport)

负责与服务端建立和维护底层连接。

**连接流程**：TCP/TLS 或 WebSocket 或 KCP 拨号 → 32 字节挑战-响应认证（token SHA256）→ smux 多路复用会话。

**smux 会话**：单 TCP 连接上承载多个逻辑流（stream），无需为每个隧道建立独立连接。关键参数：
- 会话缓冲 32MB，每流 4MB
- 30 秒 keepalive 心跳
- 支持 TCP、WebSocket（`wss://`）、KCP（UDP + FEC 纠错）三种传输

**自动重连**：连接断开后指数退避重连（2s → 30s 上限），重连后自动重新认证和注册隧道。

### 2. 代理层 (proxy)

处理到达的 smux 流，按隧道类型分发。

#### HTTP 代理 (http.go)

HTTP/WebSocket 反向代理。路由匹配优先级：
1. 路径前缀精确匹配
2. 虚拟主机约定：`<name>-<nodeId>.<domain>` 或 `<name>.<nodeId>.<domain>`
3. 兜底到第一个可用隧道

WebSocket 升级自动检测（`Upgrade: websocket` 头），升级后切换为双向中继。转发时添加 `X-Forwarded-For`、`X-Real-IP` 等 nginx 风格头。

#### TCP/UDP 代理 (tcp.go)

原始数据透传。服务端通过协议头 `\x00<tunnel-name>\n` 标识目标隧道，客户端解析后在本地建立 TCP/UDP 连接，双向 1MB 缓冲区 copy。

#### 流量统计 (relay.go)

全局和每隧道粒度的 `bytes_in`/`bytes_out` 计数器，供 API 和状态查询使用。

### 3. ser2mq 子系统

串口数据与 MQTT 消息的双向桥接，使用 ChaCha20-Poly1305-X 认证加密。

**架构**：
- `Manager`：管理多个 `Ser2MQHandler` 实例，响应隧道配置变更
- `Ser2MQHandler`：单个隧道的串口↔MQTT 双向转发
- 每隧道独立的 MQTT 连接和串口连接

**数据流**：
- 串口→MQTT：`runSerialToMQTT` goroutine 读取串口 → JSON 封装（`MQTTMessage`，base64 data） → ChaCha20-Poly1305-X 加密 → MQTT publish
- MQTT→串口：`runMQTTToSerial` goroutine 订阅 MQTT topic → 解密 → JSON 解析 → 写入串口

**加密格式**：`0x01`（版本） + 24 字节 nonce + 密文 + 16 字节 Tag。密钥为 32 字节（64 字符 hex）。

**MQTT Topic 约定**：`/mole/<nodeId>/serial/<port>/out`（串口→MQTT）、`/mole/<nodeId>/serial/<port>/in`（MQTT→串口），兼容 mole-cgui。

**串口访问**：copy-and-release 模式，不持锁跨 I/O 操作，避免死锁。`SanitizePortName()` 统一处理 `/dev/` 和 `\\.\` 前缀。

**实时数据流**：`StreamHub` pub/sub 中心，支持 SSE 推送报文摘要（hex 预览 + 长度 + 方向），带尾部队列回放历史。

### 4. VPN 管理子系统

管理外部 VPN 程序（如 vnt-cli、easytier）的完整生命周期。

**配置**：
- `binary`：程序名和路径，查找顺序 `./vnet/` → `$PATH`
- `args`：静态启动参数
- `vnt`：结构化 VNT 配置，`BuildArgs()` 动态构建 vnt-cli 命令行参数（优先于静态 args）
- `lifecycle`：autostart、崩溃自动重启（max_restarts + restart_delay）
- `watchdog`：定时健康探测 + 优雅退出宽限
- `log`：stdout/stderr 采集，环形缓冲区（max_size 上限）

**进程管理**：`ProcessMgr` 封装完整生命周期：查找二进制 → 启动子进程 → SIGTERM 优雅停止 → 宽限期后 SIGKILL → 崩溃检测 → 自动重启（延迟 + 计数限制）→ 日志采集。

**VNT 集成**：`VNTClient` 封装 vnt-cli REST API（/info、/list、/status、/route、/chart），提供对等节点列表、路由表、流量图表等运行时数据。

### 5. 事件系统 (event.go)

同步事件总线 `EventBus`，解耦各子系统。支持按类型注册和通配注册（空字符串 = 全部事件）。

事件类型：连接/断开/重连、认证/注册、心跳成功/失败、隧道更新/同步、VPN 启停/崩溃/重启、ser2mq 连接/断开/错误。

### 6. 节点 ID 生成 (nodeid.go)

确定性 ID 生成策略：
1. 优先硬件指纹：CPU VendorID + ModelName → SHA256 前 8 字符，确保同一设备生成相同 ID
2. 回退随机生成：`crypto/rand` 生成 8 字符（首字符字母，其余字母或数字）

### 7. 内置 HTTP 服务 (builtin)

提供 REST API 和 Web 管理界面，所有前端资源通过 `go:embed` 编译嵌入。

API 覆盖：隧道 CRUD、VPN 启停/日志/对等节点/路由/图表、ser2mq SSE 实时数据流、全局状态、版本信息。前端为 ES Module 模块化架构，按功能拆分为独立 JS 模块。

### 8. CLI 入口 (cmd/moleagent-client)

支持两种运行模式：
- **守护模式**：`moleagent-client -server ... -token ...` 启动长连接客户端
- **管理子命令**：`moleagent-client -tunnels --list/--add/--del` 通过内置 HTTP API 管理隧道

命令行参数可覆盖配置文件，支持 `-config` JSON 文件 + `-server/-token/-id` 等命令行标志。

## 快速开始

```bash
# 默认 TCP 传输
./moleagent-client -server 82.157.196.219:9981 -token your-token

# WebSocket 传输（穿透 HTTP 代理/CDN）
./moleagent-client -server 82.157.196.219:9981 -token your-token -transport ws

# KCP 传输（高延迟/弱网环境）
./moleagent-client -server 82.157.196.219:9981 -token your-token -transport kcp
```

### 传输协议

| 协议 | `-transport` 值 | 加密 | 适用场景 |
|------|-----------------|------|---------|
| TCP | `tcp` | TLS（`-tls` 标志） | 默认，稳定可靠 |
| WebSocket | `ws` | TLS → wss（`-tls` 标志） | 穿透 HTTP 代理/防火墙 |
| KCP (UDP) | `kcp` | AES-256（`kcp.key`） | 高延迟/弱网，FEC 纠错 |

> **注意**：KCP 不支持 TLS。加密通过 `kcp.key` 配置。

### 配置文件

```json
{
  "server_addr": "82.157.196.219:9981",
  "token": "your-token",
  "transport": "tcp",
  "tls": false,
  "tunnels": [],
  "kcp": {
    "key": "",
    "data_shards": 10,
    "parity_shards": 3,
    "nodelay": 1,
    "interval": 10,
    "resend": 2,
    "no_congestion": 1,
    "send_window": 0,
    "recv_window": 0
  }
}
```

```bash
./moleagent-client -config client.json
```

## ser2mq 配置示例

```json
{
  "name": "plc-serial",
  "type": "ser2mq",
  "target": "/dev/ttyS1",
  "enabled": true,
  "para": {
    "enable": true,
    "broker": "mqtt://user:pass@broker:1883",
    "serial": {"port": "/dev/ttyS1", "baudrate": 9600, "databits": 8, "stopbits": 1.0, "parity": "N", "timeout": 3000},
    "secret": "64字符hex密钥（32字节）",
    "qos": 1
  }
}
```

MQTT 主题：`/mole/<nodeid>/serial/<port>/out`（串口→MQTT）、`/mole/<nodeid>/serial/<port>/in`（MQTT→串口）

加密格式：`0x01` + 24字节 nonce + 密文+Tag（ChaCha20-Poly1305-X）

## vpn-manager 配置示例

```json
{
  "name": "my-vpn",
  "type": "vpn-manager",
  "target": "easytier",
  "enabled": true,
  "para": {
    "binary": {"name": "vnt-cli"},
    "vnt": {
      "token": "vpn-token",
      "server": "wss://vpn-server:1234",
      "device_id": "my-device",
      "name": "vpn-name",
      "password": "vpn-pass",
      "ip": "10.26.0.2"
    },
    "lifecycle": {"autostart": false, "restart_on_crash": true, "max_restarts": 3, "restart_delay": 5},
    "watchdog": {"enabled": true, "interval": 10, "quit_grace": 10},
    "log": {"capture": true, "max_size": 65536}
  }
}
```

当 `vnt.enabled` 为 true 时，`BuildArgs()` 将结构化 VNT 配置转为 vnt-cli 命令行参数（如 `--token`、`--server`、`--id` 等），无需手写 args 数组。否则使用静态 `args` 字段。

程序查找：`./vnet/` → `$PATH`

## 统一 API

内置 HTTP 服务（默认 `127.0.0.1:18080`），所有隧道类型共享统一路由：

### 隧道管理

| 方法 | 端点 | 说明 |
|------|------|------|
| `GET` | `/api/tunnels` | 所有隧道（含运行时状态） |
| `POST` | `/api/tunnels` | 添加/更新隧道 |
| `GET` | `/api/tunnels/:name` | 单隧道详情 |
| `DELETE` | `/api/tunnels/:name` | 删除隧道 |

### 类型特定操作

| 方法 | 端点 | 适用类型 | 说明 |
|------|------|----------|------|
| `POST` | `/api/tunnels/:name/start` | vpn-manager | 启动进程 |
| `POST` | `/api/tunnels/:name/stop` | vpn-manager | 停止进程 |
| `GET` | `/api/tunnels/:name/logs` | vpn-manager | 崩溃日志 |
| `GET` | `/api/tunnels/:name/peers` | vpn-manager | VNT 对等节点列表 |
| `GET` | `/api/tunnels/:name/routes` | vpn-manager | VNT 路由表 |
| `GET` | `/api/tunnels/:name/chart` | vpn-manager | VNT 流量图表数据 |
| `GET` | `/api/tunnels/:name/stream` | ser2mq | SSE 实时数据流 |

### 实时数据流（SSE）

`GET /api/tunnels/:name/stream` 仅适用于 ser2mq 隧道：

- 返回 `text/event-stream`
- 支持 `?tail=N` 参数（默认 20，最大 200），回放历史事件
- 事件类型 `packet`，数据为 JSON PacketEvent
- 关闭连接即取消订阅

### 全局状态

| 方法 | 端点 | 说明 |
|------|------|------|
| `GET` | `/api/status` | 客户端连接状态、全局流量统计 |
| `GET` | `/api/version` | 版本号、编译日期、CPU/内存使用 |

### 统一隧道状态响应

`GET /api/tunnels` 返回所有隧道，每条包含配置 + 运行时状态：

```json
{
  "name": "serial-1",
  "type": "ser2mq",
  "target": "mqtt://broker:1883",
  "enabled": true,
  "connected": true,
  "bytes_in": 4096,
  "bytes_out": 8192,
  "status": {
    "running": true,
    "broker": "mqtt://broker:1883",
    "serial_port": "/dev/ttyUSB0",
    "bytes_in": 4096,
    "bytes_out": 8192
  }
}
```

- `connected`: http/tcp/udp = 客户端已连接服务端；ser2mq = 隧道运行中；vpn = 进程运行中
- `status`: 类型特定的运行时详情，http/tcp/udp 为 `null`

## Web 管理界面

访问 `/ui` 可打开内置管理界面（ES Module 模块化前端）：

- 实时连接状态与流量监控
- 统一隧道列表，支持所有类型
- ser2mq 隧道详情面板（串口/MQTT 状态 + 实时数据流）
- VPN 管理面板（启停/崩溃日志/对等节点/路由）

## 扩展性设计

### 可扩展点

| 扩展点 | 位置 | 方式 |
|--------|------|------|
| 新增隧道类型 | `tunnel.go` `TunnelType` 常量 + `Validate()` | 注册新类型常量，在 `dispatchStream()` 中添加分发逻辑 |
| 新增传输协议 | `transport/dialer.go` `DialFunc` | 实现 `DialFunc` 签名，在 `Config.Transport` 中注册 |
| 新增隧道管理器 | `client.go` `notifyManagers()` | 新建 `internal/proxy/<type>/manager.go`，实现 `OnTunnelUpdate()` 接口 |
| 自定义事件 | `event.go` `EventType` 常量 | 添加新常量，通过 `EventBus.On()` 注册处理器 |
| API 端点扩展 | `builtin/server.go` | 在 `registerTunnelAPI` 中注册新路由 |
| Para 扩展字段 | `tunnel.go` `Para json.RawMessage` | `json.RawMessage` 延迟解析，不同隧道类型按需反序列化 |

### 低耦合机制

1. **EventBus 解耦**：子系统通过事件总线通信，无直接依赖。VPN 崩溃、ser2mq 断连等事件由各子系统发出，上层按需订阅。

2. **Manager 模式**：ser2mq 和 vpn 各自拥有独立的 `Manager`，通过 `OnTunnelUpdate()` 回调接收配置变更。新增隧道类型只需新增 Manager，无需修改 Client 核心。

3. **`json.RawMessage` 延迟解析**：`Tunnel.Para` 使用原始 JSON 字节存储，由各类型 Manager 按需反序列化为各自的配置结构体，避免公共类型依赖。

4. **`DialFunc` 传输抽象**：传输层通过函数类型抽象，TCP/WebSocket/KCP 三种传输互不依赖，新增传输只需实现一个拨号函数。

5. **StreamHub pub/sub**：ser2mq 的实时数据通过发布-订阅中心分发，UI 层和处理器层完全解耦。

### 新增隧道类型步骤

以添加 `rtsp` 类型为例：

1. `tunnel.go`：添加 `TunnelTypeRTSP TunnelType = "rtsp"` 常量和校验规则
2. `internal/proxy/rtsp/`：新建包，实现 `Manager`（含 `OnTunnelUpdate()`）和具体处理器
3. `client.go`：在 `notifyManagers()` 中添加 `case TunnelTypeRTSP` 分支
4. `client.go`：在 `dispatchStream()` 中添加流分发逻辑（如需要）
5. `builtin/server.go`：注册类型特定的 API 端点（如需要）

无需修改 Event 系统、传输层或其他隧道类型的代码。

## 作为第三方库集成

**适合**：本仓库的根包（`moleAgent_client`）设计了清晰的公共 API，可直接作为 Go 库集成：

```go
import moleAgent_client "moleAgent_client"

cfg := moleAgent_client.DefaultConfig()
cfg.ServerAddr = "server:9981"
cfg.Token = "my-token"

client, _ := moleAgent_client.New(cfg)

// 订阅事件
client.OnEvent("", func(ev moleAgent_client.Event) {
    log.Println(ev.Type, ev.Data)
})

// 启动（非阻塞）
go client.Run(ctx)

// 动态管理隧道
client.AddTunnel(moleAgent_client.Tunnel{
    Name: "web", Type: moleAgent_client.TunnelTypeHTTP,
    Target: "http://127.0.0.1:8080", Enabled: true,
})

// 查询状态
status := client.AllTunnelStatus()
stats := client.Stats()
```

**注意事项**：
- `internal/` 包不对外暴露，集成通过根包 API 操作
- 前端静态资源通过 `go:embed` 编译嵌入，无需额外文件
- 依赖串口库（`go.bug.st/serial`）和 gopsutil，在无串口或无 CPU 信息的环境可能需要条件编译
- 未发布 Go module（`module moleAgent_client` 为本地路径），正式集成前需发布到 Git 仓库并设置 Go module 路径

## 构建

```bash
# 当前平台（自动从 git tag 注入版本号）
make build

# 交叉编译所有平台
make release
```

版本号通过 `go build -ldflags` 注入，由 Makefile 自动从 git tag 获取：

- `Version` = git tag（如 `v0.1.0`）
- `GitHash` = git commit hash
- `BuildDate` = 编译时间（UTC）

Windows PowerShell：

```powershell
.\scripts\build.ps1
.\scripts\release.ps1
```

## 致谢

本仓库使用以下第三方开源库：

| 库 | 用途 |
|----|------|
| [xtaci/smux](https://github.com/xtaci/smux) v1.5.57 | TCP 多路复用，在单连接上承载多个逻辑流 |
| [go.bug.st/serial](https://github.com/bugst/go-serial) v1.6.4 | 跨平台串口通信 |
| [eclipse/paho.mqtt.golang](https://github.com/eclipse/paho.mqtt.golang) v1.5.1 | MQTT v3.1.1 客户端 |
| [golang.org/x/crypto](https://golang.org/x/crypto) v0.45.0 | ChaCha20-Poly1305-X 认证加密 |
| [xtaci/kcp-go](https://github.com/xtaci/kcp-go) v5.6.72 | KCP（UDP）可靠传输 + FEC 纠错 |
| [shirou/gopsutil](https://github.com/shirou/gopsutil) v3.24.5 | 系统信息采集（CPU、内存、运行时指标） |
| [gorilla/websocket](https://github.com/gorilla/websocket) v1.5.3 | WebSocket 协议（smux ws 传输依赖） |
| [klauspost/reedsolomon](https://github.com/klauspost/reedsolomon) v1.12.0 | Reed-Solomon FEC（KCP 传输依赖） |
| [tjfoc/gmsm](https://github.com/tjfoc/gmsm) v1.4.1 | 国密算法库（KCP 加密依赖） |
