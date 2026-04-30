# moleAgent_client

内网穿透客户端，支持 HTTP/TCP/UDP/ser2mq/vpn-manager 多种隧道类型。

## 隧道类型

| 类型 | 说明 |
|------|------|
| `http` | HTTP/WebSocket 代理 |
| `tcp` | TCP 透明转发 |
| `udp` | UDP 透明转发 |
| `ser2mq` | 串口 ↔ MQTT（ChaCha20-Poly1305 加密） |
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
  "para": {
    "broker": "mqtt://user:pass@broker:1883",
    "serial": {"baudrate": 9600, "databits": 8, "parity": "N", "timeout": 3000},
    "secret": "32字节hex密钥"
  }
}
```

MQTT 主题：`/mole/<nodeid>/serial/<port>/out|in`

## vpn-manager 配置示例

```json
{
  "name": "my-vpn",
  "type": "vpn-manager",
  "target": "easytier",
  "para": {
    "binary": {"name": "easytier-core"},
    "args": ["-w", "udp://server:520/"],
    "lifecycle": {"autostart": false, "restart_on_crash": true, "max_restarts": 3},
    "log": {"capture": true, "max_size": 65536}
  }
}
```

程序查找：`./vnet/` → `$PATH`

## API

内置 HTTP 服务（默认 `127.0.0.1:18080`）：

```text
GET    /api/status
GET    /api/tunnels
POST   /api/tunnels
GET    /api/tunnels/{name}
DELETE /api/tunnels/{name}
POST   /api/tunnels/{name}/start
POST   /api/tunnels/{name}/stop
GET    /api/tunnels/{name}/logs
```

## 构建

POSIX shell 环境（Linux/macOS/Git Bash）：

```bash
make build
make release
```

Windows PowerShell：

```powershell
.\scripts\build.ps1
.\scripts\release.ps1
```
