package gateway

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rathodpratham15/infermesh/internal/backend"
	"github.com/rathodpratham15/infermesh/internal/router"
)

// testRouter wraps backends in a pool with generous limits.
func testRouter(bs ...backend.InferenceBackend) *router.Pool {
	var ms []router.Member
	for _, b := range bs {
		ms = append(ms, router.Member{Backend: b, Weight: 1, MaxConcurrent: 100})
	}
	return router.NewPool(ms, router.Options{MaxQueue: 10})
}

func newTestServer(t *testing.T) (*httptest.Server, *backend.Mock) {
	t.Helper()
	m := backend.NewMock("mock", 0)
	s := New(testRouter(m), slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts, m
}

func post(t *testing.T, url, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(url+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func TestChatCompletions(t *testing.T) {
	ts, _ := newTestServer(t)
	resp := post(t, ts.URL, `{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if resp.Header.Get("X-Request-ID") == "" || resp.Header.Get("X-InferMesh-Backend") != "mock" {
		t.Fatalf("missing headers: %v", resp.Header)
	}
	var out chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Object != "chat.completion" || len(out.Choices) != 1 || out.Choices[0].Message.Content == "" {
		t.Fatalf("unexpected body: %+v", out)
	}
	if out.Usage.TotalTokens != out.Usage.PromptTokens+out.Usage.CompletionTokens {
		t.Fatalf("bad usage: %+v", out.Usage)
	}
}

func TestChatCompletionsValidation(t *testing.T) {
	ts, _ := newTestServer(t)
	for name, body := range map[string]string{"bad json": `{`, "no messages": `{"model":"m","messages":[]}`} {
		if resp := post(t, ts.URL, body); resp.StatusCode != 400 {
			t.Errorf("%s: status %d, want 400", name, resp.StatusCode)
		}
	}
}

func TestRequestIDPropagates(t *testing.T) {
	ts, _ := newTestServer(t)
	req, _ := http.NewRequest("POST", ts.URL+"/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"x"}]}`))
	req.Header.Set("X-Request-ID", "abc")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out chatResponse
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if resp.Header.Get("X-Request-ID") != "abc" || out.ID != "abc" {
		t.Fatalf("request id not propagated: header=%q body=%q", resp.Header.Get("X-Request-ID"), out.ID)
	}
}

func TestHealthAndReadiness(t *testing.T) {
	m := backend.NewMock("mock", 0)
	pool := testRouter(m)
	ts := httptest.NewServer(New(pool, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer ts.Close()
	get := func(p string) int {
		resp, err := http.Get(ts.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if get("/healthz") != 200 || get("/readyz") != 200 {
		t.Fatal("expected healthy and ready")
	}
	m.SetHealthy(false)
	pool.CheckHealth(context.Background()) // readiness and routing both use the cached state, refreshed here
	if get("/healthz") != 200 {
		t.Fatal("liveness must not depend on backends")
	}
	if get("/readyz") != 503 {
		t.Fatal("readiness should fail with no healthy backend")
	}
	if resp := post(t, ts.URL, `{"messages":[{"role":"user","content":"x"}]}`); resp.StatusCode != 503 {
		t.Fatalf("expected 503 with no healthy backend, got %d", resp.StatusCode)
	}
}

// healthCounter counts Health() calls so a test can tell a fresh probe from cached state.
type healthCounter struct {
	*backend.Mock
	calls atomic.Int64
}

func (h *healthCounter) Health(ctx context.Context) error {
	h.calls.Add(1)
	return h.Mock.Health(ctx)
}

func TestReadyzAgreesWithRouting(t *testing.T) {
	m := backend.NewMock("m", 0)
	pool := testRouter(m)
	ts := httptest.NewServer(New(pool, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer ts.Close()
	status := func(path string) int {
		var resp *http.Response
		var err error
		if path == "/v1/chat/completions" {
			resp, err = http.Post(ts.URL+path, "application/json", strings.NewReader(`{"messages":[{"role":"user","content":"x"}]}`))
		} else {
			resp, err = http.Get(ts.URL + path)
		}
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	// At every step, "ready" must mean "routing can serve a request" and vice versa.
	agree := func(step string) {
		t.Helper()
		ready := status("/readyz") == 200
		served := status("/v1/chat/completions") != http.StatusServiceUnavailable
		if ready != served {
			t.Fatalf("%s: /readyz ready=%v but routing serves=%v", step, ready, served)
		}
	}
	agree("initial")
	m.SetHealthy(false) // the backend just went down; nothing has re-probed yet
	agree("backend down, cache not refreshed")
	pool.CheckHealth(context.Background())
	agree("cache refreshed, backend down")
	m.SetHealthy(true)
	agree("backend back up, cache not refreshed")
	pool.CheckHealth(context.Background())
	agree("cache refreshed, backend up")
}

func TestReadyzDoesNotProbeBackends(t *testing.T) {
	h := &healthCounter{Mock: backend.NewMock("m", 0)}
	pool := testRouter(h)
	ts := httptest.NewServer(New(pool, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer ts.Close()
	for i := 0; i < 5; i++ {
		resp, err := http.Get(ts.URL + "/readyz")
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}
	if n := h.calls.Load(); n != 0 {
		t.Fatalf("/readyz made %d backend health calls; it must report cached state", n)
	}
}

func TestReadyzReportsTheProbeError(t *testing.T) {
	m := backend.NewMock("m", 0)
	m.SetHealthy(false)
	pool := testRouter(m)
	pool.CheckHealth(context.Background())
	ts := httptest.NewServer(New(pool, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		Ready    bool              `json:"ready"`
		Backends map[string]string `json:"backends"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode != 503 || out.Ready || !strings.Contains(out.Backends["m"], "unhealthy: mock backend marked unhealthy") {
		t.Fatalf("status=%d body=%+v", resp.StatusCode, out)
	}
}
