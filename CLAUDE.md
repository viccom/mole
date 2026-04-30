# moleAgent_client

内网穿透客户端，连接 moleAgent_Serv 服务端建立加密隧道，将外部请求转发到本地服务。

## 技术栈

Go 1.25、smux、go.bug.st/serial、paho.mqtt.golang、golang.org/x/crypto

## 包结构

```
client.go                    公共 API：Client, New(), Run(), AddTunnel(), AllTunnelStatus()...
config.go                    Config 加载/验证/默认值
tunnel.go                    TunnelType: http/tcp/udp/ser2mq/vpn-manager
event.go                     事件系统
internal/
  protocol/types.go          协议层类型
  transport/dialer.go        连接/认证/smux 会话管理
  proxy/http.go              HTTP + WebSocket 代理
  proxy/tcp.go               TCP/UDP 原始转发
  proxy/relay.go             双向数据转发 + 流量统计
  proxy/ser2mq/              串口转 MQTT（ChaCha20-Poly1305-X 加密）
  proxy/vpn/                 VPN 程序管理
  builtin/server.go          内置 HTTP 服务 + 统一 API
  builtin/static/            前端（ES Module 模块化）
    index.html               HTML 结构
    style.css                样式
    api.js                   统一 API 客户端
    app.js                    应用逻辑（状态、渲染、轮询）
```

## 隧道类型

| 类型 | 说明 |
|------|------|
| `http` | HTTP/WebSocket 代理 |
| `tcp` | TCP 透明转发 |
| `udp` | UDP 透明转发 |
| `ser2mq` | 串口 ↔ MQTT（ChaCha20-Poly1305-X 加密） |
| `vpn-manager` | VPN 程序启停监视 |

## 统一 API 设计

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
| GET | `/api/status` | 客户端全局状态 |

`TunnelStatus` 结构合并配置 + 运行时状态（connected、bytes_in/out、类型特定 status）。

## 关键约定

1. **不要修改 `internal/protocol/types.go`** 的字段名和 JSON tag
2. **节点 ID**：固定 8 字符，首字符字母，其余字母或数字
3. **HTTP 隧道 Target**：必须以 `http://` 或 `https://` 开头
4. **TunnelType**：使用类型化常量，不用原始字符串
5. **隧道持久化**：服务端是唯一来源，客户端不缓存配置
6. **串口读写**：使用 copy-and-release 模式，不持锁跨 I/O
7. **MQTT 连接**：每隧道独立连接，非共享池
8. **扩展配置**：使用 `Para json.RawMessage` 字段存储 ser2mq/vpn-manager 配置
9. **ser2mq 消息格式**：MQTTMessage JSON → ChaCha20-Poly1305-X 加密（版本 0x01 + 24字节 nonce + 密文+Tag），兼容 mole-cgui
10. **静态文件服务**：使用 `fs.Sub(staticFS, "static")` + `StripPrefix`，支持多文件 embed

## 开发约束

- **修改代码前必须先 `git pull` 同步最新远程代码**
- 新增隧道类型：在 `tunnel.go` 的 `Validate()` 中注册
- ser2mq 同串口禁止重复创建
- vpn-manager 同程序名互斥
- VPN 程序查找：`./vnet/` → `$PATH`

## 构建

```bash
make build
go vet ./...
```
