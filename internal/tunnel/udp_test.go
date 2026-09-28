package tunnel

import (
	"sync"
	"testing"
)

// claimUDPSession 语义（REL-03）：持锁把新建会话回插 sessions；
// 同 key 已有并发创建者时，新会话是败者——不落 map，返回既有会话，
// 由调用方负责关闭败者资源（cancel/读协程退出/配额释放）
func TestClaimUDPSession(t *testing.T) {
	mu := &sync.Mutex{}
	sessions := make(map[string]*udpSession)
	winner := &udpSession{}
	loser := &udpSession{}

	// 空 map：直接插入并胜出
	got, inserted := claimUDPSession(mu, sessions, "k", winner)
	if !inserted || got != winner {
		t.Fatalf("first insert must win, inserted=%v got=%p", inserted, got)
	}
	if sessions["k"] != winner {
		t.Fatal("winner must be in map")
	}

	// 预置 key 后并发插入：败者不落 map，返回既有会话供转发
	got, inserted = claimUDPSession(mu, sessions, "k", loser)
	if inserted || got != winner {
		t.Fatalf("late inserter must lose, inserted=%v got=%p", inserted, got)
	}
	if sessions["k"] != winner {
		t.Fatal("map must keep original winner")
	}

	// 同一指针重复声明：幂等胜出（已占据者重入不误判为败者）
	got, inserted = claimUDPSession(mu, sessions, "k", winner)
	if !inserted || got != winner {
		t.Fatalf("re-claim of the same session must be idempotent, inserted=%v", inserted)
	}
}
