package gateway

import (
	"net/http"
	"strings"
)

// CORS lets a browser UI served from another origin call the gateway directly.
// It is off unless Server.CORSAllowedOrigins is set; without it the gateway sends
// no CORS headers and browsers only reach it through a same-origin proxy.
//
// The list holds exact origins ("http://localhost:5173") or "*" for any origin.
// The gateway uses no cookies or credentials, so "*" does not expose a session,
// but it does let any web page call the API from a visitor's browser: prefer an
// explicit list outside local development.
const (
	corsAllowHeaders  = "Content-Type, X-Request-ID, X-InferMesh-Pin-Backend, traceparent, tracestate"
	corsAllowMethods  = "GET, POST, OPTIONS"
	corsExposeHeaders = "X-Request-ID, X-InferMesh-Backend, Retry-After"
)

func (s *Server) corsOrigin(origin string) (allowed string, ok bool) {
	for _, o := range s.CORSAllowedOrigins {
		if o == "*" {
			return "*", true
		}
		if strings.EqualFold(o, origin) {
			return origin, true
		}
	}
	return "", false
}

func (s *Server) withCORS(next http.Handler) http.Handler {
	if len(s.CORSAllowedOrigins) == 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Add("Vary", "Origin")
		allowed, ok := s.corsOrigin(origin)
		preflight := r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != ""
		if !ok {
			if preflight {
				http.Error(w, "origin not allowed", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r) // served, but without CORS headers the browser will block the response
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", allowed)
		w.Header().Set("Access-Control-Expose-Headers", corsExposeHeaders)
		if preflight {
			w.Header().Set("Access-Control-Allow-Methods", corsAllowMethods)
			w.Header().Set("Access-Control-Allow-Headers", corsAllowHeaders)
			w.Header().Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
