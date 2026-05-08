// tunnels.js — Web 隧道 (HTTP/HTTPS) + 透明隧道 (TCP/UDP) 管理
import { api } from './api.js';
import { setText, fmtBytes, esc, toast, activateSubpanel, emptyStateMarkup, renderVizBars, renderVizRing } from './main.js';

const WEB_TYPES = ['http', 'https'];
const STREAM_TYPES = ['tcp', 'udp'];
const ALL_TYPES = [...WEB_TYPES, ...STREAM_TYPES];
let tunnels = [];
let addContext = 'web'; // 'web' | 'stream'

export function initTunnels() {
  const modal = document.getElementById('tunnel-modal');

  // Web 隧道表格事件委托
  document.getElementById('web-tbody').addEventListener('click', handleAction);
  document.getElementById('stream-tbody').addEventListener('click', handleAction);

  // Web 隧道按钮
  document.getElementById('btn-add-web').addEventListener('click', () => openModal('web'));
  document.getElementById('btn-refresh-web').addEventListener('click', refreshList);

  // 透明隧道按钮
  document.getElementById('btn-add-stream').addEventListener('click', () => openModal('stream'));
  document.getElementById('btn-refresh-stream').addEventListener('click', refreshList);

  // Modal 按钮
  document.getElementById('btn-close-tunnel-modal').addEventListener('click', () => modal.style.display = 'none');
  document.getElementById('btn-cancel-tunnel').addEventListener('click', () => modal.style.display = 'none');
  modal.querySelector('.modal-backdrop').addEventListener('click', () => modal.style.display = 'none');

  document.getElementById('btn-submit-tunnel').addEventListener('click', submitTunnel);

  // 全局刷新回调
  window.__tunnelsRefresh = (allTunnels) => {
    tunnels = allTunnels.filter(t => ALL_TYPES.includes(t.type));
    renderWeb();
    renderStream();
  };
}

function handleAction(e) {
  const btn = e.target.closest('[data-action]');
  if (!btn) return;
  const action = btn.dataset.action;
  const name = btn.dataset.name;
  if (action === 'delete') {
    if (!confirm('确认删除隧道 ' + name + '?')) return;
    api.removeTunnel(name)
      .then(() => toast('已删除', 'success'))
      .catch(err => toast(err.message, 'error'));
  } else if (action === 'toggle') {
    const t = tunnels.find(t => t.name === name);
    if (!t) return;
    api.addTunnel({ name: t.name, type: t.type, target: t.target, domain: t.domain, listen_port: t.listen_port, enabled: !t.enabled })
      .then(() => toast(t.enabled ? '已禁用' : '已启用', 'success'))
      .catch(err => toast(err.message, 'error'));
  }
}

function openModal(ctx) {
  addContext = ctx;
  const sel = document.getElementById('tf-type');
  const types = ctx === 'web' ? WEB_TYPES : STREAM_TYPES;
  sel.innerHTML = types.map(t => `<option value="${t}">${t.toUpperCase()}</option>`).join('');
  document.getElementById('tf-name').value = '';
  document.getElementById('tf-target').value = '';
  document.getElementById('tunnel-modal').style.display = 'flex';
}

function submitTunnel() {
  const name = document.getElementById('tf-name').value.trim();
  const type = document.getElementById('tf-type').value;
  const target = document.getElementById('tf-target').value.trim();
  if (!name) { toast('名称不能为空', 'error'); return; }
  if (!target) { toast('目标不能为空', 'error'); return; }
  api.addTunnel({ name, type, target, enabled: true })
    .then(() => {
      document.getElementById('tunnel-modal').style.display = 'none';
      const panel = addContext === 'web' ? 'web' : 'stream';
      activateSubpanel(panel, panel + '-list-view');
      toast('隧道已添加', 'success');
    })
    .catch(e => toast(e.message, 'error'));
}

function refreshList() {
  api.listTunnels().then(list => {
    tunnels = list.filter(t => ALL_TYPES.includes(t.type));
    renderWeb();
    renderStream();
  }).catch(() => {});
}

function renderWeb() {
  const list = tunnels.filter(t => WEB_TYPES.includes(t.type));
  const online = list.filter(t => t.connected).length;
  const httpCount = list.filter(t => t.type === 'http').length;
  const httpsCount = list.filter(t => t.type === 'https').length;

  setText('web-total', String(list.length));
  setText('web-online', String(online));
  setText('web-http', String(httpCount));
  setText('web-https', String(httpsCount));
  setText('web-health-note', list.length ? `${online}/${list.length} 条在线` : '创建 Web 隧道后可查看在线率');
  renderVizBars('web-type-chart', [
    { label: 'HTTP', value: httpCount, color: '#22c55e' },
    { label: 'HTTPS', value: httpsCount, color: '#3b82f6' },
  ], '暂无 Web 隧道', '新增 HTTP/HTTPS 隧道后这里会显示协议占比。');
  renderVizRing('web-health-ring', online, list.length, '#22c55e');

  const tbody = document.getElementById('web-tbody');
  if (!list.length) {
    tbody.innerHTML = `<tr><td colspan="8" class="table-empty-cell">${emptyStateMarkup('暂无 Web 隧道', '创建 HTTP 或 HTTPS 映射，将外部请求转发到本地 Web 服务。', 'W')}</td></tr>`;
    return;
  }
  tbody.innerHTML = list.map(t => rowHtml(t)).join('');
}

function renderStream() {
  const list = tunnels.filter(t => STREAM_TYPES.includes(t.type));
  const online = list.filter(t => t.connected).length;
  const tcpCount = list.filter(t => t.type === 'tcp').length;
  const udpCount = list.filter(t => t.type === 'udp').length;

  setText('stream-total', String(list.length));
  setText('stream-online', String(online));
  setText('stream-tcp', String(tcpCount));
  setText('stream-udp', String(udpCount));
  setText('stream-health-note', list.length ? `${online}/${list.length} 条在线` : '创建透明隧道后可查看在线率');
  renderVizBars('stream-type-chart', [
    { label: 'TCP', value: tcpCount, color: '#f97316' },
    { label: 'UDP', value: udpCount, color: '#a855f7' },
  ], '暂无透明隧道', '新增 TCP/UDP 隧道后这里会显示协议占比。');
  renderVizRing('stream-health-ring', online, list.length, '#f97316');

  const tbody = document.getElementById('stream-tbody');
  if (!list.length) {
    tbody.innerHTML = `<tr><td colspan="8" class="table-empty-cell">${emptyStateMarkup('暂无透明隧道', '创建 TCP 或 UDP 映射，实现原始流量的直通转发。', 'S')}</td></tr>`;
    return;
  }
  tbody.innerHTML = list.map(t => rowHtml(t)).join('');
}

function accessUrl(t) {
  if (t.domain) return t.type === 'https' ? `https://${t.domain}` : `http://${t.domain}`;
  if (t.listen_port) {
    const host = (window.__tunnelServerAddr || '').split(':')[0];
    if (!host) return '-';
    if (t.type === 'http' || t.type === 'https') {
      const scheme = t.type === 'https' ? 'https' : 'http';
      return `${scheme}://${host}:${t.listen_port}`;
    }
    return `${host}:${t.listen_port}`;
  }
  const sa = window.__tunnelServerAddr || '';
  const host = sa.split(':')[0];
  if (!host || /^(\d{1,3}\.){3}\d{1,3}$/.test(host)) return '-';
  const nodeId = window.__tunnelNodeID || '';
  if (!nodeId) return '-';
  if (!isSafeSubdomainLabel(t.name)) return '-';
  const scheme = t.type === 'https' ? 'https' : 'http';
  return `${scheme}://${t.name}-${nodeId}.${host}`;
}

function isSafeSubdomainLabel(name) {
  return /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/i.test(name || '');
}

function rowHtml(t) {
  const statusHtml = t.connected
    ? '<span class="status on">运行中</span>'
    : '<span class="status off">离线</span>';
  const toggleBtn = t.enabled
    ? `<button class="btn btn-sm" data-action="toggle" data-name="${esc(t.name)}">禁用</button>`
    : `<button class="btn btn-primary btn-sm" data-action="toggle" data-name="${esc(t.name)}">启用</button>`;
  return `<tr>
    <td><strong>${esc(t.name)}</strong></td>
    <td><span class="badge badge-${t.type}">${esc(String(t.type || '')).toUpperCase()}</span></td>
    <td style="font-size:12px;color:#667085">${esc(t.target)}</td>
    <td style="font-size:12px;color:#4f46e5">${esc(accessUrl(t))}</td>
    <td>${statusHtml}</td>
    <td>${fmtBytes(t.bytes_in)}</td>
    <td>${fmtBytes(t.bytes_out)}</td>
    <td class="actions">${toggleBtn}<button class="btn btn-danger btn-sm" data-action="delete" data-name="${esc(t.name)}">删除</button></td>
  </tr>`;
}
