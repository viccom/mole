# 客户端侧：传输层加密改造指引

> 完整方案见服务端仓库 `moleAgent_Serv/docs/plans/2026-09-29-transport-tls-upgrade.md`。
> 本文只列**客户端需要做的部分**，便于在客户端仓库独立实施与审查。

## 背景

SEC-01 认证层已收口（v0.8.0，proof 认证上线，token 不再明文传输）。但**传输层仍明文**：控制连接与隧道载荷可被窃听。本改造借鉴 orbien 的「机会式 TLS」，做到零配置加密、双向兼容、可灰度。

## 客户端改动清单

| # | 改动 | 文件 | 要点 |
|---|---|---|---|
| C1 | `-tls` 从 bool 扩展为三档 `off`/`auto`/`strict`（默认 `auto`） | `cmd/moleagent-client/main.go:107` | 旧 bool 语义映射为 `strict`，保持存量脚本行为 |
| C2 | `auto` 档接线**既有但未使用**的 `InsecureSkipVerify` 字段 | `internal/transport/dialer.go:69-71, 84-86` | 发起 TLS 但不校验证书（防窃听；身份仍由 proof 认证保证） |
| C3 | `strict` 档支持配置 CA / server_name | `internal/transport/dialer.go` + `config.go` | 防中间人的完整档位 |
| C4 | **TLS 握手失败自动回落明文**（连旧服务端不炸） | `internal/transport/dialer.go` | 低破坏的关键；回落记 WARN |
| C5 | WS 路径同步（`ws://` 上套 TLS，对齐服务端 wss） | `internal/transport/ws_kcp_dialer.go:32-52` | 已是「升级后字节流包 TLS」结构，改动小 |
| C6 | KCP 保持 `kcp.key` 现状，文档明确独立加密路径 | — | 不做协议改造 |

## 兼容性要求（必须实测）

| 客户端 \ 服务端 | 旧服务端 | 新服务端 force=false | 新服务端 force=true |
|---|---|---|---|
| 旧客户端 ≤v0.8.0 | ✅ 明文 | ✅ 明文放行 | ❌ 拒绝（预期） |
| `-tls=auto` | ✅ 回落明文 | ✅ 加密 | ✅ 加密 |
| `-tls=off` | ✅ 明文 | ⚠️ 明文+WARN | ❌ 拒绝 |
| `-tls=strict` | ❌ 握手失败 | ✅ 加密+验证 | ✅ 加密+验证 |

最关键的两格：**`auto` ↔ 旧服务端必须回落成功**（否则新客户端无法连未升级的服务端）；**旧客户端 ↔ 新服务端 force=false 必须放行**（否则服务端升级即断服）。

## 依赖顺序

客户端改造可先于服务端部署完成（因 `auto` 能回落明文），但要等**服务端 T1 上线**（`auto_self_signed: true`）后才有实际加密效果。服务端 T1 部署时客户端零改动。

## 注意

- 自签证书**只加密不验证身份**（`auto` 档）——文档与启动日志都要说清，避免用户误以为已防中间人；要防中间人须 `strict` + CA。
- 现有 `internal/transport/dialer.go:36-41`（WS）已有 `InsecureSkipVerify` 的告警先例，沿用其文案风格。
