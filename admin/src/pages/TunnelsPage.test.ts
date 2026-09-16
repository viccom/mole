import { describe, expect, it } from 'vitest'
import type { Tunnel, TunnelUsageItem } from '../types/api'
import { buildTabStats, tunnelMatchesKeyword } from './TunnelsPage'

describe('buildTabStats', () => {
  it('将在线且启用的隧道计为活跃，而不是依赖当前连接数', () => {
    const tunnels: Tunnel[] = [
      {
        name: 'ser-idle',
        type: 'ser2tcp',
        target: 'COM3',
        enabled: true,
        node_id: 'node-1',
        para: { mode: 'server', address: ':5000' },
      },
      {
        name: 'ser-disabled',
        type: 'ser2udp',
        target: 'COM4',
        enabled: false,
        node_id: 'node-1',
        para: { mode: 'client', address: '10.0.0.8:6000' },
      },
    ]

    const usageMap: Record<string, TunnelUsageItem> = {
      'node-1:ser-idle': {
        name: 'ser-idle',
        type: 'ser2tcp',
        target: 'COM3',
        enabled: true,
        node_id: 'node-1',
        node_status: 'online',
        owner_user_id: 'user-1',
        bytes_in: 0,
        bytes_out: 0,
        total_connections: 0,
        active_connections: 0,
      },
      'node-1:ser-disabled': {
        name: 'ser-disabled',
        type: 'ser2udp',
        target: 'COM4',
        enabled: false,
        node_id: 'node-1',
        node_status: 'online',
        owner_user_id: 'user-1',
        bytes_in: 0,
        bytes_out: 0,
        total_connections: 3,
        active_connections: 2,
      },
    }

    expect(buildTabStats(tunnels, usageMap)).toEqual({
      total: 2,
      enabled: 1,
      active: 1,
      count: 2,
    })
  })
})

describe('tunnelMatchesKeyword', () => {
  const tunnel: Tunnel = {
    name: 'serial-gateway',
    type: 'ser2tcp',
    target: 'COM3',
    enabled: true,
    node_id: 'node-1',
    para: {
      mode: 'server',
      address: ':5000',
    },
  }

  it('支持搜索 ser2net 展示地址', () => {
    expect(tunnelMatchesKeyword(tunnel, '5000')).toBe(true)
  })

  it('支持搜索 ser2net 模式', () => {
    expect(tunnelMatchesKeyword(tunnel, 'server')).toBe(true)
  })

  // p2p 隧道名两端可自由起名，room 才是配对的唯一标识，必须能搜到（大小写不敏感）
  it('支持按 p2p room 搜索', () => {
    const p2pTunnel: Tunnel = {
      name: 'p2p-a',
      type: 'p2p',
      target: '127.0.0.1:8080',
      enabled: true,
      node_id: 'node-1',
      para: { room: 'MyRoom_2026_x', mappings: [{ protocol: 'tcp', local_port: 18080, target_host: '127.0.0.1', target_port: 8080 }] },
    }

    expect(tunnelMatchesKeyword(p2pTunnel, 'myroom_2026_x')).toBe(true)
    expect(tunnelMatchesKeyword(p2pTunnel, 'p2p')).toBe(true)
    expect(tunnelMatchesKeyword(p2pTunnel, 'other-room')).toBe(false)
  })
})
