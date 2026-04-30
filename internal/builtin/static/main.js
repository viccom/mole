// main.js — 入口、Tab 切换、仪表盘、全局刷新
import { api } from './api.js';
import { initTunnels } from './tunnels.js';
import { initSer2MQ } from './ser2mq.js';

// ===== 工具函数 =====
export function fmtBytes(b) {
  if (!b || b === 0) return '0 B';
  const u = ['B', 'KB', 'MB', 'GB'];
  let i = 0;
  while (b >= 1024 && i < u.length - 1) { b /= 1024; i++; }
  return b.toFixed(b >= 100 ? 0 : b >= 10 ? 1 : 2) + ' ' + u[i];
}

export function esc(s) {
  const el = document.createElement('span');
  el.textContent = s || '';
  return el.innerHTML;
}

export function toast(msg, type = 'info') {
  const c = document.getElementById('toast-container');
  const el = document.createElement('div');
  el.className = 'toast ' + type;
  el.textContent = msg;
  c.appendChild(el);
  setTimeout(() => el.remove(), 3000);
}

// ===== Tab 切换 =====
document.querySelectorAll('.tab').forEach(tab => {
  tab.addEventListener('click', () => {
    document.querySelectorAll('.tab').forEach(t => t.classList.remove('active'));
    document.querySelectorAll('.panel').forEach(p => p.classList.remove('active'));
    tab.classList.add('active');
    document.getElementById('panel-' + tab.dataset.tab).classList.add('active');
  });
});

// ===== 仪表盘渲染 =====
function renderDashboard(status) {
  const badge = document.getElementById('conn-badge');
  if (status.connected) {
    badge.textContent = '已连接';
    badge.className = 'conn-badge online';
  } else {
    badge.textContent = '已断开';
    badge.className = 'conn-badge offline';
  }
  document.getElementById('info-node').textContent = status.node_id || '-';
  document.getElementById('info-server').textContent = status.server_addr || '-';

  document.getElementById('stats-bar').innerHTML =
    statBox('隧道数', status.tunnels ? status.tunnels.length : 0) +
    statBox('TCP 流入', fmtBytes(status.tcp_bytes_in)) +
    statBox('TCP 流出', fmtBytes(status.tcp_bytes_out)) +
    statBox('HTTP 流入', fmtBytes(status.http_bytes_in)) +
    statBox('HTTP 流出', fmtBytes(status.http_bytes_out));
}

function statBox(label, value) {
  return `<div class="stat-box"><div class="label">${esc(label)}</div><div class="value">${esc(String(value))}</div></div>`;
}

// ===== 全局刷新 =====
let refreshPending = false;
let refreshTimer = null;

async function refreshData() {
  if (refreshPending) return;
  refreshPending = true;
  try {
    const [status, tunnelList] = await Promise.all([
      api.getStatus(),
      api.listTunnels()
    ]);
    renderDashboard(status);

    // 传递 nodeID 给 ser2mq 模块
    if (window.__ser2mqNodeID && status.node_id) window.__ser2mqNodeID(status.node_id);

    // 分发给各模块
    if (window.__tunnelsRefresh) window.__tunnelsRefresh(tunnelList);
    if (window.__ser2mqRefresh) window.__ser2mqRefresh(tunnelList);

    document.getElementById('refresh-info').textContent = '最近刷新: ' + new Date().toLocaleTimeString('zh-CN');
  } catch (e) {
    const badge = document.getElementById('conn-badge');
    badge.textContent = '数据过期';
    badge.className = 'conn-badge offline';
    document.getElementById('refresh-info').textContent = '刷新失败: ' + (e.message || e);
    document.getElementById('refresh-info').classList.add('error');
    console.error('refresh error:', e);
  } finally {
    refreshPending = false;
  }
}

function scheduleRefresh() {
  if (refreshTimer) clearTimeout(refreshTimer);
  refreshTimer = setTimeout(() => {
    refreshData().finally(scheduleRefresh);
  }, 3000);
}

// ===== 初始化 =====
initTunnels();
initSer2MQ();
refreshData().then(scheduleRefresh);
