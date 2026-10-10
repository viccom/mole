import { createContext, useContext, useState, useEffect, useCallback, useMemo, type ReactNode } from 'react'
import { api, setToken, getToken } from '../api/client'
import type { AuthUser } from '../types/api'

interface AuthContextType {
  user: AuthUser | null
  loading: boolean
  login: (username: string, password: string) => Promise<void>
  feishuLogin: (code: string) => Promise<{ needBind: boolean; feishuToken?: string; feishuName?: string }>
  feishuBind: (feishuToken: string, username: string, password: string) => Promise<void>
  dingtalkLogin: (code: string, source: string) => Promise<{ needBind: boolean; dingtalkToken?: string; dingtalkName?: string }>
  dingtalkBind: (dingtalkToken: string, username: string, password: string) => Promise<void>
  logout: () => void
  // 入口级权限门控用；未登录/权限未就绪返回 false，仅影响入口隐藏，不影响主流程渲染
  hasPermission: (resource: string, action: string) => boolean
}

const AuthContext = createContext<AuthContextType | null>(null)

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<AuthUser | null>(null)
  const [loading, setLoading] = useState(true)
  // 用户权限并集（"resource:action" 集合）。null=尚未加载；空集=角色接口失败降级
  const [permissions, setPermissions] = useState<Set<string> | null>(null)

  // 拉取角色定义并计算权限并集（支持 * 通配）。失败时降级为无额外权限（只显示
  // 通用入口），不能因此阻断登录或白屏。/auth/me 的 roles 是角色名（JWT 内为
  // 角色名），与角色定义按 name 或 id 双匹配以兼容两种形态
  const loadPermissions = useCallback(async (roleRefs: string[]) => {
    try {
      const roleDefs = await api.getRoles()
      const perms = new Set<string>()
      for (const role of roleDefs) {
        if (!roleRefs.includes(role.name) && !roleRefs.includes(role.id)) continue
        for (const p of role.permissions || []) perms.add(`${p.resource}:${p.action}`)
      }
      setPermissions(perms)
    } catch {
      setPermissions(new Set())
    }
  }, [])

  useEffect(() => {
    const tk = getToken()
    if (tk) {
      api.me()
        .then(u => setUser({ id: u.id, username: u.username, token: tk, roles: u.roles || [] }))
        .catch(() => setToken(null))
        .finally(() => setLoading(false))
    } else {
      setLoading(false)
    }
  }, [])

  // roles 统一从 /auth/me 补齐：登录与 SSO 绑定的响应不含 roles，缺失时先取再算权限
  useEffect(() => {
    if (!user) {
      setPermissions(null)
      return
    }
    if (user.roles) {
      void loadPermissions(user.roles)
      return
    }
    let alive = true
    api.me()
      .then(me => { if (alive) setUser(u => (u ? { ...u, roles: me.roles || [] } : u)) })
      .catch(() => { if (alive) setUser(u => (u ? { ...u, roles: [] } : u)) })
    return () => { alive = false }
  }, [user, loadPermissions])

  const hasPermission = useCallback((resource: string, action: string): boolean => {
    if (!user || !permissions) return false
    return (
      permissions.has('*:*') ||
      permissions.has(`${resource}:*`) ||
      permissions.has(`*:${action}`) ||
      permissions.has(`${resource}:${action}`)
    )
  }, [user, permissions])

  const login = useCallback(async (username: string, password: string) => {
    const res = await api.login(username, password)
    const authUser = { id: res.user.id, username: res.user.username, token: res.token }
    setToken(res.token)
    setUser(authUser)
  }, [])

  const feishuLogin = useCallback(async (code: string) => {
    const res = await api.feishuCallback(code)
    if (res.need_bind) {
      return { needBind: true as const, feishuToken: res.feishu_token, feishuName: res.feishu_name }
    }
    const authUser = { id: res.user!.id, username: res.user!.username, token: res.token! }
    setToken(res.token!)
    setUser(authUser)
    return { needBind: false as const }
  }, [])

  const feishuBind = useCallback(async (feishuToken: string, username: string, password: string) => {
    const res = await api.feishuBind(feishuToken, username, password)
    const authUser = { id: res.user.id, username: res.user.username, token: res.token }
    setToken(res.token)
    setUser(authUser)
  }, [])

  const dingtalkLogin = useCallback(async (code: string, source: string) => {
    const res = await api.dingtalkCallback(code, source)
    if (res.need_bind) {
      return { needBind: true as const, dingtalkToken: res.dingtalk_token, dingtalkName: res.dingtalk_name }
    }
    const authUser = { id: res.user!.id, username: res.user!.username, token: res.token! }
    setToken(res.token!)
    setUser(authUser)
    return { needBind: false as const }
  }, [])

  const dingtalkBind = useCallback(async (dingtalkToken: string, username: string, password: string) => {
    const res = await api.dingtalkBind(dingtalkToken, username, password)
    const authUser = { id: res.user.id, username: res.user.username, token: res.token }
    setToken(res.token)
    setUser(authUser)
  }, [])

  const logout = useCallback(() => {
    api.logout().catch(() => {})
    setToken(null)
    setUser(null)
  }, [])

  const value = useMemo(() => ({ user, loading, login, feishuLogin, feishuBind, dingtalkLogin, dingtalkBind, logout, hasPermission }), [user, loading, login, feishuLogin, feishuBind, dingtalkLogin, dingtalkBind, logout, hasPermission])

  return (
    <AuthContext.Provider value={value}>
      {children}
    </AuthContext.Provider>
  )
}

export function useAuth() {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth must be used within AuthProvider')
  return ctx
}
