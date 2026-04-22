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

```
GET  /api/tunnels           隧道列表
POST /api/tunnels           添加隧道
DELETE /api/tunnels/:name   删除隧道
GET  /api/vpn               VPN 列表
POST /api/vpn/:name/start   启动 VPN
POST /api/vpn/:name/stop    停止 VPN
GET  /api/vpn/:name/logs    崩溃日志
GET  /api/ser2mq            ser2mq 列表
GET  /api/ser2mq/:name/status  状态
```

## 构建

```bash
make build
```
