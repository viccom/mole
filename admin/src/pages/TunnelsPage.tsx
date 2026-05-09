import { useMemo, useState } from 'react'
import { RefreshCw, Plus, Pencil, Trash2, Network, Zap, ZapOff, Activity, Globe, ArrowDown, ArrowUp, Users, Search } from 'lucide-react'
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

type TabKey = 'all' | 'web' | 'stream' | 'serial' | 'vpn'
type FilterKey = 'all' | 'enabled' | 'disabled' | 'active'

const tabFilters: Record<TabKey, (t: Tunnel) => boolean> = {
  all: () => true,
  web: (t: Tunnel) => t.type === 'http' || t.type === 'https',
  stream: (t: Tunnel) => t.type === 'tcp' || t.type === 'udp',
  serial: (t: Tunnel) => ['ser2mq', 'ser2tcp', 'ser2udp'].includes(t.type),
  vpn: (t: Tunnel) => t.type === 'vpn-manager',
}

const tabs: { key: TabKey; label: string }[] = [
  { key: 'all', label: '全部' },
  { key: 'web', label: 'Web 隧道' },
  { key: 'stream', label: '透明隧道' },
  { key: 'serial', label: '串口' },
  { key: 'vpn', label: 'VPN' },
]

const filterOptions: { key: FilterKey; label: string }[] = [
  { key: 'all', label: '全部' },
  { key: 'enabled', label: '启用' },
  { key: 'disabled', label: '禁用' },
  { key: 'active', label: '活跃' },
]

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
  const [activeTab, setActiveTab] = useState<TabKey>('all')
  const [filterStatus, setFilterStatus] = useState<FilterKey>('all')
  const [searchKeyword, setSearchKeyword] = useState('')

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

  const allTabCounts = useMemo(() => {
    const counts: Record<TabKey, number> = { all: tunnels.length, web: 0, stream: 0, serial: 0, vpn: 0 }
    for (const t of tunnels) {
      if (t.type === 'http' || t.type === 'https') counts.web++
      if (t.type === 'tcp' || t.type === 'udp') counts.stream++
      if (['ser2mq', 'ser2tcp', 'ser2udp'].includes(t.type)) counts.serial++
      if (t.type === 'vpn-manager') counts.vpn++
    }
    return counts
  }, [tunnels])

  const filteredTunnels = useMemo(() => {
    let result = tunnels
    if (activeTab !== 'all') {
      result = result.filter(tabFilters[activeTab])
    }
    if (filterStatus === 'enabled') result = result.filter(t => t.enabled)
    else if (filterStatus === 'disabled') result = result.filter(t => !t.enabled)
    else if (filterStatus === 'active') {
      result = result.filter(t => {
        const usage = usageMap[`${t.node_id}:${t.name}`]
        return usage && usage.active_connections > 0
      })
    }
    if (searchKeyword) {
      const kw = searchKeyword.toLowerCase()
      result = result.filter(t =>
        t.name.toLowerCase().includes(kw) ||
        t.type.toLowerCase().includes(kw) ||
        t.target.toLowerCase().includes(kw) ||
        (t.domain || '').toLowerCase().includes(kw) ||
        (t.para?.broker || '').toLowerCase().includes(kw) ||
        (t.para?.binary?.name || '').toLowerCase().includes(kw)
      )
    }
    return result
  }, [tunnels, activeTab, filterStatus, searchKeyword, usageMap])

  const tabStats = useMemo(() => {
    const tabTunnels = activeTab === 'all' ? tunnels : tunnels.filter(tabFilters[activeTab])
    return {
      total: tabTunnels.length,
      enabled: tabTunnels.filter(t => t.enabled).length,
      active: tabTunnels.filter(t => {
        const usage = usageMap[`${t.node_id}:${t.name}`]
        return usage && usage.active_connections > 0
      }).length,
      count: tabTunnels.length,
    }
  }, [tunnels, activeTab, usageMap])

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
        para: tunnel.para,
      })
      toast(`隧道已${tunnel.enabled ? '禁用' : '启用'}`, 'success')
      fetchData()
    } catch (err: unknown) {
      toast((err as Error).message || '切换状态失败', 'error')
    }
  }

  const typeBadgeVariant = (type: string): 'info' | 'purple' | 'warning' | 'cyan' | 'pink' => {
    if (type === 'http' || type === 'https') return 'info'
    if (type === 'tcp') return 'purple'
    if (type === 'udp') return 'warning'
    if (type === 'ser2mq') return 'cyan'
    if (type === 'vpn-manager') return 'pink'
    if (type === 'ser2tcp') return 'cyan'
    if (type === 'ser2udp') return 'purple'
    return 'warning'
  }

  const typeLabel = (type: string): string => {
    if (type === 'ser2mq') return 'Ser2MQ'
    if (type === 'vpn-manager') return 'VPN'
    if (type === 'ser2tcp') return 'Ser2TCP'
    if (type === 'ser2udp') return 'Ser2UDP'
    return type.toUpperCase()
  }

  const targetDisplay = (tunnel: Tunnel): string => {
    if (tunnel.type === 'ser2mq') return tunnel.para?.broker || tunnel.target
    if (tunnel.type === 'vpn-manager') return tunnel.para?.binary?.name || tunnel.target
    if (tunnel.type === 'ser2tcp' || tunnel.type === 'ser2udp') {
      const mode = tunnel.para?.mode || 'server'
      const addr = tunnel.para?.address || '-'
      return `${mode} ${addr}`
    }
    return tunnel.target
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
        {/* Tab navigation */}
        <div className="flex items-center gap-1 px-6 pt-4 pb-2">
          {tabs.map(tab => (
            <button
              key={tab.key}
              onClick={() => { setActiveTab(tab.key); setFilterStatus('all'); setSearchKeyword('') }}
              className={`px-3 py-1.5 text-sm rounded-lg transition-colors ${
                activeTab === tab.key
                  ? 'bg-primary text-white'
                  : 'text-gray-600 hover:bg-gray-100'
              }`}
            >
              {tab.label}
              <span className={`ml-1.5 text-xs ${activeTab === tab.key ? 'text-white/70' : 'text-gray-400'}`}>
                {allTabCounts[tab.key]}
              </span>
            </button>
          ))}
        </div>

        {/* Stats cards */}
        <div className="grid grid-cols-2 lg:grid-cols-4 gap-4 px-6 py-3">
          <StatCard
            icon={Network}
            iconBg="bg-blue-100"
            iconColor="text-blue-600"
            value={String(tabStats.total)}
            label="隧道总数"
          />
          <StatCard
            icon={Zap}
            iconBg="bg-emerald-100"
            iconColor="text-emerald-600"
            value={String(tabStats.enabled)}
            label="启用中"
          />
          <StatCard
            icon={Activity}
            iconBg="bg-amber-100"
            iconColor="text-amber-600"
            value={String(tabStats.active)}
            label="活跃中"
          />
          <StatCard
            icon={Globe}
            iconBg="bg-purple-100"
            iconColor="text-purple-600"
            value={String(tabStats.count - tabStats.enabled)}
            label="未启用"
          />
        </div>

        {/* Filter bar */}
        <div className="flex items-center justify-between px-6 py-2 gap-4 border-b border-gray-200">
          <div className="flex items-center gap-1">
            {filterOptions.map(f => (
              <button
                key={f.key}
                onClick={() => setFilterStatus(f.key)}
                className={`px-2.5 py-1 text-xs rounded-full transition-colors ${
                  filterStatus === f.key
                    ? 'bg-gray-800 text-white'
                    : 'bg-gray-100 text-gray-600 hover:bg-gray-200'
                }`}
              >
                {f.label}
              </button>
            ))}
          </div>
          <div className="relative">
            <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-gray-400" />
            <input
              type="text"
              value={searchKeyword}
              onChange={e => setSearchKeyword(e.target.value)}
              placeholder="搜索名称/类型/目标..."
              className="pl-9 pr-3 py-1.5 text-sm border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-primary/30"
            />
          </div>
        </div>

        {loading ? (
          <Loading />
        ) : filteredTunnels.length === 0 ? (
          <Empty message={tunnels.length === 0 ? '暂无隧道' : '当前筛选条件下没有隧道'} />
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
              {filteredTunnels.map(tunnel => {
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
                        {typeLabel(tunnel.type)}
                      </Badge>
                    </td>
                    <td className="px-6 py-3 text-sm text-gray-600 font-mono">
                      {targetDisplay(tunnel)}
                    </td>
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
