# 传输通道加密实施文档：方案 A「双端口 + 证书」/ 方案 B「PSK 协议内升级」

> **状态**：技术实施方案（未实施）
> **前置**：SEC-01 已收口（全量 v0.8.0，`legacy_format_enabled: false` 生产生效）
> **关系**：两方案各自独立完整、互不需要对方；与 `2026-09-29-transport-tls-upgrade.md`（机会式 TLS 方案）为并列路线。本文不含方案间对比，采用哪个由部署场景决定。
> **共同范围边界**：网关 9980（访问者段）、API 9983、MQTT 1883/1882 保持现状；p2p 隧道已有端到端加密不动。
>
> **共同协议事实**（本文所有插入点均按此锚定，已逐条核实代码）：
> - 连接时序：服务端 accept 后立即写 32 字节 challenge（`control.go:383-394`）→ 客户端回认证行（先 `io.ReadFull` 等 challenge，`dialer.go:230-236`，预算 `DefaultAuthTimeout=10s`）→ 服务端回 ok 行 → 双方建 smux。
> - 服务端认证行读取已有 10s deadline（`control.go:395-412`：`SetReadDeadline` → `readBoundedLine` → 清除）。
> - 多监听架构现成：`controlSrv.AddTransport(port, transport)`（`main.go:198-202`，ws_port/kcp_port 即此模式）。
> - `TCPTransport.Listen` 已支持 TLS 包装且 `Name()` 返回 `"tls"`（`transport.go:30-45`）。
> - 客户端 `-tls`（`flag.Bool`，`main.go:44`）→ `DefaultDialer` → `tls.DialWithDialer` 系统根**严格校验**（`dialer.go:84-86`）——今天即可对接正式证书。
> - 服务端 proof 认证在匹配成功时手里持有该 token 的 32 字节 `sha256(token)`（`node_access_auth.go:93` `hex.DecodeString(token.TokenHash)`）。
> - 双端 JSON 解析均为标准 `json.Unmarshal`，**未知字段自动忽略**（新旧共存的基础）。

---

# 方案 A：双端口 + 证书

## A.1 目标形态

```
节点 ──(明文, 现状不动)──► :9981 控制口（过渡期保留，收口时关闭）
节点 ──(TLS 严格校验)───► :9984 控制口（新增，Let's Encrypt 证书）
```

- 9981 行为零变化：旧客户端、未迁移节点照常工作，无任何时间压力。
- 9984 为标准 TLS-over-TCP：客户端 `-server px.metme.top:9984 -tls`，服务端证书为 px.metme.top 的正式证书，客户端走现有严格校验路径（**无需自签、无需 skip-verify**，机密性+完整性+服务端身份认证三样齐备）。

## A.2 服务端改动点

| 文件 | 改动 | 细节 |
|---|---|---|
| `internal/config/config.go` | 新增 `Server.ControlTLSPort string yaml:"control_tls_port"`（默认空）+ 环境变量 `MA_CONTROL_TLS_PORT` | 校验规则：① `control_tls_port != ""` 时要求 `tls.cert_file/key_file` 非空（复用 316-323 现有校验形态）；② 与 `tls.enabled` **互斥**（`tls.enabled` 语义是"把 9981 本身包成 TLS"，与额外 TLS 口并列会产生歧义，直接报错）；③ 收口态支持 `control_port: ""`（仅当 `control_tls_port != ""` 时允许关闭明文口；沿用现有"无控制监听则启动失败退出码 1"路径） |
| `cmd/moleagent-serv/main.go` | ① 把 160-171 的证书加载提取为 `buildTLSConfig(cfg) *tls.Config`；② 新增接线：`if cfg.Server.ControlTLSPort != "" { controlSrv.AddTransport(cfg.Server.ControlTLSPort, tunnel.NewTCPTransport(tlsConfig)) }` | **零新传输代码**：`TCPTransport.Listen` 的 TLS 分支（`transport.go:38-39`）与 `Name()="tls"`（:43-44）现成，`AddTransport` 模式与 ws_port/kcp_port 相同 |
| `internal/tunnel/control.go` | **TLS 握手超时加固**：`handleConnection` 入口对 `*tls.Conn`（类型断言）设 `SetDeadline(now+10s)`，认证完成后清除（与现有 395-412 的读超时合并为同一窗口） | `tls.NewListener` 的握手是惰性的（首次 Read/Write 触发）：challenge 写出（:389）即触发握手，若客户端连上不回 ClientHello，**Write 会无限期挂住同步 worker**（worker 池 `NumCPU*2`，`control.go:349-353`）——慢速攻击可占满 worker 池致拒绝服务。此加固项必做 |
| `internal/tunnel/control.go` | 认证成功日志（:471 `Node authenticated` 行）增加连接加密标志 | 迁移期核对"哪些节点已加密"的唯一观测点；从 `conn.(*tls.Conn)` 断言即可，transportName 已自然显示 `tls` |
| `internal/core/listen_port.go` | 保留端口集合追加 `9984` | 防隧道 `listen_port` 撞控制 TLS 口（REL-01 同理）。**注意**：客户端 `tunnel.go` 的 `reservedListenPorts` 是手工副本（已知漂移风险点 G-4），两处同步改 |
| `internal/config/config.go`（校验段） | `tls.enabled && control_tls_port` 互斥报错文案 | 见上 |

不改动：`ws_transport.go`（wss 坏路径本方案不依赖，留待独立修复）、KCP、网关、API、MQTT。

## A.3 客户端改动点

**零代码改动。** 现有 `-tls` 旗标 + `dialer.go` 的 `tls.DialWithDialer` 严格校验即为目标路径；Let's Encrypt 证书经系统根直接验证通过。迁移仅是启动参数变化。

## A.4 实施与迁移路径

| 步骤 | 动作 | 验证 | 回滚 |
|---|---|---|---|
| 1 证书 | px.metme.top 签发 Let's Encrypt（现有 1panel/gen-cert.sh 体系），cert/key 落服务端（key 0600） | `openssl x509 -in cert -noout -dates -subject` | 重签 |
| 2 服务端 | 发版部署：config 配 `tls.cert_file/key_file` + `control_tls_port: ":9984"`，重启；防火墙放行 9984 | 启动日志双监听（tcp + tls）；9981 上 9 节点全部正常重连（复用 R2 收口后重启的观测方法） | 改回配置重启 |
| 3 逐节点迁移 | 经 WebSSH/SSH 改各节点启动参数为 `-server px.metme.top:9984 -tls`，重启进程；**不可达节点留在 9981，无时间压力** | 服务端日志该连接 `transport=tls`；节点隧道恢复 | 单节点改回 9981 |
| 4 收口 | 9/9 迁移后 `control_port: ""` 重启 | 明文连接被拒（端口已关）；用 9981 参数的客户端探测验证失败路径（复用 R2 方法论） | 恢复 control_port |

## A.5 测试要点

- 单测：config 校验矩阵（互斥/必填/收口态允许 control_port 空）；`AddTransport` 双 TCP 系监听并存 accept；TLS conn 的 deadline 加固路径（慢客户端不占死 worker）。
- E2E（扩展 WSL harness）：① 新客户端 `-tls` 连 9984 全链路（认证/注册/隧道回环/抓包 **server↔client 段**无明文）；② 明文客户端连 9984 → 失败（TLS 口拒明文）；③ 9981 明文全兼容回归（旧客户端基线不破）；④ 收口态探测；⑤ 隧道 listen_port=9984 被拒（保留端口）；⑥ 过期证书启动 fail-fast。
- 证书运维检查单：自动续期任务存在性 + 启动时剩余有效期日志告警（<14d WARN）。

## A.6 风险

| 风险 | 性质 | 对策 |
|---|---|---|
| TLS 握手惰性触发 + 同步 worker → 慢速攻击占满 worker 池 | 可用性（本方案引入的主路径） | A.2 的 deadline 加固**必做**；TDD 覆盖慢客户端场景 |
| 证书过期/续期失败 | 运营性 | 复用 1panel/LE 自动续期；启动校验 + 剩余天数告警；过期 fail-fast 拒绝启动（优于静默失效） |
| 防火墙漏放行 9984 | 部署性 | 上线检查单；迁移前先从外部探测 9984 |
| 逐节点改参数的人工成本 | 运营性 | 9 节点经 WebSSH 脚本化；单节点秒级中断、即时回滚；无窗口压力 |
| 双监听日志混淆 | 观测性 | transportName 已区分（tcp/tls）；认证日志补加密标志 |
| KCP/WS 传输不在保护范围 | 范围使然 | 生产未用 WS/KCP 控制传输（生产日志全部 transport=tcp）；KCP 维持 kcp.key；文档明示 |
| 与存量 `tls.enabled=true` 部署语义冲突 | 兼容性 | 互斥校验 fail-fast，不静默取舍 |

---

# 方案 B：PSK 协议内升级（认证后在同一条连接上协商加密）

## B.1 目标形态与协议时序

不改端口、不改传输、不引入证书。利用 proof 认证建立的共享密钥（客户端持有 `token`，服务端存有 `sha256(token)`——两端天然同值）在**认证成功后、smux 建立前**的窗口内完成 Noise 握手，此后 smux 全部字节加密：

```
服务端 → 客户端: challenge (32B 随机)                              [现状不变]
客户端 → 服务端: {"proof":"<hex>", "enc":1}          ← enc 请求位（新增字段）
服务端 → 客户端: {"cmd":"ok","msg":"authenticated","enc":{"v":1}}
                 ↑ enc 能力宣告（新增字段；enabled 时携带）
── 以下仅当双方 enc 就绪时执行（B 插入段）──
Noise XXpsk2 握手（3 条消息，psk = sha256(token) 的 32 字节值）
双方: smux 会话建立在加密层之上；其后所有控制命令与隧道载荷均为密文
```

旧客户端/旧服务端互不感知：未知 JSON 字段被双方标准 `json.Unmarshal` 自动忽略，旧组合直接进入 smux（明文，受服务端 `require` 开关管制）。

**psk 推导（零数据迁移）**：客户端 `sha256(token)` 本地计算；服务端 proof 认证匹配成功的 token 记录的 `TokenHash` 字段就是该值的 hex（`node_access_auth.go:93` 解码即 32 字节 psk）。token 轮换时 psk 随之变化，连接每次重新认证自然携带新 psk。

## B.2 协议细节

**选型：Noise `XXpsk2`（`github.com/flynn/noise`，vendor 锁版本）**

- msg2 混入 psk → 客户端（发起方）能成功解密 msg2 即证明服务端持有 psk（**服务端→客户端认证**，无证书）；客户端→服务端认证由前置 proof 完成（HMAC 挑战应答，天然防重放）。
- `ee` 提供每次连接的 DH → **前向保密**。
- 转录绑定内建：双终结中继（MITM 两端各跑一条加密腿）在 msg2 即失败，**无需手工 channel binding**——这是选 Noise 而非"TLS 自签 + 手工 MAC"的决定性理由，后者要把转录绑定做对非常容易出错。
- 升级后加密层：Noise `CipherState` 流式读写（2 字节长度前缀 + AEAD，单消息上限 65535，库标准用法）；smux 帧在其上无感知传输。

**降级策略（安全红线，写死在代码与文档）**

- 新客户端默认 `enc=on` **硬要求**：ok 应答缺 `enc` 字段 → 连接失败并记明确错误（服务端先行部署后生产中不存在无 enc 的服务端）。**绝不静默明文继续**；唯一逃生口 `-enc=off`（显式配置，记 WARN，仅限实验室/降级排查）。
- 服务端 `channel_encryption.enabled=true`（过渡期）：新客户端自动加密，旧客户端明文放行 + WARN。
- 服务端 `require=true`（收口）：客户端带 `enc:1` 但握手失败、或未带 enc 的客户端 → 拒绝 + 记日志（漏网未升级节点的观测点）。
- MITM 剥除/篡改 `enc` 字段：客户端收不到 enc → 硬失败（非降级）；伪造 enc 值 → msg2 解密失败。剥除对**旧客户端**无意义（本就明文，过渡期属性与 R1 的 legacy 门控同构）。

**握手预算**：升级握手超时 10s（对齐 `DefaultAuthTimeout`），失败计入 `connAuthLimiter`（复用 SEC-13 限流，防握手风暴）；deadline 在进入 smux 前清除。

**覆盖面**：升级层位于传输之上，**TCP / WS / KCP 三种传输统一生效**（含 KCP 载荷——`kcp.key` 在全量收敛后可退役）；WS 路径升级发生在 WS 字节流上，与 TCP 同码路径。

## B.3 服务端改动点

| 文件 | 改动 | 细节 |
|---|---|---|
| `internal/service/node_access_auth.go` | `AuthenticateNodeProof` 返回值扩展为 `(grant *core.NodeAccessGrant, psk []byte, err error)` | :93 处已解码出 32 字节 key，匹配成功时随 grant 一并返回；legacy 路径（若启用）psk = `sha256(MA_NODE_TOKEN)`（env 明文在手） |
| `internal/config/config.go` | 新增 `ChannelEncryption{Enabled bool, Require bool yaml:"channel_encryption"}`（默认全 false）+ `MA_CHANNEL_ENC_*` env | 校验：`require && !enabled` 报错（require 蕴含 enabled） |
| `internal/tunnel/control.go` | ① ok 应答在 `enabled` 时携带 `enc` 字段（`writeControlResp` 调用处 :470 传参扩展）；② **插入点 :470-474 之间**（`writeControlResp("ok")` 之后、`setupSmuxAndAccept` 之前）：客户端带 `enc:1` 且 enabled → 执行 Noise 握手（wrap conn），成功后 `setupSmuxAndAccept` 收到的 conn 换为 noiseConn（`bufferedConn` 结构照旧，套在加密层之上）；③ `require=true` 且客户端未升级 → 拒绝 + 专属日志关键词；④ 握手失败 → `authFail(ip)` + 断开 | 插入点两侧锚点已核实；认证行解析结构体加 `Enc int json:"enc"` 字段（旧客户端无此字段为零值） |
| 新文件 `internal/tunnel/noiseconn.go` | `noiseConn` 实现 `net.Conn`（Read/Write 走 CipherState，Deadline 委托底层 conn） | 服务端/客户端对称实现可放各自仓库，接口语义一致 |
| `cmd/moleagent-serv/main.go` | `controlSrv.SetChannelEncryption(cfg.ChannelEncryption)` 注入 | 与 `SetNodeAuthOptions` 同模式 |
| 日志 | `Node authenticated` 行加 `enc=true/false`；升级握手成功/失败计数；`require` 拒绝专属关键词 | 收口期观测点（对应 R2 的 `Legacy node auth rejected` 角色） |

## B.4 客户端改动点

| 文件 | 改动 | 细节 |
|---|---|---|
| `internal/transport/dialer.go` | ① 认证行（:248-254）加 `"enc":1` 字段（mode=on 时）；② 应答解析（:268-278 的 struct）加 `Enc` 字段检测；③ **插入点 :140-162 之间**（`authenticate` 返回后、`smux.Client` 前）：应答带 enc → Noise 握手，`sessionConn` 换 noiseConn；应答无 enc → 按 mode：on 硬失败 / off 继续（WARN） | `bufferedConn`（:283-290）照旧，reader 绕在加密层之上 |
| 新文件 `internal/transport/noiseconn.go` | 与服务端对称的 noiseConn | |
| `config.go` | 新增 `EncMode string json:"enc"`（默认 `"on"`，取值 on/off） | 旧配置文件无此字段 → 默认 on（缺省即加密，与 UseTLS 的 bool 语义陷阱无关，本字段天生字符串） |
| `cmd/moleagent-client/main.go` | 新增 `-enc` 旗标（`flag.String`，默认 `"on"`；用 `-enc=off` 形式，避免 bool 陷阱） | 逃生口仅此一处 |
| `client.go` | **无需改动**（升级封装在 dialer 层内完成，装配点 130-153 不感知） | |

## B.5 实施与迁移路径

| 阶段 | 动作 | 验证 | 回滚 |
|---|---|---|---|
| 0 依赖与审查 | vendor `flynn/noise`（锁版本）；**独立对抗性密码学审查**（模式选型/nonce/降级路径/转录）通过后才进 1 | 审查报告 | — |
| 1 服务端 | 发版部署：`channel_encryption.enabled=true`（require=false），重启 | 9 节点（尚为 v0.8.0）明文放行 + WARN；服务稳定 | `enabled=false` 恢复 |
| 2 客户端 | 发布 v0.9（默认 enc=on），节点自更新（**零参数改动**，复用已验证的自更新通道） | 服务端日志各节点 `enc=true` 逐个出现；抓包 server↔client 段确认密文 | 客户端二进制回退（v0.8.0 明文可用） |
| 3 收口 | 全量 enc=true 后 `require=true` 重启 | 未升级/握手失败客户端被拒 + 专属日志；用 v0.8.0 客户端探测验证拒绝路径（复用 R2 方法论） | require=false |
| 回滚总则 | 服务端 `enabled=false` 会使 enc=on 的 v0.9 客户端失联——**回滚序列必须**：先在各节点 `-enc=off`（或回退客户端二进制），再关服务端 enabled；文档与运维手册写明顺序 | | |

## B.6 测试要点

- 单测：XXpsk2 握手成/败；psk 错配（客户端 token 与服务端记录不符）必败；noiseConn 读写/deadline 委托/半关闭；`enc` 字段新旧互忽略（带 enc 的新 JSON 喂旧解析器、旧 JSON 喂新解析器均正常）；握手超时与 authFail 计数。
- E2E（扩展 WSL harness）：① 新×新（enabled/require 各态）自动加密 + 抓包；② 旧客户端×新服务端（enabled）明文放行 WARN；③ 旧客户端×require=true 拒绝；④ 新客户端×旧服务端（无 enc）**硬失败**（降级红线实测）；⑤ MITM 模拟：中间代理剥除/篡改 `enc` 字段 → 客户端必须失败；⑥ 三传输（tcp/ws/kcp）各一条升级链路；⑦ 重连风暴下握手失败限流生效。
- 收口探测：v0.8.0 二进制对 require=true 生产探测（只读方法，复用 R2 的探测纪律：token 不落日志、一次性节点 ID）。

## B.7 风险

| 风险 | 性质 | 对策 |
|---|---|---|
| 密码协议组合正确性（本方案最高风险） | 安全性 | Noise 库内建转录绑定（不手搓 TLS+MAC）；B.5 阶段 0 的独立对抗审查为硬前置；实现只做"接线"不做密码学 |
| `flynn/noise` 维护状态与供应链 | 依赖性 | vendor 锁版本；上线前对其做一次 pass（代码量小，可审） |
| 降级策略被"优化"掉（后人把硬要求改成自动回落） | 纪律性 | 红线写入代码注释与本文；`-enc=off` 是唯一显式逃生口且记 WARN |
| 回滚序列复杂（enabled=false × enc=on 客户端 = 失联） | 运维性 | B.5 回滚总则文档化；收口前保持 v0.8.0 探测能力 |
| 握手失败风暴 × connAuthLimiter 交互 | 可用性 | 失败计入限流（复用 SEC-13）；重连 5s 固定间隔为已知遗留（退避改进独立议题） |
| 三传输下 noise 分帧与各传输 conn 语义差异 | 正确性 | E2E 每传输一条链路；noiseConn 只依赖 Read/Write/deadline 语义 |
| 兼容矩阵扩大（新旧 × enabled/require × 3 传输） | 测试面 | E2E 矩阵清单化（B.6），全部进 harness |
