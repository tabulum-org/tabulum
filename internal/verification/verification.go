package verification

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
)

var (
	ErrChallengeNotFound = errors.New("challenge not found")
	ErrChallengeExpired  = errors.New("challenge has expired")
)

type Challenge struct {
	ID        string    `json:"challenge_id"`
	Type      string    `json:"challenge_type"`
	Data      any       `json:"challenge_data"`
	ExpiresAt time.Time `json:"expires_at"`
}

type Verifier interface {
	GenerateChallenge(ctx context.Context) (Challenge, error)
	VerifyResponse(ctx context.Context, challengeID string, response any) (bool, error)
}

// --- Stub implementation ---

type StubVerifier struct {
	ChallengeExpiry time.Duration
	mu              sync.Mutex
	challenges      map[string]Challenge
	done            chan struct{}
}

func NewStubVerifier(challengeExpiry time.Duration) *StubVerifier {
	v := &StubVerifier{
		ChallengeExpiry: challengeExpiry,
		challenges:      make(map[string]Challenge),
		done:            make(chan struct{}),
	}
	go v.cleanup()
	return v
}

func (v *StubVerifier) cleanup() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			v.mu.Lock()
			now := time.Now()
			for id, ch := range v.challenges {
				if now.After(ch.ExpiresAt) {
					delete(v.challenges, id)
				}
			}
			v.mu.Unlock()
		case <-v.done:
			return
		}
	}
}

func (v *StubVerifier) GenerateChallenge(_ context.Context) (Challenge, error) {
	ch := Challenge{
		ID:        uuid.New().String(),
		Type:      "stub",
		Data:      map[string]string{"instruction": "any response will be accepted"},
		ExpiresAt: time.Now().Add(v.ChallengeExpiry),
	}

	v.mu.Lock()
	v.challenges[ch.ID] = ch
	v.mu.Unlock()

	return ch, nil
}

func (v *StubVerifier) VerifyResponse(_ context.Context, challengeID string, _ any) (bool, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	ch, ok := v.challenges[challengeID]
	if !ok {
		return false, ErrChallengeNotFound
	}

	if time.Now().After(ch.ExpiresAt) {
		delete(v.challenges, challengeID)
		return false, ErrChallengeExpired
	}

	// Stub always passes
	delete(v.challenges, challengeID)
	return true, nil
}

func (v *StubVerifier) Close() {
	close(v.done)
}

// --- Proof-of-work implementation (optional) ---

type ProofOfWorkVerifier struct {
	Difficulty      int
	TimeWindow      time.Duration
	ChallengeExpiry time.Duration
	mu              sync.Mutex
	challenges      map[string]Challenge
}

func NewProofOfWorkVerifier(difficulty int, timeWindow time.Duration, challengeExpiry time.Duration) *ProofOfWorkVerifier {
	return &ProofOfWorkVerifier{
		Difficulty:      difficulty,
		TimeWindow:      timeWindow,
		ChallengeExpiry: challengeExpiry,
		challenges:      make(map[string]Challenge),
	}
}

func (v *ProofOfWorkVerifier) GenerateChallenge(_ context.Context) (Challenge, error) {
	ch := Challenge{
		ID:   uuid.New().String(),
		Type: "proof_of_work",
		Data: map[string]interface{}{
			"difficulty": v.Difficulty,
			"prefix":     uuid.New().String(),
		},
		ExpiresAt: time.Now().Add(v.ChallengeExpiry),
	}

	v.mu.Lock()
	v.challenges[ch.ID] = ch
	v.mu.Unlock()

	return ch, nil
}

func (v *ProofOfWorkVerifier) VerifyResponse(_ context.Context, challengeID string, _ any) (bool, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	ch, ok := v.challenges[challengeID]
	if !ok {
		return false, ErrChallengeNotFound
	}

	if time.Now().After(ch.ExpiresAt) {
		delete(v.challenges, challengeID)
		return false, ErrChallengeExpired
	}

	// For MVP, proof-of-work verification is not fully implemented
	delete(v.challenges, challengeID)
	return false, errors.New("proof of work verification not implemented")
}
