// Command telemetry-consumer aggregates InferMesh Kafka events into
// Prometheus metrics.
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

	"github.com/rathodpratham15/infermesh/internal/config"
	"github.com/rathodpratham15/infermesh/internal/consumer"
	"github.com/rathodpratham15/infermesh/internal/metrics"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := run(); err != nil {
		slog.Error("telemetry-consumer exited", "err", err)
		os.Exit(1)
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// Environment: KAFKA_BROKERS (required), KAFKA_TOPIC, KAFKA_GROUP, METRICS_ADDR.
func run() error {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	brokers := config.SplitList(os.Getenv("KAFKA_BROKERS"))
	if len(brokers) == 0 {
		return errors.New("KAFKA_BROKERS is required")
	}
	topic := env("KAFKA_TOPIC", config.DefaultKafkaTopic)
	group := env("KAFKA_GROUP", "infermesh-telemetry")
	addr := env("METRICS_ADDR", ":9100")

	prov, err := metrics.NewProvider("infermesh-telemetry-consumer", version)
	if err != nil {
		return fmt.Errorf("metrics: %w", err)
	}
	agg, err := consumer.NewAggregator(prov.Meter)
	if err != nil {
		return fmt.Errorf("metrics: %w", err)
	}
	c, err := consumer.New(consumer.Config{Brokers: brokers, Topic: topic, Group: group}, agg, log)
	if err != nil {
		return fmt.Errorf("kafka: %w", err)
	}

	mux := http.NewServeMux()
	mux.Handle("GET /metrics", prov.Handler)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("metrics server", "err", err)
			stop()
		}
	}()
	log.Info("consuming", "brokers", brokers, "topic", topic, "group", group, "metrics", addr)

	runErr := c.Run(ctx)

	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutCtx)
	_ = prov.Shutdown(shutCtx)
	log.Info("shutdown complete")
	return runErr
}
