package observe

import (
	"bufio"
	"crypto/subtle"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// RequireAdminAuth returns middleware that validates the admin bearer token.
func RequireAdminAuth(adminKey string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := extractBearerToken(r)
			if token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(adminKey)) != 1 {
				writeError(w, http.StatusUnauthorized, "unauthorized", "Invalid admin key")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func extractBearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return ""
	}
	const prefix = "Bearer "
	if len(auth) < len(prefix) || !strings.EqualFold(auth[:len(prefix)], prefix) {
		return ""
	}
	return auth[len(prefix):]
}

// IPRateLimiter provides per-IP rate limiting for the observation API.
type IPRateLimiter struct {
	mu       sync.Mutex
	limiters map[string]*ipEntry
	limit    rate.Limit
	burst    int
	done     chan struct{}
}

type ipEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

func NewIPRateLimiter(perMinute int) *IPRateLimiter {
	l := &IPRateLimiter{
		limiters: make(map[string]*ipEntry),
		limit:    rate.Limit(float64(perMinute) / 60.0),
		burst:    perMinute, // allow burst up to the per-minute limit
		done:     make(chan struct{}),
	}
	go l.cleanup()
	return l
}

func (l *IPRateLimiter) Allow(ip string) bool {
	l.mu.Lock()
	entry, ok := l.limiters[ip]
	if !ok {
		entry = &ipEntry{
			limiter: rate.NewLimiter(l.limit, l.burst),
		}
		l.limiters[ip] = entry
	}
	entry.lastSeen = time.Now()
	l.mu.Unlock()

	return entry.limiter.Allow()
}

func (l *IPRateLimiter) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			l.mu.Lock()
			cutoff := time.Now().Add(-30 * time.Minute)
			for ip, entry := range l.limiters {
				if entry.lastSeen.Before(cutoff) {
					delete(l.limiters, ip)
				}
			}
			l.mu.Unlock()
		case <-l.done:
			return
		}
	}
}

func (l *IPRateLimiter) Close() {
	close(l.done)
}

// CORSMiddleware adds CORS headers for browser-based observation tools.
func CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RateLimitMiddleware applies per-IP rate limiting.
func RateLimitMiddleware(limiter *IPRateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r)
			if !limiter.Allow(ip) {
				w.Header().Set("Retry-After", "60")
				writeError(w, http.StatusTooManyRequests, "rate_limited", "Rate limit exceeded")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequestLogger logs each request.
func RequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rw, r)
		log.Printf("[observe] %s %s %d %s", r.Method, r.URL.Path, rw.statusCode, time.Since(start).Round(time.Millisecond))
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

func (rw *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := rw.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, fmt.Errorf("response does not implement http.Hijacker")
}

func clientIP(r *http.Request) string {
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
