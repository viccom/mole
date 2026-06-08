// webssh.js — WebSSH 远程终端隧道管理
import { api } from './api.js';
import { setText, fmtBytes, esc, toast, activateSubpanel, emptyStateMarkup, renderVizBars, renderVizRing } from './main.js';

let tunnels = [];
let editingName = null;

export function initWebSSH() {
  document.getElementById('webssh-tbody').addEventListener('click', handleAction);
  document.getElementById('btn-add-webssh').addEventListener('click', showAddForm);
  document.getElementById('btn-add-webssh-secondary').addEventListener('click', showAddForm);
  document.getElementById('btn-refresh-webssh').addEventListener('click', refreshList);
  document.getElementById('btn-cancel-webssh').addEventListener('click', hideForm);
  document.getElementById('btn-save-webssh').addEventListener('click', saveForm);

  document.getElementById('wf-auth-type').addEventListener('change', (e) => {
    const pwRow = document.getElementById('wf-password-row');
    const keyRow = document.getElementById('wf-privkey-row');
    pwRow.style.display = e.target.value === 'password' ? '' : 'none';
    keyRow.style.display = e.target.value === 'key' ? '' : 'none';
  });

  window.__websshRefresh = (allTunnels) => {
    tunnels = allTunnels.filter(t => t.type === 'webssh');
    render();
  };
}

function handleAction(e) {
  const btn = e.target.closest('[data-action]');
  if (!btn) return;
  const action = btn.dataset.action;
  const name = btn.dataset.name;

  if (action === 'delete') {
    if (!confirm('确认删除 WebSSH 隧道 ' + name + '?')) return;
    api.removeTunnel(name)
      .then(() => toast('已删除', 'success'))
      .catch(err => toast(err.message, 'error'));
  } else if (action === 'toggle') {
    const t = tunnels.find(t => t.name === name);
    if (!t) return;
    const para = Object.assign({}, t.para || {}, { enable: !t.enabled });
    api.addTunnel({ name: t.name, type: 'webssh', target: t.target, enabled: !t.enabled, para })
      .then(() => toast(t.enabled ? '已禁用' : '已启用', 'success'))
      .catch(err => toast(err.message, 'error'));
  } else if (action === 'edit') {
    const t = tunnels.find(t => t.name === name);
    if (t) showEditForm(t);
  }
}

function render() {
  const onlineCount = tunnels.filter(t => t.connected).length;
  const enabledCount = tunnels.filter(t => t.enabled).length;
  const totalSessions = tunnels.reduce((sum, t) => sum + ((t.status || {}).sessions || 0), 0);

  setText('webssh-total', String(tunnels.length));
  setText('webssh-online', String(onlineCount));
  setText('webssh-enabled', String(enabledCount));
  setText('webssh-sessions', String(totalSessions));
  setText('webssh-health-note', tunnels.length ? `${onlineCount}/${tunnels.length} 条在线，${totalSessions} 个会话` : '创建 WebSSH 隧道后可查看连接状态');

  renderVizBars('webssh-state-chart', [
    { label: '在线', value: onlineCount, color: '#10b981' },
    { label: '离线', value: tunnels.length - onlineCount, color: '#94a3b8' },
  ], '暂无隧道', '新增 WebSSH 隧道后这里会显示在线分布。');
  renderVizRing('webssh-health-ring', onlineCount, tunnels.length, '#10b981');

  const tbody = document.getElementById('webssh-tbody');
  if (!tunnels.length) {
    tbody.innerHTML = `<tr><td colspan="8" class="table-empty-cell">${emptyStateMarkup('暂无 WebSSH 隧道', '创建 WebSSH 隧道后，可通过浏览器访问远程 SSH 终端。', '>')}</td></tr>`;
    return;
  }
  tbody.innerHTML = tunnels.map(rowHtml).join('');
}

function rowHtml(t) {
  const s = t.status || {};
  const para = t.para || {};
  const running = !!t.connected;
  const statusHtml = running
    ? '<span class="status on">在线</span>'
    : '<span class="status off">离线</span>';
  const sessionInfo = running ? `${s.sessions || 0} 个` : '-';
  const authLabel = para.auth_type === 'key' ? '密钥' : '密码';
  const errorInfo = s.error ? `<span class="badge badge-err" title="${esc(s.error)}">错误</span>` : '';

  let actions = '';
  if (t.enabled) {
    actions += `<button class="btn btn-sm" data-action="toggle" data-name="${esc(t.name)}">禁用</button>`;
  } else {
    actions += `<button class="btn btn-primary btn-sm" data-action="toggle" data-name="${esc(t.name)}">启用</button>`;
  }
  actions += `<button class="btn btn-sm" data-action="edit" data-name="${esc(t.name)}">编辑</button>`;
  actions += `<button class="btn btn-danger btn-sm" data-action="delete" data-name="${esc(t.name)}">删除</button>`;

  return `<tr>
    <td><strong>${esc(t.name)}</strong></td>
    <td style="font-size:12px;color:#667085">${esc(s.host || para.host || '-')}</td>
    <td style="font-size:12px">${esc(s.user || para.user || '-')}</td>
    <td>${statusHtml}${errorInfo}</td>
    <td style="font-size:12px">${sessionInfo}</td>
    <td>${fmtBytes(s.bytes_in || t.bytes_in)}</td>
    <td>${fmtBytes(s.bytes_out || t.bytes_out)}</td>
    <td class="actions">${actions}</td>
  </tr>`;
}

function showAddForm() {
  activateSubpanel('webssh', 'webssh-form-view');
  editingName = null;
  $('webssh-form-title').textContent = '新增 WebSSH 隧道';
  $('wf-name').value = '';
  $('wf-name').disabled = false;
  $('wf-host').value = '';
  $('wf-port').value = '22';
  $('wf-user').value = 'root';
  $('wf-auth-type').value = 'password';
  $('wf-password').value = '';
  $('wf-privkey').value = '';
  $('wf-enable').checked = true;
  $('wf-password-row').style.display = '';
  $('wf-privkey-row').style.display = 'none';
  $('webssh-form').style.display = 'block';
}

function showEditForm(t) {
  activateSubpanel('webssh', 'webssh-form-view');
  editingName = t.name;
  $('webssh-form-title').textContent = '编辑 WebSSH 隧道';
  $('wf-name').value = t.name;
  $('wf-name').disabled = true;
  $('wf-enable').checked = t.enabled;

  const para = t.para || {};
  $('wf-host').value = para.host || '';
  $('wf-port').value = String(para.port || 22);
  $('wf-user').value = para.user || 'root';
  $('wf-auth-type').value = para.auth_type || 'password';
  $('wf-password').value = para.password || '';
  $('wf-privkey').value = para.priv_key || '';
  $('wf-password-row').style.display = para.auth_type !== 'key' ? '' : 'none';
  $('wf-privkey-row').style.display = para.auth_type === 'key' ? '' : 'none';
  $('webssh-form').style.display = 'block';
}

function hideForm() {
  $('webssh-form').style.display = 'none';
  activateSubpanel('webssh', 'webssh-list-view');
  editingName = null;
}

function saveForm() {
  const name = $('wf-name').value.trim();
  const host = $('wf-host').value.trim();
  const port = parseInt($('wf-port').value) || 22;
  const user = $('wf-user').value.trim();
  const authType = $('wf-auth-type').value;
  const password = $('wf-password').value;
  const privKey = $('wf-privkey').value.trim();
  const enabled = $('wf-enable').checked;

  if (!name) { toast('名称不能为空', 'error'); return; }
  if (!host) { toast('SSH 主机不能为空', 'error'); return; }
  if (!user) { toast('用户名不能为空', 'error'); return; }
  if (authType === 'password' && !password) { toast('密码不能为空', 'error'); return; }
  if (authType === 'key' && !privKey) { toast('私钥不能为空', 'error'); return; }

  const para = {
    enable: enabled,
    host,
    port,
    user,
    auth_type: authType,
  };
  if (authType === 'password') para.password = password;
  if (authType === 'key') para.priv_key = privKey;

  api.addTunnel({
    name,
    type: 'webssh',
    target: host + ':' + port,
    enabled,
    para,
  }).then(() => {
    hideForm();
    toast(editingName ? '隧道已更新' : '隧道已添加', 'success');
  }).catch(e => toast(e.message, 'error'));
}

function refreshList() {
  api.listTunnels().then(list => {
    tunnels = list.filter(t => t.type === 'webssh');
    render();
  }).catch(() => {});
}

function $(id) { return document.getElementById(id); }
