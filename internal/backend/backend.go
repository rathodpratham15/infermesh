// Package backend defines the contract between the gateway and inference
// engines (vLLM, Triton, ...). The gateway only ever sees this interface.
package backend

import "context"

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
