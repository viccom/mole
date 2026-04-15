import { useState, useEffect, useCallback } from 'react'
import { Tab, TabGroup, TabList, TabPanel, TabPanels } from '@headlessui/react'
import { UserPlus, Pencil, Trash2, ShieldPlus } from 'lucide-react'
import { api } from '../api/client'
import type { User, Role, Permission } from '../types/api'
import { PageHeader } from '../components/PageHeader'
import { Badge } from '../components/Badge'
import { Loading } from '../components/Loading'
import { Empty } from '../components/Empty'
import { Modal } from '../components/Modal'
import { FormField } from '../components/FormField'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { useToast } from '../hooks/useToast'
import { cn } from '../lib/utils'

// ---------------------------------------------------------------------------
// CreateUserModal
// ---------------------------------------------------------------------------
function CreateUserModal({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const { toast } = useToast()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [allRoles, setAllRoles] = useState<Role[]>([])
  const [selectedRoleIds, setSelectedRoleIds] = useState<string[]>([])
  const [submitting, setSubmitting] = useState(false)

  useEffect(() => {
    api.getRoles().then(setAllRoles).catch(() => {})
  }, [])

  const toggleRole = (id: string) => {
    setSelectedRoleIds(prev => prev.includes(id) ? prev.filter(r => r !== id) : [...prev, id])
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
    } catch (err: any) {
      toast(err.message || '创建失败', 'error')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Modal title="创建用户" onClose={onClose}>
      <form onSubmit={handleSubmit} className="space-y-4">
        <FormField label="用户名">
          <input
            type="text" value={username} onChange={e => setUsername(e.target.value)}
            className="w-full border border-gray-300 rounded-lg px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-primary/30 focus:border-primary"
            placeholder="输入用户名"
          />
        </FormField>
        <FormField label="密码">
          <input
            type="password" value={password} onChange={e => setPassword(e.target.value)}
            className="w-full border border-gray-300 rounded-lg px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-primary/30 focus:border-primary"
            placeholder="至少8位" minLength={8}
          />
        </FormField>
        {allRoles.length > 0 && (
          <FormField label="分配角色">
            <div className="space-y-2 max-h-40 overflow-y-auto">
              {allRoles.map(role => (
                <label key={role.id} className="flex items-center gap-2 text-sm cursor-pointer">
                  <input
                    type="checkbox" checked={selectedRoleIds.includes(role.id)}
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
            type="submit" disabled={submitting}
            className="px-4 py-2 text-sm rounded-lg text-white bg-primary hover:bg-primary-dark disabled:opacity-50"
          >
            {submitting ? '创建中...' : '创建'}
          </button>
        </div>
      </form>
    </Modal>
  )
}

// ---------------------------------------------------------------------------
// EditUserModal
// ---------------------------------------------------------------------------
function EditUserModal({ user, onClose, onSaved }: { user: User; onClose: () => void; onSaved: () => void }) {
  const { toast } = useToast()
  const [password, setPassword] = useState('')
  const [status, setStatus] = useState(user.status)
  const [allRoles, setAllRoles] = useState<Role[]>([])
  const [selectedRoleIds, setSelectedRoleIds] = useState<string[]>([])
  const [initialRoleIds, setInitialRoleIds] = useState<string[]>([])
  const [loading, setLoading] = useState(true)
  const [submitting, setSubmitting] = useState(false)

  useEffect(() => {
    Promise.all([
      api.getRoles(),
      api.getUserRoles(user.id),
    ]).then(([roles, userRoles]) => {
      setAllRoles(roles)
      const ids = userRoles.map(r => r.id)
      setSelectedRoleIds(ids)
      setInitialRoleIds(ids)
    }).catch(() => {
      toast('加载角色数据失败', 'error')
    }).finally(() => setLoading(false))
  }, [user.id])

  const toggleRole = (id: string) => {
    setSelectedRoleIds(prev => prev.includes(id) ? prev.filter(r => r !== id) : [...prev, id])
  }

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setSubmitting(true)
    try {
      // Update user info (password/status)
      const updateData: { password?: string; status?: string } = {}
      if (password) updateData.password = password
      if (status !== user.status) updateData.status = status

      if (Object.keys(updateData).length > 0) {
        await api.updateUser(user.id, updateData)
      }

      // Sync role assignments
      const toAdd = selectedRoleIds.filter(id => !initialRoleIds.includes(id))
      const toRemove = initialRoleIds.filter(id => !selectedRoleIds.includes(id))

      await Promise.all([
        ...toAdd.map(roleId => api.assignRole(user.id, roleId)),
        ...toRemove.map(roleId => api.revokeRole(user.id, roleId)),
      ])

      toast('用户更新成功', 'success')
      onSaved()
      onClose()
    } catch (err: any) {
      toast(err.message || '更新失败', 'error')
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
              type="text" value={user.username} disabled
              className="w-full border border-gray-200 rounded-lg px-3 py-2 text-sm bg-gray-50 text-gray-500"
            />
          </FormField>
          <FormField label="新密码（留空不修改）">
            <input
              type="password" value={password} onChange={e => setPassword(e.target.value)}
              className="w-full border border-gray-300 rounded-lg px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-primary/30 focus:border-primary"
              placeholder="至少8位" minLength={8}
            />
          </FormField>
          <FormField label="状态">
            <select
              value={status} onChange={e => setStatus(e.target.value as 'active' | 'disabled')}
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
                      type="checkbox" checked={selectedRoleIds.includes(role.id)}
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
              type="submit" disabled={submitting}
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

// ---------------------------------------------------------------------------
// RoleFormModal (create / edit)
// ---------------------------------------------------------------------------
function RoleFormModal({ role, onClose, onSaved }: { role?: Role; onClose: () => void; onSaved: () => void }) {
  const { toast } = useToast()
  const isEdit = !!role

  const [roleId, setRoleId] = useState(role?.id || '')
  const [name, setName] = useState(role?.name || '')
  const [description, setDescription] = useState(role?.description || '')
  const [permissions, setPermissions] = useState<Permission[]>([])
  const [selectedPerms, setSelectedPerms] = useState<Set<string>>(() => {
    if (!role?.permissions) return new Set<string>()
    return new Set(role.permissions.map(p => `${p.resource}:${p.action}`))
  })
  const [loading, setLoading] = useState(true)
  const [submitting, setSubmitting] = useState(false)

  useEffect(() => {
    api.getPermissions().then(perms => {
      setPermissions(perms)
    }).catch(() => {
      toast('加载权限列表失败', 'error')
    }).finally(() => setLoading(false))
  }, [])

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
        await api.updateRole(role!.id, { name, description, permissions: perms })
      } else {
        await api.createRole({ id: roleId.trim(), name: name.trim(), description, permissions: perms })
      }
      toast(isEdit ? '角色更新成功' : '角色创建成功', 'success')
      onSaved()
      onClose()
    } catch (err: any) {
      toast(err.message || '操作失败', 'error')
    } finally {
      setSubmitting(false)
    }
  }

  // Group permissions by resource for better display
  const grouped = permissions.reduce<Record<string, Permission[]>>((acc, p) => {
    if (!acc[p.resource]) acc[p.resource] = []
    acc[p.resource].push(p)
    return acc
  }, {})

  return (
    <Modal title={isEdit ? '编辑角色' : '创建角色'} onClose={onClose}>
      {loading ? <Loading /> : (
        <form onSubmit={handleSubmit} className="space-y-4">
          <FormField label="角色ID">
            <input
              type="text" value={roleId} onChange={e => setRoleId(e.target.value)}
              disabled={isEdit}
              className={cn(
                'w-full border rounded-lg px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-primary/30 focus:border-primary',
                isEdit ? 'border-gray-200 bg-gray-50 text-gray-500' : 'border-gray-300'
              )}
              placeholder="如: operator"
            />
          </FormField>
          <FormField label="名称">
            <input
              type="text" value={name} onChange={e => setName(e.target.value)}
              className="w-full border border-gray-300 rounded-lg px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-primary/30 focus:border-primary"
              placeholder="角色显示名称"
            />
          </FormField>
          <FormField label="描述">
            <input
              type="text" value={description} onChange={e => setDescription(e.target.value)}
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
                      {perms.map(p => {
                        const key = `${p.resource}:${p.action}`
                        const checked = selectedPerms.has(key)
                        return (
                          <label
                            key={key}
                            className={cn(
                              'inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md text-xs font-mono cursor-pointer border transition-colors',
                              checked
                                ? 'bg-primary/10 border-primary/30 text-primary'
                                : 'bg-gray-50 border-gray-200 text-gray-600 hover:border-gray-300'
                            )}
                          >
                            <input
                              type="checkbox" checked={checked}
                              onChange={() => togglePerm(key)}
                              className="sr-only"
                            />
                            {p.action}
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
              type="submit" disabled={submitting}
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

// ---------------------------------------------------------------------------
// UsersPage
// ---------------------------------------------------------------------------
export function UsersPage() {
  const { toast } = useToast()
  const [users, setUsers] = useState<User[]>([])
  const [roles, setRoles] = useState<Role[]>([])
  const [loading, setLoading] = useState(true)
  const [tabIndex, setTabIndex] = useState(0)

  // Modal states
  const [createUserOpen, setCreateUserOpen] = useState(false)
  const [editUser, setEditUser] = useState<User | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<User | null>(null)
  const [roleFormTarget, setRoleFormTarget] = useState<Role | null | 'create'>(null)
  const [deleteRoleTarget, setDeleteRoleTarget] = useState<Role | null>(null)

  const loadData = useCallback(() => {
    Promise.all([api.getUsers(), api.getRoles()])
      .then(([u, r]) => { setUsers(u); setRoles(r) })
      .catch(() => toast('加载数据失败', 'error'))
      .finally(() => setLoading(false))
  }, [toast])

  useEffect(() => { loadData() }, [loadData])

  const handleDeleteUser = async () => {
    if (!deleteTarget) return
    try {
      await api.deleteUser(deleteTarget.id)
      toast('用户已删除', 'success')
      setDeleteTarget(null)
      loadData()
    } catch (err: any) {
      toast(err.message || '删除失败', 'error')
    }
  }

  const handleDeleteRole = async () => {
    if (!deleteRoleTarget) return
    try {
      await api.deleteRole(deleteRoleTarget.id)
      toast('角色已删除', 'success')
      setDeleteRoleTarget(null)
      loadData()
    } catch (err: any) {
      toast(err.message || '删除失败', 'error')
    }
  }

  if (loading) return <Loading />

  return (
    <>
      <TabGroup selectedIndex={tabIndex} onChange={setTabIndex}>
        <PageHeader
          title="用户与角色"
          actions={
            <TabList className="flex gap-1">
              <Tab className={({ selected }) => cn(
                'px-4 py-1.5 text-sm font-medium outline-none transition-colors',
                selected
                  ? 'text-primary border-b-2 border-primary'
                  : 'text-gray-500 border-b-2 border-transparent hover:text-gray-700'
              )}>
                用户列表
              </Tab>
              <Tab className={({ selected }) => cn(
                'px-4 py-1.5 text-sm font-medium outline-none transition-colors',
                selected
                  ? 'text-primary border-b-2 border-primary'
                  : 'text-gray-500 border-b-2 border-transparent hover:text-gray-700'
              )}>
                角色管理
              </Tab>
            </TabList>
          }
        />

        <TabPanels>
          {/* ---- Tab 1: Users ---- */}
          <TabPanel>
            <div className="flex-1 overflow-y-auto p-6">
              <div className="bg-white rounded-lg shadow-sm">
                <div className="px-5 py-4 border-b border-gray-200 flex items-center justify-between">
                  <h3 className="font-semibold text-gray-900">用户列表</h3>
                  <button
                    onClick={() => setCreateUserOpen(true)}
                    className="inline-flex items-center gap-1.5 px-3 py-1.5 text-sm rounded-lg text-white bg-primary hover:bg-primary-dark"
                  >
                    <UserPlus className="w-4 h-4" />
                    创建用户
                  </button>
                </div>

                {users.length === 0 ? <Empty message="暂无用户" /> : (
                  <div className="overflow-x-auto">
                    <table className="w-full">
                      <thead>
                        <tr className="bg-gray-50">
                          <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">ID</th>
                          <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">用户名</th>
                          <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">状态</th>
                          <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">创建时间</th>
                          <th className="text-right px-4 py-3 text-xs font-medium text-gray-500">操作</th>
                        </tr>
                      </thead>
                      <tbody className="divide-y divide-gray-100">
                        {users.map(u => (
                          <tr key={u.id} className="hover:bg-gray-50/50">
                            <td className="px-4 py-3">
                              <code className="text-xs bg-gray-100 px-1.5 py-0.5 rounded">{u.id}</code>
                            </td>
                            <td className="px-4 py-3 text-sm">{u.username}</td>
                            <td className="px-4 py-3">
                              <Badge variant={u.status === 'active' ? 'success' : 'error'}>
                                {u.status === 'active' ? '启用' : '禁用'}
                              </Badge>
                            </td>
                            <td className="px-4 py-3 text-sm text-gray-500">{u.created_at || '-'}</td>
                            <td className="px-4 py-3">
                              <div className="flex items-center justify-end gap-1">
                                <button
                                  onClick={() => setEditUser(u)}
                                  className="p-1.5 text-gray-400 hover:text-primary rounded-md hover:bg-gray-100"
                                  title="编辑"
                                >
                                  <Pencil className="w-4 h-4" />
                                </button>
                                {u.username !== 'admin' && (
                                  <button
                                    onClick={() => setDeleteTarget(u)}
                                    className="p-1.5 text-gray-400 hover:text-red-600 rounded-md hover:bg-red-50"
                                    title="删除"
                                  >
                                    <Trash2 className="w-4 h-4" />
                                  </button>
                                )}
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
          </TabPanel>

          {/* ---- Tab 2: Roles ---- */}
          <TabPanel>
            <div className="flex-1 overflow-y-auto p-6">
              <div className="bg-white rounded-lg shadow-sm">
                <div className="px-5 py-4 border-b border-gray-200 flex items-center justify-between">
                  <h3 className="font-semibold text-gray-900">角色管理</h3>
                  <button
                    onClick={() => setRoleFormTarget('create')}
                    className="inline-flex items-center gap-1.5 px-3 py-1.5 text-sm rounded-lg text-white bg-primary hover:bg-primary-dark"
                  >
                    <ShieldPlus className="w-4 h-4" />
                    创建角色
                  </button>
                </div>

                {roles.length === 0 ? <Empty message="暂无角色" /> : (
                  <div className="overflow-x-auto">
                    <table className="w-full">
                      <thead>
                        <tr className="bg-gray-50">
                          <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">ID</th>
                          <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">名称</th>
                          <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">描述</th>
                          <th className="text-left px-4 py-3 text-xs font-medium text-gray-500">权限</th>
                          <th className="text-right px-4 py-3 text-xs font-medium text-gray-500">操作</th>
                        </tr>
                      </thead>
                      <tbody className="divide-y divide-gray-100">
                        {roles.map(r => (
                          <tr key={r.id} className="hover:bg-gray-50/50">
                            <td className="px-4 py-3">
                              <code className="text-xs bg-gray-100 px-1.5 py-0.5 rounded">{r.id}</code>
                            </td>
                            <td className="px-4 py-3 text-sm">{r.name}</td>
                            <td className="px-4 py-3 text-sm text-gray-500">{r.description || '-'}</td>
                            <td className="px-4 py-3">
                              <div className="flex flex-wrap gap-1">
                                {(r.permissions || []).map((p, i) => (
                                  <code key={i} className="text-xs bg-purple-50 text-purple-700 px-1.5 py-0.5 rounded">
                                    {p.resource}:{p.action}
                                  </code>
                                ))}
                                {(!r.permissions || r.permissions.length === 0) && (
                                  <span className="text-xs text-gray-400">无权限</span>
                                )}
                              </div>
                            </td>
                            <td className="px-4 py-3">
                              <div className="flex items-center justify-end gap-1">
                                <button
                                  onClick={() => setRoleFormTarget(r)}
                                  className="p-1.5 text-gray-400 hover:text-primary rounded-md hover:bg-gray-100"
                                  title="编辑"
                                >
                                  <Pencil className="w-4 h-4" />
                                </button>
                                {r.id !== 'admin' && (
                                  <button
                                    onClick={() => setDeleteRoleTarget(r)}
                                    className="p-1.5 text-gray-400 hover:text-red-600 rounded-md hover:bg-red-50"
                                    title="删除"
                                  >
                                    <Trash2 className="w-4 h-4" />
                                  </button>
                                )}
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
          </TabPanel>
        </TabPanels>
      </TabGroup>

      {/* ---- Modals ---- */}
      {createUserOpen && (
        <CreateUserModal onClose={() => setCreateUserOpen(false)} onCreated={loadData} />
      )}
      {editUser && (
        <EditUserModal user={editUser} onClose={() => setEditUser(null)} onSaved={loadData} />
      )}
      {deleteTarget && (
        <ConfirmDialog
          title="删除用户"
          message={`确定要删除用户「${deleteTarget.username}」吗？该用户的 Access Token 将被禁用，归属节点将转移给 system。此操作不可撤销。`}
          confirmText="删除"
          onConfirm={handleDeleteUser}
          onCancel={() => setDeleteTarget(null)}
          danger
        />
      )}
      {roleFormTarget !== null && (
        <RoleFormModal
          role={roleFormTarget === 'create' ? undefined : roleFormTarget}
          onClose={() => setRoleFormTarget(null)}
          onSaved={loadData}
        />
      )}
      {deleteRoleTarget && (
        <ConfirmDialog
          title="删除角色"
          message={`确定要删除角色「${deleteRoleTarget.name}」吗？已分配该角色的用户将失去对应权限。此操作不可撤销。`}
          confirmText="删除"
          onConfirm={handleDeleteRole}
          onCancel={() => setDeleteRoleTarget(null)}
          danger
        />
      )}
    </>
  )
}
