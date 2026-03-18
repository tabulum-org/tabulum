package messaging

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/tabulum/tabulum/internal/logging"
)

var (
	ErrContentTooLong = errors.New("message content exceeds maximum length")
	ErrEmptyContent   = errors.New("message content is empty")
	ErrEmptyRecipient = errors.New("recipient address is required")
)

type Message struct {
	ID        string    `json:"message_id"`
	From      string    `json:"from"`
	To        string    `json:"to"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

// WebhookDeliverer is the interface for webhook push delivery.
type WebhookDeliverer interface {
	Deliver(ctx context.Context, req interface{}) interface{}
}

// WebhookTrigger is a function that triggers async webhook delivery.
type WebhookTrigger func(msg Message, webhookURL string)

type Router struct {
	mu               sync.RWMutex
	queues           map[string][]Message // per-agent queues
	eventLog         *logging.EventLog
	webhookTrigger   WebhookTrigger
	maxContentLength int
	maxQueuePerAgent int
}

func NewRouter(maxContentLength int, maxQueuePerAgent int) (*Router, error) {
	return &Router{
		queues:           make(map[string][]Message),
		maxContentLength: maxContentLength,
		maxQueuePerAgent: maxQueuePerAgent,
	}, nil
}

func (r *Router) SetEventLog(log interface{}) {
	if el, ok := log.(*logging.EventLog); ok {
		r.eventLog = el
	}
}

func (r *Router) SetWebhookTrigger(fn WebhookTrigger) {
	r.webhookTrigger = fn
}

func (r *Router) Send(ctx context.Context, from string, to string, content string) (*Message, error) {
	if to == "" {
		return nil, ErrEmptyRecipient
	}
	if len(content) > r.maxContentLength {
		return nil, ErrContentTooLong
	}

	msg := Message{
		ID:        uuid.New().String(),
		From:      from,
		To:        to,
		Content:   content,
		Timestamp: time.Now().UTC(),
	}

	// Log BEFORE acknowledging
	if r.eventLog != nil {
		if err := r.eventLog.Append(ctx, logging.Event{
			Type:         logging.EventMessageSent,
			AgentAddress: from,
			Data: logging.MessageSentData{
				MessageID: msg.ID,
				From:      from,
				To:        to,
				Content:   content,
				Length:    len(content),
			},
		}); err != nil {
			return nil, fmt.Errorf("log message send: %w", err)
		}
	}

	// Try webhook delivery first; only queue for pull if no webhook trigger
	if r.webhookTrigger != nil {
		r.webhookTrigger(msg, "")
	} else {
		r.queueMessage(msg)
	}

	return &msg, nil
}

func (r *Router) queueMessage(msg Message) {
	r.mu.Lock()
	defer r.mu.Unlock()

	queue := r.queues[msg.To]

	// Drop oldest if queue is full
	if len(queue) >= r.maxQueuePerAgent {
		queue = queue[1:]
	}

	r.queues[msg.To] = append(queue, msg)
}

func (r *Router) Receive(ctx context.Context, agentAddress string, limit int) (messages []Message, remaining int, err error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}

	r.mu.Lock()
	queue := r.queues[agentAddress]

	n := limit
	if n > len(queue) {
		n = len(queue)
	}

	messages = make([]Message, n)
	copy(messages, queue[:n])

	r.queues[agentAddress] = queue[n:]
	remaining = len(r.queues[agentAddress])
	if remaining == 0 {
		delete(r.queues, agentAddress)
	}
	r.mu.Unlock()

	// Log delivery events for each message
	if r.eventLog != nil {
		for _, msg := range messages {
			_ = r.eventLog.Append(ctx, logging.Event{
				Type:         logging.EventMessageDelivered,
				AgentAddress: agentAddress,
				Data: logging.MessageDeliveredData{
					MessageID:      msg.ID,
					To:             agentAddress,
					DeliveryMethod: "pull",
				},
			})
		}
	}

	return messages, remaining, nil
}

func (r *Router) QueueForPull(msg Message) error {
	r.queueMessage(msg)
	return nil
}

func (r *Router) Close() error {
	return nil
}

// --- Recovery ---

func (r *Router) ApplyMessageSent(msg Message) error {
	r.queueMessage(msg)
	return nil
}

func (r *Router) ApplyMessageDelivered(messageID string, to string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	queue := r.queues[to]
	for i, msg := range queue {
		if msg.ID == messageID {
			r.queues[to] = append(queue[:i], queue[i+1:]...)
			if len(r.queues[to]) == 0 {
				delete(r.queues, to)
			}
			return nil
		}
	}
	return nil
}
