// ser2mq.js — 串口转 MQTT 隧道管理（移植自 mole-cgui，适配 REST API + SSE）
import { api } from './api.js';
import { openSer2MQStream, closeSer2MQStream } from './ser2mq-stream.js';
import { setText, fmtBytes, esc, toast, activateTopTab, activateSubpanel, emptyStateMarkup, renderVizBars, renderVizRing } from './main.js';

let tunnels = [];
let nodeID = '';
let editingName = null;

// 数据流状态
let flowPackets = [];
let flowRenderPending = false;
let flowPaused = false;
let flowTunnel = null;
let searchDebounceId = 0;

export function initSer2MQ() {
  const tbody = document.getElementById('ser2mq-tbody');

  // 表格事件委托
  tbody.addEventListener('click', (e) => {
    const btn = e.target.closest('[data-action]');
    if (!btn) return;
    const name = btn.dataset.name;
    const action = btn.dataset.action;
    if (action === 'delete') {
      if (!confirm('确认删除隧道 ' + name + '?')) return;
      api.removeTunnel(name)
        .then(() => toast('已删除', 'success'))
        .catch(e => toast(e.message, 'error'));
    } else if (action === 'edit') {
      const t = tunnels.find(t => t.name === name);
      if (t) showEditForm(t);
    } else if (action === 'stream') {
      openStream(name);
    } else if (action === 'toggle') {
      const t = tunnels.find(t => t.name === name);
      if (!t) return;
      const para = Object.assign({}, t.para, { enable: !t.enabled });
      api.addTunnel({ name: t.name, type: 'ser2mq', target: t.target, enabled: !t.enabled, para })
        .then(() => toast(t.enabled ? '已禁用' : '已启用', 'success'))
        .catch(e => toast(e.message, 'error'));
    }
  });

  // 表单按钮
  document.getElementById('btn-add-ser2mq').addEventListener('click', showAddForm);
  document.getElementById('btn-add-ser2mq-secondary').addEventListener('click', showAddForm);
  document.getElementById('btn-cancel-ser2mq').addEventListener('click', hideForm);
  document.getElementById('btn-save-ser2mq').addEventListener('click', saveForm);
  document.getElementById('btn-gen-key').addEventListener('click', generateKey);
  document.getElementById('btn-refresh-ser2mq').addEventListener('click', () => {
    api.listTunnels().then(list => {
      tunnels = list.filter(t => t.type === 'ser2mq');
      render();
    }).catch(() => {});
  });

  // 串口端口输入更新 Topic 预览
  document.getElementById('sf-port').addEventListener('input', updateTopicPreview);

  // 数据流控件
  document.getElementById('df-select-tunnel').addEventListener('change', (e) => {
    const name = e.target.value;
    if (name) openStream(name);
    else {
      flowTunnel = null;
      setText('serial-streaming', '未选择');
      closeSer2MQStream();
      renderFlow();
    }
  });
  document.getElementById('df-filter-dir').addEventListener('change', scheduleFlowRender);
  document.getElementById('df-filter-search').addEventListener('input', () => {
    if (searchDebounceId) clearTimeout(searchDebounceId);
    searchDebounceId = setTimeout(scheduleFlowRender, 180);
  });
  document.getElementById('btn-pause-flow').addEventListener('click', togglePause);
  document.getElementById('btn-clear-flow').addEventListener('click', () => {
    flowPackets = [];
    renderFlow();
  });

  // 全局刷新回调
  window.__ser2mqRefresh = (allTunnels) => {
    tunnels = allTunnels.filter(t => t.type === 'ser2mq');
    render();
  };

  // 从 status 获取 nodeID
  window.__ser2mqNodeID = (id) => { nodeID = id; };
}

// ===== 渲染隧道表格 =====
function render() {
  const tbody = document.getElementById('ser2mq-tbody');
  const onlineCount = tunnels.filter(t => t.connected).length;
  const enabledCount = tunnels.filter(t => t.enabled).length;
  const firstBroker = tunnels.find(t => (t.status || {}).broker || t.target);
  const firstPort = tunnels.find(t => (t.status || {}).serial_port);
  setText('serial-total', String(tunnels.length));
  setText('serial-online', String(onlineCount));
  setText('serial-enabled', String(enabledCount));
  setText('serial-streaming', flowTunnel || '未选择');
  setText('serial-broker-summary', firstBroker ? ((firstBroker.status || {}).broker || firstBroker.target || '-') : '暂无 Broker');
  setText('serial-port-summary', firstPort ? ((firstPort.status || {}).serial_port || '-') : '暂无端口');
  setText('serial-health-note', tunnels.length ? `当前 ${onlineCount}/${tunnels.length} 条链路在线` : '创建串口桥接后可查看可用率');
  renderVizBars('serial-state-chart', [
    { label: '运行中', value: onlineCount, color: '#10b981' },
    { label: '已启用', value: Math.max(enabledCount - onlineCount, 0), color: '#3b82f6' },
    { label: '未启用', value: Math.max(tunnels.length - enabledCount, 0), color: '#94a3b8' }
  ], '暂无运行分布', '新增 Ser2MQ 隧道后这里会显示运行状态。');
  renderVizRing('serial-health-ring', onlineCount, tunnels.length, '#06b6d4');

  if (!tunnels.length) {
    tbody.innerHTML = `<tr><td colspan="8" class="table-empty-cell">${emptyStateMarkup('暂无 Ser2MQ 隧道', '建议先创建串口桥接，配置好 Broker、密钥和串口参数后，再进入实时数据查看收发内容。', 'S')}</td></tr>`;
    return;
  }
  tbody.innerHTML = tunnels.map(t => {
    const s = t.status || {};
    const running = !!t.connected;
    const enabledClass = t.enabled ? 'badge-on' : 'badge-off';
    const enabledText = t.enabled ? '启用' : '禁用';
    const runningClass = running ? 'badge-on' : 'badge-off';
    const runningText = running ? '运行中' : '已停止';
    const errorInfo = s.error ? `<span class="badge badge-err" title="${esc(s.error)}">${esc(s.error_phase || '错误')}</span>` : '';
    const mqttBadge = `<span class="badge ${s.mqtt_connected ? 'badge-on' : 'badge-off'}">MQTT</span>`;
    const serialBadge = `<span class="badge ${s.serial_open ? 'badge-on' : 'badge-off'}">Serial</span>`;

    let actions = `<button class="btn btn-sm" data-action="stream" data-name="${esc(t.name)}">数据流</button>`;
    actions += `<button class="btn btn-sm" data-action="edit" data-name="${esc(t.name)}">编辑</button>`;
    actions += t.enabled
      ? `<button class="btn btn-sm" data-action="toggle" data-name="${esc(t.name)}">禁用</button>`
      : `<button class="btn btn-primary btn-sm" data-action="toggle" data-name="${esc(t.name)}">启用</button>`;
    actions += `<button class="btn btn-danger btn-sm" data-action="delete" data-name="${esc(t.name)}">删除</button>`;

    return `<tr>
      <td><strong>${esc(t.name)}</strong></td>
      <td style="font-size:12px;color:#667085;max-width:120px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap">${esc(s.broker || t.target || '-')}</td>
      <td>${esc(s.serial_port || '-')}</td>
      <td><span class="badge ${enabledClass}">${enabledText}</span></td>
      <td><span class="badge ${runningClass}">${runningText}</span>${errorInfo}</td>
      <td>${mqttBadge} ${serialBadge}</td>
      <td>${fmtBytes(s.bytes_in || t.bytes_in)} / ${fmtBytes(s.bytes_out || t.bytes_out)}</td>
      <td class="actions">${actions}</td>
    </tr>`;
  }).join('');
  updateFlowTunnelOptions();
}

function updateFlowTunnelOptions() {
  const sel = document.getElementById('df-select-tunnel');
  const current = sel.value;
  sel.innerHTML = '<option value="">选择隧道</option>' +
    tunnels.map(t => `<option value="${esc(t.name)}">${esc(t.name)}${t.connected ? ' (运行中)' : ''}</option>`).join('');
  if (current && tunnels.some(t => t.name === current)) sel.value = current;
}
function showAddForm() {
  activateSubpanel('serial', 'serial-form-view');
  editingName = null;
  document.getElementById('ser2mq-form-title').textContent = '新增 Ser2MQ 隧道';
  document.getElementById('sf-name').value = '';
  document.getElementById('sf-name').disabled = false;
  document.getElementById('sf-broker').value = '';
  document.getElementById('sf-secret').value = '';
  document.getElementById('sf-qos').value = '1';
  document.getElementById('sf-port').value = '/dev/ttyUSB0';
  document.getElementById('sf-baudrate').value = '9600';
  document.getElementById('sf-databits').value = '8';
  document.getElementById('sf-stopbits').value = '1';
  document.getElementById('sf-parity').value = 'N';
  document.getElementById('sf-enable').checked = true;
  document.getElementById('topic-preview').style.display = 'none';
  document.getElementById('ser2mq-form').style.display = 'block';
}

function showEditForm(t) {
  activateSubpanel('serial', 'serial-form-view');
  editingName = t.name;
  document.getElementById('ser2mq-form-title').textContent = '编辑 Ser2MQ 隧道';
  document.getElementById('sf-name').value = t.name;
  document.getElementById('sf-name').disabled = true;
  document.getElementById('sf-broker').value = t.target || '';
  document.getElementById('sf-enable').checked = t.enabled;

  const s = t.status || {};
  const p = t.para || {};
  const serial = p.serial || {};
  if (serial.port) document.getElementById('sf-port').value = serial.port;
  if (serial.baudrate) document.getElementById('sf-baudrate').value = String(serial.baudrate);
  if (serial.databits) document.getElementById('sf-databits').value = String(serial.databits);
  if (serial.stopbits) document.getElementById('sf-stopbits').value = String(serial.stopbits);
  if (serial.parity) document.getElementById('sf-parity').value = serial.parity;
  document.getElementById('sf-timeout').value = serial.timeout || 3000;

  document.getElementById('ser2mq-form').style.display = 'block';
  updateTopicPreview();
}

function hideForm() {
  document.getElementById('ser2mq-form').style.display = 'none';
  editingName = null;
  activateSubpanel('serial', 'serial-list-view');
}

function saveForm() {
  const name = document.getElementById('sf-name').value.trim();
  const broker = document.getElementById('sf-broker').value.trim();
  const secret = document.getElementById('sf-secret').value.trim();
  const port = document.getElementById('sf-port').value.trim();

  if (!name) { toast('名称不能为空', 'error'); return; }
  if (!broker) { toast('Broker 不能为空', 'error'); return; }
  if (!editingName && (!secret || secret.length !== 64)) { toast('加密密钥必须为 64 字符 hex', 'error'); return; }
  if (!port) { toast('串口端口不能为空', 'error'); return; }

  const payload = {
    name,
    type: 'ser2mq',
    target: broker,
    enabled: document.getElementById('sf-enable').checked,
    para: {
      enable: document.getElementById('sf-enable').checked,
      broker,
      serial: {
        port,
        baudrate: parseInt(document.getElementById('sf-baudrate').value),
        databits: parseInt(document.getElementById('sf-databits').value),
        stopbits: parseFloat(document.getElementById('sf-stopbits').value),
        parity: document.getElementById('sf-parity').value,
        timeout: parseInt(document.getElementById('sf-timeout').value) || 3000
      },
      qos: parseInt(document.getElementById('sf-qos').value) || 1
    }
  };
  if (secret) payload.para.secret = secret;

  api.addTunnel(payload)
    .then(() => { hideForm(); toast(editingName ? '隧道已更新' : '隧道已添加', 'success'); })
    .catch(e => toast(e.message, 'error'));
}

// ===== 密钥生成（客户端） =====
function generateKey() {
  const bytes = new Uint8Array(32);
  crypto.getRandomValues(bytes);
  const hex = Array.from(bytes).map(b => b.toString(16).padStart(2, '0')).join('');
  document.getElementById('sf-secret').value = hex;
}

// ===== Topic 预览 =====
function sanitizePortName(port) {
  let name = port;
  if (name.startsWith('/dev/')) name = name.substring(5);
  if (name.startsWith('\\\\.\\')) name = name.substring(4);
  return name;
}

function updateTopicPreview() {
  const port = document.getElementById('sf-port').value.trim();
  const preview = document.getElementById('topic-preview');
  if (!nodeID || !port) { preview.style.display = 'none'; return; }
  const p = sanitizePortName(port);
  document.getElementById('tp-sub').textContent = `/mole/${nodeID}/serial/${p}/out`;
  document.getElementById('tp-pub').textContent = `/mole/${nodeID}/serial/${p}/in`;
  preview.style.display = 'block';
}

// ===== 数据流 =====
function openStream(name) {
  activateTopTab('serial');
  activateSubpanel('serial', 'serial-flow-view');
  flowPackets = [];
  flowTunnel = name;
  flowPaused = false;
  updatePauseBtn();
  setText('serial-streaming', name);
  document.getElementById('df-select-tunnel').value = name;
  renderFlow();

  closeSer2MQStream();
  openSer2MQStream(name, {
    onPacket: (evt) => {
      flowPackets.push(evt);
      if (flowPackets.length > 1000) flowPackets = flowPackets.slice(-1000);
      scheduleFlowRender();
    },
    onError: (err) => console.warn('stream error:', err.message)
  }, { tail: 50 });

  document.getElementById('ser2mq-dataflow').scrollIntoView({ behavior: 'smooth' });
}

function scheduleFlowRender() {
  if (!flowRenderPending) {
    flowRenderPending = true;
    requestAnimationFrame(() => { renderFlow(); flowRenderPending = false; });
  }
}

function renderFlow() {
  const list = document.getElementById('dataflow-list');
  if (flowPaused || !flowPackets.length) {
    if (!flowPackets.length) {
      list.innerHTML = emptyStateMarkup(flowTunnel ? '正在等待数据包' : '暂无数据流', flowTunnel ? '链路已打开，但暂时还没有新的串口或 MQTT 数据到达。' : '先选择一条 Ser2MQ 隧道并打开数据流，再在这里观察实时收发。', '~', true);
    }
    return;
  }

  const dirFilter = document.getElementById('df-filter-dir').value;
  const search = document.getElementById('df-filter-search').value.toLowerCase();

  const filtered = flowPackets.filter(p => {
    if (dirFilter !== 'all') {
      if (dirFilter === 'serial' && !p.dir.startsWith('serial')) return false;
      if (dirFilter === 'mqtt' && !p.dir.startsWith('mqtt')) return false;
    }
    if (search && p.hex_preview && !p.hex_preview.toLowerCase().includes(search)) return false;
    return true;
  });

  const visible = filtered.slice(-200);
  const nearBottom = list.scrollHeight - list.scrollTop - list.clientHeight <= 24;

  if (!visible.length) {
    list.innerHTML = emptyStateMarkup('没有匹配结果', '当前筛选条件下没有找到符合要求的数据包，请调整方向过滤或搜索关键字。', '?', true);
    return;
  }

  list.innerHTML = visible.map(p => {
    const dir = p.dir || '?';
    const time = p.time ? new Date(p.time).toLocaleTimeString('zh-CN', { hour12: false, hour: '2-digit', minute: '2-digit', second: '2-digit', fractionalSecondDigits: 3 }) : '';
    const len = p.length != null ? p.length + 'B' : '';
    const msg = p.message || '';
    const hex = p.hex_preview || '';
    return `<div class="flow-item">
      <span class="flow-time">${time}</span>
      <span class="flow-dir ${dir}">${dir}</span>
      ${len ? `<span class="flow-len">${len}</span>` : ''}
      ${msg ? `<span class="flow-msg">${esc(msg)}</span>` : ''}
      ${hex ? `<span class="flow-hex">${esc(hex)}</span>` : ''}
    </div>`;
  }).join('');

  if (nearBottom) list.scrollTop = list.scrollHeight;
}

function togglePause() {
  flowPaused = !flowPaused;
  updatePauseBtn();
  if (!flowPaused) scheduleFlowRender();
}

function updatePauseBtn() {
  const btn = document.getElementById('btn-pause-flow');
  btn.textContent = flowPaused ? '继续' : '暂停';
  btn.classList.toggle('btn-primary', flowPaused);
}
