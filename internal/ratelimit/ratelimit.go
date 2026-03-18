package ratelimit

import (
	"context"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type Category string

const (
	CategoryMessageSend  Category = "message_send"
	CategoryStateRead    Category = "state_read"
	CategoryStateWrite   Category = "state_write"
	CategoryRegistryRead Category = "registry_read"
	CategoryWebhookOut   Category = "webhook_out"
)

type Result struct {
	Allowed    bool
	Limit      int
	Remaining  int
	ResetAt    time.Time
	RetryAfter time.Duration
}

type agentBuckets struct {
	buckets  map[Category]*rate.Limiter
	lastSeen time.Time
}

type Limiter struct {
	mu       sync.Mutex
	agents   map[string]*agentBuckets
	ipLimits map[string]*rate.Limiter
	limits   map[Category]int
	done     chan struct{}
}

func NewLimiter(limits map[Category]int) *Limiter {
	l := &Limiter{
		agents:   make(map[string]*agentBuckets),
		ipLimits: make(map[string]*rate.Limiter),
		limits:   limits,
		done:     make(chan struct{}),
	}

	// Start cleanup goroutine for stale entries
	go l.cleanup()

	return l
}

func (l *Limiter) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			l.mu.Lock()
			cutoff := time.Now().Add(-30 * time.Minute)
			for addr, ab := range l.agents {
				if ab.lastSeen.Before(cutoff) {
					delete(l.agents, addr)
				}
			}
			l.mu.Unlock()
		case <-l.done:
			return
		}
	}
}

func (l *Limiter) getBucket(agentAddress string, category Category) *rate.Limiter {
	l.mu.Lock()
	defer l.mu.Unlock()

	ab, ok := l.agents[agentAddress]
	if !ok {
		ab = &agentBuckets{
			buckets: make(map[Category]*rate.Limiter),
		}
		l.agents[agentAddress] = ab
	}
	ab.lastSeen = time.Now()

	bucket, ok := ab.buckets[category]
	if !ok {
		perMin := l.limits[category]
		if perMin <= 0 {
			perMin = 60
		}
		// Token bucket: rate is per-second, burst allows the full per-minute amount
		r := rate.Limit(float64(perMin) / 60.0)
		bucket = rate.NewLimiter(r, perMin)
		ab.buckets[category] = bucket
	}

	return bucket
}

func (l *Limiter) Allow(_ context.Context, agentAddress string, category Category) Result {
	bucket := l.getBucket(agentAddress, category)
	perMin := l.limits[category]
	if perMin <= 0 {
		perMin = 60
	}

	now := time.Now()
	res := bucket.Reserve()
	if !res.OK() {
		return Result{
			Allowed:    false,
			Limit:      perMin,
			Remaining:  0,
			ResetAt:    now.Add(time.Minute),
			RetryAfter: time.Minute,
		}
	}

	delay := res.Delay()
	if delay > 0 {
		res.Cancel()
		return Result{
			Allowed:    false,
			Limit:      perMin,
			Remaining:  0,
			ResetAt:    now.Add(delay),
			RetryAfter: delay,
		}
	}

	// Estimate remaining tokens
	remaining := int(bucket.Tokens())
	if remaining < 0 {
		remaining = 0
	}

	return Result{
		Allowed:   true,
		Limit:     perMin,
		Remaining: remaining,
		ResetAt:   now.Add(time.Minute),
	}
}

func (l *Limiter) IPAllow(_ context.Context, ip string, limit int) Result {
	l.mu.Lock()
	bucket, ok := l.ipLimits[ip]
	if !ok {
		// limit is per hour, convert to per-second rate
		r := rate.Limit(float64(limit) / 3600.0)
		bucket = rate.NewLimiter(r, limit)
		l.ipLimits[ip] = bucket
	}
	l.mu.Unlock()

	now := time.Now()
	res := bucket.Reserve()
	if !res.OK() {
		return Result{
			Allowed:    false,
			Limit:      limit,
			Remaining:  0,
			ResetAt:    now.Add(time.Hour),
			RetryAfter: time.Hour,
		}
	}

	delay := res.Delay()
	if delay > 0 {
		res.Cancel()
		return Result{
			Allowed:    false,
			Limit:      limit,
			Remaining:  0,
			ResetAt:    now.Add(delay),
			RetryAfter: delay,
		}
	}

	remaining := int(bucket.Tokens())
	if remaining < 0 {
		remaining = 0
	}

	return Result{
		Allowed:   true,
		Limit:     limit,
		Remaining: remaining,
		ResetAt:   now.Add(time.Hour),
	}
}

func (l *Limiter) Reset(agentAddress string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.agents, agentAddress)
}

func (l *Limiter) Close() {
	close(l.done)
}
