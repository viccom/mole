import { useState } from 'react'
import { Tab, TabGroup, TabList, TabPanel, TabPanels } from '@headlessui/react'
import { RefreshCw } from 'lucide-react'
import { api } from '../api/client'
import type { User, Role } from '../types/api'
import { PageHeader } from '../components/PageHeader'
import { Loading } from '../components/Loading'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { useToast } from '../hooks/useToast'
import { cn, getErrorMessage } from '../lib/utils'
import { useRequest } from '../hooks/useRequest'
import { CreateUserModal, EditUserModal, RoleFormModal } from '../features/users/UserModals'
import { UsersTable } from '../features/users/UsersTable'
import { RolesTable } from '../features/users/RolesTable'

// ---------------------------------------------------------------------------
// UsersPage
// ---------------------------------------------------------------------------
export function UsersPage() {
  const { toast } = useToast()
  const [tabIndex, setTabIndex] = useState(0)

  // Modal states
  const [createUserOpen, setCreateUserOpen] = useState(false)
  const [editUser, setEditUser] = useState<User | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<User | null>(null)
  const [roleFormTarget, setRoleFormTarget] = useState<Role | null | 'create'>(null)
  const [deleteRoleTarget, setDeleteRoleTarget] = useState<Role | null>(null)
  const { data, loading, run: loadData } = useRequest(
    async () => {
      const [users, roles] = await Promise.all([api.getUsers(), api.getRoles()])
      return { users, roles }
    },
    {
      onError: () => {
        toast('加载数据失败', 'error')
      },
    },
  )
  const users = data?.users || []
  const roles = data?.roles || []

  const handleDeleteUser = async () => {
    if (!deleteTarget) return
    try {
      await api.deleteUser(deleteTarget.id)
      toast('用户已删除', 'success')
      setDeleteTarget(null)
      loadData()
    } catch (err: unknown) {
      toast(getErrorMessage(err, '删除失败'), 'error')
    }
  }

  const handleDeleteRole = async () => {
    if (!deleteRoleTarget) return
    try {
      await api.deleteRole(deleteRoleTarget.id)
      toast('角色已删除', 'success')
      setDeleteRoleTarget(null)
      loadData()
    } catch (err: unknown) {
      toast(getErrorMessage(err, '删除失败'), 'error')
    }
  }

  if (loading) return <Loading />

  return (
    <>
      <TabGroup selectedIndex={tabIndex} onChange={setTabIndex}>
        <PageHeader
          title="用户与角色"
          actions={
            <div className="flex items-center gap-3">
              <button
                onClick={() => { void loadData().catch(() => {}) }}
                className="flex items-center gap-1.5 px-3 py-1.5 text-sm border border-gray-300 rounded-lg hover:bg-gray-50"
              >
                <RefreshCw className="w-4 h-4" />
                刷新
              </button>
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
            </div>
          }
        />

        <TabPanels>
          {/* ---- Tab 1: Users ---- */}
          <TabPanel>
            <div className="flex-1 overflow-y-auto p-6">
              <UsersTable
                users={users}
                onCreate={() => setCreateUserOpen(true)}
                onEdit={setEditUser}
                onDelete={setDeleteTarget}
              />
            </div>
          </TabPanel>

          {/* ---- Tab 2: Roles ---- */}
          <TabPanel>
            <div className="flex-1 overflow-y-auto p-6">
              <RolesTable
                roles={roles}
                onCreate={() => setRoleFormTarget('create')}
                onEdit={setRoleFormTarget}
                onDelete={setDeleteRoleTarget}
              />
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
