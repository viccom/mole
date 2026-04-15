import type { User, Role, Node, Tunnel, TunnelStats, AccessToken, MQTTStats, MQTTClient, MQTTTopic, SystemMetrics, ServerConfig, AuthUser } from '../types/api'

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
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    ...((options.headers as Record<string, string>) || {}),
  }
  if (token) headers['Authorization'] = `Bearer ${token}`

  const res = await fetch(`${API}${path}`, { ...options, headers })

  if (res.status === 401) {
    setToken(null)
    window.location.href = '/admin/login'
    throw new Error('Unauthorized')
  }

  const data = await res.json()
  if (data.code !== undefined && data.code !== 0) {
    throw new Error(data.msg || 'Request failed')
  }
  return data.data as T
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
  createTunnel: (data: { name: string; type: string; target: string; domain?: string; listen_port?: number; enabled?: boolean; node_id: string }) =>
    request('/tunnels', { method: 'POST', body: JSON.stringify(data) }),
  deleteTunnel: (name: string) => request(`/tunnels/${name}`, { method: 'DELETE' }),

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
  getMetrics: () => request<SystemMetrics>('/metrics'),
  getConfig: () => request<ServerConfig>('/config'),
  getAccessKey: () => request<{ enabled: boolean }>('/accesskey'),
  setAccessKey: (key: string) => request('/accesskey', { method: 'PUT', body: JSON.stringify({ key }) }),
  deleteAccessKey: () => request('/accesskey', { method: 'DELETE' }),
}
