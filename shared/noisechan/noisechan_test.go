package noisechan

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/flynn/noise"
)

// ---- 测试辅助 ----

func testPSK(seed string) []byte {
	h := sha256.Sum256([]byte(seed))
	return h[:]
}

// assertHsErr 统一断言握手失败错误：哨兵可识别 + 文案前缀（客户端重连日志的观测锚点）。
func assertHsErr(t *testing.T, where string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected handshake error, got nil", where)
	}
	if !errors.Is(err, ErrHandshake) {
		t.Errorf("%s: errors.Is(err, ErrHandshake) = false, err = %v", where, err)
	}
	if !strings.HasPrefix(err.Error(), "channel encryption handshake failed") {
		t.Errorf("%s: error message missing prefix, got %q", where, err.Error())
	}
}

// upgradePair 在既有连接对上跑双端升级，任一端失败即 t.Fatal。
func upgradePair(t *testing.T, c1, c2 net.Conn, psk1, psk2 []byte, timeout time.Duration) (*Conn, *Conn) {
	t.Helper()
	type result struct {
		c   *Conn
		err error
	}
	ch := make(chan result, 1)
	go func() {
		c, err := UpgradeInitiator(c1, bufio.NewReader(c1), psk1, timeout)
		ch <- result{c, err}
	}()
	sc, err := UpgradeResponder(c2, bufio.NewReader(c2), psk2, timeout)
	if err != nil {
		t.Fatalf("responder upgrade: %v", err)
	}
	r := <-ch
	if r.err != nil {
		t.Fatalf("initiator upgrade: %v", r.err)
	}
	return r.c, sc
}

// pipePair 返回一对 net.Pipe 连接（测试结束自动关闭）。
func pipePair(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		c1.Close()
		c2.Close()
	})
	return c1, c2
}

// detPayload 生成确定性载荷（无随机性，失败可复现）。
func detPayload(seed byte, n int) []byte {
	p := make([]byte, n)
	for i := range p {
		p[i] = seed + byte(i%251)
	}
	return p
}

// readExact 用指定大小的缓冲逐段读满 n 字节（小缓冲路径即 pending 语义验证）。
func readExact(t *testing.T, c *Conn, n int, bufSize int) []byte {
	t.Helper()
	out := make([]byte, 0, n)
	p := make([]byte, bufSize)
	for len(out) < n {
		m, err := c.Read(p)
		if err != nil {
			t.Fatalf("Read after %d/%d bytes: %v", len(out), n, err)
		}
		if m == 0 {
			t.Fatalf("Read returned 0, nil")
		}
		out = append(out, p[:m]...)
	}
	return out
}

// frame 构造 2 字节大端长度前缀 + 体的分帧字节。
func frame(body []byte) []byte {
	b := make([]byte, 2+len(body))
	binary.BigEndian.PutUint16(b, uint16(len(body)))
	copy(b[2:], body)
	return b
}

// readRawFrame 在裸流上手工解一帧（bufio 穿针用例的手工发起方使用）。
func readRawFrame(t *testing.T, r io.Reader) []byte {
	t.Helper()
	var hdr [2]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		t.Fatalf("read raw frame header: %v", err)
	}
	body := make([]byte, binary.BigEndian.Uint16(hdr[:]))
	if _, err := io.ReadFull(r, body); err != nil {
		t.Fatalf("read raw frame body: %v", err)
	}
	return body
}

// ---- 1. 握手成功 + 双向回环：大数据拆帧 + 8 字节小缓冲 pending 语义 ----

func TestHandshakeRoundTripLargeDataAndSmallReads(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	psk := testPSK("roundtrip-token")
	type result struct {
		c   *Conn
		err error
	}
	srvCh := make(chan result, 1)
	go func() {
		raw, err := ln.Accept()
		if err != nil {
			srvCh <- result{nil, err}
			return
		}
		// 模拟服务端：认证阶段创建的 bufio.Reader 传入升级（穿针约束）
		c, err := UpgradeResponder(raw, bufio.NewReader(raw), psk, 5*time.Second)
		srvCh <- result{c, err}
	}()

	rawCli, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	ci, err := UpgradeInitiator(rawCli, bufio.NewReader(rawCli), psk, 5*time.Second)
	if err != nil {
		t.Fatalf("initiator upgrade: %v", err)
	}
	sr := <-srvCh
	if sr.err != nil {
		t.Fatalf("responder upgrade: %v", sr.err)
	}
	defer ci.Close()
	defer sr.c.Close()

	// 双向 200KB（超过单帧上限，必然拆帧）
	payloadI := detPayload(0x11, 200*1024)
	payloadR := detPayload(0x77, 200*1024)
	writeErr := make(chan error, 2)
	go func() {
		n, err := ci.Write(payloadI)
		if err == nil && n != len(payloadI) {
			err = fmt.Errorf("short write %d/%d", n, len(payloadI))
		}
		writeErr <- err
	}()
	go func() {
		n, err := sr.c.Write(payloadR)
		if err == nil && n != len(payloadR) {
			err = fmt.Errorf("short write %d/%d", n, len(payloadR))
		}
		writeErr <- err
	}()

	// 应答方用 8 字节小缓冲逐段读（smux 读帧头的真实形态，pending 必须续供）
	gotAtResponder := readExact(t, sr.c, len(payloadI), 8)
	gotAtInitiator := readExact(t, ci, len(payloadR), 4096)
	for i := 0; i < 2; i++ {
		if err := <-writeErr; err != nil {
			t.Fatalf("concurrent write: %v", err)
		}
	}

	if !bytes.Equal(gotAtResponder, payloadI) {
		t.Fatalf("responder received payload mismatch (200KB, 8-byte reads)")
	}
	if !bytes.Equal(gotAtInitiator, payloadR) {
		t.Fatalf("initiator received payload mismatch (200KB)")
	}
}

// ---- 2. psk 错配：两端各自报错，连接被关 ----

func TestPskMismatchBothEndsFailAndConnClosed(t *testing.T) {
	cliRaw, srvRaw := pipePair(t)
	pskA := testPSK("token-known-to-client")
	pskB := testPSK("token-known-to-server")

	type result struct {
		err error
	}
	cliCh := make(chan result, 1)
	go func() {
		_, err := UpgradeInitiator(cliRaw, bufio.NewReader(cliRaw), pskA, 5*time.Second)
		cliCh <- result{err}
	}()
	_, srvErr := UpgradeResponder(srvRaw, bufio.NewReader(srvRaw), pskB, 5*time.Second)

	// 发起方在 msg2 的 AEAD tag 上解密失败（psk 于 msg2 末 token 混入）
	cliRes := <-cliCh
	assertHsErr(t, "initiator (psk mismatch, msg2)", cliRes.err)
	// 应答方在 msg3 上失败（此时密钥链已含错误 psk）
	assertHsErr(t, "responder (psk mismatch, msg3)", srvErr)

	// 握手失败必须关闭底层 conn：关闭后再写必须报错
	// （前置写超时防坏实现不关 conn 时永久阻塞拖垮测试）
	cliRaw.SetWriteDeadline(time.Now().Add(2 * time.Second))
	srvRaw.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, err := cliRaw.Write([]byte("x")); err == nil {
		t.Errorf("write on closed initiator conn unexpectedly succeeded")
	}
	if _, err := srvRaw.Write([]byte("x")); err == nil {
		t.Errorf("write on closed responder conn unexpectedly succeeded")
	}
}

// ---- 3. 握手消息被篡改/截断：明确报错，不 panic ----

func TestGarbageAndTruncatedHandshakeNoPanic(t *testing.T) {
	psk := testPSK("garbage-token")
	cases := []struct {
		name string
		in   []byte
	}{
		// 声明长度 5：小于 AEAD tag 下界，帧损坏必须立即报错
		{"declared length below AEAD bound", []byte{0x00, 0x05, 1, 2, 3, 4, 5}},
		// 垃圾字节流：前 2 字节 'X''X' = 22616，体远不足 → 截断错误
		{"garbage bytes as frame stream", bytes.Repeat([]byte{'X'}, 200)},
		// 声明 96 字节（合法 msg2 体量）但只给 10 字节体
		{"declared 96 body but only 10 sent", append([]byte{0x00, 0x60}, make([]byte, 10)...)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := pipePair(t)
			_, err := UpgradeResponder(raw, bytes.NewReader(tc.in), psk, 2*time.Second)
			assertHsErr(t, tc.name, err)
			if !strings.Contains(err.Error(), "frame") && !strings.Contains(err.Error(), "message 1") {
				t.Errorf("%s: error should identify frame corruption, got %q", tc.name, err.Error())
			}
		})
	}
}

// ---- 4. bufio 穿针：握手前 reader 已预吞后续帧，升级后必须无丢帧 ----

func TestBufioReaderThreadingNoFrameLoss(t *testing.T) {
	cliRaw, srvRaw := pipePair(t)
	// 手工发起方的读写护栏：对端（升级实现）异常时快速失败而非挂死
	cliRaw.SetDeadline(time.Now().Add(5 * time.Second))
	psk := testPSK("threading-token")

	// 手工发起方（不经 UpgradeInitiator）：把「认证行 + msg1」一次性写入，
	// 让应答方 bufio 在认证阶段的一次 fill 中连 msg1 一起吞进缓冲。
	manualSuite := noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashSHA256)
	manualStatic, err := manualSuite.GenerateKeypair(nil)
	if err != nil {
		t.Fatalf("manual initiator static keypair: %v", err)
	}
	hsI, err := noise.NewHandshakeState(noise.Config{
		CipherSuite:           manualSuite,
		Pattern:               noise.HandshakeXX,
		Initiator:             true,
		StaticKeypair:         manualStatic,
		PresharedKey:          psk,
		PresharedKeyPlacement: 2,
	})
	if err != nil {
		t.Fatalf("manual initiator handshake state: %v", err)
	}
	msg1, _, _, err := hsI.WriteMessage(nil, nil)
	if err != nil {
		t.Fatalf("manual initiator msg1: %v", err)
	}
	blob := append([]byte("auth-ok\n"), frame(msg1)...)
	go func() {
		cliRaw.Write(blob) // net.Pipe 同步：写完即已被对端一次 fill 全部读走
	}()

	// 应答方：认证阶段用 bufio 读行 —— msg1 帧被同一批字节吞入缓冲
	br := bufio.NewReader(srvRaw)
	line, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("read auth line: %v", err)
	}
	if line != "auth-ok\n" {
		t.Fatalf("auth line = %q", line)
	}
	if br.Buffered() == 0 {
		t.Fatalf("precondition: msg1 frame should already be buffered in bufio reader")
	}

	type result struct {
		c   *Conn
		err error
	}
	ch := make(chan result, 1)
	go func() {
		c, err := UpgradeResponder(srvRaw, br, psk, 5*time.Second) // 必须复用同一 reader
		ch <- result{c, err}
	}()

	// 手工发起方继续：读 msg2 → 生成 msg3 + 数据帧，一次性写出
	// （应答方读 msg3 时的一次 fill 会把数据帧一并吞入 bufio 缓冲）
	msg2 := readRawFrame(t, cliRaw)
	if _, _, _, err := hsI.ReadMessage(nil, msg2); err != nil {
		t.Fatalf("manual initiator process msg2: %v", err)
	}
	msg3, csSend, _, err := hsI.WriteMessage(nil, nil)
	if err != nil {
		t.Fatalf("manual initiator msg3: %v", err)
	}
	var payloads [][]byte
	out := frame(msg3)
	for i := 0; i < 3; i++ {
		p := []byte(fmt.Sprintf("threaded-frame-%d-payload", i))
		payloads = append(payloads, p)
		ct, err := csSend.Encrypt(nil, nil, p)
		if err != nil {
			t.Fatalf("manual encrypt: %v", err)
		}
		out = append(out, frame(ct)...)
	}
	if _, err := cliRaw.Write(out); err != nil {
		t.Fatalf("manual write msg3+data: %v", err)
	}

	r := <-ch
	if r.err != nil {
		t.Fatalf("responder upgrade with threaded reader: %v", r.err)
	}
	sc := r.c
	defer sc.Close()
	if br.Buffered() == 0 {
		t.Fatalf("postcondition: data frames should remain buffered in bufio reader")
	}

	// 升级后从 Conn 逐帧读出——若任何一次分帧读绕开了 reader，缓冲中的字节即丢失
	var want []byte
	for _, p := range payloads {
		want = append(want, p...)
	}
	sc.SetReadDeadline(time.Now().Add(3 * time.Second)) // 防丢帧死锁挂起：挂起即失败
	got := readExact(t, sc, len(want), 7)
	if !bytes.Equal(got, want) {
		t.Fatalf("threaded read lost/mangled data: got %d bytes, want %d", len(got), len(want))
	}
}

// ---- 5. 握手超时：对端静默，短超时内失败返回 ----

func TestHandshakeTimeout(t *testing.T) {
	cliRaw, _ := pipePair(t) // 对端无人读：msg1 写入即阻塞
	psk := testPSK("timeout-token")

	start := time.Now()
	_, err := UpgradeInitiator(cliRaw, bytes.NewReader(nil), psk, 300*time.Millisecond)
	elapsed := time.Since(start)
	assertHsErr(t, "initiator (peer silent)", err)
	if elapsed > 3*time.Second {
		t.Errorf("timeout took %v, want ~300ms", elapsed)
	}
	cliRaw.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, werr := cliRaw.Write([]byte("x")); werr == nil {
		t.Errorf("write on timed-out conn unexpectedly succeeded")
	}
}

// ---- 6. Write 并发安全：-race 下多 goroutine 并写不炸、不撕帧 ----

func TestConcurrentWrite(t *testing.T) {
	cliRaw, srvRaw := pipePair(t)
	psk := testPSK("concurrent-token")
	ci, sc := upgradePair(t, cliRaw, srvRaw, psk, psk, 5*time.Second)
	defer ci.Close()
	defer sc.Close()

	const writers = 8
	const chunksPerWriter = 50
	chunkLen := 2048
	var chunkOf func(w, i int) []byte
	chunkOf = func(w, i int) []byte {
		c := make([]byte, chunkLen)
		copy(c, fmt.Sprintf("W%02dC%03d|", w, i))
		return c
	}

	total := writers * chunksPerWriter * chunkLen
	readDone := make(chan []byte, 1)
	go func() {
		readDone <- readExact(t, sc, total, 8192)
	}()

	var wg sync.WaitGroup
	errCh := make(chan error, writers)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < chunksPerWriter; i++ {
				if n, err := ci.Write(chunkOf(w, i)); err != nil || n != chunkLen {
					errCh <- fmt.Errorf("writer %d chunk %d: n=%d err=%v", w, i, n, err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent write: %v", err)
	}

	assembled := <-readDone
	if len(assembled) != total {
		t.Fatalf("assembled %d bytes, want %d", len(assembled), total)
	}
	// 帧完整性：任一原始块必须恰好完整出现一次（撕帧/重复/丢字节都会破坏此不变量）
	for w := 0; w < writers; w++ {
		for _, i := range []int{0, 17, chunksPerWriter - 1} {
			if c := bytes.Count(assembled, chunkOf(w, i)); c != 1 {
				t.Fatalf("chunk W%02dC%03d appears %d times in stream (torn/duplicated frame)", w, i, c)
			}
		}
	}
}

// ---- 7. Deadline 委托：过期 deadline 使 Read/Write 报超时 ----

func TestDeadlineDelegation(t *testing.T) {
	cliRaw, srvRaw := pipePair(t)
	psk := testPSK("deadline-token")
	ci, sc := upgradePair(t, cliRaw, srvRaw, psk, psk, 5*time.Second)
	defer ci.Close()
	defer sc.Close()

	past := time.Now().Add(-1 * time.Second)
	if err := ci.SetReadDeadline(past); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	if _, err := ci.Read(make([]byte, 8)); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Errorf("Read with past read deadline: got %v, want os.ErrDeadlineExceeded", err)
	}
	if err := ci.SetWriteDeadline(past); err != nil {
		t.Fatalf("SetWriteDeadline: %v", err)
	}
	if _, err := ci.Write([]byte("x")); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Errorf("Write with past write deadline: got %v, want os.ErrDeadlineExceeded", err)
	}
	if err := sc.SetDeadline(past); err != nil {
		t.Fatalf("SetDeadline: %v", err)
	}
	if _, err := sc.Read(make([]byte, 8)); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Errorf("Read with past combined deadline: got %v, want os.ErrDeadlineExceeded", err)
	}
}

// ---- 8. 错误可识别：哨兵 + 前缀 + 边界输入 ----

func TestErrorIdentifiability(t *testing.T) {
	// 非法 psk 长度：立即失败（不进入握手），同样可识别
	cliRaw, _ := pipePair(t)
	_, err := UpgradeInitiator(cliRaw, bytes.NewReader(nil), []byte("short-psk"), time.Second)
	assertHsErr(t, "initiator (bad psk length)", err)
	if !strings.Contains(err.Error(), "32") {
		t.Errorf("bad psk length error should mention required length, got %q", err.Error())
	}
	cliRaw.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, werr := cliRaw.Write([]byte("x")); werr == nil {
		t.Errorf("write on closed conn unexpectedly succeeded")
	}

	// psk 错配路径的哨兵/前缀断言见 TestPskMismatchBothEndsFailAndConnClosed，
	// 此处再验：ErrHandshake 作为哨兵可被包裹后识别
	wrapped := fmt.Errorf("dial: %w", ErrHandshake)
	if !errors.Is(wrapped, ErrHandshake) {
		t.Errorf("wrapped ErrHandshake not identifiable")
	}
}
