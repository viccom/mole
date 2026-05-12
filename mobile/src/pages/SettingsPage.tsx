import { useState, useEffect } from 'react'
import { useAuth } from '../hooks/useAuth'
import { api } from '../api/client'
import { LogOut, Link2, Info } from 'lucide-react'

export function SettingsPage() {
  const { user, logout } = useAuth()
  const [binding, setBinding] = useState<{ bound: boolean; feishu_name?: string } | null>(null)
  const [version, setVersion] = useState('')

  useEffect(() => {
    api.getFeishuBinding().then(setBinding).catch(() => {})
    api.getVersion().then(v => setVersion(v.version || '')).catch(() => {})
  }, [])

  const handleUnbind = async () => {
    if (!confirm('确定解除飞书绑定？')) return
    try {
      await api.unbindFeishu()
      setBinding({ bound: false })
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

        <div className="bg-white rounded-xl shadow-sm border border-gray-100">
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
        </div>

        <div className="bg-white rounded-xl shadow-sm border border-gray-100">
          <div className="px-4 py-3 flex items-center justify-between">
            <div className="flex items-center gap-2.5">
              <Info className="w-4 h-4 text-gray-400" />
              <span className="text-sm text-gray-700">版本</span>
            </div>
            <span className="text-xs text-gray-400">{version || '-'}</span>
          </div>
        </div>

        <button onClick={logout}
          className="w-full bg-white rounded-xl shadow-sm border border-gray-100 p-4 flex items-center justify-center gap-2 text-red-500">
          <LogOut className="w-4 h-4" />
          <span className="text-sm font-medium">退出登录</span>
        </button>
      </div>
    </div>
  )
}
