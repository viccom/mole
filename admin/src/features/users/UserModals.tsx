import { useState } from 'react'
import { api } from '../../api/client'
import type { Permission, Role, User } from '../../types/api'
import { Modal } from '../../components/Modal'
import { FormField } from '../../components/FormField'
import { Loading } from '../../components/Loading'
import { useToast } from '../../hooks/useToast'
import { useRequest } from '../../hooks/useRequest'
import { cn, getErrorMessage } from '../../lib/utils'

export function CreateUserModal({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const { toast } = useToast()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [selectedRoleIds, setSelectedRoleIds] = useState<string[]>([])
  const [submitting, setSubmitting] = useState(false)
  const { data } = useRequest(() => api.getRoles(), { onError: () => {} })
  const allRoles = data || []

  const toggleRole = (id: string) => {
    setSelectedRoleIds(prev => prev.includes(id) ? prev.filter(roleId => roleId !== id) : [...prev, id])
  }

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!username.trim() || password.length < 8) {
      toast('请填写完整信息（密码至少8位）', 'error')
      return
    }
    setSubmitting(true)
    try {
      await api.createUser({ username: username.trim(), password, status: 'active', role_ids: selectedRoleIds })
      toast('用户创建成功', 'success')
      onCreated()
      onClose()
    } catch (err: unknown) {
      toast(getErrorMessage(err, '创建失败'), 'error')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Modal title="创建用户" onClose={onClose}>
      <form onSubmit={handleSubmit} className="space-y-4">
        <FormField label="用户名">
          <input
            type="text"
            value={username}
            onChange={e => setUsername(e.target.value)}
            className="w-full border border-gray-300 rounded-lg px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-primary/30 focus:border-primary"
            placeholder="输入用户名"
          />
        </FormField>
        <FormField label="密码">
          <input
            type="password"
            value={password}
            onChange={e => setPassword(e.target.value)}
            className="w-full border border-gray-300 rounded-lg px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-primary/30 focus:border-primary"
            placeholder="至少8位"
            minLength={8}
          />
        </FormField>
        {allRoles.length > 0 && (
          <FormField label="分配角色">
            <div className="space-y-2 max-h-40 overflow-y-auto">
              {allRoles.map(role => (
                <label key={role.id} className="flex items-center gap-2 text-sm cursor-pointer">
                  <input
                    type="checkbox"
                    checked={selectedRoleIds.includes(role.id)}
                    onChange={() => toggleRole(role.id)}
                    className="rounded border-gray-300 text-primary focus:ring-primary/30"
                  />
                  <span>{role.name}</span>
                  {role.description && <span className="text-gray-400 text-xs">({role.description})</span>}
                </label>
              ))}
            </div>
          </FormField>
        )}
        <div className="flex justify-end gap-2 pt-2">
          <button type="button" onClick={onClose} className="px-4 py-2 text-sm border border-gray-300 rounded-lg hover:bg-gray-50">取消</button>
          <button
            type="submit"
            disabled={submitting}
            className="px-4 py-2 text-sm rounded-lg text-white bg-primary hover:bg-primary-dark disabled:opacity-50"
          >
            {submitting ? '创建中...' : '创建'}
          </button>
        </div>
      </form>
    </Modal>
  )
}

export function EditUserModal({ user, onClose, onSaved }: { user: User; onClose: () => void; onSaved: () => void }) {
  const { toast } = useToast()
  const [password, setPassword] = useState('')
  const [status, setStatus] = useState(user.status)
  const [selectedRoleIds, setSelectedRoleIds] = useState<string[]>([])
  const [initialRoleIds, setInitialRoleIds] = useState<string[]>([])
  const [submitting, setSubmitting] = useState(false)
  const { data, loading } = useRequest(
    async () => {
      const [roles, userRoles] = await Promise.all([api.getRoles(), api.getUserRoles(user.id)])
      return { roles, userRoleIds: userRoles.map(role => role.id) }
    },
    {
      onSuccess: (result) => {
        setSelectedRoleIds(result.userRoleIds)
        setInitialRoleIds(result.userRoleIds)
      },
      onError: () => {
        toast('加载角色数据失败', 'error')
      },
    },
  )
  const allRoles = data?.roles || []

  const toggleRole = (id: string) => {
    setSelectedRoleIds(prev => prev.includes(id) ? prev.filter(roleId => roleId !== id) : [...prev, id])
  }

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setSubmitting(true)
    try {
      const updateData: { password?: string; status?: string } = {}
      if (password) updateData.password = password
      if (status !== user.status) updateData.status = status

      if (Object.keys(updateData).length > 0) {
        await api.updateUser(user.id, updateData)
      }

      const toAdd = selectedRoleIds.filter(id => !initialRoleIds.includes(id))
      const toRemove = initialRoleIds.filter(id => !selectedRoleIds.includes(id))

      await Promise.all([
        ...toAdd.map(roleId => api.assignRole(user.id, roleId)),
        ...toRemove.map(roleId => api.revokeRole(user.id, roleId)),
      ])

      toast('用户更新成功', 'success')
      onSaved()
      onClose()
    } catch (err: unknown) {
      toast(getErrorMessage(err, '更新失败'), 'error')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Modal title={`编辑用户: ${user.username}`} onClose={onClose}>
      {loading ? <Loading /> : (
        <form onSubmit={handleSubmit} className="space-y-4">
          <FormField label="用户名">
            <input
              type="text"
              value={user.username}
              disabled
              className="w-full border border-gray-200 rounded-lg px-3 py-2 text-sm bg-gray-50 text-gray-500"
            />
          </FormField>
          <FormField label="新密码（留空不修改）">
            <input
              type="password"
              value={password}
              onChange={e => setPassword(e.target.value)}
              className="w-full border border-gray-300 rounded-lg px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-primary/30 focus:border-primary"
              placeholder="至少8位"
              minLength={8}
            />
          </FormField>
          <FormField label="状态">
            <select
              value={status}
              onChange={e => setStatus(e.target.value as 'active' | 'disabled')}
              className="w-full border border-gray-300 rounded-lg px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-primary/30 focus:border-primary"
            >
              <option value="active">启用</option>
              <option value="disabled">禁用</option>
            </select>
          </FormField>
          {allRoles.length > 0 && (
            <FormField label="分配角色">
              <div className="space-y-2 max-h-40 overflow-y-auto">
                {allRoles.map(role => (
                  <label key={role.id} className="flex items-center gap-2 text-sm cursor-pointer">
                    <input
                      type="checkbox"
                      checked={selectedRoleIds.includes(role.id)}
                      onChange={() => toggleRole(role.id)}
                      className="rounded border-gray-300 text-primary focus:ring-primary/30"
                    />
                    <span>{role.name}</span>
                    {role.description && <span className="text-gray-400 text-xs">({role.description})</span>}
                  </label>
                ))}
              </div>
            </FormField>
          )}
          <div className="flex justify-end gap-2 pt-2">
            <button type="button" onClick={onClose} className="px-4 py-2 text-sm border border-gray-300 rounded-lg hover:bg-gray-50">取消</button>
            <button
              type="submit"
              disabled={submitting}
              className="px-4 py-2 text-sm rounded-lg text-white bg-primary hover:bg-primary-dark disabled:opacity-50"
            >
              {submitting ? '保存中...' : '保存'}
            </button>
          </div>
        </form>
      )}
    </Modal>
  )
}

export function RoleFormModal({ role, onClose, onSaved }: { role?: Role; onClose: () => void; onSaved: () => void }) {
  const { toast } = useToast()
  const isEdit = !!role

  const [roleId, setRoleId] = useState(role?.id || '')
  const [name, setName] = useState(role?.name || '')
  const [description, setDescription] = useState(role?.description || '')
  const [selectedPerms, setSelectedPerms] = useState<Set<string>>(() => {
    if (!role?.permissions) return new Set<string>()
    return new Set(role.permissions.map(permission => `${permission.resource}:${permission.action}`))
  })
  const [submitting, setSubmitting] = useState(false)
  const { data: permissionsData, loading } = useRequest<Permission[]>(
    () => api.getPermissions(),
    {
      onError: () => {
        toast('加载权限列表失败', 'error')
      },
    },
  )
  const permissions = permissionsData || []

  const togglePerm = (key: string) => {
    setSelectedPerms(prev => {
      const next = new Set(prev)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
  }

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!roleId.trim() || !name.trim()) {
      toast('角色ID和名称不能为空', 'error')
      return
    }
    setSubmitting(true)
    try {
      const perms = Array.from(selectedPerms).map(key => {
        const [resource, action] = key.split(':')
        return { resource, action }
      })

      if (isEdit) {
        await api.updateRole(role.id, { name, description, permissions: perms })
      } else {
        await api.createRole({ id: roleId.trim(), name: name.trim(), description, permissions: perms })
      }
      toast(isEdit ? '角色更新成功' : '角色创建成功', 'success')
      onSaved()
      onClose()
    } catch (err: unknown) {
      toast(getErrorMessage(err, '操作失败'), 'error')
    } finally {
      setSubmitting(false)
    }
  }

  const grouped = permissions.reduce<Record<string, Permission[]>>((acc, permission) => {
    if (!acc[permission.resource]) acc[permission.resource] = []
    acc[permission.resource].push(permission)
    return acc
  }, {})

  return (
    <Modal title={isEdit ? '编辑角色' : '创建角色'} onClose={onClose}>
      {loading ? <Loading /> : (
        <form onSubmit={handleSubmit} className="space-y-4">
          <FormField label="角色ID">
            <input
              type="text"
              value={roleId}
              onChange={e => setRoleId(e.target.value)}
              disabled={isEdit}
              className={cn(
                'w-full border rounded-lg px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-primary/30 focus:border-primary',
                isEdit ? 'border-gray-200 bg-gray-50 text-gray-500' : 'border-gray-300',
              )}
              placeholder="如: operator"
            />
          </FormField>
          <FormField label="名称">
            <input
              type="text"
              value={name}
              onChange={e => setName(e.target.value)}
              className="w-full border border-gray-300 rounded-lg px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-primary/30 focus:border-primary"
              placeholder="角色显示名称"
            />
          </FormField>
          <FormField label="描述">
            <input
              type="text"
              value={description}
              onChange={e => setDescription(e.target.value)}
              className="w-full border border-gray-300 rounded-lg px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-primary/30 focus:border-primary"
              placeholder="角色描述"
            />
          </FormField>
          {permissions.length > 0 && (
            <FormField label="权限">
              <div className="space-y-3 max-h-56 overflow-y-auto border border-gray-200 rounded-lg p-3">
                {Object.entries(grouped).map(([resource, perms]) => (
                  <div key={resource}>
                    <div className="text-xs font-semibold text-gray-500 uppercase tracking-wider mb-1.5">{resource}</div>
                    <div className="flex flex-wrap gap-2">
                      {perms.map(permission => {
                        const key = `${permission.resource}:${permission.action}`
                        const checked = selectedPerms.has(key)
                        return (
                          <label
                            key={key}
                            className={cn(
                              'inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md text-xs font-mono cursor-pointer border transition-colors',
                              checked
                                ? 'bg-primary/10 border-primary/30 text-primary'
                                : 'bg-gray-50 border-gray-200 text-gray-600 hover:border-gray-300',
                            )}
                          >
                            <input
                              type="checkbox"
                              checked={checked}
                              onChange={() => togglePerm(key)}
                              className="sr-only"
                            />
                            {permission.action}
                          </label>
                        )
                      })}
                    </div>
                  </div>
                ))}
              </div>
            </FormField>
          )}
          <div className="flex justify-end gap-2 pt-2">
            <button type="button" onClick={onClose} className="px-4 py-2 text-sm border border-gray-300 rounded-lg hover:bg-gray-50">取消</button>
            <button
              type="submit"
              disabled={submitting}
              className="px-4 py-2 text-sm rounded-lg text-white bg-primary hover:bg-primary-dark disabled:opacity-50"
            >
              {submitting ? '保存中...' : isEdit ? '更新' : '创建'}
            </button>
          </div>
        </form>
      )}
    </Modal>
  )
}
