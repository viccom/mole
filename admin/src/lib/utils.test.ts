import { describe, expect, it } from 'vitest'
import { formatBytes, tunnelAccessUrl } from './utils'

describe('utils', () => {
  it('formats bytes in a readable way', () => {
    expect(formatBytes(0)).toBe('0 B')
    expect(formatBytes(1536)).toBe('1.5 KB')
    expect(formatBytes(15 * 1024 * 1024)).toBe('15 MB')
  })

  it('builds HTTP tunnel access URL from node and gateway host', () => {
    expect(tunnelAccessUrl({
      type: 'http',
      name: 'demo',
      node_id: 'node-a',
    })).toBe('http://demo-node-a.localhost')
  })

  it('builds HTTPS tunnel access URL from custom domain', () => {
    expect(tunnelAccessUrl({
      type: 'https',
      domain: 'app.example.com',
      name: 'ignored',
      node_id: 'ignored',
    })).toBe('https://app.example.com')
  })

  it('builds TCP tunnel access URL from listen port', () => {
    expect(tunnelAccessUrl({
      type: 'tcp',
      listen_port: 9000,
    })).toBe('tcp://localhost:9000')
  })

  // p2p 不经网关、无接入地址；room 是配对唯一标识，展示它才能看出节点加入了哪个 room
  it('shows room as access info for p2p tunnels', () => {
    expect(tunnelAccessUrl({
      type: 'p2p',
      name: 'p2p-a',
      node_id: 'node-a',
      para: { room: 'myroom_2026_x' },
    })).toBe('myroom_2026_x')
  })

  it('falls back to - for p2p without room', () => {
    expect(tunnelAccessUrl({ type: 'p2p', name: 'p2p-a' })).toBe('-')
    expect(tunnelAccessUrl({ type: 'p2p', name: 'p2p-a', para: { room: '' } })).toBe('-')
  })
})
