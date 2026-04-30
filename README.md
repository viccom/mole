# moleAgent_client

内网穿透客户端，支持 HTTP/TCP/UDP/ser2mq/vpn-manager 多种隧道类型。

## 隧道类型

| 类型 | 说明 |
|------|------|
| `http` | HTTP/WebSocket 代理 |
| `tcp` | TCP 透明转发 |
| `udp` | UDP 透明转发 |
| `ser2mq` | 串口 ↔ MQTT（ChaCha20-Poly1305-X 加密），兼容 mole-cgui |
| `vpn-manager` | VPN 程序启停监视与崩溃重启 |

## 快速开始

```bash
./moleagent-client -server 82.157.196.219:9981 -token your-token
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
    "binary": {"name": "easytier-core"},
    "args": ["-w", "udp://server:520/"],
    "lifecycle": {"autostart": false, "restart_on_crash": true, "max_restarts": 3, "restart_delay": 5},
    "watchdog": {"enabled": true, "interval": 10, "quit_grace": 10},
    "log": {"capture": true, "max_size": 65536}
  }
}
```

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

### 全局状态

| 方法 | 端点 | 说明 |
|------|------|------|
| `GET` | `/api/status` | 客户端连接状态、全局流量统计 |

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
- ser2mq 隧道详情面板（串口/MQTT 状态）
- VPN 管理面板（启停/崩溃日志）

## 构建

```bash
make build
```
