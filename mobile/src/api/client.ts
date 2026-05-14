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
  if (!res.ok) throw new Error(parsed?.msg || `请求失败 (${res.status})`)
  if (parsed && parsed.code !== undefined && parsed.code !== 0) throw new Error(parsed.msg || '请求失败')
  if (parsed && 'data' in parsed) return parsed.data as T
  return (parsed || raw) as T
}

interface TunnelPayload {
  name: string; type: string; target: string; domain?: string
  listen_port?: number; enabled?: boolean; node_id: string
  original_node_id?: string; para?: unknown
}

export const api = {
  login: (username: string, password: string) =>
    request<{ token: string; user: { id: string; username: string } }>('/auth/login', { method: 'POST', body: JSON.stringify({ username, password }) }),
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
  getTunnels: () => request<{ items: Tunnel[] }>('/tunnels'),
  getTunnelStats: () => request<{ nodes: Record<string, { tunnels: Record<string, TunnelStat> }> }>('/tunnels/stats'),
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
  remote_addr?: string; connected_at?: string; last_heartbeat?: string
  owner_user_id?: string
}

export interface Tunnel {
  name: string; type: string; target: string; domain?: string
  listen_port?: number; enabled?: boolean | null; para?: unknown
}

export interface TunnelStat {
  bytes_in: number; bytes_out: number; active_connections: number; total_connections: number
}
