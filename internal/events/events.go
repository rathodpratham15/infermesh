// Package events defines the asynchronous telemetry events the gateway emits
// to Kafka. Events are strictly out of band: publishing never blocks, and a
// Kafka outage can only lose events, never slow or fail an inference request.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

const (
	TypeStarted   = "InferenceStarted"
	TypeCompleted = "InferenceCompleted"
	TypeFailed    = "InferenceFailed"
)

// Status values carried by completed and failed events.
const (
	StatusSuccess  = "success"
	StatusError    = "error"    // backend or gateway failure
	StatusTimeout  = "timeout"  // request budget or backend timeout exceeded
	StatusRejected = "rejected" // shed before reaching a backend (queue full, no healthy backend)
	StatusCanceled = "canceled" // the client went away
)

// Event is the wire format (JSON) of every telemetry event.
//
// QueueMS is the time the request spent waiting for a backend slot. For
// streaming requests GenerationMS is the time after the first token; for
// non-streaming requests it is the whole backend call. TTFTMS is only set for
// streaming requests.
type Event struct {
	Type         string    `json:"event_type"`
	RequestID    string    `json:"request_id"`
	Timestamp    time.Time `json:"timestamp"`
	Backend      string    `json:"backend,omitempty"`
	Model        string    `json:"model,omitempty"`
	Stream       bool      `json:"stream"`
	Attempt      int       `json:"attempt,omitempty"`
	QueueMS      int64     `json:"queue_ms"`
	TTFTMS       *int64    `json:"ttft_ms,omitempty"`
	InputTokens  int       `json:"input_tokens"`
	OutputTokens int       `json:"output_tokens"`
	GenerationMS int64     `json:"generation_ms"`
	Status       string    `json:"status,omitempty"`
	Error        string    `json:"error,omitempty"`
}

// Decode parses and validates an event. Events with an unknown type or no
// request ID are rejected so consumers can count and skip them.
func Decode(b []byte) (Event, error) {
	var e Event
	if err := json.Unmarshal(b, &e); err != nil {
		return Event{}, fmt.Errorf("decode event: %w", err)
	}
	switch e.Type {
	case TypeStarted, TypeCompleted, TypeFailed:
	default:
		return Event{}, fmt.Errorf("unknown event type %q", e.Type)
	}
	if e.RequestID == "" {
		return Event{}, fmt.Errorf("event has no request_id")
	}
	return e, nil
}

// Publisher accepts events without blocking the caller.
type Publisher interface {
	Publish(Event)
	// Close flushes what it can within the context and stops the publisher.
	Close(ctx context.Context) error
}

// Noop discards events. It is the default when Kafka is not configured.
type Noop struct{}

func (Noop) Publish(Event)               {}
func (Noop) Close(context.Context) error { return nil }
