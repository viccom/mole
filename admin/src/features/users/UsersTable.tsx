import { Pencil, Trash2, UserPlus } from 'lucide-react'
import type { User } from '../../types/api'
import { Badge } from '../../components/Badge'
import { Empty } from '../../components/Empty'

interface UsersTableProps {
  users: User[]
  onCreate: () => void
  onEdit: (user: User) => void
  onDelete: (user: User) => void
}

export function UsersTable({ users, onCreate, onEdit, onDelete }: UsersTableProps) {
  return (
    <div className="bg-white rounded-lg shadow-sm">
      <div className="px-5 py-4 border-b border-gray-200 flex items-center justify-between">
        <h3 className="font-semibold text-gray-900">用户列表</h3>
        <button
          onClick={onCreate}
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
              {users.map(user => (
                <tr key={user.id} className="hover:bg-gray-50/50">
                  <td className="px-4 py-3">
                    <code className="text-xs bg-gray-100 px-1.5 py-0.5 rounded">{user.id}</code>
                  </td>
                  <td className="px-4 py-3 text-sm">{user.username}</td>
                  <td className="px-4 py-3">
                    <Badge variant={user.status === 'active' ? 'success' : 'error'}>
                      {user.status === 'active' ? '启用' : '禁用'}
                    </Badge>
                  </td>
                  <td className="px-4 py-3 text-sm text-gray-500">{user.created_at || '-'}</td>
                  <td className="px-4 py-3">
                    <div className="flex items-center justify-end gap-1">
                      <button
                        onClick={() => onEdit(user)}
                        className="p-1.5 text-gray-400 hover:text-primary rounded-md hover:bg-gray-100"
                        title="编辑"
                      >
                        <Pencil className="w-4 h-4" />
                      </button>
                      {user.username !== 'admin' && (
                        <button
                          onClick={() => onDelete(user)}
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
