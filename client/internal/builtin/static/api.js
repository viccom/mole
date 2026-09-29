// api.js — 统一 API 客户端
const BASE = '';
const DEFAULT_TIMEOUT_MS = 5000;

export async function request(method, path, body, options = {}) {
  const timeoutMs = options.timeoutMs ?? DEFAULT_TIMEOUT_MS;
  const controller = new AbortController();
  const opts = { method, headers: {}, signal: controller.signal };
  if (body !== undefined) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(body);
  }

  const timeoutId = timeoutMs > 0
    ? setTimeout(() => controller.abort(), timeoutMs)
    : 0;

  try {
    const res = await fetch(BASE + path, opts);
    const contentType = (res.headers && res.headers.get && res.headers.get('content-type')) || '';
    const isJSON = contentType.includes('application/json');
    const payload = isJSON
      ? await res.json()
      : await res.text();

    if (!res.ok) {
      const message = isJSON
        ? payload.error || JSON.stringify(payload)
        : payload || res.statusText || 'request failed';
      throw new Error(`HTTP ${res.status}: ${message}`);
    }

    if (isJSON && payload && payload.error) {
      throw new Error(payload.error);
    }

    return payload;
  } catch (error) {
    if (error && error.name === 'AbortError') {
      throw new Error(`Request timed out after ${timeoutMs}ms`);
    }
    throw error;
  } finally {
    if (timeoutId) {
      clearTimeout(timeoutId);
    }
  }
}

export const api = {
  // 全局状态
  getStatus() {
    return request('GET', '/api/status');
  },

  // 版本与系统信息
  getVersion() {
    return request('GET', '/api/version');
  },

  // 统一隧道 API
  listTunnels() {
    return request('GET', '/api/tunnels');
  },
  getTunnel(name) {
    return request('GET', '/api/tunnels/' + encodeURIComponent(name));
  },
  addTunnel(tunnel) {
    return request('POST', '/api/tunnels', tunnel);
  },
  removeTunnel(name) {
    return request('DELETE', '/api/tunnels/' + encodeURIComponent(name));
  },

  // 类型特定操作
  startTunnel(name) {
    return request('POST', '/api/tunnels/' + encodeURIComponent(name) + '/start');
  },
  stopTunnel(name) {
    return request('POST', '/api/tunnels/' + encodeURIComponent(name) + '/stop');
  },
  getTunnelLogs(name) {
    return request('GET', '/api/tunnels/' + encodeURIComponent(name) + '/logs');
  },

  // VPN 特定操作
  startVPNTunnel(name) {
    return request('POST', '/api/tunnels/' + encodeURIComponent(name) + '/start');
  },
  stopVPNTunnel(name) {
    return request('POST', '/api/tunnels/' + encodeURIComponent(name) + '/stop');
  },
  getVPNPeers(name) {
    return request('GET', '/api/tunnels/' + encodeURIComponent(name) + '/peers');
  },
  getVNTRoutes(name) {
    return request('GET', '/api/tunnels/' + encodeURIComponent(name) + '/routes');
  },
  getVPNChart(name) {
    return request('GET', '/api/tunnels/' + encodeURIComponent(name) + '/chart');
  }
};

export function buildTunnelStreamURL(name, options = {}) {
  const tail = options.tail ?? 20;
  return '/api/tunnels/' + encodeURIComponent(name) + '/stream?tail=' + tail;
}
