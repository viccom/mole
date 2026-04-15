import { cn } from '../lib/utils'

interface BadgeProps {
  variant?: 'success' | 'error' | 'info' | 'warning' | 'purple'
  className?: string
  children: React.ReactNode
}

const variantStyles = {
  success: 'bg-emerald-100 text-emerald-700 before:bg-emerald-500',
  error: 'bg-red-100 text-red-700 before:bg-red-500',
  info: 'bg-blue-100 text-blue-700 before:bg-blue-500',
  warning: 'bg-amber-100 text-amber-700 before:bg-amber-500',
  purple: 'bg-purple-100 text-purple-700 before:bg-purple-500',
}

export function Badge({ variant = 'info', className, children }: BadgeProps) {
  return (
    <span className={cn(
      'inline-flex items-center gap-1.5 px-2.5 py-0.5 rounded-full text-xs font-medium before:content-[""] before:w-1.5 before:h-1.5 before:rounded-full',
      variantStyles[variant],
      className
    )}>
      {children}
    </span>
  )
}
