// Package metrics defines InferMesh's OpenTelemetry instruments and exposes
// them in Prometheus format.
//
// Instruments are created through the OpenTelemetry metrics API; the OTel
// Prometheus exporter renders them at /metrics. Only measurements the gateway
// actually makes are exported: there are no GPU metrics here.
package metrics

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel/attribute"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"

	"github.com/rathodpratham15/infermesh/internal/backend"
	"github.com/rathodpratham15/infermesh/internal/events"
	"github.com/rathodpratham15/infermesh/internal/router"
)

const meterName = "github.com/rathodpratham15/infermesh"

// Latency buckets in seconds: fine at the low end for TTFT and queueing, long
// enough for multi-second LLM generations.
var latencyBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120}

type Metrics struct {
	handler http.Handler
	meter   metric.Meter

	requests      metric.Int64Counter
	errors        metric.Int64Counter
	active        metric.Int64UpDownCounter
	reqDuration   metric.Float64Histogram
	backendDur    metric.Float64Histogram
	ttft          metric.Float64Histogram
	tokensOut     metric.Int64Counter
	tokensIn      metric.Int64Counter
	tokensPerSec  metric.Float64Histogram
	backendFails  metric.Int64Counter
	routing       metric.Int64Counter
	eventsOut     metric.Int64Counter
	shutdownFuncs []func(context.Context) error
}

// Provider is an OTel meter backed by a private Prometheus registry.
type Provider struct {
	Meter    metric.Meter
	Handler  http.Handler
	Shutdown func(context.Context) error
}

// NewProvider builds a meter provider for the named service whose metrics are
// served in Prometheus format by Handler (Go runtime and process collectors
// included). Histograms use latencyBuckets unless an instrument overrides them.
func NewProvider(service, version string) (*Provider, error) {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	exp, err := otelprom.New(otelprom.WithRegisterer(reg), otelprom.WithoutScopeInfo())
	if err != nil {
		return nil, err
	}
	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(
		semconv.ServiceName(service), semconv.ServiceVersion(version)))
	if err != nil {
		return nil, err
	}
	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(exp),
		sdkmetric.WithResource(res),
		sdkmetric.WithView(sdkmetric.NewView(
			sdkmetric.Instrument{Kind: sdkmetric.InstrumentKindHistogram},
			sdkmetric.Stream{Aggregation: sdkmetric.AggregationExplicitBucketHistogram{Boundaries: latencyBuckets}},
		)),
	)
	return &Provider{
		Meter:    provider.Meter(meterName),
		Handler:  promhttp.HandlerFor(reg, promhttp.HandlerOpts{}),
		Shutdown: provider.Shutdown,
	}, nil
}

// New builds the gateway's Metrics backed by a real OTel SDK.
func New(version string) (*Metrics, error) {
	p, err := NewProvider("infermesh-gateway", version)
	if err != nil {
		return nil, err
	}
	m, err := newMetrics(p.Meter)
	if err != nil {
		return nil, err
	}
	m.handler = p.Handler
	m.shutdownFuncs = append(m.shutdownFuncs, p.Shutdown)
	return m, nil
}

func newMetrics(meter metric.Meter) (*Metrics, error) {
	m := &Metrics{meter: meter}
	var err error
	var errs []error
	chk := func(e error) {
		if e != nil {
			errs = append(errs, e)
		}
	}
	m.requests, err = meter.Int64Counter("infermesh.requests", metric.WithUnit("{request}"),
		metric.WithDescription("Requests handled by the gateway."))
	chk(err)
	m.errors, err = meter.Int64Counter("infermesh.request.errors", metric.WithUnit("{request}"),
		metric.WithDescription("Requests that ended in an error (HTTP status >= 400)."))
	chk(err)
	m.active, err = meter.Int64UpDownCounter("infermesh.requests.active", metric.WithUnit("{request}"),
		metric.WithDescription("Requests currently in flight in the gateway."))
	chk(err)
	m.reqDuration, err = meter.Float64Histogram("infermesh.request.duration", metric.WithUnit("s"),
		metric.WithDescription("End-to-end gateway request latency, including queueing and streaming."))
	chk(err)
	m.backendDur, err = meter.Float64Histogram("infermesh.backend.duration", metric.WithUnit("s"),
		metric.WithDescription("Time a request held a backend slot (whole stream for streaming requests)."))
	chk(err)
	m.ttft, err = meter.Float64Histogram("infermesh.ttft", metric.WithUnit("s"),
		metric.WithDescription("Time to first token for streaming requests, measured at the gateway."))
	chk(err)
	m.tokensOut, err = meter.Int64Counter("infermesh.tokens.generated", metric.WithUnit("{token}"),
		metric.WithDescription("Completion tokens reported by backends."))
	chk(err)
	m.tokensIn, err = meter.Int64Counter("infermesh.tokens.prompt", metric.WithUnit("{token}"),
		metric.WithDescription("Prompt tokens reported by backends."))
	chk(err)
	m.tokensPerSec, err = meter.Float64Histogram("infermesh.generation.rate", metric.WithUnit("{token}/s"),
		metric.WithDescription("Per-request decode rate: completion tokens / generation time."),
		metric.WithExplicitBucketBoundaries(1, 2, 5, 10, 20, 30, 50, 75, 100, 150, 200, 300, 500, 1000))
	chk(err)
	m.backendFails, err = meter.Int64Counter("infermesh.backend.failures", metric.WithUnit("{request}"),
		metric.WithDescription("Backend attempts that failed."))
	chk(err)
	m.routing, err = meter.Int64Counter("infermesh.routing.decisions", metric.WithUnit("{request}"),
		metric.WithDescription("Routing decisions by chosen backend and policy."))
	chk(err)
	m.eventsOut, err = meter.Int64Counter("infermesh.events.published", metric.WithUnit("{event}"),
		metric.WithDescription("Telemetry events handed to Kafka, by delivery result (ok, error, dropped)."))
	chk(err)
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return m, nil
}

// Handler serves the Prometheus exposition format. It is nil for a no-op Metrics.
func (m *Metrics) Handler() http.Handler { return m.handler }

// Shutdown flushes and stops the meter provider.
func (m *Metrics) Shutdown(ctx context.Context) error {
	var errs []error
	for _, f := range m.shutdownFuncs {
		errs = append(errs, f(ctx))
	}
	return errors.Join(errs...)
}

// InFlight adjusts the gateway-level active request gauge.
func (m *Metrics) InFlight(ctx context.Context, delta int64, route string) {
	m.active.Add(ctx, delta, metric.WithAttributes(attribute.String("route", route)))
}

// Request records a finished gateway request.
func (m *Metrics) Request(ctx context.Context, route string, status int, d time.Duration) {
	attrs := metric.WithAttributes(attribute.String("route", route), attribute.String("code", strconv.Itoa(status)))
	m.requests.Add(ctx, 1, attrs)
	m.reqDuration.Record(ctx, d.Seconds(), attrs)
	if status >= 400 {
		m.errors.Add(ctx, 1, attrs)
	}
}

// StreamAborted records a streaming request that failed after the response had
// already started, so the HTTP status could not carry the error.
func (m *Metrics) StreamAborted(ctx context.Context, route string) {
	m.errors.Add(ctx, 1, metric.WithAttributes(attribute.String("route", route), attribute.String("code", "stream_aborted")))
}

// Generation records token-level measurements for one completed generation.
// ttft is nil when the request was not streamed (TTFT is not observable then).
func (m *Metrics) Generation(ctx context.Context, backendName string, ttft *time.Duration, total time.Duration, u backend.Usage) {
	attrs := metric.WithAttributes(attribute.String("backend", backendName))
	m.tokensIn.Add(ctx, int64(u.PromptTokens), attrs)
	m.tokensOut.Add(ctx, int64(u.CompletionTokens), attrs)
	gen := total
	if ttft != nil {
		m.ttft.Record(ctx, ttft.Seconds(), attrs)
		gen = total - *ttft
	}
	if u.CompletionTokens > 0 && gen > 0 {
		m.tokensPerSec.Record(ctx, float64(u.CompletionTokens)/gen.Seconds(), attrs)
	}
}

// Picked implements router.Observer.
func (m *Metrics) Picked(backendName string, policy router.Policy) {
	m.routing.Add(context.Background(), 1, metric.WithAttributes(
		attribute.String("backend", backendName), attribute.String("policy", string(policy))))
}

// Released implements router.Observer.
func (m *Metrics) Released(backendName string, d time.Duration, err error) {
	ctx := context.Background()
	attrs := metric.WithAttributes(attribute.String("backend", backendName))
	if err != nil && !errors.Is(err, context.Canceled) {
		m.backendFails.Add(ctx, 1, metric.WithAttributes(
			attribute.String("backend", backendName), attribute.String("reason", failureReason(err))))
		return
	}
	if err == nil {
		m.backendDur.Record(ctx, d.Seconds(), attrs)
	}
}

func failureReason(err error) string {
	var he *backend.HTTPError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.As(err, &he):
		if he.Status == http.StatusTooManyRequests {
			return "rate_limited"
		}
		if he.Status >= 500 {
			return "http_5xx"
		}
		return "http_4xx"
	case backend.Retryable(err):
		return "connection"
	}
	return "other"
}

// RegisterPool exposes the router's live state (active requests per backend,
// health, queue depth) as observable gauges.
func (m *Metrics) RegisterPool(r router.Router) error {
	active, err := m.meter.Int64ObservableGauge("infermesh.backend.active", metric.WithUnit("{request}"),
		metric.WithDescription("Requests currently holding a slot on each backend."))
	if err != nil {
		return err
	}
	healthy, err := m.meter.Int64ObservableGauge("infermesh.backend.healthy",
		metric.WithDescription("1 if the last health probe of the backend succeeded, else 0."))
	if err != nil {
		return err
	}
	queue, err := m.meter.Int64ObservableGauge("infermesh.queue.depth", metric.WithUnit("{request}"),
		metric.WithDescription("Requests waiting for a free backend slot."))
	if err != nil {
		return err
	}
	_, err = m.meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		for _, s := range r.Stats() {
			a := metric.WithAttributes(attribute.String("backend", s.Name))
			o.ObserveInt64(active, int64(s.Active), a)
			h := int64(0)
			if s.Healthy {
				h = 1
			}
			o.ObserveInt64(healthy, h, a)
		}
		o.ObserveInt64(queue, int64(r.QueueDepth()))
		return nil
	}, active, healthy, queue)
	return err
}

// Noop returns a Metrics that records nothing, for tests and for callers that
// do not want instrumentation.
func Noop() *Metrics {
	m, err := newMetrics(noop.NewMeterProvider().Meter(meterName))
	if err != nil {
		panic(err) // the no-op meter never fails
	}
	m.handler = http.NotFoundHandler()
	return m
}

// EventPublished implements events.Reporter. A full producer buffer is reported
// as "dropped"; any other failure as "error".
func (m *Metrics) EventPublished(eventType string, err error) {
	result := "ok"
	switch {
	case errors.Is(err, events.ErrDropped):
		result = "dropped"
	case err != nil:
		result = "error"
	}
	m.eventsOut.Add(context.Background(), 1, metric.WithAttributes(
		attribute.String("event_type", eventType), attribute.String("result", result)))
}
