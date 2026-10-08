package gateway

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/rathodpratham15/infermesh/internal/backend"
	"github.com/rathodpratham15/infermesh/internal/metrics"
	"github.com/rathodpratham15/infermesh/internal/router"
)

func instrumentedServer(t *testing.T, retries int, bs ...backend.InferenceBackend) *httptest.Server {
	t.Helper()
	m, err := metrics.New("test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })
	var ms []router.Member
	for _, b := range bs {
		ms = append(ms, router.Member{Backend: b, Weight: 1, MaxConcurrent: 10})
	}
	pool := router.NewPool(ms, router.Options{Policy: router.RoundRobin, Observer: m})
	if err := m.RegisterPool(pool); err != nil {
		t.Fatal(err)
	}
	s := New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.Metrics = m
	s.MaxRetries = retries
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func scrape(t *testing.T, ts *httptest.Server) string {
	t.Helper()
	resp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func wantSeries(t *testing.T, metricsText string, lines ...string) {
	t.Helper()
	for _, l := range lines {
		if !strings.Contains(metricsText, l+"\n") {
			t.Errorf("missing series %q in /metrics output:\n%s", l, filterInfermesh(metricsText))
		}
	}
}

func filterInfermesh(s string) string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, "infermesh_") && !strings.Contains(l, "_bucket") {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

func TestMetricsEndpoint(t *testing.T) {
	bad := &countingBackend{name: "bad", err: &backend.HTTPError{Status: 503}}
	good := backend.NewMock("good", 0)
	ts := instrumentedServer(t, 1, bad, good)

	post(t, ts.URL, chatBody)          // bad (503) -> retried on good
	post(t, ts.URL, streamBody)        // good, streamed
	post(t, ts.URL, `{"messages":[]}`) // 400
	mockTokens := len(backend.NewMock("x", 0).Tokens)

	m := scrape(t, ts)
	wantSeries(t, m,
		`infermesh_requests_total{code="200",route="chat"} 2`,
		`infermesh_requests_total{code="400",route="chat"} 1`,
		`infermesh_request_errors_total{code="400",route="chat"} 1`,
		`infermesh_backend_failures_total{backend="bad",reason="http_5xx"} 2`,
		`infermesh_routing_decisions_total{backend="bad",policy="round_robin"} 2`,
		`infermesh_routing_decisions_total{backend="good",policy="round_robin"} 2`,
		fmt.Sprintf(`infermesh_tokens_generated_total{backend="good"} %d`, 2*mockTokens),
		`infermesh_ttft_seconds_count{backend="good"} 1`,
		`infermesh_generation_rate_per_second_count{backend="good"} 2`,
		`infermesh_backend_duration_seconds_count{backend="good"} 2`,
		`infermesh_requests_active{route="chat"} 0`,
		`infermesh_queue_depth 0`,
		`infermesh_backend_active{backend="good"} 0`,
		`infermesh_backend_healthy{backend="bad"} 1`,
	)
	for _, name := range []string{"infermesh_request_duration_seconds_bucket", "go_goroutines", "process_resident_memory_bytes"} {
		if !strings.Contains(m, name) {
			t.Errorf("missing metric family %s", name)
		}
	}
}

func TestMetricsCountStreamAbort(t *testing.T) {
	flaky := scriptBackend{stream: func(context.Context, backend.Request) (<-chan backend.Token, error) {
		ch := make(chan backend.Token, 2)
		ch <- backend.Token{Text: "partial"}
		ch <- backend.Token{Err: io.ErrUnexpectedEOF}
		close(ch)
		return ch, nil
	}}
	ts := instrumentedServer(t, 0, flaky)
	post(t, ts.URL, streamBody)
	m := scrape(t, ts)
	wantSeries(t, m,
		`infermesh_request_errors_total{code="stream_aborted",route="chat"} 1`,
		`infermesh_requests_total{code="200",route="chat"} 1`,
	)
}

func TestTracingSpansAndPropagation(t *testing.T) {
	rec := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(rec)))
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTracerProvider(noop.NewTracerProvider()) })

	ts := serverFor(t, backend.NewMock("mock", 0))
	const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	req, _ := http.NewRequest("POST", ts.URL+"/v1/chat/completions", strings.NewReader(chatBody))
	req.Header.Set("traceparent", "00-"+traceID+"-00f067aa0ba902b7-01")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	spans := rec.GetSpans()
	byName := map[string]tracetest.SpanStub{}
	for _, s := range spans {
		byName[s.Name] = s
	}
	root, ok1 := byName["chat.completions"]
	attempt, ok2 := byName["backend.attempt"]
	if !ok1 || !ok2 {
		t.Fatalf("expected chat.completions and backend.attempt spans, got %d spans: %+v", len(spans), spans)
	}
	if root.SpanContext.TraceID().String() != traceID {
		t.Fatalf("trace id not continued from traceparent: %s", root.SpanContext.TraceID())
	}
	if attempt.Parent.SpanID() != root.SpanContext.SpanID() {
		t.Fatal("backend.attempt is not a child of chat.completions")
	}
}
