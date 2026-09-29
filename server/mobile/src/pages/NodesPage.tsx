import { useState, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { api, type Node } from '../api/client'
import { Circle, ArrowRight } from 'lucide-react'

export function NodesPage() {
  const [nodes, setNodes] = useState<Node[]>([])
  const [loading, setLoading] = useState(true)
  const navigate = useNavigate()

  const refresh = () => {
    setLoading(true)
    api.getNodes().then(res => setNodes(res.items || [])).finally(() => setLoading(false))
  }

  useEffect(() => { refresh() }, [])

  return (
    <div className="min-h-screen bg-gray-50 pb-16">
      <header className="sticky top-0 z-40 bg-white border-b border-gray-100 px-4 py-3 flex items-center justify-between">
        <h1 className="text-lg font-semibold">节点管理</h1>
        <button onClick={refresh} className="text-sm text-blue-600" disabled={loading}>
          {loading ? '加载中...' : '刷新'}
        </button>
      </header>
      <div className="p-4 space-y-3">
        {nodes.length === 0 && !loading && (
          <div className="text-center text-gray-400 py-20">暂无节点</div>
        )}
        {nodes.map(node => (
          <button key={node.id} onClick={() => navigate(`/nodes/${node.id}`)}
            className="w-full bg-white rounded-xl p-4 shadow-sm border border-gray-100 text-left">
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-3 min-w-0">
                {node.status === 'online' ? (
                  <Circle className="w-3 h-3 text-green-500 fill-green-500 shrink-0" />
                ) : (
                  <Circle className="w-3 h-3 text-gray-300 shrink-0" />
                )}
                <div className="min-w-0">
                  <div className="font-medium text-gray-900 truncate">{node.name}</div>
                  <div className="text-xs text-gray-400 mt-0.5">
                    ID: {node.id} · {node.tunnels?.length || 0} 条隧道
                  </div>
                  <div className="text-xs text-gray-400 mt-0.5 truncate">
                    {node.remote_addr || '未连接'}
                  </div>
                </div>
              </div>
              <ArrowRight className="w-4 h-4 text-gray-300 shrink-0" />
            </div>
          </button>
        ))}
      </div>
    </div>
  )
}
