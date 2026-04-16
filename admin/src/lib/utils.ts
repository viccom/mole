import { clsx, type ClassValue } from 'clsx'

export function cn(...inputs: ClassValue[]) {
  return clsx(inputs)
}

export function getErrorMessage(err: unknown, fallback = '操作失败'): string {
  if (err instanceof Error) return err.message || fallback
  if (typeof err === 'string') return err || fallback
  return fallback
}

export function formatTimeAgo(dateStr?: string | null): string {
  if (!dateStr) return '-'
  const date = new Date(dateStr)
  if (isNaN(date.getTime())) return '-'
  const diff = Math.floor((Date.now() - date.getTime()) / 1000)
  if (diff < 0) return '刚刚'
  if (diff < 60) return diff + '秒前'
  if (diff < 3600) return Math.floor(diff / 60) + '分钟前'
  if (diff < 86400) return Math.floor(diff / 3600) + '小时前'
  return Math.floor(diff / 86400) + '天前'
}

export function formatUptime(seconds: number): string {
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor((seconds % 86400) / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  return (days > 0 ? days + '天 ' : '') + hours + '时' + minutes + '分'
}

export function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1)
  const val = bytes / Math.pow(1024, i)
  return val < 10 ? val.toFixed(1) + ' ' + units[i] : Math.round(val) + ' ' + units[i]
}

export function getGatewayBase(): string {
  const parts = window.location.hostname.split('.')
  return parts.length > 2 ? parts.slice(1).join('.') : window.location.hostname
}

export function tunnelAccessUrl(tunnel: { type: string; domain?: string; name?: string; node_id?: string; listen_port?: number }): string {
  const base = getGatewayBase()
  const proto = window.location.protocol === 'https:' ? 'https' : 'http'
  if (tunnel.type === 'http') {
    const host = tunnel.domain || `${tunnel.name}-${tunnel.node_id}.${base}`
    return `${proto}://${host}`
  }
  if (tunnel.listen_port) {
    return `${tunnel.type}://${window.location.hostname}:${tunnel.listen_port}`
  }
  return '-'
}
