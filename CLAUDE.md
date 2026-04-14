# moleAgent_client

内网穿透客户端。连接 moleAgent_Serv 服务端，通过 smux 多路复用隧道将外部请求转发到本地服务。

## 项目定位

**可独立运行，也可作为 Go 库嵌入其他程序。** 导出 `Client` 类型和完整公共 API，`internal/` 下的包不可外部访问。

## 技术栈

Go 1.25、smux（TCP 多路复用）

## 包结构

```
client.go                    公共 API：Client, New(), Run(), AddTunnel()...
config.go                    Config 加载/验证/默认值
tunnel.go                    Tunnel 类型（统一，与 protocol.Tunnel 通过转换函数互转）
event.go                     事件系统：EventType, EventHandler, EventBus
nodeid.go                    节点 ID 生成与验证
cmd/moleagent-client/main.go CLI 入口（~105 行，仅参数解析 + 启动）
internal/
  protocol/types.go          协议层类型（与 wire 格式对应）
  transport/dialer.go        连接/认证/smux 会话管理
  proxy/http.go              HTTP + WebSocket 代理
  proxy/tcp.go               TCP/UDP 原始转发
  proxy/relay.go             双向 IO 工具
  builtin/server.go          内置 HTTP 服务 + 隧道 CRUD API（仅 CLI 模式）
```

## 架构要点

**节点 ID**：默认基于 CPU 信息（VendorID+ModelName SHA256 哈希）生成确定性 8 字符 ID，首字符字母。无法获取硬件信息时 fallback 到随机生成。可通过配置或 `-id` 参数自定义。

**数据流**：`Client.Run()` 连接服务端 → 认证(challenge-response) → 建立 smux 会话 → 注册节点(空隧道) → 服务端推送持久化隧道 → 心跳循环 + 接受数据流。

**隧道持久化**：服务端是隧道配置的唯一持久化来源。客户端启动不携带隧道，连接后由服务端通过 `tunnel_push` 推送。隧道变更（增删改）实时同步并持久化到服务端 NodeRepo（Redka/SQLite）。

**流分发**（dispatchStream）：客户端 AcceptStream 后启发式判断类型：
- 首字节 `{` 且 JSON 含 `cmd:"tunnel_push"` → 服务端推送，更新本地隧道配置
- 可解析为 HTTP 请求 → HTTP/WebSocket 代理
- 其他 → TCP/UDP 原始转发

**CLI 子命令**：`-tunnels` 子命令向已运行的客户端 HTTP API 发送请求：
- `-tunnels --list` 列出隧道
- `-tunnels --add name:type:target` 添加隧道
- `-tunnels --del name` 删除隧道
- `-tunnels --addr 127.0.0.1:18080` 指定 API 地址

**并发安全**：
- `tunnels` 切片由 `sync.RWMutex` 保护
- 控制命令（register/ping/tunnel_update）由 `ctrlMu` 串行化
- 隧道更新通过 `tunReqs` channel 序列化

**协议不变**：与服务端的 wire 协议未做任何修改。客户端打开的流 = 控制命令，服务端打开的流 = 数据/推送。

## 关键约定

- `TunnelType` 是类型化常量（`"http"`, `"tcp"`, `"udp"`），不使用原始字符串
- `Tunnel` 是唯一隧道类型，`toProtocol()`/`fromProtocol()` 仅在与 wire 格式交互时使用
- 节点 ID 固定 8 字符，首字符字母，其余字母或数字
- HTTP 隧道 Target 必须以 `http://` 或 `https://` 开头
- `BuiltinHTTP = "off"` 时关闭内置 HTTP 服务（库集成模式应设为 off）
- AddTunnel/RemoveTunnel 需要已连接服务端，未连接时返回错误
- 隧道配置由服务端持久化，客户端启动时不携带隧道配置

## 构建与运行

```bash
make build                    # 编译当前平台到 ../_release/
./moleagent-client -config client.json
go vet ./...                  # 静态检查
```

## 注意事项

- **不要修改 `internal/protocol/types.go`** 的字段名和 JSON tag，必须与服务端 wire 格式保持一致
- TCP/UDP 隧道当前只能匹配第一个，这是协议层限制（服务端未在流中标识隧道名）
- smux 配置（KeepAlive、MaxFrameSize 等）必须与服务端一致
- `Client.Run()` 是阻塞调用，库集成时应 `go client.Run(ctx)`
- 事件处理器是同步调用的，耗时操作应在 handler 中启动新 goroutine
