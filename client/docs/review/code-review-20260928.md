# code-review-20260928

## 审查元数据
- 审查目录: `e:\Go_codes\mole\moleAgent_client`
- 关联仓库: `e:\Go_codes\mole\moleAgent_Serv`（服务端，协议对照）
- 审查日期: `2026-09-28`
- 审查时间: `2026-09-28 22:16:58 +08:00`（UTC `14:16:58`）
- 审查方式: 静态代码阅读 / `go build` / `go vet` / `go test ./...` / 与服务端实现逐项协议对照 / 关键发现回读源码复现
- 审查人: `TRAE Agent`
- 服务端配套报告: `moleAgent_Serv/docs/2026-09-28-双仓库深度审查报告.md`

## Git 基线

| 项 | 值 |
|---|---|
| 分支 | `master` |
| HEAD 完整 hash | `6816da845d7f4d6e1dd5a611a39b1aa11958258f` |
| HEAD 提交时间 | `2026-09-18 11:10:53 +0800` |
| HEAD 提交标题 | `docs: 规则 16 允许修改 fork 代码（须可回推上游）` |
| Remote | `https://git.metme.top/viccom/moleAgent_client.git` |
| 工作区状态 | 干净（`git status` 无未提交变更） |

服务端对照基线：`moleAgent_Serv` @ `7d6fdb94ee53620bd61fbc9fdfcebf116e716875`（`master`，2026-09-17 20:07:12 +0800）。

代码规模：168 个 Go 文件 / 约 30,848 行。

## 验证状态标记说明

- ✅ **已核实**：本次审查中已回读源码确认逻辑与行号
- ⧗ **部分核实**：关键位置已确认，完整影响链未端到端复现
- ○ **静态审查**：来自静态审查，未在本轮独立回读复现

---

## 发现总览

| 编号 | 严重度 | 问题 | 验证 |
|------|--------|------|------|
| C-SEC-01 | 🔴 严重 | 内置 HTTP API 无鉴权，可绑 `0.0.0.0`，暴露凭据与 RCE 面 | ✅ |
| C-SEC-02 | 🟡 中等 | WebSSH 主机密钥 TOFU 首信且仅内存缓存 | ○ |
| C-SEC-03 | 🟡 中等 | token / KCP key 明文存配置文件，默认值未被拒绝 | ○ |
| C-REL-01 | 🟡 中等 | 重连无退避无抖动（固定间隔无限重试） | ○ |
| C-REL-02 | 🟡 中等 | `handleServerCmd` 读行无大小上限 | ○ |
| C-REL-03 | 🟡 中等 | HTTP 隧道域名不匹配时随机 fallback 路由 | ○ |
| C-REL-04 | 🟡 中等 | `dispatchStream` 兜底转发到"第一个" TCP/UDP 隧道 | ○ |
| C-REL-05 | 🟡 中等 | ser2mq `Disconnect` 仅在已连接时调用，残留重连协程 | ○ |
| C-PERF-01 | 🟡 中等 | 每请求新建 `http.Transport`，无连接复用 | ○ |
| C-QUA-01 | 🟢 轻微 | `client.go:632` 注释与实际状态上报间隔不符 | ○ |
| C-QUA-02 | 🟢 轻微 | `internal/proxy/relay.go` `Relay` 为死代码 | ○ |
| C-QUA-03 | 🟢 轻微 | 内置 HTTP 启动失败在 goroutine 内 `log.Fatalf`，跳过清理 | ○ |
| C-QUA-04 | 🟢 轻微 | `MemTotalMB` 使用 Go 运行时内存，字段语义误导 | ○ |
| C-QUA-05 | 🟢 轻微 | ser2mq/ser2net `restart` 用两次 `OnTunnelUpdate` 实现，存在竞态窗口 | ○ |
| C-QUA-06 | 🟢 轻微 | ser2net broadcast 对慢客户端 200ms 写超时会刷错误日志 | ○ |
| C-TEST-01 | 🟢 轻微 | 3 个测试在 Windows 平台失败（平台移植性问题） | ✅ |

---

## 🔴 严重问题详述

### C-SEC-01：内置 HTTP API 无鉴权，可绑 `0.0.0.0`，暴露凭据与 RCE 面

**文件**: `internal/builtin/server.go:27-35`（监听绑定）、`:85-87`（update 端点注册）、`:119-150`（隧道 API 注册）；凭据回显 `client.go:1381`（`AllTunnelStatus` 返回 `Para`）

**问题**

`StartHTTPServer`（`server.go:27-35`）在**纯端口写法**（如 `:18080`）下显式改写为 `0.0.0.0:<port>`：

```go
if host, port, err := net.SplitHostPort(addr); err == nil && host == "" {
    addr = "0.0.0.0:" + port
}
```

服务本身**无任何认证**：全部端点（`/api/tunnels`、`/api/tunnels/{name}`、`/api/status`、`/api/self-update` 等）均未校验身份。其中：

- `GET /api/tunnels` → `client.AllTunnelStatus()`，返回结果包含 `Para` 字段**明文**（含 WebSSH 口令/私钥、ser2mq secret、VPN token 等）。
- `POST /api/self-update`（`server.go:86`）可触发下载并替换本地二进制。
- 各端点使用 `withCORS`，允许跨域来源。

代码注释（`server.go:28-29`）已明确知悉"本服务无鉴权且隧道 Para 含凭据"，但仅靠"尊重显式 host"来缓解，纯端口写法仍会绑到全部网卡。

**影响**

- 当使用 `-http :18080` 形式（或配置等价写法）时，同一局域网内任意主机可读取全部隧道凭据（等于凭据大批泄漏）。
- 配合 CORS 放宽与无鉴权的 `POST /api/self-update`，构成 CSRF/远程触发升级的利用面。
- 若客户端所在主机对外可达，风险等同远程代码执行。

**修复建议**

1. 默认仅绑定 `127.0.0.1`，纯端口写法不再自动改写为 `0.0.0.0`；
2. 用户显式要求 `0.0.0.0` 时，强制要求配置访问 token 并校验请求头；
3. `AllTunnelStatus` 输出统一剔除/脱敏 `Para`；
4. `self-update` 类高危端点至少要求本地来源（loopback）或显式开启开关。

---

## 🟡 中等问题详述

### C-SEC-02：WebSSH 主机密钥 TOFU 首信且仅内存缓存

**文件**: `internal/proxy/webssh/handler.go:290-308`

**问题**: 首次连接无条件信任目标主机密钥（TOFU），且信任缓存仅存内存。进程重启后缓存丢失，再次回到"首信"状态。

**影响**: 首次连接存在 MITM 窗口；重启后该窗口重复出现，无法形成稳定的指纹校验。

**修复建议**: 将已信任指纹落盘持久化，首次信任时输出告警并（可选）上报服务端。

---

### C-SEC-03：token / KCP key 明文存配置文件，默认值未被拒绝

**文件**: `config.go`（配置加载与默认值）

**问题**: 节点接入 token 与 KCP 共享密钥以明文存于配置文件，无文件权限检查，也没有环境变量覆盖入口；默认值 `"default-node-token-change-me"` 未被拒绝（仅使用）。

**影响**: 配置文件泄漏即凭据泄漏；默认值在生产环境被沿用时可被直接利用。

**修复建议**: 支持 `MA_TOKEN` / 环境变量覆盖；对默认值给出显式告警；文档中说明配置文件权限要求。

---

### C-REL-01：重连无退避无抖动

**文件**: `client.go:201-264`

**问题**: 断线重连使用固定间隔 `ReconnectInterval`（默认 5s）无限重试，无指数退避、无抖动。

**影响**: 服务端宕机或重启时，全体节点以固定 5s 周期同时重连，形成周期性冲击（惊群），延缓服务端恢复。

**修复建议**: 改为指数退避 + 随机抖动，并设置上限（如最大 60s）。

---

### C-REL-02：`handleServerCmd` 读行无大小上限

**文件**: `client.go:993`

**问题**: 使用 `bufio.ReadBytes('\n')` 读取服务端命令，在 5s deadline 内可无限扩容。服务端侧已使用 `readBoundedLine`（`moleAgent_Serv/internal/tunnel/control.go:348`）做了同类防护，客户端未对齐。

**影响**: 恶意或故障服务端可制造客户端内存尖峰。

**修复建议**: 引入与服务端一致的有界读行实现。

---

### C-REL-03：HTTP 隧道域名不匹配时随机 fallback 路由

**文件**: `internal/proxy/http.go:328-335`

**问题**: 请求域名未匹配任何隧道时，回退到"第一个 HTTP 隧道"，而 Go map 遍历顺序随机。

**影响**: 存在多个 HTTP 隧道时，未匹配的请求被**非确定性**地投递到任意一个后端，可能串台到错误服务。

**修复建议**: 删除随机 fallback，未匹配时直接返回 502。

---

### C-REL-04：`dispatchStream` 兜底转发到"第一个" TCP/UDP 隧道

**文件**: `client.go:976-988`

**问题**: 非控制流、非 `\x00`/`\x01` 头、非 HTTP 的流（含可能损坏的控制流）会被注入按列表序的第一个 TCP/UDP 隧道。

**影响**: 异常流量可能污染错误隧道的后端；排查困难。

**修复建议**: 至少记录告警日志；条件允许时改为丢弃并关闭。

---

### C-REL-05：ser2mq `Disconnect` 仅在已连接时调用

**文件**: `internal/proxy/ser2mq/mqtt.go:253-257`

**问题**: 仅在 `IsConnected()` 为真时调用 `Disconnect()`。当 paho 正处于自动重连循环（未连接态）时跳过，残留重连协程。

**影响**: 停止隧道后仍有残留协程与重连尝试，资源未完全回收。

**修复建议**: 无条件调用 `Disconnect()`。

---

### C-PERF-01：每请求新建 `http.Transport`

**文件**: `internal/proxy/http.go:131-137`

**问题**: 每个 HTTP 转发请求新建 `http.Transport`，无连接复用，也不调用 `CloseIdleConnections`。

**影响**: 高并发下握手开销与文件描述符压力显著。

**修复建议**: 按隧道（或全局）复用 `http.Transport`，并配置空闲连接回收。

---

## 🟢 轻微问题与建议

| 编号 | 问题 | 位置 |
|------|------|------|
| C-QUA-01 | 注释与实际不符：`statusInterval=2` 实际约 20s，注释写"约 60s" | `client.go:632` |
| C-QUA-02 | `Relay` 为死代码（全仓无调用） | `internal/proxy/relay.go:90` |
| C-QUA-03 | 内置 HTTP 启动失败在 goroutine 内 `log.Fatalf` 直接退出，跳过 `client.Close()` 清理 | `cmd/moleagent-client/main.go:151-155` |
| C-QUA-04 | `MemTotalMB` 使用 `runtime.MemStats.Sys`（Go 向 OS 申请的内存），非系统总内存，字段语义误导 | `client.go:708` |
| C-QUA-05 | ser2mq/ser2net 的 `restart` action 用两次 `OnTunnelUpdate` 实现停→启，中间窗口与并发 `tunnel_push` 存在竞态（概率低） | `client.go:1098-1109` |
| C-QUA-06 | ser2net broadcast 对慢客户端 200ms 写超时会刷错误日志 | `internal/proxy/ser2net/handler.go:332` |

---

## 协议交互清单与服务端对照

### 客户端实际实现的交互流程

**握手链**（`internal/transport/dialer.go:194-235`）

1. 客户端 TCP/WS/KCP 拨号连接服务端控制端口（默认 `:9981`）
2. 读取服务端 32 字节 challenge
3. 发送 `{"token":"<token>"}\n`
4. 读取 `{"cmd":"ok"}`；非 `ok` 则认证失败
5. 同一连接上建立 smux 会话（bufio 残余数据经 `bufferedConn` 回灌，防数据丢失）
6. KCP 场景额外发送 `0x00` 探测字节

**控制命令**（每条一个 smux stream，JSON + `'\n'`）

| 方向 | 命令 | 说明 |
|------|------|------|
| C→S | `register` | 携带 `node_id` / `name` / `tunnels` / `sysinfo` |
| C→S | `ping`（带 ts） | 服务端回 `pong`，客户端据 ts 计算 RTT |
| C→S | `tunnel_update` / `tunnel_status` / `sysinfo` | 状态上报 |
| C→S | `p2p_signal_token` | 仅 `-tags p2p` 构建启用 |
| S→C | `tunnel_push` | 全量替换 `tunnels[]`，客户端回 `{"cmd":"ok"}` |
| S→C | `tunnel_action` / `restart` | `delay ≤ 300` |

**数据流分发**（`client.go:888`）

| 首字节/形态 | 判定 |
|-------------|------|
| `{` | 控制命令 |
| `\x00<name>\n` | TCP/UDP 隧道 |
| `\x01<name>\n` | WebSSH |
| 其他（可解析为 HTTP） | HTTP 隧道 |
| 兜底 | 转发到第一个 TCP/UDP 隧道（见 C-REL-04） |

### 与服务端一致性核对结果

| 环节 | 客户端 | 服务端 | 结论 |
|------|--------|--------|------|
| 握手时序 | `dialer.go:194-235` | `tunnel/control.go:328-402` | ✅ 一致 |
| 残余缓冲回灌 | `dialer.go:237-` `bufferedConn` | 同构处理 | ✅ 一致 |
| KCP 探测字节 | `ws_kcp_dialer.go:199` | `control.go:315-326` | ✅ 闭环 |
| smux 参数 | TCP 30s/90s；KCP 5s/15s | `setupSmuxAndAccept` 同值 | ✅ 一致 |
| node_id 规则 | `nodeid.go:399` `ValidateNodeID` | `isValidNodeID` | ✅ 逐字一致 |
| 心跳 | `ping` + ts → RTT | `writeControlRespTs` | ✅ 一致 |
| 流分发头 | `\x00` / `\x01` | `tcp.go:110`、`udp.go:158`、`webssh_handler.go:132` | ✅ 一致 |
| `tunnel_push` 语义 | 全量替换 | 全量语义 | ✅ 一致 |
| `restart` delay 上限 | ≤ 300 | 同约束 | ✅ 一致 |
| p2p 信令字段 | 平铺 JSON | `p2pSignalTokenResp` | ✅ 逐字段一致 |

**一致性结论**：协议层面**无阻断性不一致**，仅以下两点存疑：

| 编号 | 内容 | 位置 |
|------|------|------|
| PROTO-01 | 客户端 `register` **不带 `token` 字段**，但 `protocol.ControlCmd.Token` 已定义、README 示例含该字段、服务端会接收。认证已在握手完成，属遗留分歧；同时该字段是服务端"明文 token 落库"的输入来源，建议一并清理 | 本仓 `internal/protocol/types.go`；服务端 `control.go:735` |
| PROTO-02 | `handleServerCmd` 读行无上限（见 C-REL-02），服务端已用 `readBoundedLine` | 本仓 `client.go:993`；服务端 `control.go:348` |

---

## 基线验证记录

| 命令 | 结果 |
|------|------|
| `go build ./...` | ✅ 通过（无输出） |
| `go vet ./...` | ✅ 零告警 |
| `go test ./...` | ⚠️ 2 个包失败（3 个用例） |

测试结果明细（执行时间 2026-09-28 22:16 本地）：

```
FAIL    moleAgent_client                    0.152s
ok      moleAgent_client/internal/builtin   0.058s
ok      moleAgent_client/internal/proxy     0.016s
ok      moleAgent_client/internal/proxy/ser2mq  0.050s
ok      moleAgent_client/internal/proxy/ser2net 3.044s
FAIL    moleAgent_client/internal/proxy/vpn  0.052s
ok      moleAgent_client/internal/shellui   0.010s
ok      moleAgent_client/internal/transport 0.058s
ok      moleAgent_client/internal/version   0.010s
```

### C-TEST-01：3 个测试在 Windows 平台失败（均非产品逻辑缺陷）

| 用例 | 文件 | 根因 |
|------|------|------|
| `TestDefaultNodeIDFile_UsesHome` | `nodeid_test.go:124-133` | 用例通过 `t.Setenv("HOME", home)` 模拟家目录，但 Windows 上 `os.UserHomeDir()`（`nodeid.go:173`）读取 `%USERPROFILE%`，注入的 `HOME` 不生效，`defaultNodeIDFile()` 返回真实用户目录 |
| `TestDefaultNodeIDFile_FallsBackWhenNoHome` | `nodeid_test.go:136-151` | 同上；用例 `t.Setenv("HOME", "")` 后仍走 `os.UserHomeDir()` 成功分支 |
| `TestCrashRestartInterruptedByStopDoesNotFatal` | `internal/proxy/vpn/process_test.go:74` | 用例依赖 PATH 中的 `sh`，Windows 无该命令，`Start` 返回 `binary "sh" not found` |

**修复建议**

1. 抽出一个可注入的"家目录解析函数"（如 `resolveHomeDir func() (string, error)`），测试中替换而非依赖环境变量；或按 `runtime.GOOS` 分别注入 `HOME` / `USERPROFILE`。
2. `vpn/process_test.go` 的 `sh` 依赖改为跨平台可用的可执行文件，或在不满足前置条件时 `t.Skip`。

---

## 整体质量评价

### 优点

1. **并发与资源管理明显经过多轮复盘**：`HandleRawStream` / `handleWebSocket` 的"一侧结束→强制 deadline 逼退另一侧"防泄漏模式；`SessionManager` 的锁存/非锁存双关闭语义；`ProcessMgr` 的 `lifecycleMu` + 代际淘汰；`sendTunnelStatus` 先收状态后取 `ctrlMu` 防死锁——均为质量较高的修复。
2. **凭据脱敏成体系**：`RedactedBroker`、`redactedArgs`、broker 错误去凭据，并有 `redact_test` 加以约束。
3. **两端协议对齐严谨**：node_id 校验、KCP 探测字节、smux 参数、流头格式逐一对上（详见上一节）。
4. **测试覆盖良好**：60 个测试文件，覆盖协议常量对齐（`tunnel_test.go`）、nodeID 生成与持久化（`nodeid_test.go`）、状态收集双路径一致性（`client_status_parity_test.go`，含源码文本断言的"接线守卫"）、p2p 配置与 Handler、ser2mq 脱敏等。

### 薄弱点

1. **外部暴露面是主要风险区**：核心问题集中在 C-SEC-01（内置 HTTP 无鉴权 + 可绑全网卡 + 明文 Para + self-update）。
2. **测试盲区恰是问题集中地**：`Run()` 重连循环、`dispatchStream` 启发式路由、`handleServerCmd` 三条路径无单元测试覆盖，而 C-REL-01/02/04 正位于此处。
3. **平台移植性**：测试与部分实现存在 Unix 假设（`HOME`、`sh`），在 Windows 上表现为测试失败。

### 结论

协议一致性无阻断性问题（仅 PROTO-01 一处遗留字段分歧）。最优先修复项为 **C-SEC-01**（内置 HTTP 无鉴权暴露凭据与 self-update），其次为 C-REL-01/02（重连退避与有界读行）。

---

> 本报告为**只读审查产出**，未对本仓库代码做任何修改。