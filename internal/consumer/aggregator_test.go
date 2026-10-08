package consumer

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rathodpratham15/infermesh/internal/events"
	"github.com/rathodpratham15/infermesh/internal/metrics"
)

func newAgg(t *testing.T) (*Aggregator, func() string) {
	t.Helper()
	p, err := metrics.NewProvider("test-consumer", "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	a, err := NewAggregator(p.Meter)
	if err != nil {
		t.Fatal(err)
	}
	return a, func() string {
		rec := httptest.NewRecorder()
		p.Handler.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
		b, _ := io.ReadAll(rec.Body)
		return string(b)
	}
}

func want(t *testing.T, text string, lines ...string) {
	t.Helper()
	for _, l := range lines {
		if !strings.Contains(text, l+"\n") {
			t.Errorf("missing %q in:\n%s", l, text)
		}
	}
}

func TestAggregatorHandle(t *testing.T) {
	a, scrape := newAgg(t)
	ctx := context.Background()
	ttft := int64(200)
	a.Handle(ctx, events.Event{Type: events.TypeStarted, RequestID: "1", Backend: "vllm"})
	a.Handle(ctx, events.Event{Type: events.TypeCompleted, RequestID: "1", Backend: "vllm", Status: "success",
		InputTokens: 100, OutputTokens: 50, QueueMS: 10, TTFTMS: &ttft, GenerationMS: 1000, Attempt: 2})
	a.Handle(ctx, events.Event{Type: events.TypeCompleted, RequestID: "2", Backend: "vllm", Status: "success",
		InputTokens: 20, OutputTokens: 5, GenerationMS: 100, Attempt: 1})
	a.Handle(ctx, events.Event{Type: events.TypeFailed, RequestID: "3", Backend: "triton", Status: "timeout"})
	a.Invalid(ctx, "decode")

	m := scrape()
	want(t, m,
		`infermesh_telemetry_events_total{backend="vllm",event_type="InferenceStarted",status=""} 1`,
		`infermesh_telemetry_events_total{backend="vllm",event_type="InferenceCompleted",status="success"} 2`,
		`infermesh_telemetry_events_total{backend="triton",event_type="InferenceFailed",status="timeout"} 1`,
		`infermesh_telemetry_events_invalid_total{reason="decode"} 1`,
		`infermesh_telemetry_tokens_input_total{backend="vllm"} 120`,
		`infermesh_telemetry_tokens_output_total{backend="vllm"} 55`,
		`infermesh_telemetry_retried_requests_total{backend="vllm"} 1`,
		`infermesh_telemetry_ttft_seconds_count{backend="vllm"} 1`,
		`infermesh_telemetry_generation_duration_seconds_count{backend="vllm"} 2`,
		`infermesh_telemetry_queue_duration_seconds_count{backend="vllm"} 2`,
	)
	// Failed events must not contribute token or latency data.
	if strings.Contains(m, `backend="triton"} `) && strings.Contains(m, `tokens_input_total{backend="triton"`) {
		t.Error("failed event leaked into token counters")
	}
}
