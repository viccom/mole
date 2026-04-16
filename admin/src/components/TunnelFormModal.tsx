import { useMemo, useState } from 'react'
import { api } from '../api/client'
import type { Node, Tunnel } from '../types/api'
import { Modal } from './Modal'
import { FormField } from './FormField'
import { useToast } from '../hooks/useToast'
import { useRequest } from '../hooks/useRequest'

interface TunnelFormModalProps {
  tunnel?: Tunnel
  presetNodeId?: string
  onClose: () => void
  onSuccess: () => void
}

type TunnelType = 'http' | 'https' | 'tcp' | 'udp'

export function TunnelFormModal({ tunnel, presetNodeId, onClose, onSuccess }: TunnelFormModalProps) {
  const { toast } = useToast()
  const isEdit = !!tunnel

  const [name, setName] = useState(tunnel?.name || '')
  const [type, setType] = useState<TunnelType>(tunnel?.type || 'http')
  const [target, setTarget] = useState(tunnel?.target || '')
  const [domain, setDomain] = useState(tunnel?.domain || '')
  const [listenPort, setListenPort] = useState(tunnel?.listen_port?.toString() || '')
  const [nodeId, setNodeId] = useState(presetNodeId || tunnel?.node_id || '')
  const [enabled, setEnabled] = useState(tunnel?.enabled !== false)
  const [submitting, setSubmitting] = useState(false)

  const { data: nodesResponse, loading: nodesLoading } = useRequest(
    () => api.getNodes(),
    { onError: () => {} },
  )
  const onlineNodes = useMemo(
    () => (nodesResponse?.items || []).filter((node: Node) => node.status === 'online'),
    [nodesResponse],
  )

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()

    if (!name.trim()) {
      toast('请输入隧道名称', 'error')
      return
    }
    if (!target.trim()) {
      toast('请输入目标地址', 'error')
      return
    }
    if (!isEdit && !nodeId) {
      toast('请选择节点', 'error')
      return
    }

    try {
      setSubmitting(true)
      const payload = {
        name: name.trim(),
        type,
        target: target.trim(),
        domain: (type === 'http' || type === 'https') && domain.trim() ? domain.trim() : undefined,
        listen_port: (type === 'tcp' || type === 'udp') && listenPort ? Number(listenPort) : undefined,
        enabled,
        node_id: nodeId,
      }
      if (isEdit) {
        await api.updateTunnel(payload)
      } else {
        await api.createTunnel(payload)
      }
      toast(isEdit ? '隧道已更新' : '隧道已创建', 'success')
      onSuccess()
    } catch (err: unknown) {
      toast((err as Error).message || '操作失败', 'error')
    } finally {
      setSubmitting(false)
    }
  }

  const inputClass = 'w-full px-3 py-2 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-primary/30 focus:border-primary'
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
          {isEdit && (
            <p className="mt-1 text-xs text-gray-500">当前后端以同节点下的隧道名称作为更新定位标识，编辑时不可修改名称。</p>
          )}
        </FormField>

        <FormField label="类型">
          <select
            value={type}
            onChange={e => setType(e.target.value as TunnelType)}
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
              {onlineNodes.map(node => (
                <option key={node.id} value={node.id}>
                  {node.name} ({node.id.slice(0, 8)})
                </option>
              ))}
            </select>
            {!nodesLoading && onlineNodes.length === 0 && (
              <p className="mt-1 text-xs text-amber-600">暂无在线节点</p>
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
