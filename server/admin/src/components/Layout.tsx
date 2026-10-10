import { NavLink, Outlet } from 'react-router-dom'
import { LayoutDashboard, Server, Network, Users, KeyRound, Radio, Settings, LogOut } from 'lucide-react'
import { useAuth } from '../hooks/useAuth'
import { isFeishuEnv } from '../lib/feishu'
import { isDingTalkEnv } from '../lib/dingtalk'

// 导航入口所需权限（对照 cmd/moleagent-serv/main.go 路由注册表的 resource:action），
// perms 内任一满足即显示；不带 perms 的是通用入口，任何登录用户可见。
// 只做入口级门控（导航隐藏），页面内不做逐按钮控制
const navItems = [
  { to: '/dashboard', icon: LayoutDashboard, label: '仪表盘' },
  { to: '/nodes', icon: Server, label: '节点管理' },
  { to: '/tunnels', icon: Network, label: '隧道管理' },
  // 用户管理页含用户与角色两个 Tab，users:read / roles:read 有其一即可进入
  { to: '/users', icon: Users, label: '用户管理', perms: ['users:read', 'roles:read'] },
  // 接入 Token 是 /me/access-tokens 自助接口（RegisterAuth），任何登录用户均可管理自己的 token
  { to: '/access-tokens', icon: KeyRound, label: '接入Token' },
  { to: '/mqtt', icon: Radio, label: 'MQTT', perms: ['mqtt:read'] },
  // 系统设置页含"修改密码"（任意用户功能），入口不能整体按 system:admin 收窄——
  // 门控下沉到页面内区块（Access Key/升级按 system:admin 隐藏）
  { to: '/settings', icon: Settings, label: '系统设置' },
]

export function Layout() {
  const { user, logout, hasPermission } = useAuth()
  const isNativeSSO = isFeishuEnv() || isDingTalkEnv()
  // 权限未就绪（加载中）时 hasPermission 返回 false，管理入口暂隐，
  // 权限到位后再显示；角色接口失败降级为只显示通用入口，不白屏
  const visibleNavItems = navItems.filter(item => {
    if (!item.perms) return true
    return item.perms.some(p => {
      const [resource, action] = p.split(':')
      return hasPermission(resource, action)
    })
  })

  return (
    <div className="flex h-screen bg-gray-100">
      <aside className="w-60 bg-sidebar flex flex-col flex-shrink-0">
        <div className="p-5 flex items-center gap-3 border-b border-white/10">
          <div className="text-primary-light">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className="w-7 h-7">
              <circle cx="12" cy="12" r="3"/>
              <path d="M12 1v4M12 19v4M4.22 4.22l2.83 2.83M16.95 16.95l2.83 2.83M1 12h4M19 12h4M4.22 19.78l2.83-2.83M16.95 7.05l2.83-2.83"/>
            </svg>
          </div>
          <span className="text-white font-semibold text-base">MoleAgent</span>
        </div>

        <nav className="flex-1 py-3 overflow-y-auto">
          {visibleNavItems.map(item => (
            <NavLink
              key={item.to}
              to={item.to}
              className={({ isActive }) =>
                `flex items-center gap-3 px-5 py-2.5 text-sm transition-colors border-l-3 ${
                  isActive
                    ? 'bg-sidebarHover text-white border-primary-light'
                    : 'text-white/60 hover:bg-sidebarHover hover:text-white border-transparent'
                }`
              }
            >
              <item.icon className="w-5 h-5 flex-shrink-0" />
              <span>{item.label}</span>
            </NavLink>
          ))}
        </nav>

        <div className="p-4 border-t border-white/10">
          <div className="text-white/70 text-xs mb-2">{user?.username || 'admin'}</div>
          {!isNativeSSO && (
            <button
              onClick={logout}
              className="flex items-center gap-2 text-white/60 hover:text-white text-xs transition-colors"
            >
              <LogOut className="w-4 h-4" /> 退出
            </button>
          )}
        </div>
      </aside>

      <main className="flex-1 flex flex-col overflow-hidden">
        <Outlet />
      </main>
    </div>
  )
}
