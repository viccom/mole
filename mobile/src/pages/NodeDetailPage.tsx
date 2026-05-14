import { useState, useEffect, useRef, type FormEvent } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { api, type Node, type Tunnel } from '../api/client'
import { ArrowLeft, Circle, Pencil, Trash2, Copy, RefreshCw, Plus, X } from 'lucide-react'
import { typeIcons } from '../lib/constants'

function getGatewayBase(): string {
  const parts = window.location.hostname.split('.')
  return parts.length > 2 ? parts.slice(1).join('.') : window.location.hostname
}

let cachedDefaultDomain: string | undefined

async function fetchDefaultDomain() {
  try {
    const res = await fetch('/api/v1/version')
    const data = await res.json()
    cachedDefaultDomain = data?.data?.default_domain || data?.default_domain || ''
  } catch { /* ignore */ }
}

function tunnelAccessUrl(t: Tunnel, nodeId: string): string {
  const dd = cachedDefaultDomain
  if (t.type === 'http' || t.type === 'https') {
    if (t.domain) return `${t.type}://${t.domain}`
    const base = dd || getGatewayBase()
    return `${t.type}://${t.name}-${nodeId}.${base}`
  }
  if (t.listen_port) {
    const host = dd || window.location.hostname
    return `${t.type}://${host}:${t.listen_port}`
  }
  return '-'
}

export function NodeDetailPage() {
  const { id } = useParams<{ id: string }>()
  const [node, setNode] = useState<Node | null>(null)
  const [loading, setLoading] = useState(true)
  const [actionTarget, setActionTarget] = useState<Tunnel | null>(null)
  const [showForm, setShowForm] = useState(false)
  const [busy, setBusy] = useState(false)
  const navigate = useNavigate()

  const refresh = () => {
    if (!id) return
    setLoading(true)
    api.getNode(id).then(n => setNode(n)).catch(() => setNode(null)).finally(() => setLoading(false))
  }

  useEffect(() => { fetchDefaultDomain(); refresh() }, [id])

  const handleToggle = async (t: Tunnel) => {
    if (!node) return
    const newEnabled = !(t.enabled == null || t.enabled === true)
    try {
      await api.updateTunnel({
        name: t.name, type: t.type, target: t.target,
        domain: t.domain || undefined, listen_port: t.listen_port || undefined,
        enabled: newEnabled, node_id: node.id, para: t.para,
      })
      refresh()
    } catch (err) {
      alert(err instanceof Error ? err.message : '操作失败')
    }
  }

  const handleDelete = async (t: Tunnel) => {
    if (!node || !confirm(`确定删除隧道「${t.name}」？`)) return
    try {
      await api.deleteTunnel(node.id, t.name)
      setActionTarget(null)
      refresh()
    } catch (err) {
      alert(err instanceof Error ? err.message : '删除失败')
    }
  }

  const handleCopyUrl = (t: Tunnel) => {
    const url = node ? tunnelAccessUrl(t, node.id) : ''
    if (url && url !== '-') {
      navigator.clipboard.writeText(url)
      alert('已复制: ' + url)
    } else {
      alert('无访问 URL')
    }
    setActionTarget(null)
  }

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
          <div className="flex items-center justify-between mb-3">
            <h2 className="text-sm font-medium text-gray-500">隧道 ({node.tunnels?.length || 0})</h2>
            <div className="flex items-center gap-1">
              <button onClick={refresh} className="text-blue-600 p-1 -m-1" disabled={loading}>
                <RefreshCw className={`w-4 h-4 ${loading ? 'animate-spin' : ''}`} />
              </button>
              {node.status === 'online' && (
                <button onClick={() => setShowForm(true)} className="text-blue-600 p-1 -m-1">
                  <Plus className="w-4 h-4" />
                </button>
              )}
            </div>
          </div>
          {(!node.tunnels || node.tunnels.length === 0) && (
            <p className="text-sm text-gray-400 text-center py-4">暂无隧道</p>
          )}
          <div className="space-y-3">
            {node.tunnels?.map(t => {
              const enabled = t.enabled == null || t.enabled === true
              return (
                <TunnelCard key={t.name} tunnel={t} enabled={enabled}
                  nodeId={node.id}
                  onLongPress={() => setActionTarget(t)}
                  onToggle={() => handleToggle(t)}
                />
              )
            })}
          </div>
        </div>
      </div>

      {actionTarget && (
        <div className="fixed inset-0 z-[60] bg-black/40 flex items-end" onClick={() => setActionTarget(null)}>
          <div className="bg-white w-full rounded-t-2xl p-6 pb-24" onClick={e => e.stopPropagation()}>
            <div className="text-center mb-4">
              <div className="text-base font-semibold text-gray-800">{actionTarget.name}</div>
              <div className="text-sm text-gray-400">{actionTarget.type} · {actionTarget.target}</div>
            </div>
            <div className="space-y-3">
              <button onClick={() => { setActionTarget(null); navigate(`/tunnels?edit=${actionTarget.name}&node=${node.id}`) }}
                className="w-full flex items-center justify-center gap-3 py-3.5 bg-blue-50 text-blue-600 rounded-xl text-base font-medium active:bg-blue-100">
                <Pencil className="w-5 h-5" /> 编辑隧道
              </button>
              <button onClick={() => handleDelete(actionTarget)}
                className="w-full flex items-center justify-center gap-3 py-3.5 bg-red-50 text-red-500 rounded-xl text-base font-medium active:bg-red-100">
                <Trash2 className="w-5 h-5" /> 删除隧道
              </button>
              <button onClick={() => handleCopyUrl(actionTarget)}
                className="w-full flex items-center justify-center gap-3 py-3.5 bg-emerald-50 text-emerald-600 rounded-xl text-base font-medium active:bg-emerald-100">
                <Copy className="w-5 h-5" /> 复制访问URL
              </button>
            </div>
          </div>
        </div>
      )}

      {showForm && node && (
        <TunnelCreateForm
          nodeId={node.id}
          onClose={() => setShowForm(false)}
          onDone={() => { setShowForm(false); refresh() }}
          busy={busy} setBusy={setBusy}
        />
      )}
    </div>
  )
}

const TUNNEL_TYPES = ['http', 'tcp', 'udp']
const TYPE_LABELS: Record<string, string> = { http: 'HTTP', tcp: 'TCP', udp: 'UDP' }
const TYPE_STYLES: Record<string, React.CSSProperties> = {
  http: { backgroundColor: '#3b82f6', color: '#fff' },
  tcp: { backgroundColor: '#10b981', color: '#fff' },
  udp: { backgroundColor: '#f59e0b', color: '#fff' },
}

function TunnelCreateForm({ nodeId, onClose, onDone, busy, setBusy }: {
  nodeId: string
  onClose: () => void
  onDone: () => void
  busy: boolean
  setBusy: (b: boolean) => void
}) {
  const [name, setName] = useState('')
  const [type, setType] = useState('http')
  const [target, setTarget] = useState('')
  const [domain, setDomain] = useState('')
  const [listenPort, setListenPort] = useState('')

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault()
    if (!name || !target) { alert('请填写名称和目标地址'); return }
    setBusy(true)
    try {
      await api.createTunnel({
        name, type, target,
        domain: type === 'http' ? domain || undefined : undefined,
        listen_port: (type === 'tcp' || type === 'udp') ? (listenPort ? Number(listenPort) : undefined) : undefined,
        enabled: true,
        node_id: nodeId,
      })
      onDone()
    } catch (err) {
      alert(err instanceof Error ? err.message : '创建失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="fixed inset-0 z-[60] bg-black/40 flex items-end" onClick={onClose}>
      <div className="bg-white w-full rounded-t-2xl p-6 pb-24 max-h-[85vh] overflow-y-auto" onClick={e => e.stopPropagation()}>
        <div className="flex items-center justify-between mb-5">
          <h2 className="text-lg font-semibold">新增隧道</h2>
          <button onClick={onClose}><X className="w-5 h-5 text-gray-400" /></button>
        </div>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className="block text-sm text-gray-600 mb-1">类型</label>
            <div className="flex gap-2">
              {TUNNEL_TYPES.map(t => (
                <button key={t} type="button" onClick={() => setType(t)}
                  style={type === t ? TYPE_STYLES[t] : undefined}
                  className={`flex-1 py-2 rounded-lg text-sm font-medium transition-colors ${type !== t ? 'bg-gray-100 text-gray-500' : ''}`}>
                  {TYPE_LABELS[t]}
                </button>
              ))}
            </div>
          </div>
          <div>
            <label className="block text-sm text-gray-600 mb-1">名称</label>
            <input value={name} onChange={e => setName(e.target.value)}
              className="w-full px-3 py-2.5 border border-gray-200 rounded-lg text-sm" placeholder="隧道名称" autoFocus />
          </div>
          <div>
            <label className="block text-sm text-gray-600 mb-1">目标地址</label>
            <input value={target} onChange={e => setTarget(e.target.value)}
              className="w-full px-3 py-2.5 border border-gray-200 rounded-lg text-sm" placeholder="127.0.0.1:8080" />
          </div>
          {type === 'http' && (
            <div>
              <label className="block text-sm text-gray-600 mb-1">域名（可选）</label>
              <input value={domain} onChange={e => setDomain(e.target.value)}
                className="w-full px-3 py-2.5 border border-gray-200 rounded-lg text-sm" placeholder="app.example.com" />
            </div>
          )}
          {(type === 'tcp' || type === 'udp') && (
            <div>
              <label className="block text-sm text-gray-600 mb-1">监听端口（可选）</label>
              <input value={listenPort} onChange={e => setListenPort(e.target.value)} type="number"
                className="w-full px-3 py-2.5 border border-gray-200 rounded-lg text-sm" placeholder="8080" />
            </div>
          )}
          <button type="submit" disabled={busy}
            style={{ backgroundColor: '#2563eb', color: '#fff' }} className="w-full py-2.5 rounded-lg text-sm font-medium disabled:opacity-50">
            {busy ? '创建中...' : '创建隧道'}
          </button>
        </form>
      </div>
    </div>
  )
}

function TunnelCard({ tunnel, enabled, nodeId, onLongPress, onToggle }: {
  tunnel: Tunnel
  enabled: boolean
  nodeId: string
  onLongPress: () => void
  onToggle: () => void
}) {
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  const startPress = () => {
    timerRef.current = setTimeout(() => { timerRef.current = null; onLongPress() }, 500)
  }

  const cancelPress = () => {
    if (timerRef.current) { clearTimeout(timerRef.current); timerRef.current = null }
  }

  return (
    <div className="bg-white rounded-xl p-3.5 shadow-sm border border-gray-100 select-none"
      onTouchStart={startPress} onTouchEnd={cancelPress} onTouchCancel={cancelPress}
      onContextMenu={e => { e.preventDefault(); onLongPress() }}>
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2.5 min-w-0 flex-1">
          <span className="text-lg shrink-0">{typeIcons[tunnel.type] || '📡'}</span>
          <div className="min-w-0">
            <div className="font-medium text-gray-800 truncate">{tunnel.name}</div>
            <div className="text-xs text-gray-400 mt-0.5">{tunnel.type} → {tunnel.target}</div>
            <div className={`text-xs mt-0.5 truncate ${tunnelAccessUrl(tunnel, nodeId) === '-' ? 'text-gray-400' : 'text-blue-500'}`}>
              {tunnelAccessUrl(tunnel, nodeId)}
            </div>
          </div>
        </div>
        <button onClick={e => { e.stopPropagation(); cancelPress(); onToggle() }}
          className={`shrink-0 w-10 h-6 rounded-full transition-colors relative ${enabled ? 'bg-green-500' : 'bg-gray-300'}`}>
          <span className={`absolute top-0.5 w-5 h-5 bg-white rounded-full shadow transition-transform ${enabled ? 'left-[18px]' : 'left-0.5'}`} />
        </button>
      </div>
    </div>
  )
}
