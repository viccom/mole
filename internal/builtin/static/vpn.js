import { api } from './api.js';
import { fmtBytes, esc, toast, activateSubpanel, emptyStateMarkup, renderVizBars, renderVizRing } from './main.js';

let tunnels = [];

export function initVPN() {
  const tbody = document.getElementById('vpn-tbody');

  tbody.addEventListener('click', (e) => {
    const btn = e.target.closest('[data-action]');
    if (!btn) return;
    const name = btn.dataset.name;
    const action = btn.dataset.action;
    if (action === 'delete') {
      if (!confirm('确认删除 VPN 隧道 ' + name + '?')) return;
      api.removeTunnel(name)
        .then(() => toast('已删除', 'success'))
        .catch(e => toast(e.message, 'error'));
    } else if (action === 'edit') {
      const t = tunnels.find(t => t.name === name);
      if (t) showEditForm(t);
    } else if (action === 'start') {
      api.startVPNTunnel(name)
        .then(() => toast('启动中...', 'success'))
        .catch(e => toast(e.message, 'error'));
    } else if (action === 'stop') {
      api.stopVPNTunnel(name)
        .then(() => toast('已停止', 'success'))
        .catch(e => toast(e.message, 'error'));
    } else if (action === 'detail') {
      showDetail(name);
    }
  });

  document.getElementById('btn-add-vpn').addEventListener('click', showAddForm);
  document.getElementById('btn-add-vpn-secondary').addEventListener('click', showAddForm);
  document.getElementById('btn-cancel-vpn').addEventListener('click', hideForm);
  document.getElementById('btn-save-vpn').addEventListener('click', saveForm);
  document.getElementById('btn-refresh-vpn').addEventListener('click', () => {
    api.listTunnels().then(list => {
      tunnels = list.filter(t => t.type === 'vpn-manager');
      render();
    }).catch(() => {});
  });
  document.getElementById('btn-close-vpn-detail').addEventListener('click', () => {
    document.getElementById('vpn-detail').style.display = 'none';
    document.getElementById('vpn-detail-empty').style.display = 'block';
    activateSubpanel('vpn', 'vpn-list-view');
  });

  window.__vpnRefresh = (allTunnels) => {
    tunnels = allTunnels.filter(t => t.type === 'vpn-manager');
    render();
  };
}

function render() {
  const tbody = document.getElementById('vpn-tbody');
  const onlineCount = tunnels.filter(t => t.connected).length;
  const enabledCount = tunnels.filter(t => t.enabled).length;
  const errorTunnels = tunnels.filter(t => (t.status || {}).error);
  const firstRunning = tunnels.find(t => t.connected);
  setText('vpn-total', String(tunnels.length));
  setText('vpn-online', String(onlineCount));
  setText('vpn-enabled', String(enabledCount));
  setText('vpn-error-count', String(errorTunnels.length));
  setText('vpn-health-summary', !tunnels.length ? '暂无实例' : (errorTunnels.length ? `${errorTunnels.length} 个实例异常` : '运行状态稳定'));
  setText('vpn-running-summary', firstRunning ? `${firstRunning.name} 在线` : '暂无在线实例');
  setText('vpn-health-note', tunnels.length ? `健康实例 ${Math.max(tunnels.length - errorTunnels.length, 0)}/${tunnels.length}` : '创建 VPN 实例后可查看健康率');
  renderVizBars('vpn-state-chart', [
    { label: '运行中', value: onlineCount, color: '#10b981' },
    { label: '异常', value: errorTunnels.length, color: '#ef4444' },
    { label: '待机', value: Math.max(tunnels.length - onlineCount - errorTunnels.length, 0), color: '#94a3b8' }
  ], '暂无实例状态', '新增 VPN 实例后这里会显示健康与运行分布。');
  renderVizRing('vpn-health-ring', Math.max(tunnels.length - errorTunnels.length, 0), tunnels.length, '#22c55e');

  if (!tunnels.length) {
    tbody.innerHTML = `<tr><td colspan="7" class="table-empty-cell">${emptyStateMarkup('暂无 VPN 隧道', '先创建一个 VPN 管理实例，配置程序名和连接参数后，就可以在这里启动、停机和排查状态。', 'V')}</td></tr>`;
    return;
  }
  tbody.innerHTML = tunnels.map(t => {
    const s = t.status || {};
    const running = !!t.connected;
    const hasError = !!s.error;
    const enabledClass = t.enabled ? 'badge-on' : 'badge-off';
    const enabledText = t.enabled ? '启用' : '禁用';

    let statusHtml;
    if (running) {
      statusHtml = '<span class="status on">运行中</span>';
    } else if (hasError) {
      statusHtml = `<span class="status off" title="${esc(s.error_phase || '')}: ${esc(s.error)}">启动失败</span>`;
    } else {
      statusHtml = '<span class="status off">已停止</span>';
    }

    let actions = '';
    if (running) {
      actions += `<button class="btn btn-sm" data-action="detail" data-name="${esc(t.name)}">详情</button>`;
      actions += `<button class="btn btn-sm" data-action="stop" data-name="${esc(t.name)}">停止</button>`;
    } else {
      actions += `<button class="btn btn-primary btn-sm" data-action="start" data-name="${esc(t.name)}">启动</button>`;
    }
    actions += `<button class="btn btn-sm" data-action="edit" data-name="${esc(t.name)}">编辑</button>`;
    actions += `<button class="btn btn-danger btn-sm" data-action="delete" data-name="${esc(t.name)}">删除</button>`;

    const pid = s.pid ? `PID ${s.pid}` : '-';
    const uptime = s.start_time ? formatUptime(Date.now() - s.start_time) : '-';

    return `<tr>
      <td><strong>${esc(t.name)}</strong></td>
      <td><span class="badge ${enabledClass}">${enabledText}</span></td>
      <td>${statusHtml}</td>
      <td style="font-size:12px;color:#667085">${pid}</td>
      <td style="font-size:12px;color:#667085">${uptime}</td>
      ${hasError ? `<td style="color:#d93025;font-size:12px" title="${esc(s.error)}">${esc(s.error_phase)}: ${esc(s.error).substring(0, 40)}</td>` : '<td>-</td>'}
      <td class="actions">${actions}</td>
    </tr>`;
  }).join('');
}

function formatUptime(ms) {
  const s = Math.floor(ms / 1000);
  if (s < 60) return s + '秒';
  const m = Math.floor(s / 60);
  if (m < 60) return m + '分钟';
  const h = Math.floor(m / 60);
  if (h < 24) return h + '小时';
  const d = Math.floor(h / 24);
  return d + '天';
}

function showAddForm() {
  activateSubpanel('vpn', 'vpn-form-view');
  editingName = null;
  document.getElementById('vpn-form-title').textContent = '新增 VPN 隧道';
  document.getElementById('vf-name').value = '';
  document.getElementById('vf-name').disabled = false;
  document.getElementById('vf-binary').value = 'vnt-cli';
  document.getElementById('vf-token').value = '';
  document.getElementById('vf-server').value = '';
  document.getElementById('vf-device-id').value = '';
  document.getElementById('vf-device-name').value = '';
  document.getElementById('vf-password').value = '';
  document.getElementById('vf-rest-port').value = '59871';
  document.getElementById('vf-autostart').checked = false;
  document.getElementById('vf-enable').checked = true;
  document.getElementById('vpn-form').style.display = 'block';
}

function showEditForm(t) {
  activateSubpanel('vpn', 'vpn-form-view');
  editingName = t.name;
  document.getElementById('vpn-form-title').textContent = '编辑 VPN 隧道';
  document.getElementById('vf-name').value = t.name;
  document.getElementById('vf-name').disabled = true;
  document.getElementById('vf-enable').checked = t.enabled;

  const para = t.para || {};
  const vnt = para.vnt || {};

  document.getElementById('vf-binary').value = (para.binary && para.binary.name) || 'vnt-cli';
  document.getElementById('vf-token').value = vnt.token || '';
  document.getElementById('vf-server').value = vnt.server || '';
  document.getElementById('vf-device-id').value = vnt.device_id || '';
  document.getElementById('vf-device-name').value = vnt.name || '';
  document.getElementById('vf-password').value = vnt.password || '';
  document.getElementById('vf-rest-port').value = String(vnt.rest_port || 59871);
  document.getElementById('vf-autostart').checked = !!(para.lifecycle && para.lifecycle.autostart);

  document.getElementById('vpn-form').style.display = 'block';
}

function hideForm() {
  document.getElementById('vpn-form').style.display = 'none';
  editingName = null;
  activateSubpanel('vpn', 'vpn-list-view');
}

let editingName = null;

function saveForm() {
  const name = document.getElementById('vf-name').value.trim();
  const binary = document.getElementById('vf-binary').value.trim();
  const token = document.getElementById('vf-token').value.trim();
  const server = document.getElementById('vf-server').value.trim();
  const deviceID = document.getElementById('vf-device-id').value.trim();

  if (!name) { toast('名称不能为空', 'error'); return; }
  if (!binary) { toast('程序名不能为空', 'error'); return; }
  if (!token) { toast('Token 不能为空', 'error'); return; }
  if (!server) { toast('服务器地址不能为空', 'error'); return; }
  if (!deviceID) { toast('设备ID不能为空', 'error'); return; }

  const args = ['-k', token, '-s', server, '-d', deviceID];
  const deviceName = document.getElementById('vf-device-name').value.trim();
  if (deviceName) args.push('-n', deviceName);
  const password = document.getElementById('vf-password').value.trim();
  if (password) args.push('-w', password);

  const restPort = parseInt(document.getElementById('vf-rest-port').value) || 59871;

  const payload = {
    name,
    type: 'vpn-manager',
    target: server,
    enabled: document.getElementById('vf-enable').checked,
    para: {
      binary: { name: binary },
      args,
      lifecycle: {
        autostart: document.getElementById('vf-autostart').checked,
        restart_on_crash: true,
        max_restarts: 3,
        restart_delay: 5,
      },
      watchdog: { enabled: true, interval: 10, quit_grace: 10 },
      log: { capture: true, max_size: 65536 },
      vnt: {
        enabled: true,
        token,
        server,
        device_id: deviceID,
        name: deviceName,
        password,
        rest_port: restPort,
      },
    },
  };

  api.addTunnel(payload)
    .then(() => { hideForm(); toast(editingName ? '隧道已更新' : '隧道已添加', 'success'); })
    .catch(e => toast(e.message, 'error'));
}

function showDetail(name) {
  const t = tunnels.find(t => t.name === name);
  if (!t) return;

  const s = t.status || {};
  const panel = document.getElementById('vpn-detail');
  document.getElementById('vpn-detail-title').textContent = name + ' — VPN 详情';

  let html = '';

  // 概览
  const info = s.vnt_info;
  if (info) {
    html += '<div class="card"><div class="card-header"><h2>连接概览</h2></div>';
    html += '<div class="info-grid">';
    html += infoItem('虚拟IP', info.virtual_ip);
    html += infoItem('网关', info.virtual_gateway);
    html += infoItem('子网掩码', info.virtual_netmask);
    html += infoItem('连接状态', info.connect_status);
    html += infoItem('中继服务器', info.relay_server);
    html += infoItem('NAT 类型', info.nat_type);
    html += infoItem('公网IP', info.public_ips);
    html += infoItem('本地地址', info.local_addr);
    html += '</div></div>';
  }

  // 系统信息
  const buildInfo = s.vnt_status;
  if (buildInfo) {
    html += '<div class="card"><div class="card-header"><h2>系统信息</h2></div>';
    html += '<div class="info-grid">';
    html += infoItem('版本', buildInfo.version);
    html += infoItem('CPU', buildInfo.cpu_usage.toFixed(1) + '%');
    html += infoItem('内存', fmtBytes(buildInfo.memory_usage));
    html += '</div></div>';
  }

  // 设备列表
  const peers = s.vnt_peers;
  if (peers && peers.length) {
    html += '<div class="card"><div class="card-header"><h2>在线设备 (' + peers.length + ')</h2></div>';
    html += '<div class="table-wrap"><table class="table"><thead><tr>';
    html += '<th>名称</th><th>虚拟IP</th><th>状态</th><th>穿透方式</th><th>公网IP</th><th>本地IP</th>';
    html += '</tr></thead><tbody>';
    for (const p of peers) {
      html += `<tr>
        <td>${esc(p.name)}</td>
        <td><strong>${esc(p.virtual_ip)}</strong></td>
        <td>${esc(p.status)}</td>
        <td>${esc(p.nat_traversal_type)}</td>
        <td style="font-size:12px;color:#667085">${esc(p.public_ips)}</td>
        <td style="font-size:12px;color:#667085">${esc(p.local_ip)}</td>
      </tr>`;
    }
    html += '</tbody></table></div></div>';
  }

  // 路由表
  const routes = s.vnt_routes;
  if (routes && routes.length) {
    html += '<div class="card"><div class="card-header"><h2>路由表</h2></div>';
    html += '<div class="table-wrap"><table class="table"><thead><tr>';
    html += '<th>目标</th><th>下一跳</th><th>度量</th><th>接口</th><th>RT</th>';
    html += '</tr></thead><tbody>';
    for (const r of routes) {
      html += `<tr>
        <td>${esc(r.destination)}</td>
        <td>${esc(r.next_hop)}</td>
        <td>${esc(r.metric)}</td>
        <td style="font-size:12px">${esc(r.interface)}</td>
        <td>${esc(r.rt)}</td>
      </tr>`;
    }
    html += '</tbody></table></div></div>';
  }

  if (!html) {
    html = `<div class="card empty-card">${emptyStateMarkup('等待运行时详情', '实例已经启动，但 vnt-cli 还没有返回完整的状态信息，请稍后再刷新查看。', 'V')}</div>`;
  }

  activateSubpanel('vpn', 'vpn-detail-view');
  document.getElementById('vpn-detail-empty').style.display = 'none';
  panel.querySelector('#vpn-detail-content').innerHTML = html;
  panel.style.display = 'block';
  panel.scrollIntoView({ behavior: 'smooth' });
}

function infoItem(label, value) {
  return `<div class="info-item"><span class="info-label">${esc(label)}</span><span class="info-value">${esc(value || '-')}</span></div>`;
}

function setText(id, value) {
  const el = document.getElementById(id);
  if (el) el.textContent = value;
}
