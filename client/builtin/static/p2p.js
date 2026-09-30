// p2p.js — P2P 打洞直连隧道管理（连接状态 + 逐端口映射监测）
import { api } from './api.js';
import { setText, fmtBytes, esc, toast, activateSubpanel, emptyStateMarkup, renderVizBars, renderVizRing } from './main.js';

// 模式链选项与显示名（对齐 admin 端 TunnelFormModal；DefaultModes = 前四）
const P2P_MODE_OPTIONS = ['lan', 'tcp-v6', 'udp-v6', 'udp-v4', 'tcp-v4', 'v4-relay'];
const P2P_MODE_LABELS = {
  'lan': '局域网',
  'tcp-v6': 'TCP v6',
  'udp-v6': 'UDP v6',
  'udp-v4': 'UDP v4',
  'tcp-v4': 'TCP v4',
  'v4-relay': 'v4 中继',
};
const P2P_ROOM_REGEXP = /^[a-zA-Z0-9_-]{8,32}$/;

let tunnels = [];
let editingName = null;
let currentDetailName = null;
let selectedModes = new Set();

export function initP2P() {
  document.getElementById('p2p-tbody').addEventListener('click', handleAction);
  document.getElementById('btn-add-p2p').addEventListener('click', showAddForm);
  document.getElementById('btn-add-p2p-secondary').addEventListener('click', showAddForm);
  document.getElementById('btn-refresh-p2p').addEventListener('click', refreshList);
  document.getElementById('btn-cancel-p2p').addEventListener('click', hideForm);
  document.getElementById('btn-save-p2p').addEventListener('click', saveForm);
  document.getElementById('btn-close-p2p-detail').addEventListener('click', () => {
    currentDetailName = null;
    $('p2p-detail').style.display = 'none';
    $('p2p-detail-empty').style.display = '';
  });

  document.getElementById('pf-modes').addEventListener('click', (e) => {
    const chip = e.target.closest('[data-mode]');
    if (!chip) return;
    const mode = chip.dataset.mode;
    if (selectedModes.has(mode)) selectedModes.delete(mode);
    else selectedModes.add(mode);
    renderModeChips();
  });

  document.getElementById('btn-add-pf-mapping').addEventListener('click', () => {
    const rows = collectMappingInputs();
    rows.push({ protocol: 'tcp', local_port: '', target_host: '', target_port: '' });
    renderMappingRows(rows);
  });

  document.getElementById('pf-mappings-rows').addEventListener('click', (e) => {
    const btn = e.target.closest('[data-action="remove-mapping"]');
    if (!btn) return;
    const rows = collectMappingInputs();
    rows.splice(parseInt(btn.dataset.index, 10), 1);
    renderMappingRows(rows);
  });

  window.__p2pRefresh = (allTunnels) => {
    tunnels = allTunnels.filter(t => t.type === 'p2p');
    render();
  };
}

function handleAction(e) {
  const btn = e.target.closest('[data-action]');
  if (!btn) return;
  const action = btn.dataset.action;
  const name = btn.dataset.name;

  if (action === 'delete') {
    if (!confirm('确认删除 P2P 隧道 ' + name + '?')) return;
    api.removeTunnel(name)
      .then(() => toast('已删除', 'success'))
      .catch(err => toast(err.message, 'error'));
  } else if (action === 'toggle') {
    const t = tunnels.find(t => t.name === name);
    if (!t) return;
    // p2p 的 Para 无 enable 字段（启停在 Tunnel.Enabled 顶层），原样回传即可
    const para = Object.assign({}, t.para || {});
    api.addTunnel({ name: t.name, type: 'p2p', target: t.target, enabled: !t.enabled, para })
      .then(() => toast(t.enabled ? '已禁用' : '已启用', 'success'))
      .catch(err => toast(err.message, 'error'));
  } else if (action === 'edit') {
    const t = tunnels.find(t => t.name === name);
    if (t) showEditForm(t);
  } else if (action === 'detail') {
    showDetail(name);
  }
}

function render() {
  const banner = $('p2p-build-banner');
  if (banner) banner.style.display = window.__p2pBuild === false ? 'block' : 'none';

  const onlineCount = tunnels.filter(t => t.connected).length;
  let mappingUp = 0, mappingTotal = 0, reconnects = 0;
  tunnels.forEach(t => {
    const s = t.status || {};
    const rows = s.mappings || [];
    rows.forEach(m => {
      if (m.up) mappingUp++;
    });
    // 本端配置行基线：常态取会话状态行（含离线行），无 handler 时退回 para 计数
    const cfgCount = ((t.para || {}).mappings || []).length;
    const liveNonRemote = rows.filter(m => !m.remote).length;
    mappingTotal += Math.max(cfgCount, liveNonRemote);
    reconnects += s.reconnects || 0;
  });

  setText('p2p-total', String(tunnels.length));
  setText('p2p-online', String(onlineCount));
  setText('p2p-mappings', `${mappingUp}/${mappingTotal}`);
  setText('p2p-reconnects', String(reconnects));
  setText('p2p-health-note', tunnels.length ? `${onlineCount}/${tunnels.length} 条连通，${mappingUp} 条映射在线` : '创建 P2P 隧道后可查看连通状态');

  renderVizBars('p2p-state-chart', [
    { label: '连通', value: onlineCount, color: '#10b981' },
    { label: '断开', value: tunnels.length - onlineCount, color: '#94a3b8' },
  ], '暂无隧道', '新增 P2P 隧道后这里会显示连通分布。');
  renderVizRing('p2p-health-ring', onlineCount, tunnels.length, '#10b981');

  const tbody = document.getElementById('p2p-tbody');
  if (!tunnels.length) {
    tbody.innerHTML = `<tr><td colspan="7" class="table-empty-cell">${emptyStateMarkup('暂无 P2P 隧道', '两端配置同一 Room 即可配对直连，流量不经服务端网关中转。', 'P')}</td></tr>`;
    return;
  }
  tbody.innerHTML = tunnels.map(rowHtml).join('');

  // 详情打开时随 3s 刷新同步重绘（vpn.js 详情模式）
  if (currentDetailName && $('p2p-detail').style.display !== 'none') {
    const t = tunnels.find(t => t.name === currentDetailName);
    if (t) renderDetail(t);
  }
}

function rowHtml(t) {
  const s = t.status || {};
  const para = t.para || {};
  const online = !!t.connected;
  const statusHtml = online
    ? '<span class="status on">连通</span>'
    : '<span class="status off">断开</span>';
  const errorInfo = s.error ? `<span class="badge badge-err" title="${esc(s.error)}">错误</span>` : '';

  // 映射概览（明细在详情页）：本端配置行在线数 / 纯会话端的对端远程映射数
  const rows = s.mappings || [];
  const cfgCount = (para.mappings || []).length;
  const modeCell = s.mode
    ? `${esc(s.mode)}${cfgCount > 0 ? ` · ${rows.filter(m => m.up).length}/${cfgCount} 映射` : ''}`
    : '默认链';

  let actions = '';
  if (t.enabled) {
    actions += `<button class="btn btn-sm" data-action="toggle" data-name="${esc(t.name)}">禁用</button>`;
  } else {
    actions += `<button class="btn btn-primary btn-sm" data-action="toggle" data-name="${esc(t.name)}">启用</button>`;
  }
  actions += `<button class="btn btn-sm" data-action="edit" data-name="${esc(t.name)}">编辑</button>`;
  actions += `<button class="btn btn-sm" data-action="detail" data-name="${esc(t.name)}">详情</button>`;
  actions += `<button class="btn btn-danger btn-sm" data-action="delete" data-name="${esc(t.name)}">删除</button>`;

  return `<tr>
    <td><span class="table-primary">${esc(t.name)}</span><span class="table-meta">${esc(para.room || '-')}</span></td>
    <td>${statusHtml}${errorInfo}</td>
    <td style="font-size:12px">${modeCell}</td>
    <td>${fmtBytes(s.bytes_in || t.bytes_in)}</td>
    <td>${fmtBytes(s.bytes_out || t.bytes_out)}</td>
    <td style="font-size:12px">${s.reconnects || 0}</td>
    <td class="actions">${actions}</td>
  </tr>`;
}

function showAddForm() {
  activateSubpanel('p2p', 'p2p-form-view');
  editingName = null;
  $('p2p-form-title').textContent = '新增 P2P 隧道';
  $('pf-name').value = '';
  $('pf-name').disabled = false;
  $('pf-room').value = '';
  $('pf-relay-server').value = '';
  $('pf-mqtt-brokers').value = '';
  $('pf-stun-servers').value = '';
  $('pf-enable').checked = true;
  selectedModes = new Set();
  renderModeChips();
  renderMappingRows([]);
  $('p2p-form').style.display = 'block';
}

function showEditForm(t) {
  activateSubpanel('p2p', 'p2p-form-view');
  editingName = t.name;
  $('p2p-form-title').textContent = '编辑 P2P 隧道';
  $('pf-name').value = t.name;
  $('pf-name').disabled = true;
  $('pf-enable').checked = t.enabled;

  // Para 全字段回填（空即省略是契约默认值信号，编辑时原样还原）
  const para = t.para || {};
  $('pf-room').value = para.room || '';
  $('pf-relay-server').value = para.relay_server || '';
  $('pf-mqtt-brokers').value = (para.mqtt_brokers || []).join(', ');
  $('pf-stun-servers').value = (para.stun_servers || []).join(', ');
  selectedModes = new Set(para.modes || []);
  renderModeChips();
  renderMappingRows((para.mappings || []).map(m => ({
    protocol: m.protocol || 'tcp',
    local_port: m.local_port != null ? String(m.local_port) : '',
    target_host: m.target_host || '',
    target_port: m.target_port != null ? String(m.target_port) : '',
  })));
  $('p2p-form').style.display = 'block';
}

function hideForm() {
  $('p2p-form').style.display = 'none';
  activateSubpanel('p2p', 'p2p-list-view');
  editingName = null;
}

function renderModeChips() {
  const wrap = $('pf-modes');
  wrap.innerHTML = P2P_MODE_OPTIONS.map(mode =>
    `<button type="button" class="pf-mode-chip${selectedModes.has(mode) ? ' active' : ''}" data-mode="${mode}">${esc(P2P_MODE_LABELS[mode] || mode)}</button>`
  ).join('');
  $('pf-relay-row').style.display = selectedModes.has('v4-relay') ? '' : 'none';
}

// renderMappingRows 整体重建（防编辑残留 stale 行）；行值为字符串（编辑回填用）
function renderMappingRows(rows) {
  const wrap = $('pf-mappings-rows');
  wrap.innerHTML = (rows || []).map((r, i) => `<div class="pf-map-row" data-index="${i}">
    <select data-field="protocol">
      <option value="tcp"${r.protocol !== 'udp' ? ' selected' : ''}>TCP</option>
      <option value="udp"${r.protocol === 'udp' ? ' selected' : ''}>UDP</option>
    </select>
    <input type="number" data-field="local_port" placeholder="9820" min="1" max="65535" value="${esc(r.local_port)}">
    <input type="text" data-field="target_host" placeholder="10.0.0.5" value="${esc(r.target_host)}">
    <input type="number" data-field="target_port" placeholder="80" min="1" max="65535" value="${esc(r.target_port)}">
    <button type="button" class="btn btn-danger btn-sm pf-map-remove" data-action="remove-mapping" data-index="${i}">×</button>
  </div>`).join('');
  $('pf-mappings-empty').style.display = wrap.children.length ? 'none' : '';
}

// collectMappingInputs 读当前 DOM 行（保留用户半填内容）；全空行跳过
function collectMappingInputs() {
  return Array.from($('pf-mappings-rows').querySelectorAll('.pf-map-row')).map(row => ({
    protocol: row.querySelector('[data-field="protocol"]').value,
    local_port: row.querySelector('[data-field="local_port"]').value.trim(),
    target_host: row.querySelector('[data-field="target_host"]').value.trim(),
    target_port: row.querySelector('[data-field="target_port"]').value.trim(),
  })).filter(r => r.local_port || r.target_host || r.target_port);
}

// collectMappings 校验并转换为提交结构（§0.3 前端预校验，错误中文提示带行号）
function collectMappings() {
  const raw = collectMappingInputs();
  const seen = new Set();
  const out = [];
  for (let i = 0; i < raw.length; i++) {
    const r = raw[i];
    const lineNo = i + 1;
    const lp = parseInt(r.local_port, 10);
    const tp = parseInt(r.target_port, 10);
    if (!Number.isInteger(lp) || lp < 1 || lp > 65535) { toast(`第 ${lineNo} 条映射：本端端口须为 1-65535 的整数`, 'error'); return null; }
    if (!r.target_host) { toast(`第 ${lineNo} 条映射：对端目标地址不能为空`, 'error'); return null; }
    if (!Number.isInteger(tp) || tp < 1 || tp > 65535) { toast(`第 ${lineNo} 条映射：对端目标端口须为 1-65535 的整数`, 'error'); return null; }
    if (seen.has(lp)) { toast(`第 ${lineNo} 条映射：本端端口 ${lp} 重复`, 'error'); return null; }
    seen.add(lp);
    out.push({ protocol: r.protocol, local_port: lp, target_host: r.target_host, target_port: tp });
  }
  return out;
}

function saveForm() {
  const name = $('pf-name').value.trim();
  const room = $('pf-room').value.trim();
  const enabled = $('pf-enable').checked;

  if (!name) { toast('名称不能为空', 'error'); return; }
  if (!P2P_ROOM_REGEXP.test(room)) { toast('Room 须为 8-32 位字母/数字/下划线/中划线', 'error'); return; }
  const modes = P2P_MODE_OPTIONS.filter(m => selectedModes.has(m));
  const relay = $('pf-relay-server').value.trim();
  if (modes.includes('v4-relay') && !relay) { toast('选中 v4-relay 模式时必须填写中继服务器地址', 'error'); return; }
  const mappings = collectMappings();
  if (mappings === null) return;

  const brokers = $('pf-mqtt-brokers').value.split(',').map(s => s.trim()).filter(Boolean);
  const stuns = $('pf-stun-servers').value.split(',').map(s => s.trim()).filter(Boolean);

  // 空即省略（admin 同款约定）：省略 = 契约默认值（默认模式链 / 公共 broker 优先等）
  const para = { room };
  if (modes.length) para.modes = modes;
  if (relay) para.relay_server = relay;
  if (brokers.length) para.mqtt_brokers = brokers;
  if (stuns.length) para.stun_servers = stuns;
  if (mappings.length) para.mappings = mappings;

  // target 仅作列表展示（p2p 的实际映射在 para.mappings，同 admin 约定）
  const target = mappings.map(m => `${m.target_host}:${m.target_port}`).join(', ');

  api.addTunnel({ name, type: 'p2p', target, enabled, para })
    .then(() => {
      hideForm();
      toast(editingName ? '隧道已更新' : '隧道已添加', 'success');
    })
    .catch(e => toast(e.message, 'error'));
}

function showDetail(name) {
  currentDetailName = name;
  const t = tunnels.find(t => t.name === name);
  if (!t) return;
  activateSubpanel('p2p', 'p2p-detail-view');
  $('p2p-detail-empty').style.display = 'none';
  $('p2p-detail').style.display = '';
  renderDetail(t);
}

function renderDetail(t) {
  const s = t.status || {};
  const para = t.para || {};
  $('p2p-detail-title').textContent = `P2P 隧道详情 · ${t.name}`;

  const uptime = (s.connected && s.connected_at)
    ? formatUptime(Date.now() - s.connected_at)
    : '-';
  const infoItems = [
    ['Room', para.room || '-'],
    ['状态', s.connected ? '连通' : '断开'],
    ['打洞模式', s.mode || (modesLabel(para.modes))],
    ['本次打洞耗时', s.punch_ms ? s.punch_ms + ' ms' : '-'],
    ['本端监听地址', s.local_addr || '-'],
    ['对端地址', s.remote_addr || '-'],
    ['本次在线时长', uptime],
    ['累计重连', String(s.reconnects || 0)],
    ['累计流入', fmtBytes(s.bytes_in || 0)],
    ['累计流出', fmtBytes(s.bytes_out || 0)],
  ];
  if (s.error) infoItems.push(['最近错误', s.error]);
  $('p2p-detail-info').innerHTML = infoItems.map(([label, value]) =>
    `<div class="info-item"><span class="info-label">${esc(label)}</span><span class="info-value">${esc(value)}</span></div>`
  ).join('');

  const rows = s.mappings || [];
  const tbody = $('p2p-detail-mappings');
  if (!rows.length) {
    tbody.innerHTML = `<tr><td colspan="8" class="table-empty-cell">${emptyStateMarkup('暂无映射', '本端为纯会话端，或会话尚未建立。', 'P')}</td></tr>`;
    return;
  }
  // 行级字节为当前会话计数（重连归零；跨重连累计仅上面总量具备）
  tbody.innerHTML = rows.map(m => {
    const src = m.remote ? '<span class="badge badge-neutral">对端远程</span>' : '<span class="badge badge-on">本端配置</span>';
    const state = m.up ? '<span class="status on">在线</span>' : '<span class="status off">离线</span>';
    const errBadge = m.error ? ` <span class="badge badge-err" title="${esc(m.error)}">错误</span>` : '';
    return `<tr>
      <td><span class="badge badge-${esc(m.protocol)}">${esc((m.protocol || '').toUpperCase())}</span></td>
      <td style="font-family:monospace">${esc(String(m.local_port))}</td>
      <td style="font-family:monospace;font-size:12px">${esc(`${m.target_host}:${m.target_port}`)}</td>
      <td>${src}</td>
      <td>${state}${errBadge}</td>
      <td>${fmtBytes(m.bytes_in || 0)}</td>
      <td>${fmtBytes(m.bytes_out || 0)}</td>
      <td style="font-size:12px;color:#667085">${esc(m.error || '-')}</td>
    </tr>`;
  }).join('');
}

function modesLabel(modes) {
  if (!modes || !modes.length) return '默认链';
  return modes.join(' → ');
}

// formatUptime 与 vpn.js 同款（该函数未导出，此处本地复制）
function formatUptime(ms) {
  if (!ms || ms < 0) return '-';
  const sec = Math.floor(ms / 1000);
  if (sec < 60) return sec + '秒';
  if (sec < 3600) return Math.floor(sec / 60) + '分钟';
  if (sec < 86400) return Math.floor(sec / 3600) + '小时';
  return Math.floor(sec / 86400) + '天';
}

function refreshList() {
  api.listTunnels().then(list => {
    tunnels = list.filter(t => t.type === 'p2p');
    render();
  }).catch(() => {});
}

function $(id) { return document.getElementById(id); }
