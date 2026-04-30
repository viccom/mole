function esc(s) {
  const value = s == null ? '' : String(s);
  return value
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#39;');
}

function fmtBytes(b) {
  if (!b || b === 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB'];
  let value = b;
  let index = 0;
  while (value >= 1024 && index < units.length - 1) {
    value /= 1024;
    index++;
  }
  return value.toFixed(value >= 100 ? 0 : value >= 10 ? 1 : 2) + ' ' + units[index];
}

function renderTunnelRowHTML(tunnel) {
  const typeClass = tunnel.type || 'http';
  const statusHtml = tunnel.connected
    ? '<span class="status on">运行中</span>'
    : '<span class="status off">离线</span>';
  let actions = `<button class="btn btn-danger btn-sm" data-action="delete" data-name="${esc(tunnel.name)}">删除</button>`;
  if (tunnel.type === 'ser2mq' || tunnel.type === 'vpn-manager') {
    actions = `<button class="btn btn-sm btn-primary" data-action="detail" data-name="${esc(tunnel.name)}">详情</button> ${actions}`;
  }

  return `<td><strong>${esc(tunnel.name)}</strong></td>
    <td><span class="badge badge-${typeClass}">${esc(String(tunnel.type || '')).toUpperCase()}</span></td>
    <td style="font-size:12px;color:#666">${esc(tunnel.target)}</td>
    <td>${statusHtml}</td>
    <td>${fmtBytes(tunnel.bytes_in)}</td>
    <td>${fmtBytes(tunnel.bytes_out)}</td>
    <td class="actions-cell">${actions}</td>`;
}

function tunnelRenderKey(tunnel, selected) {
  return JSON.stringify({
    selected,
    type: tunnel.type || '',
    target: tunnel.target || '',
    connected: !!tunnel.connected,
    bytesIn: tunnel.bytes_in || 0,
    bytesOut: tunnel.bytes_out || 0,
  });
}

function syncRow(row, tunnel, selected) {
  const nextKey = tunnelRenderKey(tunnel, selected);
  row.dataset.tunnelName = tunnel.name;
  row.className = selected ? 'selected' : '';
  if (row.dataset.renderKey !== nextKey) {
    row.innerHTML = renderTunnelRowHTML(tunnel);
    row.dataset.renderKey = nextKey;
  }
}

export function syncTunnelTable(tbody, tunnels, selectedName) {
  const doc = tbody.ownerDocument || document;
  const existingRows = new Map();
  for (const child of Array.from(tbody.children || [])) {
    if (child.dataset && child.dataset.tunnelName) {
      existingRows.set(child.dataset.tunnelName, child);
    }
  }

  if (!tunnels.length) {
    for (const child of Array.from(tbody.children || [])) {
      child.remove();
    }
    const row = doc.createElement('tr');
    row.dataset.empty = 'true';
    row.innerHTML = '<td colspan="7" style="text-align:center;color:#aaa;padding:24px">暂无隧道</td>';
    tbody.appendChild(row);
    return;
  }

  const orderedRows = [];
  for (const tunnel of tunnels) {
    const row = existingRows.get(tunnel.name) || doc.createElement('tr');
    syncRow(row, tunnel, tunnel.name === selectedName);
    orderedRows.push(row);
  }

  for (const child of Array.from(tbody.children || [])) {
    if (!orderedRows.includes(child)) {
      child.remove();
    }
  }

  for (let i = 0; i < orderedRows.length; i++) {
    const row = orderedRows[i];
    const current = tbody.children[i];
    if (current !== row) {
      tbody.insertBefore(row, current || null);
    }
  }
}
