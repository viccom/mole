package tunnel

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"moleAgent_Serv/internal/core"
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
		id   string
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
