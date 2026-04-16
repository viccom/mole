import { Pencil, ShieldPlus, Trash2 } from 'lucide-react'
import type { Role } from '../../types/api'
import { Empty } from '../../components/Empty'

interface RolesTableProps {
  roles: Role[]
  onCreate: () => void
  onEdit: (role: Role) => void
  onDelete: (role: Role) => void
}

export function RolesTable({ roles, onCreate, onEdit, onDelete }: RolesTableProps) {
  return (
    <div className="bg-white rounded-lg shadow-sm">
      <div className="px-5 py-4 border-b border-gray-200 flex items-center justify-between">
        <h3 className="font-semibold text-gray-900">角色管理</h3>
        <button
          onClick={onCreate}
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
              {roles.map(role => (
                <tr key={role.id} className="hover:bg-gray-50/50">
                  <td className="px-4 py-3">
                    <code className="text-xs bg-gray-100 px-1.5 py-0.5 rounded">{role.id}</code>
                  </td>
                  <td className="px-4 py-3 text-sm">{role.name}</td>
                  <td className="px-4 py-3 text-sm text-gray-500">{role.description || '-'}</td>
                  <td className="px-4 py-3">
                    <div className="flex flex-wrap gap-1">
                      {(role.permissions || []).map((permission, index) => (
                        <code key={index} className="text-xs bg-purple-50 text-purple-700 px-1.5 py-0.5 rounded">
                          {permission.resource}:{permission.action}
                        </code>
                      ))}
                      {(!role.permissions || role.permissions.length === 0) && (
                        <span className="text-xs text-gray-400">无权限</span>
                      )}
                    </div>
                  </td>
                  <td className="px-4 py-3">
                    <div className="flex items-center justify-end gap-1">
                      <button
                        onClick={() => onEdit(role)}
                        className="p-1.5 text-gray-400 hover:text-primary rounded-md hover:bg-gray-100"
                        title="编辑"
                      >
                        <Pencil className="w-4 h-4" />
                      </button>
                      {role.id !== 'admin' && (
                        <button
                          onClick={() => onDelete(role)}
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
  )
}
