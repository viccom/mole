import { useState, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAuth } from '../hooks/useAuth'
import { isFeishuEnv, requestAuthCode } from '../lib/feishu'
import { api } from '../api/client'

export function LoginPage() {
  const [loading, setLoading] = useState(false)
  const { feishuLogin } = useAuth()
  const navigate = useNavigate()

  useEffect(() => {
    const init = async () => {
      try {
        const config = await api.feishuConfig()
        if (!config.app_id) return
        if (isFeishuEnv()) {
          handleSSO(config.app_id)
        }
      } catch { /* feishu not configured */ }
    }
    init()
  }, [])

  const handleSSO = async (appId: string) => {
    setLoading(true)
    try {
      const code = await requestAuthCode(appId)
      const result = await feishuLogin(code)
      if (result.needBind) {
        navigate('/feishu-bind', { state: { feishuToken: result.feishuToken, feishuName: result.feishuName } })
      } else {
        navigate('/nodes')
      }
    } catch (err) {
      alert(err instanceof Error ? err.message : '登录失败')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="min-h-screen bg-gradient-to-br from-indigo-900 to-blue-600 flex items-center justify-center p-6">
      <div className="bg-white rounded-2xl p-8 w-full max-w-sm shadow-xl text-center">
        <div className="w-14 h-14 mx-auto mb-4 rounded-2xl bg-indigo-50 flex items-center justify-center">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className="w-8 h-8 text-indigo-600">
            <circle cx="12" cy="12" r="3"/><path d="M12 1v4M12 19v4M4.22 4.22l2.83 2.83M16.95 16.95l2.83 2.83M1 12h4M19 12h4M4.22 19.78l2.83-2.83M16.95 7.05l2.83-2.83"/>
          </svg>
        </div>
        <h1 className="text-2xl font-bold text-indigo-900">MoleAgent</h1>
        <p className="text-gray-400 mt-1 mb-6 text-sm">请从飞书工作台打开</p>
        {loading ? (
          <div className="text-gray-500 text-sm">飞书登录中...</div>
        ) : (
          <div className="text-xs text-gray-300">非飞书环境请使用 PC 端登录</div>
        )}
      </div>
    </div>
  )
}
