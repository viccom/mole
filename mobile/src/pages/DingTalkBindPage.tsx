import { useState, type FormEvent } from 'react'
import { useNavigate, useLocation } from 'react-router-dom'
import { useAuth } from '../hooks/useAuth'

export function DingTalkBindPage() {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [loading, setLoading] = useState(false)
  const { dingtalkBind } = useAuth()
  const navigate = useNavigate()
  const location = useLocation()
  const state = location.state as { dingtalkToken?: string; dingtalkName?: string } | null

  if (!state?.dingtalkToken) {
    return (
      <div className="min-h-screen bg-gray-50 flex items-center justify-center p-6">
        <div className="text-center">
          <p className="text-gray-500">缺少授权信息</p>
          <button onClick={() => navigate('/')} className="text-blue-600 mt-2 text-sm">返回</button>
        </div>
      </div>
    )
  }

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault()
    setLoading(true)
    try {
      await dingtalkBind(state.dingtalkToken!, username, password)
      navigate('/nodes')
    } catch (err) {
      alert(err instanceof Error ? err.message : '绑定失败')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="min-h-screen bg-gray-50 flex items-center justify-center p-6">
      <div className="bg-white rounded-2xl p-8 w-full max-w-sm shadow-sm border border-gray-100">
        <div className="text-center mb-6">
          <h1 className="text-xl font-bold text-gray-800">绑定账户</h1>
          <p className="text-gray-400 text-sm mt-1">首次钉钉登录，请绑定已有账户</p>
          {state.dingtalkName && <p className="text-blue-600 text-sm mt-2">{state.dingtalkName}</p>}
        </div>
        <form onSubmit={handleSubmit} className="space-y-4">
          <input type="text" value={username} onChange={e => setUsername(e.target.value)}
            className="w-full px-3 py-2.5 border border-gray-200 rounded-lg text-sm focus:outline-none focus:border-blue-500"
            placeholder="用户名" autoFocus />
          <input type="password" value={password} onChange={e => setPassword(e.target.value)}
            className="w-full px-3 py-2.5 border border-gray-200 rounded-lg text-sm focus:outline-none focus:border-blue-500"
            placeholder="密码" />
          <button type="submit" disabled={loading}
            className="w-full py-2.5 bg-blue-600 text-white rounded-lg text-sm font-medium disabled:opacity-50">
            {loading ? '绑定中...' : '绑定'}
          </button>
        </form>
      </div>
    </div>
  )
}
