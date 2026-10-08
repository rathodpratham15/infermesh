// Package router chooses which backend serves a request and enforces
// per-backend concurrency limits.
package router

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/rathodpratham15/infermesh/internal/backend"
)

var (
	// ErrNoBackend means no healthy backend (outside the excluded set) exists.
	ErrNoBackend = errors.New("no healthy backend available")
	// ErrQueueFull means every backend is at its concurrency limit and the
	// wait queue is full. Callers should shed load (HTTP 429).
	ErrQueueFull = errors.New("all backends are at capacity and the queue is full")
)

type Policy string

const (
	RoundRobin       Policy = "round_robin"
	LeastOutstanding Policy = "least_outstanding"
	Weighted         Policy = "weighted"
)

func ParsePolicy(s string) (Policy, error) {
	switch p := Policy(s); p {
	case RoundRobin, LeastOutstanding, Weighted:
		return p, nil
	}
	return "", fmt.Errorf("unknown routing policy %q (want round_robin, least_outstanding or weighted)", s)
}

// Member describes one backend in the pool.
type Member struct {
	Backend       backend.InferenceBackend
	Weight        int // used by the weighted policy
	MaxConcurrent int // requests in flight before the backend counts as full
}

// Options configure a Pool. Zero values mean: round robin, no queue.
type Options struct {
	Policy   Policy
	MaxQueue int // requests allowed to wait for capacity; 0 rejects immediately
	Log      *slog.Logger
}

// BackendStats is a point-in-time view of one backend.
type BackendStats struct {
	Name          string  `json:"name"`
	Healthy       bool    `json:"healthy"`
	Active        int     `json:"active"`
	MaxConcurrent int     `json:"max_concurrent"`
	Weight        int     `json:"weight"`
	Completed     uint64  `json:"completed"`
	Failures      uint64  `json:"failures"`
	LatencyEWMAMs float64 `json:"latency_ewma_ms"`
}

type member struct {
	Member
	healthy   bool
	active    int
	completed uint64
	failures  uint64
	ewma      time.Duration
	current   int // smooth weighted round-robin state
}

// Router is what the gateway depends on.
type Router interface {
	// Acquire reserves a slot on a backend chosen by the routing policy,
	// skipping any backend named in exclude. It waits (bounded) when every
	// eligible backend is full. The caller must call Lease.Release.
	Acquire(ctx context.Context, exclude ...string) (*Lease, error)
	Backends() []backend.InferenceBackend
	Stats() []BackendStats
	QueueDepth() int
}

// Pool implements Router.
type Pool struct {
	policy   Policy
	maxQueue int
	log      *slog.Logger

	mu      sync.Mutex
	members []*member
	rr      int
	waiting int
	wake    chan struct{} // closed and replaced whenever capacity or health changes
}

func NewPool(members []Member, opts Options) *Pool {
	p := &Pool{policy: opts.Policy, maxQueue: opts.MaxQueue, log: opts.Log, wake: make(chan struct{})}
	if p.policy == "" {
		p.policy = RoundRobin
	}
	if p.log == nil {
		p.log = slog.New(slog.DiscardHandler)
	}
	for _, m := range members {
		if m.Weight < 1 {
			m.Weight = 1
		}
		if m.MaxConcurrent < 1 {
			m.MaxConcurrent = 1
		}
		// Optimistically healthy until the first check says otherwise.
		p.members = append(p.members, &member{Member: m, healthy: true})
	}
	return p
}

func (p *Pool) Backends() []backend.InferenceBackend {
	out := make([]backend.InferenceBackend, len(p.members))
	for i, m := range p.members {
		out[i] = m.Backend
	}
	return out
}

func (p *Pool) Stats() []BackendStats {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]BackendStats, len(p.members))
	for i, m := range p.members {
		out[i] = BackendStats{
			Name: m.Backend.Name(), Healthy: m.healthy, Active: m.active, MaxConcurrent: m.MaxConcurrent,
			Weight: m.Weight, Completed: m.completed, Failures: m.failures,
			LatencyEWMAMs: float64(m.ewma) / float64(time.Millisecond),
		}
	}
	return out
}

func (p *Pool) QueueDepth() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.waiting
}

// broadcast wakes every waiter. Caller holds p.mu.
func (p *Pool) broadcast() {
	close(p.wake)
	p.wake = make(chan struct{})
}

func (p *Pool) Acquire(ctx context.Context, exclude ...string) (*Lease, error) {
	skip := make(map[string]bool, len(exclude))
	for _, n := range exclude {
		skip[n] = true
	}
	queued := false
	for {
		p.mu.Lock()
		if queued {
			p.waiting-- // we were woken; re-evaluate from scratch
		}
		var cands []*member
		anyHealthy := false
		for _, m := range p.members {
			if !m.healthy || skip[m.Backend.Name()] {
				continue
			}
			anyHealthy = true
			if m.active < m.MaxConcurrent {
				cands = append(cands, m)
			}
		}
		if len(cands) > 0 {
			m := p.pick(cands)
			m.active++
			p.mu.Unlock()
			return &Lease{Backend: m.Backend, pool: p, m: m, start: time.Now()}, nil
		}
		if !anyHealthy {
			p.mu.Unlock()
			return nil, ErrNoBackend
		}
		if p.waiting >= p.maxQueue {
			p.mu.Unlock()
			return nil, ErrQueueFull
		}
		p.waiting++
		queued = true
		wake := p.wake
		p.mu.Unlock()

		select {
		case <-wake:
		case <-ctx.Done():
			p.mu.Lock()
			p.waiting--
			p.mu.Unlock()
			return nil, ctx.Err()
		}
	}
}

// pick applies the routing policy to a non-empty candidate list. Caller holds p.mu.
func (p *Pool) pick(cands []*member) *member {
	switch p.policy {
	case LeastOutstanding:
		best := []*member{cands[0]}
		for _, m := range cands[1:] {
			switch {
			case m.active < best[0].active:
				best = []*member{m}
			case m.active == best[0].active:
				best = append(best, m)
			}
		}
		m := best[p.rr%len(best)] // rotate ties so equal backends share load
		p.rr++
		return m
	case Weighted:
		// Smooth weighted round robin (as in nginx): deterministic, and
		// interleaves rather than sending bursts to the heaviest backend.
		total := 0
		var best *member
		for _, m := range cands {
			m.current += m.Weight
			total += m.Weight
			if best == nil || m.current > best.current {
				best = m
			}
		}
		best.current -= total
		return best
	default: // round robin over the full member list, skipping ineligible ones
		eligible := make(map[*member]bool, len(cands))
		for _, m := range cands {
			eligible[m] = true
		}
		n := len(p.members)
		for i := 0; i < n; i++ {
			idx := (p.rr + i) % n
			if m := p.members[idx]; eligible[m] {
				p.rr = idx + 1
				return m
			}
		}
		return cands[0]
	}
}

// Lease is a reserved slot on one backend.
type Lease struct {
	Backend backend.InferenceBackend

	pool  *Pool
	m     *member
	start time.Time
	once  sync.Once
}

// Release frees the slot and records the outcome. err == nil is a success. A
// cancelled context means the client left, which is not a backend fault.
func (l *Lease) Release(err error) {
	l.once.Do(func() {
		d := time.Since(l.start)
		p := l.pool
		p.mu.Lock()
		defer p.mu.Unlock()
		l.m.active--
		switch {
		case err == nil:
			l.m.completed++
			l.m.observe(d)
		case errors.Is(err, context.Canceled):
			l.m.completed++
		default:
			l.m.failures++
		}
		p.broadcast()
	})
}

func (m *member) observe(d time.Duration) {
	const alpha = 0.2
	if m.ewma == 0 {
		m.ewma = d
		return
	}
	m.ewma = time.Duration(alpha*float64(d) + (1-alpha)*float64(m.ewma))
}

// CheckHealth probes every backend once, concurrently, and updates its state.
func (p *Pool) CheckHealth(ctx context.Context) {
	var wg sync.WaitGroup
	for _, m := range p.members {
		wg.Add(1)
		go func() {
			defer wg.Done()
			hctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			err := m.Backend.Health(hctx)
			p.mu.Lock()
			defer p.mu.Unlock()
			if ok := err == nil; ok != m.healthy {
				m.healthy = ok
				p.log.Info("backend health changed", "backend", m.Backend.Name(), "healthy", ok, "err", err)
				p.broadcast()
			}
		}()
	}
	wg.Wait()
}

// RunHealthChecks probes backends every interval until ctx is cancelled.
func (p *Pool) RunHealthChecks(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.CheckHealth(ctx)
		}
	}
}
