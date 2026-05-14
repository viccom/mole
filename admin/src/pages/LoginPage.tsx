import { useState, useEffect, FormEvent } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAuth } from '../hooks/useAuth'
import { useToast } from '../hooks/useToast'
import { isFeishuEnv, requestAuthCode, buildOAuth2URL } from '../lib/feishu'
import { isDingTalkEnv, requestAuthCode as dingtalkRequestAuthCode, buildDingTalkOAuth2URL } from '../lib/dingtalk'
import { api } from '../api/client'

export function LoginPage() {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [loading, setLoading] = useState(false)
  const [feishuAvailable, setFeishuAvailable] = useState(false)
  const [feishuAppId, setFeishuAppId] = useState('')
  const [dingtalkAvailable, setDingtalkAvailable] = useState(false)
  const [dingtalkCorpId, setDingtalkCorpId] = useState('')
  const [dingtalkAppKey, setDingtalkAppKey] = useState('')
  const { login, feishuLogin, dingtalkLogin } = useAuth()
  const { toast } = useToast()
  const navigate = useNavigate()

  useEffect(() => {
    const init = async () => {
      const [feishuCfg, dingtalkCfg] = await Promise.all([
        api.feishuConfig().catch(() => ({ app_id: '' })),
        api.dingtalkConfig().catch(() => ({ corp_id: '', app_key: '' })),
      ])
      if (feishuCfg.app_id) {
        setFeishuAvailable(true)
        setFeishuAppId(feishuCfg.app_id)
        if (isFeishuEnv()) {
          handleFeishuSSO(feishuCfg.app_id)
        }
      }
      if (dingtalkCfg.corp_id || dingtalkCfg.app_key) {
        setDingtalkAvailable(true)
        setDingtalkCorpId(dingtalkCfg.corp_id)
        setDingtalkAppKey(dingtalkCfg.app_key)
        if (isDingTalkEnv() && dingtalkCfg.corp_id) {
          handleDingTalkSSO(dingtalkCfg.corp_id, dingtalkCfg.app_key)
        }
      }
    }
    init()
  }, [])

  const handleFeishuSSO = async (appId: string) => {
    setLoading(true)
    try {
      const code = await requestAuthCode(appId)
      const result = await feishuLogin(code)
      if (result.needBind) {
        navigate('/feishu-bind', { state: { feishuToken: result.feishuToken, feishuName: result.feishuName } })
      } else {
        toast('登录成功', 'success')
        navigate('/dashboard')
      }
    } catch (err: unknown) {
      toast(err instanceof Error ? err.message : '飞书登录失败', 'error')
    } finally {
      setLoading(false)
    }
  }

  const handleFeishuClick = async () => {
    if (!feishuAppId) { toast('飞书未配置', 'error'); return }
    if (isFeishuEnv()) {
      handleFeishuSSO(feishuAppId)
    } else {
      window.location.href = buildOAuth2URL(feishuAppId)
    }
  }

  const handleDingTalkSSO = async (corpId: string, appKey: string) => {
    setLoading(true)
    try {
      const code = await dingtalkRequestAuthCode(corpId)
      const result = await dingtalkLogin(code, 'h5')
      if (result.needBind) {
        navigate('/dingtalk-bind', { state: { dingtalkToken: result.dingtalkToken, dingtalkName: result.dingtalkName } })
      } else {
        toast('登录成功', 'success')
        navigate('/dashboard')
      }
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : ''
      // 非钉钉原生环境中 JSAPI 不可用时，回退到 OAuth2 扫码登录
      if (!isDingTalkEnv() && msg.includes('unavailable') && appKey) {
        window.location.href = buildDingTalkOAuth2URL(appKey)
        return
      }
      toast(msg || '钉钉登录失败', 'error')
    } finally {
      setLoading(false)
    }
  }

  const handleDingTalkClick = async () => {
    if (!dingtalkCorpId && !dingtalkAppKey) { toast('钉钉未配置', 'error'); return }
    if (isDingTalkEnv() && dingtalkCorpId) {
      handleDingTalkSSO(dingtalkCorpId, dingtalkAppKey)
    } else {
      window.location.href = buildDingTalkOAuth2URL(dingtalkAppKey)
    }
  }

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault()
    if (!username || !password) { toast('请输入用户名和密码', 'error'); return }
    setLoading(true)
    try {
      await login(username, password)
      toast('登录成功', 'success')
      navigate('/dashboard')
    } catch (err: unknown) {
      toast(err instanceof Error ? err.message : '登录失败', 'error')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-gradient-to-br from-[#1E1B4B] to-[#4F46E5]">
      <div className="bg-white rounded-xl p-10 w-[400px] max-w-[90vw] shadow-2xl">
        <div className="text-center mb-6">
          <div className="inline-flex items-center justify-center w-12 h-12 rounded-xl bg-indigo-50 text-indigo-600 mb-4">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className="w-8 h-8">
              <circle cx="12" cy="12" r="3"/><path d="M12 1v4M12 19v4M4.22 4.22l2.83 2.83M16.95 16.95l2.83 2.83M1 12h4M19 12h4M4.22 19.78l2.83-2.83M16.95 7.05l2.83-2.83"/>
            </svg>
          </div>
          <h1 className="text-2xl font-bold text-[#3730A3]">MoleAgent</h1>
          <p className="text-gray-500 mt-1">管理后台登录</p>
        </div>
        {feishuAvailable && (
          <>
            <button onClick={handleFeishuClick} disabled={loading}
              className="w-full py-2.5 mb-4 bg-[#3370FF] text-white rounded-lg font-medium text-sm hover:bg-[#245BDB] transition-colors disabled:opacity-50 disabled:cursor-not-allowed flex items-center justify-center gap-2">
              <svg viewBox="0 0 24 24" fill="currentColor" className="w-5 h-5">
                <path d="M12 2C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2zm-1 15h-2v-2h2v2zm0-4h-2V7h2v6zm4 4h-2v-2h2v2zm0-4h-2V7h2v6z"/>
              </svg>
              {loading ? '飞书登录中...' : isFeishuEnv() ? '飞书登录' : '飞书扫码登录'}
            </button>
          </>
        )}
        {dingtalkAvailable && (
          <>
            <button onClick={handleDingTalkClick} disabled={loading}
              className="w-full py-2.5 mb-4 bg-blue-600 text-white rounded-lg font-medium text-sm hover:bg-blue-700 transition-colors disabled:opacity-50 disabled:cursor-not-allowed flex items-center justify-center gap-2">
              <svg viewBox="0 0 24 24" fill="currentColor" className="w-5 h-5">
                <path d="M12 2C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2zm-1 15h-2v-2h2v2zm0-4h-2V7h2v6zm4 4h-2v-2h2v2zm0-4h-2V7h2v6z"/>
              </svg>
              {loading ? '钉钉登录中...' : isDingTalkEnv() ? '钉钉登录' : '钉钉扫码登录'}
            </button>
          </>
        )}
        {(feishuAvailable || dingtalkAvailable) && (
          <div className="relative my-4">
            <div className="absolute inset-0 flex items-center"><div className="w-full border-t border-gray-200" /></div>
            <div className="relative flex justify-center"><span className="bg-white px-3 text-xs text-gray-400">或</span></div>
          </div>
        )}
        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1.5">用户名</label>
            <input type="text" value={username} onChange={e => setUsername(e.target.value)}
              className="w-full px-3 py-2.5 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500/20 focus:border-indigo-500"
              placeholder="请输入用户名" autoComplete="username" autoFocus />
          </div>
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1.5">密码</label>
            <input type="password" value={password} onChange={e => setPassword(e.target.value)}
              className="w-full px-3 py-2.5 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500/20 focus:border-indigo-500"
              placeholder="请输入密码" autoComplete="current-password" />
          </div>
          <button type="submit" disabled={loading}
            className="w-full py-2.5 bg-[#4F46E5] text-white rounded-lg font-medium text-sm hover:bg-[#3730A3] transition-colors disabled:opacity-50 disabled:cursor-not-allowed">
            {loading ? '登录中...' : '登 录'}
          </button>
        </form>
      </div>
    </div>
  )
}
