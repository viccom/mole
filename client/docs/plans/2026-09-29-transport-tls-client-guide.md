# 客户端侧：传输层加密改造指引

> 完整方案见服务端仓库 `moleAgent_Serv/docs/plans/2026-09-29-transport-tls-upgrade.md`（v2）。
> 本文只列**客户端需要做的部分**，便于在客户端仓库独立实施与审查。

## 背景

SEC-01 认证层已收口（v0.8.0，proof 认证）。传输层仍明文：控制连接与隧道载荷可被窃听。本改造借鉴 orbien 的「机会式 TLS」，但有一处关键差异：**mole 明文协议是服务端先开口**（连上即收 challenge），所以服务端用「peek 带超时」判定（等首字节 1.5s：`0x16`→TLS，超时→按明文发 challenge）。客户端则始终**主动发起 TLS**（连上即 ClientHello）。

## 客户端改动清单

| # | 改动 | 文件 | 要点 |
|---|---|---|---|
| C1 | `-tls` 三档 `off`/`auto`/`strict`（默认 **auto**），**自定义 `flag.Value` + `IsBoolFlag()`**：裸 `-tls`（旧脚本）→ strict；`-tls=auto/off/strict` 走 `=` 形式；空格形式 `-tls auto` 不识别，文档注明 | `cmd/moleagent-client/main.go:44`（现为 `flag.Bool`，直接改 string 裸 `-tls` 会报 `flag needs an argument`） | 旧 bool 语义完整保留 |
| C1b | 配置文件 `"tls"` 三态：旧 bool（true→strict / false→off）+ 新字符串（auto/off/strict），**缺省→auto**（自定义 UnmarshalJSON；现为裸 bool 分不出缺省与 false） | `config.go:22` | 「存量节点零改动靠默认值切换」依赖此三态 |
| C2 | `auto` 档接线既有 `InsecureSkipVerify` 字段（发起 TLS 不验证书），启动日志注明「加密未验证身份」；**接线落点在 `client.go:130-153` 的 dialer 装配层**（WS 分支现自建 `&tls.Config{}` 不走 `transport.TLSConfig`，必须统一） | `internal/transport/dialer.go:69-71` + `client.go` | 防窃听；身份由 proof 认证保证 |
| C3 | `strict` 档扩展 `TLSConfig`：`RootCAs`/`ServerName`/客户端证书对（现仅 Enabled/InsecureSkipVerify，**私有 CA 不支持**，现有「严格」=系统根校验） | `internal/transport/dialer.go` + `config.go` | 防中间人完整档位 + mTLS |
| C4 | **禁止静默降级**（安全红线）：`auto`/`strict` 握手失败即连接失败，重连重试（5s 固定间隔，可接受）；错误日志带档位信息 | `internal/transport/dialer.go` | 攻击者丢包即可诱导降级；绝不自动回落明文。连旧服务端须**显式** `-tls=off` |
| C5 | WS 路径：auto/strict 走 **wss 层序**（TLS 先行、HTTP 升级在 TLS 内；地址保持 `ws://`，按档位自动套 TLS） | `internal/transport/ws_kcp_dialer.go:35-52`（wss 已支持，此处有跳过校验告警先例可参考文案） | 服务端 S7 同步重做 WS listener（现状服务端 wss 是坏路径：`ws_transport.go:60-70` 的 `ServeTLS(ln,"","")` 必败） |
| C6 | KCP 保持 `kcp.key`；**kcp × auto → 自动降级 off + WARN（不报错）**；仅 kcp × strict 维持现有报错 | `config.go:181-183` | 不处理则默认 auto 会让 kcp 节点 Validate 失败直接起不来 |

## 兼容性要求（必须实测）

| 客户端 \ 服务端 | 旧服务端 | 新服务端 force=false | 新服务端 force=true |
|---|---|---|---|
| 旧客户端 ≤v0.8.0 | ✅ 明文 | ✅ 明文放行（建连延迟 +1.5s 属预期） | ❌ 拒绝（预期） |
| `-tls=auto` | ❌ **连接失败**（须 `-tls=off` 或先升级服务端） | ✅ 加密 | ✅ 加密 |
| `-tls=off` | ✅ 明文 | ⚠️ 明文+WARN | ❌ 拒绝 |
| `-tls=strict` | ❌ 握手失败 | ✅ 加密+验证 | ✅ 加密+验证 |

两格最关键：

- **`auto` ↔ 旧服务端：必须「连接失败」而非回落明文**（降级攻击面：攻击者丢包诱导明文，`auto` 不验证书无法区分「旧服务端」与「攻击者」）
- **旧客户端 ↔ 新服务端 force=false 必须放行**（否则服务端升级即断服）；旧客户端连接会多等 1.5s（服务端 peek 超时），在 10s 认证预算内

## 降级与超时红线（审查时必须确认）

1. 客户端**不存在**「TLS 握手失败 → 重试明文」的任何代码路径
2. 模拟阻断 TLS（iptables DROP 握手包）：客户端必须**连接失败**并重试 TLS，不得明文连上
3. 服务端 `force=false` 期间是**过渡态**：明文放行必须记 WARN（按 IP 限频），不对外宣称「已强制加密」
4. 服务端 peek 与 TLS 握手必须带 deadline（peek 1.5s / 握手 10s）——orbien 全链路零超时是其真实缺陷，勿照抄

## 依赖顺序（不可颠倒）

**服务端 T1 必须先上线**（`auto_self_signed: true` + 机会式监听），再推客户端默认 `auto`。客户端不回落明文，若客户端先升级而服务端未就绪，节点直接失联。客户端发布说明必须写明：**服务端须已升级，否则显式 `-tls=off`**。
