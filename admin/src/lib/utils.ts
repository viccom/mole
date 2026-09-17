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

export function formatBandwidth(bytesPerSec: number): string {
  if (bytesPerSec <= 0) return '-'
  if (bytesPerSec >= 1024 * 1024) return `${(bytesPerSec / 1024 / 1024).toFixed(1)} MB/s`
  if (bytesPerSec >= 1024) return `${(bytesPerSec / 1024).toFixed(0)} KB/s`
  return `${bytesPerSec} B/s`
}

export function getGatewayBase(): string {
  const parts = window.location.hostname.split('.')
  return parts.length > 2 ? parts.slice(1).join('.') : window.location.hostname
}

let cachedDefaultDomain: string | undefined

export async function fetchDefaultDomain(): Promise<string | undefined> {
  try {
    const res = await fetch('/api/v1/version')
    const data = await res.json()
    cachedDefaultDomain = data?.data?.default_domain || data?.default_domain || ''
    return cachedDefaultDomain || undefined
  } catch { return undefined }
}

export function getDefaultDomain(): string | undefined {
  return cachedDefaultDomain || undefined
}

export function tunnelAccessUrl(tunnel: { type: string; domain?: string; name?: string; node_id?: string; listen_port?: number; para?: { room?: string } }, defaultDomain?: string): string {
  const dd = defaultDomain || cachedDefaultDomain
  if (tunnel.type === 'http' || tunnel.type === 'https') {
    const scheme = tunnel.type
    if (tunnel.domain) return `${scheme}://${tunnel.domain}`
    const base = dd || getGatewayBase()
    return `${scheme}://${tunnel.name}-${tunnel.node_id}.${base}`
  }
  // p2p 无接入地址（两端打洞直连，不经网关）；room 才是配对的唯一业务标识，
  // 展示它便于用户确认该节点加入了哪个 room
  if (tunnel.type === 'p2p') {
    const room = tunnel.para?.room
    return typeof room === 'string' && room ? room : '-'
  }  if (tunnel.listen_port) {
    const host = dd || window.location.hostname
    return `${tunnel.type}://${host}:${tunnel.listen_port}`
  }
  return '-'
}
