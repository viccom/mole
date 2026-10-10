import { useState, useEffect, useRef } from 'react'
import { api } from '../api/client'
import type { ServerConfig } from '../types/api'
import { PageHeader } from '../components/PageHeader'
import { Badge } from '../components/Badge'
import { Loading } from '../components/Loading'
import { FormField } from '../components/FormField'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { useToast } from '../hooks/useToast'
import { useRequest } from '../hooks/useRequest'
import { useAuth } from '../hooks/useAuth'
import { Shield, Lock, Eye, EyeOff, RefreshCw } from 'lucide-react'

function PasswordInput({ value, onChange, show, onToggle, placeholder }: {
  value: string; onChange: (v: string) => void; show: boolean; onToggle: () => void; placeholder: string
}) {
  return (
    <div className="relative">
      <input
        type={show ? 'text' : 'password'}
        value={value}
        onChange={e => onChange(e.target.value)}
        placeholder={placeholder}
        className="w-full px-3 py-2 pr-10 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-primary/20 focus:border-primary"
      />
      <button
        type="button"
        onClick={onToggle}
        className="absolute right-2 top-1/2 -translate-y-1/2 text-gray-400 hover:text-gray-600 p-1"
      >
        {show ? <EyeOff className="w-4 h-4" /> : <Eye className="w-4 h-4" />}
      </button>
    </div>
  )
}

export function SettingsPage() {
  const { toast } = useToast()
  // 本页各区块权限相互独立（后端按 resource:action 注册）：
  //   Access Key 区 → accesskey:read 查看状态 / accesskey:admin 增删改
  //   版本更新区   → system:read 查看 / system:admin 执行升级
  //   服务器配置   → system:admin
  // 而"修改密码"是任意登录用户功能，故门控必须下沉到区块级、不可整页收窄
  const { hasPermission } = useAuth()
  const canReadAccessKey = hasPermission('accesskey', 'read') || hasPermission('accesskey', 'admin')
  const canAdminAccessKey = hasPermission('accesskey', 'admin')
  const canReadSystem = hasPermission('system', 'read')
  const canAdminSystem = hasPermission('system', 'admin')
  const canAdminConfig = hasPermission('system', 'admin')

  // Change password
  const [oldPassword, setOldPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [showOldPassword, setShowOldPassword] = useState(false)
  const [showNewPassword, setShowNewPassword] = useState(false)
  const [changingPassword, setChangingPassword] = useState(false)

  // Access key
  const [accessKeyEnabled, setAccessKeyEnabled] = useState(false)
  const [accessKeyValue, setAccessKeyValue] = useState('')
  const [savingKey, setSavingKey] = useState(false)
  const [showDisableConfirm, setShowDisableConfirm] = useState(false)

  // Version update
  const [currentVersion, setCurrentVersion] = useState('-')
  const [updateStatus, setUpdateStatus] = useState<'idle' | 'checking' | 'up-to-date' | 'available' | 'updating' | 'done' | 'error'>('idle')
  const [updateMessage, setUpdateMessage] = useState('')
  const [latestVersion, setLatestVersion] = useState('')
  const progressRef = useRef<ReturnType<typeof setInterval> | null>(null)
  const reloadTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const mountedRef = useRef(true)

  // 升级中切走页面时必须停掉轮询与延迟 reload，否则页面会被强制刷新
  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
      if (progressRef.current) clearInterval(progressRef.current)
      progressRef.current = null
      if (reloadTimerRef.current) clearTimeout(reloadTimerRef.current)
      reloadTimerRef.current = null
    }
  }, [])

  // 升级完成后刷新页面；仅组件仍挂载时才执行
  function scheduleReload(delay: number) {
    if (reloadTimerRef.current) clearTimeout(reloadTimerRef.current)
    reloadTimerRef.current = setTimeout(() => {
      reloadTimerRef.current = null
      if (mountedRef.current) location.reload()
    }, delay)
  }

  const { data, loading } = useRequest(async () => {
    const configRes = await api.getConfig().catch(() => null as ServerConfig | null)
    return { config: configRes }
  })

  // AccessKey 状态单独拉取并依赖权限就绪：useRequest 只在挂载时跑一次，
  // 而权限（roles→permissions）是异步到达的，挂载瞬间 canReadAccessKey=false
  // 会导致请求永不发起、状态恒显示"未启用"。故按权限状态重跑。
  useEffect(() => {
    if (!canReadAccessKey) return
    let alive = true
    api.getAccessKey()
      .then(res => { if (alive) setAccessKeyEnabled(res.enabled) })
      .catch(() => {})
    return () => { alive = false }
  }, [canReadAccessKey])
  const config = data?.config || null

  // Version update logic
  function formatBytes(n: number) {
    if (!n || n <= 0) return ''
    if (n < 1048576) return (n / 1024).toFixed(1) + 'K'
    return (n / 1048576).toFixed(1) + 'M'
  }

  function pollProgress() {
    if (progressRef.current) clearInterval(progressRef.current)
    progressRef.current = setInterval(() => {
      api.updateProgress().then(p => {
        if (p.error) {
          setUpdateStatus('error')
          setUpdateMessage('升级失败: ' + p.error)
          clearInterval(progressRef.current!)
          progressRef.current = null
          return
        }
        if (p.phase === 'done' || p.active === false) {
          setUpdateStatus('done')
          setUpdateMessage('升级完成，等待重启...')
          clearInterval(progressRef.current!)
          progressRef.current = null
          scheduleReload(4000)
          return
        }
        const pct = p.percent || 0
        const d = formatBytes(p.downloaded || 0)
        const t = formatBytes(p.total || 0)
        setUpdateStatus('updating')
        setUpdateMessage(`${p.phase || ''} ${pct}%${d ? ' (' + d + '/' + t + ')' : ''}`)
      }).catch(() => {
        clearInterval(progressRef.current!)
        progressRef.current = null
        setUpdateStatus('done')
        setUpdateMessage('重启中...')
        scheduleReload(3000)
      })
    }, 1000)
  }

  const handleUpdate = async () => {
    setUpdateStatus('updating')
    setUpdateMessage('正在启动升级...')
    try {
      const res = await api.selfUpdate()
      if (res.error) {
        setUpdateStatus('error')
        setUpdateMessage(res.message || '启动升级失败')
        return
      }
      pollProgress()
    } catch {
      pollProgress()
    }
  }

  useEffect(() => {
    // check-update / update-progress 需 system:read，无权限者跳过（避免无意义 403）
    if (!canReadSystem) return
    const timer = setTimeout(() => {
      api.updateProgress().then(p => {
        if (p.active === true && p.phase !== 'done') {
          pollProgress()
          return
        }
        api.checkUpdate().then(d => {
          setCurrentVersion(d.current || '-')
          if (d.has_update) {
            setLatestVersion(d.latest || '')
            setUpdateStatus('available')
          } else {
            setUpdateStatus('up-to-date')
          }
        }).catch(() => setUpdateStatus('idle'))
      }).catch(() => {
        api.checkUpdate().then(d => {
          setCurrentVersion(d.current || '-')
          if (d.has_update) {
            setLatestVersion(d.latest || '')
            setUpdateStatus('available')
          } else {
            setUpdateStatus('up-to-date')
          }
        }).catch(() => setUpdateStatus('idle'))
      })
    }, 1500)
    return () => clearTimeout(timer)
  }, [canReadSystem])

  const handleChangePassword = async () => {
    if (!oldPassword) {
      toast('请输入当前密码', 'error')
      return
    }
    if (newPassword.length < 8) {
      toast('新密码至少需要 8 个字符', 'error')
      return
    }
    setChangingPassword(true)
    try {
      await api.changePassword(oldPassword, newPassword)
      toast('密码已修改', 'success')
      setOldPassword('')
      setNewPassword('')
    } catch (err: unknown) {
      // 透传后端原因（如旧密码错误），与其它页面的错误展示模式保持一致
      toast((err as Error).message || '密码修改失败，请检查当前密码是否正确', 'error')
    } finally {
      setChangingPassword(false)
    }
  }

  const handleSaveAccessKey = async () => {
    if (!accessKeyValue.trim()) {
      toast('请输入 Access Key', 'error')
      return
    }
    setSavingKey(true)
    try {
      await api.setAccessKey(accessKeyValue.trim())
      setAccessKeyEnabled(true)
      setAccessKeyValue('')
      toast('Access Key 已设置', 'success')
    } catch (err: unknown) {
      toast((err as Error).message || '设置失败', 'error')
    } finally {
      setSavingKey(false)
    }
  }

  const handleDisableAccessKey = async () => {
    try {
      await api.deleteAccessKey()
      setAccessKeyEnabled(false)
      setShowDisableConfirm(false)
      toast('Access Key 已禁用', 'success')
    } catch (err: unknown) {
      toast((err as Error).message || '禁用失败', 'error')
    }
  }

  if (loading) return <Loading />

  return (
    <>
      <PageHeader title="系统设置" />
      <div className="flex-1 overflow-y-auto p-6 space-y-6">
        <div className="grid grid-cols-2 gap-6">
          {/* Server config (read-only)，后端 GET /config 需 system:admin */}
          {canAdminConfig && (
          <div className="bg-white rounded-lg shadow-sm">
            <div className="px-5 py-4 border-b border-gray-200">
              <h3 className="font-semibold text-gray-900 flex items-center gap-2">
                <Shield className="w-4 h-4 text-gray-500" />
                服务器配置
              </h3>
            </div>
            <div className="p-5 space-y-5">
              {/* Network */}
              <div>
                <h4 className="text-xs font-semibold text-gray-400 uppercase tracking-wider mb-2">网络</h4>
                <div className="space-y-1.5">
                  <div className="flex justify-between text-sm">
                    <span className="text-gray-500">控制端口</span>
                    <span className="font-mono font-medium">{config?.server?.control_port || '-'}</span>
                  </div>
                  <div className="flex justify-between text-sm">
                    <span className="text-gray-500">网关端口</span>
                    <span className="font-mono font-medium">{config?.server?.gateway_port || '-'}</span>
                  </div>
                  <div className="flex justify-between text-sm">
                    <span className="text-gray-500">API 端口</span>
                    <span className="font-mono font-medium">{config?.server?.api_port || '-'}</span>
                  </div>
                  <div className="flex justify-between text-sm">
                    <span className="text-gray-500">TLS</span>
                    <Badge variant={config?.server?.tls_enabled ? 'success' : 'warning'}>
                      {config?.server?.tls_enabled ? '已启用' : '未启用'}
                    </Badge>
                  </div>
                </div>
              </div>

              {/* MQTT */}
              <div>
                <h4 className="text-xs font-semibold text-gray-400 uppercase tracking-wider mb-2">MQTT</h4>
                <div className="space-y-1.5">
                  <div className="flex justify-between text-sm">
                    <span className="text-gray-500">状态</span>
                    <Badge variant={config?.mqtt?.enabled ? 'success' : 'warning'}>
                      {config?.mqtt?.enabled ? '已启用' : '未启用'}
                    </Badge>
                  </div>
                  <div className="flex justify-between text-sm">
                    <span className="text-gray-500">TCP</span>
                    <span className="font-mono font-medium">{config?.mqtt?.tcp_port || '-'}</span>
                  </div>
                  <div className="flex justify-between text-sm">
                    <span className="text-gray-500">WebSocket</span>
                    <span className="font-mono font-medium">{config?.mqtt?.ws_port || '-'}</span>
                  </div>
                </div>
              </div>

              {/* Auth */}
              <div>
                <h4 className="text-xs font-semibold text-gray-400 uppercase tracking-wider mb-2">认证</h4>
                <div className="space-y-1.5">
                  <div className="flex justify-between text-sm">
                    <span className="text-gray-500">JWT 有效期</span>
                    <span className="font-mono font-medium">{config?.auth?.jwt_expiry || '-'}</span>
                  </div>
                  <div className="flex justify-between text-sm">
                    <span className="text-gray-500">Bcrypt Cost</span>
                    <span className="font-mono font-medium">{config?.auth?.bcrypt_cost ?? '-'}</span>
                  </div>
                </div>
              </div>

              {/* Database */}
              <div>
                <h4 className="text-xs font-semibold text-gray-400 uppercase tracking-wider mb-2">数据库</h4>
                <div className="space-y-1.5">
                  <div className="flex justify-between text-sm">
                    <span className="text-gray-500">路径</span>
                    <span className="font-mono font-medium text-xs">{config?.database?.path || '-'}</span>
                  </div>
                </div>
              </div>
            </div>
          </div>
          )}

          {/* Change password */}
          <div className="bg-white rounded-lg shadow-sm">
            <div className="px-5 py-4 border-b border-gray-200">
              <h3 className="font-semibold text-gray-900 flex items-center gap-2">
                <Lock className="w-4 h-4 text-gray-500" />
                修改密码
              </h3>
            </div>
            <div className="p-5 space-y-4">
              <FormField label="当前密码">
                <PasswordInput
                  value={oldPassword}
                  onChange={setOldPassword}
                  show={showOldPassword}
                  onToggle={() => setShowOldPassword(v => !v)}
                  placeholder="输入当前密码"
                />
              </FormField>
              <FormField label="新密码">
                <PasswordInput
                  value={newPassword}
                  onChange={setNewPassword}
                  show={showNewPassword}
                  onToggle={() => setShowNewPassword(v => !v)}
                  placeholder="至少 8 个字符"
                />
              </FormField>
              <div className="flex justify-end">
                <button
                  onClick={handleChangePassword}
                  disabled={changingPassword || !oldPassword || newPassword.length < 8}
                  className="px-4 py-2 text-sm bg-primary text-white rounded-lg hover:bg-primary-dark disabled:opacity-50"
                >
                  {changingPassword ? '修改中...' : '修改密码'}
                </button>
              </div>
            </div>
          </div>
        </div>

        {/* Access Key（accesskey:read/admin）与版本更新（system:read/admin）区块，按各自权限分别显示 */}
        {(canReadAccessKey || canReadSystem) && (
        <div className="grid grid-cols-2 gap-6">
          {/* Access Key */}
          {canReadAccessKey && (
          <div className="bg-white rounded-lg shadow-sm">
            <div className="px-5 py-4 border-b border-gray-200">
              <h3 className="font-semibold text-gray-900">Access Key</h3>
            </div>
          <div className="p-5">
            <div className="flex items-center gap-4 mb-4">
              <span className="text-sm text-gray-500">当前状态：</span>
              <Badge variant={accessKeyEnabled ? 'success' : 'warning'}>
                {accessKeyEnabled ? '已启用' : '未启用'}
              </Badge>
            </div>
            {/* 增删改需 accesskey:admin；仅有 read 权限时只读展示状态 */}
            {canAdminAccessKey && (
            <div className="flex items-center gap-3">
              <input
                type="text"
                value={accessKeyValue}
                onChange={e => setAccessKeyValue(e.target.value)}
                placeholder="输入新的 Access Key"
                className="flex-1 max-w-md px-3 py-2 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-primary/20 focus:border-primary"
              />
              <button
                onClick={handleSaveAccessKey}
                disabled={savingKey || !accessKeyValue.trim()}
                className="px-4 py-2 text-sm bg-primary text-white rounded-lg hover:bg-primary-dark disabled:opacity-50"
              >
                {savingKey ? '保存中...' : '保存'}
              </button>
              {accessKeyEnabled && (
                <button
                  onClick={() => setShowDisableConfirm(true)}
                  className="px-4 py-2 text-sm border border-red-300 text-red-600 rounded-lg hover:bg-red-50"
                >
                  禁用
                </button>
              )}
            </div>
            )}
          </div>
          </div>
          )}

          {/* Version Update */}
          {canReadSystem && (
          <div className="bg-white rounded-lg shadow-sm">
            <div className="px-5 py-4 border-b border-gray-200">
              <h3 className="font-semibold text-gray-900 flex items-center gap-2">
                <RefreshCw className="w-4 h-4 text-gray-500" />
                版本更新
              </h3>
            </div>
            <div className="p-5 space-y-4">
              <div className="flex justify-between text-sm">
                <span className="text-gray-500">当前版本</span>
                <span className="font-mono font-medium">{currentVersion}</span>
              </div>
              {updateStatus === 'idle' && (
                <div className="text-sm text-gray-400">检测中...</div>
              )}
              {updateStatus === 'up-to-date' && (
                <div className="flex items-center gap-2">
                  <Badge variant="success">已是最新版</Badge>
                </div>
              )}
              {updateStatus === 'available' && (
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2">
                    <Badge variant="warning">有新版本</Badge>
                    <span className="font-mono text-sm text-primary font-medium">{latestVersion}</span>
                  </div>
                  {/* 升级会替换二进制，需 system:admin（后端 RegisterStrict） */}
                  {canAdminSystem && (
                  <button
                    onClick={handleUpdate}
                    className="px-4 py-2 text-sm bg-primary text-white rounded-lg hover:bg-primary-dark"
                  >
                    升级
                  </button>
                  )}
                </div>
              )}
              {updateStatus === 'updating' && (
                <div className="text-sm text-blue-600 font-medium">↑ {updateMessage}</div>
              )}
              {updateStatus === 'done' && (
                <div className="text-sm text-green-600 font-medium">{updateMessage}</div>
              )}
              {updateStatus === 'error' && (
                <div className="text-sm text-red-600">{updateMessage}</div>
              )}
            </div>
          </div>
          )}
        </div>
        )}
      </div>

      {/* Disable Access Key Confirm */}
      {showDisableConfirm && (
        <ConfirmDialog
          title="禁用 Access Key"
          message="确定要禁用 Access Key 吗？禁用后使用该 Key 的节点将无法接入。"
          confirmText="确认禁用"
          onConfirm={handleDisableAccessKey}
          onCancel={() => setShowDisableConfirm(false)}
          danger
        />
      )}
    </>
  )
}
