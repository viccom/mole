package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nalgeon/redka"
	_ "modernc.org/sqlite"

	"moleAgent_Serv/internal/auth"
	"moleAgent_Serv/internal/core"
	"moleAgent_Serv/internal/mqtt"
)

// mqttACLRoleRepo 最小角色仓库桩：admin → */*，mqttuser → mqtt 读写
type mqttACLRoleRepo struct{}

func (mqttACLRoleRepo) Create(*core.Role) error { return nil }
func (mqttACLRoleRepo) GetByID(id string) (*core.Role, error) {
	switch id {
	case "admin":
		return &core.Role{ID: "admin", Name: "admin", Permissions: []core.Permission{{Resource: "*", Action: "*"}}}, nil
	case "mqttuser":
		return &core.Role{ID: "mqttuser", Name: "mqttuser", Permissions: []core.Permission{
			{Resource: "mqtt", Action: "read"}, {Resource: "mqtt", Action: "write"},
		}}, nil
	}
	return nil, core.ErrRoleNotFound
}
func (mqttACLRoleRepo) GetAll() ([]*core.Role, error) { return nil, nil }
func (mqttACLRoleRepo) Update(*core.Role) error       { return nil }
func (mqttACLRoleRepo) Delete(string) error           { return nil }

// newMQTTACLTestBroker 内存 RBAC + 节点归属仲裁的 broker（nodeA 归 user1）
func newMQTTACLTestBroker(t *testing.T) *mqtt.EmbeddedBroker {
	t.Helper()
	db, err := redka.Open(":memory:", &redka.Options{DriverName: "sqlite"})
	if err != nil {
		t.Fatalf("failed to open in-memory redka: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	rbac := auth.NewRBACEngine(db, mqttACLRoleRepo{})
	for uid, rid := range map[string]string{"admin1": "admin", "user1": "mqttuser"} {
		if err := rbac.AssignRole(uid, rid); err != nil {
			t.Fatalf("assign role %s to %s: %v", rid, uid, err)
		}
	}
	broker := mqtt.NewEmbeddedBroker("", "", nil, rbac)
	broker.SetTopicAuthorizer(func(userID, nodeID string) bool {
		return nodeID == "nodeA" && userID == "user1"
	})
	return broker
}

func doMQTTPublish(h *MQTTHandler, userID, topic string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mqtt/publish",
		strings.NewReader(`{"topic":"`+topic+`","payload":"hi"}`))
	if userID != "" {
		req = req.WithContext(auth.SetClaims(req.Context(), &core.Claims{UserID: userID, Username: userID}))
	}
	w := httptest.NewRecorder()
	h.Publish(w, req)
	return w
}

// SEC-09：Publish API 走 broker inline client 不经 aclHook——发布前必须按
// aclHook 同规则校验调用者对 topic 的归属
func TestMQTTPublishTopicACL(t *testing.T) {
	h := NewMQTTHandler(newMQTTACLTestBroker(t))

	tests := []struct {
		name     string
		userID   string
		topic    string
		wantCode int
	}{
		{"普通用户发布他人节点 topic → 403", "user1", "/mole/nodeB/cmd", http.StatusForbidden},
		{"普通用户发布自己节点 topic → 200", "user1", "/mole/nodeA/cmd", http.StatusOK},
		{"admin 发布任意节点 topic → 200", "admin1", "/mole/nodeB/cmd", http.StatusOK},
		{"无身份（中间件缺位防御）→ 401", "", "/mole/nodeA/cmd", http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := doMQTTPublish(h, tt.userID, tt.topic)
			if w.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d, body=%s", w.Code, tt.wantCode, w.Body.String())
			}
		})
	}
}

// 成功路径确实发布（非仅权限判定）：订阅 inline 回调可收到消息
func TestMQTTPublishDelivers(t *testing.T) {
	broker := newMQTTACLTestBroker(t)
	h := NewMQTTHandler(broker)

	got := make(chan [2]string, 1)
	if err := broker.Subscribe("/mole/nodeA/cmd", 0, func(topic string, payload []byte) {
		got <- [2]string{topic, string(payload)}
	}); err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}

	w := doMQTTPublish(h, "user1", "/mole/nodeA/cmd")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	select {
	case m := <-got:
		if m[0] != "/mole/nodeA/cmd" || m[1] != "hi" {
			t.Fatalf("received (%q, %q), want topic /mole/nodeA/cmd payload hi", m[0], m[1])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("publish did not deliver to inline subscriber")
	}
}
