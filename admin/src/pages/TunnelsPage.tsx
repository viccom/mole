import { useMemo, useState } from 'react'
import { RefreshCw, Plus, Pencil, Trash2, Network, Zap, ZapOff, Activity, Globe, ArrowDown, ArrowUp, Users } from 'lucide-react'
import { api } from '../api/client'
import type { Tunnel, TunnelStats, TunnelUsageItem, Node } from '../types/api'
import { PageHeader } from '../components/PageHeader'
import { Badge } from '../components/Badge'
import { Loading } from '../components/Loading'
import { Empty } from '../components/Empty'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { StatCard } from '../components/StatCard'
import { TunnelFormModal } from '../components/TunnelFormModal'
import { useToast } from '../hooks/useToast'
import { useRequest } from '../hooks/useRequest'
import { tunnelAccessUrl, formatBytes, formatTimeAgo } from '../lib/utils'

type TunnelPageData = {
  tunnels: Tunnel[]
  stats: TunnelStats | null
  nodes: Node[]
  usageMap: Record<string, TunnelUsageItem>
}

export function TunnelsPage() {
  const { toast } = useToast()
  const [formModal, setFormModal] = useState<{
    tunnel?: Tunnel
    presetNodeId?: string
  } | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<Tunnel | null>(null)

  const { data, loading, run: fetchData } = useRequest<TunnelPageData>(
    async () => {
      const [tunnelsRes, statsRes, usageRes, nodesRes] = await Promise.all([
        api.getTunnels(),
        api.getTunnelStats(),
        api.getTunnelUsage(),
        api.getNodes(),
      ])

      const map: Record<string, TunnelUsageItem> = {}
      for (const item of usageRes.items || []) {
        map[`${item.node_id}:${item.name}`] = item
      }

      return {
        tunnels: tunnelsRes.items || [],
        stats: statsRes,
        nodes: nodesRes.items || [],
        usageMap: map,
      }
    },
    {
      onError: (error) => {
        toast(error.message || '加载数据失败', 'error')
      },
    },
  )

  const tunnels = data?.tunnels || []
  const stats = data?.stats || null
  const nodes = data?.nodes || []
  const usageMap = data?.usageMap || {}

  const nodeMap = useMemo(
    () => Object.fromEntries(nodes.map(node => [node.id, node])),
    [nodes],
  )

  const handleDelete = async () => {
    if (!deleteTarget) return
    if (!deleteTarget.node_id) {
      toast('缺少节点信息，无法删除隧道', 'error')
      return
    }
    try {
      await api.deleteTunnel(deleteTarget.node_id, deleteTarget.name)
      toast('隧道已删除', 'success')
      fetchData()
    } catch (err: unknown) {
      toast((err as Error).message || '删除失败', 'error')
    } finally {
      setDeleteTarget(null)
    }
  }

  const handleToggle = async (tunnel: Tunnel) => {
    if (!tunnel.node_id) {
      toast('缺少节点信息，无法切换状态', 'error')
      return
    }
    try {
      await api.updateTunnel({
        name: tunnel.name,
        type: tunnel.type,
        target: tunnel.target,
        domain: tunnel.domain,
        listen_port: tunnel.listen_port,
        enabled: !tunnel.enabled,
        node_id: tunnel.node_id,
      })
      toast(`隧道已${tunnel.enabled ? '禁用' : '启用'}`, 'success')
      fetchData()
    } catch (err: unknown) {
      toast((err as Error).message || '切换状态失败', 'error')
    }
  }

  const typeBadgeVariant = (type: string): 'info' | 'purple' | 'warning' => {
    if (type === 'http' || type === 'https') return 'info'
    if (type === 'tcp') return 'purple'
    return 'warning'
  }

  return (
    <div className="flex flex-col h-full">
      <PageHeader
        title="隧道管理"
        actions={
          <div className="flex items-center gap-2">
            <button
              onClick={fetchData}
              className="flex items-center gap-1.5 px-3 py-1.5 text-sm border border-gray-300 rounded-lg hover:bg-gray-50"
            >
              <RefreshCw className="w-4 h-4" />
              刷新
            </button>
            <button
              onClick={() => setFormModal({})}
              className="flex items-center gap-1.5 px-3 py-1.5 text-sm bg-primary text-white rounded-lg hover:bg-primary-dark"
            >
              <Plus className="w-4 h-4" />
              创建隧道
            </button>
          </div>
        }
      />

      <div className="flex-1 overflow-auto">
        {/* Stats cards */}
        {stats && (
          <div className="grid grid-cols-2 lg:grid-cols-4 gap-4 p-6">
            <StatCard
              icon={Network}
              iconBg="bg-blue-100"
              iconColor="text-blue-600"
              value={String(stats.total_tunnels)}
              label="隧道总数"
            />
            <StatCard
              icon={Zap}
              iconBg="bg-emerald-100"
              iconColor="text-emerald-600"
              value={String(stats.enabled_tunnels)}
              label="启用中"
            />
            <StatCard
              icon={Activity}
              iconBg="bg-amber-100"
              iconColor="text-amber-600"
              value={String(stats.active_tunnels)}
              label="活跃中"
            />
            <StatCard
              icon={Globe}
              iconBg="bg-purple-100"
              iconColor="text-purple-600"
              value={String((stats.http_tunnels || 0) + (stats.https_tunnels || 0))}
              label="HTTP(S)隧道"
            />
          </div>
        )}

        {loading ? (
          <Loading />
        ) : tunnels.length === 0 ? (
          <Empty message="暂无隧道" />
        ) : (
          <table className="w-full">
            <thead>
              <tr className="bg-gray-50 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                <th className="px-6 py-3">名称</th>
                <th className="px-6 py-3">状态</th>
                <th className="px-6 py-3">类型</th>
                <th className="px-6 py-3">目标</th>
                <th className="px-6 py-3">访问地址</th>
                <th className="px-6 py-3">节点</th>
                <th className="px-6 py-3">流量</th>
                <th className="px-6 py-3">连接</th>
                <th className="px-6 py-3">最近活动</th>
                <th className="px-6 py-3 text-right">操作</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100">
              {tunnels.map(tunnel => {
                const url = tunnelAccessUrl(tunnel)
                const ownerNode = tunnel.node_id ? nodeMap[tunnel.node_id] : undefined
                const usage = usageMap[`${tunnel.node_id}:${tunnel.name}`]
                return (
                  <tr key={`${tunnel.name}-${tunnel.node_id}`} className="hover:bg-gray-50/50">
                    <td className="px-6 py-3 text-sm font-medium">{tunnel.name}</td>
                    <td className="px-6 py-3">
                      <Badge variant={tunnel.enabled ? 'success' : 'error'}>
                        {tunnel.enabled ? '启用' : '禁用'}
                      </Badge>
                    </td>
                    <td className="px-6 py-3">
                      <Badge variant={typeBadgeVariant(tunnel.type)}>
                        {tunnel.type.toUpperCase()}
                      </Badge>
                    </td>
                    <td className="px-6 py-3 text-sm text-gray-600 font-mono">{tunnel.target}</td>
                    <td className="px-6 py-3 text-sm">
                      {(tunnel.type === 'http' || tunnel.type === 'https') && url !== '-' ? (
                        <a
                          href={url}
                          target="_blank"
                          rel="noopener noreferrer"
                          className="text-blue-600 hover:text-blue-700 underline"
                        >
                          {url}
                        </a>
                      ) : (
                        <span className="text-gray-500">{url}</span>
                      )}
                    </td>
                    <td className="px-6 py-3 text-sm text-gray-500">
                      {ownerNode ? ownerNode.name : tunnel.node_id || '-'}
                    </td>
                    <td className="px-6 py-3 text-sm text-gray-500">
                      {usage ? (
                        <div className="flex items-center gap-2" title={`入站: ${formatBytes(usage.bytes_in)} / 出站: ${formatBytes(usage.bytes_out)}`}>
                          <span className="flex items-center gap-0.5 text-emerald-600">
                            <ArrowDown className="w-3 h-3" />
                            {formatBytes(usage.bytes_in)}
                          </span>
                          <span className="flex items-center gap-0.5 text-blue-600">
                            <ArrowUp className="w-3 h-3" />
                            {formatBytes(usage.bytes_out)}
                          </span>
                        </div>
                      ) : (
                        <span className="text-gray-400">-</span>
                      )}
                    </td>
                    <td className="px-6 py-3 text-sm text-gray-500">
                      {usage ? (
                        <div className="flex items-center gap-1" title={`总连接: ${usage.total_connections} / 活跃: ${usage.active_connections}`}>
                          <Users className="w-3.5 h-3.5 text-gray-400" />
                          <span>{usage.active_connections}</span>
                          <span className="text-gray-400">/</span>
                          <span className="text-gray-400">{usage.total_connections}</span>
                        </div>
                      ) : (
                        <span className="text-gray-400">-</span>
                      )}
                    </td>
                    <td className="px-6 py-3 text-sm text-gray-500">
                      {usage?.last_activity ? formatTimeAgo(usage.last_activity) : '-'}
                    </td>
                    <td className="px-6 py-3 text-right">
                      <div className="flex items-center justify-end gap-1">
                        <button
                          onClick={() => handleToggle(tunnel)}
                          title={tunnel.enabled ? '禁用' : '启用'}
                          className={`p-1.5 rounded-md ${
                            tunnel.enabled
                              ? 'text-emerald-500 hover:text-emerald-700 hover:bg-emerald-50'
                              : 'text-gray-400 hover:text-emerald-600 hover:bg-emerald-50'
                          }`}
                        >
                          {tunnel.enabled ? (
                            <Zap className="w-4 h-4" />
                          ) : (
                            <ZapOff className="w-4 h-4" />
                          )}
                        </button>
                        <button
                          onClick={() => setFormModal({ tunnel })}
                          title="编辑"
                          className="p-1.5 text-gray-400 hover:text-blue-600 rounded-md hover:bg-blue-50"
                        >
                          <Pencil className="w-4 h-4" />
                        </button>
                        <button
                          onClick={() => setDeleteTarget(tunnel)}
                          title="删除"
                          className="p-1.5 text-gray-400 hover:text-red-600 rounded-md hover:bg-red-50"
                        >
                          <Trash2 className="w-4 h-4" />
                        </button>
                      </div>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        )}
      </div>

      {deleteTarget && (
        <ConfirmDialog
          title="删除隧道"
          message={`确定要删除隧道 "${deleteTarget.name}" 吗？此操作不可恢复。`}
          confirmText="删除"
          onConfirm={handleDelete}
          onCancel={() => setDeleteTarget(null)}
          danger
        />
      )}

      {formModal && (
        <TunnelFormModal
          tunnel={formModal.tunnel}
          presetNodeId={formModal.presetNodeId}
          onClose={() => setFormModal(null)}
          onSuccess={() => {
            setFormModal(null)
            fetchData()
          }}
        />
      )}
    </div>
  )
}
