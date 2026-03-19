package observe

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	bolt "go.etcd.io/bbolt"
)

type Server struct {
	config       Config
	events       *EventReader
	stateDB      *bolt.DB
	registryDB   *bolt.DB
	transparency *TransparencyLog
	limiter      *IPRateLimiter
	wsHub        *WebSocketHub
	httpServer   *http.Server
}

func NewServer(cfg Config) (*Server, error) {
	// Open event reader
	events, err := NewEventReader(cfg.Kernel.EventLogDir)
	if err != nil {
		return nil, fmt.Errorf("open event reader: %w", err)
	}

	// Open state DB read-only
	var stateDB *bolt.DB
	if cfg.Kernel.StateDBPath != "" {
		stateDB, err = bolt.Open(cfg.Kernel.StateDBPath, 0o644, &bolt.Options{ReadOnly: true, Timeout: 1 * time.Second})
		if err != nil {
			log.Printf("[observe] Warning: could not open state DB: %v (state endpoints will return empty)", err)
			stateDB = nil
		}
	}

	// Open registry DB read-only
	var registryDB *bolt.DB
	if cfg.Kernel.RegistryDBPath != "" {
		registryDB, err = bolt.Open(cfg.Kernel.RegistryDBPath, 0o644, &bolt.Options{ReadOnly: true, Timeout: 1 * time.Second})
		if err != nil {
			log.Printf("[observe] Warning: could not open registry DB: %v", err)
			registryDB = nil
		}
	}

	// Open transparency log
	transparency, err := NewTransparencyLog(cfg.Kernel.TransparencyLogPath)
	if err != nil {
		return nil, fmt.Errorf("open transparency log: %w", err)
	}

	// Create rate limiter
	limiter := NewIPRateLimiter(cfg.RateLimit.RequestsPerMinPerIP)

	// Create handlers
	handlers := NewHandlers(events, stateDB, registryDB, transparency)

	// Create WebSocket hub
	wsHub := NewWebSocketHub(events, transparency, cfg.WebSocket)

	// Load admin key and create admin handlers
	var adminHandlers *AdminHandlers
	var adminKey string
	if cfg.Admin.Enabled {
		adminKey = os.Getenv("TABULUM_ADMIN_KEY")
		if adminKey == "" {
			return nil, fmt.Errorf("admin is enabled but TABULUM_ADMIN_KEY environment variable is not set")
		}
		adminHandlers = NewAdminHandlers(cfg.Kernel.EventLogDir, cfg.Kernel.TransparencyLogPath, transparency)
		log.Println("[observe] Admin API enabled")
	}

	// Create router
	router := NewRouter(handlers, wsHub, limiter, adminHandlers, adminKey)

	httpServer := &http.Server{
		Addr:         cfg.Server.ListenAddr,
		Handler:      router,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	return &Server{
		config:       cfg,
		events:       events,
		stateDB:      stateDB,
		registryDB:   registryDB,
		transparency: transparency,
		limiter:      limiter,
		wsHub:        wsHub,
		httpServer:   httpServer,
	}, nil
}

// Handler returns the HTTP handler for use with httptest.
func (s *Server) Handler() http.Handler {
	return s.httpServer.Handler
}

func (s *Server) Start() error {
	log.Printf("[observe] Observation API starting on %s", s.config.Server.ListenAddr)
	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	log.Println("[observe] Shutting down observation API...")

	if err := s.httpServer.Shutdown(ctx); err != nil {
		log.Printf("[observe] HTTP server shutdown error: %v", err)
	}

	s.wsHub.Close()
	s.limiter.Close()

	if s.stateDB != nil {
		s.stateDB.Close()
	}
	if s.registryDB != nil {
		s.registryDB.Close()
	}

	log.Println("[observe] Observation API shutdown complete")
	return nil
}
