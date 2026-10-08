// Package router picks which backend serves a request.
package router

import (
	"context"
	"errors"

	"github.com/rathodpratham15/infermesh/internal/backend"
)

var ErrNoBackend = errors.New("no healthy backend available")

// Router selects a backend. Phase 1 uses First; round-robin,
// least-outstanding and weighted policies arrive in Phase 5.
type Router interface {
	Pick(ctx context.Context) (backend.InferenceBackend, error)
	Backends() []backend.InferenceBackend
}

// First routes every request to the first healthy backend, in config order.
type First struct {
	backends []backend.InferenceBackend
}

func NewFirst(b []backend.InferenceBackend) *First { return &First{backends: b} }

func (f *First) Backends() []backend.InferenceBackend { return f.backends }

func (f *First) Pick(ctx context.Context) (backend.InferenceBackend, error) {
	for _, b := range f.backends {
		if b.Health(ctx) == nil {
			return b, nil
		}
	}
	return nil, ErrNoBackend
}
