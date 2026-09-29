# 本地运行状态监视 API 实现计划

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 为 moleAgent_client 增加本地运行状态监视 REST API，监控连接状态、隧道详情、Target 状态及数据包统计。

**Architecture:** 在 `internal/builtin/server.go` 中增加新的 `/api/status` 端点，通过 `Client` 暴露统计数据。数据包统计在 proxy 层（`proxy/http.go`、`proxy/tcp.go`）的 IO 操作处原子计数，Client 聚合后通过 API 暴露。

**Tech Stack:** Go 标准库 `net/http`、`sync/atomic`、现有 `builtin/server.go` 的 HTTP mux 架构。

---

### Task 1: 添加 Stats 结构体和原子计数器到 proxy 包

**Files:**
- Modify: `internal/proxy/relay.go`

**Step 1: 添加数据包计数器**

```go
// relay.go 顶部添加
var (
    // 全局/每隧道统计（atomic）
    httpBytesIn  uint64
    httpBytesOut uint64
    tcpBytesIn   uint64
    tcpBytesOut  uint64
)

// AddHTTPBytesIn adds bytes to HTTP inbound counter
func AddHTTPBytesIn(n uint64) {
    atomic.AddUint64(&httpBytesIn, n)
}

// ... 类似添加其他计数器
```

**Step 2: 在 io.Copy 处插入计数**

在 `relay.go` 的 `CopyBuf` / `Relay` 函数中，用 `io.Copy` 包装或直接对 `n` 计数：
```go
n, err = io.Copy(dst, src)
AddTCPBytesOut(n) // 根据方向选合适的计数器
```

**Step 3: Commit**

```bash
git add internal/proxy/relay.go
git commit -m "feat(proxy): add bytes counter for traffic stats"
```

---

### Task 2: 在 Client 添加状态查询 API

**Files:**
- Modify: `client.go` — 添加 `Stats()` 方法

**Step 1: 添加 Stats 结构体**

```go
// client.go 末尾添加
type Stats struct {
    NodeID      string    `json:"node_id"`
    Connected   bool      `json:"connected"`
    ServerAddr  string    `json:"server_addr"`
    Uptime      string    `json:"uptime"` // 运行时间

    HTTPBytesIn  uint64   `json:"http_bytes_in"`
    HTTPBytesOut uint64   `json:"http_bytes_out"`
    TCPBytesIn   uint64   `json:"tcp_bytes_in"`
    TCPBytesOut  uint64   `json:"tcp_bytes_out"`
}
```

**Step 2: 添加 Stats() 方法**

```go
// Stats 返回当前运行状态快照
func (c *Client) Stats() Stats {
    s := Stats{
        NodeID:     c.cfg.NodeID,
        Connected:  c.Connected(),
        ServerAddr: c.cfg.ServerAddr,
        HTTPBytesIn:  proxy.GetHTTPBytesIn(),
        HTTPBytesOut: proxy.GetHTTPBytesOut(),
        TCPBytesIn:   proxy.GetTCPBytesIn(),
        TCPBytesOut:  proxy.GetTCPBytesOut(),
    }
    return s
}
```

**Step 3: Commit**

```bash
git add client.go
git commit -m "feat(client): add Stats() method for monitoring"
```

---

### Task 3: 在 builtin server 中注册 /api/status 端点

**Files:**
- Modify: `internal/builtin/server.go`

**Step 1: 添加 Status API 处理器**

```go
// 在 registerTunnelAPI 后添加
mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json")
    if r.Method != http.MethodGet {
        http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
        return
    }
    json.NewEncoder(w).Encode(c.Stats())
})
```

**Step 2: Commit**

```bash
git add internal/builtin/server.go
git commit -m "feat(builtin): add GET /api/status endpoint"
```

---

### Task 4: 验证构建通过

**Step 1: Run build**

```bash
make build
```

Expected: `>> Done: ../_release/moleagent-client`

**Step 2: Commit**

```bash
git add docs/plans/2026-04-17-status-monitoring.md
git commit -m "docs: add status monitoring implementation plan"
```

---

## 后续扩展（如需要）

- **Target 级别统计**：在 `proxy/tcp.go` 的 `HandleRawStream` 中按 target 维护 `map[string]TargetStats`（需要 `sync.RWMutex` 保护）
- **隧道连接数**：在 `proxy/tcp.go` 中用 `sync.WaitGroup` 跟踪活跃连接数
- **心跳历史**：在 `eventBus` 中记录最近 N 条事件日志，通过 `/api/events` 暴露
- **Target 健康检查**：定时 `net.Dial` 检测 target 可达性

---

## 执行选项

**1. Subagent-Driven (this session)** — I dispatch fresh subagent per task, review between tasks, fast iteration

**2. Parallel Session (separate)** — Open new session with executing-plans, batch execution with checkpoints

Which approach?
