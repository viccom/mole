# moleAgent_client

内网穿透客户端，连接 moleAgent_Serv 服务端建立加密隧道，将外部请求转发到本地服务。支持 HTTP/TCP/UDP/ser2mq/ser2tcp/ser2udp/vpn-manager 七种隧道类型，以及 TCP/WebSocket/KCP 三种传输协议。

## 🏗️ 程序家族

本项目包含多个桌面程序，统一代码库便于同步更新：

```
┌─────────────────────────────────────────────────────────────┐
│                     moleAgent_client                       │
│                   核心库（共享代码）                        │
├─────────────────────────────────────────────────────────────┤
│         │                  │                │            │
│         ▼                  ▼                ▼            │
│  moleagent-client   moleagent-desktop   moleagent-manager  │
│   命令行客户端       桌面客户端          多实例管理器     │
└─────────────────────────────────────────────────────────────┘
```

| 程序 | 说明 | 入口目录 |
|------|------|---------|
| **moleagent-client** | 命令行客户端，最小化部署 | `cmd/moleagent-client/` |
| **moleagent-desktop** | 完整桌面客户端，支持多节点管理 | `cmd/moleagent-desktop/` |
| **moleagent-manager** | Web UI 容器，连接多个后端 | `cmd/moleagent-manager/` |

### moleagent-client（命令行客户端）

最小化的命令行客户端，适合服务器部署。

```bash
./moleagent-client -server 82.157.196.219:9981 -token your-token
```

### moleagent-desktop（桌面客户端）

完整的桌面客户端，内嵌 moleAgent_client 功能。

**特性**：
- 完整桌面界面
- 本地 moleAgent_client 实例
- 多节点切换管理
- 系统托盘支持
- 单实例运行

```bash
# 构建
cd cmd/moleagent-desktop
wails build

# 运行
./build/bin/moleagent-desktop.exe
```

### moleagent-manager（多实例管理器）

轻量级桌面程序，通过 WebView 加载 moleAgent_client 的 Web UI。

**特性**：
- 加载远程后端 Web UI
- 多后端实例管理
- 节点切换
- 系统托盘支持
- 单实例运行

```bash
# 构建
cd cmd/moleagent-manager
wails build

# 运行
./build/bin/moleagent-manager.exe
```

## 程序架构

### 分层设计

```
┌─────────────────────────────────────────────────────────────┐
│                      CLI 入口层 (cmd/)                       │
│  moleagent-client: 命令行参数解析、配置加载                  │
│  moleagent-desktop: 桌面客户端入口                           │
│  moleagent-manager: 多实例管理器入口                       │
├─────────────────────────────────────────────────────────────┤
│                    公共 API 层 (根包)                        │
│  client.go  config.go  tunnel.go  event.go  nodeid.go      │
├──────────────┬──────────────────────┬────────────────────────┤
│   传输层     │      代理层          │    内置服务层           │
│  transport/ │      proxy/          │    builtin/             │
│  连接/认证/ │  http  tcp  relay   │  HTTP API + Web UI    │
│  smux 会话  │  ser2mq/  vpn/     │  静态文件嵌入          │
├──────────────┴──────────────────────┴────────────────────────┤
│                     协议层 (protocol/)                       │
└─────────────────────────────────────────────────────────────┘
```

### 包结构

```
.
├── cmd/
│   ├── moleagent-client/        命令行客户端入口
│   ├── moleagent-desktop/        桌面客户端（Wails）
│   │   ├── main.go
│   │   ├── app.go
│   │   ├── tray.go
│   │   ├── wails.json
│   │   └── frontend/dist/       前端资源
│   └── moleagent-manager/        多实例管理器（Wails）
│       ├── main.go
│       ├── app.go
│       ├── tray.go
│       ├── wails.json
│       └── frontend/dist/        前端资源
│
├── internal/                    内部实现
│   ├── protocol/types.go         协议消息结构
│   ├── transport/dialer.go      连接管理
│   ├── proxy/                   代理层
│   └── builtin/                 内置 HTTP 服务
│
├── pkg/                         共享包
│   └── node/                    节点管理（moleagent-desktop）
│
├── frontend/                    前端资源
│   ├── client/                  内置 UI（嵌入式）
│   ├── desktop/                 桌面客户端 UI
│   └── manager/                 管理器 UI
│
└── assets/icons/               图标资源
```

## 快速开始

### 命令行客户端

```bash
# 默认 TCP 传输
./moleagent-client -server 82.157.196.219:9981 -token your-token

# WebSocket 传输
./moleagent-client -server 82.157.196.219:9981 -token your-token -transport ws

# KCP 传输
./moleagent-client -server 82.157.196.219:9981 -token your-token -transport kcp
```

### 桌面客户端

```bash
cd cmd/moleagent-desktop
wails build
./build/bin/moleagent-desktop.exe
```

### 多实例管理器

```bash
cd cmd/moleagent-manager
wails build
./build/bin/moleagent-manager.exe
```

## 构建

### 命令行客户端

```bash
# 当前平台
make build

# 交叉编译
make release
```

### 桌面程序（Wails）

```bash
# moleagent-desktop
cd cmd/moleagent-desktop
wails build

# moleagent-manager
cd cmd/moleagent-manager
wails build
```

Windows PowerShell：

```powershell
.\scripts\build.ps1
```

## Web 管理界面

访问内置 HTTP 服务的 `/ui` 路径可打开管理界面：

- 实时连接状态与流量监控
- 统一隧道列表
- ser2mq 隧道详情
- VPN 管理面板

## 扩展性设计

### 可扩展点

| 扩展点 | 位置 | 方式 |
|--------|------|------|
| 新增隧道类型 | `tunnel.go` | 注册新类型常量 |
| 新增传输协议 | `transport/dialer.go` | 实现 `DialFunc` |
| 新增隧道管理器 | `client.go` | 实现 `OnTunnelUpdate()` |

## 作为第三方库集成

```go
import moleAgent_client "moleAgent_client"

cfg := moleAgent_client.DefaultConfig()
cfg.ServerAddr = "server:9981"
cfg.Token = "my-token"

client, _ := moleAgent_client.New(cfg)
go client.Run(ctx)

client.AddTunnel(moleAgent_client.Tunnel{
    Name: "web", Type: moleAgent_client.TunnelTypeHTTP,
    Target: "127.0.0.1:8080", Enabled: true,
})
```

## 致谢

| 库 | 用途 |
|----|------|
| [xtaci/smux](https://github.com/xtaci/smux) | TCP 多路复用 |
| [go.bug.st/serial](https://github.com/bugst/go-serial) | 跨平台串口通信 |
| [eclipse/paho.mqtt.golang](https://github.com/eclipse/paho.mqtt.golang) | MQTT v3.1.1 客户端 |
| [golang.org/x/crypto](https://golang.org/x/crypto) | ChaCha20-Poly1305-X 认证加密 |
| [xtaci/kcp-go](https://github.com/xtaci/kcp-go) | KCP（UDP）可靠传输 |
| [shirou/gopsutil](https://github.com/shirou/gopsutil) | 系统信息采集 |
| [gorilla/websocket](https://github.com/gorilla/websocket) | WebSocket 协议 |
| [getlantern/systray](https://github.com/getlantern/systray) | 系统托盘图标 |
| [wailsapp/wails](https://github.com/wailsapp/wails) | 桌面应用框架 |
