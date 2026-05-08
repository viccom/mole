(function () {
  const state = {
    shellInfo: { mode: "", title: "moleAgent", ui_url: "about:blank" },
    nodes: [],
    currentNode: null,
    currentURL: "",
    drawerOpen: false,
    editingNode: null
  };

  function wailsApp() {
    return window.go && window.go.main && window.go.main.App;
  }

  function toast(message, type = "info") {
    const container = document.getElementById("toast-container");
    const el = document.createElement("div");
    el.className = "toast " + type;
    el.textContent = message;
    container.appendChild(el);
    setTimeout(() => el.remove(), 2600);
  }

  function parseJSON(raw, fallback) {
    if (!raw) return fallback;
    if (typeof raw !== "string") return raw;
    try {
      return JSON.parse(raw);
    } catch {
      return fallback;
    }
  }

  async function call(name, ...args) {
    const app = wailsApp();
    if (!app || typeof app[name] !== "function") {
      throw new Error("Wails API not ready: " + name);
    }
    return app[name](...args);
  }

  function isDesktopMode() {
    return state.shellInfo.mode === "desktop";
  }

  function nodeId(node) {
    return node && (node.id || node.name) || "";
  }

  function nodeName(node) {
    return node && (node.name || node.node_name) || "未命名节点";
  }

  function nodeSubtitle(node) {
    if (!node) return "未配置";
    if (isDesktopMode()) {
      const nodeNamePart = node.node_name ? ("客户端名: " + node.node_name) : "客户端名: -";
      const serverPart = node.server_addr || "-";
      return nodeNamePart + " · 服务端: " + serverPart;
    }
    const port = node.port ? (":" + node.port) : "";
    return (node.addr || "-") + port;
  }

  function nodeOnline(node) {
    if (!node) return false;
    if (typeof node.is_online === "boolean") return node.is_online;
    if (typeof node.online === "boolean") return node.online;
    return false;
  }

  function currentNodeId() {
    return nodeId(state.currentNode);
  }

  function setHidden(id, hidden) {
    const el = document.getElementById(id);
    if (!el) return;
    el.classList.toggle("hidden", hidden);
  }

  function setDrawerOpen(open) {
    state.drawerOpen = open;
    const drawer = document.getElementById("drawer");
    const backdrop = document.getElementById("drawer-backdrop");
    drawer.classList.toggle("open", open);
    backdrop.classList.toggle("hidden", !open);
  }

  function fillDesktopForm(node) {
    document.getElementById("desktop-edit-id").value = node ? (node.id || "") : "";
    document.getElementById("desktop-name").value = node ? (node.name || "") : "";
    document.getElementById("desktop-node-name").value = node ? (node.node_name || "") : "";
    document.getElementById("desktop-server-addr").value = node ? (node.server_addr || "") : "";
    document.getElementById("desktop-token").value = node ? (node.token || "") : "";
    document.getElementById("desktop-transport").value = node ? (node.transport || "tcp") : "tcp";
    document.getElementById("desktop-tls").checked = !!(node && node.tls);
    document.getElementById("desktop-default").checked = !!(node && node.is_default);
  }

  function fillManagerForm(node) {
    document.getElementById("manager-edit-id").value = node ? (node.name || "") : "";
    document.getElementById("manager-name").value = node ? (node.name || "") : "";
    document.getElementById("manager-addr").value = node ? (node.addr || "") : "";
    document.getElementById("manager-port").value = node ? String(node.port || 59870) : "59870";
    document.getElementById("manager-default").checked = !!(node && node.is_default);
  }

  function showForm(node) {
    state.editingNode = node || null;
    const title = document.getElementById("form-title");
    title.textContent = node ? "编辑节点" : "新增节点";
    if (isDesktopMode()) {
      setHidden("desktop-form", false);
      setHidden("manager-form", true);
      fillDesktopForm(node);
    } else {
      setHidden("desktop-form", true);
      setHidden("manager-form", false);
      fillManagerForm(node);
    }
  }

  function resetForm() {
    state.editingNode = null;
    document.getElementById("form-title").textContent = "新增节点";
    if (isDesktopMode()) {
      fillDesktopForm(null);
      setHidden("desktop-form", false);
      setHidden("manager-form", true);
    } else {
      fillManagerForm(null);
      setHidden("desktop-form", true);
      setHidden("manager-form", false);
    }
  }

  function renderHeader() {
    document.getElementById("drawer-subtitle").textContent = isDesktopMode()
      ? "desktop 仅保留节点与窗口壳层"
      : "manager 仅保留节点切换壳层";

    const currentName = nodeName(state.currentNode);
    document.getElementById("current-node-name").textContent = currentName;

    const statusEl = document.getElementById("current-node-status");
    const online = !!state.shellInfo.current_online;
    statusEl.textContent = online ? "在线" : "离线";
    statusEl.className = "status-pill " + (online ? "online" : "offline");
  }

  function renderNodeList() {
    const list = document.getElementById("node-list");
    if (!state.nodes.length) {
      list.innerHTML = '<div class="node-card"><div class="node-name">暂无节点</div><div class="node-meta">先新增一个节点，再让共享前端跑起来。</div></div>';
      return;
    }

    list.innerHTML = state.nodes.map((node) => {
      const id = nodeId(node);
      const current = id === currentNodeId();
      const online = nodeOnline(node);
      const badges = [];
      if (current) badges.push('<span class="node-badge neutral">当前</span>');
      if (typeof node.is_default === "boolean" && node.is_default) badges.push('<span class="node-badge neutral">默认</span>');
      if (!isDesktopMode()) badges.push('<span class="node-badge ' + (online ? "online" : "offline") + '">' + (online ? "在线" : "离线") + '</span>');
      return '' +
        '<div class="node-card ' + (current ? "current" : "") + '">' +
          '<div class="node-card-head">' +
            '<div class="node-name">' + escapeHtml(nodeName(node)) + '</div>' +
            '<div class="node-badges">' + badges.join("") + '</div>' +
          '</div>' +
          '<div class="node-meta">' + escapeHtml(nodeSubtitle(node)) + '</div>' +
          '<div class="node-actions">' +
            (current ? "" : '<button class="btn btn-sm act-switch" data-id="' + escapeHtml(id) + '">切换</button>') +
            '<button class="btn btn-sm act-edit" data-id="' + escapeHtml(id) + '">编辑</button>' +
            '<button class="btn btn-sm act-delete" data-id="' + escapeHtml(id) + '"' + (current ? ' disabled' : "") + '>删除</button>' +
          '</div>' +
        '</div>';
    }).join("");

    list.querySelectorAll(".act-switch").forEach((btn) => {
      btn.addEventListener("click", async () => {
        const result = parseJSON(await call("SwitchNode", btn.dataset.id), {});
        if (result.error) {
          toast(result.error, "error");
          return;
        }
        toast("节点已切换", "success");
        await refreshAll(true);
        setDrawerOpen(false);
      });
    });

    list.querySelectorAll(".act-edit").forEach((btn) => {
      btn.addEventListener("click", () => {
        const node = state.nodes.find((item) => nodeId(item) === btn.dataset.id);
        showForm(node || null);
      });
    });

    list.querySelectorAll(".act-delete").forEach((btn) => {
      btn.addEventListener("click", async () => {
        const node = state.nodes.find((item) => nodeId(item) === btn.dataset.id);
        if (!node) return;
        const confirmed = window.confirm('确定删除节点 "' + nodeName(node) + '" 吗？');
        if (!confirmed) return;
        const result = parseJSON(await call("RemoveNode", btn.dataset.id), {});
        if (result.error) {
          toast(result.error, "error");
          return;
        }
        toast("节点已删除", "success");
        await refreshAll(false);
        resetForm();
      });
    });
  }

  async function refreshShellInfo() {
    state.shellInfo = parseJSON(await call("GetShellInfo"), state.shellInfo) || state.shellInfo;
  }

  async function refreshNodes() {
    state.nodes = parseJSON(await call("ListNodes"), []) || [];
    state.currentNode = parseJSON(await call("GetCurrentNode"), null);
    if (state.currentNode && Object.keys(state.currentNode).length === 0) {
      state.currentNode = null;
    }
  }

  function syncIframe(force) {
    const frame = document.getElementById("ui-frame");
    const loading = document.getElementById("loading-overlay");
    const offline = document.getElementById("offline-overlay");
    const offlineMessage = document.getElementById("offline-message");
    const nextURL = state.shellInfo.ui_url || "about:blank";

    if (nextURL === "about:blank") {
      state.currentURL = nextURL;
      loading.classList.add("hidden");
      offline.classList.remove("hidden");
      offlineMessage.textContent = isDesktopMode()
        ? "本地内置 UI 尚未可用，请检查内置 HTTP 服务。"
        : "当前节点离线或 /ui 不可访问。";
      frame.removeAttribute("src");
      return;
    }

    offline.classList.add("hidden");
    if (!force && state.currentURL === nextURL) {
      return;
    }

    state.currentURL = nextURL;
    loading.classList.remove("hidden");
    frame.onload = function () {
      loading.classList.add("hidden");
      offline.classList.add("hidden");
    };
    frame.onerror = function () {
      loading.classList.add("hidden");
      offline.classList.remove("hidden");
      offlineMessage.textContent = "共享前端加载失败，请稍后重试。";
    };
    frame.src = nextURL;
  }

  async function refreshAll(forceFrameReload) {
    await Promise.all([refreshShellInfo(), refreshNodes()]);
    renderHeader();
    renderNodeList();
    syncIframe(!!forceFrameReload);
  }

  function escapeHtml(text) {
    const div = document.createElement("div");
    div.textContent = text == null ? "" : String(text);
    return div.innerHTML;
  }

  async function saveDesktopNode(event) {
    event.preventDefault();
    const editID = document.getElementById("desktop-edit-id").value;
    const payload = {
      name: document.getElementById("desktop-name").value.trim(),
      node_name: document.getElementById("desktop-node-name").value.trim(),
      server_addr: document.getElementById("desktop-server-addr").value.trim(),
      token: document.getElementById("desktop-token").value.trim(),
      transport: document.getElementById("desktop-transport").value,
      tls: document.getElementById("desktop-tls").checked,
      is_default: document.getElementById("desktop-default").checked
    };

    const raw = editID
      ? await call("UpdateNode", editID, JSON.stringify(payload))
      : await call("AddNode", JSON.stringify(payload));
    const result = parseJSON(raw, {});
    if (result.error) {
      toast(result.error, "error");
      return;
    }
    toast(editID ? "节点已更新" : "节点已添加", "success");
    await refreshAll(false);
    resetForm();
  }

  async function saveManagerNode(event) {
    event.preventDefault();
    const editID = document.getElementById("manager-edit-id").value;
    const payload = {
      name: document.getElementById("manager-name").value.trim(),
      addr: document.getElementById("manager-addr").value.trim(),
      port: parseInt(document.getElementById("manager-port").value || "59870", 10),
      is_default: document.getElementById("manager-default").checked
    };

    const raw = editID
      ? await call("UpdateNode", editID, JSON.stringify(payload))
      : await call("AddNode", JSON.stringify(payload));
    const result = parseJSON(raw, {});
    if (result.error) {
      toast(result.error, "error");
      return;
    }
    toast(editID ? "节点已更新" : "节点已添加", "success");
    await refreshAll(false);
    resetForm();
  }

  function bindEvents() {
    document.getElementById("btn-open-drawer").addEventListener("click", () => setDrawerOpen(true));
    document.getElementById("btn-close-drawer").addEventListener("click", () => setDrawerOpen(false));
    document.getElementById("drawer-backdrop").addEventListener("click", () => setDrawerOpen(false));
    document.getElementById("btn-new-node").addEventListener("click", resetForm);
    document.getElementById("btn-refresh").addEventListener("click", () => refreshAll(true));
    document.getElementById("btn-retry").addEventListener("click", () => refreshAll(true));
    document.getElementById("desktop-form").addEventListener("submit", saveDesktopNode);
    document.getElementById("manager-form").addEventListener("submit", saveManagerNode);
  }

  async function waitForWails() {
    if (wailsApp()) return;
    await new Promise((resolve) => {
      const timer = setInterval(() => {
        if (wailsApp()) {
          clearInterval(timer);
          resolve();
        }
      }, 50);
    });
  }

  async function init() {
    bindEvents();
    await waitForWails();
    await refreshAll(true);
    resetForm();
    setInterval(() => {
      refreshAll(false).catch((error) => {
        console.error(error);
      });
    }, 4000);
  }

  document.addEventListener("DOMContentLoaded", () => {
    init().catch((error) => {
      console.error(error);
      toast(error.message || String(error), "error");
    });
  });
})();
