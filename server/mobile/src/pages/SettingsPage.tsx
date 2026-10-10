import { useState, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAuth } from '../hooks/useAuth'
import { api } from '../api/client'
import { isFeishuEnv } from '../lib/feishu'
import { isDingTalkEnv } from '../lib/dingtalk'
import { LogOut, Link2, Info } from 'lucide-react'

export function SettingsPage() {
  const { user, logout } = useAuth()
  const [binding, setBinding] = useState<{ bound: boolean; feishu_name?: string } | null>(null)
  const [dingBinding, setDingBinding] = useState<{ bound: boolean; ding_name?: string } | null>(null)
  const [version, setVersion] = useState('')
  const navigate = useNavigate()

  const isFeishu = isFeishuEnv()
  const isDingTalk = isDingTalkEnv()
  const isNative = isFeishu || isDingTalk

  useEffect(() => {
    const promises: Promise<void>[] = []
    if (isFeishu) {
      promises.push(api.getFeishuBinding().then(setBinding).catch(() => {}))
    }
    if (isDingTalk) {
      promises.push(api.getDingTalkBinding().then(setDingBinding).catch(() => {}))
    }
    promises.push(api.getVersion().then(v => setVersion(v.version || '')).catch(() => {}))
    Promise.all(promises)
  }, [])

  const reAuth = () => {
    // 必须清空 AuthContext 的 user（只清 token 守卫仍认为已登录不会重定向），
    // 并回登录页实际挂载的 '/'：路由表没有 '/login'，跳过去会白屏卡死
    logout()
    navigate('/', { replace: true })
  }

  const handleUnbind = async () => {
    if (!confirm('确定解除飞书绑定？')) return
    try {
      await api.unbindFeishu()
      reAuth()
    } catch { /* ignore */ }
  }

  const handleDingUnbind = async () => {
    if (!confirm('确定解除钉钉绑定？')) return
    try {
      await api.unbindDingTalk()
      reAuth()
    } catch { /* ignore */ }
  }

  return (
    <div className="min-h-screen bg-gray-50 pb-16">
      <header className="sticky top-0 z-40 bg-white border-b border-gray-100 px-4 py-3">
        <h1 className="text-lg font-semibold">设置</h1>
      </header>
      <div className="p-4 space-y-4">
        <div className="bg-white rounded-xl shadow-sm border border-gray-100">
          <div className="p-4 flex items-center gap-3">
            <div className="w-10 h-10 rounded-full bg-blue-100 flex items-center justify-center text-blue-600 font-semibold">
              {user?.username?.charAt(0)?.toUpperCase() || '?'}
            </div>
            <div>
              <div className="font-medium text-gray-900">{user?.username}</div>
              <div className="text-xs text-gray-400">ID: {user?.id}</div>
            </div>
          </div>
        </div>

        {(isFeishu || isDingTalk) && (
          <div className="bg-white rounded-xl shadow-sm border border-gray-100">
            {isFeishu && (
              <div className="px-4 py-3 flex items-center justify-between">
                <div className="flex items-center gap-2.5">
                  <Link2 className="w-4 h-4 text-gray-400" />
                  <span className="text-sm text-gray-700">飞书绑定</span>
                </div>
                {binding?.bound ? (
                  <button onClick={handleUnbind} className="text-xs text-red-500">解绑 ({binding.feishu_name})</button>
                ) : (
                  <span className="text-xs text-gray-400">未绑定</span>
                )}
              </div>
            )}
            {isDingTalk && (
              <div className="px-4 py-3 flex items-center justify-between border-t border-gray-50">
                <div className="flex items-center gap-2.5">
                  <Link2 className="w-4 h-4 text-gray-400" />
                  <span className="text-sm text-gray-700">钉钉绑定</span>
                </div>
                {dingBinding?.bound ? (
                  <button onClick={handleDingUnbind} className="text-xs text-red-500">解绑 ({dingBinding.ding_name})</button>
                ) : (
                  <span className="text-xs text-gray-400">未绑定</span>
                )}
              </div>
            )}
          </div>
        )}

        <div className="bg-white rounded-xl shadow-sm border border-gray-100">
          <div className="px-4 py-3 flex items-center justify-between">
            <div className="flex items-center gap-2.5">
              <Info className="w-4 h-4 text-gray-400" />
              <span className="text-sm text-gray-700">版本</span>
            </div>
            <span className="text-xs text-gray-400">{version || '-'}</span>
          </div>
        </div>

        {!isNative && (
          <button onClick={logout}
            className="w-full bg-white rounded-xl shadow-sm border border-gray-100 p-4 flex items-center justify-center gap-2 text-red-500">
            <LogOut className="w-4 h-4" />
            <span className="text-sm font-medium">退出登录</span>
          </button>
        )}
      </div>
    </div>
  )
}
