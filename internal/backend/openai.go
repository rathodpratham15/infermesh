package backend

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"io"
	"net/http"
	"strings"
	"time"
)

// HTTPError is returned when an engine answers with a non-2xx status. Retry
// logic can use Status to tell retryable failures (5xx, 429) from client errors.
type HTTPError struct {
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("backend returned HTTP %d: %s", e.Status, e.Body)
}

// openAIBackend talks to any inference server that exposes the OpenAI chat
// completions API: vLLM, Triton's OpenAI-compatible frontend, and stand-ins such
// as mlx_lm.server or llama.cpp used for local development. The engines differ
// only in how health is probed and in whether they accept stream_options, so
// those are the two knobs; VLLM and Triton are thin wrappers that set them.
type openAIBackend struct {
	name        string
	baseURL     string
	model       string
	timeout     time.Duration
	healthPath  string
	streamUsage bool // send stream_options.include_usage on streaming requests
	client      *http.Client
}

func newOpenAIBackend(name, baseURL, model string, timeout time.Duration, healthPath string, streamUsage bool) *openAIBackend {
	return &openAIBackend{
		name:        name,
		baseURL:     strings.TrimRight(baseURL, "/"),
		model:       model,
		timeout:     timeout,
		healthPath:  healthPath,
		streamUsage: streamUsage,
		client:      &http.Client{},
	}
}

func (v *openAIBackend) Name() string { return v.name }

// Health probes the engine's readiness endpoint with a short timeout.
func (v *openAIBackend) Health(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, minDuration(v.timeout, 5*time.Second))
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.baseURL+v.healthPath, nil)
	if err != nil {
		return err
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return &HTTPError{Status: resp.StatusCode}
	}
	return nil
}

type oaiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type oaiStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type oaiRequest struct {
	Model         string            `json:"model"`
	Messages      []oaiMessage      `json:"messages"`
	MaxTokens     int               `json:"max_tokens,omitempty"`
	Temperature   *float64          `json:"temperature,omitempty"`
	Stream        bool              `json:"stream"`
	StreamOptions *oaiStreamOptions `json:"stream_options,omitempty"`
}

type oaiUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

type oaiChoice struct {
	FinishReason *string `json:"finish_reason"`
	Message      struct {
		Content string `json:"content"`
	} `json:"message"`
	Delta struct {
		Content string `json:"content"`
	} `json:"delta"`
}

type oaiResponse struct {
	Choices []oaiChoice `json:"choices"`
	Usage   *oaiUsage   `json:"usage"`
}

func (v *openAIBackend) newRequest(ctx context.Context, req Request, stream bool) (*http.Request, error) {
	model := v.model
	if model == "" {
		model = req.Model
	}
	body := oaiRequest{
		Model:       model,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		Stream:      stream,
	}
	for _, m := range req.Messages {
		body.Messages = append(body.Messages, oaiMessage(m))
	}
	if stream && v.streamUsage {
		body.StreamOptions = &oaiStreamOptions{IncludeUsage: true}
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, v.baseURL+"/v1/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	hr.Header.Set("Content-Type", "application/json")
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(hr.Header))
	if req.ID != "" {
		hr.Header.Set("X-Request-ID", req.ID)
	}
	return hr, nil
}

func (v *openAIBackend) do(ctx context.Context, req Request, stream bool) (*http.Response, error) {
	hr, err := v.newRequest(ctx, req, stream)
	if err != nil {
		return nil, err
	}
	resp, err := v.client.Do(hr)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, &HTTPError{Status: resp.StatusCode, Body: strings.TrimSpace(string(b))}
	}
	return resp, nil
}

func (v *openAIBackend) Generate(ctx context.Context, req Request) (Response, error) {
	ctx, cancel := context.WithTimeout(ctx, v.timeout)
	defer cancel()
	resp, err := v.do(ctx, req, false)
	if err != nil {
		return Response{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	var out oaiResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Response{}, fmt.Errorf("decode response: %w", err)
	}
	if len(out.Choices) == 0 {
		return Response{}, errors.New("backend response has no choices")
	}
	r := Response{Content: out.Choices[0].Message.Content}
	if fr := out.Choices[0].FinishReason; fr != nil {
		r.FinishReason = *fr
	}
	if out.Usage != nil {
		r.Usage = Usage{PromptTokens: out.Usage.PromptTokens, CompletionTokens: out.Usage.CompletionTokens}
	}
	return r, nil
}

// Stream returns tokens as the engine produces them. The final token carries
// the finish reason and, when reported, usage. Failures after the stream has
// started are delivered as a Token with Err set. The channel is always closed.
func (v *openAIBackend) Stream(ctx context.Context, req Request) (<-chan Token, error) {
	ctx, cancel := context.WithTimeout(ctx, v.timeout)
	resp, err := v.do(ctx, req, true)
	if err != nil {
		cancel()
		return nil, err
	}

	// Some servers answer a streaming request with a plain JSON completion
	// instead of SSE. Serve that as a one-chunk stream rather than failing.
	asJSON := strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json")

	out := make(chan Token)
	go func() {
		defer close(out)
		defer cancel()
		defer func() { _ = resp.Body.Close() }()
		if asJSON {
			v.streamFromJSON(ctx, resp.Body, out)
			return
		}

		send := func(t Token) bool {
			select {
			case out <- t:
				return true
			case <-ctx.Done():
				return false
			}
		}
		fail := func(err error) {
			// Best effort: the consumer may already be gone.
			select {
			case out <- Token{Err: err}:
			case <-time.After(100 * time.Millisecond):
			}
		}

		var finish string
		var usage *Usage
		done := false

		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			if !strings.HasPrefix(line, "data:") {
				continue // blank separators and ": keepalive" comments
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				done = true
				break
			}
			var chunk oaiResponse
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				fail(fmt.Errorf("decode stream chunk: %w", err))
				return
			}
			if chunk.Usage != nil {
				usage = &Usage{PromptTokens: chunk.Usage.PromptTokens, CompletionTokens: chunk.Usage.CompletionTokens}
			}
			if len(chunk.Choices) == 0 {
				continue
			}
			c := chunk.Choices[0]
			if c.FinishReason != nil && *c.FinishReason != "" {
				finish = *c.FinishReason
			}
			if c.Delta.Content != "" && !send(Token{Text: c.Delta.Content}) {
				return
			}
		}
		if err := ctx.Err(); err != nil {
			fail(err)
			return
		}
		if err := sc.Err(); err != nil {
			fail(fmt.Errorf("read stream: %w", err))
			return
		}
		if !done && finish == "" {
			fail(errors.New("stream ended before completion"))
			return
		}
		send(Token{FinishReason: finish, Usage: usage})
	}()
	return out, nil
}

// streamFromJSON emits a complete (non-SSE) completion as a single text token
// followed by the finish token.
func (v *openAIBackend) streamFromJSON(ctx context.Context, body io.Reader, out chan<- Token) {
	send := func(t Token) {
		select {
		case out <- t:
		case <-ctx.Done():
		}
	}
	var rb oaiResponse
	if err := json.NewDecoder(body).Decode(&rb); err != nil {
		send(Token{Err: fmt.Errorf("decode response: %w", err)})
		return
	}
	if len(rb.Choices) == 0 {
		send(Token{Err: errors.New("backend response has no choices")})
		return
	}
	if text := rb.Choices[0].Message.Content; text != "" {
		send(Token{Text: text})
	}
	final := Token{FinishReason: "stop"}
	if fr := rb.Choices[0].FinishReason; fr != nil && *fr != "" {
		final.FinishReason = *fr
	}
	if rb.Usage != nil {
		final.Usage = &Usage{PromptTokens: rb.Usage.PromptTokens, CompletionTokens: rb.Usage.CompletionTokens}
	}
	send(final)
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
