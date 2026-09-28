package tunnel

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xtaci/smux"

	"moleAgent_Serv/internal/core"
	"moleAgent_Serv/internal/node"
)

// ---------------------------------------------------------------------------
// connState
// ---------------------------------------------------------------------------

func TestConnState_GetSetNil(t *testing.T) {
	s := &connState{}
	if got := s.get(); got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestConnState_GetSetNode(t *testing.T) {
	s := &connState{}
	n := &core.Node{ID: "Abcd1234", Status: core.NodeStatusOnline}

	s.set(n)
	got := s.get()
	if got == nil {
		t.Fatal("expected non-nil node")
	}
	if got.ID != "Abcd1234" {
		t.Errorf("expected ID Abcd1234, got %s", got.ID)
	}
}

func TestConnState_SetReplacesValue(t *testing.T) {
	s := &connState{}
	n1 := &core.Node{ID: "Node0001"}
	n2 := &core.Node{ID: "Node0002"}

	s.set(n1)
	if got := s.get(); got.ID != "Node0001" {
		t.Errorf("expected Node0001, got %s", got.ID)
	}

	s.set(n2)
	if got := s.get(); got.ID != "Node0002" {
		t.Errorf("expected Node0002 after replace, got %s", got.ID)
	}
}

func TestConnState_SetToNil(t *testing.T) {
	s := &connState{}
	s.set(&core.Node{ID: "Abcd1234"})
	s.set(nil)
	if got := s.get(); got != nil {
		t.Errorf("expected nil after set(nil), got %v", got)
	}
}

// TestConnState_ConcurrentGetSet hammers get/set from many goroutines to
// surface data races. Run with -race to detect problems.
func TestConnState_ConcurrentGetSet(t *testing.T) {
	const writers = 50
	const readers = 50
	const iterations = 500

	s := &connState{}
	var wg sync.WaitGroup

	// writers: continuously swap the node pointer
	wg.Add(writers)
	for i := 0; i < writers; i++ {
		go func(seq int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				node := &core.Node{
					ID:     "Abcd1234",
					Status: core.NodeStatusOnline,
				}
				s.set(node)
			}
		}(i)
	}

	// readers: continuously read and verify the result is valid
	wg.Add(readers)
	for i := 0; i < readers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				n := s.get()
				// n may be nil or non-nil, but must never be a corrupted pointer.
				// The important thing is no panic / data race.
				if n != nil && n.ID != "Abcd1234" {
					t.Errorf("unexpected node ID: %s", n.ID)
				}
			}
		}()
	}

	wg.Wait()
}

// TestConnState_ConcurrentMixed interleaves set(some), set(nil), and get to
// stress the transition between nil and non-nil states.
func TestConnState_ConcurrentMixed(t *testing.T) {
	const goroutines = 100
	const iterations = 300

	s := &connState{}
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(setNil bool) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				if setNil {
					s.set(nil)
				} else {
					s.set(&core.Node{ID: "XyZw9876", Status: core.NodeStatusOnline})
				}
				// Every writer also reads back to verify no panic.
				_ = s.get()
			}
		}(i%2 == 0)
	}

	wg.Wait()
}

// ---------------------------------------------------------------------------
// isValidNodeID
// ---------------------------------------------------------------------------

func TestIsValidNodeID_ValidCases(t *testing.T) {
	tests := []struct {
		id string
	}{
		{"Abcd1234"},
		{"Z0000000"},
		{"aBcDeFgH"},
		{"nodeID01"},
		{"A1234567"},
		{"zzzzzzzz"},
		{"QwErTyUi"},
	}
	for _, tt := range tests {
		if !isValidNodeID(tt.id) {
			t.Errorf("expected %q to be valid", tt.id)
		}
	}
}

func TestIsValidNodeID_InvalidCases(t *testing.T) {
	tests := []struct {
		id     string
		reason string
	}{
		{"", "empty string"},
		{"abcd123", "7 characters (too short)"},
		{"abcd12345", "9 characters (too long)"},
		{"1abcdefg", "starts with digit"},
		{"abcdefghi", "9 characters"},
		{"ab123", "5 characters"},
		{"Abcd123\n", "contains newline"},
		{"Abc 1234", "contains space"},
		{"Abcd-123", "contains dash"},
		{"Abcd_123", "contains underscore"},
		{"Abcd.123", "contains dot"},
		{"Abcd@123", "contains @"},
		{"Abcd!234", "contains !"},
		{"你好你好你好你好", "non-ASCII unicode"},
		{"Abcd123\n", "trailing newline"},
	}
	for _, tt := range tests {
		if isValidNodeID(tt.id) {
			t.Errorf("expected %q to be invalid (%s)", tt.id, tt.reason)
		}
	}
}

func TestIsValidNodeID_LengthBoundary(t *testing.T) {
	// Exactly 8 valid characters starting with a letter => valid
	if !isValidNodeID("A0000000") {
		t.Error("8-char alphanumeric starting with letter should be valid")
	}
	// 7 chars => invalid
	if isValidNodeID("A000000") {
		t.Error("7-char string should be invalid")
	}
	// 9 chars => invalid
	if isValidNodeID("A00000000") {
		t.Error("9-char string should be invalid")
	}
}

func TestIsValidNodeID_FirstCharMustBeLetter(t *testing.T) {
	// Digits not allowed as first char
	if isValidNodeID("0aaaaaaa") {
		t.Error("digit as first char should be invalid")
	}
	if isValidNodeID("9ZZZZZZZ") {
		t.Error("digit as first char should be invalid")
	}
	// Letters allowed as first char
	if !isValidNodeID("z0000000") {
		t.Error("lowercase letter as first char should be valid")
	}
	if !isValidNodeID("Z0000000") {
		t.Error("uppercase letter as first char should be valid")
	}
}

// 非 ASCII 多字节字符不得因「字节长度恰好为 8」而混过校验。
// 注意：必须逐字节判定 ASCII，用 unicode.IsLetter 会放行这些值。
func TestIsValidNodeID_RejectsNonASCIIBytes(t *testing.T) {
	tests := []struct {
		id     string
		reason string
	}{
		{"ébcdefg", "é(2B) + 6 ASCII = 8 bytes, first char non-ASCII"},
		{"abééfg", "2 + é(2B) + é(2B) + 2 = 8 bytes, non-ASCII in middle"},
		{"ａbcdef", "fullwidth ａ(3B) + 5 ASCII = 8 bytes"},
	}
	for _, tt := range tests {
		if len(tt.id) != 8 {
			t.Fatalf("test case %q is %d bytes, need exactly 8 to exercise the hole", tt.id, len(tt.id))
		}
		if isValidNodeID(tt.id) {
			t.Errorf("expected %q to be invalid (%s)", tt.id, tt.reason)
		}
	}
}

func TestIsValidNodeID_SubsequentChars(t *testing.T) {
	// All digits after first letter
	if !isValidNodeID("A1234567") {
		t.Error("all digits after first letter should be valid")
	}
	// Mix of letters and digits
	if !isValidNodeID("aB3d5E7g") {
		t.Error("mixed letters/digits should be valid")
	}
	// Special character in middle position
	if isValidNodeID("Abc_1234") {
		t.Error("underscore in position 3 should be invalid")
	}
	// Special character in last position
	if isValidNodeID("Abcd123!") {
		t.Error("exclamation in last position should be invalid")
	}
}

// ---------------------------------------------------------------------------
// writeControlResp
// ---------------------------------------------------------------------------

// bufWriter is a minimal test double implementing io.Writer for capturing output.
type bufWriter struct {
	buf bytes.Buffer
}

func (bw *bufWriter) Write(p []byte) (int, error) {
	return bw.buf.Write(p)
}

func TestWriteControlResp_OK(t *testing.T) {
	bw := &bufWriter{}
	writeControlResp(bw, "ok", "registered")

	line := bw.buf.String()
	line = strings.TrimRight(line, "\n")

	var resp ControlResponse
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatalf("failed to parse response JSON: %v\nraw: %q", err, line)
	}
	if resp.Cmd != "ok" {
		t.Errorf("expected cmd=ok, got %q", resp.Cmd)
	}
	if resp.Msg != "registered" {
		t.Errorf("expected msg=registered, got %q", resp.Msg)
	}
}

func TestWriteControlResp_EmptyMsg(t *testing.T) {
	bw := &bufWriter{}
	writeControlResp(bw, "pong", "")

	line := strings.TrimRight(bw.buf.String(), "\n")

	var resp ControlResponse
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}
	if resp.Cmd != "pong" {
		t.Errorf("expected cmd=pong, got %q", resp.Cmd)
	}
	if resp.Msg != "" {
		t.Errorf("expected empty msg, got %q", resp.Msg)
	}
}

func TestWriteControlResp_Error(t *testing.T) {
	bw := &bufWriter{}
	writeControlResp(bw, "err", "invalid token")

	line := strings.TrimRight(bw.buf.String(), "\n")

	var resp ControlResponse
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}
	if resp.Cmd != "err" {
		t.Errorf("expected cmd=err, got %q", resp.Cmd)
	}
	if resp.Msg != "invalid token" {
		t.Errorf("expected msg='invalid token', got %q", resp.Msg)
	}
}

func TestWriteControlResp_JSONLinesFormat(t *testing.T) {
	bw := &bufWriter{}
	writeControlResp(bw, "ok", "test")

	raw := bw.buf.String()
	if !strings.HasSuffix(raw, "\n") {
		t.Error("response must be newline-terminated (JSON Lines format)")
	}
}

func TestWriteControlResp_MultipleCalls(t *testing.T) {
	bw := &bufWriter{}
	writeControlResp(bw, "ok", "first")
	writeControlResp(bw, "pong", "")
	writeControlResp(bw, "err", "bad")

	raw := bw.buf.String()
	lines := strings.Split(strings.TrimRight(raw, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %q", len(lines), lines)
	}

	expected := []ControlResponse{
		{Cmd: "ok", Msg: "first"},
		{Cmd: "pong", Msg: ""},
		{Cmd: "err", Msg: "bad"},
	}
	for i, exp := range expected {
		var resp ControlResponse
		if err := json.Unmarshal([]byte(lines[i]), &resp); err != nil {
			t.Fatalf("line %d: parse error: %v", i, err)
		}
		if resp.Cmd != exp.Cmd || resp.Msg != exp.Msg {
			t.Errorf("line %d: expected %+v, got %+v", i, exp, resp)
		}
	}
}

func TestWriteControlResp_UnicodeMessage(t *testing.T) {
	bw := &bufWriter{}
	writeControlResp(bw, "err", "节点ID格式不正确")

	line := strings.TrimRight(bw.buf.String(), "\n")

	var resp ControlResponse
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatalf("failed to parse unicode response: %v", err)
	}
	if resp.Msg != "节点ID格式不正确" {
		t.Errorf("expected unicode msg, got %q", resp.Msg)
	}
}

// ---------------------------------------------------------------------------
// ControlCmd / ControlResponse JSON round-trip
// ---------------------------------------------------------------------------

func TestControlCmd_JSON(t *testing.T) {
	cmd := ControlCmd{
		Cmd:    "register",
		NodeID: "Abcd1234",
		Name:   "test-node",
		Token:  "secret",
		Tunnels: []core.Tunnel{
			{Type: core.TunnelTypeHTTP, Domain: "app.example.com", Target: "http://127.0.0.1:8080"},
		},
	}

	data, err := json.Marshal(cmd)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got ControlCmd
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Cmd != cmd.Cmd || got.NodeID != cmd.NodeID || got.Name != cmd.Name {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	if len(got.Tunnels) != 1 {
		t.Fatalf("expected 1 tunnel, got %d", len(got.Tunnels))
	}
	if got.Tunnels[0].Domain != "app.example.com" {
		t.Errorf("tunnel domain mismatch: %s", got.Tunnels[0].Domain)
	}
}

func TestControlResponse_JSON(t *testing.T) {
	resp := ControlResponse{Cmd: "ok", Msg: "registered"}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got ControlResponse
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Cmd != resp.Cmd || got.Msg != resp.Msg || got.Ts != resp.Ts {
		t.Errorf("round-trip mismatch: expected %+v, got %+v", resp, got)
	}
}

func TestControlResponse_OmitsEmptyMsg(t *testing.T) {
	resp := ControlResponse{Cmd: "pong", Msg: ""}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// The Msg field has omitempty, so it should not appear in the output.
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}
	if _, exists := parsed["msg"]; exists {
		t.Errorf("expected msg to be omitted, but found in JSON: %s", data)
	}
}

// ---------------------------------------------------------------------------
// p2p_signal_token 命令
// ---------------------------------------------------------------------------

// fakeP2PIssuer 记录签发调用的桩实现
type fakeP2PIssuer struct {
	calls  int
	nodeID string
	name   string
}

func (f *fakeP2PIssuer) IssueP2PSignalToken(nodeID, name string) (string, string, int64, error) {
	f.calls++
	f.nodeID, f.name = nodeID, name
	return "p2p-signal:p2ptfake01", "test-secret", 1234567890, nil
}

// newP2PTokenTestStream 经 net.Pipe 建立 smux 流对：返回服务端流与客户端读端
func newP2PTokenTestStream(t *testing.T) (*smux.Stream, *smux.Stream) {
	t.Helper()
	c1, c2 := net.Pipe()
	t.Cleanup(func() { c1.Close(); c2.Close() })
	cliSess, err := smux.Client(c1, nil)
	if err != nil {
		t.Fatalf("smux client: %v", err)
	}
	t.Cleanup(func() { cliSess.Close() })
	srvSess, err := smux.Server(c2, nil)
	if err != nil {
		t.Fatalf("smux server: %v", err)
	}
	t.Cleanup(func() { srvSess.Close() })
	accepted := make(chan *smux.Stream, 1)
	go func() {
		if s, err := srvSess.AcceptStream(); err == nil {
			accepted <- s
		}
	}()
	cliStream, err := cliSess.OpenStream()
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	select {
	case s := <-accepted:
		return s, cliStream
	case <-time.After(2 * time.Second):
		t.Fatal("accept stream timeout")
		return nil, nil
	}
}

// readP2PTokenResp 从客户端流读一条 ad-hoc JSON 响应
func readP2PTokenResp(t *testing.T, cliStream *smux.Stream) p2pSignalTokenResp {
	t.Helper()
	dec := json.NewDecoder(cliStream)
	var resp p2pSignalTokenResp
	if err := dec.Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}

// p2p_signal_token 命令契约：认证 + 归属 + 类型三道校验，通过后签发并返回平铺 JSON
func TestHandleP2PSignalToken(t *testing.T) {
	newServer := func(t *testing.T) (*ControlServer, *fakeP2PIssuer) {
		nodeMgr := node.NewShardedNodeManager(4)
		_ = nodeMgr.Add(context.Background(), &core.Node{
			ID:     "Node0001",
			Name:   "n1",
			Status: core.NodeStatusOnline,
			Tunnels: []core.Tunnel{
				{Name: "p2p-a", Type: core.TunnelTypeP2P},
				{Name: "web", Type: core.TunnelTypeHTTP, Target: "127.0.0.1:80"},
			},
		})
		issuer := &fakeP2PIssuer{}
		cs := &ControlServer{nodeMgr: nodeMgr, p2pIssuer: issuer}
		return cs, issuer
	}

	t.Run("未认证连接拒绝", func(t *testing.T) {
		cs, _ := newServer(t)
		srv, cli := newP2PTokenTestStream(t)
		state := &connState{} // 未 set 节点
		go cs.handleP2PSignalToken(context.Background(), ControlCmd{Cmd: "p2p_signal_token", Name: "p2p-a"}, state, srv)
		resp := readP2PTokenResp(t, cli)
		if resp.OK || resp.Error == "" {
			t.Fatalf("unauthenticated must fail, got %+v", resp)
		}
	})

	t.Run("未配置 issuer 拒绝", func(t *testing.T) {
		nodeMgr := node.NewShardedNodeManager(4)
		_ = nodeMgr.Add(context.Background(), &core.Node{ID: "Node0001", Status: core.NodeStatusOnline})
		cs := &ControlServer{nodeMgr: nodeMgr}
		srv, cli := newP2PTokenTestStream(t)
		state := &connState{}
		state.set(&core.Node{ID: "Node0001"})
		go cs.handleP2PSignalToken(context.Background(), ControlCmd{Cmd: "p2p_signal_token", Name: "p2p-a"}, state, srv)
		resp := readP2PTokenResp(t, cli)
		if resp.OK || !strings.Contains(resp.Error, "not configured") {
			t.Fatalf("nil issuer must fail, got %+v", resp)
		}
	})

	t.Run("隧道不存在或类型不符拒绝", func(t *testing.T) {
		cs, issuer := newServer(t)
		for _, name := range []string{"nope", "web"} {
			srv, cli := newP2PTokenTestStream(t)
			state := &connState{}
			state.set(&core.Node{ID: "Node0001"})
			go cs.handleP2PSignalToken(context.Background(), ControlCmd{Cmd: "p2p_signal_token", Name: name}, state, srv)
			resp := readP2PTokenResp(t, cli)
			if resp.OK || resp.Error == "" {
				t.Fatalf("tunnel %q must be rejected, got %+v", name, resp)
			}
		}
		if issuer.calls != 0 {
			t.Fatalf("issuer must not be called on rejection, calls=%d", issuer.calls)
		}
	})

	t.Run("合法请求签发并返回凭据", func(t *testing.T) {
		cs, issuer := newServer(t)
		srv, cli := newP2PTokenTestStream(t)
		state := &connState{}
		state.set(&core.Node{ID: "Node0001"})
		go cs.handleP2PSignalToken(context.Background(), ControlCmd{Cmd: "p2p_signal_token", Name: "p2p-a"}, state, srv)
		resp := readP2PTokenResp(t, cli)
		if !resp.OK || resp.Error != "" {
			t.Fatalf("happy path must succeed, got %+v", resp)
		}
		if resp.Username != "p2p-signal:p2ptfake01" || resp.Password != "test-secret" || resp.ExpiresAt != 1234567890 {
			t.Fatalf("credentials mismatch: %+v", resp)
		}
		if issuer.nodeID != "Node0001" || issuer.name != "p2p-a" {
			t.Fatalf("issuer called with (%q, %q)", issuer.nodeID, issuer.name)
		}
	})
}

// ===== 审查 #1/#8a：签发只认持久层真相源 + 禁用隧道不签发 =====

// fakeControlNodeRepo 隔离内存态与持久层的桩：nodeMgr 里有的，repo 里可以没有
// （复现 register 竞态：handleRegister 先写内存、SyncFromClient 拒绝后持久层为空）
type fakeControlNodeRepo struct {
	nodes  map[string]*core.Node
	getErr error // 注入瞬时 DB 错误（复审 R3 测试）
}

func newFakeControlNodeRepo() *fakeControlNodeRepo {
	return &fakeControlNodeRepo{nodes: map[string]*core.Node{}}
}

func (f *fakeControlNodeRepo) Create(n *core.Node) error { f.nodes[n.ID] = n; return nil }
func (f *fakeControlNodeRepo) Update(n *core.Node) error { f.nodes[n.ID] = n; return nil }
func (f *fakeControlNodeRepo) Delete(id string) error    { delete(f.nodes, id); return nil }
func (f *fakeControlNodeRepo) GetAll() ([]*core.Node, error) {
	out := make([]*core.Node, 0, len(f.nodes))
	for _, n := range f.nodes {
		out = append(out, n)
	}
	return out, nil
}

func (f *fakeControlNodeRepo) GetByID(id string) (*core.Node, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	n, ok := f.nodes[id]
	if !ok {
		return nil, core.ErrNodeNotFound
	}
	return n, nil
}

// 凭据签发的发现检查必须以持久层为准：仅存在于 nodeMgr 内存（注册竞态产物、
// 配对校验已拒绝该配置）的 p2p 隧道不得签发；禁用隧道同样不签发
func TestHandleP2PSignalTokenTrustsPersistedTruthOnly(t *testing.T) {
	enabled := true
	disabled := false

	nodeMgr := node.NewShardedNodeManager(4)
	// 内存态：包含一个配对已被拒绝的 p2p 隧道（register 竞态写入）与一个禁用隧道
	_ = nodeMgr.Add(context.Background(), &core.Node{
		ID:     "Node0001",
		Status: core.NodeStatusOnline,
		Tunnels: []core.Tunnel{
			{Name: "p2p-race", Type: core.TunnelTypeP2P},
			{Name: "p2p-off", Type: core.TunnelTypeP2P, Enabled: &disabled},
			{Name: "p2p-ok", Type: core.TunnelTypeP2P, Enabled: &enabled},
		},
	})
	repo := newFakeControlNodeRepo()
	// 持久层真相源：只有启用的 p2p-ok（配对被拒的 p2p-race 与禁用的 p2p-off 不在其中）
	_ = repo.Create(&core.Node{
		ID:     "Node0001",
		Status: core.NodeStatusOnline,
		Tunnels: []core.Tunnel{
			{Name: "p2p-ok", Type: core.TunnelTypeP2P, Enabled: &enabled},
		},
	})

	issuer := &fakeP2PIssuer{}
	cs := &ControlServer{nodeMgr: nodeMgr, nodeRepo: repo, p2pIssuer: issuer}
	state := &connState{}
	state.set(&core.Node{ID: "Node0001"})

	run := func(name string) p2pSignalTokenResp {
		srv, cli := newP2PTokenTestStream(t)
		go cs.handleP2PSignalToken(context.Background(), ControlCmd{Cmd: "p2p_signal_token", Name: name}, state, srv)
		return readP2PTokenResp(t, cli)
	}

	// 内存有、持久层无：必须拒绝（否则 register 竞态绕过配对不变量拿到凭据）
	if resp := run("p2p-race"); resp.OK {
		t.Fatal("tunnel rejected by pairing but present only in memory must NOT be issued")
	}
	// 持久层存在但禁用：必须拒绝
	if resp := run("p2p-off"); resp.OK {
		t.Fatal("disabled tunnel must NOT be issued")
	}
	// 持久层存在且启用：签发
	if resp := run("p2p-ok"); !resp.OK {
		t.Fatalf("enabled persisted tunnel must be issued, got %+v", resp)
	}
}

// 复审 R3：持久层瞬时读错误必须与「隧道不存在」区分——向客户端返回可重试
// 错误（客户端退避后重拉），而不是误判缺失
func TestHandleP2PSignalTokenTransientErrorRetryable(t *testing.T) {
	nodeMgr := node.NewShardedNodeManager(4)
	_ = nodeMgr.Add(context.Background(), &core.Node{
		ID: "Node0001", Status: core.NodeStatusOnline,
		Tunnels: []core.Tunnel{{Name: "p2p-a", Type: core.TunnelTypeP2P}},
	})
	repo := newFakeControlNodeRepo()
	repo.getErr = errors.New("sqlite busy")
	_ = repo.Create(&core.Node{ID: "Node0001", Status: core.NodeStatusOnline,
		Tunnels: []core.Tunnel{{Name: "p2p-a", Type: core.TunnelTypeP2P}}})

	issuer := &fakeP2PIssuer{}
	cs := &ControlServer{nodeMgr: nodeMgr, nodeRepo: repo, p2pIssuer: issuer}
	state := &connState{}
	state.set(&core.Node{ID: "Node0001"})

	srv, cli := newP2PTokenTestStream(t)
	go cs.handleP2PSignalToken(context.Background(), ControlCmd{Cmd: "p2p_signal_token", Name: "p2p-a"}, state, srv)
	resp := readP2PTokenResp(t, cli)
	if resp.OK {
		t.Fatal("transient storage error must not issue")
	}
	if !strings.Contains(resp.Error, "temporary") {
		t.Fatalf("error must signal retryable, got %q", resp.Error)
	}
	if issuer.calls != 0 {
		t.Fatalf("issuer must not be called, calls=%d", issuer.calls)
	}
}

// ===== SEC-03：节点持久化剥离接入 token =====

// persistNode 落库内容不得包含明文 token（注册快照是控制面的独立落库点）
func TestPersistNode_StripsToken(t *testing.T) {
	nodeMgr := node.NewShardedNodeManager(4)
	n := &core.Node{ID: "Node0001", Name: "n1", Token: "register-token-secret", Status: core.NodeStatusOnline}
	if err := nodeMgr.Add(context.Background(), n); err != nil {
		t.Fatalf("Add: %v", err)
	}
	repo := newFakeControlNodeRepo()
	cs := &ControlServer{nodeMgr: nodeMgr, nodeRepo: repo}

	cs.persistNode(n)

	got, err := repo.GetByID("Node0001")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Token != "" {
		t.Fatalf("persistNode must strip Token before persisting, got %q", got.Token)
	}
	// 内存态节点保持原样（token 在内存中仍可用于鉴权语义判断）
	if n.Token != "register-token-secret" {
		t.Fatalf("persistNode must not mutate in-memory node, got %q", n.Token)
	}
}

// ===== SEC-02：register 归属校验 =====

// runRegister 在 smux 流对上执行一次 handleRegister，返回服务端响应
func runRegister(t *testing.T, cs *ControlServer, grant *core.NodeAccessGrant, nodeID string) ControlResponse {
	t.Helper()
	srv, cli := newP2PTokenTestStream(t)
	state := &connState{grant: grant}
	go cs.handleRegister(context.Background(), ControlCmd{Cmd: "register", NodeID: nodeID, Name: "test-node"}, state, srv)
	var resp ControlResponse
	if err := json.NewDecoder(cli).Decode(&resp); err != nil {
		t.Fatalf("decode register response: %v", err)
	}
	return resp
}

// newRegisterTestServer 构造带归属校验开关与 fake 持久层的 ControlServer
func newRegisterTestServer(t *testing.T, ownerCheck bool, repo *fakeControlNodeRepo) (*ControlServer, *node.ShardedNodeManager) {
	t.Helper()
	nodeMgr := node.NewShardedNodeManager(4)
	cs := &ControlServer{nodeMgr: nodeMgr, nodeRepo: repo, registerOwnerCheck: ownerCheck}
	return cs, nodeMgr
}

// 认证身份与 node_id 持久化归属者的交叉校验：异主拒绝，其余四种情况放行
func TestHandleRegisterOwnerCheck(t *testing.T) {
	const nodeID = "Node0001"
	grantA := &core.NodeAccessGrant{UserID: "user-a"}
	grantB := &core.NodeAccessGrant{UserID: "user-b"}
	legacyGrant := &core.NodeAccessGrant{UserID: "system", LegacyGlobal: true}

	t.Run("异主拒绝", func(t *testing.T) {
		repo := newFakeControlNodeRepo()
		_ = repo.Create(&core.Node{ID: nodeID, OwnerUserID: "user-a"})
		cs, nodeMgr := newRegisterTestServer(t, true, repo)

		resp := runRegister(t, cs, grantB, nodeID)
		if resp.Cmd != "err" || resp.Msg != "node registered by another user" {
			t.Fatalf("expected rejection, got %+v", resp)
		}
		if _, ok := nodeMgr.Get(context.Background(), nodeID); ok {
			t.Fatal("rejected register must not add node to manager")
		}
	})

	t.Run("同主通过", func(t *testing.T) {
		repo := newFakeControlNodeRepo()
		_ = repo.Create(&core.Node{ID: nodeID, OwnerUserID: "user-a"})
		cs, _ := newRegisterTestServer(t, true, repo)

		if resp := runRegister(t, cs, grantA, nodeID); resp.Cmd != "ok" {
			t.Fatalf("expected ok, got %+v", resp)
		}
	})

	t.Run("system 归属通过", func(t *testing.T) {
		// legacy 时代落库的记录 OwnerUserID=system：任意用户 token 可接管
		repo := newFakeControlNodeRepo()
		_ = repo.Create(&core.Node{ID: nodeID, OwnerUserID: "system"})
		cs, _ := newRegisterTestServer(t, true, repo)

		if resp := runRegister(t, cs, grantB, nodeID); resp.Cmd != "ok" {
			t.Fatalf("expected ok (persisted owner is system), got %+v", resp)
		}
	})

	t.Run("legacy grant 通过", func(t *testing.T) {
		// 全局 token 认证（grant.UserID=system）：不受归属校验约束
		repo := newFakeControlNodeRepo()
		_ = repo.Create(&core.Node{ID: nodeID, OwnerUserID: "user-a"})
		cs, _ := newRegisterTestServer(t, true, repo)

		if resp := runRegister(t, cs, legacyGrant, nodeID); resp.Cmd != "ok" {
			t.Fatalf("expected ok (legacy global grant), got %+v", resp)
		}
	})

	t.Run("开关关闭通过", func(t *testing.T) {
		repo := newFakeControlNodeRepo()
		_ = repo.Create(&core.Node{ID: nodeID, OwnerUserID: "user-a"})
		cs, _ := newRegisterTestServer(t, false, repo)

		if resp := runRegister(t, cs, grantB, nodeID); resp.Cmd != "ok" {
			t.Fatalf("expected ok (owner check disabled), got %+v", resp)
		}
	})
}

// TestReadBoundedLine 固化生产事故（conn 级 LimitedReader 吞掉 64KB 配额）
// 的回归守卫：长度限制必须只作用于认证行本身，连接后续流量不受任何限制。
func TestReadBoundedLine(t *testing.T) {
	// 正常行：返回含换行符，可被后续 json.Unmarshal 消费
	r := bufio.NewReader(strings.NewReader("{\"token\":\"x\"}\nrest"))
	line, err := readBoundedLine(r, maxAuthLineBytes)
	if err != nil || line != "{\"token\":\"x\"}\n" {
		t.Fatalf("normal line: got %q, %v", line, err)
	}

	// 超长行：拒绝（防 OOM），错误可识别
	big := strings.Repeat("a", maxAuthLineBytes+4096) + "\n"
	if _, err := readBoundedLine(bufio.NewReaderSize(strings.NewReader(big), 4096), maxAuthLineBytes); !errors.Is(err, errAuthLineTooLarge) {
		t.Fatalf("oversized line: want errAuthLineTooLarge, got %v", err)
	}

	// 核心回归：读完一行后，同一 reader 上的后续大量数据必须完全可读——
	// 事故版（io.LimitedReader 包装连接）在此处 64KB 后返回 EOF，整条连接死亡
	const tail = 512 * 1024 // 远超 maxAuthLineBytes
	r2 := bufio.NewReaderSize(strings.NewReader("auth\n"+strings.Repeat("y", tail)), 4096)
	if _, err := readBoundedLine(r2, maxAuthLineBytes); err != nil {
		t.Fatalf("line before tail: %v", err)
	}
	rest, err := io.ReadAll(r2)
	if err != nil || len(rest) != tail {
		t.Fatalf("post-line traffic must be unlimited: got %d bytes, err %v (want %d)", len(rest), err, tail)
	}
}
