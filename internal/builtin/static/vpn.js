import { api } from './api.js';
import { setText, fmtBytes, esc, toast, activateSubpanel, emptyStateMarkup, renderVizBars, renderVizRing } from './main.js';

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
  $('btn-close-vpn-detail').addEventListener('click', () => {
    $('vpn-detail').style.display = 'none';
    $('vpn-detail-empty').style.display = 'block';
    currentDetailName = null;
    activateSubpanel('vpn', 'vpn-list-view');
  });

  $('vpn-detail-tabs').addEventListener('click', (e) => {
    const tab = e.target.closest('[data-vpn-tab]');
    if (!tab) return;
    currentVpnTab = tab.dataset.vpnTab;
    document.querySelectorAll('.vpn-tab-btn').forEach(b => b.classList.toggle('active', b === tab));
    loadVpnTab();
  });

  window.__vpnRefresh = (allTunnels) => {
    tunnels = allTunnels.filter(t => t.type === 'vpn-manager');
    render();
    if (currentDetailName && $('vpn-detail').style.display !== 'none') {
      loadVpnTab();
    }
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

function $(id) {
  return document.getElementById(id);
}

function readInput(id) {
  return $(id).value.trim();
}

function pushArg(args, id, flag) {
  const val = readInput(id);
  if (val) args.push(flag, val);
  return val;
}

function showAddForm() {
  activateSubpanel('vpn', 'vpn-form-view');
  editingName = null;
  $('vpn-form-title').textContent = '新增 VPN 隧道';
  $('vf-name').value = '';
  $('vf-name').disabled = false;
  $('vf-binary').value = 'vnt-cli';
  $('vf-token').value = '';
  $('vf-server').value = '';
  $('vf-device-id').value = '';
  $('vf-device-name').value = '';
  $('vf-password').value = '';
  $('vf-in-ip').value = '';
  $('vf-out-ip').value = '';
  $('vf-virtual-ip').value = '';
  $('vf-rest-port').value = '59871';
  $('vf-autostart').checked = false;
  $('vf-enable').checked = true;
  $('vpn-form').style.display = 'block';
}

function showEditForm(t) {
  activateSubpanel('vpn', 'vpn-form-view');
  editingName = t.name;
  $('vpn-form-title').textContent = '编辑 VPN 隧道';
  $('vf-name').value = t.name;
  $('vf-name').disabled = true;
  $('vf-enable').checked = t.enabled;

  const para = t.para || {};
  const vnt = para.vnt || {};

  $('vf-binary').value = (para.binary && para.binary.name) || 'vnt-cli';
  $('vf-token').value = vnt.token || '';
  $('vf-server').value = vnt.server || '';
  $('vf-device-id').value = vnt.device_id || '';
  $('vf-device-name').value = vnt.name || '';
  $('vf-password').value = vnt.password || '';
  $('vf-in-ip').value = vnt.in_ip || '';
  $('vf-out-ip').value = vnt.out_ip || '';
  $('vf-virtual-ip').value = vnt.ip || '';
  $('vf-rest-port').value = String(vnt.rest_port || 59871);
  $('vf-autostart').checked = !!(para.lifecycle && para.lifecycle.autostart);

  $('vpn-form').style.display = 'block';
}

function hideForm() {
  $('vpn-form').style.display = 'none';
  editingName = null;
  activateSubpanel('vpn', 'vpn-list-view');
}

let editingName = null;
let currentDetailName = null;
let currentVpnTab = 'info';

function saveForm() {
  const name = readInput('vf-name');
  const binary = readInput('vf-binary');
  const token = readInput('vf-token');

  if (!name) { toast('名称不能为空', 'error'); return; }
  if (!binary) { toast('程序名不能为空', 'error'); return; }
  if (!token) { toast('Token 不能为空', 'error'); return; }

  const args = ['-k', token];
  const server = pushArg(args, 'vf-server', '-s');
  const deviceID = pushArg(args, 'vf-device-id', '-d');
  const deviceName = pushArg(args, 'vf-device-name', '-n');
  const password = pushArg(args, 'vf-password', '-w');
  const inIP = pushArg(args, 'vf-in-ip', '-i');
  const outIP = pushArg(args, 'vf-out-ip', '-o');
  const virtualIP = pushArg(args, 'vf-virtual-ip', '--ip');

  const restPort = parseInt($('vf-rest-port').value) || 59871;

  const payload = {
    name,
    type: 'vpn-manager',
    target: server,
    enabled: $('vf-enable').checked,
    para: {
      binary: { name: binary },
      args,
      lifecycle: {
        autostart: $('vf-autostart').checked,
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
        in_ip: inIP,
        out_ip: outIP,
        ip: virtualIP,
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
  currentDetailName = name;
  currentVpnTab = 'info';

  $('vpn-detail-title').textContent = name + ' — VPN 详情';
  $('vpn-detail-empty').style.display = 'none';
  $('vpn-detail').style.display = 'block';

  document.querySelectorAll('.vpn-tab-btn').forEach((b, i) => b.classList.toggle('active', i === 0));
  activateSubpanel('vpn', 'vpn-detail-view');
  loadVpnTab();
  $('vpn-detail').scrollIntoView({ behavior: 'smooth' });
}

function loadVpnTab() {
  const t = tunnels.find(t => t.name === currentDetailName);
  if (!t) return;
  loaders[currentVpnTab](t);
}

const loaders = {
  info(t) {
    const s = t.status || {};
    const info = s.vnt_info;
    const status = s.vnt_status;
    let html = '';
    if (status) {
      const mem = status.memory_usage ? (status.memory_usage / 1048576).toFixed(1) + ' MB' : '-';
      html += '<div style="margin-bottom:12px"><div class="info-grid">'
        + field('版本', status.git_tag + ' (' + status.git_hash + ')')
        + field('编译时间', status.build_time)
        + field('程序路径', status.exe_path)
        + field('CPU 占用', status.cpu_usage.toFixed(1) + '%')
        + field('内存占用', mem)
        + field('序列号', status.serial)
        + '</div></div>';
    }
    if (info) {
      html += '<div class="info-grid">'
        + field('设备名称', info.name)
        + field('虚拟 IP', info.virtual_ip)
        + field('虚拟网关', info.virtual_gateway)
        + field('子网掩码', info.virtual_netmask)
        + field('连接状态', info.connect_status)
        + field('NAT 类型', info.nat_type)
        + field('中继服务器', info.relay_server)
        + field('公网 IP', info.public_ips)
        + field('本地地址', info.local_addr)
        + field('IPv6 地址', info.ipv6_addr)
        + field('UDP 监听', (info.udp_listen_addr || []).join(', ') || '无')
        + field('TCP 监听', info.tcp_listen_addr || '无')
        + '</div>';
    }
    $('vpn-detail-content').innerHTML = html || emptyHtml();
  },

  list(t) {
    const peers = (t.status || {}).vnt_peers;
    if (!peers || !peers.length) {
      $('vpn-detail-content').innerHTML = '<div class="empty" style="text-align:center;color:#94a3b8;padding:40px 0;font-size:14px">暂无其他设备</div>';
      return;
    }
    let html = '<div class="table-wrap"><table class="table"><thead><tr><th>#</th><th>设备名称</th><th>虚拟IP</th><th>状态</th><th>连接方式</th><th>延时</th></tr></thead><tbody>';
    peers.forEach((p, i) => {
      html += '<tr><td>' + (i + 1) + '</td><td>' + esc(p.name) + '</td><td>' + esc(p.virtual_ip)
        + '</td><td>' + statusTag(p.status) + '</td><td>' + connTag(p.nat_traversal_type)
        + '</td><td>' + (p.rt ? esc(p.rt) : '-') + '</td></tr>';
    });
    $('vpn-detail-content').innerHTML = html + '</tbody></table></div>';
  },

  route(t) {
    const routes = (t.status || {}).vnt_routes;
    if (!routes || !routes.length) {
      $('vpn-detail-content').innerHTML = '<div class="empty" style="text-align:center;color:#94a3b8;padding:40px 0;font-size:14px">暂无路由信息</div>';
      return;
    }
    let html = '<div class="table-wrap"><table class="table"><thead><tr><th>目标地址</th><th>下一跳</th><th>跃点</th><th>延时</th><th>接口</th></tr></thead><tbody>';
    routes.forEach(r => {
      html += '<tr><td>' + esc(r.destination) + '</td><td>' + esc(r.next_hop) + '</td><td>' + esc(r.metric)
        + '</td><td>' + (r.rt ? esc(r.rt) : '-') + '</td><td>' + esc(r.interface) + '</td></tr>';
    });
    $('vpn-detail-content').innerHTML = html + '</tbody></table></div>';
  },

  chart(t) {
    const name = t.name;
    api.getVPNChart(name).then(d => {
      if (currentDetailName !== name) return;
      if (d.disable_stats) {
        $('vpn-detail-content').innerHTML = '<div class="empty" style="text-align:center;color:#94a3b8;padding:40px 0;font-size:14px">流量统计未启用，请去掉 --disable-stats 参数后重启</div>';
        return;
      }
      const upTotal = d.up_total || 0, downTotal = d.down_total || 0;
      const upMap = d.up_map || {}, downMap = d.down_map || {};
      const ips = {};
      Object.keys(upMap).forEach(k => { ips[k] = true; });
      Object.keys(downMap).forEach(k => { ips[k] = true; });
      const sorted = Object.keys(ips).sort((a, b) => {
        const pa = a.split('.').map(Number), pb = b.split('.').map(Number);
        for (let i = 0; i < 4; i++) { if (pa[i] !== pb[i]) return pa[i] - pb[i]; }
        return 0;
      });
      let maxUp = 0, maxDown = 0;
      sorted.forEach(ip => {
        if ((upMap[ip] || 0) > maxUp) maxUp = upMap[ip];
        if ((downMap[ip] || 0) > maxDown) maxDown = downMap[ip];
      });
      let html = '<div class="vpn-chart-summary">'
        + '<div class="vpn-stat-card"><div class="vpn-stat-label">总上传</div><div class="vpn-stat-value up">' + fmtBytes(upTotal) + '</div></div>'
        + '<div class="vpn-stat-card"><div class="vpn-stat-label">总下载</div><div class="vpn-stat-value down">' + fmtBytes(downTotal) + '</div></div>'
        + '</div>';
      if (!sorted.length) {
        html += '<div class="empty" style="text-align:center;color:#94a3b8;padding:40px 0;font-size:14px">暂无流量数据</div>';
      } else {
        html += '<div class="table-wrap"><table class="table"><thead><tr><th>IP 地址</th><th>上传</th><th>下载</th></tr></thead><tbody>';
        sorted.forEach(ip => {
          const uv = upMap[ip] || 0, dv = downMap[ip] || 0;
          const upPct = maxUp ? (uv / maxUp * 100).toFixed(1) : '0.0';
          const downPct = maxDown ? (dv / maxDown * 100).toFixed(1) : '0.0';
          html += '<tr><td>' + esc(ip) + '</td>'
            + '<td><div class="vpn-bar-cell"><div class="vpn-bar-bg"><div class="vpn-bar-fill up" style="width:' + upPct + '%"></div></div><span>' + fmtBytes(uv) + '</span></div></td>'
            + '<td><div class="vpn-bar-cell"><div class="vpn-bar-bg"><div class="vpn-bar-fill down" style="width:' + downPct + '%"></div></div><span>' + fmtBytes(dv) + '</span></div></td></tr>';
        });
        html += '</tbody></table></div>';
      }
      $('vpn-detail-content').innerHTML = html;
    }).catch(() => {
      $('vpn-detail-content').innerHTML = '<div class="empty" style="text-align:center;color:#dc2626;padding:40px 0;font-size:14px">获取流量统计失败</div>';
    });
  }
};

function field(label, value) {
  return '<div class="info-item"><span class="info-label">' + esc(label) + '</span><span class="info-value">' + esc(value || '-') + '</span></div>';
}

function emptyHtml() {
  return '<div class="empty" style="text-align:center;color:#94a3b8;padding:40px 0;font-size:14px">等待运行时详情</div>';
}

function statusTag(s) {
  const v = esc(s);
  if (s === 'Connected') return '<span class="vpn-tag vpn-tag-green">' + v + '</span>';
  if (s === 'Disconnected' || s === 'Stopped') return '<span class="vpn-tag vpn-tag-red">' + v + '</span>';
  return '<span class="vpn-tag vpn-tag-gray">' + v + '</span>';
}

function connTag(t) {
  const v = esc(t);
  if (t === 'p2p' || t === 'tcp-p2p') return '<span class="vpn-tag vpn-tag-green">' + v + '</span>';
  if (t && t.indexOf('relay') >= 0) return '<span class="vpn-tag vpn-tag-amber">' + v + '</span>';
  return '<span class="vpn-tag vpn-tag-gray">' + v + '</span>';
}
