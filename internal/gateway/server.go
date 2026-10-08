// Package gateway implements the HTTP API in front of the inference backends.
package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/rathodpratham15/infermesh/internal/backend"
	"github.com/rathodpratham15/infermesh/internal/router"
)

const maxBodyBytes = 1 << 20

type Server struct {
	router router.Router
	log    *slog.Logger
	now    func() time.Time

	// AllowedOrigins lists origin patterns (e.g. "localhost:5173") permitted to
	// open a WebSocket from a browser. Same-origin and non-browser clients
	// are always allowed.
	AllowedOrigins []string
	// MaxRetries is the number of extra attempts on a different backend after a
	// retryable failure (before any output has reached the client).
	MaxRetries int
	// RequestTimeout bounds a whole request, including time spent queued.
	RequestTimeout time.Duration
}

func New(r router.Router, log *slog.Logger) *Server {
	return &Server{router: r, log: log, now: time.Now, MaxRetries: 1, RequestTimeout: 2 * time.Minute}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /readyz", s.readyz)
	mux.HandleFunc("GET /v1/backends", s.backends)
	mux.HandleFunc("POST /v1/chat/completions", s.chatCompletions)
	mux.HandleFunc("GET /v1/chat/stream", s.chatWebSocket)
	return s.withRequestID(mux)
}

type ctxKey struct{}

func requestID(ctx context.Context) string {
	id, _ := ctx.Value(ctxKey{}).(string)
	return id
}

func (s *Server) withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			var b [8]byte
			_, _ = rand.Read(b[:])
			id = "req-" + hex.EncodeToString(b[:])
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, id)))
	})
}

// healthz is liveness: the process is up.
func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// readyz is readiness: at least one backend is healthy.
func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	status := map[string]string{}
	ready := false
	for _, b := range s.router.Backends() {
		if err := b.Health(ctx); err != nil {
			status[b.Name()] = "unhealthy: " + err.Error()
			continue
		}
		status[b.Name()] = "ok"
		ready = true
	}
	code := http.StatusOK
	if !ready {
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, map[string]any{"ready": ready, "backends": status})
}

// parseChatRequest decodes and validates a chat request body.
func parseChatRequest(w http.ResponseWriter, r *http.Request) (chatRequest, bool) {
	var req chatRequest
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid JSON body: "+err.Error())
		return req, false
	}
	if err := req.validate(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return req, false
	}
	return req, true
}

func (c chatRequest) validate() error {
	if len(c.Messages) == 0 {
		return errors.New("messages must not be empty")
	}
	return nil
}

func (c chatRequest) toBackend(id string) backend.Request {
	br := backend.Request{ID: id, Model: c.Model, MaxTokens: c.MaxTokens, Temperature: c.Temperature}
	for _, m := range c.Messages {
		br.Messages = append(br.Messages, backend.Message{Role: m.Role, Content: m.Content})
	}
	return br
}

// upstreamStatus maps a routing or backend error to the HTTP status returned
// to the client.
func upstreamStatus(err error) int {
	switch {
	case errors.Is(err, router.ErrQueueFull):
		return http.StatusTooManyRequests
	case errors.Is(err, router.ErrNoBackend):
		return http.StatusServiceUnavailable
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout
	}
	return http.StatusBadGateway
}

func errorType(err error) string {
	switch {
	case errors.Is(err, router.ErrQueueFull):
		return "rate_limit_error"
	case errors.Is(err, router.ErrNoBackend):
		return "backend_unavailable"
	}
	return "backend_error"
}

func (s *Server) writeUpstreamError(w http.ResponseWriter, err error) {
	if errors.Is(err, router.ErrQueueFull) {
		w.Header().Set("Retry-After", "1")
	}
	writeError(w, upstreamStatus(err), errorType(err), err.Error())
}

// withTimeout applies the gateway-wide request budget.
func (s *Server) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if s.RequestTimeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, s.RequestTimeout)
}

// attempt runs fn against a backend leased from the router, retrying on a
// different backend while the error is retryable and the budget allows. fn must
// release the lease itself unless it returns an error, in which case attempt
// releases it.
func (s *Server) attempt(ctx context.Context, id string, fn func(*router.Lease) error) error {
	var tried []string
	var last error
	for n := 0; ; n++ {
		lease, err := s.router.Acquire(ctx, tried...)
		if err != nil {
			if last != nil && errors.Is(err, router.ErrNoBackend) {
				return last // nothing left to retry on: report the real failure
			}
			return err
		}
		err = fn(lease)
		if err == nil {
			return nil
		}
		lease.Release(err)
		last = err
		tried = append(tried, lease.Backend.Name())
		if n >= s.MaxRetries || !backend.Retryable(err) {
			return err
		}
		s.log.Warn("retrying on another backend", "request_id", id, "failed_backend", lease.Backend.Name(), "attempt", n+1, "err", err)
	}
}

func (s *Server) chatCompletions(w http.ResponseWriter, r *http.Request) {
	req, ok := parseChatRequest(w, r)
	if !ok {
		return
	}
	ctx, cancel := s.withTimeout(r.Context())
	defer cancel()
	id := requestID(ctx)

	if req.Stream {
		s.streamSSE(ctx, w, req, id)
		return
	}

	var resp backend.Response
	var served string
	start := s.now()
	err := s.attempt(ctx, id, func(l *router.Lease) error {
		var err error
		if resp, err = l.Backend.Generate(ctx, req.toBackend(id)); err != nil {
			return err
		}
		served = l.Backend.Name()
		l.Release(nil)
		return nil
	})
	if err != nil {
		s.log.Error("generate failed", "request_id", id, "err", err)
		s.writeUpstreamError(w, err)
		return
	}
	s.log.Info("generate ok", "request_id", id, "backend", served,
		"latency_ms", s.now().Sub(start).Milliseconds(), "output_tokens", resp.Usage.CompletionTokens)

	w.Header().Set("X-InferMesh-Backend", served)
	writeJSON(w, http.StatusOK, chatResponse{
		ID:      id,
		Object:  "chat.completion",
		Created: s.now().Unix(),
		Model:   req.Model,
		Choices: []chatChoice{{Message: chatMessage{Role: "assistant", Content: resp.Content}, FinishReason: resp.FinishReason}},
		Usage:   toUsage(resp.Usage),
	})
}

// backends reports per-backend routing state for debugging and dashboards.
func (s *Server) backends(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"queue_depth": s.router.QueueDepth(), "backends": s.router.Stats()})
}

func toUsage(u backend.Usage) usage {
	return usage{PromptTokens: u.PromptTokens, CompletionTokens: u.CompletionTokens, TotalTokens: u.PromptTokens + u.CompletionTokens}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, typ, msg string) {
	var b errorBody
	b.Error.Message, b.Error.Type = msg, typ
	writeJSON(w, code, b)
}
