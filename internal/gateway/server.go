// Package gateway implements the HTTP API in front of the inference backends.
package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"log/slog"
	"net/http"
	"time"

	"github.com/rathodpratham15/infermesh/internal/backend"
	"github.com/rathodpratham15/infermesh/internal/events"
	"github.com/rathodpratham15/infermesh/internal/metrics"
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
	// SameBackendRetryDelay is the pause before retrying a connection-level
	// failure on the same backend (only when no other backend is available).
	SameBackendRetryDelay time.Duration
	// Metrics receives request-level measurements. New installs a no-op.
	Metrics *metrics.Metrics
	// Events receives asynchronous telemetry events (Kafka). It is never on the
	// request's critical path. New installs a no-op.
	Events events.Publisher
}

// tracer is resolved per use so it follows whichever provider is installed.
func tracer() trace.Tracer { return otel.Tracer("github.com/rathodpratham15/infermesh/gateway") }

func New(r router.Router, log *slog.Logger) *Server {
	return &Server{router: r, log: log, now: time.Now, MaxRetries: 1, RequestTimeout: 2 * time.Minute, SameBackendRetryDelay: 50 * time.Millisecond, Metrics: metrics.Noop(), Events: events.Noop{}}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	if h := s.Metrics.Handler(); h != nil {
		mux.Handle("GET /metrics", h)
	}
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
		ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, ctxKey{}, id)))
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

// call carries per-request facts shared by the handlers and telemetry events.
type call struct {
	id       string
	model    string
	stream   bool
	queue    time.Duration // total time spent waiting for a backend slot
	attempts int
	backend  string // backend of the most recent attempt
}

func (s *Server) event(c *call, typ string) events.Event {
	return events.Event{
		Type: typ, RequestID: c.id, Timestamp: s.now().UTC(), Backend: c.backend, Model: c.model,
		Stream: c.stream, Attempt: c.attempts, QueueMS: c.queue.Milliseconds(),
	}
}

func (s *Server) emitStarted(c *call) {
	s.Events.Publish(s.event(c, events.TypeStarted))
}

// emitCompleted reports a successful generation. ttft is nil for non-streaming
// requests; gen is the generation time (after the first token when streaming).
func (s *Server) emitCompleted(c *call, ttft *time.Duration, gen time.Duration, u backend.Usage) {
	e := s.event(c, events.TypeCompleted)
	e.Status = events.StatusSuccess
	e.InputTokens, e.OutputTokens, e.GenerationMS = u.PromptTokens, u.CompletionTokens, gen.Milliseconds()
	if ttft != nil {
		ms := ttft.Milliseconds()
		e.TTFTMS = &ms
	}
	s.Events.Publish(e)
}

func (s *Server) emitFailed(c *call, err error) {
	e := s.event(c, events.TypeFailed)
	e.Status, e.Error = failureStatus(err), err.Error()
	s.Events.Publish(e)
}

func failureStatus(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return events.StatusCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return events.StatusTimeout
	case errors.Is(err, router.ErrQueueFull), errors.Is(err, router.ErrNoBackend):
		return events.StatusRejected
	}
	return events.StatusError
}

// attempt runs fn against a backend leased from the router, retrying on a
// different backend while the error is retryable and the budget allows. fn must
// release the lease itself unless it returns an error, in which case attempt
// releases it.
func (s *Server) attempt(ctx context.Context, c *call, fn func(context.Context, *router.Lease) error) error {
	var tried []string
	var last error
	for n := 0; ; n++ {
		waitStart := s.now()
		lease, err := s.router.Acquire(ctx, tried...)
		if err != nil && last != nil && errors.Is(err, router.ErrNoBackend) && backend.IsConnectionError(last) {
			// Every healthy backend has already failed this request, but the failure was a
			// transport-level one (for example a connection reset), which is often transient.
			// Retry the same backend once more after a short pause rather than giving up.
			// HTTP 5xx/429 are deliberately not retried this way: that would add load to a
			// server that is already failing.
			s.log.Warn("retrying on the same backend", "request_id", c.id, "attempt", n, "err", last)
			if !sleepCtx(ctx, s.SameBackendRetryDelay) {
				return last
			}
			lease, err = s.router.Acquire(ctx)
		}
		c.queue += s.now().Sub(waitStart)
		if err != nil {
			if last != nil && errors.Is(err, router.ErrNoBackend) {
				return last // nothing left to retry on: report the real failure
			}
			return err
		}
		c.attempts++
		c.backend = lease.Backend.Name()
		s.emitStarted(c)
		actx, span := tracer().Start(ctx, "backend.attempt", trace.WithAttributes(
			attribute.String("infermesh.backend", lease.Backend.Name()),
			attribute.Int("infermesh.attempt", n+1),
			attribute.String("infermesh.request_id", c.id)))
		err = fn(actx, lease)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
		if err == nil {
			return nil
		}
		lease.Release(err)
		last = err
		tried = append(tried, lease.Backend.Name())
		if n >= s.MaxRetries || !backend.Retryable(err) {
			return err
		}
		s.log.Warn("retrying on another backend", "request_id", c.id, "failed_backend", lease.Backend.Name(), "attempt", n+1, "err", err)
	}
}

// sleepCtx waits for d or until ctx ends, reporting whether the full wait elapsed.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func (s *Server) chatCompletions(w http.ResponseWriter, r *http.Request) {
	start := s.now()
	sw := &statusWriter{ResponseWriter: w, code: http.StatusOK}
	w = sw
	s.Metrics.InFlight(r.Context(), 1, "chat")
	defer func() {
		s.Metrics.InFlight(r.Context(), -1, "chat")
		s.Metrics.Request(r.Context(), "chat", sw.code, s.now().Sub(start))
	}()

	req, ok := parseChatRequest(w, r)
	if !ok {
		return
	}
	ctx, cancel := s.withTimeout(r.Context())
	defer cancel()
	id := requestID(ctx)
	ctx, span := tracer().Start(ctx, "chat.completions", trace.WithAttributes(
		attribute.String("infermesh.request_id", id),
		attribute.Bool("infermesh.stream", req.Stream),
		attribute.String("infermesh.model", req.Model)))
	defer span.End()

	c := &call{id: id, model: req.Model, stream: req.Stream}
	if req.Stream {
		s.streamSSE(ctx, w, req, c)
		return
	}

	var resp backend.Response
	var served string
	genStart := s.now()
	err := s.attempt(ctx, c, func(ctx context.Context, l *router.Lease) error {
		var err error
		if resp, err = l.Backend.Generate(ctx, req.toBackend(id)); err != nil {
			return err
		}
		served = l.Backend.Name()
		l.Release(nil)
		return nil
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		s.emitFailed(c, err)
		s.log.Error("generate failed", "request_id", id, "err", err)
		s.writeUpstreamError(w, err)
		return
	}
	genDur := s.now().Sub(genStart)
	s.Metrics.Generation(ctx, served, nil, genDur, resp.Usage)
	s.emitCompleted(c, nil, genDur, resp.Usage)
	s.log.Info("generate ok", "request_id", id, "backend", served,
		"latency_ms", genDur.Milliseconds(), "output_tokens", resp.Usage.CompletionTokens)

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

// statusWriter remembers the status code for metrics. Unwrap keeps Flush and
// other http.ResponseController features working.
type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(code int) {
	w.code = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
