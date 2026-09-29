# VPN 管理改进计划 — vnt-cli 集成

## 背景

当前 moleAgent_client 的 VPN 管理功能仅实现了进程级生命周期管理（启动/停止/崩溃重启），前端为空白占位页。需要集成 vnt-cli 的 REST API，实现运行状态监视和数据查询。

## 现状分析

### 已有能力
- `internal/proxy/vpn/` 包：ProcessMgr（进程启停、崩溃重启、日志捕获）、Manager（多进程管理）
- Config：Binary（name/path）、Args、Lifecycle（autostart/restart）、Watchdog、Log
- Status：Name、Running、CrashCount、PID、StartTime、CrashLogs
- 隧道类型 `vpn-manager`，通过 Para 字段传递 Config
- 前端 VPN tab 占位

### 缺失能力
- 无法查询 vnt-cli 的运行数据（设备列表、路由表、流量统计）
- 无法感知 vnt-cli 的连接状态（与 VPN 服务器是否连通）
- 启动失败时无详细错误反馈（仅记录 crash 日志，前端无法区分失败原因）
- 前端无操作界面
- 无 vnt-cli REST API 代理

## vnt-cli 关键信息

### REST API（默认启用，端口 59871）

vnt-cli 启动后 REST API **默认开启**，监听 `127.0.0.1:59871`，无需额外配置。

| 方法 | 端点 | 说明 |
|------|------|------|
| GET | `/info` | 本机设备信息（ID、名称、IP、虚拟IP等） |
| GET | `/list` | 在线设备列表 |
| GET | `/status` | 版本、CPU、内存使用 |
| GET | `/route` | 路由表 |
| GET | `/chart` | 流量统计图表数据 |

### CLI 参数

```
vnt-cli -k <token> -s <server> -d <device-id> [-n name] [-w password] [--rest-port 59871]
```

- `--rest-port` 默认 `59871`，设为 `0` 可禁用 REST API
- REST 端口写入文件 `env/rest-port`，可供外部程序发现
- moleAgent_client 启动 vnt-cli 时不传 `--rest-port`，使用其默认端口即可

## 改进方案

### Phase 1: 后端 — VPN 状态增强

#### 1.1 扩展 Config 支持 vnt-cli 参数

**文件**: `internal/proxy/vpn/config.go`

Config 新增 `VNT` 子配置：

```go
type VNTConfig struct {
    Enabled   bool   `json:"enabled"`    // 是否为 vnt-cli 程序
    Token     string `json:"token"`      // -k 参数（连接令牌）
    Server    string `json:"server"`     // -s 参数（VPN 服务器地址）
    DeviceID  string `json:"device_id"`  // -d 参数（设备标识）
    Name      string `json:"name"`       // -n 参数（设备名称）
    Password  string `json:"password"`   // -w 参数（密码）
    RestPort  int    `json:"rest_port"`  // REST API 端口，0=自动分配
}
```

Config 新增字段 `VNT VNTConfig`。

VNTConfig 中 `RestPort` 默认 `59871`（与 vnt-cli 默认值一致），设为 `0` 表示使用 vnt-cli 自身默认值。前端表单预填此默认值，用户一般无需修改。

#### 1.2 vnt-cli REST API 客户端

**新建文件**: `internal/proxy/vpn/vnt_client.go`

封装对 vnt-cli REST API 的调用：

```go
type VNTClient struct {
    baseURL string    // http://127.0.0.1:<restPort>
    client  *http.Client
}

func NewVNTClient(restPort int) *VNTClient
func (c *VNTClient) Info() (*VNTInfo, error)
func (c *VNTClient) List() (*VNTPeerList, error)
func (c *VNTClient) Status() (*VNTStatus, error)
func (c *VNTClient) Route() (*VNTRouteTable, error)
func (c *VNTClient) Chart() (*VNTChart, error)
```

返回结构体定义（对应 vnt-cli API 的 JSON 响应）：

```go
type VNTInfo struct {
    ID        string `json:"id"`
    Name      string `json:"name"`
    IP        string `json:"ip"`
    VirtualIP string `json:"virtual_ip"`
    // ... 其他字段根据实际 API 响应确定
}

type VNTPeer struct {
    ID        string `json:"id"`
    Name      string `json:"name"`
    IP        string `json:"ip"`
    VirtualIP string `json:"virtual_ip"`
    Connected bool   `json:"connected"`
}

type VNTPeerList struct {
    Peers []VNTPeer `json:"peers"`
}
```

#### 1.3 REST 端口发现

**文件**: `internal/proxy/vpn/process.go`

ProcessMgr 新增方法，进程启动后读取 vnt-cli 写入的 rest-port 文件：

```go
func (pm *ProcessMgr) discoverRestPort() (int, error)
```

读取路径：相对于工作目录的 `env/rest-port` 文件，或根据配置的端口直接使用。

ProcessMgr 新增字段 `vntClient *VNTClient`，在进程启动后初始化。

#### 1.4 扩展 Status 结构

**文件**: `internal/proxy/vpn/config.go`

Status 新增字段：

```go
type Status struct {
    // ... 现有字段保留 ...
    VNTInfo     *VNTInfo      `json:"vnt_info,omitempty"`
    VNTPeers    []VNTPeer     `json:"vnt_peers,omitempty"`
    VNTRoutes   []VNTRoute    `json:"vnt_routes,omitempty"`
    VNTStatus   *VNTStatus    `json:"vnt_status,omitempty"`
    RestPort    int           `json:"rest_port,omitempty"`
}
```

Manager 新增方法：

```go
func (m *Manager) VNTInfo(name string) (*VNTInfo, error)
func (m *Manager) VNTList(name string) (*VNTPeerList, error)
func (m *Manager) VNTRoute(name string) (*VNTRouteTable, error)
func (m *Manager) VNTStatus(name string) (*VNTStatus, error)
```

这些方法通过 ProcessMgr 的 vntClient 调用 vnt-cli REST API，失败时返回错误（进程未运行或 REST API 不可达）。

#### 1.5 启动失败诊断与反馈

**文件**: `internal/proxy/vpn/process.go`, `internal/proxy/vpn/config.go`

**目标**：捕获 vnt-cli 启动各阶段的失败原因，通过 Status 结构反馈给前端，协助用户排查问题。

**失败阶段分类**：

| 阶段 | 典型错误 | 捕获方式 |
|------|---------|---------|
| 二进制未找到 | `binary "vnt-cli" not found` | `findBinary()` 返回错误 |
| 进程启动失败 | 权限不足、端口冲突、参数错误 | `cmd.Start()` 返回错误 |
| 启动后立即退出 | 配置错误、token 无效、服务器不可达 | 进程退出码 + stderr 末尾日志 |
| REST API 不可达 | vnt-cli 启动成功但 REST 未就绪 | `VNTClient` 请求超时 |

**改动**：

1. Status 新增字段：

```go
type Status struct {
    // ... 现有字段 ...
    Error       string `json:"error,omitempty"`        // 最近一次错误描述
    ErrorPhase  string `json:"error_phase,omitempty"`  // 错误阶段：binary/startup/crash/api
    ErrorTime   int64  `json:"error_time,omitempty"`   // 错误时间戳
}
```

2. ProcessMgr 改动：
   - `Start()` 中 `findBinary()` 失败时设置 `ErrorPhase="binary"` + 错误信息
   - `cmd.Start()` 失败时设置 `ErrorPhase="startup"` + 错误信息
   - `handleExit()` 中提取退出原因（从 stderr 末尾日志解析关键字），设置 `ErrorPhase="crash"`
   - 首次 REST API 探测失败时设置 `ErrorPhase="api"`，成功后清除所有 Error 字段

3. 启动后健康探针（可选）：
   - 进程启动后，后台 goroutine 以 2s 间隔尝试 GET `/status`
   - 连续 3 次成功视为健康，清除 Error；连续 5 次失败标记 `ErrorPhase="api"`
   - 探针结果写入 Status，前端可区分"进程在跑但 VPN 未连通"

### Phase 2: 后端 — API 路由

#### 2.1 VPN 专用 API

**文件**: `internal/builtin/server.go`

在现有 `/api/tunnels` 基础上，VPN Manager 隧道的 `/api/tunnels/:name` GET 已返回包含 `status` 的完整状态。无需新增路由。

但 VPN 专用操作（启停、查询 vnt-cli 数据）可通过现有端点扩展：

| 方法 | 端点 | 说明 |
|------|------|------|
| POST | `/api/tunnels/:name/start` | 启动 VPN 进程 |
| POST | `/api/tunnels/:name/stop` | 停止 VPN 进程 |
| GET | `/api/tunnels/:name/logs` | 崩溃日志 |
| GET | `/api/tunnels/:name/peers` | vnt-cli 设备列表 |
| GET | `/api/tunnels/:name/routes` | vnt-cli 路由表 |

前三个端点在现有 server.go 中可能已有框架（vpn-manager 类型特殊处理），需确认并补全。后两个是新增。

#### 2.2 状态轮询中集成 vnt-cli 数据

**文件**: `internal/builtin/server.go`

现有的 `/api/status` 和 `/api/tunnels` 响应中，vpn-manager 类型隧道返回的 `status` 字段已包含 Status 结构。确保 Phase 1 扩展的 VNT 字段被正确序列化返回。

考虑为 vnt-cli 数据添加缓存（TTL 5-10s），避免每次前端轮询都请求 vnt-cli API。

### Phase 3: 前端 — VPN 管理页面

#### 3.1 VPN 隧道列表

**新建文件**: `internal/builtin/static/vpn.js`

参考 tunnels.js 模式：

- 表格列：名称、程序、状态（运行中/已停止/启动失败）、PID、运行时长、操作（启停/编辑/删除）
- 启动失败时，状态列显示红色错误标签（如"启动失败"），鼠标悬停或点击展开显示 `error` + `error_phase` 详细信息（如"binary: vnt-cli not found"、"startup: permission denied"、"crash: exit code 1 — invalid token"）
- 新增 VPN 隧道表单：
  - 名称
  - 程序（binary name，默认 `vnt-cli`）
  - vnt-cli 参数组：Token、服务器地址、设备ID、设备名称、密码、REST端口
  - 生命周期：自动启动、崩溃重启、最大重启次数
  - 启用开关

#### 3.2 VPN 状态面板

VPN 隧道运行时，展示 vnt-cli 的实时数据，分三个子区域：

**概览区域**：
- 本机设备信息（ID、名称、虚拟IP）
- 连接状态（与服务器是否连通）
- 运行时长、CPU/内存占用

**设备列表**：
- 在线设备表格：ID、名称、IP、虚拟IP、连接状态

**路由表**：
- 路由条目表格（如果有）

#### 3.3 index.html VPN Tab 更新

替换占位内容，加入：
- 隧道列表卡片（含新增/刷新按钮）
- VPN 隧道表单（默认隐藏）
- VPN 状态面板（运行时显示）

#### 3.4 全局刷新集成

**文件**: `internal/builtin/static/main.js`

添加 `window.__vpnRefresh` 回调，将 VPN 隧道数据分发给 vpn.js 模块。

## 实施优先级

| 优先级 | 内容 | 工作量 |
|--------|------|--------|
| P0 | 1.2 VNTClient + 1.3 端口发现 + 1.4 Status 扩展 | 1天 |
| P0 | 2.1 API 路由补全 | 0.5天 |
| P0 | 3.1-3.4 前端 VPN 页面 | 1天 |
| P1 | 1.1 Config VNT 扩展（vnt-cli 参数结构化） | 0.5天 |
| P1 | 2.2 vnt-cli 数据缓存 | 0.5天 |

**总计约 3-4 天**

## 技术决策

1. **REST 端口发现**：优先使用配置中的固定端口，其次读取 vnt-cli 的 `env/rest-port` 文件
2. **数据缓存**：vnt-cli 数据在前端 3s 轮询周期内缓存，避免过度请求
3. **错误处理**：vnt-cli API 不可达时，Status 中的 VNT 字段返回 null，前端显示"数据不可用"；启动失败时 Status.Error 包含阶段化错误信息，前端直接展示
4. **兼容性**：VPN Manager 不仅限于 vnt-cli，Config.VNT.Enabled=false 时跳过 vnt-cli 集成
5. **不修改协议层**：不改动 `internal/protocol/types.go` 的字段定义

## 验证方式

1. 配置一个 vpn-manager 隧道指向 vnt-cli，确认进程启停正常
2. 访问 `/api/tunnels/:name` 返回包含 VNT 字段的完整状态
3. 前端 VPN tab 显示隧道列表、设备列表、路由表
4. vnt-cli 未运行时前端优雅降级，不报错
5. 故意配置错误的 binary name → 前端显示"启动失败: binary not found"
6. 故意传错误 token → vnt-cli 退出 → 前端显示"崩溃: exit code X"及日志摘录
7. REST API 端口不匹配 → 前端显示"进程运行中，VPN 数据不可用"
8. `go vet ./...` 和 `go build` 通过
