//go:build p2p

package easyp2p

import (
	"bytes"
	"context"
	"io"
	"strconv"
	"testing"
	"time"
)

// TestExchangeRelayKey 验证两端经真实 MQTT broker 交换 ECDHE 公钥后：
//   - sharedKey 一致（ECDH 派生）
//   - isClient 互补（公钥字典序，一真一假）
//   - RelayPairingToken 两端一致且为 32 字符 hex
//
// MQTTSignalSession 是 struct 非 interface，无法 mock，必须连真实 MQTT broker。
func TestExchangeRelayKey(t *testing.T) {
	if testing.Short() {
		t.Skip("needs real MQTT broker")
	}

	// 每次运行用唯一 sessionUid，避免与并发 CI run 在公共 broker topic 上碰撞。
	sessionUid := "relay-keyx-test-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	timeout := 30 * time.Second

	type result struct {
		sharedKey *[32]byte
		isClient  bool
		err       error
	}
	resCh := make(chan result, 2)

	// endpoint 启动一个独立信令会话并执行密钥交换。
	// ctx 加 timeout：NewMQTTSignalSession 内部 client.Connect 用 ConnectRetry 无限重试，
	// 无 ctx 截止则 broker 不可达时永久挂起；加截止后能快速返回真实错误。
	endpoint := func() {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		signal, err := NewMQTTSignalSession(ctx,
			MQTT_GenerateClientID(TopicDesc_Signal, sessionUid, 0), "", io.Discard)
		if err != nil {
			resCh <- result{err: err}
			return
		}
		defer signal.Close()
		sk, isClient, err := ExchangeRelayKey(ctx, signal, sessionUid, timeout)
		resCh <- result{sharedKey: sk, isClient: isClient, err: err}
	}

	go endpoint()
	go endpoint()

	var results []result
	for len(results) < 2 {
		select {
		case r := <-resCh:
			results = append(results, r)
		case <-time.After(timeout + 15*time.Second):
			t.Fatal("timed out waiting for both endpoints")
		}
	}

	for i, r := range results {
		if r.err != nil {
			t.Fatalf("endpoint %d exchange failed: %v", i, r.err)
		}
		if r.sharedKey == nil {
			t.Fatalf("endpoint %d returned nil sharedKey", i)
		}
	}

	// 1. sharedKey 两端必须一致（ECDH）。
	if !bytes.Equal(results[0].sharedKey[:], results[1].sharedKey[:]) {
		t.Fatalf("sharedKey mismatch:\n A=%x\n B=%x",
			results[0].sharedKey[:], results[1].sharedKey[:])
	}

	// 2. isClient 两端必须互补（公钥字典序，确定性）。
	if results[0].isClient == results[1].isClient {
		t.Fatalf("isClient not complementary: A=%v B=%v",
			results[0].isClient, results[1].isClient)
	}

	// 3. RelayPairingToken 两端一致 + 32 字符 hex。
	tokA, err := RelayPairingToken(results[0].sharedKey)
	if err != nil {
		t.Fatalf("RelayPairingToken A: %v", err)
	}
	tokB, err := RelayPairingToken(results[1].sharedKey)
	if err != nil {
		t.Fatalf("RelayPairingToken B: %v", err)
	}
	if tokA != tokB {
		t.Fatalf("pairing token mismatch: A=%q B=%q", tokA, tokB)
	}
	if len(tokA) != 32 {
		t.Fatalf("pairing token length = %d, want 32: %q", len(tokA), tokA)
	}
	t.Logf("sharedKey=%x isClient(A)=%v isClient(B)=%v token=%s",
		results[0].sharedKey[:], results[0].isClient, results[1].isClient, tokA)
}
