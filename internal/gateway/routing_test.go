package gateway

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rathodpratham15/infermesh/internal/backend"
	"github.com/rathodpratham15/infermesh/internal/router"
)

// countingBackend records calls and fails or blocks on demand.
type countingBackend struct {
	name  string
	calls atomic.Int64
	err   error         // returned by Generate/Stream when set
	gate  chan struct{} // when set, Generate blocks until it is closed
}

func (b *countingBackend) Name() string                 { return b.name }
func (b *countingBackend) Health(context.Context) error { return nil }
func (b *countingBackend) Generate(ctx context.Context, _ backend.Request) (backend.Response, error) {
	b.calls.Add(1)
	if b.gate != nil {
		select {
		case <-b.gate:
		case <-ctx.Done():
			return backend.Response{}, ctx.Err()
		}
	}
	if b.err != nil {
		return backend.Response{}, b.err
	}
	return backend.Response{Content: "from " + b.name, FinishReason: "stop"}, nil
}
func (b *countingBackend) Stream(ctx context.Context, _ backend.Request) (<-chan backend.Token, error) {
	b.calls.Add(1)
	if b.err != nil {
		return nil, b.err
	}
	ch := make(chan backend.Token, 2)
	ch <- backend.Token{Text: "from " + b.name}
	ch <- backend.Token{FinishReason: "stop"}
	close(ch)
	return ch, nil
}

func routedServer(t *testing.T, opts router.Options, retries int, members ...router.Member) *httptest.Server {
	t.Helper()
	s := New(router.NewPool(members, opts), slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.MaxRetries = retries
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func member(b backend.InferenceBackend, max int) router.Member {
	return router.Member{Backend: b, Weight: 1, MaxConcurrent: max}
}

const chatBody = `{"model":"m","messages":[{"role":"user","content":"hi"}]}`

func servedBy(t *testing.T, resp *http.Response) string {
	t.Helper()
	return resp.Header.Get("X-InferMesh-Backend")
}

func TestRetryOnAnotherBackend(t *testing.T) {
	bad := &countingBackend{name: "bad", err: &backend.HTTPError{Status: 503}}
	good := &countingBackend{name: "good"}
	ts := routedServer(t, router.Options{Policy: router.RoundRobin}, 1, member(bad, 10), member(good, 10))

	// Round robin sends the first request to "bad"; the retry must land on "good".
	resp := post(t, ts.URL, chatBody)
	if resp.StatusCode != 200 || servedBy(t, resp) != "good" {
		t.Fatalf("status=%d served by %q", resp.StatusCode, servedBy(t, resp))
	}
	if bad.calls.Load() != 1 || good.calls.Load() != 1 {
		t.Fatalf("calls bad=%d good=%d", bad.calls.Load(), good.calls.Load())
	}
}

func TestRetryOnStreamStartFailure(t *testing.T) {
	bad := &countingBackend{name: "bad", err: &backend.HTTPError{Status: 500}}
	good := &countingBackend{name: "good"}
	ts := routedServer(t, router.Options{Policy: router.RoundRobin}, 1, member(bad, 10), member(good, 10))
	resp := post(t, ts.URL, streamBody)
	if resp.StatusCode != 200 || servedBy(t, resp) != "good" {
		t.Fatalf("status=%d served by %q", resp.StatusCode, servedBy(t, resp))
	}
}

func TestNoRetryForClientErrors(t *testing.T) {
	bad := &countingBackend{name: "bad", err: &backend.HTTPError{Status: 400, Body: "nope"}}
	good := &countingBackend{name: "good"}
	ts := routedServer(t, router.Options{Policy: router.RoundRobin}, 3, member(bad, 10), member(good, 10))
	resp := post(t, ts.URL, chatBody)
	if resp.StatusCode != http.StatusBadGateway || good.calls.Load() != 0 {
		t.Fatalf("status=%d good calls=%d", resp.StatusCode, good.calls.Load())
	}
}

func TestRetriesExhaustedReportsBackendError(t *testing.T) {
	a := &countingBackend{name: "a", err: &backend.HTTPError{Status: 503}}
	b := &countingBackend{name: "b", err: &backend.HTTPError{Status: 503}}
	ts := routedServer(t, router.Options{Policy: router.RoundRobin}, 5, member(a, 10), member(b, 10))
	resp := post(t, ts.URL, chatBody)
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
	if a.calls.Load() != 1 || b.calls.Load() != 1 {
		t.Fatalf("each backend should be tried once: a=%d b=%d", a.calls.Load(), b.calls.Load())
	}
}

func TestLoadShedding429(t *testing.T) {
	slow := &countingBackend{name: "slow", gate: make(chan struct{})}
	ts := routedServer(t, router.Options{MaxQueue: 1}, 0, member(slow, 1))

	var wg sync.WaitGroup
	statuses := make(chan int, 2)
	send := func() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", strings.NewReader(chatBody))
			if err != nil {
				t.Error(err)
				return
			}
			_ = resp.Body.Close()
			statuses <- resp.StatusCode
		}()
	}
	send() // takes the only slot
	waitUntil(t, func() bool { active, _ := loadState(t, ts); return active == 1 })
	send() // waits in the queue
	waitUntil(t, func() bool { _, queued := loadState(t, ts); return queued == 1 })

	// Third request: slot taken, queue full -> 429 with Retry-After.
	resp := post(t, ts.URL, chatBody)
	if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") == "" {
		t.Fatalf("status=%d retry-after=%q", resp.StatusCode, resp.Header.Get("Retry-After"))
	}
	close(slow.gate)
	wg.Wait()
	close(statuses)
	for st := range statuses {
		if st != 200 {
			t.Fatalf("queued request got %d", st)
		}
	}
}

// loadState returns the first backend's active requests and the queue depth.
func loadState(t *testing.T, ts *httptest.Server) (active, queued int) {
	t.Helper()
	resp, err := http.Get(ts.URL + "/v1/backends")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		QueueDepth int `json:"queue_depth"`
		Backends   []struct {
			Active int `json:"active"`
		} `json:"backends"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out.Backends[0].Active, out.QueueDepth
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestRequestTimeoutWhileQueued(t *testing.T) {
	slow := &countingBackend{name: "slow", gate: make(chan struct{})}
	defer close(slow.gate)
	s := New(router.NewPool([]router.Member{member(slow, 1)}, router.Options{MaxQueue: 5}),
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.RequestTimeout = 100 * time.Millisecond
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp := post(t, ts.URL, chatBody) // slot is free: backend blocks until the timeout cancels it
	if resp.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504", resp.StatusCode)
	}
}

func TestBackendsEndpointReflectsRouting(t *testing.T) {
	a := &countingBackend{name: "a"}
	b := &countingBackend{name: "b"}
	ts := routedServer(t, router.Options{Policy: router.RoundRobin}, 0, member(a, 10), member(b, 10))
	for i := 0; i < 4; i++ {
		post(t, ts.URL, chatBody)
	}
	resp, err := http.Get(ts.URL + "/v1/backends")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		Backends []router.BackendStats `json:"backends"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Backends) != 2 || out.Backends[0].Completed != 2 || out.Backends[1].Completed != 2 {
		t.Fatalf("unexpected stats: %+v", out.Backends)
	}
}
