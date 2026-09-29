# Ser2MQ SSE Stream Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 为 `moleAgent_client` 的内置前端增加 `ser2mq` 实时数据流查看能力，在尽量少改现有 REST 查询链路的前提下，通过按需建立的 `SSE` 流将后端 `ser2mq` 报文事件推送到前端。

**Architecture:** 保留现有 `/api/status` 与 `/api/tunnels` 的轮询式状态查询，只为 `ser2mq` 详情面板新增 `GET /api/tunnels/{name}/stream` 的单向事件流接口。后端在 `ser2mq` handler 的串口/MQTT 收发关键点产生轻量 `PacketEvent`，写入一个按 tunnel 名路由的内存 `hub`，SSE handler 从该 `hub` 订阅并推送给前端。

**Tech Stack:** Go `net/http`、`text/event-stream`、嵌入式前端 JS `EventSource`、现有 `node:test` 与 `go test`

---

## 目标边界

- 只为 `ser2mq` 增加实时数据流，不改 `vpn-manager` 现有日志行为。
- 不替换现有 REST 状态接口；实时流只补充到详情面板。
- 默认只推送轻量事件摘要，不默认推送完整原始报文。
- 流连接按需建立，关闭详情或切换 tunnel 即断开。
- 订阅者变慢时不阻塞串口/MQTT 主数据通路。

## 新增结构体与接口

### 后端事件模型

建议新增到 `internal/proxy/ser2mq/stream.go`：

```go
package ser2mq

type PacketEvent struct {
    Time       int64  `json:"time"`
    Tunnel     string `json:"tunnel"`
    Dir        string `json:"dir"`
    Length     int    `json:"length"`
    HexPreview string `json:"hex_preview"`
    Truncated  bool   `json:"truncated"`
    Message    string `json:"message,omitempty"`
}

type PacketSink interface {
    Publish(PacketEvent)
}
```

### Hub 订阅接口

建议新增到 `internal/proxy/ser2mq/stream.go`：

```go
type StreamHub struct {
    mu      sync.RWMutex
    tails   map[string][]PacketEvent
    subs    map[string]map[chan PacketEvent]struct{}
    tailCap int
}

func NewStreamHub(tailCap int) *StreamHub
func (h *StreamHub) Publish(PacketEvent)
func (h *StreamHub) Subscribe(tunnel string, tail int) (<-chan PacketEvent, func())
```

约束：
- `tailCap` 默认 `200`
- 每个订阅 channel 默认缓冲 `64`
- channel 满时丢弃最旧事件或直接丢当前事件，但不能阻塞主链路

### Client 暴露接口

建议新增到 `client.go`：

```go
func (c *Client) Ser2MQStreamHub() *ser2mq.StreamHub
```

用途：
- 让 `internal/builtin/server.go` 可以从 `Client` 获取 hub
- 不把 `ser2mq` 内部 handler map 暴露给 HTTP 层

---

### Task 1: 建立 ser2mq 事件模型与内存 Hub

**Files:**
- Create: `e:\Go_codes\mole\moleAgent_client\internal\proxy\ser2mq\stream.go`
- Test: `e:\Go_codes\mole\moleAgent_client\internal\proxy\ser2mq\stream_test.go`

**Step 1: Write the failing test**

写两个失败测试：

```go
func TestStreamHubSubscribeReceivesPublishedEvent(t *testing.T)
func TestStreamHubSubscribeReturnsTailBeforeLiveEvents(t *testing.T)
```

覆盖点：
- 已订阅 channel 能收到 `Publish()` 的事件
- `Subscribe("alpha", 2)` 会先收到缓存尾部事件，再收到后续 live 事件

**Step 2: Run test to verify it fails**

Run:

```bash
go test ./internal/proxy/ser2mq -run "TestStreamHub"
```

Expected:
- FAIL，提示 `StreamHub` / `PacketEvent` 未定义

**Step 3: Write minimal implementation**

在 `stream.go` 实现：
- `PacketEvent`
- `PacketSink`
- `StreamHub`
- `NewStreamHub`
- `Publish`
- `Subscribe`

实现要点：
- `tails[tunnel]` 保存最近 `tailCap` 条
- `Subscribe` 返回只读 channel 和 `unsubscribe` 闭包
- `Publish` 只做轻量内存操作，不能等待订阅方消费

**Step 4: Run test to verify it passes**

Run:

```bash
go test ./internal/proxy/ser2mq -run "TestStreamHub"
```

Expected:
- PASS

**Step 5: Commit**

```bash
git add internal/proxy/ser2mq/stream.go internal/proxy/ser2mq/stream_test.go
git commit -m "feat: add ser2mq event stream hub"
```

---

### Task 2: 在 ser2mq handler 中发出 PacketEvent

**Files:**
- Modify: `e:\Go_codes\mole\moleAgent_client\internal\proxy\ser2mq\ser2mq.go`
- Modify: `e:\Go_codes\mole\moleAgent_client\internal\proxy\ser2mq\manager.go`
- Test: `e:\Go_codes\mole\moleAgent_client\internal\proxy\ser2mq\ser2mq_stream_test.go`

**Step 1: Write the failing test**

写一个失败测试：

```go
func TestHandlerPublishesPacketEvents(t *testing.T)
```

做法：
- 构造一个带 fake sink 的 handler
- 人工调用一个新提炼出来的 `emitPacketEvent(...)` 或等价辅助函数
- 断言 sink 收到事件，且字段包含 `tunnel`、`dir`、`length`、`hex_preview`

如果当前代码不易直接测试 goroutine 主流程，允许先提炼纯函数：

```go
func buildPacketEvent(tunnel, dir string, data []byte) PacketEvent
```

并先对这个纯函数写失败测试。

**Step 2: Run test to verify it fails**

Run:

```bash
go test ./internal/proxy/ser2mq -run "TestHandlerPublishesPacketEvents|TestBuildPacketEvent"
```

Expected:
- FAIL，提示事件构建函数或 sink 字段不存在

**Step 3: Write minimal implementation**

在 `ser2mq.go`：
- 给 `Ser2MQHandler` 增加字段：

```go
sink PacketSink
```

- 更新构造函数：

```go
func NewHandler(name, nodeID string, cfg Ser2MQConfig, sink PacketSink) (*Ser2MQHandler, error)
```

- 新增辅助函数：

```go
func buildPacketEvent(tunnel, dir string, data []byte) PacketEvent
func buildStatusEvent(tunnel, message string) PacketEvent
func previewHex(data []byte, maxBytes int) (string, bool)
```

- 在这些位置发事件：
  - `Start()` 成功后发 `status`
  - `runSerialToMQTT()` 串口读到数据后发 `serial_out`
  - 发布成功后发 `mqtt_pub`
  - `handleMQTTMessage()` 解密成功后发 `mqtt_sub`
  - 串口写成功后发 `serial_in`
  - `Stop()` 时发 `status`

在 `manager.go`：
- 给 `Manager` 增加字段：

```go
streamHub *StreamHub
```

- `NewManager(...)` 中初始化：

```go
streamHub: NewStreamHub(200)
```

- 创建 handler 时改为：

```go
return NewHandler(name, nodeID, cfg, m.streamHub)
```

- 新增：

```go
func (m *Manager) StreamHub() *StreamHub
```

**Step 4: Run test to verify it passes**

Run:

```bash
go test ./internal/proxy/ser2mq -run "TestHandlerPublishesPacketEvents|TestBuildPacketEvent"
```

Expected:
- PASS

**Step 5: Commit**

```bash
git add internal/proxy/ser2mq/manager.go internal/proxy/ser2mq/ser2mq.go internal/proxy/ser2mq/ser2mq_stream_test.go
git commit -m "feat: publish ser2mq packet events"
```

---

### Task 3: 通过 Client 暴露 StreamHub

**Files:**
- Modify: `e:\Go_codes\mole\moleAgent_client\client.go`
- Test: `e:\Go_codes\mole\moleAgent_client\client_stream_test.go`

**Step 1: Write the failing test**

写失败测试：

```go
func TestClientExposesSer2MQStreamHub(t *testing.T)
```

断言：
- `New(cfg)` 后 `client.Ser2MQStreamHub()` 非空

**Step 2: Run test to verify it fails**

Run:

```bash
go test ./... -run "TestClientExposesSer2MQStreamHub"
```

Expected:
- FAIL，提示方法未定义

**Step 3: Write minimal implementation**

在 `client.go` 新增：

```go
func (c *Client) Ser2MQStreamHub() *ser2mq.StreamHub {
    return c.ser2mqMgr.StreamHub()
}
```

**Step 4: Run test to verify it passes**

Run:

```bash
go test ./... -run "TestClientExposesSer2MQStreamHub"
```

Expected:
- PASS

**Step 5: Commit**

```bash
git add client.go client_stream_test.go
git commit -m "feat: expose ser2mq stream hub from client"
```

---

### Task 4: 为内置 HTTP 服务增加 SSE 接口

**Files:**
- Modify: `e:\Go_codes\mole\moleAgent_client\internal\builtin\server.go`
- Test: `e:\Go_codes\mole\moleAgent_client\internal\builtin\server_stream_test.go`

**Step 1: Write the failing test**

写两个失败测试：

```go
func TestTunnelStreamRejectsNonGET(t *testing.T)
func TestTunnelStreamReturnsSSEHeadersAndTailEvents(t *testing.T)
```

覆盖点：
- `POST /api/tunnels/alpha/stream` 返回 `405`
- `GET /api/tunnels/alpha/stream?tail=1` 返回：
  - `Content-Type` 包含 `text/event-stream`
  - 响应体含 `event: packet`
  - 响应体含一条 `data: {...}`

测试技巧：
- 使用 fake hub 预填一条尾部事件
- 用 `httptest.NewRecorder()` 或 `httptest.NewServer()` 读取前几行输出

**Step 2: Run test to verify it fails**

Run:

```bash
go test ./internal/builtin -run "TestTunnelStream"
```

Expected:
- FAIL，提示 `stream` action 未实现

**Step 3: Write minimal implementation**

在 `server.go`：
- `handleTunnelAction(...)` 中新增 `case "stream":`
- 新增函数：

```go
func handleTunnelStream(w http.ResponseWriter, r *http.Request, c *moleAgent_client.Client, name string)
```

实现要求：
- 仅允许 `GET`
- 校验 `name` 对应 tunnel 存在，且 `type == ser2mq`
- 读取 `tail` 参数，默认 `20`，上限 `200`
- 设置 SSE 响应头
- 从 `c.Ser2MQStreamHub().Subscribe(name, tail)` 获取 channel
- 逐条输出：

```text
event: packet
data: {"time":...}

```

- 每次写后调用 `Flush()`
- `r.Context().Done()` 后取消订阅并退出

**Step 4: Run test to verify it passes**

Run:

```bash
go test ./internal/builtin -run "TestTunnelStream"
```

Expected:
- PASS

**Step 5: Commit**

```bash
git add internal/builtin/server.go internal/builtin/server_stream_test.go
git commit -m "feat: add ser2mq sse stream endpoint"
```

---

### Task 5: 为前端增加 ser2mq 实时流客户端

**Files:**
- Create: `e:\Go_codes\mole\moleAgent_client\internal\builtin\static\ser2mq-stream.js`
- Modify: `e:\Go_codes\mole\moleAgent_client\internal\builtin\static\api.js`
- Test: `e:\Go_codes\mole\moleAgent_client\internal\builtin\static\ser2mq-stream.test.mjs`

**Step 1: Write the failing test**

写两个失败测试：

```js
test('openSer2MQStream opens EventSource with encoded tunnel name', async () => {})
test('closeSer2MQStream closes previous EventSource instance', async () => {})
```

覆盖点：
- URL 为 `/api/tunnels/<encoded>/stream?tail=20`
- 重开新 stream 前会关闭旧 stream

**Step 2: Run test to verify it fails**

Run:

```bash
node --test internal/builtin/static/ser2mq-stream.test.mjs
```

Expected:
- FAIL，提示模块或导出不存在

**Step 3: Write minimal implementation**

在 `ser2mq-stream.js` 新增：

```js
let currentSource = null;

export function openSer2MQStream(name, handlers = {}, options = {}) {}
export function closeSer2MQStream() {}
```

行为：
- `open...` 自动关闭旧连接
- 创建 `new EventSource(...)`
- 监听 `packet` 与 `error`
- 将解析后的 JSON 传给 `handlers.onPacket`
- 错误时传给 `handlers.onError`

在 `api.js` 新增：

```js
export function buildTunnelStreamURL(name, options = {}) {}
```

只负责构造 URL，避免 URL 拼接逻辑散落在 UI 中。

**Step 4: Run test to verify it passes**

Run:

```bash
node --test internal/builtin/static/ser2mq-stream.test.mjs
```

Expected:
- PASS

**Step 5: Commit**

```bash
git add internal/builtin/static/api.js internal/builtin/static/ser2mq-stream.js internal/builtin/static/ser2mq-stream.test.mjs
git commit -m "feat: add frontend ser2mq sse client"
```

---

### Task 6: 将实时流接入详情面板 UI

**Files:**
- Modify: `e:\Go_codes\mole\moleAgent_client\internal\builtin\static\app.js`
- Modify: `e:\Go_codes\mole\moleAgent_client\internal\builtin\static\index.html`
- Modify: `e:\Go_codes\mole\moleAgent_client\internal\builtin\static\style.css`
- Test: `e:\Go_codes\mole\moleAgent_client\internal\builtin\static\app-stream.test.mjs`

**Step 1: Write the failing test**

写两个失败测试：

```js
test('renderDetail opens ser2mq stream when ser2mq tunnel selected', async () => {})
test('closing detail panel closes ser2mq stream', async () => {})
```

覆盖点：
- 选中 `ser2mq` tunnel 时调用 `openSer2MQStream(name, ...)`
- 关闭详情面板时调用 `closeSer2MQStream()`

如果当前 `app.js` 难以直接测 DOM，可先提炼：

```js
export function isSer2MQTunnel(tunnel) {}
export function appendSer2MQPacket(list, event) {}
```

并先测试这些纯函数。

**Step 2: Run test to verify it fails**

Run:

```bash
node --test internal/builtin/static/app-stream.test.mjs
```

Expected:
- FAIL，提示实时流逻辑未接入

**Step 3: Write minimal implementation**

在 `index.html` 的详情面板区域新增：

```html
<div id="detail-live-area"></div>
```

在 `style.css` 新增：
- `.stream-list`
- `.stream-item`
- `.stream-dir`
- `.stream-empty`

在 `app.js`：
- 引入：

```js
import { openSer2MQStream, closeSer2MQStream } from './ser2mq-stream.js';
```

- 新增状态：

```js
let streamPackets = [];
let streamRenderPending = false;
let streamPaused = false;
```

- 在 `renderDetail(tunnel)` 中：
  - 若 `tunnel.type === 'ser2mq'`，渲染实时流面板
  - 打开 stream
  - 收到事件后追加到 `streamPackets`
  - 仅保留最近 `500` 条
  - 用 `requestAnimationFrame` 合并刷新

- 在以下位置关闭 stream：
  - 切换到非 `ser2mq` 详情
  - 关闭详情面板
  - 页面卸载

**Step 4: Run test to verify it passes**

Run:

```bash
node --test internal/builtin/static/app-stream.test.mjs
```

Expected:
- PASS

**Step 5: Commit**

```bash
git add internal/builtin/static/app.js internal/builtin/static/index.html internal/builtin/static/style.css internal/builtin/static/app-stream.test.mjs
git commit -m "feat: show ser2mq live stream in detail panel"
```

---

### Task 7: 联调与回归验证

**Files:**
- Verify: `e:\Go_codes\mole\moleAgent_client\internal\builtin\server.go`
- Verify: `e:\Go_codes\mole\moleAgent_client\internal\builtin\static\app.js`
- Verify: `e:\Go_codes\mole\moleAgent_client\internal\proxy\ser2mq\ser2mq.go`
- Docs: `e:\Go_codes\mole\moleAgent_client\README.md`
- Docs: `e:\Go_codes\mole\moleAgent_client\docs\review\code-review-20260430.md`

**Step 1: Add/update docs**

在 `README.md` 增补：
- `GET /api/tunnels/{name}/stream`
- 说明其仅适用于 `ser2mq`
- 说明返回 `text/event-stream`

在 `docs/review/code-review-20260430.md` 增补：
- 已落地的 `ser2mq` 实时流方案
- 与原先 REST 轮询状态的职责分工

**Step 2: Run Go tests**

Run:

```bash
go test ./...
```

Expected:
- PASS

**Step 3: Run frontend tests**

Run:

```bash
node --test internal/builtin/static/api.test.mjs internal/builtin/static/render.test.mjs internal/builtin/static/ser2mq-stream.test.mjs internal/builtin/static/app-stream.test.mjs
```

Expected:
- PASS

**Step 4: Run syntax checks**

Run:

```bash
node --check internal/builtin/static/api.js
node --check internal/builtin/static/app.js
node --check internal/builtin/static/ser2mq-stream.js
```

Expected:
- PASS

**Step 5: Manual verification**

手工验证：
- 打开 `/ui`
- 选择一个 `ser2mq` tunnel 详情
- 观察 stream 面板能持续追加事件
- 关闭详情后浏览器网络面板中的 SSE 请求结束
- 再次打开详情能重新连接
- 网络短暂中断时前端提示错误但主页面状态轮询不受影响

**Step 6: Commit**

```bash
git add README.md docs/review/code-review-20260430.md
git commit -m "docs: document ser2mq live stream support"
```

---

## 额外实现约束

- `SSE` 只做单向日志/报文推送，不承载控制指令。
- `PacketEvent` 默认只推送摘要字段；不要默认推送完整 `base64` 原文，避免前端与网络压力激增。
- 详情页未展开时不建立 stream 连接。
- 后端 hub 满载时优先丢弃事件，不得阻塞串口与 MQTT goroutine。
- 所有新增逻辑都应围绕 `ser2mq` 自身模块展开，避免把实时流逻辑散落到 `builtin` 以外的无关组件中。

## 文件改动总表

- Create: `internal/proxy/ser2mq/stream.go`
- Create: `internal/proxy/ser2mq/stream_test.go`
- Create: `internal/proxy/ser2mq/ser2mq_stream_test.go`
- Create: `client_stream_test.go`
- Create: `internal/builtin/server_stream_test.go`
- Create: `internal/builtin/static/ser2mq-stream.js`
- Create: `internal/builtin/static/ser2mq-stream.test.mjs`
- Create: `internal/builtin/static/app-stream.test.mjs`
- Modify: `internal/proxy/ser2mq/manager.go`
- Modify: `internal/proxy/ser2mq/ser2mq.go`
- Modify: `client.go`
- Modify: `internal/builtin/server.go`
- Modify: `internal/builtin/static/api.js`
- Modify: `internal/builtin/static/app.js`
- Modify: `internal/builtin/static/index.html`
- Modify: `internal/builtin/static/style.css`
- Modify: `README.md`
- Modify: `docs/review/code-review-20260430.md`

## 验收标准

- `ser2mq` 详情页可看到近实时事件流，不依赖高频轮询。
- 普通 tunnel 列表和全局状态仍按原 REST 逻辑工作。
- 关闭详情页后实时流连接会立即释放。
- 高并发流量下不会阻塞串口/MQTT 主数据路径。
- 所有新增 Go / Node 测试通过。
