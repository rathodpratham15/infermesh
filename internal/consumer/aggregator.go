// Package consumer reads InferMesh telemetry events from Kafka and aggregates
// them into Prometheus metrics. It is independent of the gateway process: the
// gateway only ever publishes, so a slow or crashed consumer cannot affect
// inference requests.
package consumer

import (
	"context"
	"errors"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/rathodpratham15/infermesh/internal/events"
)

// Aggregator turns events into metrics. It is safe for concurrent use.
type Aggregator struct {
	events     metric.Int64Counter
	invalid    metric.Int64Counter
	tokensIn   metric.Int64Counter
	tokensOut  metric.Int64Counter
	queue      metric.Float64Histogram
	ttft       metric.Float64Histogram
	generation metric.Float64Histogram
	attempts   metric.Int64Counter
}

func NewAggregator(meter metric.Meter) (*Aggregator, error) {
	a := &Aggregator{}
	var errs []error
	var err error
	chk := func() {
		if err != nil {
			errs = append(errs, err)
		}
	}
	a.events, err = meter.Int64Counter("infermesh.telemetry.events", metric.WithUnit("{event}"),
		metric.WithDescription("Telemetry events consumed from Kafka."))
	chk()
	a.invalid, err = meter.Int64Counter("infermesh.telemetry.events.invalid", metric.WithUnit("{event}"),
		metric.WithDescription("Records that could not be decoded or validated and were skipped."))
	chk()
	a.tokensIn, err = meter.Int64Counter("infermesh.telemetry.tokens.input", metric.WithUnit("{token}"),
		metric.WithDescription("Prompt tokens in completed inferences."))
	chk()
	a.tokensOut, err = meter.Int64Counter("infermesh.telemetry.tokens.output", metric.WithUnit("{token}"),
		metric.WithDescription("Completion tokens in completed inferences."))
	chk()
	a.queue, err = meter.Float64Histogram("infermesh.telemetry.queue.duration", metric.WithUnit("s"),
		metric.WithDescription("Time requests waited for a backend slot (completed inferences)."))
	chk()
	a.ttft, err = meter.Float64Histogram("infermesh.telemetry.ttft", metric.WithUnit("s"),
		metric.WithDescription("Time to first token (completed streaming inferences)."))
	chk()
	a.generation, err = meter.Float64Histogram("infermesh.telemetry.generation.duration", metric.WithUnit("s"),
		metric.WithDescription("Generation time (after first token when streaming) of completed inferences."))
	chk()
	a.attempts, err = meter.Int64Counter("infermesh.telemetry.retried.requests", metric.WithUnit("{request}"),
		metric.WithDescription("Completed inferences that needed more than one backend attempt."))
	chk()
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return a, nil
}

// Invalid records a record that was skipped, with a short reason label.
func (a *Aggregator) Invalid(ctx context.Context, reason string) {
	a.invalid.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", reason)))
}

// Handle aggregates one event.
func (a *Aggregator) Handle(ctx context.Context, e events.Event) {
	a.events.Add(ctx, 1, metric.WithAttributes(
		attribute.String("event_type", e.Type),
		attribute.String("backend", e.Backend),
		attribute.String("status", e.Status)))

	if e.Type != events.TypeCompleted {
		return
	}
	b := metric.WithAttributes(attribute.String("backend", e.Backend))
	a.tokensIn.Add(ctx, int64(e.InputTokens), b)
	a.tokensOut.Add(ctx, int64(e.OutputTokens), b)
	a.queue.Record(ctx, ms(e.QueueMS), b)
	a.generation.Record(ctx, ms(e.GenerationMS), b)
	if e.TTFTMS != nil {
		a.ttft.Record(ctx, ms(*e.TTFTMS), b)
	}
	if e.Attempt > 1 {
		a.attempts.Add(ctx, 1, b)
	}
}

func ms(v int64) float64 { return (time.Duration(v) * time.Millisecond).Seconds() }
