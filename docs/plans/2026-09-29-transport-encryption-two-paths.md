# 传输通道加密实施文档：方案 A「双端口 + 证书」/ 方案 B「PSK 协议内升级」

> **状态**：技术实施方案（方案 B 已实施完成：分支 `feat/channel-encryption-psk`，见 B.10 实施记录；方案 A 未实施）
> **前置**：SEC-01 已收口（全量 v0.8.0，`legacy_format_enabled: false` 生产生效）
> **关系**：两方案各自独立完整、互不需要对方；与 `2026-09-29-transport-tls-upgrade.md`（机会式 TLS 方案）为并列路线。本文不含方案间对比，采用哪个由部署场景决定。
> **2026-09-30 复审移植**：本文自旧仓库 `moleAgent_Serv/docs/plans/` 移植入 mole monorepo，并按新仓库代码逐锚点复核修正（路径前缀、行号、结构体归属、单源化适配）。全部修正项汇总于文末「B.9 复审修正记录」；正文已直接改写为复核后口径，锚点行号以 2026-09-30 新仓库 master（b392ac5）为准。
> **共同范围边界**：网关 9980（访问者段）、API 9983、MQTT 1883/1882 保持现状；p2p 隧道已有端到端加密不动。
>
> **共同协议事实**（本文所有插入点均按此锚定，已逐条核实新仓库代码）：
> - 连接时序：服务端 accept 后立即写 32 字节 challenge（`server/internal/tunnel/control.go:341-351`）→ 客户端回认证行（先 `io.ReadFull` 等 challenge，`client/internal/transport/dialer.go:235-239`，预算 `DefaultAuthTimeout=10s`）→ 服务端回 ok 行 → 双方建 smux。
> - 服务端认证行读取已有 10s deadline（`control.go:358-370`：bufio 358 → `SetReadDeadline` 359 → `readBoundedLine`（定义 578-596，上限 `proto.MaxAuthLineBytes`=64KB 单源自 shared）→ 370 清除）。
> - 多监听架构现成：`controlSrv.AddTransport(port, transport)`（`server/cmd/moleagent-serv/main.go:197-213`，ws_port/kcp_port 即此模式）。
> - `TCPTransport.Listen` 已支持 TLS 包装且 `Name()` 返回 `"tls"`（`server/internal/tunnel/transport.go:30-48`，TLS 分支 37-39）。
> - 客户端 `-tls`（`flag.Bool`，`client/cmd/moleagent-client/main.go:44`，生效 106-108）→ `DefaultDialer` → `tls.DialWithDialer`（`dialer.go:86-88`；`InsecureSkipVerify` 恒 false，走系统根**严格校验**）——今天即可对接正式证书。
> - 服务端 proof 认证在匹配成功时手里持有该 token 的 32 字节 `sha256(token)`（`server/internal/service/node_access_auth.go:93` `hex.DecodeString(token.TokenHash)`）。
> - 双端 JSON 解析均为标准 `json.Unmarshal`，**未知字段自动忽略**（服务端认证行匿名 struct `control.go:372-375`、客户端应答匿名 struct `dialer.go:270-273` 均如此——新旧共存的基础）。

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
| `server/internal/config/config.go` | `ServerConfig`（56-68）新增 `ControlTLSPort string yaml:"control_tls_port"`（默认空）+ 环境变量 `MA_CONTROL_TLS_PORT`（`applyEnvOverrides` 220-266 手工追加） | 校验规则：① `control_tls_port != ""` 时要求 `tls.cert_file/key_file` 非空（复用 316-323 现有校验形态）；② 与 `tls.enabled` **互斥**（`tls.enabled` 语义是"把 9981 本身包成 TLS"，与额外 TLS 口并列会产生歧义，直接报错）；③ 收口态支持 `control_port: ""`（仅当 `control_tls_port != ""` 时允许关闭明文口；沿用现有"无控制监听则启动失败退出码 1"路径） |
| `server/cmd/moleagent-serv/main.go` | ① 把 160-171 的证书加载提取为 `buildTLSConfig(cfg) *tls.Config`；② 新增接线：`if cfg.Server.ControlTLSPort != "" { controlSrv.AddTransport(cfg.Server.ControlTLSPort, tunnel.NewTCPTransport(tlsConfig)) }` | **零新传输代码**：`TCPTransport.Listen` 的 TLS 分支（`transport.go:37-39`）与 `Name()="tls"`（43-48）现成，`AddTransport` 模式与 ws_port/kcp_port 相同 |
| 新文件 `server/internal/tunnel/certreloader.go` | **证书热加载**：`buildTLSConfig` 用 `GetCertificate` 回调替代一次性 `Certificates` 加载——reloader 缓存已解析证书 + 两文件 mtime，每次握手前 stat 比对，mtime 变化才重新解析（握手路径只多一次 stat）；**重载失败（续期瞬间文件被移动/暂缺）时继续用旧证书并记 WARN**，不做 fail | 现状 `LoadX509KeyPair`（main.go:162）是启动时一次性读入内存，LE 续期（90 天证书 / ~60 天一续）后**必须重启才生效**；热加载后新连接立即用新证书、旧连接自然结束，**续期零重启零中断**。启动时首次加载仍 fail-fast（复用现有 `os.Exit(1)`） |
| `server/internal/tunnel/control.go` | **TLS 握手超时加固**：`handleConnection`（313-452）入口对 `*tls.Conn`（类型断言）设 `SetDeadline(now+10s)`，认证完成后清除（与现有 358-370 的读超时合并为同一窗口） | `tls.NewListener` 的握手是惰性的（首次 Read/Write 触发）：challenge 写出（341-351）即触发握手，若客户端连上不回 ClientHello，**Write 会无限期挂住同步 worker**（worker 池 `NumCPU*2`，`control.go:210-220`）——慢速攻击可占满 worker 池致拒绝服务。此加固项必做 |
| `server/internal/tunnel/control.go` | 认证成功日志（:428 `Node authenticated` 行）增加连接加密标志 | 迁移期核对"哪些节点已加密"的唯一观测点；从 `conn.(*tls.Conn)` 断言即可，transportName 已自然显示 `tls` |
| `shared/listenport/listenport.go` | `ReservedPorts` 追加 `9984` | 防隧道 `listen_port` 撞控制 TLS 口（REL-01 同理）。**2026-09-30 复审修正**：monorepo 合并已完成 listenport 单源化，旧文档警示的"客户端 `tunnel.go` 手工副本（G-4 漂移风险）"已不存在，只改 shared 一处 |
| `server/internal/config/config.go`（校验段） | `tls.enabled && control_tls_port` 互斥报错文案 | 见上 |

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

两方案代码触点不相交（A：`main.go:160-213` 传输接线区 + `handleConnection` 入口 deadline；B：`control.go:427-431` 认证后插入区 + 认证服务），**可并行实施互不阻塞**。并行部署（或先后部署）时必须遵守 B.2 末尾的组合规则：9984/TLS 连接不参与 PSK 升级（单连接单加密）；B 的 `require=true` 把 TLS 传输视为已加密合规——**否则 A 收口（`control_port:""`）与 B 收口（`require=true`）同时生效会误拒全部已迁 9984 节点**。E2E 需补一条组合用例：TLS 连接 + `require=true` 必须放行。

---

# 方案 B：PSK 协议内升级（自适应回落版，2026-09-29 负责人定稿）

> **决策记录**：降级策略由「enc 硬要求」改为「自动回落」（2026-09-29 负责人决策）。新客户端默认首选 PSK 加密；**仅当 ok 应答缺 `enc` 字段（旧服务端信号）时回落明文**。已知代价（知情采纳）：过渡期（`require=false`）内主动中间人剥除 `enc` 字段可致静默明文（被动窃听两版设计等价，均无 token 泄漏）；收益：实现与运维复杂度下降、**部署顺序自由**、**回滚零接触**。收口 `require=true` 后该弱点自动消除——收口为承诺项，不无限期停留。**加密握手本身的失败（服务端已宣告能力后）任何情况下都不回落**（见 B.2）。

## B.1 目标形态与协议时序

不改端口、不改传输、不引入证书。利用 proof 认证建立的共享密钥（客户端持有 `token`，服务端存有 `sha256(token)`——两端天然同值）在**认证成功后、smux 建立前**的窗口内完成 Noise 握手，此后 smux 全部字节加密：

```
服务端 → 客户端: challenge (32B 随机)                              [现状不变]
客户端 → 服务端: {"proof":"<hex>", "enc":1}          ← enc 请求位（新增字段；enc=off 或 UseTLS 时不发）
服务端 → 客户端: {"cmd":"ok","msg":"authenticated","enc":{"v":1}}
                 ↑ enc 能力宣告（新增字段；enabled 且客户端带 enc 且传输未加密时携带；否则无此字段）
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

**enc 字段值语义**：认证行 `enc:1` 表示请求升级协议版本 1（Noise XXpsk2 + 本文分帧）；ok 应答 `enc:{"v":1}` 宣告服务端即将执行版本 1 升级。服务端仅认 `enc==1`，收到其他数值**视为未请求加密**走明文路径（保守处理不引入新失败模式；`require=true` 下自然被拒）。版本数值单源自 `shared/proto` 常量。

## B.2 协议细节

**选型：Noise `XXpsk2`（`github.com/flynn/noise`，版本经 go.mod+go.sum 锁定）**

- msg2 混入 psk → 客户端（发起方）能成功解密 msg2 即证明服务端持有 psk（**服务端→客户端认证**，无证书）；客户端→服务端认证由前置 proof 完成（HMAC 挑战应答，天然防重放）。
- `ee` 提供每次连接的 DH → **前向保密**。
- 转录绑定内建：双终结中继（MITM 两端各跑一条加密腿）在 msg2 即失败，**无需手工 channel binding**——这是选 Noise 而非"TLS 自签 + 手工 MAC"的决定性理由，后者要把转录绑定做对非常容易出错。
- 升级后加密层：Noise `CipherState` 流式读写（2 字节长度前缀 + AEAD，单消息上限 65535，库标准用法）；smux 帧在其上无感知传输。
- 套件固定 `Noise_XXpsk2_25519_ChaChaPoly_SHA256`（若库未预置 XXpsk2 pattern，按 Noise 规范在 shared 包内声明该 pattern 常量，实现时以库源码为准）。

**降级与失败策略（本版核心，两类信号严格区分）**

| 信号 | 判定 | 行为 |
|---|---|---|
| ok 应答**缺 `enc` 字段** | 旧服务端 / enabled=false（**未认证的缺席**，与剥除攻击不可区分——已知并采纳） | 新客户端回落明文继续 + **WARN（每服务端地址每进程一次**，防 5s 重连循环刷屏——见异常矩阵 #1） |
| **握手阶段失败**（服务端已宣告 enc 后：网络错误、消息畸形、MAC 失败、超时） | 攻击或链路损坏；**绝不是**旧服务端信号（旧服务端不会发 enc） | **两端一律硬失败断开，绝不回落**——若在此处回落，主动中间人只需破坏 msg2 即可获得明文，比缺席回落更糟。客户端带专属错误串走重连循环（可观测） |

- 服务端 `channel_encryption.enabled=true`：新客户端自动加密；旧客户端/enc=off 客户端明文放行 + WARN（按 IP 限频，每 IP 每 5 分钟一条）。
- 服务端 `require=true`（收口）：认证行无 `enc` 的客户端 → 拒绝 + 专属日志关键词（漏网未升级节点的观测点，对应 R2 `Legacy node auth rejected` 的角色）。TLS 传输连接视为已加密合规（组合规则 2）。
- MITM 剥除 `enc` 字段：过渡期内表现为静默明文（已采纳的残余风险，收口后消除）；收口后（require=true）服务端等 Noise msg1 却收到明文 smux → 拒绝，客户端连接失败循环（**DoS 而非降级**）。篡改 enc 值/握手消息 → msg2 解密失败 → 硬失败。
- **服务端 psk 供给边界**：psk 仅在 proof 认证路径产出（`AuthenticateNodeProof`）；legacy 明文 token 行（`AuthenticateNodeToken`）的客户端本就不发 enc，psk 置 nil，enc 协商不发生。

**握手预算与限流边界**：升级握手超时 10s（对齐 `DefaultAuthTimeout`），deadline 覆盖握手全程、进入 smux 前清除。**握手失败不计入 `connAuthLimiter`**（`server/internal/tunnel/auth_limiter.go`，SEC-13 限流语义是"提交了凭据但被拒/通过"——`authOK` 在 ok 应答写出之前、`authFail` 在 err 应答之前，`control.go:426/397`；此时客户端已通过 proof 认证，计入会混淆限流统计并在攻击下误锁合法节点）——失败仅 WARN + 断开，由客户端 5s 重连间隔自然节流。`require` 拒绝同理不计入（被拒者是合法节点，只是版本旧）。

**实现陷阱（必写进代码注释）——bufio 读者必须穿针**：noise 握手的 3 条消息用 2 字节长度前缀分帧，经**现有的同一个 `bufio.Reader`** 读写（服务端 `control.go:358`、客户端 `dialer.go:262` 创建、认证行/应答行读取所用的那个）。若 noiseConn 绕开该 reader 直接读底层 conn，reader 缓冲里可能已吞下后续密文字节 → 帧流错位 → 必然性解密失败。正确结构：noiseConn 持有该 reader 做分帧读（不仅是握手期，**连接全生命周期**的分帧读都经它）；smux 再架在 noiseConn 之上（两端对称，`bufferedConn` 模式的自然延伸——回落明文时才继续用 bufferedConn）。写路径不经过 reader，AEAD 帧直接写底层 conn。

**与方案 A（双端口 TLS）并行实施的组合规则**（2026-09-29 组合审查补，2026-09-30 复审按新代码修正判定集合，两条均为必守）：

1. **单连接单加密机制（防双重加密）**：客户端 `UseTLS`（`-tls`，即走 9984）时**不发 `enc:1`**（不论何种传输）；服务端对传输已加密连接——`transportName ∈ {"tls","wss"}`（`TCPTransport.Name()` 返回 "tls"，`WSTransport.Name()`（ws_transport.go:39-44）在有 TLS 时返回 **"wss"**）——**不携带 `enc` 字段**（即便客户端误发也不应答升级）。无此规则会出现 TCP→TLS→Noise→smux 的双重加密，全部隧道载荷白白付两份加解密开销。
2. **`require` 的合规语义 =「TLS 传输 或 Noise 升级，二者其一」**：纯明文 smux 才拒绝。**若不加此条，两方案同时收口（A：`control_port:""` 全量迁 9984 + B：`require=true`）会把全部已迁 9984 的节点误拒**——节点经 TLS 连接、客户端按规则 1 不发 enc、服务端按旧语义判"未升级"拒绝。这是两方案间唯一一处硬冲突，规则 2 即为其修复。
3. （非规则，说明）节点级可混布：同舰队内「9981+Noise」与「9984+TLS」的节点并存互不干扰；`Node authenticated` 日志的 `tls=`/`enc=` 双字段（A、B 各贡献一个）即区分手段。

**覆盖面**：升级层位于传输之上，**TCP / WS / KCP 三种传输统一生效**（含 KCP 载荷——`kcp.key` 在全量收敛后可退役）；WS 路径升级发生在 WS 字节流上，与 TCP 同码路径。注意 KCP 连接在 challenge 之前有一次 probe 字节交互（`control.go:328-338` 服务端丢弃 1 字节探测），发生在本方案插入点之前，无交互冲突。

## B.3 服务端改动点

| 文件 | 改动 | 细节 |
|---|---|---|
| `shared/proto/proto.go` | ① 新增常量 `AuthKeyEnc = "enc"`（认证行 enc 位，仿 AuthKeyProof:49-52 的 map key 用途）与 `EncProtocolV1 = 1`；② 新增 `EncCapability struct{ V int json:"v" }`；③ `ControlResponse`（150-155）新增 `Enc *EncCapability json:"enc,omitempty"` | **2026-09-30 复审新增整行**：ok 应答行走的是已单源的 `proto.ControlResponse`（control.go:37 别名接入、`writeControlResp`:996 序列化），能力宣告字段单源加在这里最自然；omitempty 指针保证旧语义零变化，双端未知字段容忍已核实 |
| `server/internal/service/node_access_auth.go` | `AuthenticateNodeProof`（:76）返回值扩展为 `(grant *core.NodeAccessGrant, psk []byte, err error)` | :93 处已解码出 32 字节 key，匹配成功时随 grant 一并返回。**psk 不放进 grant 结构体**（grant 会被整体打日志，混入即泄漏密钥材料）；legacy 路径（若启用）psk = `sha256(MA_NODE_TOKEN)`（env 明文在手），但 legacy 客户端本无 enc，实际不可达，仅在代码上闭环。**接口波及**：该方法经 `core.NodeAccessAuthenticator` 接口暴露（`core/ports.go:150-155`），签名变更会连带全部实现与测试桩（编译期暴露，逐个跟进） |
| `server/internal/config/config.go` | 新增 `ChannelEncryptionConfig{Enabled bool, Require bool}`，yaml `channel_encryption`（默认全 false）+ `MA_CHANNEL_ENC_ENABLED` / `MA_CHANNEL_ENC_REQUIRE` env | **2026-09-30 复审修正**：结构体名仿 `NodeAuthConfig` 三件套（struct 定义 + `Config` 顶层字段 + `DefaultConfig` 预填，参照 37-43 / 25 / 171-174）；env 覆盖需往 `applyEnvOverrides`（220-266）手工追加（本仓库无自动前缀机制）。校验：`require && !enabled` 报错（require 蕴含 enabled） |
| `server/internal/tunnel/control.go` | ① 认证行匿名 struct（372-375）加 `Enc int json:"enc,omitempty"`（旧客户端无此字段为零值）；② ok 应答（:427 `writeControlResp`）在 enabled 且客户端 `enc==1` 且 `transportName∉{"tls","wss"}` 时携带 `enc` 字段；③ **插入点 :427-431 之间**（ok 写出之后、`setupSmuxAndAccept`（:431，定义 472-558）之前）：客户端 `enc==1` 且 enabled 且 psk 非空 → Noise 应答方握手（经 :358 的 reader 穿针），成功后 `setupSmuxAndAccept` 收到的 conn 换为 noiseConn；④ `require=true` 且认证行 `enc!=1` 且 `transportName∉{"tls","wss"}` → 拒绝 + 专属日志关键词，不计 `authFail`；⑤ 握手失败/超时 → WARN（remote + 原因）+ 断开，**不计入 authFail/connAuthLimiter**（见 B.2 限流边界）；⑥ enabled 且客户端无 enc → 明文放行 + WARN 按 IP 限频 | 插入点两侧锚点已按 2026-09-30 代码核实；`writeControlResp`（996-1001）签名为 `func writeControlResp(w interface{ Write([]byte) (int, error) }, cmd, msg string)`（包内私有、conn 与 smux stream 共用）——ok-with-enc 需扩展写法（构造带 Enc 字段的 `ControlResponse` 后走 `writeJSONLine`） |
| `shared/noisechan/`（新包） | `Conn` 实现 `net.Conn`（Read 走 CipherState 分帧解密、Write 分帧加密后直写底层 conn、Deadline/Close 委托底层 conn）+ `UpgradeInitiator` / `UpgradeResponder`（握手入口：底层 conn + 穿针 reader + psk + 超时预算） | **2026-09-30 复审修正**：旧文档按双仓库设计写"两端各放一份 noiseconn"（server/internal/tunnel/noiseconn.go + client/internal/transport/noiseconn.go）；monorepo 下单源为 shared 新包，两端对称复用同一实现，消除复制漂移面。代价：shared 模块从零第三方依赖变为引入 `flynn/noise`（go.mod+go.sum 锁版本，经 replace 传递至 server/client；本仓库无 vendor 目录，离线构建需求出现时再评估 vendor） |
| `server/cmd/moleagent-serv/main.go` | `controlSrv.SetChannelEncryption(cfg.ChannelEncryption.Enabled, cfg.ChannelEncryption.Require)` 注入 | 与 `SetNodeAuthOptions`（:195，方法定义 control.go:194-198）同模式、同位置区 |
| 日志 | `Node authenticated` 行（:428）加 `enc=true/false`；升级握手成功/失败计数；`require` 拒绝专属关键词（`channel encryption required`） | 收口期观测点（对应 R2 的 `Legacy node auth rejected` 角色） |

## B.4 客户端改动点

| 文件 | 改动 | 细节 |
|---|---|---|
| `client/internal/transport/dialer.go` | ① 认证行写入（250-257）——**2026-09-30 复审修正：此处是 `json.Marshal(map[string]string{proto.AuthKeyProof: proof})`（:251），不是 struct**——enc=on 且非 UseTLS 时 map 追加 `proto.AuthKeyEnc: "1"`；② 应答解析匿名 struct（270-273，本地 `{Cmd, Msg}`）加 `Enc *proto.EncCapability` 字段检测，`authenticate` 签名需扩展把检测结果带出；③ **插入点 :146-164 之间**（`authenticate` 146 返回 → `bufferedConn` 149 包装 → `smux.Client` 164）分派：应答带 enc → `noisechan.UpgradeInitiator`（经 authenticate 返回的同一 reader——:262 创建——穿针），成功后 sessionConn 换 noiseConn、失败→硬失败**不回落**（专属错误串，走重连循环）；应答无 enc 且 enc=on → **回落明文**（WARN 每服务端地址每进程一次，之后静默）；`-enc=off` 或 UseTLS → 不发 enc 位、收到 enc 应答也忽略走明文 | `bufferedConn`（286-293）仅在回落/明文路径继续使用；升级路径 reader 穿针进 noiseConn（B.2 陷阱）；WARN 限频表挂在 SessionManager（防旧服务端场景下 5s 重连刷屏） |
| `client/config.go` | 新增 `EncMode string json:"enc,omitempty"`（放 16-25 连接字段区，ServerAddr/Token/NodeID/Transport/UseTLS 一组） | `ApplyDefaults`（136-164）空值补 `"on"`（旧配置文件无此字段 → 默认 on，缺省即加密）；`Validate`（167-220）仅允许 on/off |
| `client/cmd/moleagent-client/main.go` | 新增 `-enc` 旗标（`flag.String`，默认 `"on"`；用 `-enc=off` 形式，避免 bool 陷阱）→ `cfg.EncMode` | 回落已自动化后此旗标降级为**调试杆**（强制明文对照排查/抓包对比），非运维必需 |
| `client/client.go` | **2026-09-30 复审修正：由「无需改动」改为「一处传参」**——装配区（133-156 dial 选择区）需把 `cfg.EncMode` 传入 transport 构造（enc 模式是 dialer 协商输入）；Run 循环的 `Connect` 调用点（:216）仍不感知 | GUI 两模块（desktop/manager）经 client 库使用，默认 enc=on 生效，无需 GUI 侧改动 |

## B.5 实施与迁移路径（最优路线：顺序自由、回滚零接触）

| 阶段 | 动作 | 验证 | 回滚 |
|---|---|---|---|
| 0 依赖与审查 | 引入 `flynn/noise`（go.mod+go.sum 锁版本，2026-09-30 已验证 goproxy 可达，最新 v1.1.0）；**独立对抗性密码学审查**（模式选型/nonce/回落边界/转录/分帧）通过后才进 1 | 审查报告 | — |
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
| 1 | 新客户端 × 旧服务端（或 enabled=false） | 客户端回落明文正常工作 | 客户端 WARN **每服务端地址每进程一次**（防 5s 重连刷屏），后续静默；服务端（旧）无感知 | 本版设计行为；长期停留此态=加密未生效，靠服务端侧 agent_version 与 enc 覆盖对账发现 |
| 2 | 旧客户端 × 新服务端（enabled） | 明文放行 | 服务端 WARN 按 IP 限频（每 IP/5min 一条） | 收口期观测点 |
| 3 | 旧客户端 × require=true | 拒绝 | 服务端专属关键词日志（收口探测断言它） | 对应 R2 `Legacy node auth rejected` 角色 |
| 4 | 握手网络失败/超时（10s） | **双端硬断开，绝不回落**；客户端 5s 重连（重走完整认证+握手） | 客户端专属错误串；服务端 WARN（remote+原因），**不计入 SEC-13 限流**（见 B.2） | 与 #1 严格区分：此态服务端已宣告能力，失败=攻击/损坏 |
| 5 | MITM 剥除 enc 字段 | 过渡期：客户端视角=#1，静默明文（**已采纳残余风险**）；require 后：连接失败循环 | 过渡期双端无告警（这正是该风险的性质）；require 后客户端失败日志 | 收口即消除；收口承诺写入运维节奏 |
| 6 | MITM 篡改/伪造握手消息 | msg2 解密失败 → #4 路径 | 同 #4 | Noise 转录绑定拦截双终结中继 |
| 7 | token 轮换 | psk 每连接现推导（认证时刻的 token），无缓存无状态 | — | 轮换窗口内新连接自然用新 psk |
| 8 | token 在认证后、握手前被吊销 | 本连接 psk 已定（认证时刻快照），连接继续；下次重连认证失败 | 认证失败路径（现有） | 可接受：会话级快照语义 |
| 9 | 服务端重启（握手前/中/后） | TCP 断 → 客户端 5s 重连重走全程 | 现有重连日志 | 与现状同构 |
| 10 | bufio 缓冲预吞字节 | reader 穿针设计（B.2 陷阱）保证不丢帧 | 单测覆盖 | 实现期最易错点 |
| 11 | WS/KCP 传输 | 同码路径（升级层在传输之上）；noiseConn 只依赖 Read/Write/Deadline 语义 | E2E 每传输一条 | KCP 载荷同时受保护；KCP probe 字节（control.go:328-338）在插入点之前，无冲突 |
| 12 | 时钟漂移 | 无影响（无时间窗依赖：challenge-response + Noise，均不含时间戳） | — | 相比 orbien 时间戳方案的固有优势 |
| 13 | smux keepalive/长连接 | noiseConn 透传 Read/Write，Deadline 委托底层 conn | 现有 keepalive 日志 | 10s 握手 deadline 进入 smux 前已清除 |
| 14 | TLS 传输连接（方案 A 并行部署） | 客户端不发 enc、服务端不答 enc（组合规则 1，判定集合 `{"tls","wss"}`）；`require` 视为已加密（规则 2） | 日志 `tls=true enc=false` | 单连接单加密；A/B 并行时必测 |

## B.8 风险

| 风险 | 性质 | 对策 |
|---|---|---|
| 密码协议组合正确性（本方案最高风险） | 安全性 | Noise 库内建转录绑定（不手搓 TLS+MAC）；B.5 阶段 0 的独立对抗审查为硬前置；实现只做"接线"不做密码学 |
| `flynn/noise` 维护状态与供应链 | 依赖性 | go.mod+go.sum 锁版本（本仓库无 vendor 目录）；上线前对其做一次 pass（代码量小，可审） |
| **过渡期静默降级**（本版采纳的残余风险） | 安全性 | 已知情决策（见 B.1 决策记录）；缓解=收口 `require=true` 设定期限（建议 enabled 后 ≤30 天）；被动窃听两版等价、token 任何阶段不上线 |
| 「握手失败不回落」被后人"优化"成回落 | 纪律性 | #4 与 #1 的区分写入代码注释（这是安全边界，不是不便）；`-enc=off` 是唯一显式明文方式 |
| 三传输下 noise 分帧与各传输 conn 语义差异 | 正确性 | E2E 每传输一条链路；noiseConn 只依赖 Read/Write/deadline 语义 |
| 兼容矩阵扩大（新旧 × enabled/require × 3 传输） | 测试面 | E2E 矩阵清单化（B.6），全部进 harness |
| 回落 WARN 被当噪音忽略（加密长期未生效无人知） | 观测性 | 服务端侧对账：明文会话数 vs 在线节点数（收口判据），不只靠客户端 WARN |

## B.9 复审修正记录（2026-09-30，monorepo 移植复核）

对照新仓库 master（b392ac5）逐锚点复核，修正项如下（正文已按此改写）：

1. **认证行协议 struct 并未单源到 shared**（最重要）：proto 单源化覆盖的是控制流消息（ControlCmd/ControlResponse/TunnelStatus/SysInfo/P2PSignalTokenResp）与 key 常量（AuthKeyProof/AuthKeyToken，proto.go:49-52——注释明言 tag 不能用常量故只供 map key）。认证行载荷双端形态各异：服务端是 control.go:372-375 本地匿名 struct `{Token, Proof}`；客户端 dialer.go:251 是 `map[string]string`。ok 应答 struct 已单源（proto.ControlResponse），但客户端解析用的是 dialer.go:270-273 本地匿名 struct `{Cmd, Msg}`。因此 enc 字段需触及三处 + shared 常量/类型（B.3/B.4 已按此落点）。
2. **noiseconn 双份 → shared/noisechan 单源**：旧文档按双仓库写"两端各一份"；monorepo 单源化消除复制漂移面，shared 引入首个第三方依赖 flynn/noise。
3. **ok 应答 enc 字段落点定为 proto.ControlResponse 新增 `Enc *EncCapability`（omitempty）**：writeControlResp 走的就是该类型（control.go:37 别名、996 写入），加指针字段对旧语义零影响。
4. **transportName 判定集合修正**：WS 的 TLS 变体名是 **"wss"** 不是 "ws"（ws_transport.go:39-44），组合规则 1/2 与 require 合规判定用 `{"tls","wss"}`。
5. **client.go 由「无需改动」改为「一处传参」**：enc 模式须从装配区（133-156）传入 transport；Run 内 Connect（:216）不感知。
6. **行号全面刷新**：服务端插入点 470-474 → **427-431**（ok:427 / 日志:428 / setupSmuxAndAccept:431）；challenge 写 383-394 → 341-351；deadline 读 395-412 → 358-370；worker 池 349-353 → 210-220。客户端插入点 140-162 → **146-164**；reader 创建 → :262；认证行 → 250-257；应答解析 → 270-273；bufferedConn → 286-293。
7. **writeControlResp 签名事实**：包内私有 `func writeControlResp(w interface{ Write([]byte) (int, error) }, cmd, msg string)`，conn 与 smux stream 共用——ok-with-enc 需另行构造带 Enc 的 ControlResponse。
8. **AuthenticateNodeProof 接口波及**：经 `core.NodeAccessAuthenticator`（ports.go:150-155）暴露，签名加 psk 返回值会连带实现与测试桩；psk 仅 proof 路径产出，legacy token 行 psk=nil。
9. **SEC-13 计数语义锚定**：`authOK`（:426）先于 ok 应答（:427）、`authFail`（:397）先于 err 应答（:398）；握手失败与 require 拒绝均不触碰二者（B.2 已明示）。
10. **listenport 已单源**（影响 A.2）：ReservedPorts 唯一落点 shared/listenport，旧"G-4 客户端手工副本"警示作废。
11. **杂项**：服务端 config 结构体名为 `ServerConfig`、新增顶层段仿 `NodeAuthConfig` 三件套、env 覆盖须手工加 `applyEnvOverrides`；module 名 `moleAgent_Serv` / `moleAgent_client` / `mole/shared`（replace 引入）；`handleConnection` 现含 SEC-13 预检（320-325）与 KCP probe 丢弃（328-338），均在插入点之前、无交互；flynn/noise goproxy 可达（v1.1.0）。

## B.10 实施记录（2026-09-30，分支 feat/channel-encryption-psk）

| 提交 | 内容 |
|---|---|
| 3aa38ce | `shared/noisechan`：XXpsk2 经 flynn/noise v1.1.0 `PresharedKeyPlacement=2` 原生支持（库自带 XXpsk2 官方向量佐证）；2 字节大端分帧（明文单帧上限 65519 为 tag 预留）；bufio 穿针全生命周期；Read 短读 pending 续供；失败关 conn + `ErrHandshake` 哨兵。测试 8 类含 -race 并发写 |
| 33dadd9 | `shared/proto`：`NodeAuthLine`（认证行契约单源）+ `EncCapability` + `ControlResponse.Enc`（omitempty）+ `AuthKeyEnc`/`EncProtocolV1`；ControlResponse 锁测试 4→5 字段 |
| 9521dba | 双端协商：服务端 `completeAuthConnection`（require 拒绝前置不触 limiter → ok-with-enc → `UpgradeResponder` 失败绝不回落 → 明文放行 WARN 按 IP/5min 限频；`Node authenticated` 日志加 enc 字段）+ config 三件套 + `NodeAccessAuthenticator` 返回 psk（不进 grant/日志）；客户端 dialer 分派（升级/回落 WARN 每地址每进程一次/硬失败上抛）+ `EncMode`（默认 on）+ `-enc` 旗标 |
| bd49314 | E2E sc18-24 + `enc_proxy.py`（strip/corrupt 双模式 MITM）：WSL 全量 `PASS=27 FAIL=0`，sc1-17 无回归 |

**与本文的实现偏差（均为实施期改进，正文相应小节以本记录为准）**：
1. **认证行三处落点 → 单源 struct**：B.3/B.4 复审版仍按「服务端匿名 struct + 客户端 map + 客户端应答匿名 struct」三处描述；实施中发现 map 值只能为字符串（`"enc":"1"`）而 int 字段收数字——类型错配会直接炸认证行解析，遂收敛为 `proto.NodeAuthLine` 双端共用（proof-only 输出与旧形态逐字节一致，由测试锁定）。这同时消除了复审发现 #1 的三处形态漂移。
2. **noiseconn 双份 → `shared/noisechan` 单源**（B.9 #2 的既定方向）；shared 引入首个第三方依赖 flynn/noise（go.mod+go.sum 锁版本，无 vendor）。
3. `-enc` 旗标用 `flag.Visit` 仅显式传入时覆盖——无条件赋值会把配置文件 `"enc":"off"` 静默翻回 on。
4. require 拒绝日志无 node_id（此刻客户端尚匿名，register 在 smux 之后）；带 remote/transport/userId，关键词 `channel encryption required` 不变。

**E2E 覆盖对照（B.6 → 实跑）**：① sc18/sc24（enabled/require）② sc20 ③ sc21 ④ sc19 ⑤ sc23（corrupt msg2：握手失败=6、注册成功=0、回落 WARN=0）⑥ sc22（剥认证行 enc：明文仍可用、WARN 恰 1——残余风险演示）⑧ sc19（enabled=false 零接触回滚）。**未覆盖**：⑦ ws/kcp 传输升级链路（后续补）；抓包级密文验证以日志/行为断言代替；「剥 enc × require」未单列（sc21 等价覆盖 require 拒绝语义）。单测侧：双端协商/回落/硬断/psk 错配/穿针/限频全覆盖（server `channel_enc_test.go` 8 场景 + client `dialer_enc_test.go` 6 场景 + noisechan 8 类），`-race` 全绿。

**部署提醒（B.5 路线的既有约束）**：上线顺序自由（谁先谁后都不断链）；收口 `require=true` 前须确认全量 `enc=true` 且无明文 WARN；回滚 = 服务端 `MA_CHANNEL_ENC_ENABLED=false` 或 yaml 关闭，新客户端下次重连自动回落，零接触。
