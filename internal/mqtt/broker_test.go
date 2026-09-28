package mqtt

import (
	"context"
	"testing"
	"time"

	mqtt "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/packets"
	"github.com/nalgeon/redka"
	_ "modernc.org/sqlite"

	"moleAgent_Serv/internal/auth"
	"moleAgent_Serv/internal/core"
)

func TestBrokerStartNonBlocking(t *testing.T) {
	broker := NewEmbeddedBroker("", "", nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- broker.Start(ctx)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Start should not return error: %v", err)
		}
		// Start returned immediately — non-blocking as expected
	case <-time.After(2 * time.Second):
		t.Fatal("Start blocked for more than 2 seconds — should be non-blocking")
	}
}

func TestBrokerStartWithListeners(t *testing.T) {
	broker := NewEmbeddedBroker("127.0.0.1:0", "127.0.0.1:0", nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- broker.Start(ctx)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Start returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start blocked — should be non-blocking")
	}

	// Verify broker can be stopped cleanly
	cancel()
	time.Sleep(100 * time.Millisecond)
}

func TestBrokerStopWithContext(t *testing.T) {
	broker := NewEmbeddedBroker("127.0.0.1:0", "", nil, nil)

	ctx, cancel := context.WithCancel(context.Background())

	if err := broker.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Give broker time to start serving
	time.Sleep(100 * time.Millisecond)

	// Cancel context should stop the broker
	cancel()
	time.Sleep(200 * time.Millisecond)

	// Verify the server is closed by trying to stop again
	if err := broker.Stop(context.Background()); err != nil {
		t.Fatalf("Stop should be idempotent after cancel, got error: %v", err)
	}
}

func TestBrokerGetClientsAndStats_FilterInlineClient(t *testing.T) {
	broker := NewEmbeddedBroker("", "", nil, nil)

	// 订阅会创建 inline client；该 client 不应暴露给管理接口。
	if err := broker.Subscribe("test/topic", 0, func(string, []byte) {}); err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	clients := broker.GetClients()
	if len(clients) != 0 {
		t.Fatalf("expected inline client to be filtered, got %+v", clients)
	}

	stats := broker.GetStats()
	if stats.ClientsTotal != 0 || stats.ClientsConnected != 0 {
		t.Fatalf("expected inline client excluded from stats, got %+v", stats)
	}

	// Sanity check: inline client really exists underneath.
	if _, ok := broker.GetServer().Clients.Get(mqtt.InlineClientId); !ok {
		t.Fatal("expected underlying inline client to exist")
	}
}

// ---------------------------------------------------------------------------
// P2P 信令哨兵分支（authHook / aclHook）
// ---------------------------------------------------------------------------

// fakeP2PVerifier tokenID→password 白名单桩
type fakeP2PVerifier struct {
	valid map[string]string
}

func (f *fakeP2PVerifier) VerifyP2PSignalToken(tokenID, password string) bool {
	return f.valid[tokenID] == password
}

func p2pAuthPacket(username, password string) packets.Packet {
	return packets.Packet{Connect: packets.ConnectParams{
		Username: []byte(username), Password: []byte(password),
	}}
}

// 有效 P2P token 通过鉴权，且 Username 置为哨兵字符串本身（aclHook 依据）
func TestAuthHook_P2PSignalValid(t *testing.T) {
	h := &authHook{p2pTokens: &fakeP2PVerifier{valid: map[string]string{"p2ptabc01": "sec"}}}
	cl := &mqtt.Client{ID: "p2pcli"}
	ok := h.OnConnectAuthenticate(cl, p2pAuthPacket("p2p-signal:p2ptabc01", "sec"))
	if !ok {
		t.Fatal("valid p2p token must authenticate")
	}
	if got := string(cl.Properties.Username); got != "p2p-signal:p2ptabc01" {
		t.Fatalf("Username = %q, want sentinel itself", got)
	}
}

func TestAuthHook_P2PSignalInvalid(t *testing.T) {
	h := &authHook{p2pTokens: &fakeP2PVerifier{valid: map[string]string{"p2ptabc01": "sec"}}}
	cl := &mqtt.Client{ID: "p2pcli"}
	if h.OnConnectAuthenticate(cl, p2pAuthPacket("p2p-signal:p2ptabc01", "wrong")) {
		t.Fatal("wrong secret must be rejected")
	}
	// 未知 tokenID
	if h.OnConnectAuthenticate(cl, p2pAuthPacket("p2p-signal:nosuch", "sec")) {
		t.Fatal("unknown tokenID must be rejected")
	}
	// 未配置 verifier（broker 未注入）一律拒绝
	h2 := &authHook{}
	if h2.OnConnectAuthenticate(&mqtt.Client{ID: "p2pcli"}, p2pAuthPacket("p2p-signal:p2ptabc01", "sec")) {
		t.Fatal("nil verifier must reject p2p sentinel")
	}
}

// aclHook：p2p 哨兵仅放行 nat-exchange/ 精确 topic——该检查必须先于 rbac==nil
// 兜底放行（本测试 rbac 为 nil 即可验证哨兵分支生效）
func TestACLHook_P2PSignalScoping(t *testing.T) {
	h := &aclHook{} // rbac == nil
	cl := &mqtt.Client{ID: "p2pcli", Properties: mqtt.ClientProperties{
		Username: []byte("p2p-signal:p2ptabc01"),
	}}

	if !h.OnACLCheck(cl, "nat-exchange/abcdef0123456789", false) {
		t.Fatal("exact nat-exchange topic (subscribe) must be allowed")
	}
	if !h.OnACLCheck(cl, "nat-exchange/abcdef0123456789", true) {
		t.Fatal("exact nat-exchange topic (publish) must be allowed")
	}
	if h.OnACLCheck(cl, "other/topic", false) {
		t.Fatal("non nat-exchange topic must be denied even with rbac nil")
	}
	if h.OnACLCheck(cl, "nat-exchange/#", false) {
		t.Fatal("wildcard # must be denied")
	}
	if h.OnACLCheck(cl, "nat-exchange/+/x", true) {
		t.Fatal("wildcard + must be denied")
	}

	// 匿名与普通用户路径不受影响
	empty := &mqtt.Client{ID: "anon"}
	if h.OnACLCheck(empty, "nat-exchange/x", false) {
		t.Fatal("empty username must be denied")
	}
}

// ---------------------------------------------------------------------------
// CheckTopicACL：Publish API 等服务端 inline 路径的 topic 归属判定（SEC-09）
// ---------------------------------------------------------------------------

// aclRoleRepo 最小角色仓库桩：admin → */*，mqttuser → mqtt 读写
type aclRoleRepo struct{}

func (aclRoleRepo) Create(*core.Role) error { return nil }
func (aclRoleRepo) GetByID(id string) (*core.Role, error) {
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
func (aclRoleRepo) GetAll() ([]*core.Role, error) { return nil, nil }
func (aclRoleRepo) Update(*core.Role) error       { return nil }
func (aclRoleRepo) Delete(string) error           { return nil }

// newACLTestRBAC 内存 RBAC：admin1 → admin，user1 → mqttuser，user2 无角色
func newACLTestRBAC(t *testing.T) *auth.RBACEngine {
	t.Helper()
	db, err := redka.Open(":memory:", &redka.Options{DriverName: "sqlite"})
	if err != nil {
		t.Fatalf("failed to open in-memory redka: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	rbac := auth.NewRBACEngine(db, aclRoleRepo{})
	for uid, rid := range map[string]string{"admin1": "admin", "user1": "mqttuser"} {
		if err := rbac.AssignRole(uid, rid); err != nil {
			t.Fatalf("assign role %s to %s: %v", rid, uid, err)
		}
	}
	return rbac
}

func TestCheckTopicACL(t *testing.T) {
	b := NewEmbeddedBroker("", "", nil, newACLTestRBAC(t))
	// 节点归属仲裁：nodeA 归 user1
	b.SetTopicAuthorizer(func(userID, nodeID string) bool {
		return nodeID == "nodeA" && userID == "user1"
	})

	tests := []struct {
		name   string
		userID string
		topic  string
		write  bool
		want   bool
	}{
		{"admin 发布任意节点 topic", "admin1", "/mole/nodeB/cmd", true, true},
		{"admin 通配符也放行（与 aclHook 同序）", "admin1", "/mole/#", false, true},
		{"节点归属者发布自己节点", "user1", "/mole/nodeA/cmd", true, true},
		{"普通用户发布他人节点", "user1", "/mole/nodeB/cmd", true, false},
		{"非节点 topic 走 mqtt 权限", "user1", "some/other/topic", true, true},
		{"普通用户无 mqtt 权限角色", "user2", "some/other/topic", true, false},
		{"普通用户通配符拒绝", "user1", "/mole/nodeA/#", false, false},
		{"空身份拒绝", "", "/mole/nodeA/cmd", true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := b.CheckTopicACL(tt.userID, tt.topic, tt.write); got != tt.want {
				t.Errorf("CheckTopicACL(%q, %q, write=%v) = %v, want %v", tt.userID, tt.topic, tt.write, got, tt.want)
			}
		})
	}
}
