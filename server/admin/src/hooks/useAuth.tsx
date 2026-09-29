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
}

const AuthContext = createContext<AuthContextType | null>(null)

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<AuthUser | null>(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    const tk = getToken()
    if (tk) {
      api.me()
        .then(u => setUser({ id: u.id, username: u.username, token: tk }))
        .catch(() => setToken(null))
        .finally(() => setLoading(false))
    } else {
      setLoading(false)
    }
  }, [])

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

  const value = useMemo(() => ({ user, loading, login, feishuLogin, feishuBind, dingtalkLogin, dingtalkBind, logout }), [user, loading, login, feishuLogin, feishuBind, dingtalkLogin, dingtalkBind, logout])

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
