import { api } from '../api/client'
import type { Node, SystemMetrics, VersionInfo } from '../types/api'
import { PageHeader } from '../components/PageHeader'
import { StatCard } from '../components/StatCard'
import { Badge } from '../components/Badge'
import { Loading } from '../components/Loading'
import { Empty } from '../components/Empty'
import { useRequest } from '../hooks/useRequest'
import { Server, Network, Activity, Clock, RefreshCw } from 'lucide-react'
import { formatTimeAgo, formatUptime } from '../lib/utils'

export function DashboardPage() {
  const { data, loading, run: refresh } = useRequest(async () => {
    const [nodesRes, metricsRes, versionRes] = await Promise.all([
      api.getNodes().catch(() => ({ items: [] as Node[], total: 0 })),
      api.getMetrics().catch(() => null as SystemMetrics | null),
      api.getVersion().catch(() => null as VersionInfo | null),
    ])

    return {
      nodes: nodesRes.items || [],
      metrics: metricsRes,
      version: versionRes,
    }
  })

  const nodes = data?.nodes || []
  const metrics = data?.metrics || null
  const version = data?.version || null

  if (loading) return <Loading />

  const onlineCount = nodes.filter(n => n.status === 'online').length
  const totalTunnels = nodes.reduce((sum, n) => sum + n.tunnel_count, 0)

  return (
    <>
      <PageHeader
        title="仪表盘"
        actions={
          <button
            onClick={() => { void refresh().catch(() => {}) }}
            className="flex items-center gap-1.5 px-3 py-1.5 text-sm border border-gray-300 rounded-lg hover:bg-gray-50"
          >
            <RefreshCw className="w-4 h-4" />
            刷新
          </button>
        }
      />
      <div className="flex-1 overflow-y-auto p-6 space-y-6">
        <div className="grid grid-cols-4 gap-4">
          <StatCard icon={Server} iconBg="bg-emerald-100" iconColor="text-emerald-600" value={`${onlineCount}/${nodes.length}`} label="在线节点" />
          <StatCard icon={Network} iconBg="bg-blue-100" iconColor="text-blue-600" value={String(totalTunnels)} label="隧道总数" />
          <StatCard icon={Activity} iconBg="bg-purple-100" iconColor="text-purple-600" value={metrics?.goroutines ? String(metrics.goroutines) : '-'} label="Goroutines" />
          <StatCard icon={Clock} iconBg="bg-amber-100" iconColor="text-amber-600" value={metrics ? formatUptime(metrics.uptime_seconds) : '-'} label="运行时长" />
        </div>

        <div className="bg-white rounded-lg shadow-sm">
          <div className="px-5 py-4 border-b border-gray-200"><h3 className="font-semibold text-gray-900">版本信息</h3></div>
          <div className="p-5">
            <div className="space-y-2">
              <div className="flex justify-between text-sm"><span className="text-gray-500">版本</span><span className="font-mono font-medium">{version ? `${version.version} (${version.git_hash})` : '-'}</span></div>
              <div className="flex justify-between text-sm"><span className="text-gray-500">编译日期</span><span className="font-mono font-medium">{version?.build_date || '-'}</span></div>
              <div className="flex justify-between text-sm"><span className="text-gray-500">默认域名</span><span className="font-mono font-medium">{version?.default_domain || '-'}</span></div>
            </div>
          </div>
        </div>

        <div className="bg-white rounded-lg shadow-sm">
          <div className="px-5 py-4 border-b border-gray-200"><h3 className="font-semibold text-gray-900">系统资源</h3></div>
          <div className="p-5">
            <div className="space-y-2">
              <div className="flex justify-between text-sm"><span className="text-gray-500">内存分配</span><span className="font-mono font-medium">{metrics?.memory_alloc_mb?.toFixed(1) || '-'} MB</span></div>
              <div className="flex justify-between text-sm"><span className="text-gray-500">系统内存</span><span className="font-mono font-medium">{metrics?.memory_sys_mb?.toFixed(1) || '-'} MB</span></div>
              <div className="flex justify-between text-sm"><span className="text-gray-500">CPU 核数</span><span className="font-mono font-medium">{metrics?.cpu_num || '-'}</span></div>
            </div>
          </div>
        </div>

        <div className="bg-white rounded-lg shadow-sm">
          <div className="px-5 py-4 border-b border-gray-200"><h3 className="font-semibold text-gray-900">最近节点</h3></div>
          {nodes.length === 0 ? <Empty message="暂无节点" /> : (
            <div className="overflow-x-auto">
              <table className="w-full">
                <thead><tr className="bg-gray-50">
                  <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">ID</th>
                  <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">名称</th>
                  <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">状态</th>
                  <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">隧道</th>
                  <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">连接时间</th>
                  <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">心跳</th>
                  <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">远程地址</th>
                </tr></thead>
                <tbody className="divide-y divide-gray-100">
                  {nodes.slice(0, 5).map(node => (
                    <tr key={node.id} className="hover:bg-gray-50/50">
                      <td className="px-4 py-3"><code className="text-xs bg-gray-100 px-1.5 py-0.5 rounded">{node.id}</code></td>
                      <td className="px-4 py-3">{node.name}</td>
                      <td className="px-4 py-3"><Badge variant={node.status === 'online' ? 'success' : 'error'}>{node.status === 'online' ? '在线' : '离线'}</Badge></td>
                      <td className="px-4 py-3">{node.tunnel_count}</td>
                      <td className="px-4 py-3 text-gray-500">{formatTimeAgo(node.connected_at)}</td>
                      <td className="px-4 py-3 text-gray-500">{formatTimeAgo(node.last_heartbeat)}</td>
                      <td className="px-4 py-3"><code className="text-xs text-gray-500">{node.remote_addr || '-'}</code></td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </div>
    </>
  )
}
