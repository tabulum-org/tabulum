package api

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tabulum/tabulum/internal/ratelimit"
	"github.com/tabulum/tabulum/internal/registry"
)

type contextKey string

const (
	ContextKeyOperator contextKey = "operator"
	ContextKeyAgent    contextKey = "agent"
	ContextKeyClientIP contextKey = "client_ip"
)

func OperatorFromContext(ctx context.Context) *registry.Operator {
	op, _ := ctx.Value(ContextKeyOperator).(*registry.Operator)
	return op
}

func AgentFromContext(ctx context.Context) *registry.Agent {
	agent, _ := ctx.Value(ContextKeyAgent).(*registry.Agent)
	return agent
}

func ClientIPFromContext(ctx context.Context) string {
	ip, _ := ctx.Value(ContextKeyClientIP).(string)
	return ip
}

func extractBearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(auth, "Bearer ")
}

func clientIP(r *http.Request) string {
	// Check X-Forwarded-For for reverse proxy
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.SplitN(xff, ",", 2)
		ip := strings.TrimSpace(parts[0])
		if ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func RequireOperatorAuth(reg *registry.Registry, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := extractBearerToken(r)
		if token == "" {
			writeError(w, http.StatusUnauthorized, "unauthorized", "Missing or invalid API key")
			return
		}

		op, err := reg.AuthenticateOperator(r.Context(), token)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "Invalid or missing API key")
			return
		}

		ctx := context.WithValue(r.Context(), ContextKeyOperator, op)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func RequireAgentAuth(reg *registry.Registry, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := extractBearerToken(r)
		if token == "" {
			writeError(w, http.StatusUnauthorized, "unauthorized", "Missing or invalid agent token")
			return
		}

		agent, err := reg.AuthenticateAgent(r.Context(), token)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "Invalid or missing agent token")
			return
		}

		ctx := context.WithValue(r.Context(), ContextKeyAgent, agent)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func RateLimit(limiter *ratelimit.Limiter, category ratelimit.Category, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		agent := AgentFromContext(r.Context())
		if agent == nil {
			next.ServeHTTP(w, r)
			return
		}

		result := limiter.Allow(r.Context(), agent.Address, category)
		setRateLimitHeaders(w, result)

		if !result.Allowed {
			w.Header().Set("Retry-After", strconv.Itoa(int(result.RetryAfter.Seconds())+1))
			writeError(w, http.StatusTooManyRequests, "rate_limited", "Rate limit exceeded")
			return
		}

		next.ServeHTTP(w, r)
	})
}

func IPRateLimit(limiter *ratelimit.Limiter, limit int, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)

		result := limiter.IPAllow(r.Context(), ip, limit)
		setRateLimitHeaders(w, result)

		if !result.Allowed {
			w.Header().Set("Retry-After", strconv.Itoa(int(result.RetryAfter.Seconds())+1))
			writeError(w, http.StatusTooManyRequests, "rate_limited", "Rate limit exceeded")
			return
		}

		ctx := context.WithValue(r.Context(), ContextKeyClientIP, ip)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func setRateLimitHeaders(w http.ResponseWriter, result ratelimit.Result) {
	w.Header().Set("X-RateLimit-Limit", strconv.Itoa(result.Limit))
	w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(result.Remaining))
	w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(result.ResetAt.Unix(), 10))
}

func MaxBodySize(maxBytes int64, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		}
		next.ServeHTTP(w, r)
	})
}

func RequireJSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost || r.Method == http.MethodPut {
			ct := r.Header.Get("Content-Type")
			if !strings.HasPrefix(ct, "application/json") {
				writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func RequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rw, r)
		log.Printf("%s %s %d %s", r.Method, r.URL.Path, rw.statusCode, time.Since(start).Round(time.Millisecond))
	})
}

type responseWriter struct {
	http.ResponseWriter
	statusCode int
	written    bool
}

func (rw *responseWriter) WriteHeader(code int) {
	if !rw.written {
		rw.statusCode = code
		rw.written = true
	}
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	if !rw.written {
		rw.written = true
	}
	return rw.ResponseWriter.Write(b)
}

func (rw *responseWriter) Unwrap() http.ResponseWriter {
	return rw.ResponseWriter
}

func NoCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		// Remove any CORS headers that might have been set
		w.Header().Del("Access-Control-Allow-Origin")
		w.Header().Del("Access-Control-Allow-Methods")
		w.Header().Del("Access-Control-Allow-Headers")

		next.ServeHTTP(w, r)
	})
}

// Middleware to extract and store client IP
func WithClientIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		ctx := context.WithValue(r.Context(), ContextKeyClientIP, ip)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// NotFound returns a 404 JSON error for unmatched routes
func NotFound() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("No route for %s %s", r.Method, r.URL.Path))
	})
}

// MethodNotAllowed returns a 405 JSON error
func MethodNotAllowed() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed")
	})
}
