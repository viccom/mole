package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"moleAgent_client/cmd/moleagent-manager/internal/node"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// App 是主应用结构体
type App struct {
	ctx         context.Context
	nodeMgr    *node.Manager
	builtinURL string
	checking   bool
	checkMu    sync.Mutex
	checkCancel context.CancelFunc

	// 单实例激活相关
	activateMu      sync.Mutex
	activateReady   bool
	pendingActivate bool
	activateWindow  func()
}

func NewApp() *App {
	return &App{
		builtinURL: "about:blank",
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	// 初始化单实例激活
	a.activateMu.Lock()
	a.activateWindow = func() {
		showWindow()
		wailsRuntime.EventsEmit(ctx, "app:activate-window")
	}
	a.activateReady = true
	pending := a.pendingActivate
	a.pendingActivate = false
	activate := a.activateWindow
	a.activateMu.Unlock()

	if pending && activate != nil {
		activate()
	}

	// 初始化系统托盘
	initSystray(a)

	// 初始化节点管理器
	a.nodeMgr = node.NewManager()
	if err := a.nodeMgr.Load(); err != nil {
		log.Printf("load node config error: %v", err)
	}

	a.checkMu.Lock()
	a.checking = true
	checkCtx, cancel := context.WithCancel(context.Background())
	a.checkCancel = cancel
	a.checkMu.Unlock()
	a.refreshBuiltinURL()

	// 启动节点状态检测
	go a.startNodeChecker(checkCtx)
}

func (a *App) domReady(ctx context.Context) {
	wailsRuntime.EventsEmit(ctx, "app:ready", true)
}

func (a *App) beforeClose(ctx context.Context) bool {
	// 如果是通过托盘菜单退出，则允许关闭
	if IsQuitting() {
		return false
	}
	// 否则隐藏到托盘
	HideToTray()
	return true // 阻止关闭
}

func (a *App) shutdown(ctx context.Context) {
	a.checkMu.Lock()
	if a.checkCancel != nil {
		a.checkCancel()
		a.checkCancel = nil
	}
	a.checking = false
	a.checkMu.Unlock()
	if a.nodeMgr != nil {
		a.nodeMgr.Save()
	}
}

// RequestActivate 处理单实例激活
func (a *App) RequestActivate() {
	a.activateMu.Lock()
	if !a.activateReady || a.activateWindow == nil {
		a.pendingActivate = true
		a.activateMu.Unlock()
		return
	}
	activate := a.activateWindow
	a.activateMu.Unlock()

	if activate != nil {
		activate()
	}
}

// ===== 节点状态检测 =====

func (a *App) startNodeChecker(ctx context.Context) {
	interval := time.Duration(a.nodeMgr.GetCheckInterval()) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.checkMu.Lock()
			enabled := a.checking
			a.checkMu.Unlock()
			if !enabled {
				continue
			}
			a.checkAllNodes()
		}
	}
}

func (a *App) checkAllNodes() {
	nodes := a.nodeMgr.List()
	onlineCount := 0

	for i := range nodes {
		n := &nodes[i]
		isOnline := a.checkNodeOnline(n)
		n.IsOnline = isOnline
		if isOnline {
			onlineCount++
		}
	}

	// 通知前端更新
	a.refreshBuiltinURL()
	if a.ctx != nil {
		wailsRuntime.EventsEmit(a.ctx, "nodes:updated", map[string]interface{}{
			"nodes":        nodes,
			"online_count": onlineCount,
		})
	}
}

func (a *App) checkNodeOnline(n *node.Node) bool {
	url := n.GetHealthURL()

	client := &http.Client{
		Timeout: 2 * time.Second,
	}

	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}

// ===== Wails 暴露的方法 =====

// GetCurrentNodeURL 获取当前节点的 Web UI URL
func (a *App) GetCurrentNodeURL() string {
	return a.refreshBuiltinURL()
}

func (a *App) GetShellInfo() string {
	current := a.nodeMgr.GetCurrentNode()
	currentName := ""
	currentOnline := false
	if current != nil {
		currentName = current.Name
		currentOnline = a.checkNodeOnline(current)
	}
	return toJSON(map[string]interface{}{
		"mode":           "manager",
		"title":          "moleAgent Manager",
		"ui_url":         a.refreshBuiltinURL(),
		"current_name":   currentName,
		"current_online": currentOnline,
	})
}

// ListNodes 返回所有节点
func (a *App) ListNodes() string {
	nodes := a.nodeMgr.List()

	// 更新在线状态
	return toJSON(a.decorateNodes(nodes))
}

// GetCurrentNode 返回当前节点
func (a *App) GetCurrentNode() string {
	n := a.nodeMgr.GetCurrentNode()
	if n == nil {
		return "{}"
	}
	return toJSON(a.decorateNode(n))
}

// AddNode 添加节点
func (a *App) AddNode(nodeJSON string) string {
	var n node.Node
	if err := json.Unmarshal([]byte(nodeJSON), &n); err != nil {
		return errorJSON("parse node: " + err.Error())
	}

	if n.Name == "" {
		return errorJSON("name is required")
	}
	if n.Addr == "" {
		return errorJSON("addr is required")
	}
	if n.Port == 0 {
		n.Port = 59870
	}

	if err := a.nodeMgr.Add(n); err != nil {
		return errorJSON(err.Error())
	}

	if err := a.nodeMgr.Save(); err != nil {
		return errorJSON("save config: " + err.Error())
	}

	return successJSON("node added")
}

// UpdateNode 更新节点
func (a *App) UpdateNode(name string, nodeJSON string) string {
	var n node.Node
	if err := json.Unmarshal([]byte(nodeJSON), &n); err != nil {
		return errorJSON("parse node: " + err.Error())
	}

	if err := a.nodeMgr.Update(name, n); err != nil {
		return errorJSON(err.Error())
	}

	if err := a.nodeMgr.Save(); err != nil {
		return errorJSON("save config: " + err.Error())
	}

	return successJSON("node updated")
}

// RemoveNode 移除节点
func (a *App) RemoveNode(name string) string {
	current := a.nodeMgr.GetCurrentNode()
	if current != nil && current.Name == name {
		return errorJSON("cannot remove current node, switch to another node first")
	}

	if err := a.nodeMgr.Remove(name); err != nil {
		return errorJSON(err.Error())
	}

	if err := a.nodeMgr.Save(); err != nil {
		return errorJSON("save config: " + err.Error())
	}

	return successJSON("node removed")
}

// SwitchNode 切换到指定节点
func (a *App) SwitchNode(name string) string {
	n := a.nodeMgr.GetNode(name)
	if n == nil {
		return errorJSON("node not found")
	}

	// 检查节点是否在线
	if !a.checkNodeOnline(n) {
		return errorJSON("node is offline")
	}

	// 更新当前节点
	a.nodeMgr.SetCurrentNode(name)
	if err := a.nodeMgr.Save(); err != nil {
		return errorJSON("save config: " + err.Error())
	}

	// 更新 URL
	a.builtinURL = a.refreshBuiltinURL()

	// 通知前端
	if a.ctx != nil {
		wailsRuntime.EventsEmit(a.ctx, "node:switched", map[string]interface{}{
			"name": name,
			"url":  a.builtinURL,
		})
	}

	return successJSON("switched")
}

// CheckNodeStatus 检查单个节点状态
func (a *App) CheckNodeStatus(name string) string {
	n := a.nodeMgr.GetNode(name)
	if n == nil {
		return errorJSON("node not found")
	}

	isOnline := a.checkNodeOnline(n)
	return toJSON(map[string]interface{}{
		"name":   n.Name,
		"online": isOnline,
		"url":    n.GetURL(),
	})
}

// GetAllNodeStatus 获取所有节点状态
func (a *App) GetAllNodeStatus() string {
	nodes := a.nodeMgr.List()
	result := make([]map[string]interface{}, 0, len(nodes))
	for _, decorated := range a.decorateNodes(nodes) {
		decorated["online"] = decorated["is_online"]
		result = append(result, decorated)
	}
	return toJSON(result)
}

// ===== 辅助函数 =====

func successJSON(msg string) string {
	return toJSON(map[string]string{
		"status":  "ok",
		"message": msg,
	})
}

func errorJSON(err string) string {
	return toJSON(map[string]string{
		"error": err,
	})
}

func toJSON(v interface{}) string {
	data, _ := json.Marshal(v)
	return string(data)
}

func (a *App) refreshBuiltinURL() string {
	current := a.nodeMgr.GetCurrentNode()
	if current == nil {
		a.builtinURL = "about:blank"
		return a.builtinURL
	}
	if !a.checkNodeOnline(current) {
		a.builtinURL = "about:blank"
		return a.builtinURL
	}
	a.builtinURL = current.GetURL()
	return a.builtinURL
}

func (a *App) decorateNodes(nodes []node.Node) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(nodes))
	for i := range nodes {
		n := &nodes[i]
		result = append(result, a.decorateNode(n))
	}
	return result
}

func (a *App) decorateNode(n *node.Node) map[string]interface{} {
	isOnline := a.checkNodeOnline(n)
	return map[string]interface{}{
		"name":       n.Name,
		"addr":       n.Addr,
		"port":       n.Port,
		"is_default": n.IsDefault,
		"is_online":  isOnline,
		"url":        n.GetURL(),
	}
}
