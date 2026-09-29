# 传输层加密平滑升级方案（借鉴 orbien 的机会式 TLS）

> **状态**：方案设计，待评审后实施
> **前置**：SEC-01 认证层已收口（R1→R2 完成，生产 9 节点全量 v0.8.0，`legacy_format_enabled: false` 已生效）
> **参照实现**：`E:\GitHub\orbien`（Rust，v3.8.0）`core/src/transport/tls.rs`、`core/src/auth/token.rs`
> **目标**：在**低破坏**前提下，为 mole 的控制连接与隧道流量补齐传输层加密，做到「零配置也能加密、可灰度、可回退」

## 一、问题现状（已核实代码）

认证层已安全（proof 认证，token 不上线），但**传输层仍明文**：

| 项 | 现状 | 证据 |
|---|---|---|
| 服务端 TLS | 存在但**必须手配证书**，无自动生成，无 CA 校验配置 | `cmd/moleagent-serv/main.go:160-171`：`LoadX509KeyPair` 失败即 `os.Exit(1)`；**无自签兜底** |
| 客户端 TLS | `-tls` 只置 `UseTLS=true`，走**严格证书校验** | `cmd/moleagent-client/main.go:107`；`internal/transport/dialer.go:84-86` 用 `tls.DialWithDialer` 默认校验策略 |
| 客户端 `InsecureSkipVerify` | 字段**存在但未接线** | `internal/transport/dialer.go:69-71` 定义；main.go 无对应 flag 赋值 |
| KCP | 明确不支持标准 TLS | `internal/config/config.go:331-332`：kcp + tls.enabled 直接报错，引导用 `kcp.key` |
| WS | 支持 wss（TLSConfig 非 nil） | `internal/tunnel/ws_transport.go:19-32` |
| p2p 隧道 | 已有端到端加密（room 派生密钥） | 相对 orbien 的**优势**，本方案不动 |

**结论**：现在启用 TLS 是「硬切换」——服务端开 TLS 后所有未配证书、未加 `-tls` 的节点立即失联，且自签证书因客户端强制校验而**根本用不了**。这就是本轮讨论中「需要全舰队同步窗口」的根因。

## 二、orbien 的可借鉴要点（含不照搬的部分）

| 维度 | orbien 做法 | mole 是否借鉴 |
|---|---|---|
| **默认加密** | 客户端 `tls.enable` 默认 `true`；服务端默认备好 acceptor | ✅ 借鉴，但**分阶段**（见第四节），不搞突然默认 |
| **机会式 TLS** | 裸 TCP 先起流，服务端 peek 首字节 `0x16` 判断是否 TLS，是才包裹 | ✅ 借鉴（这是「同端口兼容新旧」的关键） |
| **默认不验证书** | 不填 CA 时用 `SkipServerVerification`（rustls 版 InsecureSkipVerify） | ✅ 借鉴（拿到防窃听；身份交由已有 proof 认证层） |
| **自动自签** | `certFile/keyFile` 留空即 rcgen 临时自签，两端零证书即可加密 | ✅ 借鉴（但**自签须持久化**，不像 orbien 每次重启换） |
| **灰度开关** | 服务端 `force` 默认 false，非 TLS 首字节静默放行明文 | ✅ 借鉴，但**必须补 WARN 日志**（orbien 缺此告警） |
| **QUIC 强制加密** | QUIC 内建 TLS1.3，不可关 | ⛔ 不适用（mole 无 QUIC） |
| **KCP** | KCP→TLS→mux | ⚠️ 暂不做（mole 现为 kcp.key 对称加密，改动面大） |
| **端到端加密** | 无，完全依赖传输层 | ⛔ 不照搬（mole 的 p2p E2E 是优势，保留） |
| **防重放** | 时间戳 HMAC + replay cache，但**数据连接路径漏挂** | ✅ 借鉴做法，⚠️ 避开其疏漏 |
| **空 token 放行** | `token` 为空直接 `Ok(())` 放行认证 | ⛔ 反面教材（mole 无 token 拒绝启动，更稳，保持） |

## 三、方案设计

### 3.1 核心模式：机会式 TLS（opportunistic TLS）

服务端在控制端口**同时接受** TLS 与明文两种首包：

```
客户端连接 → 首字节判断
  ├─ 0x16 (TLS ClientHello) → 走 TLS 握手 → 后续按现有 smux 流程
  └─ 其他（明文 smux/JSON）  → 明文放行（记 WARN：明文连接）
```

这样**同一端口**新旧客户端都能连，无需双端口、无需同时切换。

### 3.2 服务端改动

| # | 改动 | 文件 | 说明 |
|---|---|---|---|
| S1 | 新增 `tls.auto_self_signed`（默认 **false**，兼容期显式开启） | `internal/config/config.go` | 证书文件缺失时自动生成并**持久化落盘**（`data/tls/`），重启复用 |
| S2 | 新增自签生成（`crypto/ecdsa` P-256 + `x509.CreateCertificate`，标准库，不引依赖） | 新文件 `internal/tunnel/selfcert.go` | 生成 CN=配置域名或 `mole-server`，SAN 含 IP/域名 |
| S3 | 机会式监听：`peek` 首字节分支 | `internal/tunnel/transport.go` | 用 `bufio.Reader.Peek(1)`；TLS 分支用 `tls.Server`，明文分支把 Peek 缓冲接回 |
| S4 | 新增 `tls.force`（默认 **false**）：true 时非 TLS 首字节直接断开 | 同上 | 舰队收敛后翻 true 即「强制加密」收口 |
| S5 | 明文放行时记 **WARN**（含 remote addr） | `internal/tunnel/control.go` | orbien 的缺陷，mole 补上（可观测性） |
| S6 | 新增 `tls.client_ca_file`（可选，非空即要求客户端证书 mTLS） | `internal/config/config.go` | 高安全场景的可选升级路径 |

### 3.3 客户端改动

| # | 改动 | 文件 | 说明 |
|---|---|---|---|
| C1 | `-tls` 语义扩展为三档：`off` / `auto`（默认）/ `strict` | `cmd/moleagent-client/main.go` | 兼容旧 `-tls`（bool）——见 3.4 兼容处理 |
| C2 | `auto` 档：发起 TLS 但不校验证书（接线既有 `InsecureSkipVerify` 字段） | `internal/transport/dialer.go` | **现有字段已存在，只是没接线**，改动极小 |
| C3 | `strict` 档：校验证书（可配 CA / server_name） | 同上 + config | 沿用现有严格路径 |
| C4 | 连接失败降级：TLS 握手被拒（旧服务端）时自动回落明文并记 WARN | `internal/transport/dialer.go` | **保证新客户端连旧服务端不炸**（对应用户「低破坏」要求） |
| C5 | KCP 保持现状（`kcp.key`），文档明确其独立加密路径 | — | 不做协议改造 |

### 3.4 兼容矩阵（关键：双向兼容）

| 客户端 \ 服务端 | 旧服务端（无 TLS 支持） | 新服务端 `force=false` | 新服务端 `force=true` |
|---|---|---|---|
| **旧客户端**（≤v0.8.0） | ✅ 明文（现状） | ✅ 明文放行 + WARN | ❌ 拒绝（收口后的预期行为） |
| **新客户端 `-tls=auto`** | ✅ 自动回落明文 | ✅ **加密** | ✅ 加密 |
| **新客户端 `-tls=off`** | ✅ 明文 | ⚠️ 明文 + WARN | ❌ 拒绝 |
| **新客户端 `-tls=strict`** | ❌ 握手失败（同旧行为） | ✅ 加密+验证 | ✅ 加密+验证 |

`-tls` 旗标兼容：旧二进制传 `-tls`（无值，bool）→ 新客户端解析为 `strict`（保持旧语义：旧 `-tls` 就是严格校验）；新值 `auto/off` 为新能力。

## 四、分阶段实施计划（低破坏优先）

### 阶段 T0：验证（WSL，不改生产）
1. 服务端加 S1/S2/S3/S5，客户端加 C1/C2/C4（TDD：先写机会式监听的单测——`0x16` 首字节走 TLS、其他走明文、`force=true` 拒绝明文）
2. WSL 自签证书端到端：新客户端 `auto` 连新服务端 → 抓包确认无明文 token/隧道内容
3. **兼容性矩阵全跑**（上表 12 格）：特别验证「新客户端 auto ↔ 旧服务端」回落、「旧客户端 ↔ 新服务端 force=false」放行
4. 复用现有 E2E harness（17 场景）扩展 TLS 场景，产出证据后才进 T1

### 阶段 T1：服务端部署（灰度，零客户端改动）
- 生产升级服务端（`auto_self_signed: true`，`force: false`），**客户端一行不改**
- 观察：9 节点仍以明文放行（WARN 日志出现属预期），服务稳定
- 回滚：改回配置重启即可（无异构数据）

### 阶段 T2：客户端推进加密（分批）
- 客户端发新版本（`-tls` 默认 **auto**——新装/升级即自动加密，旧服务端自动回落所以不会炸）
- 存量节点：**启动参数不变**，靠默认值切换（这是「低破坏」的核心：不要求用户改命令）
- 观察：服务端日志中该节点从「明文放行 WARN」变为无 WARN（已加密）

### 阶段 T3：收口（可选，需决策）
- 全部节点确认加密后，服务端 `force: true` + 重启 → 明文路径关闭
- 同 R2 收口的做法：先确认覆盖，再翻开关；翻前用旧客户端主动探测验证拒绝路径（复用本轮 R2 的探测方法）

## 五、风险与对策

| 风险 | 对策 |
|---|---|
| 机会式 Peek 引入首包解析 bug | TDD 覆盖所有首字节分支；`force=false` 时容错优先（宁可放行不误杀） |
| 自签证书导致用户误以为「有验证」 | 文档/日志明确：auto 档只加密**不验证身份**，要防中间人须 strict + CA |
| 服务端重启换证书致客户端 strict 失败 | **自签持久化落盘**（不学 orbien 每次换） |
| `-tls` 语义变更破坏存量脚本 | 旧 bool 语义映射为 `strict`，保持行为；新值仅新增 |
| WARN 日志刷屏 | 按 remote IP 去重/限频（每 IP 每 5 分钟一条） |
| 性能：TLS 加解密开销 | 隧道场景吞吐非瓶颈；服务端 CPU 实测后再决定是否需 session resumption |

## 六、明确不做（含理由）

| 项 | 理由 |
|---|---|
| 端到端加密改造 | mole 的 p2p room 派生密钥已是 E2E，属优势，不向 orbien 的纯传输层依赖看齐 |
| 控制通道 ACME/Let's Encrypt | orbien 的 ACME 仅用于 desktop 域名证书，控制通道用它属过度设计；用户已有域名可直接配正式证书走 strict |
| KCP 协议改造 | 现有 `kcp.key` 对称加密可用，改造面大收益有限，独立议题 |
| QUIC 传输 | mole 无此传输，不引入 |
| 时间戳 HMAC 替换现有 challenge-response | 现有 challenge 机制**优于** orbien 的时间戳+replay cache（无时间窗依赖、天然防重放），不降级 |

## 七、验收标准

1. 兼容矩阵 12 格全部实测通过（WSL，含旧/新二进制交叉）
2. T1 后生产零中断：9 节点全部在线，无认证失败
3. T2 后目标节点确认加密（服务端无「明文放行」WARN）
4. 抓包证明：加密档位下控制连接与隧道载荷中**不含明文 token 与明文业务数据**
5. 全过程可回滚：任一阶段改配置/回退二进制即可恢复
