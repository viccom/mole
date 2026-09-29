# 传输层加密平滑升级方案（借鉴 orbien 的机会式 TLS）

> **状态**：方案设计 v2（已按 2026-09-29 二轮对抗审查修订：R1 peek 协议适配、R2 旗标兼容、R3 kcp×auto 冲突等，见修订记录）
> **前置**：SEC-01 认证层已收口（R1→R2 完成，生产 9 节点全量 v0.8.0，`legacy_format_enabled: false` 已生效）
> **参照实现**：`E:\GitHub\orbien`（Rust，v3.8.0）`core/src/transport/tls.rs`、`core/src/auth/token.rs`（其 TLS 门控逻辑无官方测试，参照结论均已逐条对抗复核并附证据）
> **目标**：在**低破坏**前提下，为 mole 的控制连接与隧道流量补齐传输层加密，做到「零配置也能加密、可灰度、可回退、无静默降级」

## 一、问题现状（已核实代码）

认证层已安全（proof 认证，token 不上线），但**传输层仍明文**：

| 项 | 现状 | 证据 |
|---|---|---|
| 服务端 TLS | 存在但**必须手配证书**，无自动生成，无 CA 校验配置 | `cmd/moleagent-serv/main.go:160-171`：`LoadX509KeyPair` 失败即 `os.Exit(1)`；**无自签兜底** |
| 客户端 TLS | `-tls` 只置 `UseTLS=true`，走**严格证书校验** | `cmd/moleagent-client/main.go:44,107`；`internal/transport/dialer.go:84-86` |
| 客户端 `InsecureSkipVerify` | 字段**存在但未接线** | `internal/transport/dialer.go:69-71` 定义；main.go 无对应 flag 赋值 |
| **服务端 wss** | **坏路径**：`newWSListener` 构造的 `httpServer` 从未挂 `TLSConfig`，`ServeTLS(ln,"","")` 在无证书时必败，WS 监听不服务；测试全用 nil TLS | `internal/tunnel/ws_transport.go:60-73`；`ws_transport_test.go` 仅 `newWSListener(ln, nil)` |
| **明文协议谁先开口** | **服务端先开口**：连接建立即写 32 字节 challenge；客户端先 `io.ReadFull` 等 challenge（预算 `DefaultAuthTimeout=10s`） | `internal/tunnel/control.go:383-394`；`internal/transport/dialer.go:26,230-236` |
| KCP | 明确不支持标准 TLS | `internal/config/config.go:331-332` |
| p2p 隧道 | 已有端到端加密（room 派生密钥） | 相对 orbien 的**优势**，本方案不动 |

**结论**：现在启用 TLS 是「硬切换」——服务端开 TLS 后所有未配证书、未加 `-tls` 的节点立即失联，且自签证书因客户端强制校验而**根本用不了**。

## 二、orbien 的可借鉴要点（含不照搬的部分，均已对抗复核）

| 维度 | orbien 做法 | mole 是否借鉴 |
|---|---|---|
| **默认加密** | 客户端 `tls.enable` 默认 `true`；服务端默认备好 acceptor | ✅ 借鉴，但**分阶段**（见第四节） |
| **机会式 TLS** | 服务端 peek 首字节 `0x16` 判断是否 TLS | ✅ 借鉴，但 **mole 必须改为「peek 带超时」**（见 3.1——orbien 成立的前提是客户端先说话，mole 明文是服务端先说话） |
| **默认不验证书** | 不填 CA 时 `SkipServerVerification`（无任何运行时告警） | ✅ 借鉴（拿到防窃听；身份交由已有 proof 认证层），但 **mole 补告警日志**（orbien 缺失） |
| **自动自签** | `certFile/keyFile` 留空即 rcgen 临时自签（内存，不落盘，每次重启换身份） | ✅ 借鉴，但**持久化落盘** |
| **灰度开关** | 服务端 `force` 默认 false，非 TLS 静默放行明文（连日志都没有） | ✅ 借鉴 + **必须 WARN**；另注：orbien 的明文路径是其 Java 客户端（无 TLS）的生态依赖，非死代码 |
| **mTLS 联动** | 服务端配 CA 即自动 `force=true`（mTLS 不允许明文回退） | ✅ 直接借鉴 |
| **超时防护** | **全链路零超时**（peek/TLS accept/WS 探测均无 deadline，慢速攻击可无限堆积 task+fd）——orbien 真实缺陷 | ⛔ 不照搬：mole 的 peek 与 TLS 握手**必须带 deadline**（见 S3） |
| **QUIC 强制加密** | QUIC 内建 TLS1.3，无任何可关加密的开关 | ⛔ 不适用（mole 无 QUIC） |
| **KCP** | KCP→TLS→mux | ⚠️ 暂不做（mole 现为 kcp.key 对称加密，改动面大） |
| **端到端加密** | 无，完全依赖传输层 | ⛔ 不照搬（mole 的 p2p E2E 是优势，保留） |
| **空 token 放行** | `token` 为空直接放行认证 | ⛔ 反面教材（mole 无 token 拒绝启动，保持） |
| **WS 层序** | TLS-in-WS（先明文 HTTP 升级再包 TLS，升级头明文可见，无 wss://） | ⛔ 不照搬：mole WS 走标准 **wss 层序**（TLS 先行，HTTP 升级在 TLS 内） |
| **时间戳 HMAC** | 认证为 HMAC(token, timestamp) + replay cache | ⛔ 不替换现有 challenge-response（无时间窗依赖、天然防重放，更优） |

## 三、方案设计

### 3.1 核心模式：机会式 TLS（**peek 带超时**——mole 协议适配版）

orbien 的 peek 模式成立前提是「明文客户端先说话」（yamux 首字节 0x00 / Login 'A' / WS 'G'）。**mole 不满足此前提**：明文协议是服务端先开口（连上即发 challenge），明文客户端不发首字节。若照抄 orbien 的阻塞 peek，每条明文连接将永久挂死并占住 `connectionWorker`（worker 池同步处理，`control.go:349-353`），**T1 上线即全舰队失联**。因此 mole 的判定规则是：

```
accept 后（限流检查之后）：
  SetReadDeadline(now + N)   ← N 默认 1.5s，可配 tls.peek_timeout
  读 1 字节：
  ├─ 0x16 (TLS ClientHello) → 清 deadline → TLS 握手（自带 10s deadline）
  │                            → 后续按现有 smux 流程（bufio 在 tls.Conn 上重建）
  ├─ 其他字节                → 按 TLS 视角即非法；force=true 拒绝；
  │                            force=false 按「明文」处理（回注该字节、发 challenge，
  │                            让后续认证路径自然处理垃圾/扫描流量——宁可放行不误杀）
  └─ 超时（无字节）          ← 明文客户端的正常形态（它不发首字节）
                              → 清 deadline → 发 challenge → 现有明文流程
                              （force=true 时超时即拒，否则 force 无意义）
```

**约束与代价**：
- **N ≪ 10s** 认证预算（`DefaultAuthTimeout`）：默认 1.5s（正常 ClientHello 到达 ≈1 RTT；高延迟移动网 <1s），可配。过渡期每条明文连接建立延迟 +N——旧客户端预算内（10s），9 节点场景可忽略，T0 实测确认
- **慢 ClientHello 误判**（>N 才到）：被判明文 → challenge 写入 TLS 流 → 客户端握手失败 → 5s 后重连重试 TLS，自愈；病态链路（持续 >N RTT）会循环——诊断手段：明文分支收到疑似 TLS 开头数据时记诊断日志。T0 必测此场景
- **无 0x16 歧义**：mole 明文路径根本不发首字节，比 orbien 更干净（orbien 靠明文首字节空间避开 0x16，且其自身在 HTTPS 网关处拒绝与控制口共听 0x16 流量）
- **peek 必须只作用于 tcp/ws 路径**：KCP 的 0x30 探测字节（`ws_kcp_dialer.go:201`）不得进 peek 逻辑
- **与限流的顺序**：`authLimiter.Allowed(ip)` 在一切之前（现状 `control.go:362-367`），peek 在限流**之后**（锁定 IP 不值得花 peek 等待）

### 3.2 服务端改动

| # | 改动 | 挂点/文件 | 说明 |
|---|---|---|---|
| S1 | 新增 `tls.auto_self_signed`（默认 **false**，兼容期显式开启） | `internal/config/config.go` | 证书文件缺失时自动生成并**持久化落盘**（`data/tls/`，私钥 0600、目录 0700），重启复用；自签有效期取长（如 10 年——auto 档不验书，减少轮换烦恼）；CN/SAN=`mole-server` + 本机非回环 IP，可配 `tls.cert_dns` 追加 |
| S2 | 自签生成（`crypto/ecdsa` P-256 + `x509.CreateCertificate`，标准库零依赖） | 新文件 `internal/tunnel/selfcert.go` | |
| S3 | 机会式判定（3.1 的 peek 带超时） | **`handleConnection` 内、challenge 写出之前**（`control.go:389` 前）；明文分支用 bufio 回注 peek 字节（现有 `bufferedConn` 先例 `control.go:473,1255-1264`）；TLS 分支 `tls.Server(conn,cfg)` 后**先 SetDeadline(10s) 再 Handshake()**（防慢速握手占死同步 worker） | 现有 `TCPTransport.Listen` 的硬 TLS 分支（`transport.go:37-39`）保留给纯 TLS 部署 |
| S4 | 新增 `tls.force`（默认 **false**）：true 时非 0x16（含 peek 超时）一律拒绝 | 同上 | 舰队收敛后翻 true 即「强制加密」收口 |
| S5 | 明文放行记 **WARN**（含 remote addr，按 IP 限频：每 IP 每 5 分钟一条） | `internal/tunnel/control.go` | orbien 的缺陷，mole 补上 |
| S6 | 新增 `tls.client_ca_file`（可选，非空即 mTLS：`ClientAuth: RequireAndVerifyClientCert`）；**非空时强制 `force=true`**（对齐 orbien：mTLS 不允许明文回退） | `internal/config/config.go` | mTLS 仅配 `strict` 档客户端（auto 档无客户端证书，对 mTLS 服务端握手必败） |
| S7 | **WS 端口机会式改造 + 修复现状坏路径**：listener 层包 peek（Accept 时 peek 首字节，0x16 → `tls.Server`，否则原样），`httpServer` 一律 `Serve()`；主传输 `transport: "ws"`（9981）与额外监听 `ws_port`（9988）同样适用 | `internal/tunnel/ws_transport.go`（重做 `newWSListener` 的 TLS 分支——现状 `ServeTLS(ln,"","")` 必败属 bug，见 §一） | WS 客户端先开口（HTTP GET / TLS ClientHello），peek 天然成立；仍带 deadline 防慢速 |
| S8 | 配置校验联动：`enabled && auto_self_signed` 时 cert/key 可空（放宽 `config.go:316-323` 的必填校验）；`client_ca_file` 非空 → force 置 true 并校验冲突 | `internal/config/config.go` | |
| S9 | 升级提示：**存量 `tls.enabled=true` 部署**升级后 force 默认 false 会放宽为接受明文（比升级前弱）——启动日志检测到此形态时提示「建议同步设 force=true」 | `cmd/moleagent-serv/main.go` | |

### 3.3 客户端改动

| # | 改动 | 文件 | 说明 |
|---|---|---|---|
| C1 | `-tls` 扩展三档 `off`/`auto`/`strict`（新默认 **auto**），用**自定义 `flag.Value` + `IsBoolFlag()`**：裸 `-tls`（旧脚本形态）→ `strict`；`-tls=auto/off/strict` 走 `=` 形式；**`-tls auto`（空格分隔）不识别，文档必须注明用 `=`** | `cmd/moleagent-client/main.go:44`（现为 `flag.Bool`） | 标准库 bool flag 改 string 后裸 `-tls` 会报 `flag needs an argument`，必须自定义 Value |
| C1b | 配置文件 `"tls"` 字段三态：兼容旧 bool（`true`→strict、`false`→off）与新字符串（auto/off/strict），**缺省 → auto**（自定义 UnmarshalJSON 或 `*bool`+字符串联合体） | `config.go:22`（现为裸 bool，无法区分缺省与 false） | T2「存量节点零改动靠默认值切换」依赖此三态 |
| C2 | `auto` 档：发起 TLS 但不校验证书（接线既有 `InsecureSkipVerify`），启动日志注明「加密未验证身份」 | dialer.go + **client.go:130-153 的 dialer 装配点**（WS 分支现在自建 `&tls.Config{}`，不走 `transport.TLSConfig`——三档接线必须统一到装配层） | |
| C3 | `strict` 档：`TLSConfig` 扩展 `RootCAs`/`ServerName`/客户端证书对字段（现仅 Enabled/InsecureSkipVerify，**私有 CA 不支持**） | `internal/transport/dialer.go` + config | 「沿用现有严格路径」低估了改动——现有严格= 系统根校验 |
| C4 | **禁止静默降级**（安全红线）：`auto`/`strict` 握手失败即连接失败，走重连重试（固定 5s 间隔，可接受），错误日志带档位信息便于区分「服务端未升级」与「网络抖动」 | `internal/transport/dialer.go` | ⚠️ 攻击者丢包即可诱导降级（见 5.1） |
| C5 | WS 路径：auto/strict 档走 **wss 层序**（TLS 先行、HTTP 升级在 TLS 内；地址可保持 `ws://` 由客户端按档位自动套 TLS，不必改 scheme） | `internal/transport/ws_kcp_dialer.go`（wss 支持已有，`ws_kcp_dialer.go:35-52` 有跳过校验告警先例） | 服务端对应 S7 |
| C6 | KCP 保持现状（`kcp.key`）；**`kcp` 传输 × `auto` 档 → 自动降级为 `off` 并记 WARN（不报错）**；仅 `kcp × strict` 维持现有报错 | `config.go:181-183`（现 kcp+UseTLS 直接 Validate 失败 → 启动 Fatal，若不处理默认 auto 会**当场打断所有 kcp 节点启动**） | |

### 3.4 兼容矩阵（双向兼容，且**默认路径无静默降级**）

| 客户端 \ 服务端 | 旧服务端（无机会式 TLS） | 新服务端 `force=false` | 新服务端 `force=true` |
|---|---|---|---|
| **旧客户端**（≤v0.8.0） | ✅ 明文（现状） | ✅ 明文放行（peek 超时路径，+N ms 建连延迟）+ WARN | ❌ 拒绝（收口后的预期行为） |
| **新客户端 `-tls=auto`** | ❌ **连接失败**（须显式 `-tls=off` 或先升级服务端） | ✅ **加密** | ✅ 加密 |
| **新客户端 `-tls=off`** | ✅ 明文 | ⚠️ 明文 + WARN | ❌ 拒绝 |
| **新客户端 `-tls=strict`** | ❌ 握手失败 | ✅ 加密+验证（含 mTLS） | ✅ 加密+验证 |

> **与早期草案的关键差异**：取消了「`auto` 档连旧服务端自动回落明文」——静默回落即降级攻击面（见 5.1）。代价是「新客户端 + 旧服务端」不再开箱可用，因此**部署顺序必须服务端先行**（T1 先于 T2）。
> `-tls` 旗标兼容见 C1（自定义 flag.Value，裸 `-tls`→strict）；配置文件三态见 C1b。

### 3.5 范围边界（明确不覆盖的端口与理由）

TLS 只作用于**节点控制口**（TCP 9981 / ws 9988）。**节点与服务端之间的全部载荷都在这上面**——TCP/HTTP/UDP 隧道转发与 WebSSH 节点段全部走节点 smux 会话（`internal/tunnel/tcp.go:94-104`、`http.go:251-257,340-352`、`udp.go:184-190`；WebSSH 由 API 侧桥到节点 stream）。以下**保持明文，属设计使然，不在本方案范围**：

| 端口 | 用途 | 说明 |
|---|---|---|
| 9980 网关 | 外部访问者 → 服务端 | 访问者无法被要求 TLS（公开入口）；访问者↔服务端段明文是隧道产品固有形态 |
| 9983 API/管理 | 浏览器/管理 | 独立议题（可配反代 + HTTPS） |
| 1883/1882 MQTT | 明文 MQTT | SEC-11 遗留（已有明文 WARN），独立议题 |
| KCP 9981/UDP | `kcp.key` 路径 | 维持现状（C6） |
| p2p 隧道 | 端到端 | 已有 room 派生密钥，不动 |

## 四、分阶段实施计划（低破坏优先）

### 阶段 T0：验证（WSL，不改生产）
1. 服务端 S1-S9、客户端 C1-C6（TDD：peek 超时三分支〔0x16/其他字节/超时〕、force 拒绝、明文 WARN 限频、自签生成与持久化、wss listener 修复、flag 三态与配置三态、kcp×auto 降级）
2. WSL 端到端：新客户端 auto 连新服务端 → 抓包确认无明文 token/隧道内容（**server↔client 段**）
3. **兼容矩阵全跑**（3.4 的 12 格）：特别验证「新客户端 auto ↔ 旧服务端」**必须失败（不回落）**、「旧客户端 ↔ 新服务端 force=false」放行（含 +N 延迟实测）
4. 专项：慢 ClientHello（人为延迟 >N）误判后的自愈；peek 超时边界（N 与 10s 预算）；阻断 TLS 握手（iptables DROP）客户端必须失败
5. 复用 WSL 侧 E2E harness（`_release/e2e/e2e_run.sh`，现 17 场景：R1 12 场景 + R2 增量 sc13-sc17）扩展 TLS 场景

### 阶段 T1：服务端部署（**必须最先**，零客户端改动）
- 生产升级服务端（`auto_self_signed: true`，`force: false`），**客户端一行不改**
- 观察：9 节点仍以明文放行（WARN 日志出现属预期），服务稳定；节点建连延迟 +N 属预期
- 回滚：改回配置重启即可（无异构数据）
- **为什么必须先行**：客户端 `auto` 档不回落明文，客户端先升而服务端未就绪会失联

### 阶段 T2：客户端推进加密（分批）
- 服务端 T1 确认稳定后，发布客户端新版本（默认 `-tls=auto`；**发布说明必须写明：服务端须已升级，否则用 `-tls=off`**）
- 存量节点：**启动参数不变**，靠默认值切换；kcp 节点自动降级 off（WARN）不受损
- 观察：服务端日志中该节点从「明文放行 WARN」变为无 WARN（已加密）
- 个别节点握手失败：**显式** `-tls=off` 临时放行，而非自动降级

### 阶段 T3：收口（可选，需决策）
- 全部节点确认加密后，服务端 `force: true` + 重启 → 明文路径关闭
- 复用 R2 收口方法论：先确认覆盖，再翻开关；翻前用旧客户端主动探测验证拒绝路径

## 五、风险与对策

### 5.1 降级攻击（头号风险）

| 路径 | 机制 | 判定 | 对策 |
|---|---|---|---|
| **客户端自动回落**（早期草案 C4，已删除） | TLS 失败 → 静默重发明文；攻击者丢包即触发；auto 档不验证书**无法区分**「旧服务端」与「攻击者」 | 🔴 不可接受 | C4 红线：握手失败即失败（orbien 同款，已复核其无回落：`connector.rs:39-44` 失败即 Err，重试用同一配置 `service.rs:106-139`） |
| **服务端放行明文**（`force=false`） | 服务端按 peek 结果放行非 TLS 连接 | 🟡 过渡期可接受（显式策略，非被诱导），但期间体系仍可被降级 | WARN 可观测 + T3 硬期限收口；**对外表述诚实**：`force=false` 阶段不宣称「已强制加密」 |

**关键认知**：只要 `force=false`，攻击者总能冒充「不支持 TLS 的旧客户端」让服务端放行明文——加密是**尽力而为**而非**保证**。真正的安全保证在 T3 之后；防中间人须再配 `strict` 档验书。proof 认证在明文通道下只防 token 泄漏与重放，**不防 MITM 冒充服务端**，不能作明文兜底。

### 5.2 其他风险

| 风险 | 对策 |
|---|---|
| peek 超时误判（慢 ClientHello） | N 默认 1.5s 可配；误判后 5s 重连自愈；明文分支遇疑似 TLS 数据记诊断日志；T0 必测 |
| peek/握手无超时占死 worker（orbien 的真实缺陷） | peek 带 N 超时 + TLS 握手 10s deadline（S3）；T0 慢速连接测试 |
| 明文连接建连延迟 +N（过渡期） | 旧客户端 10s 预算内；T0 实测增量；T3 后消失 |
| 自签导致误以为「有验证」 | auto 档启动日志注明「加密未验证身份」；防中间人须 strict+CA |
| 重启换证书致 strict 失败 | 自签**持久化落盘** |
| `-tls` 语义变更破坏存量脚本 | C1 自定义 flag.Value（裸 `-tls`→strict）+ C1b 配置三态；空格形式不可用须文档注明 |
| kcp 节点在默认 auto 下启动失败 | C6：kcp×auto 自动降级 off + WARN，仅 strict 报错 |
| 不回落导致「客户端先升、服务端未升」失联 | 部署顺序固定服务端先行；发布说明写明 |
| 存量 `tls.enabled=true` 部署升级后被放宽 | S9 启动检测并提示设 force=true |
| WARN 刷屏 | 按 IP 限频（每 IP 每 5 分钟一条），单元测试覆盖 |
| 性能：TLS 加解密开销 | 隧道场景非瓶颈；rustls 默认会话恢复仅同连接池内生效，Go 默认开启 session tickets；T0 实测吞吐 |

## 六、明确不做（含理由）

| 项 | 理由 |
|---|---|
| 端到端加密改造 | mole 的 p2p room 派生密钥已是 E2E，属优势，不向 orbien 的纯传输层依赖看齐 |
| 控制通道 ACME/Let's Encrypt | orbien 的 ACME 仅用于 desktop 域名证书；控制通道用它属过度设计；有域名可直接配正式证书走 strict |
| KCP 协议改造 | 现有 `kcp.key` 可用，改造面大收益有限 |
| QUIC 传输 | mole 无此传输 |
| TLS-in-WS 层序（orbien 式） | 升级头明文可见（元数据泄漏）；mole 走标准 wss 层序 |
| 时间戳 HMAC 替换 challenge-response | 现有机制无时间窗依赖、天然防重放，更优，不降级 |
| 网关/API/MQTT 端口 TLS | 见 3.5 范围边界（网关固有明文；API/MQTT 独立议题） |

## 七、验收标准

1. 兼容矩阵 12 格全部实测通过（WSL，含旧/新二进制交叉）
2. T1 后生产零中断：9 节点全部在线，无认证失败，建连延迟增量 ≤N+正常值
3. T2 后目标节点确认加密（服务端无「明文放行」WARN）
4. 抓包证明（**限定 server↔client 段**）：加密档位下控制连接与隧道载荷不含明文 token 与明文业务数据
5. **降级红线**：代码审查 + 实测（iptables DROP 握手包）确认客户端无「TLS 失败 → 自动回落明文」路径
6. **超时红线**：慢速连接（连上不发包）在 N+握手超时内被释放，不占死 worker；peek 三分支与慢 ClientHello 自愈有专项测试
7. **兼容红线**：裸 `-tls`（旧脚本）解析为 strict 不报错；配置文件 `"tls": true/false/缺省` 三态正确；kcp 节点默认 auto 下可启动（降级 off + WARN）
8. 全过程可回滚：任一阶段改配置/回退二进制即可恢复

## 修订记录

- v1（2026-09-29）：初稿（借鉴清单 + 机会式 TLS + T0-T3）
- v1.1（2026-09-29）：评审修正——删除客户端静默回落（降级攻击面）、部署顺序改服务端先行、新增 5.1
- v2（2026-09-29，二轮对抗审查后）：**R1** peek 改「带超时」三分支设计（mole 服务端先开口，照抄 orbien 会全舰队失联）；**R2** `-tls` 兼容改自定义 flag.Value + 配置文件三态（标准 flag 包裸 `-tls` 会报错）；**R3** 定义 kcp×auto 降级规则（否则默认 auto 打断 kcp 节点启动）；S7 修复服务端 wss 坏路径前提；S8/S9 校验与存量部署联动；C3 strict 档字段扩展；3.5 范围边界新节；T0/验收补超时、自愈、三态、kcp 专项
