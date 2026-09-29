// api.js — 桌面版统一 API 客户端（Wails + 本地内置 HTTP）
let builtinBaseURL = 'http://127.0.0.1:18080';
const DEFAULT_TIMEOUT_MS = 5000;

export async function initDesktopAPI() {
  if (window.go?.main?.App?.GetBuiltinHTTPBaseURL) {
    try {
      builtinBaseURL = await window.go.main.App.GetBuiltinHTTPBaseURL();
    } catch (_) {}
  } else if (window.go?.main?.App?.GetBuiltinHTTPPort) {
    try {
      builtinBaseURL = 'http://' + await window.go.main.App.GetBuiltinHTTPPort();
    } catch (_) {}
  }
  return builtinBaseURL;
}

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
    const res = await fetch(builtinBaseURL + path, opts);
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

async function callWailsJSON(methodName, ...args) {
  const fn = window.go?.main?.App?.[methodName];
  if (!fn) {
    throw new Error(`Wails method ${methodName} is unavailable`);
  }
  const result = await fn(...args);
  return typeof result === 'string' ? JSON.parse(result) : result;
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
  },

  // 节点管理（Wails 绑定）
  listNodes() {
    return callWailsJSON('ListNodes');
  },
  getCurrentNode() {
    return callWailsJSON('GetCurrentNode');
  },
  addNode(node) {
    return callWailsJSON('AddNode', JSON.stringify(node));
  },
  updateNode(id, node) {
    return callWailsJSON('UpdateNode', id, JSON.stringify(node));
  },
  removeNode(id) {
    return callWailsJSON('RemoveNode', id);
  },
  switchNode(id) {
    return callWailsJSON('SwitchNode', id);
  },
  async getBuiltinHTTPPort() {
    if (window.go?.main?.App?.GetBuiltinHTTPPort) {
      return await window.go.main.App.GetBuiltinHTTPPort();
    }
    return builtinBaseURL.replace(/^http:\/\//, '');
  }
};

export function buildTunnelStreamURL(name, options = {}) {
  const tail = options.tail ?? 20;
  return builtinBaseURL + '/api/tunnels/' + encodeURIComponent(name) + '/stream?tail=' + tail;
}
