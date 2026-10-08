// Command gateway is the InferMesh inference gateway.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rathodpratham15/infermesh/internal/backend"
	"github.com/rathodpratham15/infermesh/internal/config"
	"github.com/rathodpratham15/infermesh/internal/events"
	"github.com/rathodpratham15/infermesh/internal/gateway"
	"github.com/rathodpratham15/infermesh/internal/health"
	"github.com/rathodpratham15/infermesh/internal/metrics"
	"github.com/rathodpratham15/infermesh/internal/router"
	"github.com/rathodpratham15/infermesh/internal/telemetry"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	// "gateway healthcheck" probes the local /healthz and exits 0/1. Container
	// images are distroless (no shell or curl), so Docker HEALTHCHECK uses this.
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		if err := health.Probe(os.Getenv("LISTEN_ADDR"), "8080"); err != nil {
			fmt.Fprintln(os.Stderr, "unhealthy:", err)
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		slog.Error("gateway exited", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	log := newLogger(cfg.LogLevel)

	policy, err := router.ParsePolicy(cfg.RoutingPolicy)
	if err != nil {
		return err
	}
	var members []router.Member
	for _, bc := range cfg.Backends {
		be, err := newBackend(bc)
		if err != nil {
			return err
		}
		members = append(members, router.Member{Backend: be, Weight: bc.Weight, MaxConcurrent: bc.MaxConcurrent})
		log.Info("backend configured", "name", bc.Name, "kind", bc.Kind, "weight", bc.Weight, "max_concurrent", bc.MaxConcurrent)
	}
	m, err := metrics.New(version)
	if err != nil {
		return fmt.Errorf("metrics: %w", err)
	}
	pool := router.NewPool(members, router.Options{Policy: policy, MaxQueue: cfg.MaxQueue, Log: log, Observer: m})
	if err := m.RegisterPool(pool); err != nil {
		return fmt.Errorf("metrics: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	shutdownTracing, tracing, err := telemetry.SetupTracing(ctx, version)
	if err != nil {
		return fmt.Errorf("tracing: %w", err)
	}
	log.Info("telemetry configured", "prometheus", "/metrics", "otlp_tracing", tracing)
	pool.CheckHealth(ctx)
	go pool.RunHealthChecks(ctx, cfg.HealthInterval)

	gw := gateway.New(pool, log)
	gw.AllowedOrigins = cfg.AllowedOrigins
	gw.MaxRetries = cfg.MaxRetries
	gw.RequestTimeout = cfg.RequestTimeout
	gw.Metrics = m
	if len(cfg.KafkaBrokers) > 0 {
		pub, err := events.NewKafka(events.KafkaConfig{Brokers: cfg.KafkaBrokers, Topic: cfg.KafkaTopic, Reporter: m})
		if err != nil {
			return fmt.Errorf("kafka: %w", err)
		}
		gw.Events = pub
		log.Info("telemetry events enabled", "brokers", cfg.KafkaBrokers, "topic", cfg.KafkaTopic)
	} else {
		log.Info("telemetry events disabled (KAFKA_BROKERS not set)")
	}
	log.Info("routing configured", "policy", cfg.RoutingPolicy, "max_retries", cfg.MaxRetries, "max_queue", cfg.MaxQueue)
	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           gw.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("gateway listening", "addr", cfg.ListenAddr)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	log.Info("shutting down", "timeout", cfg.ShutdownTimeout.String())
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("shutdown: %w", err)
	}
	flushCtx, flushCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer flushCancel()
	if err := gw.Events.Close(flushCtx); err != nil {
		log.Warn("flushing telemetry events", "err", err)
	}
	if err := shutdownTracing(flushCtx); err != nil {
		log.Warn("flushing traces", "err", err)
	}
	if err := m.Shutdown(flushCtx); err != nil {
		log.Warn("stopping metrics", "err", err)
	}
	log.Info("shutdown complete")
	return nil
}

func newLogger(level string) *slog.Logger {
	var l slog.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		l = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: l}))
}

// factories maps a backend kind to its constructor. Real engines (vLLM,
// Triton) are registered in later phases.
var factories = map[string]func(config.Backend) (backend.InferenceBackend, error){
	"vllm": func(bc config.Backend) (backend.InferenceBackend, error) {
		return backend.NewVLLM(bc.Name, bc.URL, bc.Model, bc.Timeout), nil
	},
	"mock": func(bc config.Backend) (backend.InferenceBackend, error) {
		return backend.NewMock(bc.Name, 20*time.Millisecond), nil
	},
}

func newBackend(bc config.Backend) (backend.InferenceBackend, error) {
	f, ok := factories[bc.Kind]
	if !ok {
		return nil, fmt.Errorf("backend %q: kind %q not implemented yet", bc.Name, bc.Kind)
	}
	return f(bc)
}
