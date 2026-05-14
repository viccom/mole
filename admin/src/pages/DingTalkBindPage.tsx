import { useState, FormEvent } from 'react'
import { useNavigate, useLocation } from 'react-router-dom'
import { useAuth } from '../hooks/useAuth'
import { useToast } from '../hooks/useToast'

export function DingTalkBindPage() {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [loading, setLoading] = useState(false)
  const { dingtalkBind } = useAuth()
  const { toast } = useToast()
  const navigate = useNavigate()
  const location = useLocation()
  const state = location.state as { dingtalkToken?: string; dingtalkName?: string } | null

  if (!state?.dingtalkToken) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-gradient-to-br from-[#1E1B4B] to-[#4F46E5]">
        <div className="bg-white rounded-xl p-10 w-[400px] max-w-[90vw] shadow-2xl text-center">
          <h2 className="text-xl font-bold text-gray-800 mb-2">无效请求</h2>
          <p className="text-gray-500 mb-4">缺少钉钉授权信息，请重新从钉钉进入</p>
          <button onClick={() => navigate('/login')} className="text-indigo-600 hover:underline text-sm">返回登录</button>
        </div>
      </div>
    )
  }

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault()
    if (!username || !password) { toast('请输入用户名和密码', 'error'); return }
    setLoading(true)
    try {
      await dingtalkBind(state.dingtalkToken!, username, password)
      toast('绑定成功', 'success')
      navigate('/dashboard')
    } catch (err: unknown) {
      toast(err instanceof Error ? err.message : '绑定失败', 'error')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-gradient-to-br from-[#1E1B4B] to-[#4F46E5]">
      <div className="bg-white rounded-xl p-10 w-[400px] max-w-[90vw] shadow-2xl">
        <div className="text-center mb-6">
          <div className="inline-flex items-center justify-center w-12 h-12 rounded-xl bg-blue-50 text-blue-600 mb-4">
            <svg viewBox="0 0 24 24" fill="currentColor" className="w-8 h-8">
              <path d="M12 2C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2zm-1 15h-2v-2h2v2zm0-4h-2V7h2v6zm4 4h-2v-2h2v2zm0-4h-2V7h2v6z"/>
            </svg>
          </div>
          <h1 className="text-2xl font-bold text-[#3730A3]">绑定账户</h1>
          <p className="text-gray-500 mt-1">首次使用钉钉登录，请绑定已有账户</p>
          {state.dingtalkName && (
            <p className="text-sm text-blue-600 mt-2">钉钉用户：{state.dingtalkName}</p>
          )}
        </div>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1.5">用户名</label>
            <input type="text" value={username} onChange={e => setUsername(e.target.value)}
              className="w-full px-3 py-2.5 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500/20 focus:border-indigo-500"
              placeholder="请输入 MoleAgent 用户名" autoComplete="username" autoFocus />
          </div>
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1.5">密码</label>
            <input type="password" value={password} onChange={e => setPassword(e.target.value)}
              className="w-full px-3 py-2.5 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500/20 focus:border-indigo-500"
              placeholder="请输入密码" autoComplete="current-password" />
          </div>
          <button type="submit" disabled={loading}
            className="w-full py-2.5 bg-blue-600 text-white rounded-lg font-medium text-sm hover:bg-blue-700 transition-colors disabled:opacity-50 disabled:cursor-not-allowed">
            {loading ? '绑定中...' : '绑定账户'}
          </button>
        </form>
        <p className="text-center text-xs text-gray-400 mt-4">绑定后可使用钉钉一键登录</p>
      </div>
    </div>
  )
}
