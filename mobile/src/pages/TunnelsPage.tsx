import { useState, useEffect, useRef, type FormEvent } from 'react'
import { api, type Node, type Tunnel } from '../api/client'
import { Circle, Plus, RefreshCw, X, Pencil, Trash2 } from 'lucide-react'
import { typeIcons } from '../lib/constants'

interface FlatTunnel extends Tunnel {
  nodeId: string
  nodeName: string
  nodeStatus: string
}

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

function tunnelAccessUrl(t: FlatTunnel): string {
  const dd = cachedDefaultDomain
  if (t.type === 'http' || t.type === 'https') {
    if (t.domain) return `${t.type}://${t.domain}`
    const base = dd || getGatewayBase()
    return `${t.type}://${t.name}-${t.nodeId}.${base}`
  }
  if (t.listen_port) {
    const host = dd || window.location.hostname
    return `${t.type}://${host}:${t.listen_port}`
  }
  return '-'
}

const TUNNEL_TYPES = ['http', 'tcp', 'udp']
const TYPE_LABELS: Record<string, string> = { http: 'HTTP', tcp: 'TCP', udp: 'UDP' }
const TYPE_COLORS: Record<string, string> = {
  http: 'bg-blue-500 text-white',
  tcp: 'bg-emerald-500 text-white',
  udp: 'bg-amber-500 text-white',
}

export function TunnelsPage() {
  const [nodes, setNodes] = useState<Node[]>([])
  const [loading, setLoading] = useState(true)
  const [showForm, setShowForm] = useState(false)
  const [editTarget, setEditTarget] = useState<FlatTunnel | null>(null)
  const [busy, setBusy] = useState(false)
  const [actionTarget, setActionTarget] = useState<FlatTunnel | null>(null)

  const refresh = () => {
    setLoading(true)
    api.getNodes().then(res => setNodes(res.items || [])).finally(() => setLoading(false))
  }

  useEffect(() => { fetchDefaultDomain(); refresh() }, [])

  const allTunnels: FlatTunnel[] = nodes.flatMap(node =>
    (node.tunnels || []).map(t => ({
      ...t, nodeId: node.id, nodeName: node.name, nodeStatus: node.status,
    }))
  )

  const onlineNodes = nodes.filter(n => n.status === 'online')

  const handleToggle = async (t: FlatTunnel) => {
    const newEnabled = !(t.enabled == null || t.enabled === true)
    try {
      await api.updateTunnel({
        name: t.name, type: t.type, target: t.target,
        domain: t.domain || undefined, listen_port: t.listen_port || undefined,
        enabled: newEnabled, node_id: t.nodeId, para: t.para,
      })
      refresh()
    } catch (err) {
      alert(err instanceof Error ? err.message : '操作失败')
    }
  }

  const handleDelete = async (t: FlatTunnel) => {
    if (!confirm(`确定删除隧道「${t.name}」？`)) return
    try {
      await api.deleteTunnel(t.nodeId, t.name)
      refresh()
    } catch (err) {
      alert(err instanceof Error ? err.message : '删除失败')
    }
  }

  return (
    <div className="min-h-screen bg-gray-50 pb-16">
      <header className="sticky top-0 z-40 bg-white border-b border-gray-100 px-4 py-3 flex items-center justify-between">
        <h1 className="text-lg font-semibold">隧道管理 <span className="text-sm font-normal text-gray-400">{allTunnels.length} 条</span></h1>
        <div className="flex items-center gap-4">
          <button onClick={refresh} className="text-blue-600 p-1.5 -m-1.5" disabled={loading}>
            <RefreshCw className={`w-6 h-6 ${loading ? 'animate-spin' : ''}`} />
          </button>
          {onlineNodes.length > 0 && (
            <button onClick={() => { setEditTarget(null); setShowForm(true) }} className="text-blue-600 p-1.5 -m-1.5">
              <Plus className="w-6 h-6" />
            </button>
          )}
        </div>
      </header>

      <div className="p-4 space-y-2">
        {loading && <div className="text-center text-gray-400 py-20">加载中...</div>}
        {!loading && allTunnels.length === 0 && <div className="text-center text-gray-400 py-20">暂无隧道</div>}

        {nodes.map(node => {
          const tunnels = allTunnels.filter(t => t.nodeId === node.id)
          if (tunnels.length === 0) return null
          return (
          <div key={node.id} className="mb-4">
            <div className="flex items-center gap-2 mb-2 px-1">
              {node.status === 'online' ? (
                <Circle className="w-2.5 h-2.5 text-green-500 fill-green-500" />
              ) : (
                <Circle className="w-2.5 h-2.5 text-gray-300" />
              )}
              <span className="text-sm font-medium text-gray-600">{node.name}</span>
              <span className="text-xs text-gray-300">{tunnels.length} 条</span>
            </div>
            <div className="space-y-3">
              {tunnels.map(ft => {
                const enabled = ft.enabled == null || ft.enabled === true
                return (
                  <TunnelCard key={ft.name} tunnel={ft} enabled={enabled}
                    onLongPress={() => setActionTarget(ft)}
                    onToggle={() => handleToggle(ft)}
                  />
                )
              })}
            </div>
          </div>
          )
        })}
      </div>

      {actionTarget && (
        <ActionSheet
          tunnel={actionTarget}
          onClose={() => setActionTarget(null)}
          onEdit={() => { setActionTarget(null); setEditTarget(actionTarget); setShowForm(true) }}
          
          onDelete={() => { setActionTarget(null); handleDelete(actionTarget) }}
        />
      )}

      {showForm && (
        <TunnelForm
          mode={editTarget ? 'edit' : 'create'}
          tunnel={editTarget}
          nodes={onlineNodes}
          onClose={() => { setShowForm(false); setEditTarget(null) }}
          onDone={() => { setShowForm(false); setEditTarget(null); refresh() }}
          busy={busy} setBusy={setBusy}
        />
      )}
    </div>
  )
}

function TunnelCard({ tunnel, enabled, onLongPress, onToggle }: {
  tunnel: FlatTunnel
  enabled: boolean
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
            <div className={`text-xs mt-0.5 truncate ${tunnelAccessUrl(tunnel) === '-' ? 'text-gray-400' : 'text-blue-500'}`}>
              {tunnelAccessUrl(tunnel)}
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

function ActionSheet({ tunnel, onClose, onEdit, onDelete }: {
  tunnel: FlatTunnel
  onClose: () => void
  onEdit: () => void
  onDelete: () => void
}) {
  return (
    <div className="fixed inset-0 z-[60] bg-black/40 flex items-end" onClick={onClose}>
      <div className="bg-white w-full rounded-t-2xl p-6 pb-24" onClick={e => e.stopPropagation()}>
        <div className="text-center mb-4">
          <div className="text-base font-semibold text-gray-800">{tunnel.name}</div>
          <div className="text-sm text-gray-400">{tunnel.type} · {tunnel.nodeName}</div>
        </div>
        <div className="space-y-3">
          <button onClick={onEdit}
            className="w-full flex items-center justify-center gap-3 py-3.5 bg-blue-50 text-blue-600 rounded-xl text-base font-medium active:bg-blue-100">
            <Pencil className="w-5 h-5" /> 编辑隧道
          </button>
          <button onClick={onDelete}
            className="w-full flex items-center justify-center gap-3 py-3.5 bg-red-50 text-red-500 rounded-xl text-base font-medium active:bg-red-100">
            <Trash2 className="w-5 h-5" /> 删除隧道
          </button>
        </div>
      </div>
    </div>
  )
}

function TunnelForm({ mode, tunnel, nodes, onClose, onDone, busy, setBusy }: {
  mode: 'create' | 'edit'
  tunnel: FlatTunnel | null
  nodes: Node[]
  onClose: () => void
  onDone: () => void
  busy: boolean
  setBusy: (b: boolean) => void
}) {
  const [nodeId, setNodeId] = useState(tunnel?.nodeId || nodes[0]?.id || '')
  const [name, setName] = useState(tunnel?.name || '')
  const [type, setType] = useState(tunnel?.type || 'http')
  const [target, setTarget] = useState(tunnel?.target || '')
  const [domain, setDomain] = useState(tunnel?.domain || '')
  const [listenPort, setListenPort] = useState(tunnel?.listen_port ? String(tunnel.listen_port) : '')

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault()
    if (!name || !target) { alert('请填写名称和目标地址'); return }
    setBusy(true)
    try {
      const payload = {
        name, type, target,
        domain: type === 'http' ? domain || undefined : undefined,
        listen_port: (type === 'tcp' || type === 'udp') ? (listenPort ? Number(listenPort) : undefined) : undefined,
        enabled: mode === 'edit' ? (tunnel?.enabled == null || tunnel?.enabled === true) : true,
        node_id: nodeId,
        ...(mode === 'edit' && tunnel ? { original_node_id: tunnel.nodeId } : {}),
        para: tunnel?.para,
      }
      if (mode === 'edit') {
        await api.updateTunnel(payload)
      } else {
        await api.createTunnel(payload)
      }
      onDone()
    } catch (err) {
      alert(err instanceof Error ? err.message : (mode === 'edit' ? '更新失败' : '创建失败'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="fixed inset-0 z-[60] bg-black/40 flex items-end" onClick={onClose}>
      <div className="bg-white w-full rounded-t-2xl p-6 pb-24 max-h-[85vh] overflow-y-auto" onClick={e => e.stopPropagation()}>
        <div className="flex items-center justify-between mb-5">
          <h2 className="text-lg font-semibold">{mode === 'edit' ? '编辑隧道' : '新增隧道'}</h2>
          <button onClick={onClose}><X className="w-5 h-5 text-gray-400" /></button>
        </div>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className="block text-sm text-gray-600 mb-1">节点</label>
            <select value={nodeId} onChange={e => setNodeId(e.target.value)}
              className="w-full px-3 py-2.5 border border-gray-200 rounded-lg text-sm bg-white">
              {nodes.map(n => <option key={n.id} value={n.id}>{n.name}</option>)}
            </select>
          </div>
          <div>
            <label className="block text-sm text-gray-600 mb-1">名称</label>
            <input value={name} onChange={e => setName(e.target.value)}
              className="w-full px-3 py-2.5 border border-gray-200 rounded-lg text-sm" placeholder="隧道名称" autoFocus={mode !== 'edit'} />
          </div>
          <div>
            <label className="block text-sm text-gray-600 mb-1">类型</label>
            <div className="flex gap-2">
              {TUNNEL_TYPES.map(t => (
                <button key={t} type="button" onClick={() => setType(t)}
                  className={`flex-1 py-2 rounded-lg text-sm font-medium transition-colors ${type === t ? TYPE_COLORS[t] : 'bg-gray-100 text-gray-500'}`}>
                  {TYPE_LABELS[t]}
                </button>
              ))}
            </div>
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
            className="w-full py-2.5 bg-blue-600 text-white rounded-lg text-sm font-medium disabled:opacity-50">
            {busy ? (mode === 'edit' ? '保存中...' : '创建中...') : (mode === 'edit' ? '保存修改' : '创建隧道')}
          </button>
        </form>
      </div>
    </div>
  )
}
