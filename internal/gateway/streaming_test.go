package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/rathodpratham15/infermesh/internal/backend"
)

// scriptBackend lets a test control Stream exactly.
type scriptBackend struct {
	stream func(ctx context.Context, req backend.Request) (<-chan backend.Token, error)
}

func (scriptBackend) Name() string                 { return "script" }
func (scriptBackend) Health(context.Context) error { return nil }
func (scriptBackend) Generate(context.Context, backend.Request) (backend.Response, error) {
	return backend.Response{}, errors.New("unused")
}
func (b scriptBackend) Stream(ctx context.Context, req backend.Request) (<-chan backend.Token, error) {
	return b.stream(ctx, req)
}

func serverFor(t *testing.T, b backend.InferenceBackend) *httptest.Server {
	t.Helper()
	s := New(testRouter(b), slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts
}

const streamBody = `{"model":"m","stream":true,"messages":[{"role":"user","content":"hi there"}]}`

// readSSE returns the data payloads of an SSE response.
func readSSE(t *testing.T, r io.Reader) []string {
	t.Helper()
	var out []string
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		if d, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
			out = append(out, d)
		}
	}
	return out
}

func TestSSEStream(t *testing.T) {
	ts := serverFor(t, backend.NewMock("mock", 0))
	resp := post(t, ts.URL, streamBody)
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("status=%d type=%q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if resp.Header.Get("X-InferMesh-Backend") != "mock" || resp.Header.Get("X-Request-ID") == "" {
		t.Fatalf("missing headers: %v", resp.Header)
	}
	events := readSSE(t, resp.Body)
	if len(events) < 4 || events[len(events)-1] != "[DONE]" {
		t.Fatalf("unexpected events: %v", events)
	}

	var text strings.Builder
	var sawRole, sawFinish bool
	var u *usage
	for _, e := range events[:len(events)-1] {
		var c chatChunk
		if err := json.Unmarshal([]byte(e), &c); err != nil {
			t.Fatalf("bad chunk %q: %v", e, err)
		}
		if c.Object != "chat.completion.chunk" {
			t.Fatalf("object = %q", c.Object)
		}
		if c.Usage != nil {
			u = c.Usage
		}
		for _, ch := range c.Choices {
			sawRole = sawRole || ch.Delta.Role == "assistant"
			text.WriteString(ch.Delta.Content)
			if ch.FinishReason != nil {
				sawFinish = *ch.FinishReason == "stop"
			}
		}
	}
	want := strings.Join(backend.NewMock("x", 0).Tokens, " ")
	if text.String() != want || !sawRole || !sawFinish {
		t.Fatalf("text=%q role=%v finish=%v", text.String(), sawRole, sawFinish)
	}
	if u == nil || u.CompletionTokens != len(backend.NewMock("x", 0).Tokens) || u.PromptTokens != 2 {
		t.Fatalf("usage = %+v", u)
	}
}

func TestSSEPreStreamErrorIsJSON(t *testing.T) {
	ts := serverFor(t, scriptBackend{stream: func(context.Context, backend.Request) (<-chan backend.Token, error) {
		return nil, &backend.HTTPError{Status: 503}
	}})
	resp := post(t, ts.URL, streamBody)
	if resp.StatusCode != http.StatusBadGateway || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("status=%d type=%q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}

	// An error as the very first token is also a clean JSON error.
	ts2 := serverFor(t, scriptBackend{stream: func(context.Context, backend.Request) (<-chan backend.Token, error) {
		ch := make(chan backend.Token, 1)
		ch <- backend.Token{Err: context.DeadlineExceeded}
		close(ch)
		return ch, nil
	}})
	if resp := post(t, ts2.URL, streamBody); resp.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("status=%d, want 504", resp.StatusCode)
	}
}

func TestSSEMidStreamError(t *testing.T) {
	ts := serverFor(t, scriptBackend{stream: func(context.Context, backend.Request) (<-chan backend.Token, error) {
		ch := make(chan backend.Token, 2)
		ch <- backend.Token{Text: "partial"}
		ch <- backend.Token{Err: errors.New("engine died")}
		close(ch)
		return ch, nil
	}})
	resp := post(t, ts.URL, streamBody)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	events := readSSE(t, resp.Body)
	last := events[len(events)-1]
	if !strings.Contains(last, "engine died") || last == "[DONE]" {
		t.Fatalf("expected in-band error, got %v", events)
	}
}

func TestSSEClientDisconnectCancelsBackend(t *testing.T) {
	cancelled := make(chan struct{})
	ts := serverFor(t, scriptBackend{stream: func(ctx context.Context, _ backend.Request) (<-chan backend.Token, error) {
		ch := make(chan backend.Token)
		go func() {
			defer close(ch)
			for {
				select {
				case ch <- backend.Token{Text: "x"}:
					time.Sleep(5 * time.Millisecond)
				case <-ctx.Done():
					close(cancelled)
					return
				}
			}
		}()
		return ch, nil
	}})

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "POST", ts.URL+"/v1/chat/completions", strings.NewReader(streamBody))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(resp.Body)
	for i := 0; i < 3; i++ { // read a few events, then hang up
		if _, err := br.ReadString('\n'); err != nil {
			t.Fatal(err)
		}
	}
	cancel()
	_ = resp.Body.Close()

	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("backend context was not cancelled after client disconnect")
	}
}

func dialWS(t *testing.T, ts *httptest.Server) (*websocket.Conn, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/v1/chat/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	return conn, ctx
}

func TestWebSocketStream(t *testing.T) {
	ts := serverFor(t, backend.NewMock("mock", 0))
	conn, ctx := dialWS(t, ts)
	if err := wsjson.Write(ctx, conn, map[string]any{"model": "m", "messages": []map[string]string{{"role": "user", "content": "hi"}}}); err != nil {
		t.Fatal(err)
	}

	var msgs []wsMessage
	for {
		var m wsMessage
		if err := wsjson.Read(ctx, conn, &m); err != nil {
			break // server closes after "done"
		}
		msgs = append(msgs, m)
	}
	if len(msgs) < 3 || msgs[0].Type != "start" || msgs[0].Backend != "mock" || msgs[0].RequestID == "" {
		t.Fatalf("unexpected frames: %+v", msgs)
	}
	var text strings.Builder
	for _, m := range msgs[1 : len(msgs)-1] {
		if m.Type != "token" {
			t.Fatalf("unexpected frame: %+v", m)
		}
		text.WriteString(m.Text)
	}
	done := msgs[len(msgs)-1]
	if done.Type != "done" || done.FinishReason != "stop" || done.Usage == nil || done.TTFTMs == nil {
		t.Fatalf("bad done frame: %+v", done)
	}
	if text.String() != strings.Join(backend.NewMock("x", 0).Tokens, " ") {
		t.Fatalf("text = %q", text.String())
	}
}

func TestWebSocketValidation(t *testing.T) {
	ts := serverFor(t, backend.NewMock("mock", 0))
	conn, ctx := dialWS(t, ts)
	if err := wsjson.Write(ctx, conn, map[string]any{"messages": []string{}}); err != nil {
		t.Fatal(err)
	}
	var m wsMessage
	if err := wsjson.Read(ctx, conn, &m); err != nil || m.Type != "error" || m.Error == "" {
		t.Fatalf("frame=%+v err=%v", m, err)
	}
}

func TestWebSocketDisconnectCancelsBackend(t *testing.T) {
	cancelled := make(chan struct{})
	ts := serverFor(t, scriptBackend{stream: func(ctx context.Context, _ backend.Request) (<-chan backend.Token, error) {
		ch := make(chan backend.Token)
		go func() {
			defer close(ch)
			for {
				select {
				case ch <- backend.Token{Text: "x"}:
					time.Sleep(5 * time.Millisecond)
				case <-ctx.Done():
					close(cancelled)
					return
				}
			}
		}()
		return ch, nil
	}})
	conn, ctx := dialWS(t, ts)
	if err := wsjson.Write(ctx, conn, map[string]any{"messages": []map[string]string{{"role": "user", "content": "hi"}}}); err != nil {
		t.Fatal(err)
	}
	var m wsMessage
	for i := 0; i < 3; i++ {
		if err := wsjson.Read(ctx, conn, &m); err != nil {
			t.Fatal(err)
		}
	}
	_ = conn.Close(websocket.StatusNormalClosure, "")

	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("backend context was not cancelled after websocket close")
	}
}
