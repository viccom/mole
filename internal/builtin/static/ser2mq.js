// ser2mq.js — 串口转 MQTT 隧道管理（移植自 mole-cgui，适配 REST API + SSE）
import { api } from './api.js';
import { openSer2MQStream, closeSer2MQStream } from './ser2mq-stream.js';
import { fmtBytes, esc, toast } from './main.js';

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
    }
  });

  // 表单按钮
  document.getElementById('btn-add-ser2mq').addEventListener('click', showAddForm);
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
    else closeSer2MQStream();
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
  if (!tunnels.length) {
    tbody.innerHTML = '<tr><td colspan="7" style="text-align:center;color:#aaa;padding:20px">暂无 Ser2MQ 隧道</td></tr>';
    return;
  }
  tbody.innerHTML = tunnels.map(t => {
    const s = t.status || {};
    const running = !!t.connected;
    const enabledClass = t.enabled ? 'badge-on' : 'badge-off';
    const enabledText = t.enabled ? '启用' : '禁用';
    const runningClass = running ? 'badge-on' : 'badge-off';
    const runningText = running ? '运行中' : '已停止';

    let actions = `<button class="btn btn-sm" data-action="stream" data-name="${esc(t.name)}">数据流</button>`;
    actions += `<button class="btn btn-sm" data-action="edit" data-name="${esc(t.name)}">编辑</button>`;
    actions += `<button class="btn btn-danger btn-sm" data-action="delete" data-name="${esc(t.name)}">删除</button>`;

    return `<tr>
      <td><strong>${esc(t.name)}</strong></td>
      <td style="font-size:12px;color:#667085;max-width:120px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap">${esc(s.broker || t.target || '-')}</td>
      <td>${esc(s.serial_port || '-')}</td>
      <td><span class="badge ${enabledClass}">${enabledText}</span></td>
      <td><span class="badge ${runningClass}">${runningText}</span></td>
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
  editingName = t.name;
  document.getElementById('ser2mq-form-title').textContent = '编辑 Ser2MQ 隧道';
  document.getElementById('sf-name').value = t.name;
  document.getElementById('sf-name').disabled = true;
  document.getElementById('sf-broker').value = t.target || '';
  document.getElementById('sf-enable').checked = t.enabled;

  const s = t.status || {};
  if (s.serial_port) document.getElementById('sf-port').value = s.serial_port;
  if (s.baudrate) document.getElementById('sf-baudrate').value = String(s.baudrate);
  if (s.databits) document.getElementById('sf-databits').value = String(s.databits);
  if (s.parity) document.getElementById('sf-parity').value = s.parity;

  document.getElementById('ser2mq-form').style.display = 'block';
  updateTopicPreview();
}

function hideForm() {
  document.getElementById('ser2mq-form').style.display = 'none';
  editingName = null;
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
        timeout: 3000
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
  flowPackets = [];
  flowTunnel = name;
  flowPaused = false;
  updatePauseBtn();
  document.getElementById('df-select-tunnel').value = name;

  closeSer2MQStream();
  openSer2MQStream(name, {
    onPacket: (evt) => {
      flowPackets.push(evt);
      if (flowPackets.length > 1000) flowPackets = flowPackets.slice(-1000);
      scheduleFlowRender();
    },
    onError: (err) => console.warn('stream error:', err.message)
  }, { tail: 50 });

  // 切换到串口 tab 并滚动到数据流
  document.querySelectorAll('.tab').forEach(t => t.classList.remove('active'));
  document.querySelectorAll('.panel').forEach(p => p.classList.remove('active'));
  document.querySelector('[data-tab="serial"]').classList.add('active');
  document.getElementById('panel-serial').classList.add('active');
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
      list.innerHTML = '<div class="dataflow-empty">暂无数据</div>';
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
