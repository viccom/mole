import { useState, useEffect, useCallback } from 'react'
import { RefreshCw, ChevronDown, ChevronRight, Unplug, Trash2, Plus } from 'lucide-react'
import { api } from '../api/client'
import type { Node } from '../types/api'
import { PageHeader } from '../components/PageHeader'
import { Badge } from '../components/Badge'
import { Loading } from '../components/Loading'
import { Empty } from '../components/Empty'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { useToast } from '../hooks/useToast'
import { formatTimeAgo } from '../lib/utils'
import { TunnelFormModal } from './TunnelsPage'

export function NodesPage() {
  const { toast } = useToast()
  const [nodes, setNodes] = useState<Node[]>([])
  const [loading, setLoading] = useState(true)
  const [expandedIds, setExpandedIds] = useState<Set<string>>(new Set())
  const [confirmAction, setConfirmAction] = useState<{
    type: 'disconnect' | 'delete'
    node: Node
  } | null>(null)
  const [tunnelModal, setTunnelModal] = useState<{ presetNodeId: string } | null>(null)

  const fetchNodes = useCallback(async () => {
    try {
      setLoading(true)
      const res = await api.getNodes()
      setNodes(res.items || [])
    } catch (err: unknown) {
      toast((err as Error).message || '加载节点失败', 'error')
    } finally {
      setLoading(false)
    }
  }, [toast])

  useEffect(() => { fetchNodes() }, [fetchNodes])

  const toggleExpand = (id: string) => {
    setExpandedIds(prev => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  const handleDisconnect = async () => {
    if (!confirmAction) return
    try {
      await api.disconnectNode(confirmAction.node.id)
      toast('已断开节点连接', 'success')
      fetchNodes()
    } catch (err: unknown) {
      toast((err as Error).message || '断开连接失败', 'error')
    } finally {
      setConfirmAction(null)
    }
  }

  const handleDelete = async () => {
    if (!confirmAction) return
    try {
      await api.deleteNode(confirmAction.node.id)
      toast('节点已删除', 'success')
      fetchNodes()
    } catch (err: unknown) {
      toast((err as Error).message || '删除失败', 'error')
    } finally {
      setConfirmAction(null)
    }
  }

  const handleDeleteTunnel = async (name: string) => {
    try {
      await api.deleteTunnel(name)
      toast('隧道已删除', 'success')
      fetchNodes()
    } catch (err: unknown) {
      toast((err as Error).message || '删除隧道失败', 'error')
    }
  }

  return (
    <div className="flex flex-col h-full">
      <PageHeader
        title="节点管理"
        actions={
          <button
            onClick={fetchNodes}
            className="flex items-center gap-1.5 px-3 py-1.5 text-sm border border-gray-300 rounded-lg hover:bg-gray-50"
          >
            <RefreshCw className="w-4 h-4" />
            刷新
          </button>
        }
      />

      <div className="flex-1 overflow-auto">
        {loading ? (
          <Loading />
        ) : nodes.length === 0 ? (
          <Empty message="暂无节点" />
        ) : (
          <table className="w-full">
            <thead>
              <tr className="bg-gray-50 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                <th className="w-10 px-4 py-3"></th>
                <th className="px-4 py-3">ID</th>
                <th className="px-4 py-3">名称</th>
                <th className="px-4 py-3">归属</th>
                <th className="px-4 py-3">状态</th>
                <th className="px-4 py-3">隧道数</th>
                <th className="px-4 py-3">连接时间</th>
                <th className="px-4 py-3">心跳</th>
                <th className="px-4 py-3">远程地址</th>
                <th className="px-4 py-3 text-right">操作</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100">
              {nodes.map(node => {
                const expanded = expandedIds.has(node.id)
                return (
                  <NodeRowGroup
                    key={node.id}
                    node={node}
                    expanded={expanded}
                    onToggle={() => toggleExpand(node.id)}
                    onDisconnect={() => setConfirmAction({ type: 'disconnect', node })}
                    onDelete={() => setConfirmAction({ type: 'delete', node })}
                    onAddTunnel={() => setTunnelModal({ presetNodeId: node.id })}
                    onDeleteTunnel={handleDeleteTunnel}
                  />
                )
              })}
            </tbody>
          </table>
        )}
      </div>

      {confirmAction && (
        <ConfirmDialog
          title={confirmAction.type === 'disconnect' ? '断开节点' : '删除节点'}
          message={
            confirmAction.type === 'disconnect'
              ? `确定要断开节点 "${confirmAction.node.name}" 的连接吗？`
              : `确定要删除节点 "${confirmAction.node.name}" 吗？此操作不可恢复。`
          }
          confirmText={confirmAction.type === 'disconnect' ? '断开' : '删除'}
          onConfirm={confirmAction.type === 'disconnect' ? handleDisconnect : handleDelete}
          onCancel={() => setConfirmAction(null)}
          danger
        />
      )}

      {tunnelModal && (
        <TunnelFormModal
          presetNodeId={tunnelModal.presetNodeId}
          onClose={() => setTunnelModal(null)}
          onSuccess={() => {
            setTunnelModal(null)
            fetchNodes()
          }}
        />
      )}
    </div>
  )
}

function NodeRowGroup({
  node,
  expanded,
  onToggle,
  onDisconnect,
  onDelete,
  onAddTunnel,
  onDeleteTunnel,
}: {
  node: Node
  expanded: boolean
  onToggle: () => void
  onDisconnect: () => void
  onDelete: () => void
  onAddTunnel: () => void
  onDeleteTunnel: (name: string) => void
}) {
  return (
    <>
      <tr className="hover:bg-gray-50/50">
        <td className="px-4 py-3">
          <button onClick={onToggle} className="text-gray-400 hover:text-gray-600">
            {expanded ? <ChevronDown className="w-4 h-4" /> : <ChevronRight className="w-4 h-4" />}
          </button>
        </td>
        <td className="px-4 py-3 text-sm font-mono text-gray-500">{node.id.slice(0, 8)}</td>
        <td className="px-4 py-3 text-sm font-medium">{node.name}</td>
        <td className="px-4 py-3 text-sm text-gray-500">
          {node.owner_user_id === 'system' ? '系统' : node.owner_user_id}
        </td>
        <td className="px-4 py-3">
          <Badge variant={node.status === 'online' ? 'success' : 'error'}>
            {node.status === 'online' ? '在线' : '离线'}
          </Badge>
        </td>
        <td className="px-4 py-3 text-sm">{node.tunnel_count}</td>
        <td className="px-4 py-3 text-sm text-gray-500">{formatTimeAgo(node.connected_at)}</td>
        <td className="px-4 py-3 text-sm text-gray-500">{formatTimeAgo(node.last_heartbeat)}</td>
        <td className="px-4 py-3 text-sm font-mono text-gray-500">{node.remote_addr || '-'}</td>
        <td className="px-4 py-3 text-right">
          <div className="flex items-center justify-end gap-1">
            {node.status === 'online' && (
              <button
                onClick={onDisconnect}
                title="断开连接"
                className="p-1.5 text-gray-400 hover:text-amber-600 rounded-md hover:bg-amber-50"
              >
                <Unplug className="w-4 h-4" />
              </button>
            )}
            <button
              onClick={onDelete}
              title="删除节点"
              className="p-1.5 text-gray-400 hover:text-red-600 rounded-md hover:bg-red-50"
            >
              <Trash2 className="w-4 h-4" />
            </button>
          </div>
        </td>
      </tr>

      {expanded && (
        <tr>
          <td colSpan={10} className="bg-gray-50/70 px-8 py-4">
            <div className="space-y-4">
              {/* Token */}
              {node.token && (
                <div className="flex items-center gap-2 text-sm">
                  <span className="text-gray-500 w-16 flex-shrink-0">Token:</span>
                  <code className="bg-white px-2 py-1 rounded text-xs font-mono border border-gray-200 break-all">
                    {node.token}
                  </code>
                </div>
              )}

              {/* Time info */}
              <div className="flex items-center gap-6 text-sm text-gray-500">
                <span>连接时间: {formatTimeAgo(node.connected_at)}</span>
                <span>最近心跳: {formatTimeAgo(node.last_heartbeat)}</span>
              </div>

              {/* Tunnel list */}
              <div>
                <div className="flex items-center justify-between mb-2">
                  <span className="text-sm font-medium text-gray-700">
                    隧道列表 ({node.tunnels?.length || 0})
                  </span>
                  <button
                    onClick={onAddTunnel}
                    className="flex items-center gap-1 text-xs text-blue-600 hover:text-blue-700"
                  >
                    <Plus className="w-3 h-3" />
                    添加隧道
                  </button>
                </div>

                {node.tunnels && node.tunnels.length > 0 ? (
                  <div className="flex flex-wrap gap-2">
                    {node.tunnels.map((t, i) => (
                      <div
                        key={`${t.name}-${i}`}
                        className="inline-flex items-center gap-1.5 bg-white border border-gray-200 rounded-lg px-3 py-1.5 text-sm"
                      >
                        <span className="font-medium">{t.name}</span>
                        <Badge variant={t.enabled ? 'success' : 'error'}>
                          {t.enabled ? '启用' : '禁用'}
                        </Badge>
                        <Badge variant={t.type === 'http' ? 'info' : t.type === 'tcp' ? 'purple' : 'warning'}>
                          {t.type.toUpperCase()}
                        </Badge>
                        <span className="text-gray-400 text-xs">{t.target}</span>
                        <button
                          onClick={() => onDeleteTunnel(t.name)}
                          title="删除隧道"
                          className="ml-1 text-gray-300 hover:text-red-500"
                        >
                          <Trash2 className="w-3.5 h-3.5" />
                        </button>
                      </div>
                    ))}
                  </div>
                ) : (
                  <p className="text-sm text-gray-400">暂无隧道</p>
                )}
              </div>
            </div>
          </td>
        </tr>
      )}
    </>
  )
}
