import { useState, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAuth } from '../hooks/useAuth'
import { isFeishuEnv, requestAuthCode } from '../lib/feishu'
import { isDingTalkEnv, requestAuthCode as dingtalkRequestAuthCode } from '../lib/dingtalk'
import { api } from '../api/client'

export function LoginPage() {
  const [loading, setLoading] = useState(false)
  const [ssoType, setSsoType] = useState<'feishu' | 'dingtalk' | null>(null)
  const [ssoFailed, setSsoFailed] = useState(false)
  const { feishuLogin, dingtalkLogin } = useAuth()
  const navigate = useNavigate()

  const init = async () => {
    const [feishuCfg, dingtalkCfg] = await Promise.all([
      api.feishuConfig().catch(() => ({ app_id: '' })),
      api.dingtalkConfig().catch(() => ({ corp_id: '', app_key: '' })),
    ])
    // SSO 防重入：自动发起只在未尝试过时进行，避免 alert 弹窗循环。
    // 命中守卫说明本次会话已尝试过：置 ssoFailed 让"重新登录"按钮可见，
    // 否则刷新后 ssoFailed 随组件重建丢失 → 页面无任何可点入口（死界面）
    if (sessionStorage.getItem('sso_attempted')) {
      setSsoFailed(true)
      return
    }
    if (feishuCfg.app_id && isFeishuEnv()) {
      sessionStorage.setItem('sso_attempted', '1')
      handleFeishuSSO(feishuCfg.app_id)
    }
    if (dingtalkCfg.corp_id && isDingTalkEnv()) {
      sessionStorage.setItem('sso_attempted', '1')
      handleDingTalkSSO(dingtalkCfg.corp_id)
    }
  }

  useEffect(() => { init() }, [])

  const handleFeishuSSO = async (appId: string) => {
    setSsoType('feishu')
    setLoading(true)
    try {
      const code = await requestAuthCode(appId)
      const result = await feishuLogin(code)
      sessionStorage.removeItem('sso_attempted')
      if (result.needBind) {
        navigate('/feishu-bind', { state: { feishuToken: result.feishuToken, feishuName: result.feishuName } })
      } else {
        navigate('/nodes')
      }
    } catch (err) {
      // 失败置 ssoFailed：守卫拦自动重试，但用户可通过"重新登录"按钮主动再发起
      setSsoFailed(true)
      alert(err instanceof Error ? err.message : '登录失败')
    } finally {
      setLoading(false)
    }
  }

  const handleDingTalkSSO = async (corpId: string) => {
    setSsoType('dingtalk')
    setLoading(true)
    try {
      const code = await dingtalkRequestAuthCode(corpId)
      const result = await dingtalkLogin(code, 'h5')
      sessionStorage.removeItem('sso_attempted')
      if (result.needBind) {
        navigate('/dingtalk-bind', { state: { dingtalkToken: result.dingtalkToken, dingtalkName: result.dingtalkName } })
      } else {
        navigate('/nodes')
      }
    } catch (err) {
      setSsoFailed(true)
      alert(err instanceof Error ? err.message : '登录失败')
    } finally {
      setLoading(false)
    }
  }

  // 手动重试：清除守卫标记后重新发起（仅用户点击触发，不破坏防重入）
  const handleRetry = () => {
    sessionStorage.removeItem('sso_attempted')
    setSsoFailed(false)
    init()
  }

  const loginLabel = loading
    ? (ssoType === 'dingtalk' ? '钉钉登录中...' : '飞书登录中...')
    : '请从飞书或钉钉工作台打开'

  return (
    <div className="min-h-screen bg-gradient-to-br from-indigo-900 to-blue-600 flex items-center justify-center p-6">
      <div className="bg-white rounded-2xl p-8 w-full max-w-sm shadow-xl text-center">
        <div className="w-14 h-14 mx-auto mb-4 rounded-2xl bg-indigo-50 flex items-center justify-center">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className="w-8 h-8 text-indigo-600">
            <circle cx="12" cy="12" r="3"/><path d="M12 1v4M12 19v4M4.22 4.22l2.83 2.83M16.95 16.95l2.83 2.83M1 12h4M19 12h4M4.22 19.78l2.83-2.83M16.95 7.05l2.83-2.83"/>
          </svg>
        </div>
        <h1 className="text-2xl font-bold text-indigo-900">MoleAgent</h1>
        <p className="text-gray-400 mt-1 mb-6 text-sm">{loading ? loginLabel : '请从飞书或钉钉工作台打开'}</p>
        {ssoFailed && !loading && (
          <button onClick={handleRetry}
            className="w-full py-3 bg-indigo-600 text-white rounded-xl text-base font-medium active:bg-indigo-700">
            重新登录
          </button>
        )}
      </div>
    </div>
  )
}
