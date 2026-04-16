import { useState, useEffect, useCallback } from 'react'
import { RefreshCw, Plus, Pencil, Trash2, Network, Zap, Activity, Globe, ArrowDown, ArrowUp, Users } from 'lucide-react'
import { api } from '../api/client'
import type { Tunnel, TunnelStats, TunnelUsageItem, Node } from '../types/api'
import { PageHeader } from '../components/PageHeader'
import { Badge } from '../components/Badge'
import { Loading } from '../components/Loading'
import { Empty } from '../components/Empty'
import { Modal } from '../components/Modal'
import { FormField } from '../components/FormField'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { StatCard } from '../components/StatCard'
import { useToast } from '../hooks/useToast'
import { tunnelAccessUrl, formatBytes, formatTimeAgo } from '../lib/utils'

export function TunnelsPage() {
  const { toast } = useToast()
  const [tunnels, setTunnels] = useState<Tunnel[]>([])
  const [stats, setStats] = useState<TunnelStats | null>(null)
  const [usageMap, setUsageMap] = useState<Record<string, TunnelUsageItem>>({})
  const [nodes, setNodes] = useState<Node[]>([])
  const [loading, setLoading] = useState(true)
  const [formModal, setFormModal] = useState<{
    tunnel?: Tunnel
    presetNodeId?: string
  } | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<Tunnel | null>(null)

  const fetchData = useCallback(async () => {
    try {
      setLoading(true)
      const [tunnelsRes, statsRes, usageRes, nodesRes] = await Promise.all([
        api.getTunnels(),
        api.getTunnelStats(),
        api.getTunnelUsage(),
        api.getNodes(),
      ])
      setTunnels(tunnelsRes.items || [])
      setStats(statsRes)
      setNodes(nodesRes.items || [])
      // Build usage lookup map by tunnel name
      const map: Record<string, TunnelUsageItem> = {}
      for (const item of usageRes.items || []) {
        map[item.name] = item
      }
      setUsageMap(map)
    } catch (err: unknown) {
      toast((err as Error).message || '加载数据失败', 'error')
    } finally {
      setLoading(false)
    }
  }, [toast])

  useEffect(() => { fetchData() }, [fetchData])

  const handleDelete = async () => {
    if (!deleteTarget) return
    try {
      await api.deleteTunnel(deleteTarget.name)
      toast('隧道已删除', 'success')
      fetchData()
    } catch (err: unknown) {
      toast((err as Error).message || '删除失败', 'error')
    } finally {
      setDeleteTarget(null)
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
                const ownerNode = nodes.find(n => n.id === tunnel.node_id)
                const usage = usageMap[tunnel.name]
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
                      {tunnel.type === 'http' && url !== '-' ? (
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

// ---------------------------------------------------------------------------
// TunnelFormModal — shared form for create / edit tunnel
// ---------------------------------------------------------------------------

interface TunnelFormModalProps {
  tunnel?: Tunnel
  presetNodeId?: string
  onClose: () => void
  onSuccess: () => void
}

export function TunnelFormModal({ tunnel, presetNodeId, onClose, onSuccess }: TunnelFormModalProps) {
  const { toast } = useToast()
  const isEdit = !!tunnel

  const [name, setName] = useState(tunnel?.name || '')
  const [type, setType] = useState<'http' | 'https' | 'tcp' | 'udp'>(tunnel?.type || 'http')
  const [target, setTarget] = useState(tunnel?.target || '')
  const [domain, setDomain] = useState(tunnel?.domain || '')
  const [listenPort, setListenPort] = useState(tunnel?.listen_port?.toString() || '')
  const [nodeId, setNodeId] = useState(presetNodeId || tunnel?.node_id || '')
  const [enabled, setEnabled] = useState(tunnel?.enabled !== false)
  const [submitting, setSubmitting] = useState(false)

  const [nodes, setNodes] = useState<Node[]>([])
  const [nodesLoading, setNodesLoading] = useState(true)

  useEffect(() => {
    api.getNodes()
      .then(res => setNodes(res.items || []))
      .catch(() => {})
      .finally(() => setNodesLoading(false))
  }, [])

  const onlineNodes = nodes.filter(n => n.status === 'online')

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()

    if (!name.trim()) { toast('请输入隧道名称', 'error'); return }
    if (!target.trim()) { toast('请输入目标地址', 'error'); return }
    if (!isEdit && !nodeId) { toast('请选择节点', 'error'); return }

    try {
      setSubmitting(true)
      await api.createTunnel({
        name: name.trim(),
        type,
        target: target.trim(),
        domain: (type === 'http' || type === 'https') && domain.trim() ? domain.trim() : undefined,
        listen_port: (type === 'tcp' || type === 'udp') && listenPort ? Number(listenPort) : undefined,
        enabled,
        node_id: nodeId,
      })
      toast(isEdit ? '隧道已更新' : '隧道已创建', 'success')
      onSuccess()
    } catch (err: unknown) {
      toast((err as Error).message || '操作失败', 'error')
    } finally {
      setSubmitting(false)
    }
  }

  const inputClass = "w-full px-3 py-2 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-primary/30 focus:border-primary"
  const selectClass = inputClass

  return (
    <Modal title={isEdit ? '编辑隧道' : '创建隧道'} onClose={onClose}>
      <form onSubmit={handleSubmit} className="space-y-4">
        <FormField label="隧道名称">
          <input
            type="text"
            value={name}
            onChange={e => setName(e.target.value)}
            disabled={isEdit}
            placeholder="例如: my-web"
            className={inputClass}
          />
        </FormField>

        <FormField label="类型">
          <select
            value={type}
            onChange={e => setType(e.target.value as 'http' | 'https' | 'tcp' | 'udp')}
            className={selectClass}
          >
            <option value="http">HTTP</option>
            <option value="https">HTTPS</option>
            <option value="tcp">TCP</option>
            <option value="udp">UDP</option>
          </select>
        </FormField>

        <FormField label="目标地址">
          <input
            type="text"
            value={target}
            onChange={e => setTarget(e.target.value)}
            placeholder="例如: 127.0.0.1:8080"
            className={inputClass}
          />
        </FormField>

        {(type === 'http' || type === 'https') && (
          <FormField label="域名（可选）">
            <input
              type="text"
              value={domain}
              onChange={e => setDomain(e.target.value)}
              placeholder="例如: app.example.com"
              className={inputClass}
            />
          </FormField>
        )}

        {(type === 'tcp' || type === 'udp') && (
          <FormField label="监听端口">
            <input
              type="number"
              value={listenPort}
              onChange={e => setListenPort(e.target.value)}
              placeholder="例如: 8080"
              className={inputClass}
            />
          </FormField>
        )}

        {!isEdit && (
          <FormField label="节点">
            <select
              value={nodeId}
              onChange={e => setNodeId(e.target.value)}
              disabled={nodesLoading || !!presetNodeId}
              className={selectClass}
            >
              <option value="">{nodesLoading ? '加载中...' : '选择节点'}</option>
              {onlineNodes.map(n => (
                <option key={n.id} value={n.id}>{n.name} ({n.id.slice(0, 8)})</option>
              ))}
            </select>
            {!nodesLoading && onlineNodes.length === 0 && (
              <p className="text-xs text-amber-600 mt-1">暂无在线节点</p>
            )}
          </FormField>
        )}

        <FormField label="启用">
          <label className="flex items-center gap-2 cursor-pointer">
            <input
              type="checkbox"
              checked={enabled}
              onChange={e => setEnabled(e.target.checked)}
              className="w-4 h-4 text-primary rounded border-gray-300 focus:ring-primary"
            />
            <span className="text-sm text-gray-600">启用此隧道</span>
          </label>
        </FormField>

        <div className="flex justify-end gap-2 pt-2">
          <button
            type="button"
            onClick={onClose}
            className="px-4 py-2 text-sm border border-gray-300 rounded-lg hover:bg-gray-50"
          >
            取消
          </button>
          <button
            type="submit"
            disabled={submitting}
            className="px-4 py-2 text-sm bg-primary text-white rounded-lg hover:bg-primary-dark disabled:opacity-50"
          >
            {submitting ? '提交中...' : isEdit ? '保存' : '创建'}
          </button>
        </div>
      </form>
    </Modal>
  )
}
