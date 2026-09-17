import { describe, expect, it } from 'vitest'
import type { Node, Tunnel, TunnelUsageItem } from '../types/api'
import { buildTabStats, p2pClientStatus, tunnelMatchesKeyword } from './TunnelsPage'

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

describe('p2pClientStatus', () => {
  const baseNode: Node = {
    id: 'node-1',
    name: 'street-a',
    status: 'online',
    owner_user_id: 'user-1',
    tunnel_count: 1,
  }
  const p2pTunnel: Tunnel = {
    name: 't',
    type: 'p2p',
    target: '127.0.0.1:8080',
    enabled: true,
    node_id: 'node-1',
    para: { room: 'myroom_2026_x' },
  }

  // 门禁内聚：非 p2p / 已禁用不显示徽章（禁用隧道 handler 必然停止，标「未运行」是误导）
  it('非 p2p 与已禁用的隧道返回 null', () => {
    expect(p2pClientStatus(baseNode, { ...p2pTunnel, type: 'webssh' })).toBeNull()
    expect(p2pClientStatus(baseNode, { ...p2pTunnel, enabled: false })).toBeNull()
  })

  // 数据源是客户端上报：节点缺失（拉取竞态）/离线/未上报必须与「未连通」区分，
  // 否则会误报故障。/tunnels 只列在线节点，node 缺失与离线同义
  it('节点缺失/离线/未上报不显示为连接故障', () => {
    expect(p2pClientStatus(undefined, p2pTunnel)).toEqual({ label: '节点离线', variant: 'info' })
    expect(p2pClientStatus({ ...baseNode, status: 'offline' }, p2pTunnel)).toEqual({ label: '节点离线', variant: 'info' })
    expect(p2pClientStatus(baseNode, p2pTunnel)).toEqual({
      label: '未上报',
      variant: 'info',
      title: '客户端尚未上报该隧道状态（刚启动或旧版本）',
    })
  })

  it('已连通 → success 且无 tooltip；running 但未连通 → warning 并透出客户端错误', () => {
    const connected = p2pClientStatus(
      { ...baseNode, client_statuses: [{ name: 't', type: 'p2p', running: true, connected: true }] },
      p2pTunnel,
    )
    expect(connected).toEqual({ label: '已连通', variant: 'success' })
    expect(connected?.title).toBeUndefined()

    const punching = p2pClientStatus(
      { ...baseNode, client_statuses: [{ name: 't', type: 'p2p', running: true, connected: false, error: 'udp-v4 attempt timeout' }] },
      p2pTunnel,
    )
    expect(punching?.label).toBe('未连通')
    expect(punching?.variant).toBe('warning')
    expect(punching?.title).toBe('udp-v4 attempt timeout')
  })

  // wire 真实形态：connected 键经 omitempty 省略（打洞中的隧道不发该字段），
  // 不能只按显式 connected:false 通过
  it('connected 键缺省（omitempty）同样判为未连通', () => {
    const st = p2pClientStatus(
      { ...baseNode, client_statuses: [{ name: 't', type: 'p2p', running: true }] },
      p2pTunnel,
    )
    expect(st?.label).toBe('未连通')
    expect(st?.variant).toBe('warning')
    expect(st?.title).toBe('会话未建立，打洞重试中')
  })

  // running=false 条目不携带 error（客户端 handler 存在时 Running 恒 true），
  // 成因多个（构建缺失/端口冲突/配置跳过），文案不得断言单一成因
  it('running=false → 未运行（error），文案列出多种成因', () => {
    const notRunning = p2pClientStatus(
      { ...baseNode, client_statuses: [{ name: 't', type: 'p2p', running: false }] },
      p2pTunnel,
    )
    expect(notRunning?.label).toBe('未运行')
    expect(notRunning?.variant).toBe('error')
    expect(notRunning?.title).toContain('-tags p2p')
    expect(notRunning?.title).toContain('端口冲突')
    expect(notRunning?.title).toContain('本地日志')
  })

  it('按 name+type 匹配，同名非 p2p 条目不误判', () => {
    const st = p2pClientStatus(
      { ...baseNode, client_statuses: [{ name: 't', type: 'webssh', running: true, connected: true }] },
      p2pTunnel,
    )
    expect(st?.label).toBe('未上报')
  })
})
