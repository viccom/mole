import { useState } from 'react'
import { api } from '../api/client'
import { Modal } from './Modal'
import { FormField } from './FormField'
import { useToast } from '../hooks/useToast'
import type { Tunnel } from '../types/api'

interface BatchRateLimitModalProps {
  tunnels: Pick<Tunnel, 'name' | 'node_id' | 'rate_limit' | 'effective_max_conns' | 'effective_max_bandwidth'>[]
  onClose: () => void
  onSuccess: () => void
}

export function BatchRateLimitModal({ tunnels, onClose, onSuccess }: BatchRateLimitModalProps) {
  const { toast } = useToast()
  const [maxConns, setMaxConns] = useState('')
  const [maxBandwidth, setMaxBandwidth] = useState('')
  const [bwUnit, setBwUnit] = useState<'kbps' | 'mbps'>('kbps')
  const [submitting, setSubmitting] = useState(false)

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    const conns = Number(maxConns)
    let bw = Number(maxBandwidth)
    if (bwUnit === 'kbps') bw *= 1024
    else if (bwUnit === 'mbps') bw *= 1024 * 1024

    if (!conns && !bw) {
      toast('请至少填写一项限速值', 'error')
      return
    }

    setSubmitting(true)
    try {
      const items = tunnels.map(t => ({
        node_id: t.node_id!,
        tunnel_name: t.name,
        rate_limit: { max_conns: conns || 0, max_bandwidth: bw || 0 },
      }))
      await api.batchUpdateTunnelRateLimit(items)
      toast(`已更新 ${tunnels.length} 条隧道的限速配置`, 'success')
      onSuccess()
    } catch (err: unknown) {
      toast((err as Error).message || '批量限速失败', 'error')
    } finally {
      setSubmitting(false)
    }
  }

  const inputClass = 'w-full px-3 py-2 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-primary/30'

  return (
    <Modal title="批量设置限速" onClose={onClose} size="lg">
      <form onSubmit={handleSubmit} className="space-y-4">
        <div className="bg-blue-50 rounded-lg p-3 text-sm text-blue-700">
          为选中的 {tunnels.length} 条隧道统一设置限速值。
          填写的值将覆盖所有选中隧道的限速配置，至少填写一项。
        </div>

        <div className="max-h-48 overflow-y-auto space-y-1 bg-gray-50 rounded-lg p-3">
          {tunnels.map(t => (
            <div key={`${t.node_id}/${t.name}`} className="flex items-center gap-2 text-sm px-2 py-0.5">
              <span className="font-mono text-gray-600">{t.node_id?.slice(0, 8)}</span>
              <span className="text-gray-400">/</span>
              <span className="font-medium">{t.name}</span>
              {t.effective_max_conns ? (
                <span className="text-xs text-gray-400 ml-auto">{t.effective_max_conns} 连</span>
              ) : null}
            </div>
          ))}
        </div>

        <div className="grid grid-cols-2 gap-4">
          <FormField label="最大连接数">
            <input
              type="number"
              value={maxConns}
              onChange={e => setMaxConns(e.target.value)}
              placeholder="留空 = 保持不变"
              min="1"
              max="100000"
              className={inputClass}
            />
          </FormField>
          <FormField label="最大带宽">
            <div className="flex gap-2">
              <input
                type="number"
                value={maxBandwidth}
                onChange={e => setMaxBandwidth(e.target.value)}
                placeholder="留空 = 保持不变"
                min="1"
                className={inputClass}
              />
              <select
                value={bwUnit}
                onChange={e => setBwUnit(e.target.value as 'kbps' | 'mbps')}
                className={`${inputClass} w-24 shrink-0`}
              >
                <option value="kbps">KB/s</option>
                <option value="mbps">MB/s</option>
              </select>
            </div>
          </FormField>
        </div>

        <div className="flex justify-end gap-2 pt-2">
          <button
            type="button"
            onClick={onClose}
            disabled={submitting}
            className="px-4 py-2 text-sm border border-gray-300 rounded-lg hover:bg-gray-50"
          >
            取消
          </button>
          <button
            type="submit"
            disabled={submitting}
            className="px-4 py-2 text-sm bg-primary text-white rounded-lg hover:bg-primary-dark disabled:opacity-50"
          >
            {submitting ? '提交中...' : `更新 ${tunnels.length} 条`}
          </button>
        </div>
      </form>
    </Modal>
  )
}
