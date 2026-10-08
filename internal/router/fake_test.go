package router

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/rathodpratham15/infermesh/internal/backend"
)

// fakeBackend is a minimal test double; a configurable mock backend arrives in
// the next phase.
type fakeBackend struct {
	name    string
	healthy atomic.Bool
}

func newFake(name string) *fakeBackend {
	f := &fakeBackend{name: name}
	f.healthy.Store(true)
	return f
}

func (f *fakeBackend) Name() string      { return f.name }
func (f *fakeBackend) SetHealthy(v bool) { f.healthy.Store(v) }
func (f *fakeBackend) Health(context.Context) error {
	if !f.healthy.Load() {
		return errors.New("unhealthy")
	}
	return nil
}
func (f *fakeBackend) Generate(context.Context, backend.Request) (backend.Response, error) {
	return backend.Response{}, nil
}
func (f *fakeBackend) Stream(context.Context, backend.Request) (<-chan backend.Token, error) {
	return nil, errors.New("not implemented")
}
