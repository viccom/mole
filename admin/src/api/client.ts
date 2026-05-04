import type { User, Role, Node, Tunnel, TunnelStats, TunnelUsageResponse, AccessToken, MQTTStats, MQTTClient, MQTTTopic, SystemMetrics, ServerConfig, VersionInfo } from '../types/api'

const API = '/api/v1'
let token = localStorage.getItem('ma_tk')

export function setToken(t: string | null) {
  token = t
  if (t) localStorage.setItem('ma_tk', t)
  else localStorage.removeItem('ma_tk')
}

export function getToken() {
  return token
}

async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  const headers = new Headers(options.headers)
  if (!headers.has('Content-Type') && options.body !== undefined) {
    headers.set('Content-Type', 'application/json')
  }
  if (token) headers.set('Authorization', `Bearer ${token}`)

  const res = await fetch(`${API}${path}`, { ...options, headers })
  const contentType = res.headers.get('content-type') || ''
  const rawBody = await res.text()
  let parsed: { code?: number; msg?: string; message?: string; data?: T } | null = null

  if (rawBody && contentType.includes('application/json')) {
    try {
      parsed = JSON.parse(rawBody) as { code?: number; msg?: string; message?: string; data?: T }
    } catch {
      parsed = null
    }
  }

  if (res.status === 401) {
    setToken(null)
    window.location.href = '/admin/login'
    throw new Error('Unauthorized')
  }

  if (!res.ok) {
    const message =
      parsed?.msg ||
      parsed?.message ||
      rawBody.trim() ||
      `Request failed (${res.status})`
    throw new Error(message)
  }

  if (!rawBody) {
    return undefined as T
  }

  if (parsed) {
    if (parsed.code !== undefined && parsed.code !== 0) {
      throw new Error(parsed.msg || parsed.message || 'Request failed')
    }
    if ('data' in parsed) {
      return parsed.data as T
    }
    return parsed as T
  }

  return rawBody as T
}

// TunnelPayload 支持所有隧道类型
interface TunnelPayload {
  name: string
  type: string
  target?: string
  domain?: string
  listen_port?: number
  enabled?: boolean
  node_id: string
  para?: {
    broker?: string
    serial?: { port?: string; baudrate?: number; databits?: number; parity?: string; timeout?: number }
    secret?: string
    binary?: { name?: string; path?: string }
    args?: string[]
    lifecycle?: { autostart?: boolean; restart_on_crash?: boolean; max_restarts?: number; restart_delay?: number }
    watchdog?: { enabled?: boolean; interval?: number; quit_grace?: number }
    log?: { capture?: boolean; max_size?: number; output_path?: string }
  }
}

export const api = {
  // Auth
  login: (username: string, password: string) =>
    request<{ token: string; user: { id: string; username: string } }>('/auth/login', {
      method: 'POST', body: JSON.stringify({ username, password }),
    }),
  logout: () => request('/auth/logout', { method: 'POST' }),
  me: () => request<{ id: string; username: string; roles: string[] }>('/auth/me'),
  changePassword: (oldPassword: string, newPassword: string) =>
    request('/auth/changepass', {
      method: 'POST', body: JSON.stringify({ old_password: oldPassword, new_password: newPassword }),
    }),

  // Users
  getUsers: () => request<User[]>('/users'),
  createUser: (data: { username: string; password: string; status: string; role_ids?: string[] }) =>
    request<User>('/users', { method: 'POST', body: JSON.stringify(data) }),
  updateUser: (id: string, data: { username?: string; password?: string; status?: string }) =>
    request<User>(`/users/${id}`, { method: 'PUT', body: JSON.stringify(data) }),
  deleteUser: (id: string) => request(`/users/${id}`, { method: 'DELETE' }),
  assignRole: (userId: string, roleId: string) =>
    request(`/users/${userId}/roles/${roleId}`, { method: 'POST' }),
  revokeRole: (userId: string, roleId: string) =>
    request(`/users/${userId}/roles/${roleId}`, { method: 'DELETE' }),
  getUserRoles: (userId: string) => request<Role[]>(`/users/${userId}/roles`),

  // Roles
  getRoles: () => request<Role[]>('/roles'),
  createRole: (data: Role) => request<Role>('/roles', { method: 'POST', body: JSON.stringify(data) }),
  updateRole: (id: string, data: Partial<Role>) =>
    request<Role>(`/roles/${id}`, { method: 'PUT', body: JSON.stringify(data) }),
  deleteRole: (id: string) => request(`/roles/${id}`, { method: 'DELETE' }),
  getPermissions: () => request<{ resource: string; action: string }[]>('/permissions'),

  // Nodes
  getNodes: () => request<{ items: Node[]; total: number }>('/nodes'),
  getNode: (id: string) => request<Node>(`/nodes/${id}`),
  createNode: (data: { name: string; token?: string; tunnels?: Tunnel[] }) =>
    request<Node>('/nodes', { method: 'POST', body: JSON.stringify(data) }),
  updateNode: (id: string, data: { name?: string; tunnels?: Tunnel[] }) =>
    request<Node>(`/nodes/${id}`, { method: 'PUT', body: JSON.stringify(data) }),
  deleteNode: (id: string) => request(`/nodes/${id}`, { method: 'DELETE' }),
  disconnectNode: (id: string) => request(`/nodes/${id}/connection`, { method: 'DELETE' }),

  // Tunnels
  getTunnels: () => request<{ items: Tunnel[]; total: number }>('/tunnels'),
  getTunnelStats: () => request<TunnelStats>('/tunnels/stats'),
  getTunnelUsage: (params?: { node_id?: string; type?: string; status?: string }) => {
    const query = new URLSearchParams()
    if (params?.node_id) query.set('node_id', params.node_id)
    if (params?.type) query.set('type', params.type)
    if (params?.status) query.set('status', params.status)
    const qs = query.toString()
    return request<TunnelUsageResponse>(`/tunnels/usage${qs ? '?' + qs : ''}`)
  },
  createTunnel: (data: TunnelPayload) =>
    request('/tunnels', { method: 'POST', body: JSON.stringify(data) }),
  updateTunnel: (data: TunnelPayload) =>
    request('/tunnels', { method: 'POST', body: JSON.stringify(data) }),
  deleteTunnel: (nodeId: string, name: string) =>
    request(`/tunnels/${encodeURIComponent(name)}?node_id=${encodeURIComponent(nodeId)}`, { method: 'DELETE' }),

  // Access Tokens
  getAccessTokens: () => request<{ items: AccessToken[]; total: number }>('/me/access-tokens'),
  createAccessToken: (name: string) =>
    request<{ id: string; name: string; token: string }>('/me/access-tokens', {
      method: 'POST', body: JSON.stringify({ name }),
    }),
  rotateAccessToken: (id: string) =>
    request<{ id: string; name: string; token: string }>(`/me/access-tokens/${id}/rotate`, { method: 'POST' }),
  deleteAccessToken: (id: string) => request(`/me/access-tokens/${id}`, { method: 'DELETE' }),

  // MQTT
  getMQTTStats: () => request<MQTTStats>('/mqtt/stats'),
  getMQTTClients: () => request<MQTTClient[]>('/mqtt/clients'),
  getMQTTTopics: () => request<MQTTTopic[]>('/mqtt/topics'),
  publishMQTT: (topic: string, payload: string, qos: number, retain: boolean) =>
    request('/mqtt/publish', { method: 'POST', body: JSON.stringify({ topic, payload, qos, retain }) }),

  // System
  getVersion: () => request<VersionInfo>('/version'),
  getMetrics: () => request<SystemMetrics>('/metrics'),
  getConfig: () => request<ServerConfig>('/config'),
  getAccessKey: () => request<{ enabled: boolean }>('/accesskey'),
  setAccessKey: (key: string) => request('/accesskey', { method: 'PUT', body: JSON.stringify({ key }) }),
  deleteAccessKey: () => request('/accesskey', { method: 'DELETE' }),
}
