package gateway

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/rathodpratham15/infermesh/internal/backend"
	"github.com/rathodpratham15/infermesh/internal/events"
	"github.com/rathodpratham15/infermesh/internal/router"
)

type capturePublisher struct {
	mu  sync.Mutex
	got []events.Event
}

func (c *capturePublisher) Publish(e events.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.got = append(c.got, e)
}
func (c *capturePublisher) Close(context.Context) error { return nil }
func (c *capturePublisher) all() []events.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]events.Event(nil), c.got...)
}

func eventServer(t *testing.T, opts router.Options, retries int, ms ...router.Member) (*httptest.Server, *capturePublisher) {
	t.Helper()
	pub := &capturePublisher{}
	s := New(router.NewPool(ms, opts), slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.MaxRetries = retries
	s.Events = pub
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts, pub
}

func types(es []events.Event) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.Type)
	}
	return out
}

func TestEventsNonStreamingSuccess(t *testing.T) {
	ts, pub := eventServer(t, router.Options{}, 0, member(backend.NewMock("mock", 0), 10))
	resp := post(t, ts.URL, `{"model":"mdl","messages":[{"role":"user","content":"hello there"}]}`)
	id := resp.Header.Get("X-Request-ID")

	es := pub.all()
	if got := types(es); len(got) != 2 || got[0] != events.TypeStarted || got[1] != events.TypeCompleted {
		t.Fatalf("events = %v", got)
	}
	c := es[1]
	if c.RequestID != id || c.Backend != "mock" || c.Model != "mdl" || c.Stream || c.Status != events.StatusSuccess {
		t.Fatalf("bad completed event: %+v", c)
	}
	if c.InputTokens != 2 || c.OutputTokens != len(backend.NewMock("x", 0).Tokens) || c.TTFTMS != nil || c.Attempt != 1 {
		t.Fatalf("bad completed fields: %+v", c)
	}
}

func TestEventsStreamingHaveTTFT(t *testing.T) {
	ts, pub := eventServer(t, router.Options{}, 0, member(backend.NewMock("mock", 2*time.Millisecond), 10))
	resp := post(t, ts.URL, streamBody)
	_, _ = io.Copy(io.Discard, resp.Body)

	es := pub.all()
	if got := types(es); len(got) != 2 || got[1] != events.TypeCompleted {
		t.Fatalf("events = %v", got)
	}
	c := es[1]
	if !c.Stream || c.TTFTMS == nil || c.OutputTokens == 0 || c.GenerationMS <= 0 {
		t.Fatalf("bad streaming completed event: %+v", c)
	}
}

func TestEventsRetryAndFailure(t *testing.T) {
	bad := &countingBackend{name: "bad", err: &backend.HTTPError{Status: 503}}
	good := &countingBackend{name: "good"}
	ts, pub := eventServer(t, router.Options{Policy: router.RoundRobin}, 1, member(bad, 10), member(good, 10))
	post(t, ts.URL, chatBody)

	es := pub.all()
	if got := types(es); len(got) != 3 || got[0] != events.TypeStarted || got[1] != events.TypeStarted || got[2] != events.TypeCompleted {
		t.Fatalf("events = %v", got)
	}
	if es[0].Backend != "bad" || es[0].Attempt != 1 || es[1].Backend != "good" || es[1].Attempt != 2 || es[2].Attempt != 2 {
		t.Fatalf("attempt bookkeeping wrong: %+v", es)
	}

	// Everything failing ends in one InferenceFailed.
	ts2, pub2 := eventServer(t, router.Options{}, 0, member(&countingBackend{name: "bad", err: &backend.HTTPError{Status: 500, Body: "x"}}, 10))
	post(t, ts2.URL, chatBody)
	es2 := pub2.all()
	if got := types(es2); len(got) != 2 || got[1] != events.TypeFailed {
		t.Fatalf("events = %v", got)
	}
	if es2[1].Status != events.StatusError || es2[1].Error == "" || es2[1].Backend != "bad" {
		t.Fatalf("bad failed event: %+v", es2[1])
	}
}

func TestEventsRejectedAndQueueTime(t *testing.T) {
	// No healthy backend: failed event, status rejected, no started event.
	m := backend.NewMock("mock", 0)
	m.SetHealthy(false)
	pool := router.NewPool([]router.Member{member(m, 1)}, router.Options{})
	pool.CheckHealth(context.Background())
	pub := &capturePublisher{}
	s := New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.Events = pub
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	if resp := post(t, ts.URL, chatBody); resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status %d", resp.StatusCode)
	}
	es := pub.all()
	if len(es) != 1 || es[0].Type != events.TypeFailed || es[0].Status != events.StatusRejected {
		t.Fatalf("events = %+v", es)
	}

	// A request that queues behind a busy slot reports a non-zero queue_ms.
	slow := &countingBackend{name: "slow", gate: make(chan struct{})}
	ts2, pub2 := eventServer(t, router.Options{MaxQueue: 5}, 0, member(slow, 1))
	done := make(chan struct{})
	go func() {
		defer close(done)
		post(t, ts2.URL, chatBody)
	}()
	waitUntil(t, func() bool { a, _ := loadState(t, ts2); return a == 1 })
	go func() { post(t, ts2.URL, chatBody) }()
	waitUntil(t, func() bool { _, q := loadState(t, ts2); return q == 1 })
	time.Sleep(30 * time.Millisecond)
	close(slow.gate)
	<-done
	waitUntil(t, func() bool { return len(pub2.all()) == 4 })
	var queued int64
	for _, e := range pub2.all() {
		queued = max(queued, e.QueueMS)
	}
	if queued < 20 {
		t.Fatalf("expected queue_ms >= 20 for the queued request, got %d", queued)
	}
}
