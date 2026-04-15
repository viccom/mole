import { type LucideIcon } from 'lucide-react'

interface StatCardProps {
  icon: LucideIcon
  iconBg: string
  iconColor: string
  value: string
  label: string
}

export function StatCard({ icon: Icon, iconBg, iconColor, value, label }: StatCardProps) {
  return (
    <div className="bg-white rounded-lg shadow-sm p-5 flex items-center gap-4">
      <div className={`w-12 h-12 rounded-xl flex items-center justify-center flex-shrink-0 ${iconBg}`}>
        <Icon className={`w-6 h-6 ${iconColor}`} />
      </div>
      <div>
        <div className="text-2xl font-bold leading-tight">{value}</div>
        <div className="text-sm text-gray-500">{label}</div>
      </div>
    </div>
  )
}
