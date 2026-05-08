package main

import (
	"context"
	"encoding/json"
	"log"
	"sync"

	"moleAgent_client"
	"moleAgent_client/cmd/moleagent-desktop/internal/node"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// App 是主应用结构体
type App struct {
	ctx         context.Context
	nodeMgr     *node.Manager
	nodeMgrAPI  *node.NodeManagerAPI
	builtinPort string
	clientMu    sync.Mutex
	client      *moleAgent_client.Client
	serverMu    sync.Mutex
	apiServer   builtinHTTPServer

	// 单实例激活相关
	activateMu      sync.Mutex
	activateReady   bool
	pendingActivate bool
	activateWindow  func()
}

func NewApp() *App {
	return &App{}
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

	// 初始化节点管理器
	a.nodeMgr = node.NewManager()
	if err := a.nodeMgr.Load(); err != nil {
		log.Printf("load node config error: %v", err)
	}

	// 设置内置 HTTP 端口
	a.builtinPort = a.nodeMgr.GetBuiltinHTTP()
	if a.builtinPort == "" {
		a.builtinPort = moleAgent_client.DefaultConfig().BuiltinHTTP
	}

	// 创建节点管理 API
	a.nodeMgrAPI = node.NewNodeManagerAPI(a.nodeMgr)

	if err := a.startBuiltinHTTPServer(); err != nil {
		log.Printf("start built-in HTTP server error: %v", err)
	}

	// 初始化系统托盘
	initSystray(a)

	// 获取当前节点并启动客户端
	currentNode := a.nodeMgr.GetCurrentNode()
	if currentNode != nil {
		go func() {
			if err := a.startClientWithNode(currentNode); err != nil {
				log.Printf("start initial client error: %v", err)
			}
		}()
	}
}

func (a *App) domReady(ctx context.Context) {
	wailsRuntime.EventsEmit(ctx, "app:ready", true)
}

func (a *App) beforeClose(ctx context.Context) bool {
	if IsQuitting() {
		return false
	}
	HideToTray()
	return true
}

func (a *App) shutdown(ctx context.Context) {
	a.stopClient()
	if err := a.stopBuiltinHTTPServer(ctx); err != nil {
		log.Printf("stop built-in HTTP server error: %v", err)
	}
	if a.nodeMgr != nil {
		if err := a.nodeMgr.Save(); err != nil {
			log.Printf("save node config error: %v", err)
		}
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

// ===== 客户端控制 =====

func (a *App) startClientWithNode(n *node.Node) error {
	cfg := a.clientConfigForNode(n)
	newClient, err := moleAgent_client.New(cfg)
	if err != nil {
		return err
	}

	a.clientMu.Lock()
	oldClient := a.client
	a.client = newClient
	a.clientMu.Unlock()

	if oldClient != nil {
		oldClient.Close()
	}

	ctx := context.Background()
	go newClient.Run(ctx)
	log.Printf("client started for node: %s", n.Name)
	return nil
}

func (a *App) clientConfigForNode(n *node.Node) *moleAgent_client.Config {
	nodeName := n.NodeName
	if nodeName == "" {
		nodeName = n.Name
	}

	cfg := &moleAgent_client.Config{
		ServerAddr:        n.ServerAddr,
		Token:             n.Token,
		NodeName:          nodeName,
		Transport:         n.Transport,
		UseTLS:            n.TLS,
		BuiltinHTTP:       a.builtinPort,
		HeartbeatInterval: 10e9,
		HeartbeatTimeout:  5e9,
		ReconnectInterval: 5e9,
	}
	cfg.ApplyDefaults()
	return cfg
}

func (a *App) stopClient() {
	a.clientMu.Lock()
	defer a.clientMu.Unlock()

	if a.client != nil {
		a.client.Close()
		a.client = nil
	}
}

func (a *App) currentClient() *moleAgent_client.Client {
	a.clientMu.Lock()
	defer a.clientMu.Unlock()
	return a.client
}

func (a *App) switchNode(nodeID string) error {
	n := a.nodeMgr.GetNode(nodeID)
	if n == nil {
		return errString("node not found")
	}

	if err := validateDesktopNode(n, a.builtinPort); err != nil {
		return err
	}
	if err := a.startClientWithNode(n); err != nil {
		return err
	}

	a.nodeMgr.SetCurrentNode(nodeID)
	if err := a.nodeMgr.Save(); err != nil {
		return err
	}

	if a.ctx != nil {
		wailsRuntime.EventsEmit(a.ctx, "node:switched", n.Name)
	}
	return nil
}

// SwitchToNode 切换到指定节点
func (a *App) SwitchToNode(nodeID string) {
	if err := a.switchNode(nodeID); err != nil {
		log.Printf("switch node error: %v", err)
	}
}

// ===== Wails 暴露的方法 =====

func (a *App) GetStatus() map[string]interface{} {
	a.clientMu.Lock()
	defer a.clientMu.Unlock()

	if a.client == nil {
		return map[string]interface{}{
			"connected":   false,
			"node_id":     "",
			"server_addr": "",
			"tunnels":     []interface{}{},
		}
	}

	stats := a.client.Stats()
	return map[string]interface{}{
		"connected":    stats.Connected,
		"node_id":      stats.NodeID,
		"server_addr":  stats.ServerAddr,
		"tunnels":      a.client.AllTunnelStatus(),
	}
}

func (a *App) GetVersion() map[string]string {
	return desktopVersionMap()
}

func (a *App) GetAllTunnelStatus() string {
	a.clientMu.Lock()
	defer a.clientMu.Unlock()
	if a.client == nil {
		return "[]"
	}
	status := a.client.AllTunnelStatus()
	data, _ := json.Marshal(status)
	return string(data)
}

func (a *App) AddTunnel(tunnelJSON string) string {
	a.clientMu.Lock()
	defer a.clientMu.Unlock()
	if a.client == nil {
		return errorJSON("client not connected")
	}
	var tunnel moleAgent_client.Tunnel
	if err := json.Unmarshal([]byte(tunnelJSON), &tunnel); err != nil {
		return errorJSON("parse tunnel: " + err.Error())
	}
	if err := a.client.AddTunnel(tunnel); err != nil {
		return errorJSON(err.Error())
	}
	return successJSON("tunnel added")
}

func (a *App) RemoveTunnel(name string) string {
	a.clientMu.Lock()
	defer a.clientMu.Unlock()
	if a.client == nil {
		return errorJSON("client not connected")
	}
	if err := a.client.RemoveTunnel(name); err != nil {
		return errorJSON(err.Error())
	}
	return successJSON("tunnel removed")
}

func (a *App) GetVPNStatus(name string) string {
	a.clientMu.Lock()
	defer a.clientMu.Unlock()
	if a.client == nil {
		return errorJSON("client not connected")
	}
	status, err := a.client.VPNStatus(name)
	if err != nil {
		return errorJSON(err.Error())
	}
	data, _ := json.Marshal(status)
	return string(data)
}

func (a *App) VPNStart(name string) string {
	a.clientMu.Lock()
	defer a.clientMu.Unlock()
	if a.client == nil {
		return errorJSON("client not connected")
	}
	if err := a.client.VPNStart(name); err != nil {
		return errorJSON(err.Error())
	}
	return successJSON("vpn started")
}

func (a *App) VPNStop(name string) string {
	a.clientMu.Lock()
	defer a.clientMu.Unlock()
	if a.client == nil {
		return errorJSON("client not connected")
	}
	if err := a.client.VPNStop(name); err != nil {
		return errorJSON(err.Error())
	}
	return successJSON("vpn stopped")
}

func (a *App) GetSer2MQStatus(name string) string {
	a.clientMu.Lock()
	defer a.clientMu.Unlock()
	if a.client == nil {
		return errorJSON("client not connected")
	}
	status, err := a.client.Ser2MQStatus(name)
	if err != nil {
		return errorJSON(err.Error())
	}
	data, _ := json.Marshal(status)
	return string(data)
}

func (a *App) GetBuiltinHTTPPort() string {
	return a.builtinPort
}

func (a *App) GetBuiltinHTTPBaseURL() string {
	return "http://" + a.builtinPort
}

func (a *App) GetShellInfo() string {
	current := a.nodeMgr.GetCurrentNode()
	currentName := ""
	if current != nil {
		currentName = current.Name
	}

	online := false
	if client := a.currentClient(); client != nil {
		online = client.Stats().Connected
	}

	return successJSONMap(map[string]interface{}{
		"mode":           "desktop",
		"title":          "moleAgent Desktop",
		"ui_url":         a.GetBuiltinHTTPBaseURL() + "/ui",
		"current_name":   currentName,
		"current_online": online,
	})
}

func (a *App) SetBuiltinHTTPPort(port string) {
	if err := a.setBuiltinHTTPPort(port); err != nil {
		log.Printf("restart built-in HTTP server error: %v", err)
	}
}

// ===== 节点管理 API 委托 =====

func (a *App) ListNodes() string {
	return a.nodeMgrAPI.ListNodes()
}

func (a *App) GetCurrentNode() string {
	return a.nodeMgrAPI.GetCurrentNode()
}

func (a *App) AddNode(nodeJSON string) string {
	var n node.Node
	if err := json.Unmarshal([]byte(nodeJSON), &n); err != nil {
		return errorJSON("parse node: " + err.Error())
	}
	shouldActivate := a.nodeMgr.GetCurrentNode() == nil
	if n.Name == "" {
		return errorJSON("name is required")
	}
	if n.ServerAddr == "" {
		return errorJSON("server_addr is required")
	}
	if n.Token == "" {
		return errorJSON("token is required")
	}
	if err := validateDesktopNode(&n, a.builtinPort); err != nil {
		return errorJSON(err.Error())
	}
	if err := a.nodeMgr.Add(n); err != nil {
		return errorJSON(err.Error())
	}
	if err := a.nodeMgr.Save(); err != nil {
		return errorJSON("save config: " + err.Error())
	}
	if shouldActivate {
		nodes := a.nodeMgr.List()
		if len(nodes) == 0 {
			return errorJSON("node added but not found")
		}
		if err := a.switchNode(nodes[len(nodes)-1].ID); err != nil {
			return errorJSON(err.Error())
		}
	}
	return successJSON("added")
}

func (a *App) UpdateNode(id string, nodeJSON string) string {
	var n node.Node
	if err := json.Unmarshal([]byte(nodeJSON), &n); err != nil {
		return errorJSON("parse node: " + err.Error())
	}
	if err := validateDesktopNode(&n, a.builtinPort); err != nil {
		return errorJSON(err.Error())
	}
	if err := a.nodeMgr.Update(id, n); err != nil {
		return errorJSON(err.Error())
	}
	if err := a.nodeMgr.Save(); err != nil {
		return errorJSON("save config: " + err.Error())
	}
	current := a.nodeMgr.GetCurrentNode()
	if current != nil && current.ID == id {
		a.startClientWithNode(current)
	}
	return successJSON("updated")
}

func (a *App) RemoveNode(id string) string {
	current := a.nodeMgr.GetCurrentNode()
	if current != nil && current.ID == id {
		return errorJSON("cannot remove current node, switch to another node first")
	}
	if err := a.nodeMgr.Remove(id); err != nil {
		return errorJSON(err.Error())
	}
	if err := a.nodeMgr.Save(); err != nil {
		return errorJSON("save config: " + err.Error())
	}
	return successJSON("removed")
}

func (a *App) SwitchNode(id string) string {
	if err := a.switchNode(id); err != nil {
		return errorJSON(err.Error())
	}
	return successJSON("switched")
}

// ===== 辅助函数 =====

func successJSON(msg string) string {
	data, _ := json.Marshal(map[string]string{
		"status":  "ok",
		"message": msg,
	})
	return string(data)
}

func successJSONMap(payload map[string]interface{}) string {
	data, _ := json.Marshal(payload)
	return string(data)
}

func errorJSON(err string) string {
	data, _ := json.Marshal(map[string]string{
		"error": err,
	})
	return string(data)
}

type errString string

func (e errString) Error() string {
	return string(e)
}

func validateDesktopNode(n *node.Node, builtinPort string) error {
	nodeName := n.NodeName
	if nodeName == "" {
		nodeName = n.Name
	}
	cfg := &moleAgent_client.Config{
		ServerAddr:  n.ServerAddr,
		Token:       n.Token,
		NodeName:    nodeName,
		Transport:   n.Transport,
		UseTLS:      n.TLS,
		BuiltinHTTP: builtinPort,
	}
	cfg.ApplyDefaults()
	return cfg.Validate()
}
