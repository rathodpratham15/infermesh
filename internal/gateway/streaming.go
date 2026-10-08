package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/rathodpratham15/infermesh/internal/backend"
)

// streamSSE serves an OpenAI-style server-sent-events stream. The HTTP status
// is committed only once the first backend token arrives, so failures before
// that point are reported as ordinary JSON errors. Cancelling the request
// (client disconnect) cancels the backend call through the request context.
func (s *Server) streamSSE(w http.ResponseWriter, r *http.Request, req chatRequest, be backend.InferenceBackend, id string) {
	start := s.now()
	ch, err := be.Stream(r.Context(), req.toBackend(id))
	if err != nil {
		s.log.Error("stream failed", "request_id", id, "backend", be.Name(), "err", err)
		writeError(w, upstreamStatus(err), "backend_error", err.Error())
		return
	}

	first, ok := <-ch
	if !ok {
		writeError(w, http.StatusBadGateway, "backend_error", "backend closed the stream without output")
		return
	}
	if first.Err != nil {
		s.log.Error("stream failed", "request_id", id, "backend", be.Name(), "err", first.Err)
		writeError(w, upstreamStatus(first.Err), "backend_error", first.Err.Error())
		return
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	h.Set("X-InferMesh-Backend", be.Name())
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)

	created := s.now().Unix()
	writeChunk := func(c chatChunk) bool {
		c.ID, c.Object, c.Created, c.Model = id, "chat.completion.chunk", created, req.Model
		b, err := json.Marshal(c)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
			return false
		}
		return rc.Flush() == nil
	}

	if !writeChunk(chatChunk{Choices: []chunkChoice{{Delta: chunkDelta{Role: "assistant"}}}}) {
		return
	}

	var ttft time.Duration
	var tokens int
	var final backend.Token
	for tok, more := first, true; more; tok, more = <-ch {
		if tok.Err != nil {
			s.log.Error("stream aborted", "request_id", id, "backend", be.Name(), "err", tok.Err)
			// Headers are already sent; report in-band like OpenAI does.
			_, _ = fmt.Fprintf(w, "data: {\"error\":{\"message\":%q,\"type\":\"backend_error\"}}\n\n", tok.Err.Error())
			_ = rc.Flush()
			return
		}
		if tok.FinishReason != "" || tok.Usage != nil {
			final = tok
			continue
		}
		if tokens == 0 {
			ttft = s.now().Sub(start)
		}
		tokens++
		if !writeChunk(chatChunk{Choices: []chunkChoice{{Delta: chunkDelta{Content: tok.Text}}}}) {
			return // client went away; the request context cancels the backend
		}
	}

	finish := final.FinishReason
	if finish == "" {
		finish = "stop"
	}
	if !writeChunk(chatChunk{Choices: []chunkChoice{{FinishReason: &finish}}}) {
		return
	}
	if final.Usage != nil {
		u := toUsage(*final.Usage)
		if !writeChunk(chatChunk{Choices: []chunkChoice{}, Usage: &u}) {
			return
		}
	}
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	_ = rc.Flush()
	s.log.Info("stream ok", "request_id", id, "backend", be.Name(),
		"ttft_ms", ttft.Milliseconds(), "total_ms", s.now().Sub(start).Milliseconds(), "chunks", tokens)
}

// chatWebSocket streams one completion over a WebSocket. The client sends a
// single JSON chat request; the server replies with wsMessage frames
// (start, token..., done) or a single error frame, then closes.
func (s *Server) chatWebSocket(w http.ResponseWriter, r *http.Request) {
	id := requestID(r.Context())
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: s.AllowedOrigins})
	if err != nil {
		s.log.Warn("websocket accept failed", "request_id", id, "err", err)
		return
	}
	defer func() { _ = conn.CloseNow() }()
	conn.SetReadLimit(maxBodyBytes)

	fail := func(code websocket.StatusCode, msg string) {
		_ = wsjson.Write(r.Context(), conn, wsMessage{Type: "error", RequestID: id, Error: msg})
		_ = conn.Close(code, "")
	}

	readCtx, cancelRead := context.WithTimeout(r.Context(), 10*time.Second)
	var req chatRequest
	err = wsjson.Read(readCtx, conn, &req)
	cancelRead()
	if err != nil {
		fail(websocket.StatusInvalidFramePayloadData, "expected a JSON chat request: "+err.Error())
		return
	}
	if err := req.validate(); err != nil {
		fail(websocket.StatusPolicyViolation, err.Error())
		return
	}

	// CloseRead keeps draining control frames and cancels ctx when the peer
	// disconnects, which in turn cancels the backend call.
	ctx := conn.CloseRead(r.Context())

	be, err := s.router.Pick(ctx)
	if err != nil {
		fail(websocket.StatusTryAgainLater, err.Error())
		return
	}
	start := s.now()
	ch, err := be.Stream(ctx, req.toBackend(id))
	if err != nil {
		s.log.Error("ws stream failed", "request_id", id, "backend", be.Name(), "err", err)
		fail(websocket.StatusInternalError, err.Error())
		return
	}

	if err := wsjson.Write(ctx, conn, wsMessage{Type: "start", RequestID: id, Backend: be.Name()}); err != nil {
		return
	}
	var ttft *int64
	var final backend.Token
	for tok := range ch {
		if tok.Err != nil {
			s.log.Error("ws stream aborted", "request_id", id, "backend", be.Name(), "err", tok.Err)
			fail(websocket.StatusInternalError, tok.Err.Error())
			return
		}
		if tok.FinishReason != "" || tok.Usage != nil {
			final = tok
			continue
		}
		if ttft == nil {
			ms := s.now().Sub(start).Milliseconds()
			ttft = &ms
		}
		if err := wsjson.Write(ctx, conn, wsMessage{Type: "token", Text: tok.Text}); err != nil {
			return
		}
	}
	done := wsMessage{Type: "done", RequestID: id, Backend: be.Name(), FinishReason: final.FinishReason, TTFTMs: ttft}
	if final.Usage != nil {
		u := toUsage(*final.Usage)
		done.Usage = &u
	}
	if err := wsjson.Write(ctx, conn, done); err != nil {
		return
	}
	_ = conn.Close(websocket.StatusNormalClosure, "")
}
