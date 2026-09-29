# P2P 隧道「连接参数 + 端口映射」两层重构 —— 客户端实施计划

> 关联文档：`moleAgent_Serv/docs/plans/2026-09-15-p2p-mapping-redesign-server.md`
> 两份文档的 **§0 协议契约逐字相同**，改动任一份必须同步另一份。
>
> 状态：**待实施**（2026-09-15 起草）
> 前置：本仓库 `feature/p2p-tunnel` 工作区已干净（第 4 轮修复已提交 `daf90e8`）。

---

## §0 协议契约（冻结，两端逐字一致）

### 0.1 分层模型

P2P 隧道分两层，**第一层两端对称，第二层各端独立**：

| 层 | 字段 | 谁配置 | 说明 |
|---|---|---|---|
| 连接参数 | `room` / `modes` / `relay_server` / `mqtt_brokers` / `stun_servers` | **两端各配一条同 `room` 的 p2p 隧道** | 决定两端能否建连 |
| 端口映射 | `mappings[]` | **仅访问发起端配置** | 会话建立后逐条 `CreateTunnel` |

对端**无需任何映射配置**：映射参数随 `TUNNEL:OPEN` 控制消息在线传给对端，对端
`AcceptRemote` 被动接受（p2punch 原生行为，`internal/p2p/tunnel/tunnel.go:191-243`）。

### 0.2 Para JSON Schema（冻结）

```json
{
  "room":         "string, 必填, 8-32 字符 [a-zA-Z0-9_-]",
  "modes":        ["lan","tcp-v6","udp-v6","udp-v4","tcp-v4","v4-relay"],
  "relay_server": "host:port",
  "mqtt_brokers": ["tcp://broker:1883"],
  "stun_servers": ["stun.example.com:3478"],
  "mappings": [
    {
      "protocol":    "tcp | udp",
      "local_port":  1-65535,
      "target_host": "非空主机名或 IP",
      "target_port":  1-65535
    }
  ]
}
```

- `modes` 省略或空数组 = 客户端默认链（`engine.DefaultModes`）
- `relay_server`：`modes` 含 `v4-relay` 时必填，否则忽略
- `mqtt_brokers` / `stun_servers` 省略或空 = **公共服务器优先 + 本服务端兜底**
- `mappings` 省略或空数组 = 纯会话端（只建会话，不在本机监听任何端口）
- **不包含** `protocol` / `local_port` / `target_host` / `target_port` 顶层字段
  （旧单组映射结构已废弃；因无存量数据，不写兼容代码）

### 0.3 校验规则（两端必须一致）

**必拒：**

| 编号 | 场景 | 样例 para |
|---|---|---|
| I1 | room 过短（7 字符） | `{"room":"abcd123"}` |
| I2 | room 过长（33 字符） | `{"room":"<33个a>"}` |
| I3 | room 含非法字符（空格） | `{"room":"abcd 1234"}` |
| I4 | room 缺失 | `{}` |
| I5 | modes 含未知值 | `{"room":"roomOK123456","modes":["bogus"]}` |
| I6 | v4-relay 缺 relay_server | `{"room":"roomOK123456","modes":["v4-relay"]}` |
| I7 | mapping.protocol 非 tcp/udp | `{"room":"roomOK123456","mappings":[{"protocol":"quic","local_port":1,"target_host":"h","target_port":2}]}` |
| I8 | mapping.local_port = 0 | 同上但 `local_port:0` |
| I9 | mapping.local_port > 65535 | `local_port:70000` |
| I10 | mapping.target_port = 0 | `target_port:0` |
| I11 | mapping.target_host 为空 | `target_host:""` |
| I12 | **同一 para 内 local_port 重复** | 两组 mapping 同为 `local_port:18080` |

**必收：**

| 编号 | 场景 | 样例 para |
|---|---|---|
| V1 | 纯会话端（仅连接参数） | `{"room":"roomOK123456"}` |
| V2 | 一组映射 | `{"room":"roomOK123456","mappings":[{"protocol":"tcp","local_port":18080,"target_host":"127.0.0.1","target_port":80}]}` |
| V3 | 多组映射（端口各异） | 两组 `local_port` 18080/18081 |
| V4 | 带 modes | `{"room":"roomOK123456","modes":["lan","udp-v4"]}` |
| V5 | v4-relay 带 relay_server | `{"room":"roomOK123456","modes":["v4-relay"],"relay_server":"relay.example.com:9999"}` |
| V6 | 自定义 mqtt/stun | `{"room":"roomOK123456","mqtt_brokers":["tcp://b:1883"],"stun_servers":["s:3478"]}` |
| V7 | udp 映射 | `{"room":"roomOK123456","mappings":[{"protocol":"udp","local_port":53,"target_host":"1.1.1.1","target_port":53}]}` |

> 共享测试向量：两端测试**都必须覆盖上表全部编号**，通过/拒绝结果必须一致。
> 这是防两端漂移的硬机制——改契约时先改这张表，再改两侧代码与测试。

### 0.4 运行时语义

```
① 两端各起 Handler（同 room）→ 打洞 → secure upgrade → yamux → Session
② 会话就绪后，本端遍历 mappings[] 逐条 sess.CreateTunnel(params)
     每条 = 一个独立 tunnel，fork 的 Manager 原生支持（tunnels map[uint32]*Tunnel）
③ 本端在 local_port 监听；对端收到 TUNNEL:OPEN 后 dial target_host:target_port
     —— target_host 由【对端】解析（通常是 127.0.0.1 或对端内网地址）
④ mappings 变更 = Handler 重建 + 会话重建（重打洞，见 §6 取舍）
```

---

## §1 背景

当前实现（`internal/proxy/p2p/config.go`）把两层压成了一层：`P2PConfig` 同时含
连接参数与**单组**端口映射（`protocol`/`local_port`/`target_host`/`target_port`）。
由此产生三个结构性缺陷：

1. **伪概念「本端角色」**：用「`target_host` 是否为空」反推身份，再在 UI 暴露成
   显式选择项——p2punch 没有这个概念，用户只需「填或不填端口映射」。
2. **只支持一组映射**：p2punch 支持任意多组，且可在线增删。
3. **加映射撞配对校验**：`validateP2PRoomPairing` 限制「同 room 最多 2 条记录」，
   而一条记录同时承担连接参数与一组映射，想加映射就得新建隧道 → 被拒。

本次重构把两层拆开，恢复 p2punch 原生语义。

---

## §2 改动范围

### 2.1 允许修改的文件（**仅限以下**）

| 文件 | 改动 |
|---|---|
| `internal/proxy/p2p/config.go` | `P2PConfig` 结构体重定义（§0.2）；`Validate()` 按 §0.3 重写；新增 `Mapping` 类型 |
| `internal/proxy/p2p/handler.go` | `restoreTunnel` 改为遍历 `mappings`（多隧道）；`cfg.TargetHost != ""` 判定改为 `len(cfg.Mappings) > 0` |
| `internal/proxy/p2p/manager.go` | 端口冲突检测改为遍历各隧道的 `mappings`（跨隧道唯一性，保留 F11 语义） |
| `internal/proxy/p2p/config_test.go` | 按 §0.3 测试向量重写 |
| `internal/proxy/p2p/manager_test.go` | 端口冲突用例适配新结构 |
| `client_p2p_test.go` | 测试夹具适配新 para |
| `CLAUDE.md` | 隧道类型表补 p2p 一行（可选，若时间允许） |

### 2.2 禁止修改的文件

- `internal/p2p/**` —— **fork 自 p2punch 的上游代码，一行都不许动**。该层已原生
  支持多隧道（`tunnel.Manager.tunnels` 是 map，`CreateTunnel` 可重复调用），
  本次重构不需要它做任何改动。若发现「必须改 fork 才能实现」，**停止并上报**。
- `internal/protocol/types.go` —— 与服务端共享协议，`Tunnel` 结构体不变。
- `tunnel.go`（根）—— `TunnelTypeP2P` 常量与 `Validate()` 已正确豁免 target，不动。
- `internal/proxy/vpn/**`、`internal/proxy/ser2mq/**`、`internal/builtin/**` —— 与本次无关。
- 工作区状态：起始应为干净树。**禁止 `git stash` / `git checkout --` / `git reset`**。

---

## §3 实施步骤

> 每步完成即跑该步验证，不通过不进下一步。

### 步骤 1：重定义配置结构（`config.go`）

```go
// Mapping 单条端口映射（对端无需配置：参数随 TUNNEL:OPEN 在线传给对端）
type Mapping struct {
    Protocol   string `json:"protocol"`    // "tcp" / "udp"
    LocalPort  int    `json:"local_port"`  // 本端监听端口
    TargetHost string `json:"target_host"` // 对端解析的目标地址
    TargetPort int    `json:"target_port"` // 对端目标端口
}

type P2PConfig struct {
    Room        string    `json:"room"`
    Modes       []string  `json:"modes,omitempty"`
    RelayServer string    `json:"relay_server,omitempty"`
    MQTTBrokers []string  `json:"mqtt_brokers,omitempty"`
    STUNServers []string  `json:"stun_servers,omitempty"`
    Mappings    []Mapping `json:"mappings,omitempty"`
}
```

`Validate()` 实现 §0.3 全部规则（含 I12 同 para 内 local_port 去重）。

**验证**：`go vet -tags p2p ./internal/proxy/p2p/` 通过。

### 步骤 2：Handler 支持多映射（`handler.go`）

- `tryConnect` 中 `h.cfg.TargetHost != ""` → `len(h.cfg.Mappings) > 0`
- `restoreTunnel` 改为遍历 `mappings` 逐条 `CreateTunnel`；**任一条失败**是否
  整体判失败？—— 采用「逐条尽力 + 记录失败项」：全部成功才 `return true`，
  否则 `return false` 走退避（与现有 F5 语义一致：本端监听不完整视同未连通）。
  失败计数与错误信息汇总进 `setFailure`。

**验证**：`go vet -tags p2p ./...` 通过。

### 步骤 3：Manager 端口唯一性（`manager.go`）

现有 `usedPorts` 逻辑改为遍历 `cfg.Mappings`；跨隧道同名端口仍跳过并告警。
**保留** `local_port` 越界不参与登记的既有防御。

**验证**：`go vet -tags p2p ./...` 通过。

### 步骤 4：测试更新

按 §0.3 测试向量重写 `config_test.go`；适配 `manager_test.go` 与 `client_p2p_test.go`。
新增用例：**多组映射时 `restoreTunnel` 逐条建隧道**（可用测试注入的假 session 计数）。

**验证**：`go test -tags p2p ./internal/proxy/p2p/ ./...` 全绿。

### 步骤 5：全量门禁

见 §4。

---

## §4 验证门禁（全部必须通过）

```bash
go vet ./...
go vet -tags p2p ./...
go test ./...
go test -tags p2p ./...
go test -race -tags p2p ./internal/proxy/p2p/ ./internal/p2p/...
make build
```

**任一失败必须如实报告，禁止以「应该能过」交付。**

---

## §4b 联合验收测试（**必跑**）

脚本由服务端计划提供：`moleAgent_Serv/docs/plans/2026-09-15-p2p-verify.sh`
（已实测可用——基础设施部分在重构前代码上跑通，P2P 相关 4 项按预期失败）。

```bash
# 1. 客户端构建（-tags p2p）
go build -tags p2p -o /tmp/p2p-verify/vt-cli ./cmd/moleagent-client

# 2. 服务端构建（在 ../moleAgent_Serv）
(cd ../moleAgent_Serv && go build -o /tmp/p2p-verify/vt-serv ./cmd/moleagent-serv)

# 3. 联合验收（⚠️ 二进制不要放脚本同目录——脚本会清空自己的工作目录）
bash ../moleAgent_Serv/docs/plans/2026-09-15-p2p-verify.sh /tmp/p2p-verify/vt-serv /tmp/p2p-verify/vt-cli
```

**⚠️ 先决条件：服务端必须先完成本次重构**（见服务端文档 §3）。若服务端仍是旧
schema，P2P 相关 4 项会失败并报 `p2p protocol must be tcp or udp`——那是预期行为，
不是客户端的问题。

脚本验证：两端注册上线 → A 侧两条同 room 隧道（A 带 2 组 mappings、B 不带）→
第三端同 room 被拒 → **两条映射端到端打通**（A 的 `local_port` → B 的 `target`）。
重构后必须 6/6 全绿。

---

## §5 与另一端的耦合点

| 耦合点 | 约定 |
|---|---|
| para 字段名与 JSON tag | §0.2，两端逐字一致 |
| 校验规则 | §0.3，两端测试向量一致 |
| `room` 配对不变量 | 服务端 `validateP2PRoomPairing` **逻辑不变**（只读 room），本端不受影响 |
| 部署顺序 | **服务端二进制先上，再 admin 前端，最后本客户端**（原因见服务端文档 §5） |
| 构建标签 | 本端改动全部在 `//go:build p2p` 内，默认构建不受影响 |

---

## §6 设计取舍（已确认）

1. **`mappings` 放在 para 内**（服务端持久化 + 分发）——服务端是唯一配置真相源。
2. **映射变更 = 会话重建（重打洞）**。理由：低频操作、实现简单、风险最低。
   在线增量增删（`CreateTunnel`/`CloseTunnel` 而不重建 handler）作为**后续迭代**，
   本次不做。
3. **`target_host` 由对端解析**（p2punch 语义）。UI 文案必须写「对端目标地址」，
   避免用户误填本机地址。

---

## §7 回滚

本次改动集中在 3 个 `-tags p2p` 文件。回滚 = `git revert <本提交>`；
p2p 尚未用户级测试，无需数据回滚。
