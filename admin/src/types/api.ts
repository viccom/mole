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

export interface TunnelRateLimit {
  max_conns: number
  max_bandwidth: number
}

export interface NodeRateLimit {
  max_conns: number
}

export interface SysInfo {
  os?: string
  hostname?: string
  uptime_seconds?: number
  go_version?: string
  agent_version?: string
  num_cpu?: number
  mem_total_mb?: number
  mem_used_mb?: number
}

export interface ClientTunnelStatus {
  name: string
  type: string
  running: boolean
  connected?: boolean
  serial_open?: boolean
  mqtt_connected?: boolean
  clients?: number
  pid?: number
  uptime_seconds?: number
  bytes_in?: number
  bytes_out?: number
  error?: string
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
  rate_limit?: NodeRateLimit
  sysinfo?: SysInfo
  client_statuses?: ClientTunnelStatus[]
  rtt?: number
}

export interface PersistedNode {
  id: string
  name: string
  online: boolean
  owner_user_id: string
  tunnel_count: number
  tunnels: Tunnel[]
}

export interface Tunnel {
  name: string
  type: 'http' | 'https' | 'tcp' | 'udp' | 'ser2mq' | 'vpn-manager' | 'ser2tcp' | 'ser2udp' | 'webssh' | 'p2p'
  target: string
  domain?: string
  listen_port?: number
  enabled: boolean
  node_id?: string
  para?: TunnelPara
  rate_limit?: TunnelRateLimit
  effective_max_conns?: number
  effective_max_bandwidth?: number
}

export interface TunnelPara {
  // ser2mq 配置
  enable?: boolean
  broker?: string
  serial?: SerialConfig
  secret?: string
  qos?: number
  // vpn-manager 配置
  binary?: BinaryConfig
  args?: string[]
  vnt?: VNTConfig
  lifecycle?: LifecycleConfig
  watchdog?: WatchdogConfig
  log?: LogConfig
  // ser2net 配置
  mode?: string
  address?: string
  max_conn?: number
  // webssh 配置
  host?: string
  port?: number
  user?: string
  auth_type?: 'password' | 'key'
  password?: string
  priv_key?: string
  // p2p 配置（字段与服务端 core.ValidateP2PPara / 客户端 proxy/p2p.P2PConfig 一致）
  room?: string
  modes?: string[]
  relay_server?: string
  mqtt_brokers?: string[]
  stun_servers?: string[]
  protocol?: 'tcp' | 'udp'
  local_port?: number
  target_host?: string
  target_port?: number
}

export interface SerialConfig {
  port?: string
  baudrate?: number
  databits?: number
  stopbits?: number
  parity?: string
  timeout?: number
}

export interface BinaryConfig {
  name?: string
  path?: string
}

export interface VNTConfig {
  enabled?: boolean
  token?: string
  server?: string
  device_id?: string
  name?: string
  password?: string
  in_ip?: string
  out_ip?: string
  ip?: string
  rest_port?: number
}

export interface LifecycleConfig {
  autostart?: boolean
  restart_on_crash?: boolean
  max_restarts?: number
  restart_delay?: number
}

export interface WatchdogConfig {
  enabled?: boolean
  interval?: number
  quit_grace?: number
}

export interface LogConfig {
  capture?: boolean
  max_size?: number
  output_path?: string
}

export interface TunnelStats {
  total_tunnels: number
  enabled_tunnels: number
  active_tunnels: number
  http_tunnels: number
  tcp_tunnels: number
  udp_tunnels: number
  https_tunnels: number
  ser2tcp_tunnels?: number
  ser2udp_tunnels?: number
}

export interface TunnelUsageItem {
  name: string
  type: 'http' | 'https' | 'tcp' | 'udp' | 'ser2mq' | 'ser2tcp' | 'ser2udp' | 'vpn-manager' | 'webssh' | 'p2p'
  target: string
  domain?: string
  listen_port?: number
  enabled: boolean
  node_id: string
  node_status: string
  owner_user_id: string
  bytes_in: number
  bytes_out: number
  total_connections: number
  active_connections: number
  last_activity?: string
  rate_limit?: TunnelRateLimit
  effective_max_conns?: number
  effective_max_bandwidth?: number
}

export interface TunnelUsageResponse {
  items: TunnelUsageItem[]
  total: number
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

export interface VersionInfo {
  version: string
  git_hash: string
  build_date: string
  binary_path: string
  cpu_num: number
  goroutines: number
  mem_alloc_mb: number
  mem_sys_mb: number
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
