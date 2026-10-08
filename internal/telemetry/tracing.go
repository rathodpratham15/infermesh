// Package telemetry configures OpenTelemetry tracing for the gateway.
package telemetry

import (
	"context"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

// SetupTracing installs the W3C trace-context propagator (always) and, when an
// OTLP endpoint is configured via the standard OTEL_EXPORTER_OTLP_ENDPOINT or
// OTEL_EXPORTER_OTLP_TRACES_ENDPOINT variables, an OTLP/HTTP span exporter.
// Without an endpoint, tracing stays a no-op and costs nothing. The returned
// function flushes and stops the exporter.
func SetupTracing(ctx context.Context, version string) (shutdown func(context.Context) error, enabled bool, err error) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" && os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") == "" {
		return func(context.Context) error { return nil }, false, nil
	}
	exp, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, false, err
	}
	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(
		semconv.ServiceName("infermesh-gateway"), semconv.ServiceVersion(version)))
	if err != nil {
		return nil, false, err
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp), sdktrace.WithResource(res))
	otel.SetTracerProvider(tp)
	return tp.Shutdown, true, nil
}
