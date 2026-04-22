# moleAgent_client

内网穿透客户端，连接 moleAgent_Serv 服务端建立加密隧道，将外部请求转发到本地服务。

## 技术栈

Go 1.25、smux、go.bug.st/serial、paho.mqtt.golang、golang.org/x/crypto

## 包结构

```
client.go                    公共 API：Client, New(), Run(), AddTunnel()...
config.go                    Config 加载/验证/默认值
tunnel.go                    TunnelType: http/tcp/udp/ser2mq/vpn-manager
event.go                     事件系统
internal/
  protocol/types.go          协议层类型
  transport/dialer.go        连接/认证/smux 会话管理
  proxy/http.go               HTTP + WebSocket 代理
  proxy/tcp.go               TCP/UDP 原始转发
  proxy/ser2mq/              串口转 MQTT
  proxy/vpn/                 VPN 程序管理
  builtin/server.go           内置 HTTP 服务 + API
```

## 隧道类型

| 类型 | 说明 |
|------|------|
| `http` | HTTP/WebSocket 代理 |
| `tcp` | TCP 透明转发 |
| `udp` | UDP 透明转发 |
| `ser2mq` | 串口 ↔ MQTT（ChaCha20-Poly1305 加密） |
| `vpn-manager` | VPN 程序启停监视 |

## 关键约定

1. **不要修改 `internal/protocol/types.go`** 的字段名和 JSON tag
2. **节点 ID**：固定 8 字符，首字符字母，其余字母或数字
3. **HTTP 隧道 Target**：必须以 `http://` 或 `https://` 开头
4. **TunnelType**：使用类型化常量，不用原始字符串
5. **隧道持久化**：服务端是唯一来源，客户端不缓存配置
6. **串口读写**：使用 `sync.RWMutex` 保护
7. **MQTT 连接池**：相同 Broker 共享连接
8. **扩展配置**：使用 `Para json.RawMessage` 字段存储 ser2mq/vpn-manager 配置

## 开发约束

- 新增隧道类型：在 `tunnel.go` 的 `Validate()` 中注册
- ser2mq 同串口禁止重复创建
- vpn-manager 同程序名互斥
- VPN 程序查找：`./vnet/` → `$PATH`

## 构建

```bash
make build
go vet ./...
```
