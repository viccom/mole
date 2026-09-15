package api

import (
	"moleAgent_Serv/internal/auth"
	"moleAgent_Serv/internal/core"
	"net/http"
)

// IsAdmin 检查用户是否是管理员
func IsAdmin(claims *core.Claims) bool {
	if claims == nil {
		return false
	}
	// AccessKey 是中间件明文声明的 RBAC 全量旁路身份；资源归属层必须认识它，
	// 否则该身份过了 RBAC 却被归属过滤挡成"什么都看不见、什么都改不了"
	if claims.UserID == "access_key" {
		return true
	}
	for _, role := range claims.Roles {
		if role == "admin" {
			return true
		}
	}
	return false
}

// CanAccessNode 检查用户是否可以访问指定节点
func CanAccessNode(claims *core.Claims, node *core.Node) bool {
	if IsAdmin(claims) {
		return true
	}
	if claims == nil {
		return false
	}
	return node.OwnerUserID == claims.UserID
}

// FilterNodes 按归属过滤节点列表，管理员可见全部
func FilterNodes(claims *core.Claims, nodes []*core.Node) []*core.Node {
	if IsAdmin(claims) {
		return nodes
	}
	if claims == nil {
		return []*core.Node{}
	}
	filtered := make([]*core.Node, 0, len(nodes))
	for _, n := range nodes {
		if n.OwnerUserID == claims.UserID {
			filtered = append(filtered, n)
		}
	}
	return filtered
}

// checkNodeOwnership 检查节点归属，非管理员访问他人节点返回 false 和 404 响应
// 无 claims 时（测试或内部调用）放行
func checkNodeOwnership(w http.ResponseWriter, r *http.Request, node *core.Node) bool {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		return true // 无认证信息时放行（测试场景）
	}
	if CanAccessNode(claims, node) {
		return true
	}
	ResponseError(w, http.StatusNotFound, 404, "Node not found")
	return false
}
