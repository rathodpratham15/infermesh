package router

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/rathodpratham15/infermesh/internal/backend"
)

func pool(policy Policy, maxQueue int, ms ...Member) *Pool {
	return NewPool(ms, Options{Policy: policy, MaxQueue: maxQueue})
}

func mem(name string, weight, max int) (Member, *backend.Mock) {
	b := backend.NewMock(name, 0)
	return Member{Backend: b, Weight: weight, MaxConcurrent: max}, b
}

// acquireN acquires n leases (without releasing) and returns the backend name per lease.
func acquireN(t *testing.T, p *Pool, n int) []string {
	t.Helper()
	var names []string
	for i := 0; i < n; i++ {
		l, err := p.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, l.Backend.Name())
		l.Release(nil)
	}
	return names
}

func TestRoundRobin(t *testing.T) {
	a, _ := mem("a", 1, 10)
	b, _ := mem("b", 1, 10)
	c, _ := mem("c", 1, 10)
	got := acquireN(t, pool(RoundRobin, 0, a, b, c), 6)
	want := []string{"a", "b", "c", "a", "b", "c"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestRoundRobinSkipsUnhealthy(t *testing.T) {
	a, ab := mem("a", 1, 10)
	b, _ := mem("b", 1, 10)
	p := pool(RoundRobin, 0, a, b)
	ab.SetHealthy(false)
	p.CheckHealth(context.Background())
	for _, n := range acquireN(t, p, 4) {
		if n != "b" {
			t.Fatalf("routed to unhealthy backend %s", n)
		}
	}
	ab.SetHealthy(true)
	p.CheckHealth(context.Background())
	seen := map[string]bool{}
	for _, n := range acquireN(t, p, 4) {
		seen[n] = true
	}
	if !seen["a"] || !seen["b"] {
		t.Fatalf("recovered backend not used: %v", seen)
	}
}

func TestWeighted(t *testing.T) {
	a, _ := mem("a", 3, 100)
	b, _ := mem("b", 1, 100)
	counts := map[string]int{}
	for _, n := range acquireN(t, pool(Weighted, 0, a, b), 400) {
		counts[n]++
	}
	if counts["a"] != 300 || counts["b"] != 100 {
		t.Fatalf("expected exact 3:1 split, got %v", counts)
	}
}

func TestLeastOutstanding(t *testing.T) {
	a, _ := mem("a", 1, 10)
	b, _ := mem("b", 1, 10)
	p := pool(LeastOutstanding, 0, a, b)

	// Pin three requests on whichever backend is picked first, then confirm new
	// requests flow to the other one until counts even out.
	first, _ := p.Acquire(context.Background())
	second, _ := p.Acquire(context.Background())
	if first.Backend.Name() == second.Backend.Name() {
		t.Fatal("two idle backends: second request should go to the other one")
	}
	first.Release(nil) // first's backend is now idle, second's has 1 active
	third, _ := p.Acquire(context.Background())
	if third.Backend.Name() != first.Backend.Name() {
		t.Fatalf("expected idle backend %s, got %s", first.Backend.Name(), third.Backend.Name())
	}
}

func TestConcurrencyLimitQueueAndReject(t *testing.T) {
	a, _ := mem("a", 1, 1)
	p := pool(RoundRobin, 1, a)
	held, err := p.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// Second request waits in the queue.
	type result struct {
		l   *Lease
		err error
	}
	res := make(chan result, 1)
	go func() {
		l, err := p.Acquire(context.Background())
		res <- result{l, err}
	}()
	waitFor(t, func() bool { return p.QueueDepth() == 1 })

	// Third request finds the queue full.
	if _, err := p.Acquire(context.Background()); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("expected ErrQueueFull, got %v", err)
	}

	held.Release(nil)
	r := <-res
	if r.err != nil {
		t.Fatal(r.err)
	}
	r.l.Release(nil)
	if p.QueueDepth() != 0 {
		t.Fatalf("queue depth = %d", p.QueueDepth())
	}
}

func TestQueuedAcquireHonoursContext(t *testing.T) {
	a, _ := mem("a", 1, 1)
	p := pool(RoundRobin, 5, a)
	held, _ := p.Acquire(context.Background())
	defer held.Release(nil)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := p.Acquire(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
	if p.QueueDepth() != 0 {
		t.Fatalf("waiter leaked: depth %d", p.QueueDepth())
	}
}

func TestExcludeAndNoBackend(t *testing.T) {
	a, _ := mem("a", 1, 10)
	b, _ := mem("b", 1, 10)
	p := pool(RoundRobin, 0, a, b)
	l, err := p.Acquire(context.Background(), "a")
	if err != nil || l.Backend.Name() != "b" {
		t.Fatalf("got %v, %v", l, err)
	}
	l.Release(nil)
	if _, err := p.Acquire(context.Background(), "a", "b"); !errors.Is(err, ErrNoBackend) {
		t.Fatalf("expected ErrNoBackend, got %v", err)
	}
}

func TestStats(t *testing.T) {
	a, _ := mem("a", 1, 10)
	p := pool(RoundRobin, 0, a)
	l1, _ := p.Acquire(context.Background())
	l2, _ := p.Acquire(context.Background())
	l3, _ := p.Acquire(context.Background())
	time.Sleep(5 * time.Millisecond)
	l1.Release(nil)
	l1.Release(nil) // idempotent
	l2.Release(errors.New("boom"))
	l3.Release(context.Canceled)
	s := p.Stats()[0]
	if s.Active != 0 || s.Completed != 2 || s.Failures != 1 || s.LatencyEWMAMs <= 0 {
		t.Fatalf("stats = %+v", s)
	}
}

func TestAcquireRaceNeverExceedsLimit(t *testing.T) {
	const limit = 3
	a, _ := mem("a", 1, limit)
	p := pool(RoundRobin, 1000, a)
	var mu sync.Mutex
	inFlight, peak := 0, 0
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l, err := p.Acquire(context.Background())
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			inFlight++
			peak = max(peak, inFlight)
			mu.Unlock()
			time.Sleep(time.Millisecond)
			mu.Lock()
			inFlight--
			mu.Unlock()
			l.Release(nil)
		}()
	}
	wg.Wait()
	if peak > limit {
		t.Fatalf("peak concurrency %d exceeded limit %d", peak, limit)
	}
}

func TestParsePolicy(t *testing.T) {
	for _, s := range []string{"round_robin", "least_outstanding", "weighted"} {
		if _, err := ParsePolicy(s); err != nil {
			t.Errorf("%s: %v", s, err)
		}
	}
	if _, err := ParsePolicy("random"); err == nil {
		t.Error("expected error for unknown policy")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(time.Millisecond)
	}
}
