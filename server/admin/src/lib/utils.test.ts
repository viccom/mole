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
})
