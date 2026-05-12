import { useState, useEffect } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { api, type Node } from '../api/client'
import { ArrowLeft, Circle } from 'lucide-react'

import { typeIcons } from '../lib/constants'

export function NodeDetailPage() {
  const { id } = useParams<{ id: string }>()
  const [node, setNode] = useState<Node | null>(null)
  const [loading, setLoading] = useState(true)
  const navigate = useNavigate()

  useEffect(() => {
    if (id) api.getNode(id).then(n => setNode(n)).catch(() => setNode(null)).finally(() => setLoading(false))
  }, [id])

  if (loading) return <div className="min-h-screen bg-gray-50 flex items-center justify-center text-gray-400">加载中...</div>
  if (!node) return <div className="min-h-screen bg-gray-50 flex items-center justify-center text-gray-400">节点未找到</div>

  return (
    <div className="min-h-screen bg-gray-50 pb-8">
      <header className="sticky top-0 z-40 bg-white border-b border-gray-100 px-4 py-3 flex items-center gap-3">
        <button onClick={() => navigate('/nodes')}><ArrowLeft className="w-5 h-5 text-gray-600" /></button>
        <h1 className="text-lg font-semibold">{node.name}</h1>
        <div className="ml-auto flex items-center gap-1.5">
          {node.status === 'online' ? (
            <><Circle className="w-2.5 h-2.5 text-green-500 fill-green-500" /><span className="text-xs text-green-600">在线</span></>
          ) : (
            <><Circle className="w-2.5 h-2.5 text-gray-300" /><span className="text-xs text-gray-400">离线</span></>
          )}
        </div>
      </header>

      <div className="p-4 space-y-4">
        <div className="bg-white rounded-xl p-4 shadow-sm border border-gray-100">
          <h2 className="text-sm font-medium text-gray-500 mb-2">节点信息</h2>
          <div className="space-y-1.5 text-sm">
            <div className="flex justify-between"><span className="text-gray-400">ID</span><span className="text-gray-700">{node.id}</span></div>
            {node.remote_addr && <div className="flex justify-between"><span className="text-gray-400">地址</span><span className="text-gray-700">{node.remote_addr}</span></div>}
            {node.connected_at && <div className="flex justify-between"><span className="text-gray-400">连接时间</span><span className="text-gray-700">{new Date(node.connected_at).toLocaleString()}</span></div>}
          </div>
        </div>

        <div className="bg-white rounded-xl p-4 shadow-sm border border-gray-100">
          <h2 className="text-sm font-medium text-gray-500 mb-3">隧道 ({node.tunnels?.length || 0})</h2>
          {(!node.tunnels || node.tunnels.length === 0) && (
            <p className="text-sm text-gray-400 text-center py-4">暂无隧道</p>
          )}
          <div className="space-y-2">
            {node.tunnels?.map(t => (
              <div key={t.name} className="flex items-center justify-between py-2 border-b border-gray-50 last:border-0">
                <div className="flex items-center gap-2 min-w-0">
                  <span className="text-base">{typeIcons[t.type] || '📡'}</span>
                  <div className="min-w-0">
                    <div className="text-sm font-medium text-gray-800 truncate">{t.name}</div>
                    <div className="text-xs text-gray-400">{t.type} · {t.target}</div>
                  </div>
                </div>
                {(t.enabled === null || t.enabled === true) ? (
                  <span className="text-[10px] px-1.5 py-0.5 rounded bg-green-50 text-green-600">启用</span>
                ) : (
                  <span className="text-[10px] px-1.5 py-0.5 rounded bg-gray-100 text-gray-400">禁用</span>
                )}
              </div>
            ))}
          </div>
        </div>
      </div>
    </div>
  )
}
