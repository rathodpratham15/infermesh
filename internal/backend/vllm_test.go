package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func collect(t *testing.T, ch <-chan Token) (string, Token) {
	t.Helper()
	var sb strings.Builder
	var last Token
	for tok := range ch {
		if tok.Err != nil {
			t.Fatalf("stream error: %v", tok.Err)
		}
		sb.WriteString(tok.Text)
		last = tok
	}
	return sb.String(), last
}

func TestVLLMGenerate(t *testing.T) {
	var got oaiRequest
	var gotID string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		gotID = r.Header.Get("X-Request-ID")
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = fmt.Fprint(w, `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"hello"}}],"usage":{"prompt_tokens":5,"completion_tokens":1}}`)
	}))
	defer ts.Close()

	v := NewVLLM("vllm", ts.URL+"/", "configured-model", time.Second)
	resp, err := v.Generate(context.Background(), Request{ID: "r1", Model: "ignored", MaxTokens: 7,
		Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "hello" || resp.FinishReason != "stop" || resp.Usage.PromptTokens != 5 || resp.Usage.CompletionTokens != 1 {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if got.Model != "configured-model" || got.Stream || got.MaxTokens != 7 || len(got.Messages) != 1 || gotID != "r1" {
		t.Fatalf("unexpected upstream request: %+v id=%q", got, gotID)
	}
}

func TestVLLMUsesRequestModelWhenUnconfigured(t *testing.T) {
	var got oaiRequest
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = fmt.Fprint(w, `{"choices":[{"finish_reason":"stop","message":{"content":"x"}}]}`)
	}))
	defer ts.Close()
	if _, err := NewVLLM("v", ts.URL, "", time.Second).Generate(context.Background(), Request{Model: "from-request"}); err != nil {
		t.Fatal(err)
	}
	if got.Model != "from-request" {
		t.Fatalf("model = %q", got.Model)
	}
}

func TestVLLMHTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "overloaded", http.StatusServiceUnavailable)
	}))
	defer ts.Close()
	v := NewVLLM("v", ts.URL, "m", time.Second)

	_, err := v.Generate(context.Background(), Request{})
	var he *HTTPError
	if !errors.As(err, &he) || he.Status != 503 {
		t.Fatalf("expected HTTPError 503, got %v", err)
	}
	if _, err := v.Stream(context.Background(), Request{}); !errors.As(err, &he) {
		t.Fatalf("expected HTTPError from Stream, got %v", err)
	}
}

func TestVLLMTimeout(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body) // lets the server notice the client going away
		<-r.Context().Done()
	}))
	defer ts.Close()
	_, err := NewVLLM("v", ts.URL, "m", 50*time.Millisecond).Generate(context.Background(), Request{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
}

const sseStream = `: keepalive 1/5

data: {"choices":[{"delta":{"role":"assistant","content":"Hel"},"finish_reason":null}]}

data: {"choices":[{"delta":{"content":"lo"},"finish_reason":null}]}

data: {"choices":[{"delta":{},"finish_reason":"stop"}]}

data: {"choices":[],"usage":{"prompt_tokens":4,"completion_tokens":2}}

data: [DONE]

`

func TestVLLMStream(t *testing.T) {
	var got oaiRequest
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, sseStream)
	}))
	defer ts.Close()

	ch, err := NewVLLM("v", ts.URL, "m", time.Second).Stream(context.Background(), Request{})
	if err != nil {
		t.Fatal(err)
	}
	text, last := collect(t, ch)
	if text != "Hello" || last.FinishReason != "stop" || last.Usage == nil || last.Usage.CompletionTokens != 2 {
		t.Fatalf("text=%q last=%+v", text, last)
	}
	if !got.Stream || got.StreamOptions == nil || !got.StreamOptions.IncludeUsage {
		t.Fatalf("stream request flags wrong: %+v", got)
	}
}

func TestVLLMStreamTruncated(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
	}))
	defer ts.Close()
	ch, err := NewVLLM("v", ts.URL, "m", time.Second).Stream(context.Background(), Request{})
	if err != nil {
		t.Fatal(err)
	}
	var sawErr bool
	for tok := range ch {
		if tok.Err != nil {
			sawErr = true
		}
	}
	if !sawErr {
		t.Fatal("expected an error for a stream that ends before completion")
	}
}

func TestVLLMStreamCancel(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := NewVLLM("v", ts.URL, "m", 5*time.Second).Stream(ctx, Request{})
	if err != nil {
		t.Fatal(err)
	}
	if tok := <-ch; tok.Text != "a" {
		t.Fatalf("first token = %+v", tok)
	}
	cancel()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("channel not closed after cancel")
		}
	}
}

func TestVLLMHealth(t *testing.T) {
	status := http.StatusOK
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
	}))
	defer ts.Close()
	v := NewVLLM("v", ts.URL, "m", time.Second)
	if err := v.Health(context.Background()); err != nil {
		t.Fatal(err)
	}
	status = http.StatusServiceUnavailable
	if err := v.Health(context.Background()); err == nil {
		t.Fatal("expected unhealthy")
	}
	ts.Close()
	if err := v.Health(context.Background()); err == nil {
		t.Fatal("expected unhealthy when server is down")
	}
}

// TestVLLMLive runs against a real OpenAI-compatible server. It is skipped
// unless INFERMESH_TEST_VLLM_URL (and INFERMESH_TEST_VLLM_MODEL) are set.
func TestVLLMLive(t *testing.T) {
	url, model := os.Getenv("INFERMESH_TEST_VLLM_URL"), os.Getenv("INFERMESH_TEST_VLLM_MODEL")
	if url == "" {
		t.Skip("INFERMESH_TEST_VLLM_URL not set")
	}
	v := NewVLLM("live", url, model, 60*time.Second)
	ctx := context.Background()
	if err := v.Health(ctx); err != nil {
		t.Fatal(err)
	}
	req := Request{MaxTokens: 16, Messages: []Message{{Role: "user", Content: "Say hello in three words."}}}
	resp, err := v.Generate(ctx, req)
	if err != nil || resp.Content == "" || resp.Usage.CompletionTokens == 0 {
		t.Fatalf("generate: %+v, %v", resp, err)
	}
	ch, err := v.Stream(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	text, last := collect(t, ch)
	if text == "" || last.FinishReason == "" {
		t.Fatalf("stream: text=%q last=%+v", text, last)
	}
	t.Logf("generate=%q stream=%q usage=%+v", resp.Content, text, last.Usage)
}
