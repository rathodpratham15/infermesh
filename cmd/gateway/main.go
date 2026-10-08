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
	"github.com/rathodpratham15/infermesh/internal/gateway"
	"github.com/rathodpratham15/infermesh/internal/router"
)

func main() {
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

	var backends []backend.InferenceBackend
	for _, bc := range cfg.Backends {
		be, err := newBackend(bc)
		if err != nil {
			return err
		}
		backends = append(backends, be)
		log.Info("backend configured", "name", bc.Name, "kind", bc.Kind)
	}

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           gateway.New(router.NewFirst(backends), log).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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
