export interface ApiResponse<T = unknown> {
  code?: number
  msg?: string
  data: T
}

export interface User {
  id: string
  username: string
  status: 'active' | 'disabled'
  created_at?: string
}

export interface Role {
  id: string
  name: string
  description?: string
  permissions: Permission[]
}

export interface Permission {
  resource: string
  action: string
}

export interface Node {
  id: string
  name: string
  status: 'online' | 'offline'
  owner_user_id: string
  tunnel_count: number
  connected_at?: string
  last_heartbeat?: string
  remote_addr?: string
  token?: string
  tunnels?: Tunnel[]
}

export interface Tunnel {
  name: string
  type: 'http' | 'tcp' | 'udp'
  target: string
  domain?: string
  listen_port?: number
  enabled: boolean
  node_id?: string
}

export interface TunnelStats {
  total_tunnels: number
  enabled_tunnels: number
  active_tunnels: number
  http_tunnels: number
  tcp_tunnels: number
  udp_tunnels: number
}

export interface AccessToken {
  id: string
  name: string
  token_prefix: string
  status: 'active' | 'disabled'
  last_used_at?: string
  created_at: string
}

export interface MQTTStats {
  clients_connected: number
  subscriptions: number
  messages_published: number
}

export interface MQTTClient {
  client_id: string
  username?: string
}

export interface MQTTTopic {
  topic: string
  qos: number
  subscribers: number
}

export interface SystemMetrics {
  cpu_num: number
  goroutines: number
  memory_alloc_mb: number
  memory_sys_mb: number
  uptime_seconds: number
}

export interface ServerConfig {
  server: {
    control_port: string
    gateway_port: string
    api_port: string
    tls_enabled: boolean
  }
  mqtt: {
    enabled: boolean
    tcp_port: string
    ws_port: string
  }
  auth: {
    jwt_expiry: string
    bcrypt_cost: number
  }
  database: {
    path: string
  }
}

export interface AuthUser {
  id: string
  username: string
  token: string
  expires_at?: string
}
