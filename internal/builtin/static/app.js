// app.js — 主应用逻辑
import { api } from './api.js';
import { syncTunnelTable } from './render.js';

// ===== 工具函数 =====

const $ = (s) => document.querySelector(s);

function fmtBytes(b) {
  if (!b || b === 0) return '0 B';
  const u = ['B', 'KB', 'MB', 'GB'];
  let i = 0;
  while (b >= 1024 && i < u.length - 1) { b /= 1024; i++; }
  return b.toFixed(b >= 100 ? 0 : b >= 10 ? 1 : 2) + ' ' + u[i];
}

function fmtTime(ts) {
  if (!ts) return '-';
  const d = new Date(ts * 1000);
  return d.toLocaleString('zh-CN');
}

function esc(s) {
  const el = document.createElement('span');
  el.textContent = s || '';
  return el.innerHTML;
}

function toast(msg, type = 'info') {
  const c = $('#toast-container');
  const el = document.createElement('div');
  el.className = 'toast ' + type;
  el.textContent = msg;
  c.appendChild(el);
  setTimeout(() => { el.remove(); }, 3000);
}

// ===== 状态 =====

let globalStatus = null;
let tunnels = [];
let selectedName = null;
let refreshPending = false;
let refreshTimer = null;
let lastStatsKey = '';
let lastTunnelListKey = '';
let lastDetailKey = '';

function setRefreshInfo(text, isError = false) {
  const el = $('#refresh-info');
  if (!el) return;
  el.textContent = text;
  el.classList.toggle('error', isError);
}

function staleConnectionBadge() {
  const badge = $('#conn-badge');
  badge.textContent = '数据过期';
  badge.className = 'conn-badge stale';
}

// ===== 渲染: 全局状态 =====

function renderHeader(status) {
  const badge = $('#conn-badge');
  if (status.connected) {
    badge.textContent = '已连接';
    badge.className = 'conn-badge online';
  } else {
    badge.textContent = '已断开';
    badge.className = 'conn-badge offline';
  }
  $('#header-info').innerHTML =
    `<span>节点: ${esc(status.node_id || '-')}</span>` +
    `<span>服务器: ${esc(status.server_addr || '-')}</span>`;
}

function renderStatsBar(status) {
  $('#stats-bar').innerHTML =
    statBox('隧道数', status.tunnels ? status.tunnels.length : 0) +
    statBox('TCP 流入', fmtBytes(status.tcp_bytes_in)) +
    statBox('TCP 流出', fmtBytes(status.tcp_bytes_out)) +
    statBox('HTTP 流入', fmtBytes(status.http_bytes_in)) +
    statBox('HTTP 流出', fmtBytes(status.http_bytes_out));
}

function statBox(label, value) {
  return `<div class="stat-box"><div class="label">${esc(label)}</div><div class="value">${esc(String(value))}</div></div>`;
}

// ===== 渲染: 隧道列表 =====

function renderTunnels() {
  const tbody = $('#tunnels-tbody');
  syncTunnelTable(tbody, tunnels, selectedName);
}

// ===== 渲染: 详情面板 =====

function renderDetail(tunnel) {
  const panel = $('#detail-panel');
  const content = $('#detail-content');
  const detailKey = JSON.stringify(tunnel);
  if (!panel.classList.contains('hidden') && detailKey === lastDetailKey) {
    return;
  }
  lastDetailKey = detailKey;
  $('#detail-title').textContent = tunnel.name + ' — ' + tunnel.type.toUpperCase();

  let html = '<div class="detail-grid">';
  html += detailItem('名称', tunnel.name);
  html += detailItem('类型', tunnel.type);
  html += detailItem('目标', tunnel.target);
  html += detailItem('状态', tunnel.connected ? '运行中' : '离线');
  html += detailItem('流入', fmtBytes(tunnel.bytes_in));
  html += detailItem('流出', fmtBytes(tunnel.bytes_out));

  if (tunnel.type === 'ser2mq' && tunnel.status) {
    const s = tunnel.status;
    html += detailItem('MQTT Broker', s.broker || '-');
    html += detailItem('串口', s.serial_port || '-');
  }
  if (tunnel.type === 'vpn-manager' && tunnel.status) {
    const s = tunnel.status;
    html += detailItem('PID', s.pid || '-');
    html += detailItem('崩溃次数', s.crash_count || 0);
    if (s.start_time) html += detailItem('启动时间', fmtTime(s.start_time));
  }
  html += '</div>';

  // 类型特定操作按钮
  html += '<div class="detail-actions">';
  if (tunnel.type === 'vpn-manager') {
    if (tunnel.connected) {
      html += `<button class="btn btn-danger" id="detail-stop">停止</button>`;
    } else {
      html += `<button class="btn btn-primary" id="detail-start">启动</button>`;
    }
    html += `<button class="btn btn-ghost" id="detail-logs">崩溃日志</button>`;
  }
  html += '</div>';

  // 日志区域
  html += '<div id="detail-log-area"></div>';

  content.innerHTML = html;
  panel.classList.remove('hidden');

  // 绑定详情按钮
  const startBtn = content.querySelector('#detail-start');
  const stopBtn = content.querySelector('#detail-stop');
  const logsBtn = content.querySelector('#detail-logs');

  if (startBtn) startBtn.addEventListener('click', () => vpnAction(tunnel.name, 'start'));
  if (stopBtn) stopBtn.addEventListener('click', () => vpnAction(tunnel.name, 'stop'));
  if (logsBtn) logsBtn.addEventListener('click', () => loadVPNLogs(tunnel.name));
}

function detailItem(label, value) {
  return `<div class="detail-item"><div class="dl">${esc(label)}</div><div class="dv">${esc(String(value))}</div></div>`;
}

async function vpnAction(name, action) {
  try {
    if (action === 'start') {
      await api.startTunnel(name);
      toast(name + ' 已启动', 'success');
    } else {
      await api.stopTunnel(name);
      toast(name + ' 已停止', 'success');
    }
    await refreshData();
    const t = tunnels.find(t => t.name === name);
    if (t) renderDetail(t);
  } catch (e) {
    toast(e.message, 'error');
  }
}

async function loadVPNLogs(name) {
  try {
    const logs = await api.getTunnelLogs(name);
    const area = $('#detail-log-area');
    if (!logs || !logs.length) {
      area.innerHTML = '<div style="margin-top:8px;color:#aaa;font-size:12px">无崩溃日志</div>';
      return;
    }
    area.innerHTML = '<div class="log-list">' +
      logs.map(l => {
        const time = new Date(l.timestamp * 1000).toLocaleString('zh-CN');
        const info = `退出码: ${l.exit_code || '?'}, 信号: ${l.signal || '-'}`;
        const tail = l.log_tail ? atob(String(l.log_tail)) : '';
        return `<div class="log-item"><strong>${time}</strong> ${info}\n${esc(tail)}</div>`;
      }).join('') +
      '</div>';
  } catch (e) {
    toast(e.message, 'error');
  }
}

// ===== 表单: 新增隧道 =====

function openModal() {
  $('#modal-title').textContent = '新增隧道';
  $('#f-name').value = '';
  $('#f-name').disabled = false;
  $('#f-type').value = 'http';
  $('#f-target').value = '';
  renderParaFields('http');
  $('#edit-modal').classList.remove('hidden');
}

function closeModal() {
  $('#edit-modal').classList.add('hidden');
}

function renderParaFields(type) {
  const el = $('#para-fields');
  if (type === 'ser2mq') {
    el.innerHTML =
      '<div class="form-group"><label>MQTT Broker</label><input id="f-broker" placeholder="mqtt://user:pass@broker:1883"></div>' +
      '<div class="form-group"><label>串口端口</label><input id="f-serial-port" placeholder="/dev/ttyUSB0" value="/dev/ttyUSB0"></div>' +
      '<div class="form-row">' +
        '<div class="form-group"><label>波特率</label><select id="f-baudrate">' +
          [1200,2400,4800,9600,19200,38400,57600,115200].map(b =>
            `<option value="${b}"${b===9600?' selected':''}>${b}</option>`
          ).join('') +
        '</select></div>' +
        '<div class="form-group"><label>数据位</label><select id="f-databits">' +
          [5,6,7,8].map(b => `<option value="${b}"${b===8?' selected':''}>${b}</option>`).join('') +
        '</select></div>' +
        '<div class="form-group"><label>校验位</label><select id="f-parity">' +
          '<option value="N" selected>无</option><option value="E">偶</option><option value="O">奇</option>' +
        '</select></div>' +
      '</div>' +
      '<div class="form-group"><label>加密密钥</label><input id="f-secret" placeholder="64字符hex密钥" maxlength="64"><div class="form-hint">32字节 hex 编码，可通过 openssl rand -hex 32 生成</div></div>';
  } else if (type === 'vpn-manager') {
    el.innerHTML =
      '<div class="form-group"><label>程序名称</label><input id="f-binary-name" placeholder="easytier-core"></div>' +
      '<div class="form-group"><label>程序路径（可选）</label><input id="f-binary-path" placeholder="留空则自动查找"></div>' +
      '<div class="form-group"><label>启动参数</label><input id="f-args" placeholder="用空格分隔"></div>' +
      '<div class="form-row">' +
        '<div class="form-group"><label>最大重启</label><input type="number" id="f-max-restarts" value="3" min="0" max="10"></div>' +
        '<div class="form-group"><label>重启延迟(秒)</label><input type="number" id="f-restart-delay" value="5" min="1" max="60"></div>' +
      '</div>' +
      '<div class="form-check"><input type="checkbox" id="f-autostart"> 自动启动</div>' +
      '<div class="form-check"><input type="checkbox" id="f-restart" checked> 崩溃自动重启</div>';
  } else {
    el.innerHTML = '';
  }
}

function submitForm() {
  const name = $('#f-name').value.trim();
  const type = $('#f-type').value;
  const target = $('#f-target').value.trim();

  if (!name) { toast('名称不能为空', 'error'); return; }

  const payload = { name, type, enabled: true };

  if (type === 'http' || type === 'https') {
    if (!target) { toast('目标不能为空', 'error'); return; }
    payload.target = target;
  } else if (type === 'tcp' || type === 'udp') {
    payload.target = target || '127.0.0.1:0';
  } else if (type === 'ser2mq') {
    const broker = $('#f-broker').value.trim();
    const secret = $('#f-secret').value.trim();
    if (!broker) { toast('MQTT Broker 不能为空', 'error'); return; }
    if (!secret || secret.length !== 64) { toast('加密密钥必须为 64 字符 hex', 'error'); return; }
    const port = $('#f-serial-port').value.trim() || '/dev/ttyUSB0';
    payload.target = broker;
    payload.para = {
      enable: true,
      broker,
      serial: {
        port,
        baudrate: parseInt($('#f-baudrate').value),
        databits: parseInt($('#f-databits').value),
        parity: $('#f-parity').value
      },
      secret
    };
  } else if (type === 'vpn-manager') {
    const binName = $('#f-binary-name').value.trim();
    if (!binName) { toast('程序名称不能为空', 'error'); return; }
    payload.target = binName;
    payload.para = {
      binary: { name: binName, path: $('#f-binary-path').value.trim() || undefined },
      args: ($('#f-args').value.trim() || '').split(/\s+/).filter(Boolean),
      lifecycle: {
        autostart: !!$('#f-autostart').checked,
        restart_on_crash: !!$('#f-restart').checked,
        max_restarts: parseInt($('#f-max-restarts').value) || 3,
        restart_delay: parseInt($('#f-restart-delay').value) || 5
      },
      watchdog: { enabled: true, interval: 10, quit_grace: 10 },
      log: { capture: true, max_size: 65536 }
    };
  }

  api.addTunnel(payload)
    .then(() => { closeModal(); toast('隧道已添加', 'success'); return refreshData(); })
    .catch(e => toast(e.message, 'error'));
}

// ===== 数据刷新（单飞） =====

async function refreshData() {
  if (refreshPending) return;
  refreshPending = true;

  try {
    const [status, tunnelList] = await Promise.all([
      api.getStatus(),
      api.listTunnels()
    ]);
    globalStatus = status;
    tunnels = tunnelList;
    const statsKey = JSON.stringify(status);
    if (statsKey !== lastStatsKey) {
      renderHeader(status);
      renderStatsBar(status);
      lastStatsKey = statsKey;
    }

    const tunnelListKey = JSON.stringify(tunnelList);
    if (tunnelListKey !== lastTunnelListKey) {
      renderTunnels();
      lastTunnelListKey = tunnelListKey;
    }

    // 更新详情面板
    if (selectedName) {
      const t = tunnels.find(t => t.name === selectedName);
      if (t) renderDetail(t);
    }
    setRefreshInfo('最近刷新: ' + new Date().toLocaleTimeString('zh-CN'));
  } catch (e) {
    staleConnectionBadge();
    setRefreshInfo('刷新失败: ' + (e.message || e), true);
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

// ===== 事件绑定 =====

// 表格事件委托
$('#tunnels-tbody').addEventListener('click', (e) => {
  const btn = e.target.closest('[data-action]');
  if (!btn) return;
  const action = btn.dataset.action;
  const name = btn.dataset.name;

  if (action === 'delete') {
    if (!confirm('确认删除隧道 ' + name + '?')) return;
    api.removeTunnel(name)
      .then(() => { toast('已删除', 'success'); if (selectedName === name) { selectedName = null; $('#detail-panel').classList.add('hidden'); } return refreshData(); })
      .catch(e => toast(e.message, 'error'));
  } else if (action === 'detail') {
    selectedName = name;
    const t = tunnels.find(t => t.name === name);
    if (t) renderDetail(t);
    renderTunnels();
  }
});

$('#btn-add').addEventListener('click', openModal);
$('#btn-cancel').addEventListener('click', closeModal);
$('#btn-submit').addEventListener('click', submitForm);
$('#f-type').addEventListener('change', function () { renderParaFields(this.value); });
$('#btn-close-detail').addEventListener('click', () => {
  selectedName = null;
  $('#detail-panel').classList.add('hidden');
  renderTunnels();
});

// Modal overlay 点击关闭
$('#edit-modal .modal-overlay').addEventListener('click', closeModal);

// ===== 启动 =====
refreshData().then(scheduleRefresh);
