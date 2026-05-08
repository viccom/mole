// main.js - moleAgent Manager 主入口

let currentNodeURL = 'about:blank';
let nodeDrawerOpen = false;

// ===== API 封装 - 使用全局函数 =====

const api = {
  async getCurrentNodeURL() {
    return await window.go.main.App.GetCurrentNodeURL();
  },
  
  async listNodes() {
    const result = await window.go.main.App.ListNodes();
    try {
      return JSON.parse(result);
    } catch {
      return [];
    }
  },
  
  async getCurrentNode() {
    const result = await window.go.main.App.GetCurrentNode();
    try {
      return JSON.parse(result);
    } catch {
      return null;
    }
  },
  
  async addNode(node) {
    const result = await window.go.main.App.AddNode(JSON.stringify(node));
    try {
      return JSON.parse(result);
    } catch {
      return { error: result };
    }
  },
  
  async updateNode(name, node) {
    const result = await window.go.main.App.UpdateNode(name, JSON.stringify(node));
    try {
      return JSON.parse(result);
    } catch {
      return { error: result };
    }
  },
  
  async removeNode(name) {
    const result = await window.go.main.App.RemoveNode(name);
    try {
      return JSON.parse(result);
    } catch {
      return { error: result };
    }
  },
  
  async switchNode(name) {
    const result = await window.go.main.App.SwitchNode(name);
    try {
      return JSON.parse(result);
    } catch {
      return { error: result };
    }
  },
  
  async checkNodeStatus(name) {
    const result = await window.go.main.App.CheckNodeStatus(name);
    try {
      return JSON.parse(result);
    } catch {
      return { online: false };
    }
  },
  
  async getAllNodeStatus() {
    const result = await window.go.main.App.GetAllNodeStatus();
    try {
      return JSON.parse(result);
    } catch {
      return [];
    }
  }
};

// ===== Toast 提示 =====
function toast(msg, type = 'info') {
  const container = document.getElementById('toast-container');
  const el = document.createElement('div');
  el.className = `toast ${type}`;
  el.textContent = msg;
  container.appendChild(el);
  setTimeout(() => el.remove(), 3000);
}

// ===== WebView 控制 =====
function loadWebView(url) {
  const webview = document.getElementById('webview');
  const loading = document.getElementById('loading-overlay');
  const offline = document.getElementById('offline-overlay');
  
  if (url === 'about:blank') {
    loading.style.display = 'none';
    offline.style.display = 'flex';
    return;
  }
  
  loading.style.display = 'flex';
  offline.style.display = 'none';
  
  webview.src = url;
  
  webview.onload = () => {
    loading.style.display = 'none';
    offline.style.display = 'none';
    console.log('WebView loaded:', url);
  };
  
  webview.onerror = () => {
    loading.style.display = 'none';
    offline.style.display = 'flex';
    console.error('WebView error');
  };
}

function showOffline() {
  document.getElementById('loading-overlay').style.display = 'none';
  document.getElementById('offline-overlay').style.display = 'flex';
}

function showLoading() {
  document.getElementById('loading-overlay').style.display = 'flex';
  document.getElementById('offline-overlay').style.display = 'none';
}

// ===== 节点管理 =====

async function refreshNodeList() {
  const nodes = await api.getAllNodeStatus();
  const listEl = document.getElementById('node-list');
  const currentNode = await api.getCurrentNode();
  const currentName = currentNode ? currentNode.name : '';
  
  if (nodes.length === 0) {
    listEl.innerHTML = `
      <div class="empty-state">
        <div class="empty-state-icon">📡</div>
        <div class="empty-state-title">暂无节点</div>
        <div class="empty-state-desc">添加第一个节点开始使用</div>
      </div>
    `;
    return;
  }
  
  listEl.innerHTML = nodes.map(node => `
    <div class="node-item ${node.name === currentName ? 'active' : ''} ${node.name === currentName ? 'is-current' : ''}" data-name="${escapeHtml(node.name)}">
      <div class="node-item-info">
        <div class="node-item-name">
          <span class="node-item-status ${node.online ? 'online' : 'offline'}">●</span>
          ${escapeHtml(node.name)}
        </div>
        <div class="node-item-addr">${escapeHtml(node.addr)}:${node.port}</div>
      </div>
      <div class="node-item-actions">
        ${node.name !== currentName ? `<button class="btn btn-sm btn-connect" data-name="${escapeHtml(node.name)}" ${!node.online ? 'disabled' : ''}>连接</button>` : ''}
        <button class="btn btn-sm btn-edit" data-name="${escapeHtml(node.name)}">编辑</button>
        <button class="btn btn-sm btn-danger btn-delete" data-name="${escapeHtml(node.name)}" ${node.name === currentName ? 'disabled' : ''}>删除</button>
      </div>
    </div>
  `).join('');
  
  // 绑定事件
  listEl.querySelectorAll('.btn-connect').forEach(btn => {
    btn.addEventListener('click', async () => {
      const name = btn.dataset.name;
      const result = await api.switchNode(name);
      if (result.error) {
        toast(result.error, 'error');
      } else {
        toast('已切换到: ' + name, 'success');
        refreshNodeList();
        refreshCurrentNode();
        
        // 获取新的 URL 并加载 WebView
        const newUrl = await api.getCurrentNodeURL();
        if (newUrl && newUrl !== 'about:blank') {
          loadWebView(newUrl);
        } else {
          showOffline();
        }
        
        closeDrawer();
      }
    });
  });
  
  listEl.querySelectorAll('.btn-edit').forEach(btn => {
    btn.addEventListener('click', () => {
      const name = btn.dataset.name;
      editNode(name, nodes.find(n => n.name === name));
    });
  });
  
  listEl.querySelectorAll('.btn-delete').forEach(btn => {
    btn.addEventListener('click', async () => {
      const name = btn.dataset.name;
      if (!confirm(`确定删除节点 "${name}" 吗？`)) return;
      
      const result = await api.removeNode(name);
      if (result.error) {
        toast(result.error, 'error');
      } else {
        toast('节点已删除', 'success');
        refreshNodeList();
      }
    });
  });
}

async function refreshCurrentNode() {
  const node = await api.getCurrentNode();
  const nameEl = document.getElementById('current-node-name');
  const statusEl = document.getElementById('current-node-status');
  
  if (node && node.name) {
    nameEl.textContent = node.name;
    statusEl.className = `current-node-status ${node.is_online ? 'online' : 'offline'}`;
    currentNodeURL = node.is_online ? node.url : 'about:blank';
  } else {
    nameEl.textContent = '未连接';
    statusEl.className = 'current-node-status offline';
    currentNodeURL = 'about:blank';
  }
}

function openDrawer() {
  nodeDrawerOpen = true;
  document.getElementById('node-drawer').classList.add('open');
  document.getElementById('drawer-backdrop').classList.add('visible');
  resetForm();
  refreshNodeList();
}

function closeDrawer() {
  nodeDrawerOpen = false;
  document.getElementById('node-drawer').classList.remove('open');
  document.getElementById('drawer-backdrop').classList.remove('visible');
}

function resetForm() {
  document.getElementById('edit-name').value = '';
  document.getElementById('form-title').textContent = '新增节点';
  document.getElementById('nf-name').value = '';
  document.getElementById('nf-addr').value = '';
  document.getElementById('nf-port').value = '18080';
  document.getElementById('nf-default').checked = false;
}

function editNode(name, node) {
  if (!node) return;
  
  document.getElementById('edit-name').value = name;
  document.getElementById('form-title').textContent = '编辑节点';
  document.getElementById('nf-name').value = node.name;
  document.getElementById('nf-addr').value = node.addr;
  document.getElementById('nf-port').value = node.port;
  document.getElementById('nf-default').checked = node.is_default || false;
}

async function saveNode(e) {
  e.preventDefault();
  
  const editName = document.getElementById('edit-name').value;
  const name = document.getElementById('nf-name').value.trim();
  const addr = document.getElementById('nf-addr').value.trim();
  const port = parseInt(document.getElementById('nf-port').value) || 18080;
  const isDefault = document.getElementById('nf-default').checked;
  
  if (!name || !addr) {
    toast('请填写完整信息', 'error');
    return;
  }
  
  const node = { name, addr, port, is_default: isDefault };
  
  let result;
  if (editName) {
    // 更新
    result = await api.updateNode(editName, node);
  } else {
    // 新增
    result = await api.addNode(node);
  }
  
  if (result.error) {
    toast(result.error, 'error');
  } else {
    toast(editName ? '节点已更新' : '节点已添加', 'success');
    resetForm();
    refreshNodeList();
  }
}

// ===== 工具函数 =====
function escapeHtml(text) {
  const div = document.createElement('div');
  div.textContent = text;
  return div.innerHTML;
}

// ===== 事件绑定 =====
function bindEvents() {
  // 节点面板切换
  document.getElementById('btn-node-panel').addEventListener('click', () => {
    if (nodeDrawerOpen) {
      closeDrawer();
    } else {
      openDrawer();
    }
  });
  
  // 关闭按钮
  document.getElementById('btn-close-drawer').addEventListener('click', closeDrawer);
  
  // 背景遮罩点击
  document.getElementById('drawer-backdrop').addEventListener('click', closeDrawer);
  
  // 刷新按钮
  document.getElementById('btn-refresh').addEventListener('click', async () => {
    showLoading();
    const url = await api.getCurrentNodeURL();
    if (url !== 'about:blank') {
      loadWebView(url);
    } else {
      showOffline();
    }
    await refreshCurrentNode();
    await refreshNodeList();
  });
  
  // 重试按钮
  document.getElementById('btn-retry').addEventListener('click', async () => {
    showLoading();
    const url = await api.getCurrentNodeURL();
    if (url !== 'about:blank') {
      loadWebView(url);
    } else {
      showOffline();
    }
  });
  
  // 表单提交
  document.getElementById('node-form').addEventListener('submit', saveNode);
  
  // 取消按钮
  document.getElementById('btn-cancel-form').addEventListener('click', () => {
    resetForm();
    closeDrawer();
  });
  
  // 快捷键 Ctrl+N
  document.addEventListener('keydown', (e) => {
    if (e.ctrlKey && e.key === 'n') {
      e.preventDefault();
      if (nodeDrawerOpen) {
        closeDrawer();
      } else {
        openDrawer();
      }
    }
  });
  
  // WebView 错误处理
  const webview = document.getElementById('webview');
  webview.addEventListener('did-fail-load', () => {
    showOffline();
  });
}

// ===== 初始化 =====
async function init() {
  console.log('Initializing moleAgent Manager...');
  
  // 绑定事件
  bindEvents();
  
  // 等待 Wails 就绪
  await new Promise(resolve => {
    if (window.runtime && window.go) {
      resolve();
    } else {
      window.addEventListener('load', resolve);
    }
  });
  
  // 刷新当前节点
  await refreshCurrentNode();
  
  // 加载 WebView
  const url = await api.getCurrentNodeURL();
  loadWebView(url);
  
  console.log('moleAgent Manager initialized');
}

// 页面加载完成后初始化
document.addEventListener('DOMContentLoaded', init);
