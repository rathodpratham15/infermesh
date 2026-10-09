package backend

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"time"
)

// Mock is a deterministic in-process backend for tests and for running the
// gateway without any model. It makes no claims about real inference
// performance: latency is whatever TokenDelay is configured to.
type Mock struct {
	name       string
	TokenDelay time.Duration
	// Tokens is the fixed reply, emitted one word at a time.
	Tokens []string

	healthy atomic.Bool
}

func NewMock(name string, tokenDelay time.Duration) *Mock {
	m := &Mock{
		name:       name,
		TokenDelay: tokenDelay,
		Tokens:     strings.Fields("This is a mock response from the InferMesh development backend."),
	}
	m.healthy.Store(true)
	return m
}

func (m *Mock) Name() string { return m.name }

// Model reports the fixed name the mock advertises.
func (m *Mock) Model() string { return "mock" }

func (m *Mock) SetHealthy(v bool) { m.healthy.Store(v) }

func (m *Mock) Health(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !m.healthy.Load() {
		return errors.New("mock backend marked unhealthy")
	}
	return nil
}

func (m *Mock) Generate(ctx context.Context, req Request) (Response, error) {
	ch, err := m.Stream(ctx, req)
	if err != nil {
		return Response{}, err
	}
	var sb strings.Builder
	var resp Response
	for tok := range ch {
		if tok.Err != nil {
			return Response{}, tok.Err
		}
		sb.WriteString(tok.Text)
		if tok.FinishReason != "" {
			resp.FinishReason = tok.FinishReason
		}
		if tok.Usage != nil {
			resp.Usage = *tok.Usage
		}
	}
	// A cancelled context closes the channel without a deliverable error
	// token, so surface it here instead of returning a truncated reply.
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	resp.Content = sb.String()
	return resp, nil
}

func (m *Mock) Stream(ctx context.Context, req Request) (<-chan Token, error) {
	if err := m.Health(ctx); err != nil {
		return nil, err
	}
	words := m.Tokens
	finish := "stop"
	if req.MaxTokens > 0 && req.MaxTokens < len(words) {
		words = words[:req.MaxTokens]
		finish = "length"
	}
	prompt := 0
	for _, msg := range req.Messages {
		prompt += len(strings.Fields(msg.Content))
	}

	out := make(chan Token)
	go func() {
		defer close(out)
		send := func(t Token) bool {
			select {
			case out <- t:
				return true
			case <-ctx.Done():
				return false
			}
		}
		for i, w := range words {
			if m.TokenDelay > 0 {
				select {
				case <-time.After(m.TokenDelay):
				case <-ctx.Done():
					send(Token{Err: ctx.Err()})
					return
				}
			}
			text := w
			if i > 0 {
				text = " " + w
			}
			if !send(Token{Text: text}) {
				return
			}
		}
		send(Token{FinishReason: finish, Usage: &Usage{PromptTokens: prompt, CompletionTokens: len(words)}})
	}()
	return out, nil
}
