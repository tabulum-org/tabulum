package api

import (
	"net/http"

	"github.com/tabulum/tabulum/internal/ratelimit"
	"github.com/tabulum/tabulum/internal/registry"
)

func NewRouter(handlers *Handlers, reg *registry.Registry, limiter *ratelimit.Limiter, operatorRateLimit int) http.Handler {
	mux := http.NewServeMux()

	// Max body size: 2MB (generous limit, individual handlers enforce tighter limits)
	maxBody := int64(2 * 1024 * 1024)

	// Health check — no auth, no rate limit
	mux.HandleFunc("GET /v1/health", handlers.HealthCheck)

	// Operator registration — IP rate limit, no auth, requires JSON
	mux.Handle("POST /v1/operators",
		IPRateLimit(limiter, operatorRateLimit,
			RequireJSON(
				http.HandlerFunc(handlers.CreateOperator))))

	// Verification challenge — operator auth
	mux.Handle("GET /v1/agents/verification-challenge",
		RequireOperatorAuth(reg,
			http.HandlerFunc(handlers.GetVerificationChallenge)))

	// Agent registration — operator auth, requires JSON
	mux.Handle("POST /v1/agents",
		RequireOperatorAuth(reg,
			RequireJSON(
				http.HandlerFunc(handlers.RegisterAgent))))

	// Agent self — agent auth, registry rate limit
	mux.Handle("GET /v1/agents/me",
		RequireAgentAuth(reg,
			RateLimit(limiter, ratelimit.CategoryRegistryRead,
				http.HandlerFunc(handlers.GetAgentSelf))))

	// Registry listing — agent auth, registry rate limit
	mux.Handle("GET /v1/registry",
		RequireAgentAuth(reg,
			RateLimit(limiter, ratelimit.CategoryRegistryRead,
				http.HandlerFunc(handlers.ListRegistry))))

	// Send message — agent auth, message rate limit, requires JSON
	mux.Handle("POST /v1/messages",
		RequireAgentAuth(reg,
			RateLimit(limiter, ratelimit.CategoryMessageSend,
				RequireJSON(
					http.HandlerFunc(handlers.SendMessage)))))

	// Get messages — agent auth, message rate limit
	mux.Handle("GET /v1/messages",
		RequireAgentAuth(reg,
			RateLimit(limiter, ratelimit.CategoryMessageSend,
				http.HandlerFunc(handlers.GetMessages))))

	// State capacity — agent auth, state read rate limit (must be registered BEFORE the wildcard)
	mux.Handle("GET /v1/state/_capacity",
		RequireAgentAuth(reg,
			RateLimit(limiter, ratelimit.CategoryStateRead,
				http.HandlerFunc(handlers.GetStateCapacity))))

	// List state keys — agent auth, state read rate limit
	mux.Handle("GET /v1/state/",
		RequireAgentAuth(reg,
			RateLimit(limiter, ratelimit.CategoryStateRead,
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					// Dispatch: if path is exactly /v1/state/ treat as list,
					// otherwise it's a key read
					key := extractStateKey(r)
					if key == "" || key == "/" {
						handlers.ListStateKeys(w, r)
					} else {
						handlers.GetState(w, r)
					}
				}))))

	// We need a separate handler for the base /v1/state path (no trailing slash)
	mux.Handle("GET /v1/state",
		RequireAgentAuth(reg,
			RateLimit(limiter, ratelimit.CategoryStateRead,
				http.HandlerFunc(handlers.ListStateKeys))))

	// Set state — agent auth, state write rate limit, requires JSON
	mux.Handle("PUT /v1/state/",
		RequireAgentAuth(reg,
			RateLimit(limiter, ratelimit.CategoryStateWrite,
				RequireJSON(
					http.HandlerFunc(handlers.SetState)))))

	// Delete state — agent auth, state write rate limit
	mux.Handle("DELETE /v1/state/",
		RequireAgentAuth(reg,
			RateLimit(limiter, ratelimit.CategoryStateWrite,
				http.HandlerFunc(handlers.DeleteState))))

	// Wrap everything with outer middleware
	var handler http.Handler = mux
	handler = MaxBodySize(maxBody, handler)
	handler = NoCORS(handler)
	handler = RequestLogger(handler)

	return handler
}
