// api.js — 统一 API 客户端
const BASE = '';

async function request(method, path, body) {
  const opts = { method, headers: {} };
  if (body !== undefined) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(body);
  }
  const res = await fetch(BASE + path, opts);
  const data = await res.json();
  if (data.error) {
    throw new Error(data.error);
  }
  return data;
}

export const api = {
  // 全局状态
  getStatus() {
    return request('GET', '/api/status');
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
  }
};
