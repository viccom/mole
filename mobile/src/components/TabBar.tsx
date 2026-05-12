import { useLocation, useNavigate } from 'react-router-dom'
import { Radio, GitBranch, Settings } from 'lucide-react'
import clsx from 'clsx'

const tabs = [
  { path: '/nodes', label: '节点', icon: Radio },
  { path: '/tunnels', label: '隧道', icon: GitBranch },
  { path: '/settings', label: '设置', icon: Settings },
]

export function TabBar() {
  const location = useLocation()
  const navigate = useNavigate()

  return (
    <nav className="fixed bottom-0 left-0 right-0 bg-white border-t border-gray-200 z-50 safe-area-bottom">
      <div className="flex justify-around items-center h-14">
        {tabs.map(tab => {
          const active = location.pathname.startsWith(tab.path)
          return (
            <button key={tab.path} onClick={() => navigate(tab.path)}
              className={clsx('flex flex-col items-center justify-center flex-1 h-full', active ? 'text-blue-600' : 'text-gray-400')}>
              <tab.icon className="w-5 h-5" />
              <span className="text-[10px] mt-0.5">{tab.label}</span>
            </button>
          )
        })}
      </div>
    </nav>
  )
}
