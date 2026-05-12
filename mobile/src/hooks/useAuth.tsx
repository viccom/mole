import { createContext, useContext, useState, useEffect, useCallback, useMemo, type ReactNode } from 'react'
import { api, setToken, getToken } from '../api/client'

interface AuthUser { id: string; username: string; token: string }

interface AuthContextType {
  user: AuthUser | null
  loading: boolean
  login: (username: string, password: string) => Promise<void>
  feishuLogin: (code: string) => Promise<{ needBind: boolean; feishuToken?: string; feishuName?: string }>
  feishuBind: (feishuToken: string, username: string, password: string) => Promise<void>
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

  const login = useCallback(async (username: string, password: string) => {
    const res = await api.login(username, password)
    const u = { id: res.user.id, username: res.user.username, token: res.token }
    setToken(res.token); setUser(u)
  }, [])

  const feishuLogin = useCallback(async (code: string) => {
    const res = await api.feishuCallback(code)
    if (res.need_bind) return { needBind: true as const, feishuToken: res.feishu_token, feishuName: res.feishu_name }
    const u = { id: res.user!.id, username: res.user!.username, token: res.token! }
    setToken(res.token!); setUser(u)
    return { needBind: false as const }
  }, [])

  const feishuBind = useCallback(async (feishuToken: string, username: string, password: string) => {
    const res = await api.feishuBind(feishuToken, username, password)
    const u = { id: res.user.id, username: res.user.username, token: res.token }
    setToken(res.token); setUser(u)
  }, [])

  const logout = useCallback(() => { setToken(null); setUser(null) }, [])
  const value = useMemo(() => ({ user, loading, login, feishuLogin, feishuBind, logout }), [user, loading, login, feishuLogin, feishuBind, logout])
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuth() {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth must be used within AuthProvider')
  return ctx
}
