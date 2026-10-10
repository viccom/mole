const API = '/api/v1'
let token = localStorage.getItem('ma_tk')

export function setToken(t: string | null) {
  token = t
  if (t) localStorage.setItem('ma_tk', t)
  else localStorage.removeItem('ma_tk')
}

export function getToken() { return token }

async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  const headers = new Headers(options.headers)
  if (!headers.has('Content-Type') && options.body !== undefined) {
    headers.set('Content-Type', 'application/json')
  }
  if (token) headers.set('Authorization', `Bearer ${token}`)
  const res = await fetch(`${API}${path}`, { ...options, headers })
  const raw = await res.text()
  let parsed: { code?: number; msg?: string; data?: T } | null = null
  try { parsed = JSON.parse(raw) } catch { parsed = null }

  if (res.status === 401) { setToken(null); window.location.href = '/mobile/'; throw new Error('Unauthorized') }
  if (!res.ok) throw new ApiError(parsed?.msg || `请求失败 (${res.status})`, res.status)
  if (parsed && parsed.code !== undefined && parsed.code !== 0) throw new Error(parsed.msg || '请求失败')
  if (parsed && 'data' in parsed) return parsed.data as T
  return (parsed || raw) as T
}

// 带 HTTP 状态码的错误：让调用方能区分"资源真不存在(404)"与"网络/服务失败"
export class ApiError extends Error {
  readonly status: number
  constructor(message: string, status: number) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

interface TunnelPayload {
  name: string; type: string; target: string; domain?: string
  listen_port?: number; enabled?: boolean; node_id: string
  original_node_id?: string; para?: unknown; rate_limit?: TunnelRateLimit
}

export const api = {
  me: () => request<{ id: string; username: string; roles: string[] }>('/auth/me'),

  feishuConfig: () => request<{ app_id: string }>('/auth/feishu/config'),
  feishuCallback: (code: string) =>
    request<{ need_bind: boolean; feishu_token?: string; feishu_name?: string; token?: string; user?: { id: string; username: string } }>('/auth/feishu/callback', { method: 'POST', body: JSON.stringify({ code }) }),
  feishuBind: (feishuToken: string, username: string, password: string) =>
    request<{ token: string; user: { id: string; username: string } }>('/auth/feishu/bind', { method: 'POST', body: JSON.stringify({ feishu_token: feishuToken, username, password }) }),
  getFeishuBinding: () => request<{ bound: boolean; feishu_name?: string }>('/me/feishu-bindings'),
  unbindFeishu: () => request('/me/feishu-bindings', { method: 'DELETE' }),

  dingtalkConfig: () => request<{ corp_id: string; app_key: string }>('/auth/dingtalk/config'),
  dingtalkCallback: (code: string, source: string) =>
    request<{ need_bind: boolean; dingtalk_token?: string; dingtalk_name?: string; token?: string; user?: { id: string; username: string } }>('/auth/dingtalk/callback', { method: 'POST', body: JSON.stringify({ code, source }) }),
  dingtalkBind: (dingtalkToken: string, username: string, password: string) =>
    request<{ token: string; user: { id: string; username: string } }>('/auth/dingtalk/bind', { method: 'POST', body: JSON.stringify({ dingtalk_token: dingtalkToken, username, password }) }),
  getDingTalkBinding: () => request<{ bound: boolean; ding_name?: string }>('/me/dingtalk-bindings'),
  unbindDingTalk: () => request('/me/dingtalk-bindings', { method: 'DELETE' }),

  getNodes: () => request<{ items: Node[] }>('/nodes'),
  getNode: (id: string) => request<Node>(`/nodes/${id}`),
  createTunnel: (data: TunnelPayload) =>
    request('/tunnels', { method: 'POST', body: JSON.stringify(data) }),
  updateTunnel: (data: TunnelPayload) =>
    request('/tunnels', { method: 'POST', body: JSON.stringify(data) }),
  deleteTunnel: (nodeId: string, name: string) =>
    request(`/tunnels/${encodeURIComponent(name)}?node_id=${encodeURIComponent(nodeId)}`, { method: 'DELETE' }),
  getVersion: () => request<{ version: string; default_domain: string }>('/version'),
}

export interface Node {
  id: string; name: string; status: string; tunnels: Tunnel[]
  // 仅列表接口的 nodeInfo 带 tunnel_count，详情接口直回 core.Node 无此字段
  tunnel_count?: number
  remote_addr?: string; connected_at?: string; last_heartbeat?: string
  owner_user_id?: string
  sysinfo?: SysInfo
  client_statuses?: ClientTunnelStatus[]
  rtt?: number
}

export interface Tunnel {
  name: string; type: string; target: string; domain?: string
  listen_port?: number; enabled?: boolean | null; para?: unknown
  rate_limit?: TunnelRateLimit
}

// per-tunnel 限速覆盖（与后端 core.TunnelRateLimit / admin 类型对齐）
// 后端字段带 omitempty：值为 0 时整个键缺失，故声明为可选
export interface TunnelRateLimit {
  max_conns?: number; max_bandwidth?: number
}

// 客户端上报的系统信息（不持久化）
export interface SysInfo {
  os?: string; hostname?: string; uptime_seconds?: number
  go_version?: string; agent_version?: string
  num_cpu?: number; mem_total_mb?: number; mem_used_mb?: number
}

// 客户端上报的隧道状态（不持久化）
export interface ClientTunnelStatus {
  name: string; type: string; running: boolean
  connected?: boolean; serial_open?: boolean; mqtt_connected?: boolean
  clients?: number; pid?: number; uptime_seconds?: number
  bytes_in?: number; bytes_out?: number; error?: string
}
