package observe

import (
	"net/http"
	"strings"
)

func NewRouter(handlers *Handlers, wsHub *WebSocketHub, limiter *IPRateLimiter, adminHandlers *AdminHandlers, adminKey string) http.Handler {
	mux := http.NewServeMux()

	// Health check — no rate limit
	mux.HandleFunc("GET /observe/health", handlers.Health)

	// Events
	mux.HandleFunc("GET /observe/events", handlers.StreamEvents)

	// WebSocket event stream
	mux.HandleFunc("GET /observe/events/stream", wsHub.HandleWebSocket)

	// State — list and read
	mux.HandleFunc("GET /observe/state", handlers.ListState)
	mux.HandleFunc("GET /observe/state/", func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.URL.Path, "/observe/state/")
		if key == "" || key == "/" {
			handlers.ListState(w, r)
		} else {
			handlers.ReadState(w, r)
		}
	})

	// Agents — list and detail
	mux.HandleFunc("GET /observe/agents", handlers.ListAgents)
	mux.HandleFunc("GET /observe/agents/", func(w http.ResponseWriter, r *http.Request) {
		addr := strings.TrimPrefix(r.URL.Path, "/observe/agents/")
		if addr == "" || addr == "/" {
			handlers.ListAgents(w, r)
		} else {
			handlers.AgentDetail(w, r)
		}
	})

	// Network graph
	mux.HandleFunc("GET /observe/network", handlers.Network)

	// Transparency log
	mux.HandleFunc("GET /observe/transparency", handlers.Transparency)

	// Admin routes — no rate limit, no CORS
	if adminKey != "" && adminHandlers != nil {
		adminAuth := RequireAdminAuth(adminKey)
		mux.Handle("POST /admin/remove", adminAuth(http.HandlerFunc(adminHandlers.AdminRemove)))
		mux.Handle("GET /admin/removals", adminAuth(http.HandlerFunc(adminHandlers.AdminListRemovals)))
	}

	// Wrap public routes with middleware
	var handler http.Handler = mux
	handler = CORSMiddleware(handler)
	handler = RateLimitMiddleware(limiter)(handler)
	handler = RequestLogger(handler)

	return handler
}
