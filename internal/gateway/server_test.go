package gateway

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
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
	if get("/healthz") != 200 {
		t.Fatal("liveness must not depend on backends")
	}
	if get("/readyz") != 503 {
		t.Fatal("readiness should fail with no healthy backend")
	}
	// Routing uses cached health, refreshed by the periodic check.
	pool.CheckHealth(context.Background())
	if resp := post(t, ts.URL, `{"messages":[{"role":"user","content":"x"}]}`); resp.StatusCode != 503 {
		t.Fatalf("expected 503 with no healthy backend, got %d", resp.StatusCode)
	}
}
