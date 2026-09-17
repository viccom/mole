package node

import (
	"encoding/json"
)

// NodeManagerAPI 节点管理 API（暴露给前端）
type NodeManagerAPI struct {
	mgr *Manager
	app interface{} // 避免循环引用
}

// NewNodeManagerAPI 创建节点管理器 API
func NewNodeManagerAPI(mgr *Manager) *NodeManagerAPI {
	return &NodeManagerAPI{
		mgr: mgr,
	}
}

// ListNodes 返回所有节点
func (nm *NodeManagerAPI) ListNodes() string {
	nodes := nm.mgr.List()
	return toJSON(nodes)
}

// GetCurrentNode 返回当前节点
func (nm *NodeManagerAPI) GetCurrentNode() string {
	n := nm.mgr.GetCurrentNode()
	if n == nil {
		return "{}"
	}
	return toJSON(n)
}

// AddNode 添加节点
func (nm *NodeManagerAPI) AddNode(nodeJSON string) string {
	var n Node
	if err := parseJSON(nodeJSON, &n); err != nil {
		return errorJSON("parse node: " + err.Error())
	}

	if n.Name == "" {
		return errorJSON("name is required")
	}
	if n.ServerAddr == "" {
		return errorJSON("server_addr is required")
	}
	if n.Token == "" {
		return errorJSON("token is required")
	}

	if err := nm.mgr.Add(n); err != nil {
		return errorJSON(err.Error())
	}

	if err := nm.mgr.Save(); err != nil {
		return errorJSON("save config: " + err.Error())
	}

	return successJSON("added")
}

// UpdateNode 更新节点
func (nm *NodeManagerAPI) UpdateNode(id string, nodeJSON string) string {
	var n Node
	if err := parseJSON(nodeJSON, &n); err != nil {
		return errorJSON("parse node: " + err.Error())
	}
	if n.Name == "" {
		return errorJSON("name is required")
	}
	if n.ServerAddr == "" {
		return errorJSON("server_addr is required")
	}
	if n.Token == "" {
		return errorJSON("token is required")
	}

	if err := nm.mgr.Update(id, n); err != nil {
		return errorJSON(err.Error())
	}

	if err := nm.mgr.Save(); err != nil {
		return errorJSON("save config: " + err.Error())
	}

	return successJSON("updated")
}

// RemoveNode 移除节点
func (nm *NodeManagerAPI) RemoveNode(id string) string {
	// 检查是否是当前节点
	current := nm.mgr.GetCurrentNode()
	if current != nil && current.ID == id {
		return errorJSON("cannot remove current node, switch to another node first")
	}

	if err := nm.mgr.Remove(id); err != nil {
		return errorJSON(err.Error())
	}

	if err := nm.mgr.Save(); err != nil {
		return errorJSON("save config: " + err.Error())
	}

	return successJSON("removed")
}

// SwitchNode 切换到指定节点
func (nm *NodeManagerAPI) SwitchNode(id string) string {
	n := nm.mgr.GetNode(id)
	if n == nil {
		return errorJSON("node not found")
	}

	// 更新当前节点
	nm.mgr.SetCurrentNode(id)
	if err := nm.mgr.Save(); err != nil {
		return errorJSON("save config: " + err.Error())
	}

	return successJSON("switched")
}

// GetBuiltinHTTPPort 获取内置 HTTP 端口
func (nm *NodeManagerAPI) GetBuiltinHTTPPort() string {
	return toJSON(nm.mgr.GetBuiltinHTTP())
}

// SetBuiltinHTTPPort 设置内置 HTTP 端口
func (nm *NodeManagerAPI) SetBuiltinHTTPPort(port string) string {
	nm.mgr.SetBuiltinHTTP(port)
	if err := nm.mgr.Save(); err != nil {
		return errorJSON("save config: " + err.Error())
	}
	return successJSON("updated")
}

// 辅助函数
func toJSON(v interface{}) string {
	data, _ := json.Marshal(v)
	return string(data)
}

func parseJSON(s string, v interface{}) error {
	return json.Unmarshal([]byte(s), v)
}

func successJSON(msg string) string {
	data, _ := json.Marshal(map[string]string{
		"status":  "ok",
		"message": msg,
	})
	return string(data)
}

func errorJSON(err string) string {
	data, _ := json.Marshal(map[string]string{
		"error": err,
	})
	return string(data)
}
