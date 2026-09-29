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
| 新文件 `internal/tunnel/certreloader.go` | **证书热加载**：`buildTLSConfig` 用 `GetCertificate` 回调替代一次性 `Certificates` 加载——reloader 缓存已解析证书 + 两文件 mtime，每次握手前 stat 比对，mtime 变化才重新解析（握手路径只多一次 stat）；**重载失败（续期瞬间文件被移动/暂缺）时继续用旧证书并记 WARN**，不做 fail | 现状 `LoadX509KeyPair` 是启动时一次性读入内存，LE 续期（90 天证书 / ~60 天一续）后**必须重启才生效**；热加载后新连接立即用新证书、旧连接自然结束，**续期零重启零中断**。启动时首次加载仍 fail-fast（复用现有 `os.Exit(1)`） |
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
| 证书过期/续期失败 | 运营性 | 复用 1panel/LE 自动续期；**续期生效不需重启**（GetCertificate 热加载，见 A.2）；启动校验 + 剩余天数告警；过期 fail-fast 拒绝启动（优于静默失效）；续期短暂失败的兜底 = 服务继续用旧证书运行（90 天证书留有充足余量，告警提前量足够） |
| 防火墙漏放行 9984 | 部署性 | 上线检查单；迁移前先从外部探测 9984 |
| 逐节点改参数的人工成本 | 运营性 | 9 节点经 WebSSH 脚本化；单节点秒级中断、即时回滚；无窗口压力 |
| 双监听日志混淆 | 观测性 | transportName 已区分（tcp/tls）；认证日志补加密标志 |
| KCP/WS 传输不在保护范围 | 范围使然 | 生产未用 WS/KCP 控制传输（生产日志全部 transport=tcp）；KCP 维持 kcp.key；文档明示 |
| 与存量 `tls.enabled=true` 部署语义冲突 | 兼容性 | 互斥校验 fail-fast，不静默取舍 |

## A.7 与方案 B 并行实施的组合规则

两方案代码触点不相交（A：`main.go:160-199` 传输接线区 + `handleConnection` 入口 deadline；B：`control.go:470-474` 认证后插入区 + 认证服务），**可并行实施互不阻塞**。并行部署（或先后部署）时必须遵守 B.2 末尾的组合规则：9984/TLS 连接不参与 PSK 升级（单连接单加密）；B 的 `require=true` 把 TLS 传输视为已加密合规——**否则 A 收口（`control_port:""`）与 B 收口（`require=true`）同时生效会误拒全部已迁 9984 节点**。E2E 需补一条组合用例：TLS 连接 + `require=true` 必须放行。

---

# 方案 B：PSK 协议内升级（自适应回落版，2026-09-29 负责人定稿）

> **决策记录**：降级策略由「enc 硬要求」改为「自动回落」（2026-09-29 负责人决策）。新客户端默认首选 PSK 加密；**仅当 ok 应答缺 `enc` 字段（旧服务端信号）时回落明文**。已知代价（知情采纳）：过渡期（`require=false`）内主动中间人剥除 `enc` 字段可致静默明文（被动窃听两版设计等价，均无 token 泄漏）；收益：实现与运维复杂度下降、**部署顺序自由**、**回滚零接触**。收口 `require=true` 后该弱点自动消除——收口为承诺项，不无限期停留。**加密握手本身的失败（服务端已宣告能力后）任何情况下都不回落**（见 B.2）。

## B.1 目标形态与协议时序

不改端口、不改传输、不引入证书。利用 proof 认证建立的共享密钥（客户端持有 `token`，服务端存有 `sha256(token)`——两端天然同值）在**认证成功后、smux 建立前**的窗口内完成 Noise 握手，此后 smux 全部字节加密：

```
服务端 → 客户端: challenge (32B 随机)                              [现状不变]
客户端 → 服务端: {"proof":"<hex>", "enc":1}          ← enc 请求位（新增字段；enc=off 时不发）
服务端 → 客户端: {"cmd":"ok","msg":"authenticated","enc":{"v":1}}
                 ↑ enc 能力宣告（新增字段；enabled 且客户端带 enc 时携带；否则无此字段）
── 分支 ──
A. 双方 enc 就绪: Noise XXpsk2 握手（3 条消息，psk = sha256(token) 32 字节）
                 → smux 建立在加密层上，其后控制命令与隧道载荷全部密文
B. ok 缺 enc 字段（旧服务端 / enabled=false）:
                 → 新客户端自动回落明文 smux（WARN 一次/每服务端/每进程，见 B.2）
                 → 旧客户端本就无感知，直接明文 smux
```

**兼容矩阵**：

| 客户端 \ 服务端 | 旧服务端（现行 v0.7.x） | 新服务端 `enabled=true` | 新服务端 `require=true` |
|---|---|---|---|
| 旧客户端 v0.8.0 | 明文（现状） | 明文放行 + WARN（限频） | 拒绝 + 专属日志 |
| 新客户端 `enc=on`（默认） | **自动回落明文** + WARN | **加密** | 加密 |
| 新客户端 `-enc=off`（调试用） | 明文 | 明文 + WARN | 拒绝 |

旧客户端/旧服务端互不感知：未知 JSON 字段被双方标准 `json.Unmarshal` 自动忽略。**部署顺序自由**（服务端与客户端谁先升级都可以，加密在两端都就绪的下一次重连自动激活）——这是回落策略带来的、硬要求版不具备的性质。

**psk 推导（零数据迁移）**：客户端 `sha256(token)` 本地计算；服务端 proof 认证匹配成功的 token 记录的 `TokenHash` 字段就是该值的 hex（`node_access_auth.go:93` 解码即 32 字节 psk）。token 轮换时 psk 随之变化，连接每次重新认证自然携带新 psk。

## B.2 协议细节

**选型：Noise `XXpsk2`（`github.com/flynn/noise`，vendor 锁版本）**

- msg2 混入 psk → 客户端（发起方）能成功解密 msg2 即证明服务端持有 psk（**服务端→客户端认证**，无证书）；客户端→服务端认证由前置 proof 完成（HMAC 挑战应答，天然防重放）。
- `ee` 提供每次连接的 DH → **前向保密**。
- 转录绑定内建：双终结中继（MITM 两端各跑一条加密腿）在 msg2 即失败，**无需手工 channel binding**——这是选 Noise 而非"TLS 自签 + 手工 MAC"的决定性理由，后者要把转录绑定做对非常容易出错。
- 升级后加密层：Noise `CipherState` 流式读写（2 字节长度前缀 + AEAD，单消息上限 65535，库标准用法）；smux 帧在其上无感知传输。

**降级与失败策略（本版核心，两类信号严格区分）**

| 信号 | 判定 | 行为 |
|---|---|---|
| ok 应答**缺 `enc` 字段** | 旧服务端 / enabled=false（**未认证的缺席**，与剥除攻击不可区分——已知并采纳） | 新客户端回落明文继续 + **WARN（每服务端地址每进程一次**，防 5s 重连循环刷屏——见异常矩阵 #1） |
| **握手阶段失败**（服务端已宣告 enc 后：网络错误、消息畸形、MAC 失败、超时） | 攻击或链路损坏；**绝不是**旧服务端信号（旧服务端不会发 enc） | **两端一律硬失败断开，绝不回落**——若在此处回落，主动中间人只需破坏 msg2 即可获得明文，比缺席回落更糟。客户端带专属错误串走重连循环（可观测） |

- 服务端 `channel_encryption.enabled=true`：新客户端自动加密；旧客户端/enc=off 客户端明文放行 + WARN（按 IP 限频，每 IP 每 5 分钟一条）。
- 服务端 `require=true`（收口）：认证行无 `enc` 的客户端 → 拒绝 + 专属日志关键词（漏网未升级节点的观测点，对应 R2 `Legacy node auth rejected` 的角色）。
- MITM 剥除 `enc` 字段：过渡期内表现为静默明文（已采纳的残余风险，收口后消除）；收口后（require=true）服务端等 Noise msg1 却收到明文 smux → 拒绝，客户端连接失败循环（**DoS 而非降级**）。篡改 enc 值/握手消息 → msg2 解密失败 → 硬失败。

**握手预算与限流边界**：升级握手超时 10s（对齐 `DefaultAuthTimeout`），deadline 覆盖握手全程、进入 smux 前清除。**握手失败不计入 `connAuthLimiter`**（SEC-13 限流语义是"未认证尝试"；此时客户端已通过 proof 认证，计入会混淆限流统计并在攻击下误锁合法节点）——失败仅 WARN + 断开，由客户端 5s 重连间隔自然节流。

**实现陷阱（必写进代码注释）——bufio 读者必须穿针**：noise 握手的 3 条消息用 2 字节长度前缀分帧，经**现有的同一个 `bufio.Reader`** 读写（服务端 `control.go:396`、客户端 `dialer.go:259` 创建的那个）。若 noiseConn 绕开该 reader 直接读底层 conn，reader 缓冲里可能已吞下后续密文字节 → 帧流错位 → 必然性解密失败。正确结构：noiseConn 持有该 reader 做分帧读；smux 再架在 noiseConn 之上（两端对称，`bufferedConn` 模式的自然延伸）。

**与方案 A（双端口 TLS）并行实施的组合规则**（2026-09-29 组合审查补，两条均为必守）：

1. **单连接单加密机制（防双重加密）**：客户端 `UseTLS`（`-tls`，即走 9984）时**不发 `enc:1`**；服务端对 `transportName=="tls"` 的连接**不携带 `enc` 字段**（即便客户端误发也不应答升级）。无此规则会出现 TCP→TLS→Noise→smux 的双重加密，全部隧道载荷白白付两份加解密开销。
2. **`require` 的合规语义 =「TLS 传输 或 Noise 升级，二者其一」**：纯明文 smux 才拒绝。**当前文档若不加此条，两方案同时收口（A：`control_port:""` 全量迁 9984 + B：`require=true`）会把全部已迁 9984 的节点误拒**——节点经 TLS 连接、客户端按规则 1 不发 enc、服务端按旧语义判"未升级"拒绝。这是两方案间唯一一处硬冲突，规则 2 即为其修复。
3. （非规则，说明）节点级可混布：同舰队内「9981+Noise」与「9984+TLS」的节点并存互不干扰；`Node authenticated` 日志的 `tls=`/`enc=` 双字段（A、B 各贡献一个）即区分手段。

**覆盖面**：升级层位于传输之上，**TCP / WS / KCP 三种传输统一生效**（含 KCP 载荷——`kcp.key` 在全量收敛后可退役）；WS 路径升级发生在 WS 字节流上，与 TCP 同码路径。

## B.3 服务端改动点

| 文件 | 改动 | 细节 |
|---|---|---|
| `internal/service/node_access_auth.go` | `AuthenticateNodeProof` 返回值扩展为 `(grant *core.NodeAccessGrant, psk []byte, err error)` | :93 处已解码出 32 字节 key，匹配成功时随 grant 一并返回。**psk 不放进 grant 结构体**（grant 会被整体打日志，混入即泄漏密钥材料）；legacy 路径（若启用）psk = `sha256(MA_NODE_TOKEN)`（env 明文在手），但 legacy 客户端本无 enc，实际不可达，仅在代码上闭环 |
| `internal/config/config.go` | 新增 `ChannelEncryption{Enabled bool, Require bool yaml:"channel_encryption"}`（默认全 false）+ `MA_CHANNEL_ENC_*` env | 校验：`require && !enabled` 报错（require 蕴含 enabled） |
| `internal/tunnel/control.go` | ① ok 应答在 `enabled` 且客户端带 `enc:1` 时携带 `enc` 字段（`writeControlResp` 调用处 :470 旁新增 ok-with-enc 写入）；② **插入点 :470-474 之间**（ok 之后、`setupSmuxAndAccept` 之前）：客户端带 `enc:1` 且 enabled → Noise 握手（经现有 `reader` 分帧，见 B.2 陷阱），成功后 `setupSmuxAndAccept` 收到的 conn 换为 noiseConn；③ `require=true` 且认证行无 `enc` → 拒绝 + 专属日志关键词；④ 握手失败/超时 → WARN（remote + 原因）+ 断开，**不计入 authFail/connAuthLimiter**（见 B.2 限流边界） | 插入点两侧锚点已核实；认证行解析结构体加 `Enc int json:"enc"` 字段（旧客户端无此字段为零值） |
| 新文件 `internal/tunnel/noiseconn.go` | `noiseConn` 实现 `net.Conn`（Read/Write 走 CipherState，Deadline 委托底层 conn） | 服务端/客户端对称实现可放各自仓库，接口语义一致 |
| `cmd/moleagent-serv/main.go` | `controlSrv.SetChannelEncryption(cfg.ChannelEncryption)` 注入 | 与 `SetNodeAuthOptions` 同模式 |
| 日志 | `Node authenticated` 行加 `enc=true/false`；升级握手成功/失败计数；`require` 拒绝专属关键词 | 收口期观测点（对应 R2 的 `Legacy node auth rejected` 角色） |

## B.4 客户端改动点

| 文件 | 改动 | 细节 |
|---|---|---|
| `internal/transport/dialer.go` | ① 认证行（:248-254）`enc=on` 时加 `"enc":1` 字段；② 应答解析（:268-278 的 struct）加 `Enc` 字段检测；③ **插入点 :140-162 之间**（`authenticate` 返回后、`smux.Client` 前）分派：应答带 enc → Noise 握手（经同一 reader 分帧），成功后 `sessionConn` 换 noiseConn、失败→硬失败**不回落**（专属错误串）；应答无 enc 且 enc=on → **回落明文**（WARN 每服务端地址每进程一次，之后静默计数）；`-enc=off` → 不发 enc 位、收到 enc 应答也忽略走明文 | `bufferedConn`（:283-290）照旧，reader 穿针到 noiseConn（B.2 陷阱）；WARN 限频防旧服务端场景下 5s 重连刷屏 |
| 新文件 `internal/transport/noiseconn.go` | 与服务端对称的 noiseConn | |
| `config.go` | 新增 `EncMode string json:"enc"`（默认 `"on"`，取值 on/off） | 旧配置文件无此字段 → 默认 on（缺省即加密，与 UseTLS 的 bool 语义陷阱无关，本字段天生字符串） |
| `cmd/moleagent-client/main.go` | 新增 `-enc` 旗标（`flag.String`，默认 `"on"`；用 `-enc=off` 形式，避免 bool 陷阱） | 回落已自动化后此旗标降级为**调试杆**（强制明文对照排查/抓包对比），非运维必需 |
| `client.go` | **无需改动**（升级封装在 dialer 层内完成，装配点 130-153 不感知） | |

## B.5 实施与迁移路径（最优路线：顺序自由、回滚零接触）

| 阶段 | 动作 | 验证 | 回滚 |
|---|---|---|---|
| 0 依赖与审查 | vendor `flynn/noise`（锁版本）；**独立对抗性密码学审查**（模式选型/nonce/回落边界/转录/分帧）通过后才进 1 | 审查报告 | — |
| 1 双端发布（顺序不限，可同窗） | 服务端：`channel_encryption.enabled=true`（require=false）重启；客户端：发布 v0.9（默认 enc=on）经自更新铺开。**谁先谁后都不断链**：新客户端×旧服务端自动回落、旧客户端×新服务端明文放行；加密在"两端都新"的下一次重连自动激活 | 服务端日志逐节点出现 `enc=true`；抓包 server↔client 段确认密文；明文 WARN 与 enc 覆盖率对账（对应 agent_version 收敛，复用 R2 观测方法） | **零接触**：服务端 `enabled=false` → 全部新客户端下次重连自动回落明文；或单节点 `-enc=off`/二进制回退 |
| 2 收口（全量 enc=true 且连续 N 天无明文 WARN 后） | `require=true` 重启 | 旧客户端/enc=off 客户端被拒 + 专属日志关键词；用 v0.8.0 二进制探测验证拒绝路径（复用 R2 探测纪律：token 不落日志、一次性节点 ID） | `require=false`（客户端自动恢复加密或回落，无失联窗口） |

与硬要求版的本质差异：**回滚不再是"序列操作"**。硬要求版关 enabled 会让 enc=on 客户端失联、必须先动客户端再动服务端；本版服务端单开关回滚即全舰队自动回落，客户端无感。

## B.6 测试要点

- 单测：XXpsk2 握手成/败；psk 错配（客户端 token 与服务端记录不符）必败；noiseConn 读写/deadline 委托/**reader 穿针**（构造 reader 预吞后续字节验证不丢帧）；`enc` 字段新旧互忽略（带 enc 的新 JSON 喂旧解析器、旧 JSON 喂新解析器均正常）；握手超时路径；回落 WARN 限频（每服务端一次）。
- E2E（扩展 WSL harness）：① 新×新（enabled/require 各态）自动加密 + 抓包；② 旧客户端×新服务端（enabled）明文放行 WARN 限频；③ 旧客户端×require=true 拒绝；④ **新客户端×旧服务端 → 回落明文成功 + WARN 一次**（本版核心行为）；⑤ **握手失败绝不回落**：中间代理破坏 msg2 → 客户端必须硬失败（专属错误串）而非明文连上；⑥ MITM 剥除 enc 字段：过渡期表现为明文连接（已采纳风险，断言 WARN/日志可见）；require=true 后表现为连接拒绝；⑦ 三传输（tcp/ws/kcp）各一条升级链路；⑧ 服务端 enabled=false 回滚 → 新客户端自动回落（零接触回滚实测）。
- 收口探测：v0.8.0 二进制对 require=true 生产探测（R2 探测纪律）。

## B.7 异常情况处理矩阵

| # | 异常 | 双端行为 | 可观测性 | 备注 |
|---|---|---|---|---|
| 1 | 新客户端 × 旧服务端（或 enabled=false） | 客户端回落明文正常工作 | 客户端 WARN **每服务端地址每进程一次**（防 5s 重连刷屏），后续静默计数；服务端（旧）无感知 | 本版设计行为；长期停留此态=加密未生效，靠服务端侧 agent_version 与 enc 覆盖对账发现 |
| 2 | 旧客户端 × 新服务端（enabled） | 明文放行 | 服务端 WARN 按 IP 限频（每 IP/5min 一条） | 收口期观测点 |
| 3 | 旧客户端 × require=true | 拒绝 | 服务端专属关键词日志（收口探测断言它） | 对应 R2 `Legacy node auth rejected` 角色 |
| 4 | 握手网络失败/超时（10s） | **双端硬断开，绝不回落**；客户端 5s 重连（重走完整认证+握手） | 客户端专属错误串；服务端 WARN（remote+原因），**不计入 SEC-13 限流**（见 B.2） | 与 #1 严格区分：此态服务端已宣告能力，失败=攻击/损坏 |
| 5 | MITM 剥除 enc 字段 | 过渡期：客户端视角=#1，静默明文（**已采纳残余风险**）；require 后：连接失败循环 | 过渡期双端无告警（这正是该风险的性质）；require 后客户端失败日志 | 收口即消除；收口承诺写入运维节奏 |
| 6 | MITM 篡改/伪造握手消息 | msg2 解密失败 → #4 路径 | 同 #4 | Noise 转录绑定拦截双终结中继 |
| 7 | token 轮换 | psk 每连接现推导（认证时刻的 token），无缓存无状态 | — | 轮换窗口内新连接自然用新 psk |
| 8 | token 在认证后、握手前被吊销 | 本连接 psk 已定（认证时刻快照），连接继续；下次重连认证失败 | 认证失败路径（现有） | 可接受：会话级快照语义 |
| 9 | 服务端重启（握手前/中/后） | TCP 断 → 客户端 5s 重连重走全程 | 现有重连日志 | 与现状同构 |
| 10 | bufio 缓冲预吞字节 | reader 穿针设计（B.2 陷阱）保证不丢帧 | 单测覆盖 | 实现期最易错点 |
| 11 | WS/KCP 传输 | 同码路径（升级层在传输之上）；noiseConn 只依赖 Read/Write/Deadline 语义 | E2E 每传输一条 | KCP 载荷同时受保护 |
| 12 | 时钟漂移 | 无影响（无时间窗依赖：challenge-response + Noise，均不含时间戳） | — | 相比 orbien 时间戳方案的固有优势 |
| 13 | smux keepalive/长连接 | noiseConn 透传 Read/Write，Deadline 委托底层 conn | 现有 keepalive 日志 | 10s 握手 deadline 进入 smux 前已清除 |
| 14 | TLS 传输连接（方案 A 并行部署） | 客户端不发 enc、服务端不答 enc（组合规则 1）；`require` 视为已加密（规则 2） | 日志 `tls=true enc=false` | 单连接单加密；A/B 并行时必测 |

## B.8 风险

| 风险 | 性质 | 对策 |
|---|---|---|
| 密码协议组合正确性（本方案最高风险） | 安全性 | Noise 库内建转录绑定（不手搓 TLS+MAC）；B.5 阶段 0 的独立对抗审查为硬前置；实现只做"接线"不做密码学 |
| `flynn/noise` 维护状态与供应链 | 依赖性 | vendor 锁版本；上线前对其做一次 pass（代码量小，可审） |
| **过渡期静默降级**（本版采纳的残余风险） | 安全性 | 已知情决策（见 B.1 决策记录）；缓解=收口 `require=true` 设定期限（建议 enabled 后 ≤30 天）；被动窃听两版等价、token 任何阶段不上线 |
| 「握手失败不回落」被后人"优化"成回落 | 纪律性 | #4 与 #1 的区分写入代码注释（这是安全边界，不是不便）；`-enc=off` 是唯一显式明文方式 |
| 三传输下 noise 分帧与各传输 conn 语义差异 | 正确性 | E2E 每传输一条链路；noiseConn 只依赖 Read/Write/deadline 语义 |
| 兼容矩阵扩大（新旧 × enabled/require × 3 传输） | 测试面 | E2E 矩阵清单化（B.6），全部进 harness |
| 回落 WARN 被当噪音忽略（加密长期未生效无人知） | 观测性 | 服务端侧对账：明文会话数 vs 在线节点数（收口判据），不只靠客户端 WARN |
