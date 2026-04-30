// tunnels.js — HTTP/HTTPS/TCP/UDP 隧道管理
import { api } from './api.js';
import { fmtBytes, esc, toast } from './main.js';

const TUNNEL_TYPES = ['http', 'https', 'tcp', 'udp'];
let tunnels = [];

export function initTunnels() {
  const tbody = document.getElementById('tunnels-tbody');

  // 事件委托
  tbody.addEventListener('click', (e) => {
    const btn = e.target.closest('[data-action]');
    if (!btn) return;
    const action = btn.dataset.action;
    const name = btn.dataset.name;
    if (action === 'delete') {
      if (!confirm('确认删除隧道 ' + name + '?')) return;
      api.removeTunnel(name)
        .then(() => { toast('已删除', 'success'); })
        .catch(e => toast(e.message, 'error'));
    }
  });

  // 新增 Modal
  const modal = document.getElementById('tunnel-modal');
  document.getElementById('btn-add-tunnel').addEventListener('click', () => {
    document.getElementById('tf-name').value = '';
    document.getElementById('tf-type').value = 'http';
    document.getElementById('tf-target').value = '';
    modal.style.display = 'flex';
  });
  document.getElementById('btn-close-tunnel-modal').addEventListener('click', () => modal.style.display = 'none');
  document.getElementById('btn-cancel-tunnel').addEventListener('click', () => modal.style.display = 'none');
  modal.querySelector('.modal-backdrop').addEventListener('click', () => modal.style.display = 'none');

  document.getElementById('btn-submit-tunnel').addEventListener('click', () => {
    const name = document.getElementById('tf-name').value.trim();
    const type = document.getElementById('tf-type').value;
    const target = document.getElementById('tf-target').value.trim();
    if (!name) { toast('名称不能为空', 'error'); return; }
    if (!target) { toast('目标不能为空', 'error'); return; }
    api.addTunnel({ name, type, target, enabled: true })
      .then(() => { modal.style.display = 'none'; toast('隧道已添加', 'success'); })
      .catch(e => toast(e.message, 'error'));
  });

  document.getElementById('btn-refresh-tunnels').addEventListener('click', () => {
    api.listTunnels().then(list => {
      tunnels = list.filter(t => TUNNEL_TYPES.includes(t.type));
      render();
    }).catch(() => {});
  });

  // 注册全局刷新回调
  window.__tunnelsRefresh = (allTunnels) => {
    tunnels = allTunnels.filter(t => TUNNEL_TYPES.includes(t.type));
    render();
  };
}

function render() {
  const tbody = document.getElementById('tunnels-tbody');
  if (!tunnels.length) {
    tbody.innerHTML = '<tr><td colspan="7" style="text-align:center;color:#aaa;padding:20px">暂无隧道</td></tr>';
    return;
  }
  tbody.innerHTML = tunnels.map(t => {
    const typeClass = t.type || 'http';
    const statusHtml = t.connected
      ? '<span class="status on">运行中</span>'
      : '<span class="status off">离线</span>';
    return `<tr>
      <td><strong>${esc(t.name)}</strong></td>
      <td><span class="badge badge-${typeClass}">${esc(String(t.type || '')).toUpperCase()}</span></td>
      <td style="font-size:12px;color:#667085">${esc(t.target)}</td>
      <td>${statusHtml}</td>
      <td>${fmtBytes(t.bytes_in)}</td>
      <td>${fmtBytes(t.bytes_out)}</td>
      <td><button class="btn btn-danger btn-sm" data-action="delete" data-name="${esc(t.name)}">删除</button></td>
    </tr>`;
  }).join('');
}
