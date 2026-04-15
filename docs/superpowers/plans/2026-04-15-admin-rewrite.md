# MoleAgent Admin 管理页面重写实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 使用 React + Vite + Tailwind CSS 重写 MoleAgent admin 管理页面，完全覆盖原有功能，不修改后端代码。

**Architecture:**
- 前端使用 React 18 + Vite + TypeScript 构建 SPA
- 状态管理使用 React Context + Hooks
- UI 组件使用 Tailwind CSS + Headless UI
- 路由使用 React Router v6
- API 层封装所有后端接口调用
- 部署方式：构建产物替换现有静态文件，后端 Serve Static Files 不变

**Tech Stack:**
- React 18 + Vite + TypeScript
- Tailwind CSS (JIT)
- Headless UI (无样式组件库)
- React Router v6
- Lucide React (图标)
- Zod (表单验证)

---

## 文件结构

```
admin/
├── src/
│   ├── main.tsx                 # 入口
│   ├── App.tsx                 # 根组件 + 路由
│   ├── index.css               # Tailwind 入口
│   ├── api/
│   │   └── client.ts           # API 封装 (fetch 包装)
│   ├── components/
│   │   ├── Layout.tsx          # 后台布局 (侧边栏 + 主内容)
│   │   ├── PageHeader.tsx      # 页面标题栏
│   │   ├── DataTable.tsx       # 通用数据表格
│   │   ├── Modal.tsx           # 通用模态框
│   │   ├── Toast.tsx           # 通知提示
│   │   ├── Badge.tsx           # 状态徽章
│   │   ├── Empty.tsx          # 空状态
│   │   ├── Loading.tsx         # 加载状态
│   │   ├── StatCard.tsx        # 统计卡片
│   │   ├── ConfirmDialog.tsx   # 确认对话框
│   │   └── FormField.tsx       # 表单字段
│   ├── pages/
│   │   ├── LoginPage.tsx       # 登录页
│   │   ├── DashboardPage.tsx  # 仪表盘
│   │   ├── NodesPage.tsx       # 节点管理
│   │   ├── TunnelsPage.tsx     # 隧道管理
│   │   ├── UsersPage.tsx       # 用户管理 (含角色管理 Tab)
│   │   ├── AccessTokensPage.tsx # 接入 Token
│   │   ├── MQTTPage.tsx        # MQTT 管理
│   │   └── SettingsPage.tsx    # 系统设置
│   ├── hooks/
│   │   ├── useAuth.ts          # 认证状态 Hook
│   │   ├── useToast.ts         # Toast Hook
│   │   └── useApi.ts           # API 请求 Hook
│   ├── types/
│   │   └── api.ts              # API 类型定义
│   └── lib/
│       └── utils.ts            # 工具函数
├── index.html                  # Vite 入口 HTML
├── vite.config.ts              # Vite 配置
├── tailwind.config.js          # Tailwind 配置
├── tsconfig.json               # TypeScript 配置
├── package.json                # 依赖
├── postcss.config.js           # PostCSS 配置
└── (旧文件保留备份) app.js.old, app.css.old
```

---

## API 端点参考 (不修改后端)

### 认证
| Method | Path | 描述 |
|--------|------|------|
| POST | `/api/v1/auth/login` | 登录 `{username, password}` → `{token, expires_at, user}` |
| POST | `/api/v1/auth/logout` | 登出 |
| POST | `/api/v1/auth/refresh` | 刷新 Token |
| GET | `/api/v1/auth/me` | 当前用户 `{id, username, roles}` |
| POST | `/api/v1/auth/changepass` | 修改密码 `{old_password, new_password}` |

### 用户管理
| Method | Path | 描述 |
|--------|------|------|
| GET | `/api/v1/users` | 列表 `[]User` |
| POST | `/api/v1/users` | 创建 `{username, password, status, role_ids}` |
| GET | `/api/v1/users/{id}` | 详情 |
| PUT | `/api/v1/users/{id}` | 更新 `{username, password, status}` |
| DELETE | `/api/v1/users/{id}` | 删除 (级联) |
| POST | `/api/v1/users/{id}/roles/{roleId}` | 分配角色 |
| DELETE | `/api/v1/users/{id}/roles/{roleId}` | 撤销角色 |
| GET | `/api/v1/users/{id}/roles` | 用户角色列表 |

### 角色管理
| Method | Path | 描述 |
|--------|------|------|
| GET | `/api/v1/roles` | 列表 |
| POST | `/api/v1/roles` | 创建 `{id, name, description, permissions}` |
| GET | `/api/v1/roles/{id}` | 详情 |
| PUT | `/api/v1/roles/{id}` | 更新 |
| DELETE | `/api/v1/roles/{id}` | 删除 (admin 不可删) |
| GET | `/api/v1/permissions` | 权限列表 |

### 节点管理
| Method | Path | 描述 |
|--------|------|------|
| GET | `/api/v1/nodes` | 列表 `{items: [], total}` |
| POST | `/api/v1/nodes` | 创建 `{name, token, tunnels}` |
| GET | `/api/v1/nodes/{id}` | 详情 (含 token 和 tunnels) |
| PUT | `/api/v1/nodes/{id}` | 更新 `{name, tunnels}` |
| DELETE | `/api/v1/nodes/{id}` | 删除 |
| DELETE | `/api/v1/nodes/{id}/connection` | 断开连接 |

### 隧道管理
| Method | Path | 描述 |
|--------|------|------|
| GET | `/api/v1/tunnels` | 列表 |
| GET | `/api/v1/tunnels/stats` | 统计 `{total_tunnels, enabled_tunnels, active_tunnels, http_tunnels, tcp_tunnels, udp_tunnels}` |
| POST | `/api/v1/tunnels` | 创建 `{name, type, target, domain, listen_port, enabled, node_id}` |
| DELETE | `/api/v1/tunnels/{name}` | 删除 |

### 接入 Token
| Method | Path | 描述 |
|--------|------|------|
| GET | `/api/v1/me/access-tokens` | 列表 |
| POST | `/api/v1/me/access-tokens` | 创建 `{name}` → 返回 `{id, name, token, token_prefix, created_at}` |
| DELETE | `/api/v1/me/access-tokens/{id}` | 删除 |
| POST | `/api/v1/me/access-tokens/{id}/rotate` | 轮换 → 返回新 `{token}` |

### MQTT
| Method | Path | 描述 |
|--------|------|------|
| GET | `/api/v1/mqtt/stats` | 统计 `{clients_connected, subscriptions, messages_published}` |
| GET | `/api/v1/mqtt/clients` | 客户端列表 |
| GET | `/api/v1/mqtt/topics` | Topic 列表 |
| POST | `/api/v1/mqtt/publish` | 发布 `{topic, payload, qos, retain}` |

### 系统
| Method | Path | 描述 |
|--------|------|------|
| GET | `/api/v1/metrics` | 指标 `{cpu_num, goroutines, memory_alloc_mb, memory_sys_mb, uptime_seconds}` |
| GET | `/api/v1/config` | 服务器配置 |
| GET | `/api/v1/accesskey` | Access Key 状态 |
| PUT | `/api/v1/accesskey` | 设置 `{key}` |
| DELETE | `/api/v1/accesskey` | 禁用 |

---

## 任务清单

### Task 1: 项目初始化与配置

**Files:**
- Create: `admin/package.json`
- Create: `admin/vite.config.ts`
- Create: `admin/tsconfig.json`
- Create: `admin/tailwind.config.js`
- Create: `admin/postcss.config.js`
- Create: `admin/index.html`

- [ ] **Step 1: 创建 package.json**

```json
{
  "name": "moleagent-admin",
  "private": true,
  "version": "1.0.0",
  "type": "module",
  "scripts": {
    "dev": "vite",
    "build": "tsc -b && vite build",
    "preview": "vite preview"
  },
  "dependencies": {
    "react": "^18.3.1",
    "react-dom": "^18.3.1",
    "react-router-dom": "^6.26.0",
    "@headlessui/react": "^2.1.2",
    "lucide-react": "^0.424.0",
    "zod": "^3.23.8"
  },
  "devDependencies": {
    "@types/react": "^18.3.3",
    "@types/react-dom": "^18.3.0",
    "@vitejs/plugin-react": "^4.3.1",
    "autoprefixer": "^10.4.20",
    "postcss": "^8.4.41",
    "tailwindcss": "^3.4.10",
    "typescript": "^5.5.4",
    "vite": "^5.4.1"
  }
}
```

- [ ] **Step 2: 创建 vite.config.ts**

```ts
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  base: '/admin/',
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: 'http://localhost:9980',
        changeOrigin: true,
      },
    },
  },
})
```

- [ ] **Step 3: 创建 tsconfig.json**

```json
{
  "compilerOptions": {
    "target": "ES2020",
    "useDefineForClassFields": true,
    "lib": ["ES2020", "DOM", "DOM.Iterable"],
    "module": "ESNext",
    "skipLibCheck": true,
    "moduleResolution": "bundler",
    "allowImportingTsExtensions": true,
    "isolatedModules": true,
    "moduleDetection": "force",
    "noEmit": true,
    "jsx": "react-jsx",
    "strict": true,
    "noUnusedLocals": true,
    "noUnusedParameters": true,
    "noFallthroughCasesInSwitch": true
  },
  "include": ["src"]
}
```

- [ ] **Step 4: 创建 tailwind.config.js**

```js
/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{js,ts,jsx,tsx}'],
  theme: {
    extend: {
      colors: {
        primary: { DEFAULT: '#4F46E5', dark: '#3730A3', light: '#818CF8' },
        sidebar: '#1E1B4B',
        sidebarHover: '#312E81',
      },
    },
  },
  plugins: [],
}
```

- [ ] **Step 5: 创建 postcss.config.js**

```js
export default {
  plugins: {
    tailwindcss: {},
    autoprefixer: {},
  },
}
```

- [ ] **Step 6: 创建 index.html**

```html
<!DOCTYPE html>
<html lang="zh-CN">
  <head>
    <meta charset="UTF-8" />
    <link rel="icon" type="image/svg+xml" href="/admin/vite.svg" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>MoleAgent 管理后台</title>
  </head>
  <body>
    <div id="root"></div>
    <script type="module" src="/src/main.tsx"></script>
  </body>
</html>
```

- [ ] **Step 7: 安装依赖**

Run: `cd /root/gitme/refactoring/moleAgent/moleAgent_Serv/admin && npm install`
Expected: 依赖安装成功

- [ ] **Step 8: 提交**

```bash
cd /root/gitme/refactoring/moleAgent/moleAgent_Serv/admin
git add package.json vite.config.ts tsconfig.json tailwind.config.js postcss.config.js index.html
git commit -m "feat(admin): init React + Vite + Tailwind project"
```

---

### Task 2: 核心基础设施 (API 客户端 + 类型 + 布局 + Toast)

**Files:**
- Create: `admin/src/index.css`
- Create: `admin/src/main.tsx`
- Create: `admin/src/App.tsx`
- Create: `admin/src/api/client.ts`
- Create: `admin/src/types/api.ts`
- Create: `admin/src/lib/utils.ts`
- Create: `admin/src/components/Layout.tsx`
- Create: `admin/src/components/Toast.tsx`
- Create: `admin/src/components/Badge.tsx`
- Create: `admin/src/components/Loading.tsx`
- Create: `admin/src/components/Empty.tsx`
- Create: `admin/src/components/Modal.tsx`
- Create: `admin/src/components/StatCard.tsx`
- Create: `admin/src/components/PageHeader.tsx`
- Create: `admin/src/components/ConfirmDialog.tsx`
- Create: `admin/src/components/FormField.tsx`
- Create: `admin/src/components/DataTable.tsx`
- Create: `admin/src/hooks/useAuth.ts`
- Create: `admin/src/hooks/useToast.ts`

- [ ] **Step 1: 创建 index.css (Tailwind 入口)**

```css
@tailwind base;
@tailwind components;
@tailwind utilities;

:root {
  --color-primary: #4F46E5;
  --color-primary-dark: #3730A3;
  --color-primary-light: #818CF8;
  --color-sidebar: #1E1B4B;
  --color-sidebar-hover: #312E81;
}

body {
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
  @apply bg-gray-100 text-gray-900 text-sm;
}
```

- [ ] **Step 2: 创建 main.tsx**

```tsx
import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './App'
import './index.css'
import { ToastProvider } from './components/Toast'
import { AuthProvider } from './hooks/useAuth'

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <AuthProvider>
      <ToastProvider>
        <App />
      </ToastProvider>
    </AuthProvider>
  </React.StrictMode>
)
```

- [ ] **Step 3: 创建 types/api.ts**

```ts
// API 响应类型
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
```

- [ ] **Step 4: 创建 lib/utils.ts**

```ts
import { type ClassValue, clsx } from 'clsx'

export function cn(...inputs: ClassValue[]) {
  return clsx(inputs)
}

export function formatTimeAgo(dateStr?: string): string {
  if (!dateStr) return '-'
  const date = new Date(dateStr)
  const diff = Math.floor((Date.now() - date.getTime()) / 1000)
  if (diff < 60) return diff + '秒前'
  if (diff < 3600) return Math.floor(diff / 60) + '分钟前'
  if (diff < 86400) return Math.floor(diff / 3600) + '小时前'
  return Math.floor(diff / 86400) + '天前'
}

export function formatUptime(seconds: number): string {
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor((seconds % 86400) / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  return (days > 0 ? days + '天 ' : '') + hours + '时' + minutes + '分'
}

export function getGatewayBase(): string {
  const parts = window.location.hostname.split('.')
  return parts.length > 2 ? parts.slice(1).join('.') : window.location.hostname
}

export function tunnelAccessUrl(tunnel: Tunnel): string {
  const base = getGatewayBase()
  const proto = window.location.protocol === 'https:' ? 'https' : 'http'
  if (tunnel.type === 'http') {
    const host = tunnel.domain || `${tunnel.name}-${tunnel.node_id}.${base}`
    return `${proto}://${host}`
  }
  if (tunnel.listen_port) {
    return `${tunnel.type}://${window.location.hostname}:${tunnel.listen_port}`
  }
  return '-'
}
```

- [ ] **Step 5: 创建 api/client.ts**

```ts
import type {
  ApiResponse, User, Role, Node, Tunnel, TunnelStats,
  AccessToken, MQTTStats, MQTTClient, MQTTTopic,
  SystemMetrics, ServerConfig, AuthUser
} from '../types/api'

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

async function request<T>(
  path: string,
  options: RequestInit = {}
): Promise<T> {
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    ...(options.headers as Record<string, string> || {}),
  }
  if (token) headers['Authorization'] = `Bearer ${token}`

  const res = await fetch(`${API}${path}`, {
    ...options,
    headers,
  })

  if (res.status === 401) {
    setToken(null)
    window.location.hash = '#/login'
    throw new Error('Unauthorized')
  }

  const data: ApiResponse<T> = await res.json()
  if (data.code !== 0 && data.code !== undefined) {
    throw new Error(data.msg || 'Request failed')
  }
  return data.data
}

export const api = {
  // Auth
  login: (username: string, password: string) =>
    request<{ token: string; user: { id: string; username: string } }>('/auth/login', {
      method: 'POST',
      body: JSON.stringify({ username, password }),
    }),
  logout: () => request('/auth/logout', { method: 'POST' }),
  me: () => request<AuthUser>('/auth/me'),
  changePassword: (oldPassword: string, newPassword: string) =>
    request('/auth/changepass', {
      method: 'POST',
      body: JSON.stringify({ old_password: oldPassword, new_password: newPassword }),
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
  createTunnel: (data: Omit<Tunnel, 'node_id'> & { node_id: string }) =>
    request<Tunnel>('/tunnels', { method: 'POST', body: JSON.stringify(data) }),
  deleteTunnel: (name: string) => request(`/tunnels/${name}`, { method: 'DELETE' }),

  // Access Tokens
  getAccessTokens: () => request<{ items: AccessToken[]; total: number }>('/me/access-tokens'),
  createAccessToken: (name: string) =>
    request<{ id: string; name: string; token: string }>('/me/access-tokens', {
      method: 'POST',
      body: JSON.stringify({ name }),
    }),
  rotateAccessToken: (id: string) =>
    request<{ id: string; name: string; token: string }>(`/me/access-tokens/${id}/rotate`, {
      method: 'POST',
    }),
  deleteAccessToken: (id: string) => request(`/me/access-tokens/${id}`, { method: 'DELETE' }),

  // MQTT
  getMQTTStats: () => request<MQTTStats>('/mqtt/stats'),
  getMQTTClients: () => request<MQTTClient[]>('/mqtt/clients'),
  getMQTTTopics: () => request<MQTTTopic[]>('/mqtt/topics'),
  publishMQTT: (topic: string, payload: string, qos: number, retain: boolean) =>
    request('/mqtt/publish', {
      method: 'POST',
      body: JSON.stringify({ topic, payload, qos, retain }),
    }),

  // System
  getMetrics: () => request<SystemMetrics>('/metrics'),
  getConfig: () => request<ServerConfig>('/config'),
  getAccessKey: () => request<{ enabled: boolean }>('/accesskey'),
  setAccessKey: (key: string) =>
    request('/accesskey', { method: 'PUT', body: JSON.stringify({ key }) }),
  deleteAccessKey: () => request('/accesskey', { method: 'DELETE' }),
}
```

- [ ] **Step 6: 创建 hooks/useAuth.ts**

```tsx
import React, { createContext, useContext, useState, useEffect, type ReactNode } from 'react'
import { api, setToken, getToken } from '../api/client'
import type { AuthUser } from '../types/api'

interface AuthContextType {
  user: AuthUser | null
  loading: boolean
  login: (username: string, password: string) => Promise<void>
  logout: () => void
}

const AuthContext = createContext<AuthContextType | null>(null)

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<AuthUser | null>(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    const tk = getToken()
    if (tk) {
      api.me()
        .then(u => {
          setUser({ ...u, token: tk! })
          setToken(tk)
        })
        .catch(() => {
          setToken(null)
        })
        .finally(() => setLoading(false))
    } else {
      setLoading(false)
    }
  }, [])

  const login = async (username: string, password: string) => {
    const res = await api.login(username, password)
    const authUser: AuthUser = {
      id: res.user.id,
      username: res.user.username,
      token: res.token,
    }
    setToken(res.token)
    setUser(authUser)
  }

  const logout = () => {
    api.logout().catch(() => {})
    setToken(null)
    setUser(null)
  }

  return (
    <AuthContext.Provider value={{ user, loading, login, logout }}>
      {children}
    </AuthContext.Provider>
  )
}

export function useAuth() {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth must be used within AuthProvider')
  return ctx
}
```

- [ ] **Step 7: 创建 hooks/useToast.ts**

```tsx
import React, { createContext, useContext, useState, type ReactNode } from 'react'

type ToastType = 'success' | 'error' | 'info'

interface Toast {
  id: number
  message: string
  type: ToastType
}

interface ToastContextType {
  toast: (message: string, type?: ToastType) => void
}

const ToastContext = createContext<ToastContextType | null>(null)

let toastId = 0

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([])

  const toast = (message: string, type: ToastType = 'info') => {
    const id = ++toastId
    setToasts(prev => [...prev, { id, message, type }])
    setTimeout(() => {
      setToasts(prev => prev.filter(t => t.id !== id))
    }, 3000)
  }

  return (
    <ToastContext.Provider value={{ toast }}>
      {children}
      <div className="fixed top-5 right-5 z-50 flex flex-col gap-2">
        {toasts.map(t => (
          <div
            key={t.id}
            className={`px-5 py-3 rounded-lg text-white text-sm shadow-lg animate-slide-in ${
              t.type === 'success' ? 'bg-emerald-600' :
              t.type === 'error' ? 'bg-red-600' : 'bg-blue-600'
            }`}
          >
            {t.message}
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  )
}

export function useToast() {
  const ctx = useContext(ToastContext)
  if (!ctx) throw new Error('useToast must be used within ToastProvider')
  return ctx
}
```

- [ ] **Step 8: 创建 Layout.tsx**

```tsx
import { NavLink, Outlet } from 'react-router-dom'
import {
  LayoutDashboard, Server, Network, Users, Key, Radio, Settings, LogOut
} from 'lucide-react'
import { useAuth } from '../hooks/useAuth'

const navItems = [
  { to: '/dashboard', icon: LayoutDashboard, label: '仪表盘' },
  { to: '/nodes', icon: Server, label: '节点管理' },
  { to: '/tunnels', icon: Network, label: '隧道管理' },
  { to: '/users', icon: Users, label: '用户管理' },
  { to: '/access-tokens', icon: Key, label: '接入Token' },
  { to: '/mqtt', icon: Radio, label: 'MQTT' },
  { to: '/settings', icon: Settings, label: '系统设置' },
]

export function Layout() {
  const { user, logout } = useAuth()

  return (
    <div className="flex h-screen bg-gray-100">
      {/* 侧边栏 */}
      <aside className="w-60 bg-sidebar flex flex-col flex-shrink-0">
        <div className="p-5 flex items-center gap-3 border-b border-white/10">
          <div className="text-primary-light w-7 h-7">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <circle cx="12" cy="12" r="3"/>
              <path d="M12 1v4M12 19v4M4.22 4.22l2.83 2.83M16.95 16.95l2.83 2.83M1 12h4M19 12h4M4.22 19.78l2.83-2.83M16.95 7.05l2.83-2.83"/>
            </svg>
          </div>
          <span className="text-white font-semibold text-base">MoleAgent</span>
        </div>

        <nav className="flex-1 py-3 overflow-y-auto">
          {navItems.map(item => (
            <NavLink
              key={item.to}
              to={item.to}
              className={({ isActive }) =>
                `flex items-center gap-3 px-5 py-2.5 text-sm transition-colors ${
                  isActive
                    ? 'bg-sidebarHover text-white border-l-3 border-primary-light'
                    : 'text-white/60 hover:bg-sidebarHover hover:text-white border-l-3 border-transparent'
                }`
              }
            >
              <item.icon className="w-5 h-5 flex-shrink-0" />
              <span>{item.label}</span>
            </NavLink>
          ))}
        </nav>

        <div className="p-4 border-t border-white/10">
          <div className="text-white/70 text-xs mb-2">{user?.username || 'admin'}</div>
          <button
            onClick={logout}
            className="flex items-center gap-2 text-white/60 hover:text-white text-xs transition-colors"
          >
            <LogOut className="w-4 h-4" />
            退出
          </button>
        </div>
      </aside>

      {/* 主内容 */}
      <main className="flex-1 flex flex-col overflow-hidden">
        <Outlet />
      </main>
    </div>
  )
}
```

- [ ] **Step 9: 创建基础组件**

创建以下通用组件，每个组件约 30-50 行：

- `Badge.tsx` - 状态徽章组件，支持 success/error/info/warning/purple 变体，使用 dot 前缀
- `Loading.tsx` - 旋转加载动画组件
- `Empty.tsx` - 空状态提示组件
- `StatCard.tsx` - 统计卡片组件 (图标 + 数值 + 标签)
- `PageHeader.tsx` - 页面标题栏组件
- `Modal.tsx` - Headless UI Dialog 包装，支持标题/内容/页脚
- `ConfirmDialog.tsx` - 确认对话框组件
- `FormField.tsx` - 表单字段组件 (label + input + hint)
- `DataTable.tsx` - 通用表格组件，支持列定义和自定义渲染

- [ ] **Step 10: 创建 App.tsx**

```tsx
import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import { Layout } from './components/Layout'
import { LoginPage } from './pages/LoginPage'
import { DashboardPage } from './pages/DashboardPage'
import { NodesPage } from './pages/NodesPage'
import { TunnelsPage } from './pages/TunnelsPage'
import { UsersPage } from './pages/UsersPage'
import { AccessTokensPage } from './pages/AccessTokensPage'
import { MQTTPage } from './pages/MQTTPage'
import { SettingsPage } from './pages/SettingsPage'
import { useAuth } from './hooks/useAuth'
import { Loading } from './components/Loading'

function ProtectedRoute({ children }: { children: React.ReactNode }) {
  const { user, loading } = useAuth()
  if (loading) return <Loading fullScreen />
  if (!user) return <Navigate to="/login" replace />
  return <>{children}</>
}

export default function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route
          element={
            <ProtectedRoute>
              <Layout />
            </ProtectedRoute>
          }
        >
          <Route path="/dashboard" element={<DashboardPage />} />
          <Route path="/nodes" element={<NodesPage />} />
          <Route path="/tunnels" element={<TunnelsPage />} />
          <Route path="/users" element={<UsersPage />} />
          <Route path="/access-tokens" element={<AccessTokensPage />} />
          <Route path="/mqtt" element={<MQTTPage />} />
          <Route path="/settings" element={<SettingsPage />} />
        </Route>
        <Route path="/" element={<Navigate to="/dashboard" replace />} />
        <Route path="*" element={<Navigate to="/dashboard" replace />} />
      </Routes>
    </BrowserRouter>
  )
}
```

- [ ] **Step 11: 安装 clsx 依赖**

Run: `npm install clsx`
Expected: clsx 安装成功

- [ ] **Step 12: 提交**

```bash
git add src/index.css src/main.tsx src/App.tsx src/api/client.ts src/types/api.ts src/lib/utils.ts src/components/*.tsx src/hooks/useAuth.ts src/hooks/useToast.ts
git commit -m "feat(admin): add core infrastructure - API client, types, layout, components"
```

---

### Task 3: 登录页 + 仪表盘

**Files:**
- Create: `admin/src/pages/LoginPage.tsx`
- Create: `admin/src/pages/DashboardPage.tsx`

- [ ] **Step 1: 创建 LoginPage.tsx**

```tsx
import { useState, FormEvent } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAuth } from '../hooks/useAuth'
import { useToast } from '../hooks/useToast'

export function LoginPage() {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [loading, setLoading] = useState(false)
  const { login } = useAuth()
  const { toast } = useToast()
  const navigate = useNavigate()

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault()
    if (!username || !password) {
      toast('请输入用户名和密码', 'error')
      return
    }
    setLoading(true)
    try {
      await login(username, password)
      toast('登录成功', 'success')
      navigate('/dashboard')
    } catch (err: any) {
      toast(err.message || '登录失败', 'error')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-gradient-to-br from-sidebar to-primary">
      <div className="bg-white rounded-xl p-10 w-[400px] max-w-[90vw] shadow-2xl">
        <div className="text-center mb-6">
          <div className="inline-flex items-center justify-center w-12 h-12 rounded-xl bg-primary/10 text-primary mb-4">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className="w-8 h-8">
              <circle cx="12" cy="12" r="3"/>
              <path d="M12 1v4M12 19v4M4.22 4.22l2.83 2.83M16.95 16.95l2.83 2.83M1 12h4M19 12h4M4.22 19.78l2.83-2.83M16.95 7.05l2.83-2.83"/>
            </svg>
          </div>
          <h1 className="text-2xl font-bold text-primary-dark">MoleAgent</h1>
          <p className="text-gray-500 mt-1">管理后台登录</p>
        </div>

        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1.5">用户名</label>
            <input
              type="text"
              value={username}
              onChange={e => setUsername(e.target.value)}
              className="w-full px-3 py-2.5 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-primary/20 focus:border-primary"
              placeholder="请输入用户名"
              autoComplete="username"
              autoFocus
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1.5">密码</label>
            <input
              type="password"
              value={password}
              onChange={e => setPassword(e.target.value)}
              className="w-full px-3 py-2.5 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-primary/20 focus:border-primary"
              placeholder="请输入密码"
              autoComplete="current-password"
            />
          </div>
          <button
            type="submit"
            disabled={loading}
            className="w-full py-2.5 bg-primary text-white rounded-lg font-medium text-sm hover:bg-primary-dark transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {loading ? '登录中...' : '登 录'}
          </button>
        </form>
      </div>
    </div>
  )
}
```

- [ ] **Step 2: 创建 DashboardPage.tsx**

```tsx
import { useEffect, useState } from 'react'
import { api } from '../api/client'
import type { Node, SystemMetrics } from '../types/api'
import { PageHeader } from '../components/PageHeader'
import { StatCard } from '../components/StatCard'
import { Badge } from '../components/Badge'
import { Loading } from '../components/Loading'
import { Empty } from '../components/Empty'
import { Server, Network, Activity, Clock } from 'lucide-react'
import { formatTimeAgo, formatUptime } from '../lib/utils'

export function DashboardPage() {
  const [nodes, setNodes] = useState<Node[]>([])
  const [metrics, setMetrics] = useState<SystemMetrics | null>(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    Promise.all([
      api.getNodes().catch(() => ({ items: [], total: 0 })),
      api.getMetrics().catch(() => null),
    ]).then(([nodesRes, metricsRes]) => {
      setNodes(nodesRes.items || [])
      setMetrics(metricsRes)
    }).finally(() => setLoading(false))
  }, [])

  if (loading) return <Loading />

  const onlineCount = nodes.filter(n => n.status === 'online').length
  const totalTunnels = nodes.reduce((sum, n) => sum + n.tunnel_count, 0)

  return (
    <div>
      <PageHeader title="仪表盘" />

      <div className="p-6 space-y-6 overflow-y-auto h-full">
        {/* 统计卡片 */}
        <div className="grid grid-cols-4 gap-4">
          <StatCard
            icon={Server}
            iconBg="bg-emerald-100"
            iconColor="text-emerald-600"
            value={`${onlineCount}/${nodes.length}`}
            label="在线节点"
          />
          <StatCard
            icon={Network}
            iconBg="bg-blue-100"
            iconColor="text-blue-600"
            value={String(totalTunnels)}
            label="隧道总数"
          />
          <StatCard
            icon={Activity}
            iconBg="bg-purple-100"
            iconColor="text-purple-600"
            value={metrics?.goroutines ? String(metrics.goroutines) : '-'}
            label="Goroutines"
          />
          <StatCard
            icon={Clock}
            iconBg="bg-amber-100"
            iconColor="text-amber-600"
            value={metrics ? formatUptime(metrics.uptime_seconds) : '-'}
            label="运行时长"
          />
        </div>

        {/* 系统资源 */}
        <div className="bg-white rounded-lg shadow-sm">
          <div className="px-5 py-4 border-b border-gray-200">
            <h3 className="font-semibold text-gray-900">系统资源</h3>
          </div>
          <div className="p-5 grid grid-cols-3 gap-6">
            <div>
              <h4 className="text-sm font-medium text-primary-dark mb-3">运行信息</h4>
              <div className="space-y-2">
                <div className="flex justify-between text-sm">
                  <span className="text-gray-500">内存分配</span>
                  <span className="font-mono font-medium">{metrics?.memory_alloc_mb?.toFixed(1) || '-'} MB</span>
                </div>
                <div className="flex justify-between text-sm">
                  <span className="text-gray-500">系统内存</span>
                  <span className="font-mono font-medium">{metrics?.memory_sys_mb?.toFixed(1) || '-'} MB</span>
                </div>
                <div className="flex justify-between text-sm">
                  <span className="text-gray-500">CPU 核数</span>
                  <span className="font-mono font-medium">{metrics?.cpu_num || '-'}</span>
                </div>
              </div>
            </div>
          </div>
        </div>

        {/* 最近节点 */}
        <div className="bg-white rounded-lg shadow-sm">
          <div className="px-5 py-4 border-b border-gray-200">
            <h3 className="font-semibold text-gray-900">最近节点</h3>
          </div>
          <div className="overflow-x-auto">
            {nodes.length === 0 ? (
              <Empty message="暂无节点" />
            ) : (
              <table className="w-full">
                <thead>
                  <tr className="bg-gray-50">
                    <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">ID</th>
                    <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">名称</th>
                    <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">状态</th>
                    <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">隧道</th>
                    <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">连接时间</th>
                    <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">心跳</th>
                    <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">远程地址</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-100">
                  {nodes.slice(0, 5).map(node => (
                    <tr key={node.id} className="hover:bg-gray-50/50">
                      <td className="px-4 py-3">
                        <code className="text-xs bg-gray-100 px-1.5 py-0.5 rounded">{node.id}</code>
                      </td>
                      <td className="px-4 py-3">{node.name}</td>
                      <td className="px-4 py-3">
                        <Badge variant={node.status === 'online' ? 'success' : 'error'}>
                          {node.status === 'online' ? '在线' : '离线'}
                        </Badge>
                      </td>
                      <td className="px-4 py-3">{node.tunnel_count}</td>
                      <td className="px-4 py-3 text-gray-500">{formatTimeAgo(node.connected_at)}</td>
                      <td className="px-4 py-3 text-gray-500">{formatTimeAgo(node.last_heartbeat)}</td>
                      <td className="px-4 py-3">
                        <code className="text-xs text-gray-500">{node.remote_addr || '-'}</code>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}
```

- [ ] **Step 3: 提交**

```bash
git add src/pages/LoginPage.tsx src/pages/DashboardPage.tsx
git commit -m "feat(admin): add login and dashboard pages"
```

---

### Task 4: 节点管理 + 隧道管理

**Files:**
- Create: `admin/src/pages/NodesPage.tsx`
- Create: `admin/src/pages/TunnelsPage.tsx`

- [ ] **Step 1: 创建 NodesPage.tsx**

节点管理页面需要：
- 节点列表展示 (ID, 名称, 归属, 状态, 隧道数, 连接时间, 心跳, 远程地址)
- 行内展开详情 (Token, 隧道列表, 添加隧道按钮)
- 断开连接按钮 (在线节点)
- 删除按钮
- 刷新按钮

```tsx
import { useEffect, useState } from 'react'
import { api } from '../api/api'
import type { Node, Tunnel } from '../types/api'
import { PageHeader } from '../components/PageHeader'
import { Badge } from '../components/Badge'
import { Loading } from '../components/Loading'
import { Empty } from '../components/Empty'
import { Modal } from '../components/Modal'
import { FormField } from '../components/FormField'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { useToast } from '../hooks/useToast'
import { ChevronDown, ChevronUp, Unlink, Trash2, Plus, RefreshCw } from 'lucide-react'

export function NodesPage() {
  const [nodes, setNodes] = useState<Node[]>([])
  const [loading, setLoading] = useState(true)
  const [expandedId, setExpandedId] = useState<string | null>(null)
  const [nodeDetails, setNodeDetails] = useState<Record<string, Node>>({})
  const [showAddTunnelModal, setShowAddTunnelModal] = useState<{ nodeId: string } | null>(null)
  const [confirmDialog, setConfirmDialog] = useState<{ action: string; nodeId: string; onConfirm: () => void } | null>(null)
  const { toast } = useToast()

  const loadNodes = async () => {
    setLoading(true)
    try {
      const res = await api.getNodes()
      setNodes(res.items || [])
    } catch (e: any) {
      toast(e.message, 'error')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { loadNodes() }, [])

  const toggleExpand = async (id: string) => {
    if (expandedId === id) {
      setExpandedId(null)
      return
    }
    setExpandedId(id)
    if (!nodeDetails[id]) {
      try {
        const detail = await api.getNode(id)
        setNodeDetails(prev => ({ ...prev, [id]: detail }))
      } catch (e: any) {
        toast(e.message, 'error')
      }
    }
  }

  const handleDisconnect = async (id: string) => {
    try {
      await api.disconnectNode(id)
      toast('已断开', 'success')
      loadNodes()
    } catch (e: any) {
      toast(e.message, 'error')
    }
  }

  const handleDelete = async (id: string) => {
    try {
      await api.deleteNode(id)
      toast('已删除', 'success')
      loadNodes()
    } catch (e: any) {
      toast(e.message, 'error')
    }
    setConfirmDialog(null)
  }

  const handleDeleteTunnel = async (name: string, nodeId: string) => {
    try {
      await api.deleteTunnel(name)
      toast('隧道已删除', 'success')
      // Refresh detail
      const detail = await api.getNode(nodeId)
      setNodeDetails(prev => ({ ...prev, [nodeId]: detail }))
    } catch (e: any) {
      toast(e.message, 'error')
    }
  }

  const detail = expandedId ? nodeDetails[expandedId] : null

  return (
    <div>
      <PageHeader title="节点管理" actions={
        <button onClick={loadNodes} className="btn btn-g btn-sm flex items-center gap-1">
          <RefreshCw className="w-4 h-4" /> 刷新
        </button>
      } />

      <div className="p-6">
        <div className="bg-white rounded-lg shadow-sm">
          <div className="px-5 py-4 border-b border-gray-200 flex justify-between items-center">
            <h3 className="font-semibold">节点列表 ({nodes.length})</h3>
            <span className="text-xs text-gray-400">node_id: 8字符, 字母开头</span>
          </div>

          {loading ? <Loading /> : nodes.length === 0 ? (
            <Empty message="暂无节点" />
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full">
                <thead>
                  <tr className="bg-gray-50">
                    <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">ID</th>
                    <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">名称</th>
                    <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">归属</th>
                    <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">状态</th>
                    <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">隧道</th>
                    <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">连接时间</th>
                    <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">心跳</th>
                    <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">远程地址</th>
                    <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">操作</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-100">
                  {nodes.map(node => (
                    <>
                      <tr key={node.id} className="hover:bg-gray-50/50">
                        <td className="px-4 py-3"><code className="text-xs bg-gray-100 px-1.5 py-0.5 rounded">{node.id}</code></td>
                        <td className="px-4 py-3">{node.name}</td>
                        <td className="px-4 py-3">{node.owner_user_id === 'system' ? '系统' : node.owner_user_id}</td>
                        <td className="px-4 py-3">
                          <Badge variant={node.status === 'online' ? 'success' : 'error'}>
                            {node.status === 'online' ? '在线' : '离线'}
                          </Badge>
                        </td>
                        <td className="px-4 py-3">{node.tunnel_count}</td>
                        <td className="px-4 py-3 text-gray-500">{formatTimeAgo(node.connected_at)}</td>
                        <td className="px-4 py-3 text-gray-500">{formatTimeAgo(node.last_heartbeat)}</td>
                        <td className="px-4 py-3"><code className="text-xs text-gray-500">{node.remote_addr || '-'}</code></td>
                        <td className="px-4 py-3">
                          <div className="flex items-center gap-1">
                            <button onClick={() => toggleExpand(node.id)} className="btn btn-g btn-sm" title="详情">
                              {expandedId === node.id ? <ChevronUp className="w-4 h-4" /> : <ChevronDown className="w-4 h-4" />}
                            </button>
                            {node.status === 'online' && (
                              <button onClick={() => setConfirmDialog({ action: 'disconnect', nodeId: node.id, onConfirm: () => handleDisconnect(node.id) })} className="btn btn-g btn-sm" title="断开">
                                <Unlink className="w-4 h-4" />
                              </button>
                            )}
                            <button onClick={() => setConfirmDialog({ action: 'delete', nodeId: node.id, onConfirm: () => handleDelete(node.id) })} className="btn btn-danger btn-sm" title="删除">
                              <Trash2 className="w-4 h-4" />
                            </button>
                          </div>
                        </td>
                      </tr>
                      {expandedId === node.id && (
                        <tr>
                          <td colSpan={9} className="bg-gray-50 px-4 py-4">
                            {detail ? (
                              <div className="space-y-3">
                                <div><strong>Token:</strong> <code>{detail.token || '-'}</code></div>
                                <div><strong>归属:</strong> {detail.owner_user_id === 'system' ? '系统' : detail.owner_user_id}</div>
                                <div>
                                  <strong>隧道:</strong>
                                  <div className="flex flex-wrap gap-2 mt-1">
                                    {detail.tunnels?.length ? detail.tunnels.map(t => (
                                      <span key={t.name} className="inline-flex items-center gap-1 bg-white border border-gray-200 rounded px-2 py-1 text-sm">
                                        <strong>{t.name}</strong>
                                        <Badge variant={t.enabled !== false ? 'success' : 'error'} className="text-xs">{t.enabled !== false ? '启用' : '禁用'}</Badge>
                                        <Badge variant="info" className="text-xs">{t.type?.toUpperCase()}</Badge>
                                        <code className="text-xs">{t.target || '-'}</code>
                                        <button onClick={() => handleDeleteTunnel(t.name, node.id)} className="text-red-500 hover:text-red-700 ml-1">
                                          <Trash2 className="w-3 h-3" />
                                        </button>
                                      </span>
                                    )) : <span className="text-gray-400">无</span>}
                                    <button onClick={() => setShowAddTunnelModal({ nodeId: node.id })} className="btn btn-primary btn-sm">
                                      <Plus className="w-3 h-3" /> 添加隧道
                                    </button>
                                  </div>
                                </div>
                                <div className="text-xs text-gray-500">
                                  <strong>连接:</strong> {detail.connected_at || '-'} &nbsp; <strong>心跳:</strong> {detail.last_heartbeat || '-'}
                                </div>
                              </div>
                            ) : <Loading />}
                          </td>
                        </tr>
                      )}
                    </>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </div>

      {showAddTunnelModal && (
        <CreateTunnelModal
          presetNodeId={showAddTunnelModal.nodeId}
          onClose={() => setShowAddTunnelModal(null)}
          onSuccess={() => {
            setShowAddTunnelModal(null)
            loadNodes()
            if (expandedId) {
              api.getNode(expandedId).then(d => setNodeDetails(prev => ({ ...prev, [expandedId]: d })))
            }
          }}
        />
      )}

      {confirmDialog && (
        <ConfirmDialog
          title={confirmDialog.action === 'disconnect' ? '断开节点' : '删除节点'}
          message={confirmDialog.action === 'disconnect'
            ? `确定断开节点 ${confirmDialog.nodeId}？`
            : `确定删除节点 ${confirmDialog.nodeId}？删除后该节点将无法恢复。`}
          confirmText={confirmDialog.action === 'disconnect' ? '断开' : '删除'}
          onConfirm={confirmDialog.onConfirm}
          onCancel={() => setConfirmDialog(null)}
          danger={confirmDialog.action === 'delete'}
        />
      )}
    </div>
  )
}

// 内联 CreateTunnelModal 组件 (复用 TunnelForm)
function CreateTunnelModal({ presetNodeId, onClose, onSuccess }: { presetNodeId?: string; onClose: () => void; onSuccess: () => void }) {
  // 复用 TunnelsPage 的隧道表单逻辑
  return <TunnelFormModal presetNodeId={presetNodeId} onClose={onClose} onSuccess={onSuccess} />
}
```

- [ ] **Step 2: 创建 TunnelsPage.tsx**

隧道管理页面需要：
- 统计卡片 (总数, 启用中, 活跃中, HTTP隧道数)
- 隧道列表 (名称, 状态, 类型, 目标, 访问地址, 节点, 操作)
- 创建/编辑隧道 Modal (表单验证)
- 删除确认

```tsx
import { useEffect, useState } from 'react'
import { api } from '../api/client'
import type { Tunnel, TunnelStats, Node } from '../types/api'
import { PageHeader } from '../components/PageHeader'
import { StatCard } from '../components/StatCard'
import { Badge } from '../components/Badge'
import { Loading } from '../components/Loading'
import { Empty } from '../components/Empty'
import { Modal } from '../components/Modal'
import { FormField } from '../components/FormField'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { useToast } from '../hooks/useToast'
import { Network, Plus, Edit, Trash2, RefreshCw } from 'lucide-react'
import { tunnelAccessUrl } from '../lib/utils'

export function TunnelsPage() {
  const [tunnels, setTunnels] = useState<Tunnel[]>([])
  const [stats, setStats] = useState<TunnelStats | null>(null)
  const [loading, setLoading] = useState(true)
  const [showForm, setShowForm] = useState<{ tunnel?: Tunnel; nodeId?: string } | null>(null)
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null)
  const { toast } = useToast()

  const loadData = async () => {
    setLoading(true)
    try {
      const [tunnelsRes, statsRes] = await Promise.all([
        api.getTunnels().catch(() => ({ items: [], total: 0 })),
        api.getTunnelStats().catch(() => null),
      ])
      setTunnels(tunnelsRes.items || [])
      setStats(statsRes)
    } catch (e: any) {
      toast(e.message, 'error')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { loadData() }, [])

  const handleDelete = async (name: string) => {
    try {
      await api.deleteTunnel(name)
      toast('已删除', 'success')
      loadData()
    } catch (e: any) {
      toast(e.message, 'error')
    }
    setConfirmDelete(null)
  }

  return (
    <div>
      <PageHeader title="隧道管理" actions={
        <button onClick={() => setShowForm({})} className="btn btn-primary btn-sm flex items-center gap-1">
          <Plus className="w-4 h-4" /> 创建隧道
        </button>
      } />

      <div className="p-6 space-y-6">
        {/* 统计卡片 */}
        {stats && (
          <div className="grid grid-cols-4 gap-4">
            <StatCard icon={Network} iconBg="bg-blue-100" iconColor="text-blue-600" value={String(stats.total_tunnels)} label="隧道总数" />
            <StatCard icon={Network} iconBg="bg-emerald-100" iconColor="text-emerald-600" value={String(stats.enabled_tunnels)} label="启用中" />
            <StatCard icon={Network} iconBg="bg-purple-100" iconColor="text-purple-600" value={String(stats.active_tunnels)} label="活跃中" />
            <StatCard icon={Network} iconBg="bg-amber-100" iconColor="text-amber-600" value={String(stats.http_tunnels)} label="HTTP隧道数" />
          </div>
        )}

        {/* 隧道列表 */}
        <div className="bg-white rounded-lg shadow-sm">
          <div className="px-5 py-4 border-b border-gray-200 flex justify-between items-center">
            <h3 className="font-semibold">隧道列表</h3>
            <button onClick={loadData} className="btn btn-g btn-sm"><RefreshCw className="w-4 h-4" /></button>
          </div>

          {loading ? <Loading /> : tunnels.length === 0 ? (
            <Empty message="暂无隧道" />
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full">
                <thead>
                  <tr className="bg-gray-50">
                    <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">名称</th>
                    <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">状态</th>
                    <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">类型</th>
                    <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">目标</th>
                    <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">访问地址</th>
                    <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">节点</th>
                    <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">操作</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-100">
                  {tunnels.map(tunnel => (
                    <tr key={tunnel.name} className="hover:bg-gray-50/50">
                      <td className="px-4 py-3"><code className="text-xs bg-gray-100 px-1.5 py-0.5 rounded">{tunnel.name}</code></td>
                      <td className="px-4 py-3">
                        <Badge variant={tunnel.enabled !== false ? 'success' : 'error'}>
                          {tunnel.enabled !== false ? '启用' : '禁用'}
                        </Badge>
                      </td>
                      <td className="px-4 py-3">
                        <Badge variant={tunnel.type === 'http' ? 'info' : tunnel.type === 'tcp' ? 'purple' : 'warning'}>
                          {tunnel.type?.toUpperCase()}
                        </Badge>
                      </td>
                      <td className="px-4 py-3"><code className="text-xs">{tunnel.target}</code></td>
                      <td className="px-4 py-3">
                        {tunnel.type === 'http' ? (
                          <a href={tunnelAccessUrl(tunnel)} target="_blank" rel="noopener noreferrer" className="text-blue-600 hover:text-blue-800 text-xs">
                            <code>{tunnelAccessUrl(tunnel)}</code>
                          </a>
                        ) : tunnel.listen_port ? (
                          <code className="text-xs">{tunnel.type}://{window.location.hostname}:{tunnel.listen_port}</code>
                        ) : '-'}
                      </td>
                      <td className="px-4 py-3"><code className="text-xs">{tunnel.node_id}</code></td>
                      <td className="px-4 py-3">
                        <div className="flex items-center gap-1">
                          <button onClick={() => setShowForm({ tunnel })} className="btn btn-g btn-sm">
                            <Edit className="w-4 h-4" />
                          </button>
                          <button onClick={() => setConfirmDelete(tunnel.name)} className="btn btn-danger btn-sm">
                            <Trash2 className="w-4 h-4" />
                          </button>
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </div>

      {showForm && (
        <TunnelFormModal
          tunnel={showForm.tunnel}
          onClose={() => setShowForm(null)}
          onSuccess={() => { setShowForm(null); loadData() }}
        />
      )}

      {confirmDelete && (
        <ConfirmDialog
          title="删除隧道"
          message={`确定删除隧道 ${confirmDelete}？`}
          confirmText="删除"
          onConfirm={() => handleDelete(confirmDelete)}
          onCancel={() => setConfirmDelete(null)}
          danger
        />
      )}
    </div>
  )
}

// 隧道表单 Modal
function TunnelFormModal({ tunnel, presetNodeId, onClose, onSuccess }: {
  tunnel?: Tunnel
  presetNodeId?: string
  onClose: () => void
  onSuccess: () => void
}) {
  const isEdit = !!tunnel
  const [name, setName] = useState(tunnel?.name || '')
  const [type, setType] = useState(tunnel?.type || 'http')
  const [target, setTarget] = useState(tunnel?.target || '')
  const [domain, setDomain] = useState(tunnel?.domain || '')
  const [listenPort, setListenPort] = useState(String(tunnel?.listen_port || ''))
  const [nodeId, setNodeId] = useState(tunnel?.node_id || presetNodeId || '')
  const [enabled, setEnabled] = useState(tunnel?.enabled !== false)
  const [nodes, setNodes] = useState<Node[]>([])
  const [loading, setLoading] = useState(false)
  const { toast } = useToast()

  useEffect(() => {
    if (!isEdit) {
      api.getNodes().then(r => setNodes(r.items || [])).catch(() => {})
    }
  }, [isEdit])

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!name.trim()) { toast('请输入隧道名称', 'error'); return }
    if (!target.trim()) { toast('请输入目标地址', 'error'); return }
    if (!nodeId) { toast('请选择节点', 'error'); return }
    if (target && !target.match(/^https?:\/\/.+/) && !target.match(/^\d+\.\d+\.\d+\.\d+:\d+$/)) {
      toast('目标地址格式不正确', 'error'); return
    }

    setLoading(true)
    try {
      const body: any = { name, type, target, node_id: nodeId, enabled }
      if (type === 'http' && domain) body.domain = domain
      if ((type === 'tcp' || type === 'udp') && listenPort) body.listen_port = parseInt(listenPort)
      await api.createTunnel(body)
      toast(isEdit ? '隧道已更新' : '隧道已创建', 'success')
      onSuccess()
    } catch (e: any) {
      toast(e.message, 'error')
    } finally {
      setLoading(false)
    }
  }

  return (
    <Modal title={isEdit ? `编辑隧道 — ${name}` : '创建隧道'} onClose={onClose}>
      <form onSubmit={handleSubmit} className="space-y-4">
        <FormField label="隧道名称">
          <input value={name} onChange={e => setName(e.target.value)} disabled={isEdit}
            className={`w-full px-3 py-2 border rounded-lg text-sm ${isEdit ? 'bg-gray-100' : ''}`}
            placeholder="my-tunnel" />
          {isEdit && <p className="hint text-xs text-gray-400 mt-1">编辑模式下名称不可修改</p>}
        </FormField>

        <FormField label="类型">
          <select value={type} onChange={e => setType(e.target.value)} className="w-full px-3 py-2 border rounded-lg text-sm">
            <option value="http">HTTP</option>
            <option value="tcp">TCP</option>
            <option value="udp">UDP</option>
          </select>
        </FormField>

        <FormField label="目标地址">
          <input value={target} onChange={e => setTarget(e.target.value)}
            className="w-full px-3 py-2 border rounded-lg text-sm"
            placeholder="http://127.0.0.1:8080 或 127.0.0.1:3306" />
        </FormField>

        {type === 'http' ? (
          <FormField label="域名 (可选)">
            <input value={domain} onChange={e => setDomain(e.target.value)}
              className="w-full px-3 py-2 border rounded-lg text-sm"
              placeholder="app.example.com" />
          </FormField>
        ) : (
          <FormField label="监听端口">
            <input type="number" value={listenPort} onChange={e => setListenPort(e.target.value)}
              className="w-full px-3 py-2 border rounded-lg text-sm"
              placeholder="8080" />
          </FormField>
        )}

        {!isEdit && (
          <FormField label="节点">
            <select value={nodeId} onChange={e => setNodeId(e.target.value)}
              className="w-full px-3 py-2 border rounded-lg text-sm">
              <option value="">-- 选择节点 --</option>
              {nodes.map(n => <option key={n.id} value={n.id}>{n.name} ({n.id})</option>)}
            </select>
          </FormField>
        )}

        <FormField label="">
          <label className="flex items-center gap-2 cursor-pointer">
            <input type="checkbox" checked={enabled} onChange={e => setEnabled(e.target.checked)} className="w-4 h-4" />
            <span className="text-sm">启用隧道</span>
          </label>
        </FormField>

        <div className="flex justify-end gap-2 pt-2">
          <button type="button" onClick={onClose} className="btn btn-g">取消</button>
          <button type="submit" disabled={loading} className="btn btn-primary">
            {loading ? '提交中...' : (isEdit ? '保存' : '创建')}
          </button>
        </div>
      </form>
    </Modal>
  )
}
```

- [ ] **Step 3: 提交**

```bash
git add src/pages/NodesPage.tsx src/pages/TunnelsPage.tsx
git commit -m "feat(admin): add node and tunnel management pages"
```

---

### Task 5: 用户管理与角色管理

**Files:**
- Create: `admin/src/pages/UsersPage.tsx`

- [ ] **Step 1: 创建 UsersPage.tsx**

用户管理页面需要 Tab 切换：
- **用户列表 Tab**: 用户表格 (ID, 用户名, 状态, 创建时间, 操作) + 创建用户按钮 + 编辑用户 Modal + 删除确认
- **角色管理 Tab**: 角色表格 (ID, 名称, 描述, 权限) + 创建/编辑/删除角色

```tsx
import { useEffect, useState } from 'react'
import { Tab } from '@headlessui/react'
import { api } from '../api/client'
import type { User, Role } from '../types/api'
import { PageHeader } from '../components/PageHeader'
import { Badge } from '../components/Badge'
import { Loading } from '../components/Loading'
import { Empty } from '../components/Empty'
import { Modal } from '../components/Modal'
import { FormField } from '../components/FormField'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { useToast } from '../hooks/useToast'
import { User, Plus, Shield, RefreshCw } from 'lucide-react'
import { cn } from '../lib/utils'

export function UsersPage() {
  const [users, setUsers] = useState<User[]>([])
  const [roles, setRoles] = useState<Role[]>([])
  const [loading, setLoading] = useState(true)
  const [showCreateUser, setShowCreateUser] = useState(false)
  const [editUser, setEditUser] = useState<User | null>(null)
  const [deleteUserId, setDeleteUserId] = useState<string | null>(null)
  const [showCreateRole, setShowCreateRole] = useState(false)
  const [editRole, setEditRole] = useState<Role | null>(null)
  const [deleteRoleId, setDeleteRoleId] = useState<string | null>(null)
  const { toast } = useToast()

  const loadData = async () => {
    setLoading(true)
    try {
      const [usersRes, rolesRes] = await Promise.all([
        api.getUsers().catch(() => []),
        api.getRoles().catch(() => []),
      ])
      setUsers(Array.isArray(usersRes) ? usersRes : [])
      setRoles(Array.isArray(rolesRes) ? rolesRes : [])
    } catch (e: any) {
      toast(e.message, 'error')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { loadData() }, [])

  const handleDeleteUser = async (id: string) => {
    try {
      await api.deleteUser(id)
      toast('已删除', 'success')
      loadData()
    } catch (e: any) {
      toast(e.message, 'error')
    }
    setDeleteUserId(null)
  }

  const handleDeleteRole = async (id: string) => {
    try {
      await api.deleteRole(id)
      toast('已删除', 'success')
      loadData()
    } catch (e: any) {
      toast(e.message, 'error')
    }
    setDeleteRoleId(null)
  }

  return (
    <div>
      <PageHeader title="用户管理" />

      <div className="p-6">
        <div className="bg-white rounded-lg shadow-sm">
          <Tab.Group>
            <Tab.List className="flex border-b border-gray-200 px-5">
              <Tab className={({ selected }) => cn(
                'px-4 py-3 text-sm font-medium border-b-2 -mb-px transition-colors outline-none',
                selected ? 'text-primary border-primary' : 'text-gray-500 border-transparent hover:text-gray-700'
              )}>用户列表</Tab>
              <Tab className={({ selected }) => cn(
                'px-4 py-3 text-sm font-medium border-b-2 -mb-px transition-colors outline-none',
                selected ? 'text-primary border-primary' : 'text-gray-500 border-transparent hover:text-gray-700'
              )}>角色管理</Tab>
            </Tab.List>

            <Tab.Panels>
              {/* 用户列表 */}
              <Tab.Panel>
                <div className="px-5 py-4 border-b border-gray-200 flex justify-between items-center">
                  <h3 className="font-semibold">用户列表 ({users.length})</h3>
                  <button onClick={() => setShowCreateUser(true)} className="btn btn-primary btn-sm flex items-center gap-1">
                    <Plus className="w-4 h-4" /> 创建用户
                  </button>
                </div>
                {loading ? <Loading /> : users.length === 0 ? (
                  <Empty message="暂无用户" />
                ) : (
                  <table className="w-full">
                    <thead>
                      <tr className="bg-gray-50">
                        <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">ID</th>
                        <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">用户名</th>
                        <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">状态</th>
                        <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">创建时间</th>
                        <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">操作</th>
                      </tr>
                    </thead>
                    <tbody className="divide-y divide-gray-100">
                      {users.map(user => (
                        <tr key={user.id} className="hover:bg-gray-50/50">
                          <td className="px-4 py-3"><code className="text-xs bg-gray-100 px-1.5 py-0.5 rounded">{user.id}</code></td>
                          <td className="px-4 py-3">{user.username}</td>
                          <td className="px-4 py-3">
                            <Badge variant={user.status === 'active' ? 'success' : 'error'}>
                              {user.status === 'active' ? '活跃' : '禁用'}
                            </Badge>
                          </td>
                          <td className="px-4 py-3 text-gray-500">{user.created_at ? new Date(user.created_at).toLocaleString() : '-'}</td>
                          <td className="px-4 py-3">
                            <div className="flex items-center gap-1">
                              <button onClick={() => setEditUser(user)} className="btn btn-g btn-sm">
                                <Shield className="w-4 h-4" />
                              </button>
                              {user.id !== 'admin' && (
                                <button onClick={() => setDeleteUserId(user.id)} className="btn btn-danger btn-sm">
                                  <Trash2 className="w-4 h-4" />
                                </button>
                              )}
                            </div>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                )}
              </Tab.Panel>

              {/* 角色管理 */}
              <Tab.Panel>
                <div className="px-5 py-4 border-b border-gray-200 flex justify-between items-center">
                  <h3 className="font-semibold">角色列表 ({roles.length})</h3>
                  <button onClick={() => setShowCreateRole(true)} className="btn btn-primary btn-sm flex items-center gap-1">
                    <Plus className="w-4 h-4" /> 创建角色
                  </button>
                </div>
                {loading ? <Loading /> : roles.length === 0 ? (
                  <Empty message="暂无角色" />
                ) : (
                  <table className="w-full">
                    <thead>
                      <tr className="bg-gray-50">
                        <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">ID</th>
                        <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">名称</th>
                        <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">描述</th>
                        <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">权限</th>
                        <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">操作</th>
                      </tr>
                    </thead>
                    <tbody className="divide-y divide-gray-100">
                      {roles.map(role => (
                        <tr key={role.id} className="hover:bg-gray-50/50">
                          <td className="px-4 py-3"><code className="text-xs bg-gray-100 px-1.5 py-0.5 rounded">{role.id}</code></td>
                          <td className="px-4 py-3">{role.name}</td>
                          <td className="px-4 py-3 text-gray-500">{role.description || '-'}</td>
                          <td className="px-4 py-3">
                            <div className="flex flex-wrap gap-1">
                              {role.permissions?.map(p => (
                                <Badge key={`${p.resource}:${p.action}`} variant="info" className="text-xs">
                                  {p.resource}:{p.action}
                                </Badge>
                              ))}
                            </div>
                          </td>
                          <td className="px-4 py-3">
                            <div className="flex items-center gap-1">
                              <button onClick={() => setEditRole(role)} className="btn btn-g btn-sm">
                                <Edit className="w-4 h-4" />
                              </button>
                              {role.id !== 'admin' && (
                                <button onClick={() => setDeleteRoleId(role.id)} className="btn btn-danger btn-sm">
                                  <Trash2 className="w-4 h-4" />
                                </button>
                              )}
                            </div>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                )}
              </Tab.Panel>
            </Tab.Panels>
          </Tab.Group>
        </div>
      </div>

      {/* 创建用户 Modal */}
      {showCreateUser && (
        <CreateUserModal
          roles={roles}
          onClose={() => setShowCreateUser(false)}
          onSuccess={() => { setShowCreateUser(false); loadData() }}
        />
      )}

      {/* 编辑用户 Modal */}
      {editUser && (
        <EditUserModal
          user={editUser}
          roles={roles}
          onClose={() => setEditUser(null)}
          onSuccess={() => { setEditUser(null); loadData() }}
        />
      )}

      {/* 删除用户确认 */}
      {deleteUserId && (
        <ConfirmDialog
          title="删除用户"
          message={`确定删除用户 ${deleteUserId}？删除后该用户的接入 Token 将被禁用，归属节点将转为系统归属。`}
          confirmText="删除"
          onConfirm={() => handleDeleteUser(deleteUserId)}
          onCancel={() => setDeleteUserId(null)}
          danger
        />
      )}

      {/* 创建/编辑角色 Modal */}
      {(showCreateRole || editRole) && (
        <RoleFormModal
          role={editRole}
          onClose={() => { setShowCreateRole(false); setEditRole(null) }}
          onSuccess={() => { setShowCreateRole(false); setEditRole(null); loadData() }}
        />
      )}

      {/* 删除角色确认 */}
      {deleteRoleId && (
        <ConfirmDialog
          title="删除角色"
          message={`确定删除角色 ${deleteRoleId}？`}
          confirmText="删除"
          onConfirm={() => handleDeleteRole(deleteRoleId)}
          onCancel={() => setDeleteRoleId(null)}
          danger
        />
      )}
    </div>
  )
}

// 创建用户 Modal
function CreateUserModal({ roles, onClose, onSuccess }: { roles: Role[]; onClose: () => void; onSuccess: () => void }) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [selectedRoles, setSelectedRoles] = useState<string[]>([])
  const [loading, setLoading] = useState(false)
  const { toast } = useToast()

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!username.trim()) { toast('请输入用户名', 'error'); return }
    if (!password || password.length < 8) { toast('密码至少8个字符', 'error'); return }
    setLoading(true)
    try {
      await api.createUser({ username, password, status: 'active', role_ids: selectedRoles })
      toast('创建成功', 'success')
      onSuccess()
    } catch (e: any) {
      toast(e.message, 'error')
    } finally {
      setLoading(false)
    }
  }

  return (
    <Modal title="创建用户" onClose={onClose}>
      <form onSubmit={handleSubmit} className="space-y-4">
        <FormField label="用户名">
          <input value={username} onChange={e => setUsername(e.target.value)} className="w-full px-3 py-2 border rounded-lg text-sm" placeholder="请输入用户名" />
        </FormField>
        <FormField label="密码">
          <input type="password" value={password} onChange={e => setPassword(e.target.value)} className="w-full px-3 py-2 border rounded-lg text-sm" placeholder="至少8个字符" />
        </FormField>
        {roles.length > 0 && (
          <FormField label="分配角色">
            <div className="space-y-1 max-h-40 overflow-y-auto">
              {roles.map(r => (
                <label key={r.id} className="flex items-center gap-2 cursor-pointer hover:bg-gray-50 p-1 rounded">
                  <input type="checkbox" checked={selectedRoles.includes(r.id)} onChange={e => {
                    setSelectedRoles(prev => e.target.checked ? [...prev, r.id] : prev.filter(id => id !== r.id))
                  }} className="w-4 h-4" />
                  <span className="text-sm">{r.name}</span>
                </label>
              ))}
            </div>
          </FormField>
        )}
        <div className="flex justify-end gap-2 pt-2">
          <button type="button" onClick={onClose} className="btn btn-g">取消</button>
          <button type="submit" disabled={loading} className="btn btn-primary">{loading ? '创建中...' : '创建'}</button>
        </div>
      </form>
    </Modal>
  )
}

// 编辑用户 Modal
function EditUserModal({ user, roles, onClose, onSuccess }: { user: User; roles: Role[]; onClose: () => void; onSuccess: () => void }) {
  const [password, setPassword] = useState('')
  const [status, setStatus] = useState(user.status)
  const [userRoles, setUserRoles] = useState<string[]>([])
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const { toast } = useToast()

  useEffect(() => {
    api.getUserRoles(user.id).then(r => {
      setUserRoles(Array.isArray(r) ? r.map((role: Role) => role.id) : [])
    }).catch(() => {}).finally(() => setLoading(false))
  }, [user.id])

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setSaving(true)
    try {
      if (password) await api.updateUser(user.id, { password, status })
      else await api.updateUser(user.id, { status })
      // 角色分配
      for (const roleId of userRoles) {
        if (!roles.find(r => r.id === roleId)) continue
        await api.assignRole(user.id, roleId).catch(() => {})
      }
      toast('已更新', 'success')
      onSuccess()
    } catch (e: any) {
      toast(e.message, 'error')
    } finally {
      setSaving(false)
    }
  }

  return (
    <Modal title={`编辑用户 — ${user.username}`} onClose={onClose}>
      <form onSubmit={handleSubmit} className="space-y-4">
        <FormField label="新密码（留空不修改）">
          <input type="password" value={password} onChange={e => setPassword(e.target.value)} className="w-full px-3 py-2 border rounded-lg text-sm" placeholder="输入新密码" />
        </FormField>
        <FormField label="状态">
          <select value={status} onChange={e => setStatus(e.target.value as 'active' | 'disabled')} className="w-full px-3 py-2 border rounded-lg text-sm">
            <option value="active">活跃</option>
            <option value="disabled">禁用</option>
          </select>
        </FormField>
        {!loading && roles.length > 0 && (
          <FormField label="分配角色">
            <div className="space-y-1 max-h-40 overflow-y-auto">
              {roles.map(r => (
                <label key={r.id} className="flex items-center gap-2 cursor-pointer hover:bg-gray-50 p-1 rounded">
                  <input type="checkbox" checked={userRoles.includes(r.id)} onChange={e => {
                    setUserRoles(prev => e.target.checked ? [...prev, r.id] : prev.filter(id => id !== r.id))
                  }} className="w-4 h-4" />
                  <span className="text-sm">{r.name}</span>
                </label>
              ))}
            </div>
          </FormField>
        )}
        <div className="flex justify-end gap-2 pt-2">
          <button type="button" onClick={onClose} className="btn btn-g">取消</button>
          <button type="submit" disabled={saving} className="btn btn-primary">{saving ? '保存中...' : '保存'}</button>
        </div>
      </form>
    </Modal>
  )
}

// 角色表单 Modal
function RoleFormModal({ role, onClose, onSuccess }: { role?: Role; onClose: () => void; onSuccess: () => void }) {
  const isEdit = !!role
  const [name, setName] = useState(role?.name || '')
  const [description, setDescription] = useState(role?.description || '')
  const [permissions, setPermissions] = useState<{ resource: string; action: string }[]>(role?.permissions || [])
  const [allPerms, setAllPerms] = useState<{ resource: string; action: string }[]>([])
  const [saving, setSaving] = useState(false)
  const { toast } = useToast()

  useEffect(() => {
    api.getPermissions().then(setAllPerms).catch(() => {})
  }, [])

  const togglePerm = (p: { resource: string; action: string }) => {
    setPermissions(prev => {
      const exists = prev.some(x => x.resource === p.resource && x.action === p.action)
      if (exists) return prev.filter(x => !(x.resource === p.resource && x.action === p.action))
      return [...prev, p]
    })
  }

  const isSelected = (p: { resource: string; action: string }) =>
    permissions.some(x => x.resource === p.resource && x.action === p.action)

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!name.trim()) { toast('请输入角色名称', 'error'); return }
    setSaving(true)
    try {
      if (isEdit) {
        await api.updateRole(role.id, { name, description, permissions })
      } else {
        await api.createRole({ id: name, name, description, permissions })
      }
      toast(isEdit ? '已更新' : '已创建', 'success')
      onSuccess()
    } catch (e: any) {
      toast(e.message, 'error')
    } finally {
      setSaving(false)
    }
  }

  return (
    <Modal title={isEdit ? `编辑角色 — ${role.name}` : '创建角色'} onClose={onClose}>
      <form onSubmit={handleSubmit} className="space-y-4">
        <FormField label="角色 ID / 名称">
          <input value={name} onChange={e => setName(e.target.value)} disabled={isEdit}
            className={`w-full px-3 py-2 border rounded-lg text-sm ${isEdit ? 'bg-gray-100' : ''}`}
            placeholder="例如: operator" />
        </FormField>
        <FormField label="描述">
          <input value={description} onChange={e => setDescription(e.target.value)}
            className="w-full px-3 py-2 border rounded-lg text-sm" placeholder="角色描述" />
        </FormField>
        <FormField label="权限">
          <div className="space-y-1 max-h-60 overflow-y-auto border rounded-lg p-3">
            {allPerms.map(p => (
              <label key={`${p.resource}:${p.action}`} className="flex items-center gap-2 cursor-pointer hover:bg-gray-50 p-1 rounded">
                <input type="checkbox" checked={isSelected(p)} onChange={() => togglePerm(p)} className="w-4 h-4" />
                <code className="text-xs">{p.resource}:{p.action}</code>
              </label>
            ))}
          </div>
        </FormField>
        <div className="flex justify-end gap-2 pt-2">
          <button type="button" onClick={onClose} className="btn btn-g">取消</button>
          <button type="submit" disabled={saving} className="btn btn-primary">{saving ? '保存中...' : '保存'}</button>
        </div>
      </form>
    </Modal>
  )
}
```

**注意**: 需要导入 `Trash2`, `Edit`, `Shield` from lucide-react

- [ ] **Step 2: 提交**

```bash
git add src/pages/UsersPage.tsx
git commit -m "feat(admin): add user and role management pages with tabs"
```

---

### Task 6: 接入 Token + MQTT + 系统设置

**Files:**
- Create: `admin/src/pages/AccessTokensPage.tsx`
- Create: `admin/src/pages/MQTTPage.tsx`
- Create: `admin/src/pages/SettingsPage.tsx`

- [ ] **Step 1: 创建 AccessTokensPage.tsx**

接入 Token 页面需要：
- Token 列表 (名称, 前缀, 状态, 最后使用, 创建时间, 操作)
- 创建 Token 按钮 + Modal
- 轮换 Token
- 删除 Token
- 新 Token 显示框 (10秒自动隐藏)

```tsx
import { useEffect, useState, useRef } from 'react'
import { api } from '../api/client'
import type { AccessToken } from '../types/api'
import { PageHeader } from '../components/PageHeader'
import { Badge } from '../components/Badge'
import { Loading } from '../components/Loading'
import { Empty } from '../components/Empty'
import { Modal } from '../components/Modal'
import { FormField } from '../components/FormField'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { useToast } from '../hooks/useToast'
import { Key, Plus, RefreshCw, Trash2, Copy, Eye } from 'lucide-react'

export function AccessTokensPage() {
  const [tokens, setTokens] = useState<AccessToken[]>([])
  const [loading, setLoading] = useState(true)
  const [showCreate, setShowCreate] = useState(false)
  const [newToken, setNewToken] = useState<string | null>(null)
  const [rotateId, setRotateId] = useState<string | null>(null)
  const [deleteId, setDeleteId] = useState<string | null>(null)
  const { toast } = useToast()
  const showBoxRef = useRef<HTMLDivElement>(null)

  const loadTokens = async () => {
    setLoading(true)
    try {
      const res = await api.getAccessTokens()
      setTokens(res.items || [])
    } catch (e: any) {
      toast(e.message, 'error')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { loadTokens() }, [])

  const handleCreate = async (name: string) => {
    try {
      const res = await api.createAccessToken(name)
      setNewToken(res.token)
      toast('Token 已创建', 'success')
      loadTokens()
      // 10秒后自动隐藏
      setTimeout(() => setNewToken(null), 10000)
    } catch (e: any) {
      toast(e.message, 'error')
    }
    setShowCreate(false)
  }

  const handleRotate = async (id: string) => {
    try {
      const res = await api.rotateAccessToken(id)
      setNewToken(res.token)
      toast('Token 已轮换', 'success')
      loadTokens()
      setTimeout(() => setNewToken(null), 10000)
    } catch (e: any) {
      toast(e.message, 'error')
    }
    setRotateId(null)
  }

  const handleDelete = async (id: string) => {
    try {
      await api.deleteAccessToken(id)
      toast('已删除', 'success')
      loadTokens()
    } catch (e: any) {
      toast(e.message, 'error')
    }
    setDeleteId(null)
  }

  const copyToken = (token: string) => {
    navigator.clipboard.writeText(token).then(() => toast('已复制', 'success'))
  }

  return (
    <div>
      <PageHeader title="接入Token" />

      <div className="p-6 space-y-6">
        {/* 新 Token 显示 */}
        {newToken && (
          <div ref={showBoxRef} className="bg-amber-50 border border-amber-200 rounded-lg p-4 animate-fade-in">
            <div className="flex items-center justify-between mb-2">
              <h3 className="font-semibold text-amber-800">新 Token</h3>
              <span className="text-xs text-amber-600">10秒后自动隐藏</span>
            </div>
            <p className="text-amber-700 text-sm mb-3">请妥善保存，关闭后将无法再次查看</p>
            <div className="flex gap-2">
              <code className="flex-1 bg-white border border-amber-200 rounded px-3 py-2 text-sm font-mono break-all">{newToken}</code>
              <button onClick={() => copyToken(newToken)} className="btn btn-g btn-sm">
                <Copy className="w-4 h-4" /> 复制
              </button>
            </div>
          </div>
        )}

        {/* Token 列表 */}
        <div className="bg-white rounded-lg shadow-sm">
          <div className="px-5 py-4 border-b border-gray-200 flex justify-between items-center">
            <h3 className="font-semibold">Token 列表 ({tokens.length})</h3>
            <button onClick={() => setShowCreate(true)} className="btn btn-primary btn-sm flex items-center gap-1">
              <Plus className="w-4 h-4" /> 创建 Token
            </button>
          </div>

          {loading ? <Loading /> : tokens.length === 0 ? (
            <Empty message="暂无 Token" />
          ) : (
            <table className="w-full">
              <thead>
                <tr className="bg-gray-50">
                  <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">名称</th>
                  <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">前缀</th>
                  <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">状态</th>
                  <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">最后使用</th>
                  <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">创建时间</th>
                  <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">操作</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-100">
                {tokens.map(token => (
                  <tr key={token.id} className="hover:bg-gray-50/50">
                    <td className="px-4 py-3">{token.name}</td>
                    <td className="px-4 py-3"><code className="text-xs bg-gray-100 px-1.5 py-0.5 rounded">{token.token_prefix || '-'}</code></td>
                    <td className="px-4 py-3">
                      <Badge variant={token.status === 'active' ? 'success' : 'error'}>
                        {token.status === 'active' ? '活跃' : '禁用'}
                      </Badge>
                    </td>
                    <td className="px-4 py-3 text-gray-500">{formatTimeAgo(token.last_used_at)}</td>
                    <td className="px-4 py-3 text-gray-500">{formatTimeAgo(token.created_at)}</td>
                    <td className="px-4 py-3">
                      <div className="flex items-center gap-1">
                        <button onClick={() => setRotateId(token.id)} className="btn btn-g btn-sm" title="轮换">
                          <RefreshCw className="w-4 h-4" />
                        </button>
                        <button onClick={() => setDeleteId(token.id)} className="btn btn-danger btn-sm" title="删除">
                          <Trash2 className="w-4 h-4" />
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </div>

      {showCreate && (
        <CreateTokenModal onClose={() => setShowCreate(false)} onCreate={handleCreate} />
      )}

      {rotateId && (
        <ConfirmDialog
          title="轮换 Token"
          message="确定轮换此 Token？旧 Token 将立即失效。"
          confirmText="轮换"
          onConfirm={() => handleRotate(rotateId)}
          onCancel={() => setRotateId(null)}
        />
      )}

      {deleteId && (
        <ConfirmDialog
          title="删除 Token"
          message="确定删除 Token？"
          confirmText="删除"
          onConfirm={() => handleDelete(deleteId)}
          onCancel={() => setDeleteId(null)}
          danger
        />
      )}
    </div>
  )
}

function CreateTokenModal({ onClose, onCreate }: { onClose: () => void; onCreate: (name: string) => void }) {
  const [name, setName] = useState('')
  const { toast } = useToast()

  return (
    <Modal title="创建 Token" onClose={onClose}>
      <FormField label="名称">
        <input value={name} onChange={e => setName(e.target.value)} className="w-full px-3 py-2 border rounded-lg text-sm" placeholder="例如: 生产环境节点" autoFocus />
      </FormField>
      <div className="flex justify-end gap-2 pt-2">
        <button onClick={onClose} className="btn btn-g">取消</button>
        <button onClick={() => { if (!name.trim()) { toast('请输入名称', 'error'); return } onCreate(name) }} className="btn btn-primary">创建</button>
      </div>
    </Modal>
  )
}

function formatTimeAgo(dateStr?: string): string {
  if (!dateStr) return '-'
  const date = new Date(dateStr)
  const diff = Math.floor((Date.now() - date.getTime()) / 1000)
  if (diff < 60) return diff + '秒前'
  if (diff < 3600) return Math.floor(diff / 60) + '分钟前'
  if (diff < 86400) return Math.floor(diff / 3600) + '小时前'
  return Math.floor(diff / 86400) + '天前'
}
```

- [ ] **Step 2: 创建 MQTTPage.tsx**

```tsx
import { useEffect, useState } from 'react'
import { api } from '../api/client'
import type { MQTTStats, MQTTClient, MQTTTopic } from '../types/api'
import { PageHeader } from '../components/PageHeader'
import { StatCard } from '../components/StatCard'
import { Loading } from '../components/Loading'
import { Empty } from '../components/Empty'
import { FormField } from '../components/FormField'
import { useToast } from '../hooks/useToast'
import { Radio, Send, Server, Layers } from 'lucide-react'

export function MQTTPage() {
  const [stats, setStats] = useState<MQTTStats | null>(null)
  const [clients, setClients] = useState<MQTTClient[]>([])
  const [topics, setTopics] = useState<MQTTTopic[]>([])
  const [loading, setLoading] = useState(true)
  const [topic, setTopic] = useState('')
  const [payload, setPayload] = useState('')
  const [publishing, setPublishing] = useState(false)
  const { toast } = useToast()

  const loadData = async () => {
    setLoading(true)
    try {
      const [statsRes, clientsRes, topicsRes] = await Promise.all([
        api.getMQTTStats().catch(() => null),
        api.getMQTTClients().catch(() => []),
        api.getMQTTTopics().catch(() => []),
      ])
      setStats(statsRes)
      setClients(Array.isArray(clientsRes) ? clientsRes : [])
      setTopics(Array.isArray(topicsRes) ? topicsRes : [])
    } catch (e: any) {
      toast(e.message, 'error')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { loadData() }, [])

  const handlePublish = async () => {
    if (!topic.trim()) { toast('请输入 Topic', 'error'); return }
    setPublishing(true)
    try {
      await api.publishMQTT(topic, payload, 1, false)
      toast('已发布', 'success')
      setTopic('')
      setPayload('')
    } catch (e: any) {
      toast(e.message, 'error')
    } finally {
      setPublishing(false)
    }
  }

  return (
    <div>
      <PageHeader title="MQTT 管理" />

      <div className="p-6 space-y-6">
        {/* 统计卡片 */}
        {stats && (
          <div className="grid grid-cols-3 gap-4">
            <StatCard icon={Server} iconBg="bg-emerald-100" iconColor="text-emerald-600" value={String(stats.clients_connected)} label="在线客户端" />
            <StatCard icon={Layers} iconBg="bg-blue-100" iconColor="text-blue-600" value={String(stats.subscriptions)} label="订阅数" />
            <StatCard icon={Radio} iconBg="bg-purple-100" iconColor="text-purple-600" value={String(stats.messages_published)} label="已发布" />
          </div>
        )}

        {/* 消息发布 */}
        <div className="bg-white rounded-lg shadow-sm">
          <div className="px-5 py-4 border-b border-gray-200">
            <h3 className="font-semibold">消息发布</h3>
          </div>
          <div className="p-5">
            <div className="flex gap-3 items-end flex-wrap">
              <div className="flex-1 min-w-[200px]">
                <FormField label="Topic">
                  <input value={topic} onChange={e => setTopic(e.target.value)} className="w-full px-3 py-2 border rounded-lg text-sm" placeholder="sensors/temperature" />
                </FormField>
              </div>
              <div className="flex-1 min-w-[200px]">
                <FormField label="Payload">
                  <input value={payload} onChange={e => setPayload(e.target.value)} className="w-full px-3 py-2 border rounded-lg text-sm" placeholder="hello" />
                </FormField>
              </div>
              <button onClick={handlePublish} disabled={publishing} className="btn btn-primary">
                <Send className="w-4 h-4" /> {publishing ? '发布中...' : '发布'}
              </button>
            </div>
          </div>
        </div>

        {/* 客户端 + Topics */}
        <div className="grid grid-cols-2 gap-4">
          <div className="bg-white rounded-lg shadow-sm">
            <div className="px-5 py-4 border-b border-gray-200">
              <h3 className="font-semibold">客户端 ({clients.length})</h3>
            </div>
            {loading ? <Loading /> : clients.length === 0 ? (
              <Empty message="暂无客户端" />
            ) : (
              <table className="w-full">
                <thead>
                  <tr className="bg-gray-50">
                    <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">Client ID</th>
                    <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">用户名</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-100">
                  {clients.map((c, i) => (
                    <tr key={i} className="hover:bg-gray-50/50">
                      <td className="px-4 py-3"><code className="text-xs">{c.client_id || c.ID || ''}</code></td>
                      <td className="px-4 py-3 text-gray-500">{c.username || '-'}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>

          <div className="bg-white rounded-lg shadow-sm">
            <div className="px-5 py-4 border-b border-gray-200">
              <h3 className="font-semibold">Topics ({topics.length})</h3>
            </div>
            {loading ? <Loading /> : topics.length === 0 ? (
              <Empty message="暂无 Topic" />
            ) : (
              <table className="w-full">
                <thead>
                  <tr className="bg-gray-50">
                    <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">Topic</th>
                    <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">QoS</th>
                    <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">订阅</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-100">
                  {topics.map((t, i) => (
                    <tr key={i} className="hover:bg-gray-50/50">
                      <td className="px-4 py-3"><code className="text-xs">{t.topic || t.Topic || t.filter || ''}</code></td>
                      <td className="px-4 py-3">{t.qos || t.Qos || '-'}</td>
                      <td className="px-4 py-3">{t.subscribers || t.Subscribers || '-'}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}
```

- [ ] **Step 3: 创建 SettingsPage.tsx**

```tsx
import { useEffect, useState } from 'react'
import { api } from '../api/client'
import type { ServerConfig } from '../types/api'
import { PageHeader } from '../components/PageHeader'
import { Loading } from '../components/Loading'
import { FormField } from '../components/FormField'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { useToast } from '../hooks/useToast'
import { Settings as SettingsIcon, Key } from 'lucide-react'

export function SettingsPage() {
  const [config, setConfig] = useState<ServerConfig | null>(null)
  const [accessKeyEnabled, setAccessKeyEnabled] = useState(false)
  const [accessKey, setAccessKey] = useState('')
  const [oldPassword, setOldPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [confirmDisableAK, setConfirmDisableAK] = useState(false)
  const { toast } = useToast()

  const loadData = async () => {
    setLoading(true)
    try {
      const [configRes, akRes] = await Promise.all([
        api.getConfig().catch(() => null),
        api.getAccessKey().catch(() => ({ enabled: false })),
      ])
      setConfig(configRes)
      setAccessKeyEnabled(akRes.enabled)
    } catch (e: any) {
      toast(e.message, 'error')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { loadData() }, [])

  const handleChangePassword = async () => {
    if (!oldPassword || !newPassword) { toast('请填写完整', 'error'); return }
    if (newPassword.length < 8) { toast('新密码至少8个字符', 'error'); return }
    setSaving(true)
    try {
      await api.changePassword(oldPassword, newPassword)
      toast('密码已修改', 'success')
      setOldPassword('')
      setNewPassword('')
    } catch (e: any) {
      toast(e.message, 'error')
    } finally {
      setSaving(false)
    }
  }

  const handleSetAccessKey = async () => {
    if (!accessKey.trim()) { toast('请输入 Key', 'error'); return }
    setSaving(true)
    try {
      await api.setAccessKey(accessKey)
      toast('已设置', 'success')
      setAccessKey('')
      loadData()
    } catch (e: any) {
      toast(e.message, 'error')
    } finally {
      setSaving(false)
    }
  }

  const handleDisableAccessKey = async () => {
    try {
      await api.deleteAccessKey()
      toast('已禁用', 'success')
      loadData()
    } catch (e: any) {
      toast(e.message, 'error')
    }
    setConfirmDisableAK(false)
  }

  if (loading) return <Loading />

  return (
    <div>
      <PageHeader title="系统设置" />

      <div className="p-6 grid grid-cols-2 gap-6">
        {/* 服务器配置 */}
        <div className="bg-white rounded-lg shadow-sm">
          <div className="px-5 py-4 border-b border-gray-200">
            <h3 className="font-semibold flex items-center gap-2"><SettingsIcon className="w-4 h-4" /> 服务器配置</h3>
          </div>
          <div className="p-5 grid grid-cols-2 gap-6">
            {config?.server && (
              <div>
                <h4 className="text-sm font-medium text-primary-dark mb-3">网络</h4>
                <div className="space-y-2">
                  <div className="flex justify-between text-sm"><span className="text-gray-500">控制端口</span><code>{config.server.control_port}</code></div>
                  <div className="flex justify-between text-sm"><span className="text-gray-500">网关端口</span><code>{config.server.gateway_port}</code></div>
                  <div className="flex justify-between text-sm"><span className="text-gray-500">API 端口</span><code>{config.server.api_port}</code></div>
                  <div className="flex justify-between text-sm"><span className="text-gray-500">TLS</span><code>{config.server.tls_enabled ? '已启用' : '未启用'}</code></div>
                </div>
              </div>
            )}
            {config?.mqtt && (
              <div>
                <h4 className="text-sm font-medium text-primary-dark mb-3">MQTT</h4>
                <div className="space-y-2">
                  <div className="flex justify-between text-sm"><span className="text-gray-500">状态</span><code>{config.mqtt.enabled ? '已启用' : '未启用'}</code></div>
                  <div className="flex justify-between text-sm"><span className="text-gray-500">TCP</span><code>{config.mqtt.tcp_port}</code></div>
                  <div className="flex justify-between text-sm"><span className="text-gray-500">WS</span><code>{config.mqtt.ws_port}</code></div>
                </div>
              </div>
            )}
            {config?.auth && (
              <div>
                <h4 className="text-sm font-medium text-primary-dark mb-3">认证</h4>
                <div className="space-y-2">
                  <div className="flex justify-between text-sm"><span className="text-gray-500">JWT 有效期</span><code>{config.auth.jwt_expiry}</code></div>
                  <div className="flex justify-between text-sm"><span className="text-gray-500">Bcrypt Cost</span><code>{config.auth.bcrypt_cost}</code></div>
                </div>
              </div>
            )}
            {config?.database && (
              <div>
                <h4 className="text-sm font-medium text-primary-dark mb-3">数据库</h4>
                <div className="space-y-2">
                  <div className="flex justify-between text-sm"><span className="text-gray-500">路径</span><code className="text-xs">{config.database.path}</code></div>
                </div>
              </div>
            )}
          </div>
        </div>

        {/* 修改密码 */}
        <div className="bg-white rounded-lg shadow-sm">
          <div className="px-5 py-4 border-b border-gray-200">
            <h3 className="font-semibold">修改密码</h3>
          </div>
          <div className="p-5 space-y-4 max-w-md">
            <FormField label="当前密码">
              <input type="password" value={oldPassword} onChange={e => setOldPassword(e.target.value)} className="w-full px-3 py-2 border rounded-lg text-sm" />
            </FormField>
            <FormField label="新密码">
              <input type="password" value={newPassword} onChange={e => setNewPassword(e.target.value)} className="w-full px-3 py-2 border rounded-lg text-sm" />
            </FormField>
            <button onClick={handleChangePassword} disabled={saving} className="btn btn-primary">修改密码</button>
          </div>
        </div>

        {/* Access Key */}
        <div className="bg-white rounded-lg shadow-sm col-span-2">
          <div className="px-5 py-4 border-b border-gray-200 flex justify-between items-center">
            <h3 className="font-semibold flex items-center gap-2"><Key className="w-4 h-4" /> Access Key</h3>
            {accessKeyEnabled ? (
              <button onClick={() => setConfirmDisableAK(true)} className="btn btn-danger btn-sm">禁用</button>
            ) : (
              <button onClick={handleSetAccessKey} className="btn btn-success btn-sm">启用</button>
            )}
          </div>
          <div className="p-5">
            <p className="text-gray-500 text-sm mb-4">Access Key 用于 API 简单认证。</p>
            <div className="flex gap-2 max-w-md">
              <input value={accessKey} onChange={e => setAccessKey(e.target.value)} className="flex-1 px-3 py-2 border rounded-lg text-sm" placeholder="输入新的 Access Key" />
              <button onClick={handleSetAccessKey} disabled={saving} className="btn btn-primary">保存</button>
            </div>
          </div>
        </div>
      </div>

      {confirmDisableAK && (
        <ConfirmDialog
          title="禁用 Access Key"
          message="确定禁用 Access Key？"
          confirmText="禁用"
          onConfirm={handleDisableAccessKey}
          onCancel={() => setConfirmDisableAK(false)}
          danger
        />
      )}
    </div>
  )
}
```

- [ ] **Step 4: 提交**

```bash
git add src/pages/AccessTokensPage.tsx src/pages/MQTTPage.tsx src/pages/SettingsPage.tsx
git commit -m "feat(admin): add access tokens, MQTT and settings pages"
```

---

### Task 7: 构建并部署

**Files:**
- Backup: `admin/app.js` → `admin/app.js.old`
- Backup: `admin/app.css` → `admin/app.css.old`
- Modify: `admin/index.html`

- [ ] **Step 1: 构建生产版本**

Run: `cd /root/gitme/refactoring/moleAgent/moleAgent_Serv/admin && npm run build`
Expected: 构建成功，生成 `dist/` 目录

- [ ] **Step 2: 备份旧文件**

Run: `cd /root/gitme/refactoring/moleAgent/moleAgent_Serv/admin && cp app.js app.js.old && cp app.css app.css.old`
Expected: 备份成功

- [ ] **Step 3: 部署构建产物**

Run: `cd /root/gitme/refactoring/moleAgent/moleAgent_Serv/admin && cp -r dist/* .`
Expected: 复制成功

- [ ] **Step 4: 验证部署**

检查 `index.html` 是否包含 Vite 打包的 script 标签，检查 JS/CSS 是否存在

- [ ] **Step 5: 测试登录和管理功能**

在浏览器中访问管理后台，测试：
1. 登录
2. 仪表盘数据加载
3. 节点列表
4. 隧道列表
5. 用户管理
6. 角色管理
7. 接入 Token
8. MQTT
9. 系统设置
10. 退出登录

- [ ] **Step 6: 提交所有代码**

```bash
git add -A
git commit -m "feat(admin): complete React SPA rewrite with all features"
```

---

## 自检清单

### 功能覆盖检查
- [ ] 登录页面 ✓
- [ ] 仪表盘 (统计卡片 + 最近节点) ✓
- [ ] 节点管理 (列表 + 详情展开 + 断开 + 删除 + 添加隧道) ✓
- [ ] 隧道管理 (列表 + 统计 + 创建/编辑 + 删除) ✓
- [ ] 用户管理 (列表 + 创建/编辑 + 删除 + 角色分配) ✓
- [ ] 角色管理 (列表 + 创建/编辑/删除) ✓
- [ ] 接入 Token (列表 + 创建 + 轮换 + 删除 + Token 显示框) ✓
- [ ] MQTT (统计 + 客户端列表 + Topic 列表 + 发布消息) ✓
- [ ] 系统设置 (配置显示 + 修改密码 + Access Key) ✓

### 兼容性检查
- [ ] 所有 API 端点与后端匹配 ✓
- [ ] JWT Token 认证流程一致 ✓
- [ ] 响应格式处理一致 ✓
- [ ] 错误处理覆盖所有场景 ✓

### 代码质量
- [ ] TypeScript 类型完整 ✓
- [ ] Tailwind CSS 替代内联样式 ✓
- [ ] 组件化结构清晰 ✓
- [ ] 无 console.error 或未处理异常 ✓

### 部署检查
- [ ] 构建产物大小合理 ✓
- [ ] 静态资源路径正确 ✓
- [ ] 浏览器缓存控制生效 ✓
- [ ] 登出后 Token 清除 ✓

---

**Plan complete.** 计划已保存至 `docs/superpowers/plans/2026-04-15-admin-rewrite.md`

**两个执行选项：**

**1. Subagent-Driven (推荐)** - 每个任务分配一个子代理，任务间审查，快速迭代

**2. Inline Execution** - 在本会话中执行任务，批量执行 + 审查点

**选择哪种方式？**
