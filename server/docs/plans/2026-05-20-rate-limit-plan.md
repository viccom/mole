# 三级限流实施方案

> 日期：2026-05-20
> 状态：待审批
> 目标：在 moleAgent_Serv 中实现用户级 API 限流、节点级连接限流、隧道级带宽限流，零侵入现有业务逻辑

---

## 1. 背景与目标

### 1.1 现状问题

- REST API 无速率限制，存在暴力破解和滥用风险
- Gateway 无每节点/每隧道并发控制，单节点可占满全局信号量
- 无带宽控制，单隧道可耗尽全部上行带宽
- 已有基础设施可直接复用：`countingConn`（统计包装器）、`Semaphore`（并发控制）、`StatsTracker`（计数器）

### 1.2 设计目标

| 目标 | 说明 |
|------|------|
| 非侵入 | 核心 handler/repo/auth/nodeManager 零改动 |
| 零开销 | 未启用时 NopLimiter 编译器内联优化，无运行时损耗 |
| 可分阶段 | 每个 Phase 独立可测可交付，可停在任意 Phase |
| 可配置 | 全局默认 + 每隧道可覆盖 |

### 1.3 三级限流总览

| 级别 | 作用域 | Key | 限流维度 | 拦截位置 |
|------|--------|-----|---------|---------|
| Level 1 | API 请求 | userID / IP | 请求速率 (req/s) | Router 中间件 |
| Level 2 | Gateway 连接 | nodeID / tunnelName | 并发连接数 | TunnelGateway 转发前 |
| Level 3 | Gateway 带宽 | nodeID / tunnelName | 字节速率 (bytes/s) | countingConn 读写 |

---

## 2. 架构设计

### 2.1 新增包结构

```
internal/ratelimit/
  ├── limiter.go       # GatewayLimiter 接口定义 + NopLimiter 空实现
  ├── api_limiter.go   # APILimiter — API 请求级限流
  ├── conn_limiter.go  # ConnLimiter — 节点/隧道并发连接数限流
  ├── bw_limiter.go    # BWLimiter — 隧道带宽限流
  └── config.go        # 限流配置结构体
```

不引入 `internal/ratelimit/` 之外的任何新包。依赖仅 `golang.org/x/time/rate` + 标准库。

### 2.2 接口定义

```go
// internal/ratelimit/limiter.go

// GatewayLimiter Gateway 层限流接口
// TunnelGateway 持有此接口的实例，通过 NopLimiter 实现零开销可关闭
type GatewayLimiter interface {
    // AllowConn 检查是否允许新建连接（node + tunnel 双重检查）
    // 返回 false 表示超限，调用方应拒绝连接
    AllowConn(nodeID, sKey string) bool

    // AcquireConn 占用一个连接槽位（阻塞语义可选，当前实现为非阻塞）
    AcquireConn(nodeID, sKey string) bool

    // ReleaseConn 释放一个连接槽位
    ReleaseConn(nodeID, sKey string)

    // BWLimiterFor 返回指定隧道的带宽限流器，nil 表示不限速
    BWLimiterFor(sKey string) *rate.Limiter

    // UpdateTunnelConfig 更新某隧道的限流配置（运行时热更新）
    UpdateTunnelConfig(sKey string, cfg TunnelRateConfig)

    // RemoveTunnel 清理离线隧道的限流状态
    RemoveTunnel(sKey string)
}

// NopLimiter 空实现 — config.enabled=false 时注入
// 所有方法为内联空操作，编译器优化后零开销
type NopLimiter struct{}

func (NopLimiter) AllowConn(_, _ string) bool              { return true }
func (NopLimiter) AcquireConn(_, _ string) bool             { return true }
func (NopLimiter) ReleaseConn(_, _ string)                  {}
func (NopLimiter) BWLimiterFor(_ string) *rate.Limiter      { return nil }
func (NopLimiter) UpdateTunnelConfig(_ string, _ TunnelRateConfig) {}
func (NopLimiter) RemoveTunnel(_ string)                    {}
```

### 2.3 注入点示意

```
┌─ API 通路 ──────────────────────────────────────────────┐
│                                                          │
│  Request                                                 │
│    → loggingMiddleware          (existing, unchanged)    │
│    → rateLimitMiddleware  ★NEW   (APILimiter)            │
│    → route match                (existing, unchanged)    │
│    → Auth middleware            (existing, unchanged)    │
│    → Handler                   (existing, unchanged)    │
│                                                          │
└──────────────────────────────────────────────────────────┘

┌─ Gateway 通路 ──────────────────────────────────────────┐
│                                                          │
│  外部请求                                                │
│    → sem.Acquire               (existing, unchanged)    │
│    → findNodeForTunnel         (existing, unchanged)    │
│    → limiter.AllowConn  ★NEW    (GatewayLimiter)        │
│    → limiter.AcquireConn ★NEW                            │
│    → stats.ConnOpened          (existing, unchanged)    │
│    → session.OpenStream        (existing, unchanged)    │
│    → countingConn(+bwLimiter ★NEW) → biCopy             │
│    → limiter.ReleaseConn ★NEW   (defer)                 │
│    → stats.ConnClosed          (existing, unchanged)    │
│                                                          │
└──────────────────────────────────────────────────────────┘
```

---

## 3. 配置设计

### 3.1 全局配置 (config.yaml)

```yaml
ratelimit:
  api:
    enabled: false           # 总开关，false 时注入 NopLimiter
    per_user: 100            # 每用户每秒请求数 (0=不限)
    per_ip: 30               # 每IP每秒请求数 (0=不限)
    burst: 20                # 突发桶容量
  gateway:
    enabled: false           # 总开关
    max_conns_per_node: 1000       # 每节点最大并发连接 (0=不限)
    max_conns_per_tunnel: 500      # 每隧道最大并发连接 (0=不限)
    max_bps_per_tunnel: 0          # 每隧道默认带宽上限 bytes/sec (0=不限)
    bw_burst: 65536               # 带宽突发桶容量 bytes
    cleanup_interval: 300         # 空闲限流器清理间隔 (秒)
```

### 3.2 Go 配置结构

```go
// internal/ratelimit/config.go

type RateLimitConfig struct {
    API     APIRateLimitConfig     `yaml:"api"`
    Gateway GatewayRateLimitConfig `yaml:"gateway"`
}

type APIRateLimitConfig struct {
    Enabled bool `yaml:"enabled"`
    PerUser int  `yaml:"per_user"`  // 0=不限
    PerIP   int  `yaml:"per_ip"`    // 0=不限
    Burst   int  `yaml:"burst"`
}

type GatewayRateLimitConfig struct {
    Enabled           bool  `yaml:"enabled"`
    MaxConnsPerNode   int   `yaml:"max_conns_per_node"`
    MaxConnsPerTunnel int   `yaml:"max_conns_per_tunnel"`
    MaxBPSPerTunnel   int64 `yaml:"max_bps_per_tunnel"`  // 0=不限
    BWBurst           int   `yaml:"bw_burst"`
    CleanupInterval   int   `yaml:"cleanup_interval"`
}

// TunnelRateConfig 单条隧道的限流覆盖配置（可选）
type TunnelRateConfig struct {
    MaxConns int   `json:"max_conns,omitempty"`  // 覆盖全局 max_conns_per_tunnel
    MaxBPS   int64 `json:"max_bps,omitempty"`    // 覆盖全局 max_bps_per_tunnel
}
```

### 3.3 Tunnel 模型扩展

在 `core.Tunnel` 新增可选字段：

```go
type Tunnel struct {
    // ... existing fields ...
    RateLimit *TunnelRateConfig `json:"rate_limit,omitempty"` // 限流覆盖配置
}
```

- `omitempty`：不设置时 JSON 序列化不输出，旧数据完全兼容
- `nil`：使用全局默认值
- 非零值：覆盖全局配置

### 3.4 Config 结构扩展

在 `config.Config` 新增：

```go
type Config struct {
    Server    ServerConfig       `yaml:"server"`
    MQTT      MQTTConfig         `yaml:"mqtt"`
    Auth      AuthConfig         `yaml:"auth"`
    Database  DatabaseConfig     `yaml:"database"`
    Logging   LoggingConfig      `yaml:"logging"`
    Feishu    FeishuConfig       `yaml:"feishu"`
    DingTalk  DingTalkConfig     `yaml:"dingtalk"`
    RateLimit ratelimit.RateLimitConfig `yaml:"ratelimit"` // NEW
}
```

---

## 4. 各组件详细设计

### 4.1 APILimiter — API 用户级限流

```go
// internal/ratelimit/api_limiter.go

type APILimiter struct {
    mu      sync.Mutex
    users   map[string]*rate.Limiter // userID → limiter
    ips     map[string]*rate.Limiter // IP → limiter
    perUser rate.Limit               // 每秒令牌数
    perIP   rate.Limit
    burst   int
    jwtMgr  *auth.JWTManager         // 轻量解析 JWT (仅取 userID，不做 RBAC)
}

func NewAPILimiter(cfg APIRateLimitConfig, jwtMgr *auth.JWTManager) *APILimiter

// Middleware 返回 HTTP 中间件，注入到 Router.Build() 链中
func (l *APILimiter) Middleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        key, kind := l.extractKey(r) // kind="user" or "ip"
        limiter := l.getLimiter(key, kind)
        if !limiter.Allow() {
            w.Header().Set("Retry-After", "1")
            api.ResponseError(w, http.StatusTooManyRequests, 429, "rate limit exceeded")
            return
        }
        next.ServeHTTP(w, r)
    })
}

func (l *APILimiter) extractKey(r *http.Request) (string, string) {
    // 1. 尝试 Bearer Token
    if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
        if claims, err := l.jwtMgr.VerifyToken(strings.TrimPrefix(h, "Bearer ")); err == nil {
            return claims.UserID, "user"
        }
    }
    // 2. 尝试 Cookie
    if c, err := r.Cookie("token"); err == nil {
        if claims, err := l.jwtMgr.VerifyToken(c.Value); err == nil {
            return claims.UserID, "user"
        }
    }
    // 3. 回退 IP
    ip, _, _ := net.SplitHostPort(r.RemoteAddr)
    return ip, "ip"
}
```

**空闲清理**：APILimiter 每 5 分钟清理 3 分钟内未使用的 limiter 条目，防止内存泄漏。

### 4.2 ConnLimiter — 节点/隧道并发连接数限流

```go
// internal/ratelimit/conn_limiter.go

type ConnLimiter struct {
    mu              sync.Mutex
    nodeConns       map[string]int64 // nodeID → 当前连接数
    tunnelConns     map[string]int64 // sKey → 当前连接数
    tunnelOverrides map[string]connLimit // sKey → 覆盖配置
    maxConnsPerNode   int64
    maxConnsPerTunnel int64
}

type connLimit struct {
    maxConns int64
}

func (cl *ConnLimiter) AllowConn(nodeID, sKey string) bool {
    cl.mu.Lock()
    defer cl.mu.Unlock()

    // 节点级检查
    if cl.maxConnsPerNode > 0 {
        if cl.nodeConns[nodeID] >= cl.maxConnsPerNode {
            return false
        }
    }

    // 隧道级检查
    limit := cl.maxConnsPerTunnel
    if override, ok := cl.tunnelOverrides[sKey]; ok {
        limit = override.maxConns
    }
    if limit > 0 {
        if cl.tunnelConns[sKey] >= limit {
            return false
        }
    }

    return true
}

func (cl *ConnLimiter) AcquireConn(nodeID, sKey string) bool {
    if !cl.AllowConn(nodeID, sKey) {
        return false
    }
    cl.mu.Lock()
    cl.nodeConns[nodeID]++
    cl.tunnelConns[sKey]++
    cl.mu.Unlock()
    return true
}

func (cl *ConnLimiter) ReleaseConn(nodeID, sKey string) {
    cl.mu.Lock()
    cl.nodeConns[nodeID]--
    cl.tunnelConns[sKey]--
    // 清理零值条目
    if cl.nodeConns[nodeID] <= 0 {
        delete(cl.nodeConns, nodeID)
    }
    if cl.tunnelConns[sKey] <= 0 {
        delete(cl.tunnelConns, sKey)
    }
    cl.mu.Unlock()
}
```

**设计选择**：用 `int64` 计数而非 `Semaphore`/`channel`，理由：
- 需要同时检查 node 和 tunnel 两个维度
- 需要支持 `Allow`（非阻塞检查）语义
- 计数器方案 O(1) 内存开销（仅在线节点/隧道占用条目）
- 释放时清理零值条目，节点断开后自然回收

### 4.3 BWLimiter — 隧道带宽限流

```go
// internal/ratelimit/bw_limiter.go

type BWLimiter struct {
    mu         sync.Mutex
    limiters   map[string]*rate.Limiter // sKey → limiter
    overrides  map[string]int64         // sKey → 覆盖 bps
    defaultBPS int64                    // 全局默认 bytes/sec
    burst      int
}

func (bl *BWLimiter) LimiterFor(sKey string) *rate.Limiter {
    bl.mu.Lock()
    defer bl.mu.Unlock()
    if l, ok := bl.limiters[sKey]; ok {
        return l
    }
    bps := bl.defaultBPS
    if override, ok := bl.overrides[sKey]; ok {
        bps = override
    }
    if bps <= 0 {
        return nil
    }
    l := rate.NewLimiter(rate.Limit(bps), bl.burst)
    bl.limiters[sKey] = l
    return l
}

func (bl *BWLimiter) UpdateTunnelConfig(sKey string, cfg TunnelRateConfig) {
    bl.mu.Lock()
    defer bl.mu.Unlock()
    if cfg.MaxBPS > 0 {
        bl.overrides[sKey] = cfg.MaxBPS
        // 如果 limiter 已存在，更新其速率
        if l, ok := bl.limiters[sKey]; ok {
            l.SetRate(rate.Limit(cfg.MaxBPS))
        }
    } else {
        delete(bl.overrides, sKey)
        if l, ok := bl.limiters[sKey]; ok {
            l.SetRate(rate.Limit(bl.defaultBPS))
        }
    }
}

func (bl *BWLimiter) RemoveTunnel(sKey string) {
    bl.mu.Lock()
    delete(bl.limiters, sKey)
    delete(bl.overrides, sKey)
    bl.mu.Unlock()
}
```

**带宽限流行为**：`rate.Limiter.WaitN()` 令牌桶方式。超限时阻塞等待而非丢包，保证数据完整性，仅降低吞吐。

### 4.4 countingConn 扩展

```go
// internal/tunnel/counting.go — 改动

type countingConn struct {
    net.Conn
    onRead    func(int)
    onWrite   func(int)
    bwLimiter *rate.Limiter // NEW: nil = 不限速（零开销分支）
}

func (c *countingConn) Read(p []byte) (int, error) {
    n, err := c.Conn.Read(p)
    if n > 0 {
        if c.bwLimiter != nil {
            c.bwLimiter.WaitN(context.Background(), n)
        }
        if c.onRead != nil {
            c.onRead(n)
        }
    }
    return n, err
}

func (c *countingConn) Write(p []byte) (int, error) {
    n, err := c.Conn.Write(p)
    if n > 0 {
        if c.bwLimiter != nil {
            c.bwLimiter.WaitN(context.Background(), n)
        }
        if c.onWrite != nil {
            c.onWrite(n)
        }
    }
    return n, err
}
```

**改动**：新增一个 `*rate.Limiter` 字段 + 两处 nil 检查。现有调用点仅需在构造时多传一个参数。

---

## 5. 注入点改动详单

### 5.1 config/config.go

```go
// Config 结构体新增字段
type Config struct {
    // ... existing ...
    RateLimit ratelimit.RateLimitConfig `yaml:"ratelimit"`
}

// DefaultConfig() 中新增默认值
func DefaultConfig() *Config {
    return &Config{
        // ... existing ...
        RateLimit: ratelimit.RateLimitConfig{
            API: ratelimit.APIRateLimitConfig{
                PerUser: 100,
                PerIP:   30,
                Burst:   20,
            },
            Gateway: ratelimit.GatewayRateLimitConfig{
                MaxConnsPerNode:   1000,
                MaxConnsPerTunnel: 500,
                BWBurst:          65536,
                CleanupInterval:  300,
            },
        },
    }
}
```

### 5.2 api/router.go

```go
// Router 新增字段
type Router struct {
    mw      *auth.AuthMiddleware
    limiter *ratelimit.APILimiter  // NEW: nil = 不限流
    routes  []routeEntry
}

func NewRouter(mw *auth.AuthMiddleware, limiter *ratelimit.APILimiter) *Router {
    return &Router{mw: mw, limiter: limiter}
}

// Build() 中增加限流中间件
func (r *Router) Build() http.Handler {
    handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
        // ... existing route matching logic (unchanged) ...
    })

    wrapped := loggingMiddleware(handler)

    // NEW: 注入 API 限流中间件
    if r.limiter != nil {
        wrapped = r.limiter.Middleware(wrapped)
    }

    return wrapped
}
```

### 5.3 tunnel/registry.go

```go
// TunnelGateway 新增字段
type TunnelGateway struct {
    // ... existing fields ...
    limiter ratelimit.GatewayLimiter // NEW: interface, NopLimiter = 不限流
}

func NewTunnelGateway(nodeMgr NodeProvider, maxConcurrent int, limiter ratelimit.GatewayLimiter) *TunnelGateway {
    return &TunnelGateway{
        // ... existing ...
        limiter: limiter,
    }
}
```

### 5.4 tunnel/http.go

**handleHTTPProxy** (`http.go:216`)：
```go
func (tg *TunnelGateway) handleHTTPProxy(w http.ResponseWriter, r *http.Request, node *core.Node, tunnelName string) {
    sKey := statsKey(node.ID, tunnelName)

    // ★NEW: 连接数限流
    if !tg.limiter.AcquireConn(node.ID, sKey) {
        http.Error(w, "Too many connections", http.StatusTooManyRequests)
        return
    }
    defer tg.limiter.ReleaseConn(node.ID, sKey)

    tg.stats.ConnOpened(sKey)
    defer tg.stats.ConnClosed(sKey)

    // ... existing session/stream logic (unchanged) ...

    // ★NEW: 带宽限流器
    bwLimiter := tg.limiter.BWLimiterFor(sKey)

    trackedStream := &countingConn{
        Conn:      stream,
        onWrite:   func(n int) { tg.stats.RecordBytesIn(sKey, int64(n)) },
        onRead:    func(n int) { tg.stats.RecordBytesOut(sKey, int64(n)) },
        bwLimiter: bwLimiter,  // ★NEW
    }
    // ... rest unchanged ...
}
```

**handleWebSocketGateway** (`http.go:272`)：同理，在 `stats.ConnOpened` 前加 `AcquireConn`，在 `countingConn` 构造时传入 `bwLimiter`。

### 5.5 tunnel/tcp.go

**handleTCPConn** (`tcp.go:51`)：
```go
func (tg *TunnelGateway) handleTCPConn(ctx context.Context, conn net.Conn, tunnel core.Tunnel) {
    // ... existing: TCP_NODELAY, sem.Acquire ...

    node := tg.findNodeForTunnel(ctx, tunnel.Name)
    if node == nil {
        slog.Warn("No node found for TCP tunnel", "tunnel", tunnel.Name)
        return
    }

    sKey := statsKey(node.ID, tunnel.Name)

    // ★NEW: 连接数限流
    if !tg.limiter.AcquireConn(node.ID, sKey) {
        slog.Warn("TCP connection limit exceeded", "tunnel", tunnel.Name, "nodeId", node.ID)
        return
    }
    defer tg.limiter.ReleaseConn(node.ID, sKey)

    // ... existing: tunnelEnabled check, session, stream, proxy header ...

    // ★NEW: 带宽限流器
    bwLimiter := tg.limiter.BWLimiterFor(sKey)

    trackedConn := &countingConn{
        Conn:      conn,
        onRead:    func(n int) { tg.stats.RecordBytesIn(sKey, int64(n)) },
        onWrite:   func(n int) { tg.stats.RecordBytesOut(sKey, int64(n)) },
        bwLimiter: bwLimiter,  // ★NEW
    }
    biCopy(stream, trackedConn)
}
```

### 5.6 tunnel/udp.go

UDP 转发没有 `countingConn`，带宽限流直接在转发点注入。

**新会话创建处** (`udp.go:96` 附近)，在 `tg.stats.ConnOpened(newSKey)` 前：
```go
// ★NEW: 连接数限流
if !tg.limiter.AcquireConn(node.ID, newSKey) {
    slog.Debug("UDP connection limit exceeded", "tunnel", tunnel.Name)
    newStream.Close()
    continue
}
```

**数据入站转发处** (`udp.go:207` 附近)：
```go
tg.stats.RecordBytesIn(curSKey, int64(n))
// ★NEW: 带宽限流
if bwLimiter := tg.limiter.BWLimiterFor(curSKey); bwLimiter != nil {
    bwLimiter.WaitN(tunnelCtx, n)
}
if fwdStream != nil {
    fwdStream.Write(buf[:n])
}
```

**会话过期清理处** (`udp.go:69` 附近)：
```go
// 在 s.cancel() + s.stream.Close() + tg.stats.ConnClosed 之后
tg.limiter.ReleaseConn(/* 需要在此处获取 nodeID */)
```

> **注意**：UDP 会话中需将 nodeID 也存入 `udpSession` 结构体，以便清理时调用 `ReleaseConn`。当前 `udpSession` 只有 `sKey`，需要新增 `nodeID` 字段。

### 5.7 cmd/moleagent-serv/main.go

在依赖注入链中，`gateway` 创建之后、`controlSrv` 创建之前：

```go
// ★NEW: 限流器初始化
var gatewayLimiter ratelimit.GatewayLimiter = ratelimit.NopLimiter{}
var apiLimiter *ratelimit.APILimiter

if cfg.RateLimit.Gateway.Enabled {
    gatewayLimiter = ratelimit.NewGatewayLimiter(cfg.RateLimit.Gateway)
}
if cfg.RateLimit.API.Enabled {
    apiLimiter = ratelimit.NewAPILimiter(cfg.RateLimit.API, jwtMgr)
}

// 注入到 Gateway（构造函数新增参数）
gateway := tunnel.NewTunnelGateway(nodeMgr, cfg.Server.MaxConcurrent, gatewayLimiter)

// 注入到 Router（构造函数新增参数）
apiRouter := buildAPIRouter(..., apiLimiter)
```

**节点断开时清理限流状态**：

```go
disconnectHandler := func(nodeID string, tunnels []core.Tunnel) {
    tunnelSvc.ReleaseNodeResources(context.Background(), nodeID, tunnels)
    // ★NEW: 清理限流器条目
    for _, t := range tunnels {
        gatewayLimiter.RemoveTunnel(statsKey(nodeID, t.Name))
    }
}
```

**隧道配置变更时同步限流配置**（在 `TunnelConfigService.ApplyTunnel` 调用链中）：

```go
// 在 applyRuntimeTunnels 中，隧道上线时：
if t.RateLimit != nil {
    s.limiter.UpdateTunnelConfig(statsKey(nodeID, t.Name), *t.RateLimit)
}
```

---

## 6. 完整改动文件清单

| 文件 | 改动类型 | 改动量 | 说明 |
|------|---------|--------|------|
| `internal/ratelimit/limiter.go` | **新增** | ~40 行 | 接口 + NopLimiter |
| `internal/ratelimit/config.go` | **新增** | ~30 行 | 配置结构体 |
| `internal/ratelimit/api_limiter.go` | **新增** | ~80 行 | API 限流实现 |
| `internal/ratelimit/conn_limiter.go` | **新增** | ~70 行 | 连接数限流实现 |
| `internal/ratelimit/bw_limiter.go` | **新增** | ~60 行 | 带宽限流实现 |
| `internal/config/config.go` | 修改 | ~15 行 | 新增 RateLimit 配置字段 |
| `internal/core/domain.go` | 修改 | ~8 行 | Tunnel 新增 RateLimit 字段 + 结构体 |
| `internal/api/router.go` | 修改 | ~15 行 | Router 新增 limiter 字段 + 中间件注入 |
| `internal/tunnel/registry.go` | 修改 | ~5 行 | TunnelGateway 新增 limiter 字段 |
| `internal/tunnel/counting.go` | 修改 | ~8 行 | countingConn 新增 bwLimiter 字段 |
| `internal/tunnel/http.go` | 修改 | ~15 行 | handleHTTPProxy + handleWebSocketGateway 加限流 |
| `internal/tunnel/tcp.go` | 修改 | ~10 行 | handleTCPConn 加限流 |
| `internal/tunnel/udp.go` | 修改 | ~15 行 | UDP 转发加限流 + udpSession 新增 nodeID |
| `internal/service/tunnel_service.go` | 修改 | ~8 行 | 隧道配置变更时同步限流配置 |
| `cmd/moleagent-serv/main.go` | 修改 | ~15 行 | 初始化限流器 + 注入依赖 |
| `configs/config.example.yaml` | 修改 | ~15 行 | 新增 ratelimit 配置段 |

**总计**：新增 ~280 行，修改 ~130 行。

**零改动的文件**：
- 所有 Handler（auth/user/role/node/tunnel/mqtt/system/update/feishu/dingtalk）
- 所有 Repo（user/role/node/access_token/feishu/dingtalk）
- Auth 中间件、JWT、RBAC、Password
- NodeManager、HealthCheck
- TunnelConfigService（除 ApplyTunnel 调用链外）
- ControlServer、Transport
- MQTT Broker
- 前端代码

---

## 7. 分阶段实施计划

### Phase 1: 基础设施（预估 1 天）

**目标**：新增 ratelimit 包 + NopLimiter + 配置结构

**交付物**：
- [ ] `internal/ratelimit/limiter.go` — 接口 + NopLimiter
- [ ] `internal/ratelimit/config.go` — 配置结构
- [ ] `config/config.go` — Config 新增 RateLimit 字段
- [ ] `configs/config.example.yaml` — 新增配置段
- [ ] 单元测试：NopLimiter 所有方法返回零值

**验收标准**：
- `go build ./...` 编译通过
- `go test ./...` 全量测试通过
- 不启用限流时，系统行为与改动前完全一致

### Phase 2: API 用户级限流（预估 1 天）

**目标**：REST API 支持按用户/IP 请求速率限制

**交付物**：
- [ ] `internal/ratelimit/api_limiter.go`
- [ ] `internal/api/router.go` — 注入限流中间件
- [ ] `cmd/moleagent-serv/main.go` — 初始化 APILimiter
- [ ] 单元测试：限流生效/超限返回 429/空闲清理
- [ ] 集成测试：连续请求触发限流

**验收标准**：
- `enabled: true` 时，超过 per_user 阈值返回 429 + `Retry-After` header
- `enabled: false` 时，行为与 Phase 1 一致
- `/health`、`/version` 等公共端点走 IP 限流
- 管理员可通过 config 开关控制

### Phase 3: 节点级连接限流（预估 1 天）

**目标**：Gateway 支持按节点/隧道并发连接数限制

**交付物**：
- [ ] `internal/ratelimit/conn_limiter.go`
- [ ] `internal/tunnel/registry.go` — Gateway 新增 limiter 字段
- [ ] `internal/tunnel/http.go` — HTTP/WS 连接限流
- [ ] `internal/tunnel/tcp.go` — TCP 连接限流
- [ ] `internal/tunnel/udp.go` — UDP 连接限流
- [ ] `internal/tunnel/udp.go` — udpSession 新增 nodeID
- [ ] `cmd/moleagent-serv/main.go` — 初始化 GatewayLimiter
- [ ] 单元测试：ConnLimiter 并发安全、超限拒绝

**验收标准**：
- 节点连接数达到 max_conns_per_node 时，新连接被拒绝
- 隧道连接数达到 max_conns_per_tunnel 时，新连接被拒绝
- 连接关闭后计数正确递减
- 节点断开时限流状态被清理
- NopLimiter 模式下行为不变

### Phase 4: 隧道级带宽限流（预估 1 天）

**目标**：Gateway 支持按隧道带宽限制

**交付物**：
- [ ] `internal/ratelimit/bw_limiter.go`
- [ ] `internal/tunnel/counting.go` — 扩展 bwLimiter 字段
- [ ] `internal/tunnel/http.go` — 传入 bwLimiter
- [ ] `internal/tunnel/tcp.go` — 传入 bwLimiter
- [ ] `internal/tunnel/udp.go` — 带宽限流
- [ ] 单元测试：BWLimiter 速率控制准确性
- [ ] 基准测试：限流对吞吐量的影响

**验收标准**：
- 隧道带宽超过 max_bps_per_tunnel 时，吞吐量被平滑限制（降速不丢包）
- 不设限（max_bps=0）时，吞吐量与无限流一致
- `bwLimiter=nil` 时 countingConn 无额外开销

### Phase 5: 隧道级配置覆盖 + API 暴露（预估 1 天）

**目标**：支持每条隧道独立设置限流参数，通过 API 管理

**交付物**：
- [ ] `internal/core/domain.go` — Tunnel 新增 RateLimit 字段
- [ ] `internal/service/tunnel_service.go` — 配置变更时同步限流器
- [ ] `internal/api/tunnel_handler.go` — Create/Update 支持 rate_limit 参数
- [ ] `internal/api/tunnel_handler.go` — Stats API 返回限流状态
- [ ] 前端适配：隧道编辑表单新增限流配置区域
- [ ] 集成测试：动态更新限流配置立即生效

**验收标准**：
- 创建隧道时指定 `rate_limit`，限流立即生效
- 修改已有隧道的 `rate_limit`，运行时热更新
- 删除隧道后限流器被清理
- API 返回每条隧道的限流配置和当前使用率

---

## 8. 测试策略

### 8.1 单元测试

| 测试对象 | 覆盖场景 |
|---------|---------|
| `NopLimiter` | 所有方法不 panic、AllowConn 返回 true、BWLimiterFor 返回 nil |
| `APILimiter` | 用户限流生效、IP 限流生效、burst 允许突发、超限返回 429 |
| `ConnLimiter` | 节点级超限拒绝、隧道级超限拒绝、并发安全（100 goroutine 竞争）、Release 后可再次 Acquire |
| `BWLimiter` | 速率准确性（±10%）、nil limiter 无阻塞、热更新速率立即生效 |
| `countingConn` | bwLimiter=nil 时行为不变、bwLimiter 非空时 WaitN 被调用 |

### 8.2 集成测试

- 启动完整服务，配置限流，验证端到端行为
- TCP 隧道：100 并发连接，设 max_conns=50，验证后 50 个被拒绝
- HTTP 隧道：持续请求，设 max_bps=1MB/s，验证传输速率
- API：快速连续请求，验证 429 响应

### 8.3 回归测试

每个 Phase 完成后运行 `go test ./...`，确保全部现有测试通过。

---

## 9. 风险与缓解

| 风险 | 缓解措施 |
|------|---------|
| 限流器内存泄漏（大量临时 key） | ConnLimiter 释放时删除零值条目；APILimiter 定时清理过期 limiter |
| BWLimiter.WaitN 阻塞影响 goroutine | WaitN 带 context，连接关闭时 context 取消退出等待 |
| 计数器与实际连接不一致（panic/异常退出） | ReleaseConn 在 defer 中调用，与 stats.ConnClosed 同生命周期 |
| 限流配置与隧道配置不同步 | 通过 TunnelConfigService 统一入口同步，不绕过 |
| config.example.yaml 缺少新配置段导致用户困惑 | Phase 1 同步更新配置示例 |

---

## 10. 后续扩展方向

本方案完成后，可低成本扩展：

- **Per-user Gateway 限流**：基于 node.OwnerUserID 聚合，限制单用户所有节点的总带宽/总连接数
- **限流指标导出**：将当前限流状态（超限次数、排队时间）暴露给 Prometheus
- **限流告警**：频繁触发限流时通过飞书/钉钉告警
- **动态限流 API**：运行时通过 API 调整限流参数，无需重启
- **限流日志**：记录每次超限事件的 key、阈值、当前值，便于事后分析
