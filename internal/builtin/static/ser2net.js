// ser2net.js — 串口转 TCP/UDP 隧道管理
import { api } from './api.js';
import { setText, fmtBytes, esc, toast, activateSubpanel, emptyStateMarkup, renderVizBars, renderVizRing } from './main.js';

let tunnels = [];
let editingName = null;

export function initSer2Net() {
  const tbody = document.getElementById('ser2net-tbody');

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
    } else if (action === 'toggle') {
      const t = tunnels.find(t => t.name === name);
      if (!t) return;
      const para = Object.assign({}, t.para, { enable: !t.enabled });
      api.addTunnel({ name: t.name, type: t.type, target: t.target, enabled: !t.enabled, para })
        .then(() => toast(t.enabled ? '已禁用' : '已启用', 'success'))
        .catch(e => toast(e.message, 'error'));
    }
  });

  document.getElementById('btn-add-ser2net').addEventListener('click', showAddForm);
  document.getElementById('btn-add-ser2net-secondary').addEventListener('click', showAddForm);
  document.getElementById('btn-cancel-ser2net').addEventListener('click', hideForm);
  document.getElementById('btn-save-ser2net').addEventListener('click', saveForm);
  document.getElementById('btn-refresh-ser2net').addEventListener('click', () => {
    api.listTunnels().then(list => {
      tunnels = list.filter(t => t.type === 'ser2tcp' || t.type === 'ser2udp');
      render();
    }).catch(() => {});
  });

  document.getElementById('snf-type').addEventListener('change', updateModeHint);

  window.__ser2netRefresh = (allTunnels) => {
    tunnels = allTunnels.filter(t => t.type === 'ser2tcp' || t.type === 'ser2udp');
    render();
  };
}

function render() {
  const tbody = document.getElementById('ser2net-tbody');
  const onlineCount = tunnels.filter(t => t.connected).length;
  const enabledCount = tunnels.filter(t => t.enabled).length;
  const tcpCount = tunnels.filter(t => t.type === 'ser2tcp').length;
  const udpCount = tunnels.filter(t => t.type === 'ser2udp').length;

  setText('ser2net-total', String(tunnels.length));
  setText('ser2net-online', String(onlineCount));
  setText('ser2net-enabled', String(enabledCount));
  renderVizBars('ser2net-type-chart', [
    { label: 'TCP', value: tcpCount, color: '#f97316' },
    { label: 'UDP', value: udpCount, color: '#a855f7' },
  ], '暂无 Ser2Net 隧道', '新增串口转 TCP/UDP 隧道后这里会显示类型分布。');
  renderVizRing('ser2net-health-ring', onlineCount, tunnels.length, '#f97316');

  if (!tunnels.length) {
    tbody.innerHTML = `<tr><td colspan="8" class="table-empty-cell">${emptyStateMarkup('暂无串口网络隧道', '创建串口转 TCP/UDP 桥接，实现串口设备的网络透明传输。', 'N')}</td></tr>`;
    return;
  }

  tbody.innerHTML = tunnels.map(t => {
    const s = t.status || {};
    const running = !!t.connected;
    const enabledClass = t.enabled ? 'badge-on' : 'badge-off';
    const enabledText = t.enabled ? '启用' : '禁用';
    const runningClass = running ? 'badge-on' : 'badge-off';
    const runningText = running ? '运行中' : '已停止';
    const errorInfo = s.error ? `<span class="badge badge-err" title="${esc(s.error)}">错误</span>` : '';
    const serialBadge = `<span class="badge ${s.serial_open ? 'badge-on' : 'badge-off'}">Serial</span>`;
    const clientsInfo = s.clients > 0 ? ` (${s.clients}客户端)` : '';
    const modeBadge = `<span class="badge" style="background:#e0e7ff;color:#3730a3">${esc(s.mode || '-')}</span>`;

    let actions = `<button class="btn btn-sm" data-action="edit" data-name="${esc(t.name)}">编辑</button>`;
    actions += t.enabled
      ? `<button class="btn btn-sm" data-action="toggle" data-name="${esc(t.name)}">禁用</button>`
      : `<button class="btn btn-primary btn-sm" data-action="toggle" data-name="${esc(t.name)}">启用</button>`;
    actions += `<button class="btn btn-danger btn-sm" data-action="delete" data-name="${esc(t.name)}">删除</button>`;

    return `<tr>
      <td><strong>${esc(t.name)}</strong></td>
      <td><span class="badge badge-${t.type}">${esc(t.type).toUpperCase()}</span></td>
      <td>${esc(s.serial_port || '-')}</td>
      <td>${modeBadge}</td>
      <td style="font-size:12px;color:#667085">${esc(s.address || '-')}</td>
      <td><span class="badge ${enabledClass}">${enabledText}</span> <span class="badge ${runningClass}">${runningText}</span>${errorInfo}</td>
      <td>${serialBadge}${esc(clientsInfo)} ${fmtBytes(s.bytes_in || t.bytes_in)} / ${fmtBytes(s.bytes_out || t.bytes_out)}</td>
      <td class="actions">${actions}</td>
    </tr>`;
  }).join('');
}

function showAddForm() {
  activateSubpanel('serial', 'serial-form-view');
  editingName = null;
  document.getElementById('ser2net-form-title').textContent = '新增串口网络隧道';
  document.getElementById('snf-name').value = '';
  document.getElementById('snf-name').disabled = false;
  document.getElementById('snf-type').value = 'ser2tcp';
  document.getElementById('snf-mode').value = 'server';
  document.getElementById('snf-address').value = ':5000';
  document.getElementById('snf-max-conn').value = '1';
  document.getElementById('snf-port').value = '/dev/ttyUSB0';
  document.getElementById('snf-baudrate').value = '9600';
  document.getElementById('snf-databits').value = '8';
  document.getElementById('snf-stopbits').value = '1';
  document.getElementById('snf-parity').value = 'N';
  document.getElementById('snf-timeout').value = '3000';
  document.getElementById('snf-enable').checked = true;
  updateModeHint();
  document.getElementById('ser2net-form').style.display = 'block';
  document.getElementById('ser2mq-form').style.display = 'none';
}

function showEditForm(t) {
  activateSubpanel('serial', 'serial-form-view');
  editingName = t.name;
  document.getElementById('ser2net-form-title').textContent = '编辑串口网络隧道';
  document.getElementById('snf-name').value = t.name;
  document.getElementById('snf-name').disabled = true;
  document.getElementById('snf-type').value = t.type;
  document.getElementById('snf-enable').checked = t.enabled;

  const p = t.para || {};
  const serial = p.serial || {};
  document.getElementById('snf-mode').value = p.mode || 'server';
  document.getElementById('snf-address').value = p.address || '';
  document.getElementById('snf-max-conn').value = String(p.max_conn || 1);
  if (serial.port) document.getElementById('snf-port').value = serial.port;
  if (serial.baudrate) document.getElementById('snf-baudrate').value = String(serial.baudrate);
  if (serial.databits) document.getElementById('snf-databits').value = String(serial.databits);
  if (serial.stopbits) document.getElementById('snf-stopbits').value = String(serial.stopbits);
  if (serial.parity) document.getElementById('snf-parity').value = serial.parity;
  document.getElementById('snf-timeout').value = serial.timeout || 3000;

  updateModeHint();
  document.getElementById('ser2net-form').style.display = 'block';
  document.getElementById('ser2mq-form').style.display = 'none';
}

function hideForm() {
  document.getElementById('ser2net-form').style.display = 'none';
  editingName = null;
  activateSubpanel('serial', 'serial-list-view');
}

function updateModeHint() {
  const typ = document.getElementById('snf-type').value;
  const mode = document.getElementById('snf-mode').value;
  const hint = document.getElementById('snf-mode-hint');
  const maxConnRow = document.getElementById('snf-max-conn-row');

  if (typ === 'ser2tcp' && mode === 'server') {
    hint.textContent = 'TCP Server 模式：监听指定端口，接受客户端连接，串口数据广播给所有客户端。';
    maxConnRow.style.display = '';
  } else if (typ === 'ser2tcp' && mode === 'client') {
    hint.textContent = 'TCP Client 模式：主动连接远端 TCP 服务器，断线自动重连。';
    maxConnRow.style.display = 'none';
  } else if (typ === 'ser2udp' && mode === 'server') {
    hint.textContent = 'UDP Server 模式：绑定本地 UDP 端口，自动学习远端地址，串口数据广播给所有已知远端。';
    maxConnRow.style.display = 'none';
  } else {
    hint.textContent = 'UDP Client 模式：向指定远端发送串口数据，接收远端 UDP 数据转发到串口。';
    maxConnRow.style.display = 'none';
  }

  const addrLabel = document.getElementById('snf-address-label');
  addrLabel.textContent = mode === 'server' ? '监听地址' : '远端地址';
  document.getElementById('snf-address').placeholder = mode === 'server' ? ':5000' : '192.168.1.100:5000';
}

function saveForm() {
  const name = document.getElementById('snf-name').value.trim();
  const typ = document.getElementById('snf-type').value;
  const mode = document.getElementById('snf-mode').value;
  const address = document.getElementById('snf-address').value.trim();
  const port = document.getElementById('snf-port').value.trim();

  if (!name) { toast('名称不能为空', 'error'); return; }
  if (!address) { toast('地址不能为空', 'error'); return; }
  if (!port) { toast('串口端口不能为空', 'error'); return; }

  const payload = {
    name,
    type: typ,
    target: port,
    enabled: document.getElementById('snf-enable').checked,
    para: {
      enable: document.getElementById('snf-enable').checked,
      mode,
      address,
      max_conn: parseInt(document.getElementById('snf-max-conn').value) || 1,
      serial: {
        port,
        baudrate: parseInt(document.getElementById('snf-baudrate').value),
        databits: parseInt(document.getElementById('snf-databits').value),
        stopbits: parseFloat(document.getElementById('snf-stopbits').value),
        parity: document.getElementById('snf-parity').value,
        timeout: parseInt(document.getElementById('snf-timeout').value) || 3000
      }
    }
  };

  api.addTunnel(payload)
    .then(() => { hideForm(); toast(editingName ? '隧道已更新' : '隧道已添加', 'success'); })
    .catch(e => toast(e.message, 'error'));
}
