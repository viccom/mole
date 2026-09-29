import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAuth } from '../hooks/useAuth'
import { api } from '../api/client'

export function FeishuCallbackPage() {
  const [error, setError] = useState('')
  const { feishuLogin } = useAuth()
  const navigate = useNavigate()

  useEffect(() => {
    const params = new URLSearchParams(window.location.search)
    const code = params.get('code')
    if (!code) {
      setError('授权失败：未收到授权码')
      return
    }

    feishuLogin(code).then(result => {
      if (result.needBind) {
        navigate('/feishu-bind', { state: { feishuToken: result.feishuToken, feishuName: result.feishuName } })
      } else {
        navigate('/dashboard', { replace: true })
      }
    }).catch(err => {
      setError(err instanceof Error ? err.message : '飞书登录失败')
    })
  }, [])

  return (
    <div className="min-h-screen flex items-center justify-center bg-gradient-to-br from-[#1E1B4B] to-[#4F46E5]">
      <div className="bg-white rounded-xl p-10 w-[400px] max-w-[90vw] shadow-2xl text-center">
        {error ? (
          <>
            <div className="text-red-500 text-lg mb-4">登录失败</div>
            <div className="text-gray-500 text-sm mb-6">{error}</div>
            <button onClick={() => navigate('/login')}
              className="px-6 py-2 bg-[#4F46E5] text-white rounded-lg text-sm hover:bg-[#3730A3]">
              返回登录
            </button>
          </>
        ) : (
          <>
            <div className="animate-spin w-8 h-8 border-2 border-indigo-600 border-t-transparent rounded-full mx-auto mb-4" />
            <div className="text-gray-600">飞书登录中...</div>
          </>
        )}
      </div>
    </div>
  )
}
