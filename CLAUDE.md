# moleAgent_client

内网穿透客户端，连接 moleAgent_Serv 服务端建立加密隧道，将外部请求转发到本地服务。

## 技术栈

Go 1.25、smux、go.bug.st/serial、paho.mqtt.golang、golang.org/x/crypto

## 包结构

```
client.go                    公共 API：Client, New(), Run(), AddTunnel(), AllTunnelStatus()...
config.go                    Config 加载/验证/默认值
tunnel.go                    TunnelType 常量、Tunnel 结构体、校验与转换
event.go                     EventBus 同步事件总线
nodeid.go                    节点 ID 生成（硬件指纹 + 随机回退）
p2p_hook.go                  p2pController 接口 + P2PRuntime 状态结构（无 tag）
client_p2p.go                //go:build p2p      真实现（Para 解析过滤 + 信令凭据拉取）
client_p2p_stub.go           //go:build !p2p    空实现（默认构建零 P2P 代码）
internal/
  protocol/types.go          协议层类型（ControlCmd, Tunnel 等）
  transport/dialer.go        连接/认证/smux 会话管理
  p2p/                       fork 自 p2punch 的上游代码（easyp2p/engine/session/tunnel）——禁止修改
  proxy/http.go              HTTP + WebSocket 代理
  proxy/tcp.go               TCP/UDP 原始转发
  proxy/relay.go             双向数据转发 + 流量统计
  proxy/ser2mq/
    ser2mq.go                核心处理器：串口↔MQTT 双向转发
    crypto.go                ChaCha20-Poly1305-X 加解密
    mqtt.go                  MQTT 客户端封装（每隧道独立连接）
    serial.go                串口抽象层（copy-and-release）
    manager.go               多隧道生命周期管理
    stream.go                SSE 事件发布/订阅中心
  proxy/ser2net/
    handler.go               核心处理器：串口↔TCP/UDP（Server/Client 双模式，本地透传）
    tcp.go / udp.go          TCP/UDP 收发
    manager.go               多隧道生命周期管理
  proxy/vpn/
    config.go                配置结构 + BuildArgs() 动态参数构建
    manager.go               多隧道生命周期管理
    process.go               进程管理：启停、崩溃检测、日志采集
    vnt_client.go            vnt-cli REST API 客户端
  proxy/webssh/
    handler.go               SSH 连接/会话桥接 + TOFU 主机密钥
    handler_sftp.go          SFTP 文件操作（列表/上传/下载/删除/读取）
    manager.go               多隧道生命周期管理
  proxy/p2p/                 （//go:build p2p）P2P 打洞直连
    config.go                P2PConfig 两层结构（连接参数 + mappings[]）+ §0.3 校验
    handler.go               连接编排：mode 链逐个尝试 → session → 按 mappings 逐条 CreateTunnel
    manager.go               多隧道生命周期管理 + local_port 跨隧道唯一性
  builtin/
    server.go                内置 HTTP 服务 + REST API
    static/                  前端（ES Module 模块化，go:embed 嵌入）
```

## 隧道类型

| 类型 | 说明 |
|------|------|
| `http` | HTTP/WebSocket 代理 |
| `https` | HTTPS 代理（转发到 HTTPS 后端） |
| `tcp` | TCP 透明转发 |
| `udp` | UDP 透明转发 |
| `ser2mq` | 串口 ↔ MQTT（ChaCha20-Poly1305-X 加密） |
| `ser2tcp` | 串口 ↔ TCP（Server/Client 双模式，本地透传，不参与路由） |
| `ser2udp` | 串口 ↔ UDP（Server/Client 双模式，本地透传，不参与路由） |
| `vpn-manager` | VPN 程序启停监视 |
| `webssh` | WebSSH 远程终端 + SFTP（TOFU 主机密钥） |
| `p2p` | P2P 打洞直连（`-tags p2p` 构建；连接参数 + 端口映射两层，同 room 配对，映射仅访问发起端配置） |

## 统一 API

所有隧道类型共享 `/api/tunnels` 路由：

| 方法 | 端点 | 说明 |
|------|------|------|
| GET | `/api/tunnels` | 所有隧道（配置+运行时状态） |
| POST | `/api/tunnels` | 添加/更新隧道 |
| GET | `/api/tunnels/:name` | 单隧道详情 |
| DELETE | `/api/tunnels/:name` | 删除隧道 |
| POST | `/api/tunnels/:name/start` | 启动（vpn-manager） |
| POST | `/api/tunnels/:name/stop` | 停止（vpn-manager） |
| GET | `/api/tunnels/:name/logs` | 崩溃日志（vpn-manager） |
| GET | `/api/tunnels/:name/peers` | VNT 对等节点列表 |
| GET | `/api/tunnels/:name/routes` | VNT 路由表 |
| GET | `/api/tunnels/:name/chart` | VNT 流量图表 |
| GET | `/api/tunnels/:name/stream` | SSE 实时数据流（ser2mq） |
| GET | `/api/status` | 客户端全局状态 |
| GET | `/api/version` | 版本与系统信息（含 `p2p` 构建能力标志） |
| GET | `/api/check-update` | 检测新版本 |
| POST | `/api/self-update` | 自动升级并重启 |

`TunnelStatus` 合并配置 + 运行时状态（connected、bytes_in/out、类型特定 status）。
webssh 隧道无专属 REST 端点：运行时状态（sessions、bytes、last_rx/tx）合并进 `GET /api/tunnels`，终端与文件数据走 smux 流（dispatchStream 的 `0x01` 协议头）。
服务端可通过 `tunnel_action` 命令远程对 vpn-manager/ser2mq/ser2tcp/ser2udp 执行 start/stop/restart（http/tcp/udp/webssh 不支持远程 action）。

## 控制协议命令

客户端 ↔ 服务端通过 smux 流交换 JSON 控制命令（`protocol.ControlCmd.Cmd`）：

| 方向 | 命令 | 说明 |
|------|------|------|
| C→S | `register` | 注册节点（携带 tunnels + sysinfo） |
| C→S | `ping` / S→C `pong` | 心跳，携带 ts 计算 RTT |
| C→S | `sysinfo` | 周期上报系统信息（每 5 次心跳） |
| C→S | `tunnel_status` | 周期上报各隧道运行时状态（每 2 次心跳） |
| C→S | `tunnel_update` | 本地变更写服务端持久化 |
| S→C | `tunnel_push` | 全量替换客户端 `tunnels[]` |
| S→C | `tunnel_action` | 远程 start/stop/restart（vpn-manager/ser2mq/ser2tcp/ser2udp） |
| S→C | `restart` | 远程重启（CAS 防重复，delay 上限 300s，仅标记由 supervisor 拉起） |
| C→S | `p2p_signal_token` | p2p 信令 MQTT 凭据请求（仅 -tags p2p；平铺 JSON 响应，凭据只存内存） |

响应统一经 `readResponse()` 读取（单次读，仅适用于简短 ack）。

## 隧道配置数据流（⚠️ 重要）

**服务端是唯一权威源，客户端不持久化隧道配置。** 客户端仅持有内存快照。

### 双向同步路径

- **服务端→客户端**：管理后台修改 → 服务端 `tunnel_push` JSON → `dispatchStream()` 首字节 `{` 路由到 `handleServerCmd()` → 直接替换 `tunnels[]` → `notifyManagers()`
- **客户端→服务端**：本地 API 操作 → `AddTunnel()`/`RemoveTunnel()` → `tunReqs` channel → `processTunnelUpdates()` 序列化处理 → `sendTunnelUpdate()` 发服务端持久化 → 成功后更新内存 → `notifyManagers()`

### 变更处理流水线

```
applyTunnelMutation() 在快照上执行增/删/替换
  → sendTunnelUpdate() 写服务端
    → 失败：内存不更新，Manager 不通知，返回错误
    → 成功：更新内存 tunnels[]
      → notifyManagers() 分发到 ser2mq/ser2net/vpn/webssh Manager
        → Manager.OnTunnelUpdate() 比对新旧 map，增量启停
```

**三种 mutation**：`add`（新增或同名替换）、`remove`（按名称删除）、`replace_all`（全量替换）。

### notifyManagers 机制

遍历 tunnels 列表，按类型提取配置：
- ser2mq：`json.Unmarshal(Para)` → `Ser2MQConfig` → `ser2mqMgr.OnTunnelUpdate(configs map)`
- ser2tcp/ser2udp：`json.Unmarshal(Para)` → `ser2net.Ser2NetConfig` → `ser2netMgr.OnTunnelUpdate(configs map)`
- vpn-manager：`json.Unmarshal(Para)` → `vpn.Config` → `vpnMgr.OnTunnelUpdate(names, configs map)`
- webssh：`json.Unmarshal(Para)` → `WebSSHConfig` → `websshMgr.OnTunnelUpdate(configs map)`
- p2p：经 `p2pController.Notify` 过滤（TunnelTypeP2P + IsEnabled + Para 合法）→ `p2pMgr.OnTunnelUpdate(configs map)`；仅 -tags p2p 构建，默认构建 controller 为 nil，p2p 隧道**静默不启动**
- disabled 的隧道不进入 configs map，Manager 检测到"消失"会停止对应处理器/进程

### ⚠️ 易出错点

1. **`applyTunnelMutation` 替换整个 Tunnel 对象**：toggle enable/disable 时必须保留 name、target、domain、listen_port、Para 等所有字段，不能只传 enabled 字段
2. **Para 的 enable 字段可能缺失**：服务端存储的 Para 可能不含 `enable` 字段，Go `json.Unmarshal` 默认 bool 为 false。`notifyManagers()` 中必须用 `cfg.Enable = t.IsEnabled()` 同步，`IsEnabled()` 将 nil Para 视为 true
3. **前端 buildPara 必须包含所有字段**：编辑 ser2mq/vpn 隧道时，即使某些字段未在表单中显示，也要原样传回（如 secret、qos、stopbits、lifecycle 等），否则服务端存储会丢失字段
4. **VPN BuildArgs 优先级**：`vnt.enabled == true` 时 `BuildArgs()` 从结构化 VNT 配置动态构建命令行，忽略静态 `args` 数组；否则使用 `args`
5. **dispatchStream 启发式路由**：首字节 `{` → 控制命令（`handleServerCmd` 处理 tunnel_push/tunnel_action/restart）；`\x00` → TCP/UDP 代理头；`\x01` → WebSSH 协议头；可解析 HTTP → HTTP 代理；fallback → 原始转发

## 关键约定

1. **不要修改 `internal/protocol/types.go`** 的字段名和 JSON tag（与服务端共享协议）
2. **节点 ID**：固定 8 字符，首字符字母，其余字母或数字
3. **HTTP/HTTPS 隧道 Target**：必须是裸 `host:port`（如 `127.0.0.1:8080`），**不得含 `://`**。`Validate()` 会拒绝任何含 scheme 的 target（服务端 `validateTunnel` 同样拒绝——`SplitHostPort` 对含 scheme 的值报 too many colons），scheme 由 `dispatchStream` 按隧道类型缺省补齐。`tcp`/`udp` 同样为 `host:port`。**注意两端已知分歧**：对 ser2mq/ser2tcp/ser2udp/webssh，服务端不校验 target（空 target 可落库），客户端 `Validate()` 更严（要求非空）——`tunnel_push` 告警对这类空 target 静默跳过
3.1 **`Validate()` 实际约束清单**（`tunnel.go`）：name 非空且不含 `\x00`/`\n`/`\r`（名称进入 `\x00<name>\n` 代理头，控制字符会破坏定界）；type 必须是 10 个已注册常量之一；target 必填（`vpn-manager`/`p2p` 空 target 豁免）；http/https/tcp/udp 的 target 需 host 非空 + 端口 1-65535；rate_limit 若存在须为对象且 max_conns 0-100000、max_bandwidth 0-10737418240、至少一项非零（`null` 或省略 = 清除）。**`listen_port` 两端均不校验**
4. **TunnelType**：使用类型化常量，不用原始字符串
5. **串口读写**：使用 copy-and-release 模式，不持锁跨 I/O
6. **MQTT 连接**：每隧道独立连接，非共享池
7. **Para 字段**：使用 `json.RawMessage` 延迟解析，各 Manager 按需反序列化
8. **ser2mq 消息格式**：MQTTMessage JSON → ChaCha20-Poly1305-X 加密（版本 0x01 + 24字节 nonce + 密文+Tag），兼容 mole-cgui
9. **ser2mq MQTT Topic**：`/mole/<nodeId>/serial/<port>/out`（串口→MQTT）、`/mole/<nodeId>/serial/<port>/in`（MQTT→串口）
10. **静态文件服务**：`fs.Sub(staticFS, "static")` + `StripPrefix`，支持多文件 embed
11. **VPN 程序查找**：`./vnet/` → `$PATH`
12. **HTTP 路由匹配优先级**：路径前缀精确匹配 → 虚拟主机约定（`name-nodeId.domain`）→ 兜底
13. **WebSSH 主机密钥**：优先加载可执行文件同目录 `config/known_hosts`；不存在时 TOFU（首次信任，缓存跨连接复用，进程重启首次仍信任）
14. **WebSSH 消息协议**：`[type 1B][len 2B BE][payload]`，type 见 `handler.go` 常量（终端数据/resize/心跳/文件操作），payload 上限 65535 字节
15. **WebSSH 凭据**：password/key 明文存于 Para 并发服务端（同 ser2mq secret 设计，私钥更敏感）
16. **`internal/p2p/**` 是 fork 自 p2punch 的上游代码**：一行都不能改——多隧道/信令/打洞原生支持，集成 glue 全在 `proxy/p2p`；同步上游走专门流程
17. **p2p Para 两层契约**：连接参数 + `mappings[]` 的 schema 与校验规则（§0.2/§0.3）和服务端逐字一致，共享测试向量 I1-I12/V1-V7 改契约时两端同步更新；映射变更 = Handler 重建 + 会话重打洞（对端零配置）；信令默认公共 MQTT broker 优先 + 服务端内嵌 broker 兜底
18. **p2p 多隧道并行**：一个节点可同时运行多条 p2p 隧道（Manager 按隧道名分发，每条独立 Handler + session，fork 原生支持多隧道），但有两条硬约束：
    - **`local_port` 全节点唯一（跨隧道）**：`manager.go` 启动前登记所有在跑隧道已占端口，冲突的新隧道整条跳过（slog.Error 不启动）。服务端只保证同 para 内不重复（§0.3 I12），跨隧道唯一性仅客户端检查
    - **`mqtt_brokers` 全节点同质**：签名不一致的新隧道拒绝启动。根因是 fork 的 easyp2p 服务器列表与信令凭据均为**包级全局单值**（`SetServers`/`SetSignalCredentials` 写全局，`MQTTSignal` 读全局），混用会把 A 隧道 token 发给 B 隧道配置的第三方 broker——安全考量，宁可拒绝。默认配置下不受影响（`DefaultMQTTBrokers` 只依赖 serverHost，不随 room 变）；手工给不同隧道配不同 broker 列表才会触发。要支持多 broker 需改造 fork，非配置可绕过
    - 服务端侧：同 room 全局最多 2 条记录且必须分属 2 个不同节点（`validateP2PRoomPairing`），同节点内不得有两条同 room——多隧道必须用不同 room

## 开发约束

- **修改代码前必须先 `git pull` 同步最新远程代码**
- 新增隧道类型：`tunnel.go` 的 `TunnelType` 常量 + `Validate()` 注册 + `client.go` 的 `dispatchStream()` 和 `notifyManagers()` 添加分支
- ser2mq 同串口禁止重复创建
- vpn-manager 同程序名互斥
- 所有隧道变更（增删改、启用禁用）必须经过 `notifyManagers()` 通知 Manager，否则 ser2mq/vpn 不会生效
- 修改前端表单提交逻辑时，确保 buildPara 包含该类型 Para 的所有字段

## 构建

**P2P 已进入默认编译产物**：Makefile 的 `TAGS := p2p` 令 `build` / `build-desktop` /
`release` / `publish` 全部带 `-tags p2p`，官方产物一律含 P2P 打洞代码
（`/api/version` 的 `p2p` 标志为 true）。源码零改动——`internal/p2p/**` fork
保持上游原样（rule 16），隔离全靠 build tag。

```bash
make build         # 编译当前平台（含 p2p）
make test          # 全部测试（默认 + -tags p2p 两种构建，约 42s）
make test-race     # 竞态检测（-tags p2p，含 fork 并发测试；约 40s）
make vet           # go vet 两种构建标签
make fmt-check     # gofmt 合规（internal/p2p fork 豁免——rule 16 禁改）
make publish       # 交叉编译 + latest.json + 上传（需先打 tag，见下）

# 裸 go build 不含 p2p（tag 未显式给出）——发布走 make，勿直接 go build
go build ./cmd/moleagent-client              # 无 P2P
go build -tags p2p ./cmd/moleagent-client    # 含 P2P（等价于 make build）
```

### 发布纪律

`publish` 依赖 `check-tag` 前置守卫：**VERSION 必须是 HEAD 上的干净 tag**。
`git describe` 若带 `-N-g<hash>`（未打 tag）或 `-dirty`（工作区脏），publish 立即中止。
原因：`latest.json` 里的 `version` 例如 `v0.6.0-31-gfad1b01` 会被 selfupdater 的
`fallbackParse` 解析成 `0.6.0` + pre-release，按 SemVer 低于 `0.6.0`，老客户端会
判定「无更新」——升级永远不生效。发布顺序：**先 `git tag` → 再 `make publish`**。

`scripts/build.ps1` / `scripts/release.ps1`（Windows）已同步带 `-tags p2p`。
