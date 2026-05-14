import { NavLink, Outlet } from 'react-router-dom'
import { LayoutDashboard, Server, Network, Users, KeyRound, Radio, Settings, LogOut } from 'lucide-react'
import { useAuth } from '../hooks/useAuth'
import { isFeishuEnv } from '../lib/feishu'
import { isDingTalkEnv } from '../lib/dingtalk'

const navItems = [
  { to: '/dashboard', icon: LayoutDashboard, label: '仪表盘' },
  { to: '/nodes', icon: Server, label: '节点管理' },
  { to: '/tunnels', icon: Network, label: '隧道管理' },
  { to: '/users', icon: Users, label: '用户管理' },
  { to: '/access-tokens', icon: KeyRound, label: '接入Token' },
  { to: '/mqtt', icon: Radio, label: 'MQTT' },
  { to: '/settings', icon: Settings, label: '系统设置' },
]

export function Layout() {
  const { user, logout } = useAuth()
  const isNativeSSO = isFeishuEnv() || isDingTalkEnv()

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
          {navItems.map(item => (
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
