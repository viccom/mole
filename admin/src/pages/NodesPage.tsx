import { useState } from 'react'
import { RefreshCw, ChevronDown, ChevronRight, Trash2, Plus, Database, ArrowRightLeft, RotateCcw, Cpu, HardDrive, Clock, Monitor, Activity } from 'lucide-react'
import { api } from '../api/client'
import type { Node, PersistedNode, Tunnel, SysInfo, ClientTunnelStatus } from '../types/api'
import { PageHeader } from '../components/PageHeader'
import { Badge } from '../components/Badge'
import { Loading } from '../components/Loading'
import { Empty } from '../components/Empty'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { TunnelFormModal } from '../components/TunnelFormModal'
import { useToast } from '../hooks/useToast'
import { useRequest } from '../hooks/useRequest'
import { formatTimeAgo, formatBytes } from '../lib/utils'

export function NodesPage() {
  const { toast } = useToast()
  const [expandedIds, setExpandedIds] = useState<Set<string>>(new Set())
  const [deleteTarget, setDeleteTarget] = useState<Node | null>(null)
  const [tunnelModal, setTunnelModal] = useState<{ presetNodeId: string } | null>(null)
  const [showPersisted, setShowPersisted] = useState(false)
  const [migrateTarget, setMigrateTarget] = useState<{ tunnel: Tunnel; fromNodeId: string; fromNodeName: string } | null>(null)
  const [migrateNodeId, setMigrateNodeId] = useState('')
  const [migrateAll, setMigrateAll] = useState<{ fromNodeId: string; fromNodeName: string; tunnels: Tunnel[] } | null>(null)
  const [migrateAllNodeId, setMigrateAllNodeId] = useState('')
  const [migrating, setMigrating] = useState(false)
  const [bulkDeleteIds, setBulkDeleteIds] = useState<Set<string>>(new Set())
  const [confirmBulkDelete, setConfirmBulkDelete] = useState(false)

  const { data, loading, run: fetchNodes } = useRequest(
    () => api.getNodes(),
    {
      onError: (error) => {
        toast(error.message || '加载节点失败', 'error')
      },
    },
  )
  const nodes = data?.items || []

  const { data: persistedData, loading: persistedLoading, run: fetchPersisted } = useRequest(
    () => api.getPersistedNodes(),
    { onError: () => {} },
  )
  const persistedNodes = persistedData?.items || []

  const onlineNodes = nodes.filter(n => n.status === 'online')

  const toggleExpand = (id: string) => {
    setExpandedIds(prev => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  const handleDelete = async () => {
    if (!deleteTarget) return
    try {
      await api.deleteNode(deleteTarget.id)
      toast('节点已删除', 'success')
      fetchNodes()
    } catch (err: unknown) {
      toast((err as Error).message || '删除失败', 'error')
    } finally {
      setDeleteTarget(null)
    }
  }

  const handleDeleteTunnel = async (nodeId: string, name: string) => {
    try {
      await api.deleteTunnel(nodeId, name)
      toast('隧道已删除', 'success')
      fetchNodes()
      if (showPersisted) fetchPersisted()
    } catch (err: unknown) {
      toast((err as Error).message || '删除隧道失败', 'error')
    }
  }

  const handleDeletePersistedNode = async (nodeId: string) => {
    try {
      await api.deleteNode(nodeId)
      toast('节点数据已清理', 'success')
      fetchPersisted()
      fetchNodes()
    } catch (err: unknown) {
      toast((err as Error).message || '删除失败', 'error')
    }
  }

  const handleMigrateTunnel = async () => {
    if (!migrateTarget || !migrateNodeId) return
    try {
      await api.updateTunnel({
        name: migrateTarget.tunnel.name,
        type: migrateTarget.tunnel.type,
        target: migrateTarget.tunnel.target,
        domain: migrateTarget.tunnel.domain,
        listen_port: migrateTarget.tunnel.listen_port,
        enabled: migrateTarget.tunnel.enabled,
        node_id: migrateNodeId,
        original_node_id: migrateTarget.fromNodeId,
        para: migrateTarget.tunnel.para,
      })
      toast('隧道已迁移', 'success')
      setMigrateTarget(null)
      setMigrateNodeId('')
      fetchPersisted()
      fetchNodes()
    } catch (err: unknown) {
      toast((err as Error).message || '迁移失败', 'error')
    }
  }

  const handleMigrateAll = async () => {
    if (!migrateAll || !migrateAllNodeId) return
    setMigrating(true)
    let success = 0
    let failed = 0
    for (const t of migrateAll.tunnels) {
      try {
        await api.updateTunnel({
          name: t.name,
          type: t.type,
          target: t.target,
          domain: t.domain,
          listen_port: t.listen_port,
          enabled: t.enabled,
          node_id: migrateAllNodeId,
          original_node_id: migrateAll.fromNodeId,
          para: t.para,
        })
        success++
      } catch {
        failed++
      }
    }
    toast(`已迁移 ${success} 条隧道${failed > 0 ? `，${failed} 条失败` : ''}`, failed > 0 ? 'error' : 'success')
    setMigrating(false)
    setMigrateAll(null)
    setMigrateAllNodeId('')
    fetchPersisted()
    fetchNodes()
  }

  const handleBulkDelete = async () => {
    let failed = 0
    for (const id of bulkDeleteIds) {
      try {
        await api.deleteNode(id)
      } catch {
        failed++
      }
    }
    toast(`已清理 ${bulkDeleteIds.size - failed} 个离线节点${failed > 0 ? `，${failed} 个失败` : ''}`, failed > 0 ? 'error' : 'success')
    setBulkDeleteIds(new Set())
    setConfirmBulkDelete(false)
    fetchPersisted()
    fetchNodes()
  }

  const toggleBulkItem = (id: string) => {
    setBulkDeleteIds(prev => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  const offlinePersisted = persistedNodes.filter(n => !n.online)

  return (
    <div className="flex flex-col h-full">
      <PageHeader
        title="节点管理"
        actions={
          <div className="flex items-center gap-2">
            <button
              onClick={() => { setShowPersisted(!showPersisted); if (!showPersisted) fetchPersisted() }}
              className={`flex items-center gap-1.5 px-3 py-1.5 text-sm border rounded-lg transition-colors ${showPersisted ? 'bg-gray-800 text-white border-gray-800' : 'border-gray-300 hover:bg-gray-50'}`}
            >
              <Database className="w-4 h-4" />
              持久化数据
            </button>
            <button
              onClick={fetchNodes}
              className="flex items-center gap-1.5 px-3 py-1.5 text-sm border border-gray-300 rounded-lg hover:bg-gray-50"
            >
              <RefreshCw className="w-4 h-4" />
              刷新
            </button>
          </div>
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
                <th className="px-4 py-3">远程地址</th>
                <th className="px-4 py-3">连接时间</th>
                <th className="px-4 py-3">心跳</th>
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
                    onDelete={() => setDeleteTarget(node)}
                    onAddTunnel={() => setTunnelModal({ presetNodeId: node.id })}
                    onDeleteTunnel={handleDeleteTunnel}
                    onNodeUpdated={fetchNodes}
                  />
                )
              })}
            </tbody>
          </table>
        )}

        {/* Persisted data panel */}
        {showPersisted && (
          <div className="border-t-2 border-gray-300">
            <div className="px-6 py-3 bg-gray-100 flex items-center justify-between">
              <div className="flex items-center gap-2">
                <Database className="w-4 h-4 text-gray-500" />
                <span className="text-sm font-medium text-gray-700">持久化节点数据</span>
                <span className="text-xs text-gray-400">{offlinePersisted.length} 个离线</span>
              </div>
              {offlinePersisted.length > 0 && (
                <div className="flex items-center gap-2">
                  {bulkDeleteIds.size > 0 && (
                    <button
                      onClick={() => setConfirmBulkDelete(true)}
                      className="px-3 py-1 text-xs bg-red-600 text-white rounded-lg hover:bg-red-700"
                    >
                      清理选中 ({bulkDeleteIds.size})
                    </button>
                  )}
                  <button
                    onClick={() => {
                      if (bulkDeleteIds.size === offlinePersisted.length) setBulkDeleteIds(new Set())
                      else setBulkDeleteIds(new Set(offlinePersisted.map(n => n.id)))
                    }}
                    className="px-3 py-1 text-xs border border-gray-300 rounded-lg hover:bg-gray-200"
                  >
                    {bulkDeleteIds.size === offlinePersisted.length ? '取消全选' : '全选离线'}
                  </button>
                </div>
              )}
            </div>

            {persistedLoading ? (
              <Loading />
            ) : persistedNodes.length === 0 ? (
              <Empty message="无持久化数据" />
            ) : (
              <table className="w-full">
                <thead>
                  <tr className="bg-gray-50/50 text-left text-xs font-medium text-gray-400 uppercase tracking-wider">
                    <th className="w-10 px-4 py-2"></th>
                    <th className="px-4 py-2">ID</th>
                    <th className="px-4 py-2">名称</th>
                    <th className="px-4 py-2">状态</th>
                    <th className="px-4 py-2">隧道数</th>
                    <th className="px-4 py-2">隧道配置</th>
                    <th className="px-4 py-2 text-right">操作</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-100">
                  {persistedNodes.map(n => (
                    <tr key={n.id} className={`hover:bg-gray-50/50 ${!n.online ? 'bg-red-50/30' : ''}`}>
                      <td className="px-4 py-2">
                        {!n.online && (
                          <input
                            type="checkbox"
                            checked={bulkDeleteIds.has(n.id)}
                            onChange={() => toggleBulkItem(n.id)}
                            className="w-4 h-4 rounded border-gray-300"
                          />
                        )}
                      </td>
                      <td className="px-4 py-2 text-sm font-mono text-gray-500">{n.id.slice(0, 8)}</td>
                      <td className="px-4 py-2 text-sm font-medium">{n.name}</td>
                      <td className="px-4 py-2">
                        <Badge variant={n.online ? 'success' : 'error'}>
                          {n.online ? '在线' : '离线'}
                        </Badge>
                      </td>
                      <td className="px-4 py-2 text-sm">{n.tunnel_count}</td>
                      <td className="px-4 py-2">
                        <div className="flex flex-wrap gap-1">
                          {n.tunnels && n.tunnels.length > 0 ? n.tunnels.map((t, i) => (
                            <span key={i} className="inline-flex items-center gap-1 text-xs bg-white border border-gray-200 rounded px-2 py-0.5">
                              <span className="font-medium">{t.name}</span>
                              <span className="text-gray-400">{t.type.toUpperCase()}</span>
                              {!n.online && (
                                <button
                                  onClick={() => setMigrateTarget({ tunnel: t, fromNodeId: n.id, fromNodeName: n.name })}
                                  title="迁移到其他节点"
                                  className="text-blue-400 hover:text-blue-600 ml-0.5"
                                >
                                  <ArrowRightLeft className="w-3 h-3" />
                                </button>
                              )}
                            </span>
                          )) : (
                            <span className="text-xs text-gray-400">无</span>
                          )}
                        </div>
                      </td>
                      <td className="px-4 py-2 text-right">
                        <div className="flex items-center justify-end gap-1">
                          {!n.online && n.tunnels && n.tunnels.length > 0 && (
                            <button
                              onClick={() => setMigrateAll({ fromNodeId: n.id, fromNodeName: n.name, tunnels: n.tunnels })}
                              title="全部迁移到其他节点"
                              className="p-1.5 text-gray-400 hover:text-blue-600 rounded-md hover:bg-blue-50"
                            >
                              <ArrowRightLeft className="w-4 h-4" />
                            </button>
                          )}
                          {!n.online && (
                            <button
                              onClick={() => handleDeletePersistedNode(n.id)}
                              title="清理此节点数据"
                              className="p-1.5 text-gray-400 hover:text-red-600 rounded-md hover:bg-red-50"
                            >
                              <Trash2 className="w-4 h-4" />
                            </button>
                          )}
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        )}
      </div>

      {deleteTarget && (
        <ConfirmDialog
          title="删除节点"
          message={`确定要删除离线节点 "${deleteTarget.name}" 吗？`}
          confirmText="删除"
          onConfirm={handleDelete}
          onCancel={() => setDeleteTarget(null)}
          danger
        />
      )}

      {confirmBulkDelete && (
        <ConfirmDialog
          title="批量清理离线节点"
          message={`确定要清理 ${bulkDeleteIds.size} 个离线节点的数据吗？此操作不可恢复。`}
          confirmText="清理"
          onConfirm={handleBulkDelete}
          onCancel={() => setConfirmBulkDelete(false)}
          danger
        />
      )}

      {migrateTarget && (
        <div className="fixed inset-0 z-[1000] flex items-center justify-center">
          <div className="fixed inset-0 bg-black/50" onClick={() => { setMigrateTarget(null); setMigrateNodeId('') }} />
          <div className="relative bg-white rounded-xl w-full max-w-md p-6 shadow-2xl">
            <h3 className="text-base font-semibold mb-4">迁移隧道</h3>
            <div className="space-y-3 text-sm">
              <div className="bg-gray-50 rounded-lg p-3">
                <span className="text-gray-500">隧道：</span>
                <span className="font-medium">{migrateTarget.tunnel.name}</span>
                <span className="text-gray-400 ml-2">({migrateTarget.tunnel.type.toUpperCase()})</span>
              </div>
              <div className="bg-gray-50 rounded-lg p-3">
                <span className="text-gray-500">来源：</span>
                <span className="font-medium">{migrateTarget.fromNodeName}</span>
                <span className="text-gray-400 ml-2">({migrateTarget.fromNodeId.slice(0, 8)})</span>
              </div>
              <div>
                <label className="block text-gray-500 mb-1">目标节点（在线）</label>
                <select
                  value={migrateNodeId}
                  onChange={e => setMigrateNodeId(e.target.value)}
                  className="w-full px-3 py-2 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-primary/30"
                >
                  <option value="">选择目标节点</option>
                  {onlineNodes.map(n => (
                    <option key={n.id} value={n.id}>
                      {n.name} ({n.id.slice(0, 8)})
                    </option>
                  ))}
                </select>
              </div>
            </div>
            <div className="flex justify-end gap-2 mt-4">
              <button
                onClick={() => { setMigrateTarget(null); setMigrateNodeId('') }}
                className="px-4 py-2 text-sm border border-gray-300 rounded-lg hover:bg-gray-50"
              >
                取消
              </button>
              <button
                onClick={handleMigrateTunnel}
                disabled={!migrateNodeId}
                className="px-4 py-2 text-sm bg-primary text-white rounded-lg hover:bg-primary-dark disabled:opacity-50"
              >
                迁移
              </button>
            </div>
          </div>
        </div>
      )}

      {migrateAll && (
        <div className="fixed inset-0 z-[1000] flex items-center justify-center">
          <div className="fixed inset-0 bg-black/50" onClick={() => { if (!migrating) { setMigrateAll(null); setMigrateAllNodeId('') } }} />
          <div className="relative bg-white rounded-xl w-full max-w-md p-6 shadow-2xl">
            <h3 className="text-base font-semibold mb-4">全部迁移</h3>
            <div className="space-y-3 text-sm">
              <div className="bg-gray-50 rounded-lg p-3">
                <span className="text-gray-500">来源：</span>
                <span className="font-medium">{migrateAll.fromNodeName}</span>
                <span className="text-gray-400 ml-2">({migrateAll.fromNodeId.slice(0, 8)})</span>
              </div>
              <div className="bg-gray-50 rounded-lg p-3">
                <span className="text-gray-500">隧道数量：</span>
                <span className="font-medium">{migrateAll.tunnels.length} 条</span>
                <div className="flex flex-wrap gap-1 mt-1.5">
                  {migrateAll.tunnels.map((t, i) => (
                    <span key={i} className="text-xs text-gray-500 bg-white border border-gray-200 rounded px-1.5 py-0.5">
                      {t.name}
                    </span>
                  ))}
                </div>
              </div>
              <div>
                <label className="block text-gray-500 mb-1">目标节点（在线）</label>
                <select
                  value={migrateAllNodeId}
                  onChange={e => setMigrateAllNodeId(e.target.value)}
                  disabled={migrating}
                  className="w-full px-3 py-2 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-primary/30"
                >
                  <option value="">选择目标节点</option>
                  {onlineNodes.map(n => (
                    <option key={n.id} value={n.id}>
                      {n.name} ({n.id.slice(0, 8)})
                    </option>
                  ))}
                </select>
              </div>
            </div>
            <div className="flex justify-end gap-2 mt-4">
              <button
                onClick={() => { setMigrateAll(null); setMigrateAllNodeId('') }}
                disabled={migrating}
                className="px-4 py-2 text-sm border border-gray-300 rounded-lg hover:bg-gray-50 disabled:opacity-50"
              >
                取消
              </button>
              <button
                onClick={handleMigrateAll}
                disabled={!migrateAllNodeId || migrating}
                className="px-4 py-2 text-sm bg-primary text-white rounded-lg hover:bg-primary-dark disabled:opacity-50"
              >
                {migrating ? '迁移中...' : `迁移 ${migrateAll.tunnels.length} 条隧道`}
              </button>
            </div>
          </div>
        </div>
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

function rttColor(rtt: number): string {
  if (rtt < 50) return 'text-emerald-600'
  if (rtt < 200) return 'text-amber-600'
  return 'text-red-600'
}

function rttBg(rtt: number): string {
  if (rtt < 50) return 'bg-emerald-50'
  if (rtt < 200) return 'bg-amber-50'
  return 'bg-red-50'
}

function formatUptime(seconds: number): string {
  if (seconds <= 0) return '<1m'
  if (seconds < 60) return `${seconds}s`
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ${Math.floor((seconds % 3600) / 60)}m`
  return `${Math.floor(seconds / 86400)}d ${Math.floor((seconds % 86400) / 3600)}h`
}

function formatMemUsed(sys: SysInfo): string {
  if (!sys.mem_total_mb || !sys.mem_used_mb) return '-'
  const pct = Math.round((sys.mem_used_mb / sys.mem_total_mb) * 100)
  return `${sys.mem_used_mb}/${sys.mem_total_mb} MB (${pct}%)`
}

function NodeRowGroup({
  node,
  expanded,
  onToggle,
  onDelete,
  onAddTunnel,
  onDeleteTunnel,
  onNodeUpdated,
}: {
  node: Node
  expanded: boolean
  onToggle: () => void
  onDelete: () => void
  onAddTunnel: () => void
  onDeleteTunnel: (nodeId: string, name: string) => void
  onNodeUpdated: () => void
}) {
  const { toast } = useToast()
  const [nodeMaxConns, setNodeMaxConns] = useState(node.rate_limit?.max_conns?.toString() || '')
  const [savingRL, setSavingRL] = useState(false)
  const [restarting, setRestarting] = useState(false)
  const [confirmRestart, setConfirmRestart] = useState(false)

  const handleSaveNodeRL = async () => {
    const val = Number(nodeMaxConns)
    if (val <= 0 && nodeMaxConns !== '') {
      toast('最大连接数必须大于 0', 'error')
      return
    }
    if (nodeMaxConns === '') {
      toast('请输入最大连接数', 'error')
      return
    }
    setSavingRL(true)
    try {
      await api.updateNodeRateLimit(node.id, val)
      toast('节点限速已更新', 'success')
      onNodeUpdated()
    } catch (err: unknown) {
      toast((err as Error).message || '更新失败', 'error')
    } finally {
      setSavingRL(false)
    }
  }

  const handleRestart = async () => {
    setRestarting(true)
    setConfirmRestart(false)
    try {
      await api.restartNode(node.id, 0, 'manual restart from admin')
      toast('重启指令已发送', 'success')
    } catch (err: unknown) {
      toast((err as Error).message || '重启失败', 'error')
    } finally {
      setRestarting(false)
    }
  }

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
          <div className="flex items-center gap-2">
            <Badge variant={node.status === 'online' ? 'success' : 'error'}>
              {node.status === 'online' ? '在线' : '离线'}
            </Badge>
            {node.status === 'online' && node.rtt != null && node.rtt > 0 && (
              <span className={`inline-flex items-center gap-0.5 text-xs px-1.5 py-0.5 rounded-full ${rttBg(node.rtt)} ${rttColor(node.rtt)}`}>
                {node.rtt}ms
              </span>
            )}
          </div>
        </td>
        <td className="px-4 py-3 text-sm">{node.tunnel_count}</td>
        <td className="px-4 py-3 text-sm font-mono text-gray-500">{node.remote_addr || '-'}</td>
        <td className="px-4 py-3 text-sm text-gray-500">{formatTimeAgo(node.connected_at)}</td>
        <td className="px-4 py-3 text-sm text-gray-500">{formatTimeAgo(node.last_heartbeat)}</td>
        <td className="px-4 py-3 text-right">
          <div className="flex items-center justify-end gap-1">
            {node.status === 'online' && (
              <button
                onClick={() => setConfirmRestart(true)}
                disabled={restarting}
                title="重启客户端"
                className="p-1.5 text-gray-400 hover:text-amber-600 rounded-md hover:bg-amber-50 disabled:opacity-50"
              >
                <RotateCcw className={`w-4 h-4 ${restarting ? 'animate-spin' : ''}`} />
              </button>
            )}
            {node.status !== 'online' && (
              <button
                onClick={onDelete}
                title="删除离线节点"
                className="p-1.5 text-gray-400 hover:text-red-600 rounded-md hover:bg-red-50"
              >
                <Trash2 className="w-4 h-4" />
              </button>
            )}
          </div>
        </td>
      </tr>

      {expanded && (
        <tr>
          <td colSpan={10} className="bg-gray-50/70 px-8 py-4">
            <div className="space-y-4">
              <div className="flex flex-wrap items-center gap-x-6 gap-y-1 text-sm text-gray-500">
                <span>节点 ID: <code className="text-xs bg-white px-1.5 py-0.5 rounded border">{node.id}</code></span>
                {node.remote_addr && <span>来源 IP: <code className="text-xs bg-white px-1.5 py-0.5 rounded border">{node.remote_addr}</code></span>}
                <span>连接时间: {formatTimeAgo(node.connected_at)}</span>
                <span>最近心跳: {formatTimeAgo(node.last_heartbeat)}</span>
                {node.rtt != null && node.rtt > 0 && (
                  <span>RTT: <span className={`font-mono font-medium ${rttColor(node.rtt)}`}>{node.rtt}ms</span></span>
                )}
              </div>

              {/* 系统信息 */}
              {node.sysinfo && (
                <div className="bg-white rounded-lg border border-gray-200 p-4">
                  <div className="flex items-center gap-2 mb-3">
                    <Monitor className="w-4 h-4 text-gray-500" />
                    <span className="text-sm font-medium text-gray-700">系统信息</span>
                    {node.sysinfo.agent_version && (
                      <span className="text-xs text-gray-400 bg-gray-100 px-2 py-0.5 rounded-full">v{node.sysinfo.agent_version}</span>
                    )}
                  </div>
                  <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
                    {node.sysinfo.hostname && (
                      <div className="flex items-center gap-2">
                        <HardDrive className="w-3.5 h-3.5 text-gray-400" />
                        <div>
                          <div className="text-xs text-gray-400">主机名</div>
                          <div className="text-sm font-medium">{node.sysinfo.hostname}</div>
                        </div>
                      </div>
                    )}
                    {node.sysinfo.os && (
                      <div className="flex items-center gap-2">
                        <Monitor className="w-3.5 h-3.5 text-gray-400" />
                        <div>
                          <div className="text-xs text-gray-400">系统</div>
                          <div className="text-sm font-medium">{node.sysinfo.os}{node.sysinfo.num_cpu ? ` / ${node.sysinfo.num_cpu} CPU` : ''}</div>
                        </div>
                      </div>
                    )}
                    {node.sysinfo.mem_total_mb != null && node.sysinfo.mem_total_mb > 0 && (
                      <div className="flex items-center gap-2">
                        <Cpu className="w-3.5 h-3.5 text-gray-400" />
                        <div>
                          <div className="text-xs text-gray-400">内存</div>
                          <div className="text-sm font-medium">{formatMemUsed(node.sysinfo)}</div>
                        </div>
                      </div>
                    )}
                    {node.sysinfo.uptime_seconds != null && (
                      <div className="flex items-center gap-2">
                        <Clock className="w-3.5 h-3.5 text-gray-400" />
                        <div>
                          <div className="text-xs text-gray-400">运行时间</div>
                          <div className="text-sm font-medium">{formatUptime(node.sysinfo.uptime_seconds)}</div>
                        </div>
                      </div>
                    )}
                  </div>
                </div>
              )}

              {/* 客户端隧道状态 */}
              {node.client_statuses && node.client_statuses.length > 0 && (
                <div className="bg-white rounded-lg border border-gray-200 p-4">
                  <div className="flex items-center gap-2 mb-3">
                    <Activity className="w-4 h-4 text-gray-500" />
                    <span className="text-sm font-medium text-gray-700">客户端隧道状态</span>
                    <span className="text-xs text-gray-400">{node.client_statuses.filter(s => s.running).length}/{node.client_statuses.length} 运行中</span>
                  </div>
                  <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-2">
                    {node.client_statuses.map((cs, i) => (
                      <div
                        key={`${cs.name}-${i}`}
                        className={`flex items-center justify-between px-3 py-2 rounded-lg border text-sm ${
                          cs.running ? 'border-emerald-200 bg-emerald-50/50' : 'border-gray-200 bg-gray-50/50'
                        }`}
                      >
                        <div className="flex items-center gap-2 min-w-0">
                          <span className={`w-2 h-2 rounded-full shrink-0 ${cs.running ? 'bg-emerald-500' : 'bg-gray-300'}`} />
                          <span className="font-medium truncate">{cs.name}</span>
                          <span className="text-xs text-gray-400 shrink-0">{cs.type}</span>
                        </div>
                        <div className="flex items-center gap-2 shrink-0 ml-2">
                          {cs.running && cs.bytes_in != null && cs.bytes_out != null && (
                            <span className="text-xs text-gray-400">{formatBytes(cs.bytes_in)}/{formatBytes(cs.bytes_out)}</span>
                          )}
                          {cs.running && cs.clients != null && cs.clients > 0 && (
                            <span className="text-xs text-blue-500">{cs.clients}c</span>
                          )}
                          {cs.error && (
                            <span className="text-xs text-red-500 truncate max-w-[80px]" title={cs.error}>{cs.error}</span>
                          )}
                        </div>
                      </div>
                    ))}
                  </div>
                </div>
              )}

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
                        <Badge variant={t.enabled !== false ? 'success' : 'error'}>
                          {t.enabled !== false ? '启用' : '禁用'}
                        </Badge>
                        <Badge variant={t.type === 'http' || t.type === 'https' ? 'info' : t.type === 'tcp' ? 'purple' : 'warning'}>
                          {t.type.toUpperCase()}
                        </Badge>
                        <span className="text-gray-400 text-xs">{t.target}</span>
                        {t.listen_port && (
                          <span className="text-gray-400 text-xs">:{t.listen_port}</span>
                        )}
                        <button
                          onClick={() => onDeleteTunnel(node.id, t.name)}
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

              {/* 节点级限速 */}
              <div className="border-t border-gray-200 pt-3">
                <span className="text-sm font-medium text-gray-700 mb-2 block">节点级限速</span>
                <div className="flex items-center gap-3">
                  <label className="text-sm text-gray-500 shrink-0">最大连接数</label>
                  <input
                    type="number"
                    value={nodeMaxConns}
                    onChange={e => setNodeMaxConns(e.target.value)}
                    placeholder="留空 = 使用全局默认"
                    min="1"
                    max="100000"
                    className="w-36 px-3 py-1.5 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-primary/30"
                  />
                  <button
                    onClick={handleSaveNodeRL}
                    disabled={savingRL}
                    className="px-3 py-1.5 text-xs bg-primary text-white rounded-lg hover:bg-primary-dark disabled:opacity-50"
                  >
                    {savingRL ? '保存中...' : '保存'}
                  </button>
                </div>
              </div>
            </div>
          </td>
        </tr>
      )}

      {confirmRestart && (
        <ConfirmDialog
          title="重启客户端"
          message={`确定要向节点 "${node.name}" (${node.id.slice(0, 8)}) 发送重启指令吗？客户端将在收到指令后断开并重启。`}
          confirmText="重启"
          onConfirm={handleRestart}
          onCancel={() => setConfirmRestart(false)}
          danger
        />
      )}
    </>
  )
}
