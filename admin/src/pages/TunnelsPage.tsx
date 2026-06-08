import { useMemo, useState, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { RefreshCw, Plus, Pencil, Trash2, Network, Zap, ZapOff, Activity, Globe, ArrowDown, ArrowUp, Users, Search, Columns3, Filter, Gauge, Terminal } from 'lucide-react'
import { api } from '../api/client'
import type { Tunnel, TunnelStats, TunnelUsageItem, Node } from '../types/api'
import { PageHeader } from '../components/PageHeader'
import { Badge } from '../components/Badge'
import { Loading } from '../components/Loading'
import { Empty } from '../components/Empty'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { StatCard } from '../components/StatCard'
import { TunnelFormModal } from '../components/TunnelFormModal'
import { BatchRateLimitModal } from '../components/BatchRateLimitModal'
import { useToast } from '../hooks/useToast'
import { useRequest } from '../hooks/useRequest'
import { tunnelAccessUrl, formatBytes, formatBandwidth, formatTimeAgo, fetchDefaultDomain } from '../lib/utils'

type TabKey = 'all' | 'web' | 'stream' | 'serial' | 'vpn' | 'webssh'
type FilterKey = 'all' | 'enabled' | 'disabled' | 'active'

const tabFilters: Record<TabKey, (t: Tunnel) => boolean> = {
  all: () => true,
  web: (t: Tunnel) => t.type === 'http' || t.type === 'https',
  stream: (t: Tunnel) => t.type === 'tcp' || t.type === 'udp',
  serial: (t: Tunnel) => ['ser2mq', 'ser2tcp', 'ser2udp'].includes(t.type),
  vpn: (t: Tunnel) => t.type === 'vpn-manager',
  webssh: (t: Tunnel) => t.type === 'webssh',
}

const tabs: { key: TabKey; label: string }[] = [
  { key: 'all', label: '全部' },
  { key: 'web', label: 'Web 隧道' },
  { key: 'stream', label: '透明隧道' },
  { key: 'serial', label: '串口' },
  { key: 'vpn', label: 'VPN' },
  { key: 'webssh', label: '远程终端' },
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

export function tunnelMatchesKeyword(tunnel: Tunnel, keyword: string): boolean {
  const kw = keyword.trim().toLowerCase()
  if (!kw) return true

  return [
    tunnel.name,
    tunnel.type,
    tunnel.target,
    tunnel.domain || '',
    tunnel.para?.broker || '',
    tunnel.para?.binary?.name || '',
    tunnel.para?.address || '',
    tunnel.para?.mode || '',
  ].some(value => value.toLowerCase().includes(kw))
}

export function buildTabStats(
  tunnels: Tunnel[],
  _usageMap: Record<string, TunnelUsageItem>,
): { total: number; enabled: number; active: number; count: number } {
  const enabled = tunnels.filter(t => t.enabled).length
  return {
    total: tunnels.length,
    enabled,
    active: enabled,
    count: tunnels.length,
  }
}

export function TunnelsPage() {
  const { toast } = useToast()
  const navigate = useNavigate()
  const [formModal, setFormModal] = useState<{
    tunnel?: Tunnel
    presetNodeId?: string
    defaultType?: string
  } | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<Tunnel | null>(null)
  const [activeTab, setActiveTab] = useState<TabKey>('all')
  const [filterStatus, setFilterStatus] = useState<FilterKey>('all')
  const [searchKeyword, setSearchKeyword] = useState('')
  const [showDetails, setShowDetails] = useState(false)
  useEffect(() => { fetchDefaultDomain() }, [])
  const [filterNodeId, setFilterNodeId] = useState('')
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set())
  const [batchModal, setBatchModal] = useState(false)

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
    const counts: Record<TabKey, number> = { all: tunnels.length, web: 0, stream: 0, serial: 0, vpn: 0, webssh: 0 }
    for (const t of tunnels) {
      if (t.type === 'http' || t.type === 'https') counts.web++
      if (t.type === 'tcp' || t.type === 'udp') counts.stream++
      if (['ser2mq', 'ser2tcp', 'ser2udp'].includes(t.type)) counts.serial++
      if (t.type === 'vpn-manager') counts.vpn++
      if (t.type === 'webssh') counts.webssh++
    }
    return counts
  }, [tunnels])

  const filteredTunnels = useMemo(() => {
    let result = tunnels
    if (activeTab !== 'all') {
      result = result.filter(tabFilters[activeTab])
    }
    if (filterNodeId) {
      result = result.filter(t => t.node_id === filterNodeId)
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
      result = result.filter(t => tunnelMatchesKeyword(t, searchKeyword))
    }
    return result
  }, [tunnels, activeTab, filterNodeId, filterStatus, searchKeyword, usageMap])

  const tabStats = useMemo(() => {
    const tabTunnels = activeTab === 'all' ? tunnels : tunnels.filter(tabFilters[activeTab])
    return buildTabStats(tabTunnels, usageMap)
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
    if (type === 'webssh') return 'info'
    return 'warning'
  }

  const typeLabel = (type: string): string => {
    if (type === 'ser2mq') return 'Ser2MQ'
    if (type === 'vpn-manager') return 'VPN'
    if (type === 'ser2tcp') return 'Ser2TCP'
    if (type === 'ser2udp') return 'Ser2UDP'
    if (type === 'webssh') return 'WebSSH'
    return type.toUpperCase()
  }

  const targetDisplay = (tunnel: Tunnel): string => {
    return tunnel.target || '-'
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
              onClick={() => setFormModal({ defaultType: activeTab === 'web' ? 'http' : activeTab === 'stream' ? 'tcp' : activeTab === 'serial' ? 'ser2mq' : activeTab === 'vpn' ? 'vpn-manager' : 'http' })}
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
            {selectedIds.size > 0 && (
              <button
                onClick={() => setBatchModal(true)}
                className="flex items-center gap-1 px-2.5 py-1 text-xs rounded-full bg-amber-100 text-amber-700 hover:bg-amber-200 transition-colors"
              >
                <Gauge className="w-3 h-3" />
                批量限速 ({selectedIds.size})
              </button>
            )}
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
          <div className="flex items-center gap-2">
            <select
              value={filterNodeId}
              onChange={e => setFilterNodeId(e.target.value)}
              className="px-2 py-1.5 text-sm border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-primary/30"
            >
              <option value="">全部节点</option>
              {nodes.map(n => (
                <option key={n.id} value={n.id}>
                  {n.name} ({n.id.slice(0, 8)})
                </option>
              ))}
            </select>
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
            <button
              onClick={() => setShowDetails(!showDetails)}
              title={showDetails ? '隐藏详情列' : '显示详情列（流量/连接/最近活动）'}
              className={`p-1.5 rounded transition-colors ${showDetails ? 'bg-gray-800 text-white' : 'text-gray-400 hover:bg-gray-100'}`}
            >
              <Columns3 className="w-4 h-4" />
            </button>
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
                <th className="w-10 px-4 py-3">
                  <input
                    type="checkbox"
                    checked={filteredTunnels.length > 0 && filteredTunnels.every(t => selectedIds.has(`${t.node_id}:${t.name}`))}
                    onChange={() => {
                      const allSelected = filteredTunnels.every(t => selectedIds.has(`${t.node_id}:${t.name}`))
                      setSelectedIds(prev => {
                        const next = new Set(prev)
                        filteredTunnels.forEach(t => {
                          const key = `${t.node_id}:${t.name}`
                          allSelected ? next.delete(key) : next.add(key)
                        })
                        return next
                      })
                    }}
                    className="w-4 h-4 rounded border-gray-300"
                  />
                </th>
                <th className="px-6 py-3">名称</th>
                <th className="px-6 py-3">状态</th>
                <th className="px-6 py-3">类型</th>
                <th className="px-6 py-3">目标</th>
                <th className="px-6 py-3">访问地址</th>
                <th className="px-6 py-3">节点</th>
                {showDetails && <th className="px-6 py-3">流量</th>}
                {showDetails && <th className="px-6 py-3">连接</th>}
                {showDetails && <th className="px-6 py-3">限速</th>}
                {showDetails && <th className="px-6 py-3">最近活动</th>}
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
                    <td className="px-4 py-3">
                      <input
                        type="checkbox"
                        checked={selectedIds.has(`${tunnel.node_id}:${tunnel.name}`)}
                        onChange={() => {
                          const key = `${tunnel.node_id}:${tunnel.name}`
                          setSelectedIds(prev => {
                            const next = new Set(prev)
                            if (next.has(key)) next.delete(key)
                            else next.add(key)
                            return next
                          })
                        }}
                        className="w-4 h-4 rounded border-gray-300"
                      />
                    </td>
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
                    {showDetails && (
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
                    )}
                    {showDetails && (
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
                    )}
                    {showDetails && (
                      <td className="px-6 py-3 text-sm text-gray-500">
                        {tunnel.effective_max_conns ? (
                          <div className="space-y-0.5" title={`有效最大连接数: ${tunnel.effective_max_conns} / 有效最大带宽: ${formatBandwidth(tunnel.effective_max_bandwidth || 0)}`}>
                            <div className="flex items-center gap-1">
                              <Gauge className="w-3 h-3 text-gray-400" />
                              <span>{tunnel.effective_max_conns}</span>
                            </div>
                            {tunnel.effective_max_bandwidth ? (
                              <div className="text-xs text-gray-400">{formatBandwidth(tunnel.effective_max_bandwidth)}</div>
                            ) : null}
                          </div>
                        ) : (
                          <span className="text-gray-400">-</span>
                        )}
                      </td>
                    )}
                    {showDetails && (
                      <td className="px-6 py-3 text-sm text-gray-500">
                        {usage?.last_activity ? formatTimeAgo(usage.last_activity) : '-'}
                      </td>
                    )}
                    <td className="px-6 py-3 text-right">
                      <div className="flex items-center justify-end gap-1">
                        {tunnel.type === 'webssh' && tunnel.enabled && (
                          <button
                            onClick={() => window.open(`/admin/webssh/${encodeURIComponent(tunnel.name)}`, '_blank')}
                            title="打开终端"
                            className="p-1.5 text-gray-400 hover:text-green-600 rounded-md hover:bg-green-50"
                          >
                            <Terminal className="w-4 h-4" />
                          </button>
                        )}
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
          defaultType={formModal.defaultType as any}
          onClose={() => setFormModal(null)}
          onSuccess={() => {
            setFormModal(null)
            fetchData()
          }}
        />
      )}

      {batchModal && (
        <BatchRateLimitModal
          tunnels={tunnels.filter(t => selectedIds.has(`${t.node_id}:${t.name}`))}
          onClose={() => setBatchModal(false)}
          onSuccess={() => {
            setBatchModal(false)
            setSelectedIds(new Set())
            fetchData()
          }}
        />
      )}
    </div>
  )
}
