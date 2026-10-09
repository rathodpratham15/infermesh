package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"syscall"
	"testing"
	"time"
)

var (
	_ InferenceBackend = (*VLLM)(nil)
	_ InferenceBackend = (*Triton)(nil)
)

// fakeTriton imitates Triton's OpenAI-compatible frontend closely enough to
// catch the ways the backend could talk to it wrongly:
//   - it only answers /health/ready for health (not /v1/models);
//   - it rejects requests containing stream_options with 422, as a strict
//     schema would;
//   - with jsonOnly it answers stream:true with a plain JSON completion.
type fakeTriton struct {
	ready          bool
	jsonOnly       bool
	acceptStreamOp bool
	lastModel      string
	sawStreamOpts  bool
}

func (f *fakeTriton) server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, _ *http.Request) {
		if !f.ready {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
		}
	})
	mux.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var req oaiRequest
		var raw map[string]any
		body := make([]byte, 1<<16)
		n, _ := r.Body.Read(body)
		_ = json.Unmarshal(body[:n], &req)
		_ = json.Unmarshal(body[:n], &raw)
		f.lastModel = req.Model
		if _, ok := raw["stream_options"]; ok {
			f.sawStreamOpts = true
			if !f.acceptStreamOp {
				http.Error(w, `{"detail":"stream_options not permitted"}`, http.StatusUnprocessableEntity)
				return
			}
		}
		if req.Stream && !f.jsonOnly {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hel\"}}]}\n\n"+
				"data: {\"choices\":[{\"delta\":{\"content\":\"lo\"},\"finish_reason\":\"stop\"}]}\n\n")
			if f.acceptStreamOp {
				_, _ = fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":2}}\n\n")
			}
			_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"choices":[{"finish_reason":"stop","message":{"content":"Hello"}}],"usage":{"prompt_tokens":4,"completion_tokens":2}}`)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func TestTritonHealthUsesReadyEndpoint(t *testing.T) {
	f := &fakeTriton{ready: true}
	ts := f.server(t)
	tr := NewTriton("triton", ts.URL, "m", time.Second, false)
	if err := tr.Health(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.ready = false
	if err := tr.Health(context.Background()); err == nil {
		t.Fatal("expected unhealthy while /health/ready returns 503")
	}
	// The same server has no /v1/models: a vLLM-style probe must fail against it,
	// which proves the Triton backend is not accidentally using that path.
	if err := NewVLLM("v", ts.URL, "m", time.Second).Health(context.Background()); err == nil {
		t.Fatal("vLLM health path unexpectedly succeeded against the Triton fake")
	}
}

func TestTritonGenerateSendsConfiguredModel(t *testing.T) {
	f := &fakeTriton{ready: true}
	ts := f.server(t)
	resp, err := NewTriton("triton", ts.URL, "tensorrt_llm_bls", time.Second, false).
		Generate(context.Background(), Request{Model: "ignored", Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "Hello" || resp.Usage.CompletionTokens != 2 || f.lastModel != "tensorrt_llm_bls" {
		t.Fatalf("resp=%+v model=%q", resp, f.lastModel)
	}
}

func TestTritonStreamDoesNotSendStreamOptionsByDefault(t *testing.T) {
	f := &fakeTriton{ready: true} // would answer 422 if stream_options were sent
	ts := f.server(t)
	ch, err := NewTriton("triton", ts.URL, "m", time.Second, false).Stream(context.Background(), Request{})
	if err != nil {
		t.Fatalf("stream failed (stream_options sent?): %v", err)
	}
	text, last := collect(t, ch)
	if text != "Hello" || last.FinishReason != "stop" || f.sawStreamOpts {
		t.Fatalf("text=%q last=%+v sawStreamOpts=%v", text, last, f.sawStreamOpts)
	}
	if last.Usage != nil {
		t.Fatalf("usage must be absent (not invented) when the engine did not report it: %+v", last.Usage)
	}
}

func TestTritonStreamUsageOptIn(t *testing.T) {
	f := &fakeTriton{ready: true, acceptStreamOp: true}
	ts := f.server(t)
	ch, err := NewTriton("triton", ts.URL, "m", time.Second, true).Stream(context.Background(), Request{})
	if err != nil {
		t.Fatal(err)
	}
	_, last := collect(t, ch)
	if !f.sawStreamOpts || last.Usage == nil || last.Usage.CompletionTokens != 2 {
		t.Fatalf("sawStreamOpts=%v usage=%+v", f.sawStreamOpts, last.Usage)
	}
}

func TestStreamFallsBackWhenServerAnswersWithJSON(t *testing.T) {
	f := &fakeTriton{ready: true, jsonOnly: true}
	ts := f.server(t)
	for name, b := range map[string]InferenceBackend{
		"triton": NewTriton("t", ts.URL, "m", time.Second, false),
		"vllm":   NewVLLM("v", ts.URL, "m", time.Second),
	} {
		f.acceptStreamOp = name == "vllm" // vLLM sends stream_options; allow it for this case
		ch, err := b.Stream(context.Background(), Request{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		text, last := collect(t, ch)
		if text != "Hello" || last.FinishReason != "stop" || last.Usage == nil || last.Usage.PromptTokens != 4 {
			t.Fatalf("%s: text=%q last=%+v", name, text, last)
		}
	}
}

// Both engines must satisfy the same contract so the gateway can treat them as
// interchangeable: the same requests produce the same logical results.
func TestVLLMAndTritonShareOneContract(t *testing.T) {
	f := &fakeTriton{ready: true, acceptStreamOp: true}
	ts := f.server(t)
	// The fake only serves /health/ready, so give the vLLM backend its own /v1/models.
	vmux := http.NewServeMux()
	vmux.HandleFunc("GET /v1/models", func(http.ResponseWriter, *http.Request) {})
	vmux.Handle("/", ts.Config.Handler)
	vs := httptest.NewServer(vmux)
	defer vs.Close()

	for name, b := range map[string]InferenceBackend{
		"vllm":   NewVLLM("vllm", vs.URL, "m", time.Second),
		"triton": NewTriton("triton", ts.URL, "m", time.Second, true),
	} {
		ctx := context.Background()
		if err := b.Health(ctx); err != nil {
			t.Errorf("%s health: %v", name, err)
		}
		req := Request{MaxTokens: 8, Messages: []Message{{Role: "user", Content: "hi"}}}
		resp, err := b.Generate(ctx, req)
		if err != nil || resp.Content != "Hello" || resp.FinishReason != "stop" || resp.Usage.CompletionTokens != 2 {
			t.Errorf("%s generate: %+v %v", name, resp, err)
		}
		ch, err := b.Stream(ctx, req)
		if err != nil {
			t.Errorf("%s stream: %v", name, err)
			continue
		}
		text, last := collect(t, ch)
		if text != "Hello" || last.FinishReason != "stop" || last.Usage == nil || last.Usage.CompletionTokens != 2 {
			t.Errorf("%s stream: text=%q last=%+v", name, text, last)
		}
	}
}

func TestIsConnectionError(t *testing.T) {
	reset := &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{reset, true},
		{fmt.Errorf("wrapped: %w", reset), true},
		{io.ErrUnexpectedEOF, true},
		{&HTTPError{Status: 503}, false},
		{&HTTPError{Status: 429}, false},
		{context.Canceled, false},
		{context.DeadlineExceeded, false},
		{&net.OpError{Op: "read", Err: context.DeadlineExceeded}, false}, // net timeout, not a reset
		{errors.New("decode failure"), false},
	}
	for _, c := range cases {
		if got := IsConnectionError(c.err); got != c.want {
			t.Errorf("IsConnectionError(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}
