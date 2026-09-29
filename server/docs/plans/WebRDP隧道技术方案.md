# WebRDP 隧道技术方案

> 状态：研究阶段，暂不实施
> 日期：2026-06-11

## 目标

实现 `Windows桌面(RDP:3389) → moleAgent_client → moleAgent_Serv → 浏览器(HTML5 Canvas)` 的 WebRDP 隧道，用户通过管理后台 Web 页面即可远程操作 Windows 桌面。

## 架构

```
Windows桌面(RDP:3389)
    ↓ RDP 协议
moleAgent_client (internal/proxy/webrdp/)
    ↓ smux 二进制帧 [type 1B][len 2B][payload]
moleAgent_Serv (internal/api/webrdp_handler.go)
    ↓ WebSocket
浏览器 (HTML5 Canvas + 键盘/鼠标事件)
```

与现有 WebSSH 隧道架构完全一致，仅协议层不同。

## RDP 协议概览

RDP 是基于 TCP 的二进制协议（端口 3389），协议栈层次：

```
TCP → TPKT/X.224（连接协商）→ TLS/CredSSP（加密+NLA认证）→ T.125 MCS（通道复用）→ RDP PDUs（图形/输入/剪贴板/音频等）
```

复杂度远超 SSH：数百种能力集、多种图形编解码器（RFX、GFX、H.264、NSCodec、ClearCodec）、大量虚拟通道（剪贴板、音频、磁盘映射、打印机、USB、智能卡）。

## 候选方案对比

| 项目 | 语言 | 架构 | 许可证 | 推荐度 |
|------|------|------|--------|--------|
| **gopher-rdp** | 纯 Go | Browser ←WS→ Go server ←RDP→ Windows | MIT | ★★★★★ |
| **go-rdp** | Go + WASM | Browser(WASM解码) ←WS→ Go server ←RDP→ Windows | MIT | ★★★★ |
| **Apache Guacamole** | C + Java | Browser ←WS→ Java ←TCP→ guacd(FreeRDP) ←RDP→ Windows | Apache 2.0 | ★★★ |
| **grdp** | Go | 类似 gopher-rdp，支持 H.264(FFmpeg) | GPL-3.0 | ★★ |
| **Teleport** | Rust + Go | CGo 调用 rdp-rs | Apache 2.0 | ★★ |

## 推荐方案：gopher-rdp

**理由：**

1. **纯 Go、零外部依赖** —— 与现有代码栈一致，无 CGo
2. **协议覆盖完整** —— TPKT、X.224、MCS、TLS+NLA/CredSSP、RDPGFX v8-v10.7、多种编解码器、剪贴板/音频/磁盘/打印机等虚拟通道
3. **已有 Web 查看器** —— `display/web` 包提供 HTTP+WebSocket 服务，可参考实现
4. **MIT 许可证** —— 无 copyleft 风险

仓库：`github.com/bouncyball-git/gopher-rdp`

## 集成路径

### 客户端（moleAgent_client）

1. `tunnel.go` 新增 `TunnelTypeWebRDP TunnelType = "webrdp"`
2. `internal/proxy/webrdp/` 新建目录，包含：
   - `handler.go` —— 封装 gopher-rdp 客户端，接收解码帧转发到 smux 流
   - `manager.go` —— 多隧道生命周期管理
   - `config.go` —— 隧道配置结构（target、username、password、分辨率等）
3. `dispatchStream()` 和 `notifyManagers()` 添加 webrdp 分支

### 服务端（moleAgent_Serv）

1. `internal/api/webrdp_handler.go` —— WebSocket ↔ smux 消息透传
2. `router.go` 注册 `/api/v1/tunnels/:name/webrdp` 路由

### 前端（admin）

1. `WebRDPPage.tsx` —— HTML5 Canvas 渲染 + 输入事件处理
2. 键盘事件 → RDP scancode 映射表
3. 鼠标事件 → RDP 指针事件
4. 工具栏：断开、全屏、Ctrl+Alt+Del 等

### 消息协议设计

复用 WebSSH 的 `[type 1B][payload]` 格式，新增消息类型：

| 类型 | 方向 | 说明 |
|------|------|------|
| 0x10 | 上行 | 鼠标事件（x, y, button, state） |
| 0x11 | 上行 | 键盘事件（scancode, isDown） |
| 0x12 | 上行 | 剪贴板文本 |
| 0x13 | 下行 | 图形帧（编码后的位图数据） |
| 0x14 | 下行 | 剪贴板文本 |
| 0x15 | 双向 | 分辨率变更 |
| 0x1F | 上行 | 客户端信息（分辨率、色深） |

## 主要挑战

| 挑战 | 说明 | 应对 |
|------|------|------|
| 协议复杂度 | RDP 比 SSH 复杂 10 倍 | gopher-rdp 已实现 ~20 个协议子包 |
| 带宽性能 | 原始位图传输带宽大 | 使用 RDPGFX 图形管线 + 编解码压缩 |
| NLA 认证 | 需要 NTLMv2/CredSSP | gopher-rdp 已处理 |
| 键盘映射 | 浏览器 keycode → RDP scancode | 需实现布局映射表（先支持美式布局） |
| 库成熟度 | 不如 FreeRDP 经过实战检验 | 边界情况需测试不同 Windows 版本 |

## 工作量预估

| 阶段 | 内容 | 时间 |
|------|------|------|
| 基础桌面查看 | RDP 连接 + Canvas 渲染 + 鼠标操作 | 2-3 周 |
| 键盘输入 | 完整键盘映射（含组合键） | 1 周 |
| 剪贴板同步 | 双向剪贴板重定向 | 3-5 天 |
| 高级功能 | 音频、磁盘映射、打印机 | 各 1-2 周 |

## 结论

技术上可行，中等工作量。gopher-rdp 提供了纯 Go 的完整 RDP 协议栈，集成模式与现有 WebSSH 完全一致。核心难点在于 RDP 协议本身的复杂性和浏览器端的输入映射。建议从基本桌面查看 + 鼠标操作开始，逐步扩展键盘和高级功能。

## 参考资料

- [gopher-rdp (pkg.go.dev)](https://pkg.go.dev/github.com/bouncyball-git/gopher-rdp)
- [go-rdp (rcarmo)](https://github.com/rcarmo/go-rdp)
- [grdp (nakagami)](https://github.com/nakagami/grdp)
- [MS-RDPBCGR 规范](https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpbcgr)
- [Apache Guacamole](https://guacamole.apache.org/)
