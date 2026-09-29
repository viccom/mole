# P2P 隧道「连接参数 + 端口映射」两层重构 —— 服务端实施计划

> 关联文档：`moleAgent_client/docs/plans/2026-09-15-p2p-mapping-redesign-client.md`
> 两份文档的 **§0 协议契约逐字相同**，改动任一份必须同步另一份。
>
> 状态：**待实施**（2026-09-15 起草）
> 前置：本仓库 `feature/p2p-tunnel` 工作区已干净（`1e0e93b` / `30104ed` 已提交）。

---

## §0 协议契约（冻结，两端逐字一致）

### 0.1 分层模型

P2P 隧道分两层，**第一层两端对称，第二层各端独立**：

| 层 | 字段 | 谁配置 | 说明 |
|---|---|---|---|
| 连接参数 | `room` / `modes` / `relay_server` / `mqtt_brokers` / `stun_servers` | **两端各配一条同 `room` 的 p2p 隧道** | 决定两端能否建连 |
| 端口映射 | `mappings[]` | **仅访问发起端配置** | 会话建立后逐条 `CreateTunnel` |

对端**无需任何映射配置**：映射参数随 `TUNNEL:OPEN` 控制消息在线传给对端，对端
`AcceptRemote` 被动接受（p2punch 原生行为）。

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

- `modes` 省略或空数组 = 客户端默认链
- `relay_server`：`modes` 含 `v4-relay` 时必填，否则忽略
- `mqtt_brokers` / `stun_servers` 省略或空 = **公共服务器优先 + 本服务端兜底**
- `mappings` 省略或空数组 = 纯会话端（只建会话，不在本机监听任何端口）
- **不包含** `protocol` / `local_port` / `target_host` / `target_port` 顶层字段
  （旧单组映射结构已废弃；**线上无存量 p2p 数据**，不写兼容代码）

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

---

## §1 背景

客户端把「连接参数」与「单组端口映射」压进了同一个 `P2PConfig`，导致只支持一组映射、
且引入了伪概念「本端角色」。本次重构把两层拆开（详见客户端文档 §1）。

**服务端侧的实质改动很小**：服务端只做 para 校验与持久化，不参与打洞/映射建立。

---

## §2 改动范围

### 2.1 允许修改的文件（**仅限以下**）

| 文件 | 改动 |
|---|---|
| `internal/core/validate_para.go` | `ValidateP2PPara` 改为按 §0.3 校验（含 `mappings[]` 逐条 + 同 para 内去重）；去掉「发起端/纯会话端」角色规则 |
| `internal/core/validate_para_test.go` | 按 §0.3 测试向量重写 |
| `internal/service/tunnel_service.go` | **仅**在 `validateP2PRoomPairing` 中确认 room 解析不受影响（读 `core.P2PRoom`，逻辑不变）；若无需改动则**不改** |
| `admin/src/types/api.ts` | `TunnelPara` 的 p2p 字段改为 `mappings` 数组 |
| `admin/src/components/TunnelFormModal.tsx` | p2p 表单：连接参数区 + **端口映射多行表格**（增删行），去掉「本端角色」选择 |
| `admin/src/pages/TunnelsPage.tsx` | 搜索/展示适配 `mappings`（可选） |

### 2.2 禁止修改的文件

- `internal/service/tunnel_service.go` 中**除** room 配对校验确认外的任何逻辑
  （尤其 `MoveTunnel` / `persistUpdatedNode` / `applyTunnelChange` —— 刚修完，禁止再动）。
- `internal/tunnel/**`、`internal/node/**`、`internal/storage/**`、`internal/mqtt/**` —— 与本次无关。
- 前端其它页面与组件。
- 工作区状态：起始应为干净树。**禁止 `git stash` / `git checkout --` / `git reset`**。

---

## §3 实施步骤

### 步骤 1：校验规则（`internal/core/validate_para.go`）

```go
type p2pMapping struct {
    Protocol   string `json:"protocol"`
    LocalPort  int    `json:"local_port"`
    TargetHost string `json:"target_host"`
    TargetPort int    `json:"target_port"`
}
type p2pPara struct {
    Room        string       `json:"room"`
    Modes       []string     `json:"modes"`
    RelayServer string       `json:"relay_server"`
    MQTTBrokers []string     `json:"mqtt_brokers"`
    STUNServers []string     `json:"stun_servers"`
    Mappings    []p2pMapping `json:"mappings"`
}
```

`ValidateP2PPara` 按 §0.3 逐条实现。**保留** `P2PRoom` 函数不变（配对校验依赖它）。
**保留** `p2pModeSet`、`p2pRoomRegexp` 不变。

**验证**：`go test ./internal/core/` 全绿（含新测试向量）。

### 步骤 2：确认配对校验不受影响（**已核实：无需改动**）

`internal/service/tunnel_service.go` 的 `validateP2PRoomPairing` 已逐行核实：
它**只**通过 `core.P2PRoom(t.Para)` 取 room，对被删字段
（`LocalPort`/`TargetHost`/`TargetPort`/`Protocol`/`Mappings`）的引用次数**均为 0**。
因此本步骤**不需要修改 `tunnel_service.go`**。

若实施时发现与服务端文档描述不符（例如某处引用了被删字段），**停止并上报**，
不要自行扩大改动范围。

**验证**：`go test ./internal/service/` 全绿。

### 步骤 3：admin 类型定义（`admin/src/types/api.ts`）

`TunnelPara` 的 p2p 部分改为：

```ts
room?: string
modes?: string[]
relay_server?: string
mqtt_brokers?: string[]
stun_servers?: string[]
mappings?: Array<{
  protocol: 'tcp' | 'udp'
  local_port: number
  target_host: string
  target_port: number
}>
```

**验证**：`cd admin && npx tsc -b` 通过。

### 步骤 4：admin 表单（`admin/src/components/TunnelFormModal.tsx`）

- **删除**「本端角色」下拉与三个平铺字段
- **新增**：「端口映射」区块，支持多行增删（每行：协议 / 本端监听端口 / 对端目标地址 /
  对端目标端口）；空列表 = 纯会话端
- 文案：「目标地址由**对端**解析（如 127.0.0.1 或对端内网地址）」
- `buildPara` 输出 §0.2 结构；客户端校验按 §0.3（含 local_port 去重）
- 保持既有风格（`FormField`、`inputClass`），不引入新组件库

**验证**：`npx tsc -b`、`npm run test:run`、`npm run build` 全通过。

### 步骤 5：构建与门禁

见 §4。

---

## §4 验证门禁（全部必须通过）

```bash
go vet ./...
go test ./...
go test -race ./internal/service/ ./internal/core/
go build -o /tmp/moleagent-serv ./cmd/moleagent-serv
cd admin && npx tsc -b && npm run test:run && npm run build
```

---

## §4b 联合验收测试（**必跑**）

骨架脚本已随本计划提供：`docs/plans/2026-09-15-p2p-verify.sh`（**已实测可用**——
基础设施部分在本重构前的代码上跑通，P2P 相关 4 项按预期失败）。

```bash
# 1. 构建（服务端用本仓库，客户端用 client 仓库的 -tags p2p 构建）
go build -o /tmp/p2p-verify/vt-serv ./cmd/moleagent-serv
(cd ../moleAgent_client && go build -tags p2p -o /tmp/p2p-verify/vt-cli ./cmd/moleagent-client)

# 2. 跑联合验收（⚠️ 二进制不要放脚本同目录——脚本会清空自己的工作目录）
bash docs/plans/2026-09-15-p2p-verify.sh /tmp/p2p-verify/vt-serv /tmp/p2p-verify/vt-cli
```

脚本验证：服务端启动 → 两端客户端注册上线 → **A 侧两条同 room p2p 隧道（A 带 2 组
mappings、B 不带）→ 第三端同 room 被拒 → 两条映射端到端打通**。

> 该脚本在 **重构后**必须 6/6 全绿。重构前跑会看到 4 项失败
> （`p2p protocol must be tcp or udp`，即旧 schema 拒绝新 para 格式）——这正是本次要改的。

**注意**：脚本使用固定端口 29980/29981/29983 等，跑之前确认无其它实例占用；
若本机有常驻的 mole 客户端连着别的服务端，不影响（端口不同）。

---

## §5 部署顺序（**关键，两个 agent 都必须知道**）

新 para 去掉了顶层 `protocol` 字段，而**当前生产版 `ValidateP2PPara` 强制要求它**，
因此顺序不可颠倒：

```
① 服务端二进制（本次改动）  →  ② admin 前端  →  ③ 客户端（-tags p2p）
```

**若先上 admin 前端而后端未换，用户建 p2p 隧道会直接 500。**

服务端部署按 `prod-deploy-px` memory 中的流程（构建 → 留档 → supervisorctl 停 → 换 → 起 → 验证）。
**部署是独立操作，需用户明确授权后执行**——实施本计划的 agent **不要自行部署**。

---

## §6 设计取舍（已确认）

1. `mappings` 放在 para 内 → 服务端是唯一配置真相源。
2. 映射变更 = 会话重建（重打洞），低风险优先；在线增量增删作为后续迭代。
3. `target_host` 由对端解析（p2punch 语义）。
4. **无存量 p2p 数据** → 不写兼容/迁移代码。

---

## §7 回滚

服务端改动集中在 `validate_para.go` + admin 前端。回滚 = `git revert <本提交>`；
无数据迁移，无需数据回滚。
