package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/tabulum/tabulum/internal/logging"
	"github.com/tabulum/tabulum/internal/messaging"
)

var (
	ErrNotHTTPS     = errors.New("webhook URL must use HTTPS")
	ErrDeniedIP     = errors.New("webhook URL resolves to denied IP range")
	ErrInvalidURL   = errors.New("invalid webhook URL")

	deniedCIDRs []*net.IPNet
)

func init() {
	cidrs := []string{
		"127.0.0.0/8",
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"169.254.0.0/16",
		"0.0.0.0/8",
		"::1/128",
		"fc00::/7",
		"fe80::/10",
	}
	for _, cidr := range cidrs {
		_, network, err := net.ParseCIDR(cidr)
		if err == nil {
			deniedCIDRs = append(deniedCIDRs, network)
		}
	}
}

type DeliveryRequest struct {
	MessageID    string
	From         string
	To           string
	Content      string
	Timestamp    time.Time
	WebhookURL   string
	AgentAddress string
}

type DeliveryResult struct {
	Success    bool
	StatusCode int
	Error      string
}

type circuitState struct {
	failures     int
	openUntil    time.Time
}

type Delivery struct {
	client         *http.Client
	eventLog       *logging.EventLog
	fallbackQueue  *messaging.Router
	cbThreshold    int
	cbDuration     time.Duration
	maxRetries     int
	mu             sync.Mutex
	circuits       map[string]*circuitState
	done           chan struct{}
}

func NewDelivery(connectTimeout time.Duration, totalTimeout time.Duration, cbThreshold int, cbDuration time.Duration, maxRetries int) (*Delivery, error) {
	dialer := &net.Dialer{
		Timeout: connectTimeout,
	}

	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			// Resolve the hostname and check IP against denylist
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}

			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}

			for _, ip := range ips {
				if isDeniedIP(ip.IP) {
					return nil, ErrDeniedIP
				}
			}

			// Connect to the first allowed IP
			for _, ip := range ips {
				conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if err == nil {
					return conn, nil
				}
			}
			return nil, fmt.Errorf("failed to connect to %s", addr)
		},
		TLSHandshakeTimeout: connectTimeout,
		DisableKeepAlives:   true,
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   totalTimeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("redirects not allowed")
		},
	}

	return &Delivery{
		client:      client,
		cbThreshold: cbThreshold,
		cbDuration:  cbDuration,
		maxRetries:  maxRetries,
		circuits:    make(map[string]*circuitState),
		done:        make(chan struct{}),
	}, nil
}

func (d *Delivery) SetEventLog(log interface{}) {
	if el, ok := log.(*logging.EventLog); ok {
		d.eventLog = el
	}
}

func (d *Delivery) SetFallbackQueue(q interface{}) {
	if r, ok := q.(*messaging.Router); ok {
		d.fallbackQueue = r
	}
}

func (d *Delivery) Deliver(ctx context.Context, req DeliveryRequest) DeliveryResult {
	// Check circuit breaker
	d.mu.Lock()
	cs, ok := d.circuits[req.AgentAddress]
	if ok && time.Now().Before(cs.openUntil) {
		d.mu.Unlock()
		d.fallback(req)
		return DeliveryResult{Success: false, Error: "circuit breaker open"}
	}
	d.mu.Unlock()

	var lastErr error
	for attempt := 0; attempt <= d.maxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff
			backoff := time.Duration(1<<uint(attempt-1)) * time.Second
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				d.fallback(req)
				return DeliveryResult{Success: false, Error: ctx.Err().Error()}
			case <-d.done:
				d.fallback(req)
				return DeliveryResult{Success: false, Error: "delivery shutting down"}
			}
		}

		result := d.deliverOnce(ctx, req)
		if result.Success {
			// Reset circuit breaker on success
			d.mu.Lock()
			delete(d.circuits, req.AgentAddress)
			d.mu.Unlock()

			// Log delivery
			if d.eventLog != nil {
				_ = d.eventLog.Append(ctx, logging.Event{
					Type:         logging.EventMessageDelivered,
					AgentAddress: req.AgentAddress,
					Data: logging.MessageDeliveredData{
						MessageID:      req.MessageID,
						To:             req.AgentAddress,
						DeliveryMethod: "push",
					},
				})
			}
			return result
		}
		lastErr = errors.New(result.Error)
	}

	// All retries exhausted — update circuit breaker
	d.mu.Lock()
	if cs == nil {
		cs = &circuitState{}
		d.circuits[req.AgentAddress] = cs
	}
	cs.failures++
	if cs.failures >= d.cbThreshold {
		cs.openUntil = time.Now().Add(d.cbDuration)
	}
	d.mu.Unlock()

	d.fallback(req)
	return DeliveryResult{Success: false, Error: lastErr.Error()}
}

func (d *Delivery) deliverOnce(ctx context.Context, req DeliveryRequest) DeliveryResult {
	payload := map[string]interface{}{
		"message_id": req.MessageID,
		"from":       req.From,
		"to":         req.To,
		"content":    req.Content,
		"timestamp":  req.Timestamp.Format(time.RFC3339Nano),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return DeliveryResult{Success: false, Error: err.Error()}
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, req.WebhookURL, strings.NewReader(string(body)))
	if err != nil {
		return DeliveryResult{Success: false, Error: err.Error()}
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := d.client.Do(httpReq)
	if err != nil {
		return DeliveryResult{Success: false, Error: err.Error()}
	}
	resp.Body.Close() // Discard response body

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return DeliveryResult{Success: true, StatusCode: resp.StatusCode}
	}
	return DeliveryResult{Success: false, StatusCode: resp.StatusCode, Error: fmt.Sprintf("HTTP %d", resp.StatusCode)}
}

func (d *Delivery) fallback(req DeliveryRequest) {
	if d.fallbackQueue != nil {
		_ = d.fallbackQueue.QueueForPull(messaging.Message{
			ID:        req.MessageID,
			From:      req.From,
			To:        req.To,
			Content:   req.Content,
			Timestamp: req.Timestamp,
		})
	}
}

func ValidateWebhookURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ErrInvalidURL
	}

	if u.Scheme != "https" {
		return ErrNotHTTPS
	}

	host := u.Hostname()
	if host == "" {
		return ErrInvalidURL
	}

	// Resolve hostname and check all IPs
	ips, err := net.DefaultResolver.LookupIPAddr(context.Background(), host)
	if err != nil {
		return fmt.Errorf("DNS resolution failed: %w", err)
	}

	for _, ip := range ips {
		if isDeniedIP(ip.IP) {
			return ErrDeniedIP
		}
	}

	return nil
}

func isDeniedIP(ip net.IP) bool {
	for _, cidr := range deniedCIDRs {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

func (d *Delivery) Close() error {
	close(d.done)
	return nil
}
