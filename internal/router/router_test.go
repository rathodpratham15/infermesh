package router

import (
	"context"
	"testing"

	"github.com/rathodpratham15/infermesh/internal/backend"
)

func TestFirstSkipsUnhealthy(t *testing.T) {
	a, b := newFake("a"), newFake("b")
	r := NewFirst([]backend.InferenceBackend{a, b})

	got, err := r.Pick(context.Background())
	if err != nil || got.Name() != "a" {
		t.Fatalf("got %v, %v", got, err)
	}
	a.SetHealthy(false)
	if got, _ = r.Pick(context.Background()); got.Name() != "b" {
		t.Fatalf("expected b, got %s", got.Name())
	}
	b.SetHealthy(false)
	if _, err = r.Pick(context.Background()); err != ErrNoBackend {
		t.Fatalf("expected ErrNoBackend, got %v", err)
	}
}
