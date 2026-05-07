// main.js — 入口、Tab 切换、仪表盘、全局刷新
import { api } from './api.js';
import { initTunnels } from './tunnels.js';
import { initSer2MQ } from './ser2mq.js';
import { initVPN } from './vpn.js';

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

export function emptyStateMarkup(title, desc, icon = 'i', dark = false) {
  return `<div class="empty-state${dark ? ' empty-state-dark' : ''}">
    <div class="empty-state-icon">${esc(icon)}</div>
    <div class="empty-state-title">${esc(title)}</div>
    <div class="empty-state-desc">${esc(desc)}</div>
  </div>`;
}

export function renderVizBars(containerId, items, emptyTitle, emptyDesc) {
  const el = document.getElementById(containerId);
  if (!el) return;

  const valid = (items || []).filter(item => item && item.value > 0);
  if (!valid.length) {
    el.innerHTML = emptyStateMarkup(emptyTitle, emptyDesc, '0');
    return;
  }

  const total = valid.reduce((sum, item) => sum + item.value, 0) || 1;
  el.innerHTML = valid.map(item => {
    const width = Math.max(8, Math.round(item.value / total * 100));
    return `<div class="viz-item">
      <span class="viz-item-label">${esc(item.label)}</span>
      <span class="viz-item-bar"><span class="viz-item-fill" style="--w:${width}%;--c:${item.color || '#4f46e5'}"></span></span>
      <span class="viz-item-value">${esc(String(item.value))}</span>
    </div>`;
  }).join('');
}

export function renderVizRing(containerId, value, total, color = '#4f46e5') {
  const el = document.getElementById(containerId);
  if (!el) return;

  const safeTotal = total > 0 ? total : 0;
  const percent = safeTotal ? Math.round((value / safeTotal) * 100) : 0;
  el.style.setProperty('--ring-gradient', `conic-gradient(${color} 0 ${percent}%, #e2e8f0 ${percent}% 100%)`);
  el.innerHTML = `<span>${percent}%</span>`;
}

export function setText(id, value) {
  const el = document.getElementById(id);
  if (el) el.textContent = value;
}

export function activateTopTab(tabName) {
  document.querySelectorAll('.tab').forEach(t => t.classList.remove('active'));
  document.querySelectorAll('.panel').forEach(p => p.classList.remove('active'));
  const tab = document.querySelector(`[data-tab="${tabName}"]`);
  const panel = document.getElementById('panel-' + tabName);
  if (tab) tab.classList.add('active');
  if (panel) panel.classList.add('active');
}

export function activateSubpanel(panelName, targetId) {
  const panel = document.getElementById('panel-' + panelName);
  if (!panel) return;

  panel.querySelectorAll('.subnav-item').forEach(item => {
    item.classList.toggle('active', item.dataset.target === targetId);
  });
  panel.querySelectorAll('.subview').forEach(view => {
    view.classList.toggle('active', view.id === targetId);
  });
}

// ===== Tab 切换 =====
document.querySelectorAll('.tab').forEach(tab => {
  tab.addEventListener('click', () => {
    activateTopTab(tab.dataset.tab);
  });
});

document.querySelectorAll('.subnav-item').forEach(item => {
  item.addEventListener('click', () => {
    activateSubpanel(item.dataset.panel, item.dataset.target);
  });
});

// ===== 仪表盘渲染 =====
function renderDashboard(status) {
  const badge = document.getElementById('conn-badge');
  const refreshInfo = document.getElementById('refresh-info');
  const tunnelList = Array.isArray(status.tunnels) ? status.tunnels : [];
  const onlineTunnelCount = tunnelList.filter(t => t && t.connected).length;
  const connectedText = status.connected ? '已连接' : '已断开';

  if (status.connected) {
    badge.textContent = connectedText;
    badge.className = 'conn-badge online';
  } else {
    badge.textContent = connectedText;
    badge.className = 'conn-badge offline';
  }

  document.getElementById('dashboard-hero').classList.toggle('online', !!status.connected);
  document.getElementById('dashboard-hero').classList.toggle('offline', !status.connected);

  setText('info-node', status.node_id || '-');
  setText('info-server', status.server_addr || '-');
  setText('info-node-secondary', status.node_id || '-');
  setText('info-server-secondary', status.server_addr || '-');
  setText('info-conn-secondary', connectedText);
  setText('overview-connection', status.connected ? '链路已建立' : '等待服务端握手');
  setText('overview-conn-text', connectedText);
  setText('overview-tunnel-count', `${tunnelList.length} 条隧道`);
  setText('overview-online-count', String(onlineTunnelCount));

  document.getElementById('stats-bar').innerHTML =
    statBox('隧道数', tunnelList.length) +
    statBox('TCP 流入', fmtBytes(status.tcp_bytes_in)) +
    statBox('TCP 流出', fmtBytes(status.tcp_bytes_out)) +
    statBox('HTTP 流入', fmtBytes(status.http_bytes_in)) +
    statBox('HTTP 流出', fmtBytes(status.http_bytes_out));

  refreshInfo.classList.remove('error');
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

    // 版本信息（独立请求，失败不影响主流程）
    api.getVersion().then(v => {
      if (!v) return;
      setText('info-version', `${v.version} (${v.git_hash})`);
      setText('info-build-date', v.build_date || '-');
      setText('info-binary-path', v.binary_path || '-');
    }).catch(() => {});

    // 传递 nodeID 给 ser2mq 模块
    if (window.__ser2mqNodeID && status.node_id) window.__ser2mqNodeID(status.node_id);

    // 分发给各模块
    window.__tunnelServerAddr = status.server_addr || '';
    window.__tunnelNodeID = status.node_id || '';
    if (window.__tunnelsRefresh) window.__tunnelsRefresh(tunnelList);
    if (window.__ser2mqRefresh) window.__ser2mqRefresh(tunnelList);
    if (window.__vpnRefresh) window.__vpnRefresh(tunnelList);

    setText('refresh-info', '最近刷新: ' + new Date().toLocaleTimeString('zh-CN'));
  } catch (e) {
    const badge = document.getElementById('conn-badge');
    badge.textContent = '数据过期';
    badge.className = 'conn-badge offline';
    const hero = document.getElementById('dashboard-hero');
    hero.classList.remove('online');
    hero.classList.add('offline');
    setText('overview-connection', '数据同步失败');
    setText('overview-conn-text', '数据过期');
    setText('info-conn-secondary', '数据过期');
    setText('refresh-info', '刷新失败: ' + (e.message || e));
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
initVPN();
refreshData().then(scheduleRefresh);
