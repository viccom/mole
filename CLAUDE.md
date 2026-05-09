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
internal/
  protocol/types.go          协议层类型（ControlCmd, Tunnel 等）
  transport/dialer.go        连接/认证/smux 会话管理
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
  proxy/vpn/
    config.go                配置结构 + BuildArgs() 动态参数构建
    manager.go               多隧道生命周期管理
    process.go               进程管理：启停、崩溃检测、日志采集
    vnt_client.go            vnt-cli REST API 客户端
  builtin/
    server.go                内置 HTTP 服务 + REST API
    static/                  前端（ES Module 模块化，go:embed 嵌入）
```

## 隧道类型

| 类型 | 说明 |
|------|------|
| `http` | HTTP/WebSocket 代理 |
| `tcp` | TCP 透明转发 |
| `udp` | UDP 透明转发 |
| `ser2mq` | 串口 ↔ MQTT（ChaCha20-Poly1305-X 加密） |
| `ser2tcp` | 串口 ↔ TCP（Server/Client 双模式，本地透传，不参与路由） |
| `ser2udp` | 串口 ↔ UDP（Server/Client 双模式，本地透传，不参与路由） |
| `vpn-manager` | VPN 程序启停监视 |

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
| GET | `/api/version` | 版本与系统信息 |

`TunnelStatus` 合并配置 + 运行时状态（connected、bytes_in/out、类型特定 status）。

## 隧道配置数据流（⚠️ 重要）

**服务端是唯一权威源，客户端不持久化隧道配置。** 客户端仅持有内存快照。

### 双向同步路径

- **服务端→客户端**：管理后台修改 → 服务端 `tunnel_push` JSON → `handlePossiblePush()` 直接替换 `tunnels[]` → `notifyManagers()`
- **客户端→服务端**：本地 API 操作 → `AddTunnel()`/`RemoveTunnel()` → `tunReqs` channel → `processTunnelUpdates()` 序列化处理 → `sendTunnelUpdate()` 发服务端持久化 → 成功后更新内存 → `notifyManagers()`

### 变更处理流水线

```
applyTunnelMutation() 在快照上执行增/删/替换
  → sendTunnelUpdate() 写服务端
    → 失败：内存不更新，Manager 不通知，返回错误
    → 成功：更新内存 tunnels[]
      → notifyManagers() 分发到 ser2mq/vpn Manager
        → Manager.OnTunnelUpdate() 比对新旧 map，增量启停
```

**三种 mutation**：`add`（新增或同名替换）、`remove`（按名称删除）、`replace_all`（全量替换）。

### notifyManagers 机制

遍历 tunnels 列表，按类型提取配置：
- ser2mq：`json.Unmarshal(Para)` → `Ser2MQConfig` → `ser2mqMgr.OnTunnelUpdate(configs map)`
- vpn：`json.Unmarshal(Para)` → `vpn.Config` → `vpnMgr.OnTunnelUpdate(names, configs map)`
- disabled 的隧道不进入 configs map，Manager 检测到"消失"会停止对应处理器/进程

### ⚠️ 易出错点

1. **`applyTunnelMutation` 替换整个 Tunnel 对象**：toggle enable/disable 时必须保留 name、target、domain、listen_port、Para 等所有字段，不能只传 enabled 字段
2. **Para 的 enable 字段可能缺失**：服务端存储的 Para 可能不含 `enable` 字段，Go `json.Unmarshal` 默认 bool 为 false。`notifyManagers()` 中必须用 `cfg.Enable = t.IsEnabled()` 同步，`IsEnabled()` 将 nil Para 视为 true
3. **前端 buildPara 必须包含所有字段**：编辑 ser2mq/vpn 隧道时，即使某些字段未在表单中显示，也要原样传回（如 secret、qos、stopbits、lifecycle 等），否则服务端存储会丢失字段
4. **VPN BuildArgs 优先级**：`vnt.enabled == true` 时 `BuildArgs()` 从结构化 VNT 配置动态构建命令行，忽略静态 `args` 数组；否则使用 `args`
5. **dispatchStream 启发式路由**：首字节 `{` 且含 `cmd:"tunnel_push"` → 控制推送；`\x00` → TCP/UDP 代理头；可解析 HTTP → HTTP 代理；fallback → 原始转发

## 关键约定

1. **不要修改 `internal/protocol/types.go`** 的字段名和 JSON tag（与服务端共享协议）
2. **节点 ID**：固定 8 字符，首字符字母，其余字母或数字
3. **HTTP 隧道 Target**：必须以 `http://` 或 `https://` 开头
4. **TunnelType**：使用类型化常量，不用原始字符串
5. **串口读写**：使用 copy-and-release 模式，不持锁跨 I/O
6. **MQTT 连接**：每隧道独立连接，非共享池
7. **Para 字段**：使用 `json.RawMessage` 延迟解析，各 Manager 按需反序列化
8. **ser2mq 消息格式**：MQTTMessage JSON → ChaCha20-Poly1305-X 加密（版本 0x01 + 24字节 nonce + 密文+Tag），兼容 mole-cgui
9. **ser2mq MQTT Topic**：`/mole/<nodeId>/serial/<port>/out`（串口→MQTT）、`/mole/<nodeId>/serial/<port>/in`（MQTT→串口）
10. **静态文件服务**：`fs.Sub(staticFS, "static")` + `StripPrefix`，支持多文件 embed
11. **VPN 程序查找**：`./vnet/` → `$PATH`
12. **HTTP 路由匹配优先级**：路径前缀精确匹配 → 虚拟主机约定（`name-nodeId.domain`）→ 兜底

## 开发约束

- **修改代码前必须先 `git pull` 同步最新远程代码**
- 新增隧道类型：`tunnel.go` 的 `TunnelType` 常量 + `Validate()` 注册 + `client.go` 的 `dispatchStream()` 和 `notifyManagers()` 添加分支
- ser2mq 同串口禁止重复创建
- vpn-manager 同程序名互斥
- 所有隧道变更（增删改、启用禁用）必须经过 `notifyManagers()` 通知 Manager，否则 ser2mq/vpn 不会生效
- 修改前端表单提交逻辑时，确保 buildPara 包含该类型 Para 的所有字段

## 构建

```bash
make build
go vet ./...
```
