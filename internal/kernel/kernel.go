package kernel

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/tabulum/tabulum/internal/api"
	"github.com/tabulum/tabulum/internal/config"
	"github.com/tabulum/tabulum/internal/logging"
	"github.com/tabulum/tabulum/internal/messaging"
	"github.com/tabulum/tabulum/internal/ratelimit"
	"github.com/tabulum/tabulum/internal/registry"
	"github.com/tabulum/tabulum/internal/state"
	"github.com/tabulum/tabulum/internal/verification"
	"github.com/tabulum/tabulum/internal/version"
	"github.com/tabulum/tabulum/internal/webhook"
)

type Kernel struct {
	config     config.Config
	eventLog   *logging.EventLog
	stateStore *state.Store
	registry   *registry.Registry
	router     *messaging.Router
	limiter    *ratelimit.Limiter
	webhook    *webhook.Delivery
	verifier   verification.Verifier
	httpServer *http.Server
}

func New(configPath string) (*Kernel, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	return NewWithConfig(cfg)
}

// NewWithConfig creates a kernel from an existing config (useful for testing).
func NewWithConfig(cfg config.Config) (*Kernel, error) {
	el, err := logging.NewEventLog(cfg.EventLog.DataDir, cfg.EventLog.MaxFileSize)
	if err != nil {
		return nil, fmt.Errorf("open event log: %w", err)
	}

	ss, err := state.NewStore(cfg.State.DataDir, cfg.State.MaxCapacityBytes, cfg.State.MaxKeyLength, cfg.State.MaxValueLength)
	if err != nil {
		el.Close()
		return nil, fmt.Errorf("open state store: %w", err)
	}

	reg, err := registry.NewRegistry(cfg.State.DataDir)
	if err != nil {
		ss.Close()
		el.Close()
		return nil, fmt.Errorf("open registry: %w", err)
	}

	msgRouter, err := messaging.NewRouter(cfg.Messaging.MaxContentLength, cfg.Messaging.MaxQueuePerAgent)
	if err != nil {
		reg.Close()
		ss.Close()
		el.Close()
		return nil, fmt.Errorf("create message router: %w", err)
	}

	// Replay event log — only message events for queue recovery.
	// bbolt is the source of truth for registry and state; they survive restarts natively.
	log.Println("Replaying event log for message queue recovery...")
	eventCount := 0
	err = el.Replay(context.Background(), func(event logging.Event) error {
		eventCount++
		switch event.Type {
		case logging.EventMessageSent:
			data, ok := event.Data.(logging.MessageSentData)
			if !ok {
				raw, _ := json.Marshal(event.Data)
				if err := json.Unmarshal(raw, &data); err != nil {
					return nil // skip malformed events
				}
			}
			return msgRouter.ApplyMessageSent(messaging.Message{
				ID:        data.MessageID,
				From:      data.From,
				To:        data.To,
				Content:   data.Content,
				Timestamp: event.Timestamp,
			})

		case logging.EventMessageDelivered:
			data, ok := event.Data.(logging.MessageDeliveredData)
			if !ok {
				raw, _ := json.Marshal(event.Data)
				if err := json.Unmarshal(raw, &data); err != nil {
					return nil
				}
			}
			return msgRouter.ApplyMessageDelivered(data.MessageID, data.To)
		}
		return nil
	})
	if err != nil {
		msgRouter.Close()
		reg.Close()
		ss.Close()
		el.Close()
		return nil, fmt.Errorf("replay event log: %w", err)
	}
	log.Printf("Recovery complete: replayed %d events", eventCount)

	lim := ratelimit.NewLimiter(map[ratelimit.Category]int{
		ratelimit.CategoryMessageSend:  cfg.RateLimits.MessageSendPerMin,
		ratelimit.CategoryStateRead:    cfg.RateLimits.StateReadPerMin,
		ratelimit.CategoryStateWrite:   cfg.RateLimits.StateWritePerMin,
		ratelimit.CategoryRegistryRead: cfg.RateLimits.RegistryReadPerMin,
		ratelimit.CategoryWebhookOut:   cfg.RateLimits.WebhookOutboundPerMin,
	})

	wh, err := webhook.NewDelivery(
		cfg.Webhook.ConnectTimeout,
		cfg.Webhook.TotalTimeout,
		cfg.Webhook.CircuitBreakerThreshold,
		cfg.Webhook.CircuitBreakerDuration,
		cfg.Webhook.MaxRetries,
	)
	if err != nil {
		lim.Close()
		msgRouter.Close()
		reg.Close()
		ss.Close()
		el.Close()
		return nil, fmt.Errorf("create webhook delivery: %w", err)
	}

	var verifier verification.Verifier
	switch cfg.Verification.GateType {
	case "pipeline":
		verifier = verification.NewPipelineVerifier(cfg.Verification.ChallengeExpiry)
	case "stub":
		verifier = verification.NewStubVerifier(cfg.Verification.ChallengeExpiry)
	default:
		verifier = verification.NewPipelineVerifier(cfg.Verification.ChallengeExpiry)
	}

	// Wire cross-references
	ss.SetEventLog(el)
	reg.SetEventLog(el)
	reg.SetVerifier(verifier)
	msgRouter.SetEventLog(el)
	wh.SetEventLog(el)
	wh.SetFallbackQueue(msgRouter)

	// Wire webhook trigger into messaging router.
	// For agents with webhook URLs: deliver via webhook (fallback to pull on failure).
	// For agents without: queue for pull delivery.
	msgRouter.SetWebhookTrigger(func(msg messaging.Message, _ string) {
		agent, err := reg.GetAgent(context.Background(), msg.To)
		if err != nil || agent.WebhookURL == "" {
			msgRouter.QueueForPull(msg)
			return
		}
		go wh.Deliver(context.Background(), webhook.DeliveryRequest{
			MessageID:    msg.ID,
			From:         msg.From,
			To:           msg.To,
			Content:      msg.Content,
			Timestamp:    msg.Timestamp,
			WebhookURL:   agent.WebhookURL,
			AgentAddress: msg.To,
		})
	})

	handlers := api.NewHandlers(reg, msgRouter, ss, verifier, lim, cfg)
	httpRouter := api.NewRouter(handlers, reg, lim, cfg.RateLimits.OperatorRegPerHourPerIP)

	server := &http.Server{
		Addr:         cfg.Server.ListenAddr,
		Handler:      httpRouter,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	return &Kernel{
		config:     cfg,
		eventLog:   el,
		stateStore: ss,
		registry:   reg,
		router:     msgRouter,
		limiter:    lim,
		webhook:    wh,
		verifier:   verifier,
		httpServer: server,
	}, nil
}

// Handler returns the HTTP handler for use with httptest.
func (k *Kernel) Handler() http.Handler {
	return k.httpServer.Handler
}

func (k *Kernel) Start() error {
	log.Printf("Tabulum kernel %s starting on %s", Version(), k.config.Server.ListenAddr)
	if err := k.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (k *Kernel) Shutdown(ctx context.Context) error {
	log.Println("Shutting down kernel...")

	if err := k.httpServer.Shutdown(ctx); err != nil {
		log.Printf("HTTP server shutdown error: %v", err)
	}

	if err := k.webhook.Close(); err != nil {
		log.Printf("Webhook close error: %v", err)
	}

	k.limiter.Close()
	k.router.Close()

	if err := k.stateStore.Close(); err != nil {
		log.Printf("State store close error: %v", err)
	}

	if err := k.registry.Close(); err != nil {
		log.Printf("Registry close error: %v", err)
	}

	if err := k.eventLog.Close(); err != nil {
		log.Printf("Event log close error: %v", err)
	}

	log.Println("Kernel shutdown complete")
	return nil
}

func Version() string {
	return version.Version
}
