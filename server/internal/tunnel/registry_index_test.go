package tunnel

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"


	"moleAgent_Serv/internal/core"
)

// flipNodeProvider 交替返回两套节点视图（模拟高频配置变更下
// RebuildIndex 的输入在两个版本间翻转）
type flipNodeProvider struct {
	mu   sync.Mutex
	flip bool
	nA   *core.Node
	nB   *core.Node
}

func (f *flipNodeProvider) Get(_ context.Context, id string) (*core.Node, bool) {
	if id == f.nA.ID {
		return f.nA, true
	}
	if id == f.nB.ID {
		return f.nB, true
	}
	return nil, false
}

func (f *flipNodeProvider) GetAll(_ context.Context) []*core.Node {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.flip = !f.flip
	if f.flip {
		return []*core.Node{f.nA}
	}
	return []*core.Node{f.nB}
}

func (f *flipNodeProvider) GetSession(_ context.Context, _ string) (any, error) {
	return nil, errors.New("no session")
}

// 单次重建后双索引必须同版本：domain 与 tunnel 索引都指向同一次输入
func TestRebuildIndexDualIndexConsistent(t *testing.T) {
	nA := onlineNode("NodeA001", core.Tunnel{
		Name: "ta", Type: core.TunnelTypeHTTP, Target: "127.0.0.1:80", Domain: "foo.example.com",
	})
	nB := onlineNode("NodeB002", core.Tunnel{
		Name: "tb", Type: core.TunnelTypeHTTP, Target: "127.0.0.1:80", Domain: "bar.example.com",
	})
	provider := &flipNodeProvider{nA: nA, nB: nB}
	tg := NewTunnelGateway(provider, 10, nil)
	ctx := context.Background()

	tg.RebuildIndex(ctx)
	if n, name := tg.findByDomain(ctx, "foo.example.com"); n == nil || n.ID != nA.ID || name != "ta" {
		t.Fatalf("version A domain index mismatch: node=%v name=%q", n, name)
	}
	if n := tg.findByTunnelName(ctx, "ta"); n == nil || n.ID != nA.ID {
		t.Fatalf("version A tunnel index mismatch: %v", n)
	}

	tg.RebuildIndex(ctx)
	if n, name := tg.findByDomain(ctx, "bar.example.com"); n == nil || n.ID != nB.ID || name != "tb" {
		t.Fatalf("version B domain index mismatch: node=%v name=%q", n, name)
	}
	if n := tg.findByTunnelName(ctx, "tb"); n == nil || n.ID != nB.ID {
		t.Fatalf("version B tunnel index mismatch: %v", n)
	}
}

// 并发重建 + 双索引交叉读（QUA-01）：-race 下验证索引替换无数据竞争，
// 读取结果始终为 nil 或预期节点（合法快照值）。
// 说明：读侧对两索引是两次独立方法调用，跨调用本就允许版本推进，
// 故原子性以结构保证（同锁替换），此处压测守卫竞争与值合法性。
func TestRebuildIndexConcurrentSwapAndRead(t *testing.T) {
	nA := onlineNode("NodeA001", core.Tunnel{
		Name: "ta", Type: core.TunnelTypeHTTP, Target: "127.0.0.1:80", Domain: "foo.example.com",
	})
	nB := onlineNode("NodeB002", core.Tunnel{
		Name: "tb", Type: core.TunnelTypeHTTP, Target: "127.0.0.1:80", Domain: "bar.example.com",
	})
	provider := &flipNodeProvider{nA: nA, nB: nB}
	tg := NewTunnelGateway(provider, 10, nil)
	ctx := context.Background()

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					tg.RebuildIndex(ctx)
				}
			}
		}()
	}
	valid := map[string]*core.Node{nA.ID: nA, nB.ID: nB}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					for _, q := range []string{"foo.example.com", "bar.example.com"} {
						if n, _ := tg.findByDomain(ctx, q); n != nil {
							if _, ok := valid[n.ID]; !ok {
								t.Errorf("domain index returned unknown node %v", n.ID)
								return
							}
						}
					}
					for _, q := range []string{"ta", "tb"} {
						if n := tg.findByTunnelName(ctx, q); n != nil {
							if _, ok := valid[n.ID]; !ok {
								t.Errorf("tunnel index returned unknown node %v", n.ID)
								return
							}
						}
					}
				}
			}
		}()
	}

	time.Sleep(300 * time.Millisecond)
	close(stop)
	wg.Wait()
}
