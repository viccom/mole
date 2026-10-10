import { useEffect, useRef, useState } from 'react'
import { api } from '../api/client'
import type { AccessToken } from '../types/api'
import { PageHeader } from '../components/PageHeader'
import { Badge } from '../components/Badge'
import { Loading } from '../components/Loading'
import { Empty } from '../components/Empty'
import { Modal } from '../components/Modal'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { FormField } from '../components/FormField'
import { useToast } from '../hooks/useToast'
import { useRequest } from '../hooks/useRequest'
import { formatTimeAgo } from '../lib/utils'
import { Plus, RefreshCw, Trash2, Copy, Check } from 'lucide-react'

export function AccessTokensPage() {
  const { toast } = useToast()

  // New token display
  const [revealedToken, setRevealedToken] = useState<string | null>(null)
  const [copied, setCopied] = useState(false)
  const hideTimerRef = useRef<ReturnType<typeof setTimeout>>()

  // Create modal
  const [showCreate, setShowCreate] = useState(false)
  const [createName, setCreateName] = useState('')
  const [creating, setCreating] = useState(false)

  // Rotate confirm
  const [rotateTarget, setRotateTarget] = useState<AccessToken | null>(null)
  const [rotating, setRotating] = useState(false)

  // Delete confirm
  const [deleteTarget, setDeleteTarget] = useState<AccessToken | null>(null)
  const [deleting, setDeleting] = useState(false)
  const { data, loading, run: fetchTokens } = useRequest(
    () => api.getAccessTokens(),
    {
      onError: () => {
        toast('获取 Token 列表失败', 'error')
      },
    },
  )
  const tokens = data?.items || []

  // Auto-hide revealed token after 10 seconds
  useEffect(() => {
    if (revealedToken) {
      if (hideTimerRef.current) clearTimeout(hideTimerRef.current)
      hideTimerRef.current = setTimeout(() => setRevealedToken(null), 10000)
    }
    return () => { if (hideTimerRef.current) clearTimeout(hideTimerRef.current) }
  }, [revealedToken])

  const showNewToken = (token: string) => {
    setRevealedToken(token)
    setCopied(false)
  }

  const handleCopy = () => {
    if (!revealedToken) return
    navigator.clipboard.writeText(revealedToken).then(() => {
      setCopied(true)
      toast('已复制到剪贴板', 'success')
    })
  }

  const handleCreate = async () => {
    if (!createName.trim()) return
    setCreating(true)
    try {
      const res = await api.createAccessToken(createName.trim())
      showNewToken(res.token)
      setShowCreate(false)
      setCreateName('')
      fetchTokens()
      toast('Token 创建成功', 'success')
    } catch (err: unknown) {
      // 透传后端原因（如名称重复），不能只报 generic 文案
      toast((err as Error).message || '创建失败', 'error')
    } finally {
      setCreating(false)
    }
  }

  const handleRotate = async () => {
    if (!rotateTarget) return
    setRotating(true)
    try {
      const res = await api.rotateAccessToken(rotateTarget.id)
      showNewToken(res.token)
      setRotateTarget(null)
      fetchTokens()
      toast('Token 已轮换', 'success')
    } catch (err: unknown) {
      toast((err as Error).message || '轮换失败', 'error')
    } finally {
      setRotating(false)
    }
  }

  const handleDelete = async () => {
    if (!deleteTarget) return
    setDeleting(true)
    try {
      await api.deleteAccessToken(deleteTarget.id)
      setDeleteTarget(null)
      fetchTokens()
      toast('Token 已删除', 'success')
    } catch (err: unknown) {
      toast((err as Error).message || '删除失败', 'error')
    } finally {
      setDeleting(false)
    }
  }

  if (loading) return <Loading />

  return (
    <>
      <PageHeader
        title="Access Tokens"
        actions={
          <button
            onClick={() => setShowCreate(true)}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 text-sm bg-primary text-white rounded-lg hover:bg-primary-dark"
          >
            <Plus className="w-4 h-4" /> 创建 Token
          </button>
        }
      />
      <div className="flex-1 overflow-y-auto p-6 space-y-6">
        {/* Revealed token alert */}
        {revealedToken && (
          <div className="bg-amber-50 border border-amber-200 rounded-lg p-4 flex items-center gap-3">
            <div className="flex-1 min-w-0">
              <p className="text-sm font-medium text-amber-800 mb-1">新 Token 已生成，请立即复制保存</p>
              <code className="block text-sm text-amber-900 font-mono bg-amber-100 px-3 py-2 rounded break-all select-all">
                {revealedToken}
              </code>
            </div>
            <button
              onClick={handleCopy}
              className="flex-shrink-0 inline-flex items-center gap-1 px-3 py-1.5 text-sm bg-amber-600 text-white rounded-lg hover:bg-amber-700"
            >
              {copied ? <Check className="w-4 h-4" /> : <Copy className="w-4 h-4" />}
              {copied ? '已复制' : '复制'}
            </button>
          </div>
        )}

        {/* Token list */}
        <div className="bg-white rounded-lg shadow-sm">
          <div className="px-5 py-4 border-b border-gray-200">
            <h3 className="font-semibold text-gray-900">Token 列表</h3>
          </div>
          {tokens.length === 0 ? (
            <Empty message="暂无 Token" />
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full">
                <thead>
                  <tr className="bg-gray-50">
                    <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">名称</th>
                    <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">前缀</th>
                    <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">状态</th>
                    <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">最后使用</th>
                    <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">创建时间</th>
                    <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">操作</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-100">
                  {tokens.map(t => (
                    <tr key={t.id} className="hover:bg-gray-50/50">
                      <td className="px-4 py-3 font-medium">{t.name}</td>
                      <td className="px-4 py-3">
                        <code className="text-xs bg-gray-100 px-1.5 py-0.5 rounded">{t.token_prefix}...</code>
                      </td>
                      <td className="px-4 py-3">
                        <Badge variant={t.status === 'active' ? 'success' : 'error'}>
                          {t.status === 'active' ? '启用' : '禁用'}
                        </Badge>
                      </td>
                      <td className="px-4 py-3 text-gray-500 text-sm">{formatTimeAgo(t.last_used_at)}</td>
                      <td className="px-4 py-3 text-gray-500 text-sm">{formatTimeAgo(t.created_at)}</td>
                      <td className="px-4 py-3">
                        <div className="flex items-center gap-1">
                          <button
                            onClick={() => setRotateTarget(t)}
                            className="p-1.5 text-gray-400 hover:text-blue-600 rounded hover:bg-blue-50"
                            title="轮换"
                          >
                            <RefreshCw className="w-4 h-4" />
                          </button>
                          <button
                            onClick={() => setDeleteTarget(t)}
                            className="p-1.5 text-gray-400 hover:text-red-600 rounded hover:bg-red-50"
                            title="删除"
                          >
                            <Trash2 className="w-4 h-4" />
                          </button>
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </div>

      {/* Create Modal */}
      {showCreate && (
        <Modal title="创建 Token" onClose={() => { setShowCreate(false); setCreateName('') }}>
          <div className="space-y-4">
            <FormField label="名称">
              <input
                type="text"
                value={createName}
                onChange={e => setCreateName(e.target.value)}
                placeholder="输入 Token 名称"
                className="w-full px-3 py-2 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-primary/20 focus:border-primary"
                autoFocus
                onKeyDown={e => { if (e.key === 'Enter') handleCreate() }}
              />
            </FormField>
            <div className="flex justify-end gap-2">
              <button
                onClick={() => { setShowCreate(false); setCreateName('') }}
                className="px-4 py-2 text-sm border border-gray-300 rounded-lg hover:bg-gray-50"
              >
                取消
              </button>
              <button
                onClick={handleCreate}
                disabled={!createName.trim() || creating}
                className="px-4 py-2 text-sm bg-primary text-white rounded-lg hover:bg-primary-dark disabled:opacity-50"
              >
                {creating ? '创建中...' : '创建'}
              </button>
            </div>
          </div>
        </Modal>
      )}

      {/* Rotate Confirm */}
      {rotateTarget && (
        <ConfirmDialog
          title="轮换 Token"
          message={`确定要轮换「${rotateTarget.name}」的 Token 吗？旧 Token 将立即失效，请确保已更新所有使用该 Token 的客户端配置。`}
          confirmText={rotating ? '轮换中...' : '确认轮换'}
          onConfirm={handleRotate}
          onCancel={() => setRotateTarget(null)}
          danger
        />
      )}

      {/* Delete Confirm */}
      {deleteTarget && (
        <ConfirmDialog
          title="删除 Token"
          message={`确定要删除「${deleteTarget.name}」吗？此操作不可恢复。`}
          confirmText={deleting ? '删除中...' : '确认删除'}
          onConfirm={handleDelete}
          onCancel={() => setDeleteTarget(null)}
          danger
        />
      )}
    </>
  )
}
