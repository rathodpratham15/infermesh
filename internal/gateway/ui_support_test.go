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

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/rathodpratham15/infermesh/internal/backend"
	"github.com/rathodpratham15/infermesh/internal/router"
)

func uiServer(t *testing.T, configure func(*Server), bs ...backend.InferenceBackend) *httptest.Server {
	t.Helper()
	var ms []router.Member
	for _, b := range bs {
		ms = append(ms, router.Member{Backend: b, Weight: 1, MaxConcurrent: 10})
	}
	s := New(router.NewPool(ms, router.Options{Policy: router.RoundRobin}), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if configure != nil {
		configure(s)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func do(t *testing.T, method, url string, headers map[string]string, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// ---------- GET /v1/models ----------

func TestModelsEndpoint(t *testing.T) {
	ts := uiServer(t, nil,
		backend.NewMock("mock", 0),
		backend.NewVLLM("vllm", "http://x", "Qwen/Qwen2.5-0.5B-Instruct", 0),
		backend.NewVLLM("passthrough", "http://y", "", 0), // no fixed model: must not be listed
		&countingBackend{name: "plain"},                   // does not implement Modeled
	)
	resp := do(t, "GET", ts.URL+"/v1/models", nil, "")
	var out struct {
		Object string `json:"object"`
		Data   []struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			OwnedBy string `json:"owned_by"`
			Backend string `json:"backend"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || out.Object != "list" || len(out.Data) != 2 {
		t.Fatalf("status=%d body=%+v", resp.StatusCode, out)
	}
	if out.Data[0].ID != "mock" || out.Data[0].Backend != "mock" || out.Data[1].ID != "Qwen/Qwen2.5-0.5B-Instruct" ||
		out.Data[1].Backend != "vllm" || out.Data[1].Object != "model" || out.Data[1].OwnedBy != "infermesh" {
		t.Fatalf("unexpected entries: %+v", out.Data)
	}

	// No fixed models at all: an empty list (not null), so clients can iterate safely.
	ts2 := uiServer(t, nil, &countingBackend{name: "plain"})
	var raw map[string]json.RawMessage
	_ = json.NewDecoder(do(t, "GET", ts2.URL+"/v1/models", nil, "").Body).Decode(&raw)
	if string(raw["data"]) != "[]" {
		t.Fatalf("data = %s, want []", raw["data"])
	}
}

// ---------- backend pinning ----------

func servedBy2(resp *http.Response) string { return resp.Header.Get("X-InferMesh-Backend") }

func TestPinViaHeaderAndQueryRoutesOnlyToThatBackend(t *testing.T) {
	a, b := &countingBackend{name: "a"}, &countingBackend{name: "b"}
	ts := uiServer(t, nil, a, b)
	for i := 0; i < 3; i++ {
		if r := do(t, "POST", ts.URL+"/v1/chat/completions", map[string]string{pinHeader: "b"}, chatBody); r.StatusCode != 200 || servedBy2(r) != "b" {
			t.Fatalf("header pin: status=%d served by %q", r.StatusCode, servedBy2(r))
		}
		if r := do(t, "POST", ts.URL+"/v1/chat/completions?backend=b", nil, chatBody); r.StatusCode != 200 || servedBy2(r) != "b" {
			t.Fatalf("query pin: status=%d served by %q", r.StatusCode, servedBy2(r))
		}
	}
	if a.calls.Load() != 0 || b.calls.Load() != 6 {
		t.Fatalf("calls a=%d b=%d, want 0 and 6", a.calls.Load(), b.calls.Load())
	}
	// Streaming honours the pin too, and an unpinned request still round-robins.
	if r := do(t, "POST", ts.URL+"/v1/chat/completions", map[string]string{pinHeader: "a"}, streamBody); servedBy2(r) != "a" {
		t.Fatalf("streaming pin served by %q", servedBy2(r))
	}
}

func TestPinErrors(t *testing.T) {
	a, b := &countingBackend{name: "a"}, &countingBackend{name: "b"}
	ts := uiServer(t, nil, a, b)

	r := do(t, "POST", ts.URL+"/v1/chat/completions", map[string]string{pinHeader: "nope"}, chatBody)
	var e errorBody
	_ = json.NewDecoder(r.Body).Decode(&e)
	if r.StatusCode != http.StatusBadRequest || e.Error.Type != "invalid_request_error" || !strings.Contains(e.Error.Message, "a, b") {
		t.Fatalf("unknown pin: status=%d body=%+v", r.StatusCode, e)
	}

	// A pinned backend that fails is NOT retried on the other backend.
	bad := &countingBackend{name: "bad", err: &backend.HTTPError{Status: 503}}
	good := &countingBackend{name: "good"}
	ts2 := uiServer(t, func(s *Server) { s.MaxRetries = 3 }, bad, good)
	if r := do(t, "POST", ts2.URL+"/v1/chat/completions", map[string]string{pinHeader: "bad"}, chatBody); r.StatusCode != http.StatusBadGateway || good.calls.Load() != 0 {
		t.Fatalf("pinned failure fell over to another backend: status=%d good calls=%d", r.StatusCode, good.calls.Load())
	}
}

func TestPinnedUnhealthyBackendIsNotReplaced(t *testing.T) {
	m := backend.NewMock("m", 0)
	other := &countingBackend{name: "other"}
	pool := router.NewPool([]router.Member{{Backend: m, Weight: 1, MaxConcurrent: 10}, {Backend: other, Weight: 1, MaxConcurrent: 10}}, router.Options{})
	s := New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	m.SetHealthy(false)
	pool.CheckHealth(context.Background())
	if r := do(t, "POST", ts.URL+"/v1/chat/completions", map[string]string{pinHeader: "m"}, chatBody); r.StatusCode != http.StatusServiceUnavailable || other.calls.Load() != 0 {
		t.Fatalf("status=%d other calls=%d, want 503 and no fallback", r.StatusCode, other.calls.Load())
	}
	if r := do(t, "POST", ts.URL+"/v1/chat/completions", nil, chatBody); r.StatusCode != 200 {
		t.Fatalf("unpinned requests should still work: %d", r.StatusCode)
	}
}

func TestPinCanBeDisabled(t *testing.T) {
	ts := uiServer(t, func(s *Server) { s.AllowBackendPin = false }, &countingBackend{name: "a"}, &countingBackend{name: "b"})
	if r := do(t, "POST", ts.URL+"/v1/chat/completions", map[string]string{pinHeader: "a"}, chatBody); r.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 when pinning is disabled", r.StatusCode)
	}
	if r := do(t, "POST", ts.URL+"/v1/chat/completions", nil, chatBody); r.StatusCode != 200 {
		t.Fatalf("unpinned requests must still work: %d", r.StatusCode)
	}
}

func TestPinOverWebSocket(t *testing.T) {
	ts := uiServer(t, nil, &countingBackend{name: "a"}, &countingBackend{name: "b"})
	dial := func(query string) (*websocket.Conn, context.Context) {
		ctx, cancel := context.WithTimeout(context.Background(), 5e9)
		t.Cleanup(cancel)
		c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/v1/chat/stream"+query, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.CloseNow() })
		return c, ctx
	}
	req := map[string]any{"messages": []map[string]string{{"role": "user", "content": "hi"}}}

	c, ctx := dial("?backend=b")
	_ = wsjson.Write(ctx, c, req)
	var m wsMessage
	if err := wsjson.Read(ctx, c, &m); err != nil || m.Type != "start" || m.Backend != "b" {
		t.Fatalf("pinned start frame: %+v %v", m, err)
	}

	c2, ctx2 := dial("?backend=nope")
	_ = wsjson.Write(ctx2, c2, req)
	var e wsMessage
	if err := wsjson.Read(ctx2, c2, &e); err != nil || e.Type != "error" || !strings.Contains(e.Error, "unknown backend") {
		t.Fatalf("unknown pin frame: %+v %v", e, err)
	}
}

// ---------- CORS ----------

func corsServer(t *testing.T, origins ...string) *httptest.Server {
	return uiServer(t, func(s *Server) { s.CORSAllowedOrigins = origins }, backend.NewMock("mock", 0))
}

func TestCORSIsOffByDefault(t *testing.T) {
	ts := uiServer(t, nil, backend.NewMock("mock", 0))
	r := do(t, "GET", ts.URL+"/v1/backends", map[string]string{"Origin": "http://evil.example"}, "")
	if r.Header.Get("Access-Control-Allow-Origin") != "" || r.Header.Get("Vary") != "" {
		t.Fatalf("CORS headers must not be sent unless configured: %v", r.Header)
	}
}

func TestCORSAllowedOrigin(t *testing.T) {
	ts := corsServer(t, "http://localhost:5173")
	r := do(t, "POST", ts.URL+"/v1/chat/completions", map[string]string{"Origin": "http://localhost:5173"}, chatBody)
	h := r.Header
	if r.StatusCode != 200 || h.Get("Access-Control-Allow-Origin") != "http://localhost:5173" || !strings.Contains(h.Get("Vary"), "Origin") {
		t.Fatalf("status=%d headers=%v", r.StatusCode, h)
	}
	// The browser can only read these response headers if they are exposed.
	for _, want := range []string{"X-Request-ID", "X-InferMesh-Backend", "Retry-After"} {
		if !strings.Contains(h.Get("Access-Control-Expose-Headers"), want) {
			t.Errorf("%s not exposed: %q", want, h.Get("Access-Control-Expose-Headers"))
		}
	}
	// Origins match exactly: a different port or scheme is not allowed.
	for _, o := range []string{"http://localhost:5174", "https://localhost:5173", "http://localhost:5173.evil.example"} {
		if r := do(t, "GET", ts.URL+"/v1/backends", map[string]string{"Origin": o}, ""); r.Header.Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("origin %q must not be allowed", o)
		}
	}
	// A request without an Origin (curl, server-to-server) is untouched.
	if r := do(t, "GET", ts.URL+"/v1/backends", nil, ""); r.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Error("no Origin header: no CORS headers expected")
	}
}

func TestCORSPreflight(t *testing.T) {
	ts := corsServer(t, "http://localhost:5173")
	pre := map[string]string{
		"Origin": "http://localhost:5173", "Access-Control-Request-Method": "POST",
		"Access-Control-Request-Headers": "content-type, x-infermesh-pin-backend",
	}
	r := do(t, "OPTIONS", ts.URL+"/v1/chat/completions", pre, "")
	h := r.Header
	if r.StatusCode != http.StatusNoContent || h.Get("Access-Control-Allow-Origin") != "http://localhost:5173" ||
		!strings.Contains(h.Get("Access-Control-Allow-Methods"), "POST") ||
		!strings.Contains(strings.ToLower(h.Get("Access-Control-Allow-Headers")), "x-infermesh-pin-backend") ||
		!strings.Contains(strings.ToLower(h.Get("Access-Control-Allow-Headers")), "content-type") || h.Get("Access-Control-Max-Age") == "" {
		t.Fatalf("preflight: status=%d headers=%v", r.StatusCode, h)
	}
	// A preflight from a disallowed origin is rejected outright.
	pre["Origin"] = "http://evil.example"
	if r := do(t, "OPTIONS", ts.URL+"/v1/chat/completions", pre, ""); r.StatusCode != http.StatusForbidden || r.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("disallowed preflight: status=%d headers=%v", r.StatusCode, r.Header)
	}
}

func TestCORSWildcard(t *testing.T) {
	ts := corsServer(t, "*")
	r := do(t, "GET", ts.URL+"/v1/models", map[string]string{"Origin": "http://anything.example"}, "")
	if r.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("wildcard: %v", r.Header)
	}
}
