import { createContext, useContext, useState, useEffect, useCallback, useMemo, type ReactNode } from 'react'
import { api, setToken, getToken } from '../api/client'

interface AuthUser { id: string; username: string; token: string }

interface AuthContextType {
  user: AuthUser | null
  loading: boolean
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
      api.me().then(u => setUser({ id: u.id, username: u.username, token: tk }))
        .catch(() => setToken(null)).finally(() => setLoading(false))
    } else { setLoading(false) }
  }, [])

  const feishuLogin = useCallback(async (code: string) => {
    const res = await api.feishuCallback(code)
    if (res.need_bind) return { needBind: true as const, feishuToken: res.feishu_token, feishuName: res.feishu_name }
    // 显式判空替代非空断言：后端契约异常时给出可读错误而不是带着 undefined 往下走
    if (!res.user || !res.token) throw new Error('登录响应异常，请重试')
    const u = { id: res.user.id, username: res.user.username, token: res.token }
    setToken(res.token); setUser(u)
    return { needBind: false as const }
  }, [])

  const feishuBind = useCallback(async (feishuToken: string, username: string, password: string) => {
    const res = await api.feishuBind(feishuToken, username, password)
    const u = { id: res.user.id, username: res.user.username, token: res.token }
    setToken(res.token); setUser(u)
  }, [])

  const dingtalkLogin = useCallback(async (code: string, source: string) => {
    const res = await api.dingtalkCallback(code, source)
    if (res.need_bind) return { needBind: true as const, dingtalkToken: res.dingtalk_token, dingtalkName: res.dingtalk_name }
    if (!res.user || !res.token) throw new Error('登录响应异常，请重试')
    const u = { id: res.user.id, username: res.user.username, token: res.token }
    setToken(res.token); setUser(u)
    return { needBind: false as const }
  }, [])

  const dingtalkBind = useCallback(async (dingtalkToken: string, username: string, password: string) => {
    const res = await api.dingtalkBind(dingtalkToken, username, password)
    const u = { id: res.user.id, username: res.user.username, token: res.token }
    setToken(res.token); setUser(u)
  }, [])

  const logout = useCallback(() => { setToken(null); setUser(null) }, [])
  const value = useMemo(() => ({ user, loading, feishuLogin, feishuBind, dingtalkLogin, dingtalkBind, logout }), [user, loading, feishuLogin, feishuBind, dingtalkLogin, dingtalkBind, logout])
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuth() {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth must be used within AuthProvider')
  return ctx
}
