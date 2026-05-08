// ser2net.js — 串口 <-> TCP/UDP 隧道管理
import { api } from './api.js';
import { setText, fmtBytes, esc, toast, activateSubpanel, emptyStateMarkup, renderVizBars, renderVizRing, showSerialFormTemplate } from './main.js';

const SER2NET_TYPES = new Set(['ser2tcp', 'ser2udp']);

let tunnels = [];
let editingName = null;
let activeFilter = 'all';
let searchKeyword = '';

export function initSer2Net() {
  const tbody = document.getElementById('ser2net-tbody');
  const filterGroup = document.getElementById('ser2net-filter-group');
  const searchInput = document.getElementById('ser2net-search');

  tbody.addEventListener('click', (e) => {
    const btn = e.target.closest('[data-action]');
    if (!btn) return;

    const name = btn.dataset.name;
    const action = btn.dataset.action;

    if (action === 'delete') {
      handleDelete(name);
      return;
    }
    if (action === 'edit') {
      const tunnel = tunnels.find(t => t.name === name);
      if (tunnel) showEditForm(tunnel);
      return;
    }
    if (action === 'toggle') {
      const tunnel = tunnels.find(t => t.name === name);
      if (tunnel) handleToggle(tunnel);
    }
  });

  if (filterGroup) {
    filterGroup.addEventListener('click', (e) => {
      const btn = e.target.closest('[data-filter]');
      if (!btn) return;
      activeFilter = btn.dataset.filter || 'all';
      render();
    });
  }

  if (searchInput) {
    searchInput.addEventListener('input', (e) => {
      searchKeyword = (e.target.value || '').trim().toLowerCase();
      render();
    });
  }

  document.getElementById('btn-add-ser2net').addEventListener('click', showAddForm);
  document.getElementById('btn-add-ser2net-secondary').addEventListener('click', showAddForm);
  document.getElementById('btn-cancel-ser2net').addEventListener('click', hideForm);
  document.getElementById('btn-save-ser2net').addEventListener('click', saveForm);
  document.getElementById('btn-refresh-ser2net').addEventListener('click', () => {
    refreshSer2NetTunnels(true);
  });

  document.getElementById('snf-type').addEventListener('change', updateModeHint);
  document.getElementById('snf-mode').addEventListener('change', updateModeHint);

  window.__ser2netRefresh = (allTunnels) => {
    tunnels = allTunnels.filter(t => SER2NET_TYPES.has(t.type));
    render();
  };
}

async function refreshSer2NetTunnels(showToastOnError = false) {
  try {
    const list = await api.listTunnels();
    tunnels = list.filter(t => SER2NET_TYPES.has(t.type));
    render();
  } catch (e) {
    if (showToastOnError) toast(e.message || '刷新失败', 'error');
  }
}

async function handleDelete(name) {
  if (!confirm('确认删除隧道 ' + name + '?')) return;
  try {
    await api.removeTunnel(name);
    await refreshSer2NetTunnels();
    toast('已删除', 'success');
  } catch (e) {
    toast(e.message, 'error');
  }
}

async function handleToggle(tunnel) {
  const para = Object.assign({}, tunnel.para, { enable: !tunnel.enabled });
  try {
    await api.addTunnel({
      name: tunnel.name,
      type: tunnel.type,
      target: tunnel.target,
      enabled: !tunnel.enabled,
      para
    });
    await refreshSer2NetTunnels();
    toast(tunnel.enabled ? '已禁用' : '已启用', 'success');
  } catch (e) {
    toast(e.message, 'error');
  }
}

function render() {
  const tbody = document.getElementById('ser2net-tbody');
  const state = analyzeTunnels(tunnels);
  const visibleTunnels = tunnels;

  renderMetrics(state);
  renderToolbarSummary(visibleTunnels.length, tunnels.length);

  if (!visibleTunnels.length) {
    tbody.innerHTML = `<tr><td colspan="8" class="table-empty-cell">${emptyStateMarkup('暂无 Ser2Net 隧道', '创建串口与 TCP/UDP 的桥接后，会在这里显示列表清单。', 'N')}</td></tr>`;
    return;
  }

  tbody.innerHTML = visibleTunnels.map(tunnel => renderRow(tunnel, state.maxTraffic)).join('');
}

function analyzeTunnels(source) {
  const total = source.length;
  const runningCount = source.filter(isRunningTunnel).length;
  const enabledCount = source.filter(t => t.enabled).length;
  const tcpCount = source.filter(t => t.type === 'ser2tcp').length;
  const udpCount = source.filter(t => t.type === 'ser2udp').length;
  const waitingCount = source.filter(isWaitingTunnel).length;
  const alerts = source
    .filter(isAlertTunnel)
    .map(tunnel => ({
      tunnel,
      severity: tunnelSeverity(tunnel),
      title: tunnelAlertTitle(tunnel),
      detail: statusNote(tunnelStatus(tunnel), tunnel.enabled)
    }))
    .sort((a, b) => severityWeight(b.severity) - severityWeight(a.severity));
  const alertCount = alerts.length;
  const peerCount = source.reduce((sum, tunnel) => sum + activePeerCount(tunnel), 0);
  const topTalkers = [...source]
    .sort((a, b) => totalTraffic(b) - totalTraffic(a))
    .slice(0, 5);
  const maxTraffic = Math.max(...topTalkers.map(totalTraffic), 0);
  const riskState = alertCount > 0 ? (alerts.some(item => item.severity === 'critical') ? 'critical' : 'warning') : (waitingCount > 0 ? 'warning' : 'stable');

  return {
    total,
    runningCount,
    enabledCount,
    tcpCount,
    udpCount,
    waitingCount,
    alertCount,
    peerCount,
    topTalkers,
    maxTraffic,
    alerts,
    riskState
  };
}

function renderHero(state, visibleCount) {
  const badge = document.getElementById('ser2net-health-badge');
  if (!badge) return;
  badge.className = `ops-health-badge ${state.riskState}`;
  badge.textContent = riskLabel(state);

  setText('ser2net-hero-title', heroTitle(state));
  setText('ser2net-hero-desc', heroDesc(state));
  setText('ser2net-visible-count', `${visibleCount} / ${state.total}`);
  setText('ser2net-peer-count', String(state.peerCount));
  setText('ser2net-top-tunnel', state.topTalkers[0] ? state.topTalkers[0].name : '暂无');
}

function renderMetrics(state) {
  setText('ser2net-total', String(state.total));
  setText('ser2net-online', String(state.runningCount));
  setText('ser2net-alerts', String(state.alertCount));
  setText('ser2net-peers', String(state.peerCount));
  setText('ser2net-type-summary', state.total ? `TCP ${state.tcpCount} / UDP ${state.udpCount}` : '暂无隧道');
  setText('ser2net-status-summary', ser2netStatusSummary(state));
}

function renderFilterChips() {
  document.querySelectorAll('#ser2net-filter-group [data-filter]').forEach(btn => {
    btn.classList.toggle('active', btn.dataset.filter === activeFilter);
  });
}

function renderToolbarSummary(visibleCount, totalCount) {
  const summary = totalCount ? `共 ${totalCount} 条，运行中 ${visibleCount} 条桥接配置` : '全部视图 · 显示 0 / 0';
  setText('ser2net-toolbar-summary', summary);
}

function ser2netStatusSummary(state) {
  if (!state.total) return '等待配置';
  if (state.alertCount > 0) return `${state.alertCount} 条异常待处理`;
  if (state.peerCount > 0) return `${state.peerCount} 个网络端连接活跃`;
  if (state.waitingCount > 0) return `${state.waitingCount} 条链路等待接入`;
  return '当前状态稳定';
}

function renderAlertList(alerts) {
  const container = document.getElementById('ser2net-alert-list');
  if (!container) return;
  if (!alerts.length) {
    container.innerHTML = `<div class="ops-card-empty">${emptyStateMarkup('暂无异常告警', '当前 Ser2Net 链路状态平稳，可优先关注待接入和热区流量变化。', '!')}</div>`;
    return;
  }

  container.innerHTML = alerts.slice(0, 5).map(item => {
    const status = tunnelStatus(item.tunnel);
    return `<div class="ops-alert-item severity-${item.severity}">
      <div class="ops-alert-head">
        <span class="ops-alert-name">${esc(item.tunnel.name)}</span>
        <span class="ops-alert-severity severity-${item.severity}">${esc(severityText(item.severity))}</span>
      </div>
      <div class="ops-alert-title">${esc(item.title)}</div>
      <div class="ops-alert-desc">${esc(item.detail || routeSummary(item.tunnel.type, tunnelMode(item.tunnel)))}</div>
      <div class="ops-alert-meta">${esc(serialPortOf(item.tunnel))} · ${esc(status.address || item.tunnel.para?.address || '-')}</div>
    </div>`;
  }).join('');
}

function renderTopList(topTalkers, maxTraffic) {
  const container = document.getElementById('ser2net-top-list');
  if (!container) return;
  if (!topTalkers.length) {
    container.innerHTML = `<div class="ops-card-empty">${emptyStateMarkup('暂无流量数据', '等有串口收发或网络连接活动后，这里会显示最忙的隧道。', '~')}</div>`;
    return;
  }

  const safeMax = maxTraffic > 0 ? maxTraffic : 1;
  container.innerHTML = topTalkers.map(tunnel => {
    const traffic = totalTraffic(tunnel);
    const percent = Math.max(traffic > 0 ? Math.round((traffic / safeMax) * 100) : 0, traffic > 0 ? 8 : 0);
    return `<div class="ops-top-item">
      <div class="ops-top-head">
        <span class="ops-top-name">${esc(tunnel.name)}</span>
        <span class="ops-top-value">${esc(fmtBytes(traffic))}</span>
      </div>
      <div class="ops-top-meta">${esc(routeSummary(tunnel.type, tunnelMode(tunnel)))} · ${esc(connectionText(tunnelStatus(tunnel)))}</div>
      <div class="traffic-meter compact"><span class="traffic-meter-fill" style="width:${percent}%"></span></div>
    </div>`;
  }).join('');
}

function applyFilters(list) {
  return list.filter(tunnel => matchesFilter(tunnel) && matchesSearch(tunnel));
}

function matchesFilter(tunnel) {
  switch (activeFilter) {
    case 'alert':
      return isAlertTunnel(tunnel);
    case 'live':
      return isLiveTunnel(tunnel);
    case 'waiting':
      return isWaitingTunnel(tunnel);
    case 'disabled':
      return !tunnel.enabled;
    default:
      return true;
  }
}

function matchesSearch(tunnel) {
  if (!searchKeyword) return true;
  const status = tunnelStatus(tunnel);
  const haystack = [
    tunnel.name,
    tunnel.type,
    serialPortOf(tunnel),
    status.address,
    tunnel.para?.address,
    tunnel.para?.mode,
    routeSummary(tunnel.type, tunnelMode(tunnel))
  ].filter(Boolean).join(' ').toLowerCase();
  return haystack.includes(searchKeyword);
}

function renderRow(tunnel, maxTraffic) {
  const status = tunnelStatus(tunnel);
  const running = !!status.running;
  const mode = tunnelMode(tunnel);
  const enabledClass = tunnel.enabled ? 'badge-on' : 'badge-off';
  const runningClass = running ? 'badge-on' : 'badge-off';
  const enabledText = tunnel.enabled ? '启用' : '禁用';
  const runningText = running ? (mode === 'server' ? '监听中' : '运行中') : '已停止';
  const modeBadge = `<span class="badge badge-neutral">${esc(modeLabel(mode))}</span>`;
  const serialBadge = `<span class="badge ${status.serial_open ? 'badge-on' : 'badge-off'}">Serial</span>`;
  const peerBadge = `<span class="badge ${running ? 'badge-on' : 'badge-off'}">${esc(connectionBadgeText(status))}</span>`;
  const errorInfo = status.error ? `<span class="badge badge-err" title="${esc(status.error)}">错误</span>` : '';
  const traffic = totalTraffic(tunnel);
  const trafficPercent = trafficPercentOf(traffic, maxTraffic);

  let actions = `<button class="btn btn-sm" data-action="edit" data-name="${esc(tunnel.name)}">编辑</button>`;
  actions += tunnel.enabled
    ? `<button class="btn btn-sm" data-action="toggle" data-name="${esc(tunnel.name)}">禁用</button>`
    : `<button class="btn btn-primary btn-sm" data-action="toggle" data-name="${esc(tunnel.name)}">启用</button>`;
  actions += `<button class="btn btn-danger btn-sm" data-action="delete" data-name="${esc(tunnel.name)}">删除</button>`;

  return `<tr class="${isAlertTunnel(tunnel) ? 'row-alert' : ''}">
    <td>
      <span class="table-primary">${esc(tunnel.name)}</span>
      <span class="table-meta">${esc(routeSummary(tunnel.type, mode))}</span>
    </td>
    <td>
      <span class="badge badge-${tunnel.type}">${esc(typeLabel(tunnel.type))}</span>
      <span class="table-meta">${esc(typeHint(tunnel.type))}</span>
    </td>
    <td>
      <span class="table-primary">${esc(serialPortOf(tunnel))}</span>
      <span class="table-meta">${esc(serialDetail(tunnel))}</span>
    </td>
    <td>
      ${modeBadge}
      <span class="table-meta">${esc(modeHint(mode))}</span>
    </td>
    <td>
      <span class="table-primary">${esc(tunnelAddress(tunnel))}</span>
      <span class="table-meta">${esc(addressHint(mode))}</span>
    </td>
    <td>
      <div class="status-stack">
        <span class="badge ${enabledClass}">${enabledText}</span>
        <span class="badge ${runningClass}">${runningText}</span>
        ${errorInfo}
      </div>
      <div class="status-note">${esc(statusNote(status, tunnel.enabled))}</div>
    </td>
    <td>
      <div class="status-stack">
        ${serialBadge}
        ${peerBadge}
      </div>
      <div class="status-note">${esc(flowSummary(status, tunnel))}</div>
      <div class="traffic-meter"><span class="traffic-meter-fill" style="width:${trafficPercent}%"></span></div>
      <div class="traffic-caption">${traffic > 0 ? `热度 ${trafficPercent}%` : '暂无流量'}</div>
    </td>
    <td class="actions">${actions}</td>
  </tr>`;
}

function showAddForm() {
  activateSubpanel('serial', 'serial-form-view');
  editingName = null;

  document.getElementById('ser2net-form-title').textContent = '新增 Ser2Net 隧道';
  document.getElementById('snf-name').value = '';
  document.getElementById('snf-name').disabled = false;
  document.getElementById('snf-type').value = 'ser2tcp';
  document.getElementById('snf-mode').value = 'server';
  document.getElementById('snf-address').value = ':5000';
  document.getElementById('snf-max-conn').value = '1';
  document.getElementById('snf-port').value = '';
  document.getElementById('snf-baudrate').value = '9600';
  document.getElementById('snf-databits').value = '8';
  document.getElementById('snf-stopbits').value = '1';
  document.getElementById('snf-parity').value = 'N';
  document.getElementById('snf-timeout').value = '3000';
  document.getElementById('snf-enable').checked = true;

  updateModeHint();
  showSerialFormTemplate('ser2net');
}

function showEditForm(tunnel) {
  activateSubpanel('serial', 'serial-form-view');
  editingName = tunnel.name;

  document.getElementById('ser2net-form-title').textContent = '编辑 Ser2Net 隧道';
  document.getElementById('snf-name').value = tunnel.name;
  document.getElementById('snf-name').disabled = true;
  document.getElementById('snf-type').value = tunnel.type;
  document.getElementById('snf-enable').checked = tunnel.enabled;

  const para = tunnel.para || {};
  const serial = para.serial || {};
  document.getElementById('snf-mode').value = para.mode || 'server';
  document.getElementById('snf-address').value = para.address || '';
  document.getElementById('snf-max-conn').value = String(para.max_conn || 1);
  document.getElementById('snf-port').value = serial.port || '';
  document.getElementById('snf-baudrate').value = String(serial.baudrate || 9600);
  document.getElementById('snf-databits').value = String(serial.databits || 8);
  document.getElementById('snf-stopbits').value = String(serial.stopbits || 1);
  document.getElementById('snf-parity').value = serial.parity || 'N';
  document.getElementById('snf-timeout').value = String(serial.timeout || 3000);

  updateModeHint();
  showSerialFormTemplate('ser2net');
}

function hideForm() {
  showSerialFormTemplate('');
  editingName = null;
  activateSubpanel('serial', 'serial-list-view');
}

function updateModeHint() {
  const type = document.getElementById('snf-type').value;
  const mode = document.getElementById('snf-mode').value;
  const hint = document.getElementById('snf-mode-hint');
  const maxConnRow = document.getElementById('snf-max-conn-row');
  const addressLabel = document.getElementById('snf-address-label');
  const addressInput = document.getElementById('snf-address');
  const addressNote = document.getElementById('snf-address-note');
  const routeSummaryEl = document.getElementById('snf-route-summary');
  const scenarioSummaryEl = document.getElementById('snf-scenario-summary');

  hint.textContent = formHint(type, mode);
  routeSummaryEl.textContent = routeSummary(type, mode);
  scenarioSummaryEl.textContent = scenarioSummary(type, mode);

  maxConnRow.style.display = type === 'ser2tcp' && mode === 'server' ? '' : 'none';
  addressLabel.textContent = mode === 'server' ? '监听地址' : '远端地址';
  addressInput.placeholder = mode === 'server' ? ':5000' : '192.168.1.100:5000';
  addressNote.textContent = mode === 'server'
    ? 'Server 模式填写本地监听地址，例如 :5000 或 0.0.0.0:5000。'
    : 'Client 模式填写远端服务地址，例如 192.168.1.100:5000。';
}

async function saveForm() {
  const name = document.getElementById('snf-name').value.trim();
  const type = document.getElementById('snf-type').value;
  const mode = document.getElementById('snf-mode').value;
  const address = document.getElementById('snf-address').value.trim();
  const port = document.getElementById('snf-port').value.trim();
  const baudrate = parseInt(document.getElementById('snf-baudrate').value, 10);
  const databits = parseInt(document.getElementById('snf-databits').value, 10);
  const stopbits = parseFloat(document.getElementById('snf-stopbits').value);
  const timeout = parseInt(document.getElementById('snf-timeout').value, 10);
  const enabled = document.getElementById('snf-enable').checked;
  const maxConn = type === 'ser2tcp' && mode === 'server'
    ? Math.max(parseInt(document.getElementById('snf-max-conn').value, 10) || 1, 1)
    : 1;

  if (!name) {
    toast('名称不能为空', 'error');
    return;
  }
  if (!address) {
    toast('地址不能为空', 'error');
    return;
  }
  if (!port) {
    toast('串口端口不能为空', 'error');
    return;
  }
  if (!Number.isFinite(baudrate) || baudrate <= 0) {
    toast('波特率必须为正整数', 'error');
    return;
  }
  if (![5, 6, 7, 8].includes(databits)) {
    toast('数据位仅支持 5/6/7/8', 'error');
    return;
  }
  if (![1, 1.5, 2].includes(stopbits)) {
    toast('停止位仅支持 1 / 1.5 / 2', 'error');
    return;
  }
  if (!Number.isFinite(timeout) || timeout <= 0) {
    toast('读超时必须大于 0', 'error');
    return;
  }

  const isEditing = !!editingName;
  const payload = {
    name,
    type,
    target: port,
    enabled,
    para: {
      enable: enabled,
      mode,
      address,
      max_conn: maxConn,
      serial: {
        port,
        baudrate,
        databits,
        stopbits,
        parity: document.getElementById('snf-parity').value,
        timeout
      }
    }
  };

  try {
    await api.addTunnel(payload);
    await refreshSer2NetTunnels();
    hideForm();
    toast(isEditing ? '隧道已更新' : '隧道已添加', 'success');
  } catch (e) {
    toast(e.message, 'error');
  }
}

function tunnelStatus(tunnel) {
  return tunnel.status || {};
}

function isRunningTunnel(tunnel) {
  return !!tunnelStatus(tunnel).running;
}

function isWaitingTunnel(tunnel) {
  const status = tunnelStatus(tunnel);
  return !!(tunnel.enabled && status.running && tunnelMode(tunnel) === 'server' && (status.clients || 0) === 0 && !status.error);
}

function isLiveTunnel(tunnel) {
  const status = tunnelStatus(tunnel);
  if (!status.running) return false;
  return tunnelMode(tunnel) === 'client' || (status.clients || 0) > 0;
}

function isAlertTunnel(tunnel) {
  const status = tunnelStatus(tunnel);
  return !!(status.error || (tunnel.enabled && !status.running) || (tunnel.enabled && status.running && !status.serial_open));
}

function tunnelSeverity(tunnel) {
  const status = tunnelStatus(tunnel);
  if (status.error) return 'critical';
  if (tunnel.enabled && !status.running) return 'warning';
  if (tunnel.enabled && status.running && !status.serial_open) return 'warning';
  return 'stable';
}

function tunnelAlertTitle(tunnel) {
  const status = tunnelStatus(tunnel);
  if (status.error) return status.error;
  if (tunnel.enabled && !status.running) return '已启用但运行态未建立';
  if (tunnel.enabled && status.running && !status.serial_open) return '运行中但串口未打开';
  return '状态正常';
}

function severityWeight(severity) {
  return severity === 'critical' ? 3 : severity === 'warning' ? 2 : 1;
}

function severityText(severity) {
  return severity === 'critical' ? '高风险' : severity === 'warning' ? '关注' : '正常';
}

function riskLabel(state) {
  if (state.riskState === 'critical') return '告警';
  if (state.riskState === 'warning') return state.alertCount > 0 ? '注意' : '待接入';
  return '稳定';
}

function heroTitle(state) {
  if (!state.total) return '暂无 Ser2Net 隧道';
  if (state.alertCount > 0) return `发现 ${state.alertCount} 条需处理风险，请优先排查`;
  if (state.waitingCount > 0) return `${state.waitingCount} 条监听链路待接入，其他链路保持稳定`;
  return '链路状态稳定，适合日常巡检';
}

function heroDesc(state) {
  if (!state.total) return '创建串口桥接后，这里会集中显示健康度、热区流量和需要关注的隧道。';
  if (state.alertCount > 0) return '建议先查看左侧风险雷达，优先处理启用未运行、串口未打开或运行时错误的链路。';
  if (state.waitingCount > 0) return '当前存在正在监听但尚未有网络端接入的隧道，适合进一步确认对端是否已经连接。';
  return '目前没有明显异常，可以通过筛选器查看已连接链路和高流量隧道，继续做值班巡检。';
}

function filterName(filter) {
  switch (filter) {
    case 'alert':
      return '异常视图';
    case 'live':
      return '已连接视图';
    case 'waiting':
      return '待接入视图';
    case 'disabled':
      return '已禁用视图';
    default:
      return '全部视图';
  }
}

function emptyTitle() {
  if (!tunnels.length) return '暂无串口网络隧道';
  return '当前筛选条件下没有结果';
}

function emptyDesc() {
  if (!tunnels.length) return '创建串口 <-> TCP/UDP 桥接后，可在这里统一查看监听、连接和流量状态。';
  if (searchKeyword) return '试试缩短关键字，或者切换到其他视图查看不同状态的隧道。';
  return '试试切换筛选器，查看异常、已连接或待接入的不同巡检视角。';
}

function trafficPercentOf(traffic, maxTraffic) {
  if (traffic <= 0 || maxTraffic <= 0) return 0;
  return Math.max(Math.round((traffic / maxTraffic) * 100), 8);
}

function activePeerCount(tunnel) {
  const status = tunnelStatus(tunnel);
  if (tunnelMode(tunnel) === 'server') return status.clients || 0;
  return status.running ? 1 : 0;
}

function tunnelMode(tunnel) {
  return tunnelStatus(tunnel).mode || tunnel.para?.mode || 'server';
}

function tunnelAddress(tunnel) {
  return tunnelStatus(tunnel).address || tunnel.para?.address || '-';
}

function bytesInOf(tunnel) {
  return tunnelStatus(tunnel).bytes_in ?? tunnel.bytes_in ?? 0;
}

function bytesOutOf(tunnel) {
  return tunnelStatus(tunnel).bytes_out ?? tunnel.bytes_out ?? 0;
}

function totalTraffic(tunnel) {
  return bytesInOf(tunnel) + bytesOutOf(tunnel);
}

function serialPortOf(tunnel) {
  return tunnelStatus(tunnel).serial_port || tunnel.para?.serial?.port || tunnel.target || '-';
}

function serialDetail(tunnel) {
  const serial = tunnel.para?.serial || {};
  if (!serial.baudrate) return '等待补充串口参数';
  return `${serial.baudrate} baud / ${serial.databits || 8}${serial.parity || 'N'}${serial.stopbits || 1}`;
}

function flowSummary(status, tunnel) {
  const bytesIn = fmtBytes(bytesInOf(tunnel));
  const bytesOut = fmtBytes(bytesOutOf(tunnel));
  return `${connectionText(status)} | 入 ${bytesIn} / 出 ${bytesOut}`;
}

function statusNote(status, enabled) {
  if (status.error) return status.error;
  if (!enabled) return '配置保留，当前未启用。';
  if (!status.running) return '等待启动或监听地址生效。';
  if (status.mode === 'server') {
    return status.clients > 0 ? `当前已有 ${status.clients} 个网络端接入。` : '正在监听，等待网络端接入。';
  }
  return '已建立主动连接，断线会自动重连。';
}

function connectionText(status) {
  if (!status.running) return '链路未建立';
  if (status.mode === 'server') {
    return status.clients > 0 ? `${status.clients} 路连接` : '等待接入';
  }
  return '主动连接已建立';
}

function connectionBadgeText(status) {
  if (!status.running) return 'Net';
  if (status.mode === 'server') return status.clients > 0 ? `Net x${status.clients}` : 'Net 待接入';
  return 'Net 已连接';
}

function typeLabel(type) {
  return type === 'ser2udp' ? 'SER2UDP' : 'SER2TCP';
}

function typeHint(type) {
  return type === 'ser2udp' ? '面向报文' : '面向连接';
}

function modeLabel(mode) {
  return mode === 'client' ? 'Client' : 'Server';
}

function modeHint(mode) {
  return mode === 'client' ? '主动连接远端' : '本地监听接入';
}

function addressHint(mode) {
  return mode === 'client' ? '远端地址' : '本地监听地址';
}

function routeSummary(type, mode) {
  if (type === 'ser2tcp' && mode === 'server') return '串口 <-> 多 TCP 客户端';
  if (type === 'ser2tcp' && mode === 'client') return '串口 <-> 远端 TCP 服务';
  if (type === 'ser2udp' && mode === 'server') return '串口 <-> 多 UDP 对端';
  return '串口 <-> 固定 UDP 对端';
}

function scenarioSummary(type, mode) {
  if (type === 'ser2tcp' && mode === 'server') return '适合设备挂在本机，由远端 TCP 客户端接入采集。';
  if (type === 'ser2tcp' && mode === 'client') return '适合把本地串口主动桥接到现有 TCP 服务端。';
  if (type === 'ser2udp' && mode === 'server') return '适合广播型或轻量报文场景，自动学习对端地址。';
  return '适合固定 UDP 网关或上位机，配置简单、延迟低。';
}

function formHint(type, mode) {
  if (type === 'ser2tcp' && mode === 'server') {
    return 'TCP Server：监听本地端口，网络侧多个客户端可接入，串口数据会广播到当前在线客户端。';
  }
  if (type === 'ser2tcp' && mode === 'client') {
    return 'TCP Client：主动连接远端 TCP 服务端，适合把本地串口设备接入已有采集平台。';
  }
  if (type === 'ser2udp' && mode === 'server') {
    return 'UDP Server：监听本地 UDP 端口，自动学习最近活跃的远端地址，再把串口数据转发出去。';
  }
  return 'UDP Client：固定向远端 UDP 地址发包，并把收到的 UDP 数据回写到本地串口。';
}
