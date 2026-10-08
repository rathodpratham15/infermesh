// Package backend defines the contract between the gateway and inference
// engines (vLLM, Triton, ...). The gateway only ever sees this interface.
package backend

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
)

type Message struct {
	Role    string
	Content string
}

type Request struct {
	ID          string
	Model       string
	Messages    []Message
	MaxTokens   int
	Temperature *float64
}

type Usage struct {
	PromptTokens     int
	CompletionTokens int
}

type Response struct {
	Content      string
	FinishReason string
	Usage        Usage
}

// Token is one streamed chunk. Exactly one of Text/Err/FinishReason is
// meaningful per value; the final token carries FinishReason (and Usage when
// the engine reports it). The channel is closed after the last token.
type Token struct {
	Text         string
	FinishReason string
	Usage        *Usage
	Err          error
}

type InferenceBackend interface {
	Name() string
	Health(ctx context.Context) error
	Generate(ctx context.Context, req Request) (Response, error)
	Stream(ctx context.Context, req Request) (<-chan Token, error)
}

// Retryable reports whether a failed attempt is worth repeating on another
// backend: server-side failures (5xx, 429) and connection-level errors.
// Cancellations and timeouts are not retried (the client left or the budget is
// spent), and neither are 4xx responses, which another backend would repeat.
func Retryable(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var he *HTTPError
	if errors.As(err, &he) {
		return he.Status == http.StatusTooManyRequests || he.Status >= 500
	}
	var ne net.Error
	return errors.As(err, &ne) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF)
}
