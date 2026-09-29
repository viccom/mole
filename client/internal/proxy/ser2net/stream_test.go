package ser2net

import (
	"sync"
	"testing"
)

func TestStreamHubSubscribeReceivesPublishedEvent(t *testing.T) {
	hub := NewStreamHub(10)

	ch, unsub := hub.Subscribe("alpha", 0)
	defer unsub()

	evt := PacketInfo{Tunnel: "alpha", Dir: "UDP_IN", DataLen: 8}
	hub.Publish(evt)

	got := <-ch
	if got.Tunnel != "alpha" {
		t.Fatalf("expected tunnel=alpha, got %s", got.Tunnel)
	}
	if got.Dir != "UDP_IN" {
		t.Fatalf("expected dir=UDP_IN, got %s", got.Dir)
	}
	if got.DataLen != 8 {
		t.Fatalf("expected data_len=8, got %d", got.DataLen)
	}
}

func TestStreamHubSubscribeTail(t *testing.T) {
	hub := NewStreamHub(200)
	hub.Publish(PacketInfo{Tunnel: "alpha", Dir: "SERIAL_OUT", DataLen: 1})
	hub.Publish(PacketInfo{Tunnel: "alpha", Dir: "SERIAL_OUT", DataLen: 2})
	hub.Publish(PacketInfo{Tunnel: "alpha", Dir: "SERIAL_OUT", DataLen: 3})

	ch, unsub := hub.Subscribe("alpha", 2)
	defer unsub()

	first := <-ch
	second := <-ch
	if first.DataLen != 2 || second.DataLen != 3 {
		t.Fatalf("tail events want lengths [2,3], got [%d,%d]", first.DataLen, second.DataLen)
	}
}

func TestStreamHubConcurrentPublish(t *testing.T) {
	hub := NewStreamHub(100)
	ch, unsub := hub.Subscribe("alpha", 0)
	defer unsub()

	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				hub.Publish(PacketInfo{Tunnel: "alpha", Dir: "UDP_OUT", DataLen: i})
			}
		}()
	}
	wg.Wait()

	for i := 0; i < 10; i++ {
		<-ch
	}
}
