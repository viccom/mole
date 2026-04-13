# moleAgent_client

内网穿透客户端，连接 moleAgent_Serv 服务端建立加密隧道，将外部请求转发到本地服务。支持作为独立程序运行或作为 Go 库集成到其他应用。

## 快速开始

### 独立运行

```bash
# 最简模式（自动生成节点 ID，连接本地服务端）
./moleagent-client -server 82.157.196.219:9981 -token your-token

# 使用配置文件
./moleagent-client -config client.json
```

### 配置文件示例

```json
{
  "server_addr": "82.157.196.219:9981",
  "token": "your-node-token",
  "node_id": "myapp001",
  "tunnels": [
    {"name": "web", "type": "http", "target": "http://127.0.0.1:8080"},
    {"name": "api", "type": "http", "target": "http://127.0.0.1:3000", "domain": "api.example.com"},
    {"name": "db", "type": "tcp", "target": "127.0.0.1:3306", "listen_port": 13306}
  ]
}
```

---

## 集成到 Go 程序

moleAgent_client 设计为可直接 `import` 使用的 Go 库。

### 基础集成

```go
package main

import (
    "context"
    "log"

    moleAgent_client "moleAgent_client"
)

func main() {
    cfg := moleAgent_client.DefaultConfig()
    cfg.ServerAddr = "82.157.196.219:9981"
    cfg.Token = "your-token"
    cfg.BuiltinHTTP = "off" // 关闭内置 HTTP 服务

    client, err := moleAgent_client.New(cfg)
    if err != nil {
        log.Fatal(err)
    }

    // 注册事件监听
    client.OnEvent(moleAgent_client.EventConnected, func(e moleAgent_client.Event) {
        log.Println("已连接到服务端")
    })

    // Run 阻塞运行，ctx 取消时退出
    client.Run(context.Background())
}
```

### 动态隧道管理

```go
// 添加隧道（自动同步到服务端）
client.AddTunnel(moleAgent_client.Tunnel{
    Name:   "myapp",
    Type:   moleAgent_client.TunnelTypeHTTP,
    Target: "http://127.0.0.1:8080",
})

// 移除隧道
client.RemoveTunnel("myapp")

// 替换所有隧道
client.UpdateTunnels([]moleAgent_client.Tunnel{
    {Name: "web", Type: moleAgent_client.TunnelTypeHTTP, Target: "http://localhost:3000"},
    {Name: "ssh", Type: moleAgent_client.TunnelTypeTCP, Target: "127.0.0.1:22"},
})

// 获取当前隧道列表（快照，线程安全）
tunnels := client.Tunnels()
```

### 事件监听

```go
// 监听特定事件
client.OnEvent(moleAgent_client.EventTunnelUpdated, func(e moleAgent_client.Event) {
    log.Printf("服务端推送了隧道更新，当前 %d 条隧道", e.Data["tunnels"])
})

// 通配符：监听所有事件
client.OnEvent("", func(e moleAgent_client.Event) {
    log.Printf("[%s] %v", e.Type, e.Data)
})
```

### 从配置文件加载

```go
cfg, err := moleAgent_client.LoadConfigFile("client.json")
if err != nil {
    log.Fatal(err)
}
client, err := moleAgent_client.New(cfg)
```

---

## 隧道类型与规则

### HTTP 隧道

将外部 HTTP/WebSocket 请求转发到本地 HTTP 后端。

```json
{"name": "web", "type": "http", "target": "http://127.0.0.1:8080"}
```

**路由规则（服务端 → 客户端匹配顺序）**：
1. **泛域名匹配**（最常用）：`隧道名-节点ID.域名` → `web-myapp001.px.metme.top` → 匹配隧道名 `web`
2. **精确域名匹配**：配置了 `domain` 字段时直接匹配
3. **Fallback**：未匹配时使用名称为 `web` 的隧道，或第一个 HTTP 隧道

**Target 格式**：必须以 `http://` 或 `https://` 开头。

**WebSocket**：自动检测 `Upgrade: websocket` 头，透明转发。

### TCP 隧道

将服务端指定端口的 TCP 连接转发到本地地址。

```json
{"name": "ssh", "type": "tcp", "target": "127.0.0.1:22", "listen_port": 10022}
```

- 服务端在 `listen_port` 监听外部连接
- 所有数据双向透明转发
- Target 格式：`host:port`

### UDP 隧道

与 TCP 类似，转发 UDP 数据包。

```json
{"name": "dns", "type": "udp", "target": "127.0.0.1:53", "listen_port": 10053}
```

---

## 节点 ID 规则

- 固定 **8 个字符**
- 首字符必须是**字母**（a-z, A-Z）
- 其余字符为**字母或数字**（a-z, A-Z, 0-9）
- 不指定时自动生成随机 ID
- 用于泛域名路由：`隧道名-节点ID.域名`

```go
// 自动生成
id := moleAgent_client.GenerateNodeID() // 如 "k7x9m2ab"

// 手动指定（配置文件）
"node_id": "myapp001"

// 验证
moleAgent_client.ValidateNodeID("myapp001") // true
moleAgent_client.ValidateNodeID("test-node") // false（含连字符）
```

---

## 运行模式

### 模式一：CLI 独立运行

适合部署在内网服务器上，通过命令行或配置文件管理。

```bash
# 完整参数
./moleagent-client \
  -server 82.157.196.219:9981 \
  -token your-token \
  -id myapp001 \
  -http 127.0.0.1:18080

# 使用配置文件
./moleagent-client -config client.json

# 无内置 HTTP 服务（仅隧道转发）
./moleagent-client -server ... -token ... -http off
```

内置 HTTP 服务提供：
- `GET /` — 默认欢迎页
- `GET /health` — 健康检查
- `POST /api/tunnels` — 动态添加隧道
- `DELETE /api/tunnels/{name}` — 动态删除隧道
- `GET /api/tunnels` — 查看隧道列表

### 模式二：Go 库集成

适合嵌入到其他 Go 应用中，程序化控制隧道生命周期。

```go
cfg := &moleAgent_client.Config{
    ServerAddr: "82.157.196.219:9981",
    Token:      "your-token",
    NodeID:     "myapp001",
    Tunnels: []moleAgent_client.Tunnel{
        {Name: "web", Type: moleAgent_client.TunnelTypeHTTP, Target: "http://localhost:8080"},
    },
    BuiltinHTTP: "off",
}
client, _ := moleAgent_client.New(cfg)
go client.Run(ctx) // 非阻塞运行

// 在应用逻辑中动态管理隧道
client.AddTunnel(newTunnel)
```

### 模式三：最小化嵌入

只需隧道转发功能，不需要任何管理接口。

```go
cfg := moleAgent_client.DefaultConfig()
cfg.ServerAddr = "server:9981"
cfg.Token = "token"
cfg.BuiltinHTTP = "off"          // 关闭内置 HTTP
cfg.NodeID = "minicli1"

client, _ := moleAgent_client.New(cfg)
client.Run(ctx) // 仅转发，无管理 API
```

---

## 事件类型

| 事件 | 触发时机 | Data 字段 |
|------|---------|-----------|
| `connected` | TCP 连接建立 | — |
| `authenticated` | 认证成功 | — |
| `registered` | 节点注册完成 | `tunnels`: 隧道数量 |
| `disconnected` | 连接断开 | — |
| `reconnecting` | 正在重连 | `error`: 失败原因 |
| `heartbeat_ok` | 心跳成功 | — |
| `heartbeat_fail` | 心跳失败 | `error`: 失败原因 |
| `tunnel_updated` | 服务端推送更新 | `tunnels`: 隧道数量 |
| `tunnel_synced` | 客户端同步完成 | `count`: 隧道数量 |

---

## 配置参考

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `server_addr` | string | `127.0.0.1:9981` | 服务端地址 |
| `token` | string | `default-node-token-change-me` | 认证令牌 |
| `node_id` | string | 自动生成 | 8 字符，首字符字母 |
| `node_name` | string | 同 node_id | 节点名称 |
| `tls` | bool | false | 启用 TLS 连接 |
| `http_port` | string | `127.0.0.1:18080` | 内置 HTTP 端口，`off` 关闭 |
| `tunnels` | array | 自动创建 web 隧道 | 隧道配置列表 |
| `heartbeat_interval` | duration | `10s` | 心跳间隔 |
| `heartbeat_timeout` | duration | `5s` | 心跳超时 |
| `reconnect_interval` | duration | `5s` | 重连间隔 |

## 构建

```bash
make build          # 当前平台
make release        # 所有平台（linux/darwin/windows, amd64/arm64）
make clean          # 清理构建产物
```
