package verification

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
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

// --- Pipeline implementation ---

// PipelineVerifier generates challenges consisting of chained string operations.
// Any entity capable of executing code can solve these trivially.
// A human using curl alone cannot.
type PipelineVerifier struct {
	ChallengeExpiry time.Duration
	mu              sync.Mutex
	pending         map[string]pendingChallenge
	done            chan struct{}
}

type pendingChallenge struct {
	expected  string
	expiresAt time.Time
}

// Available pipeline operations (no arguments).
var simpleOps = []string{
	"reverse",
	"base64_encode",
	"hex_encode",
	"sha256",
	"uppercase",
	"lowercase",
	"rot13",
}

func NewPipelineVerifier(challengeExpiry time.Duration) *PipelineVerifier {
	v := &PipelineVerifier{
		ChallengeExpiry: challengeExpiry,
		pending:         make(map[string]pendingChallenge),
		done:            make(chan struct{}),
	}
	go v.cleanup()
	return v
}

func (v *PipelineVerifier) cleanup() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			v.mu.Lock()
			now := time.Now()
			for id, p := range v.pending {
				if now.After(p.expiresAt) {
					delete(v.pending, id)
				}
			}
			v.mu.Unlock()
		case <-v.done:
			return
		}
	}
}

func (v *PipelineVerifier) Close() {
	close(v.done)
}

func (v *PipelineVerifier) GenerateChallenge(_ context.Context) (Challenge, error) {
	seed := randomHex(16)
	ops := generateOperations()

	// Compute the expected result server-side.
	expected, err := executePipeline(seed, ops)
	if err != nil {
		return Challenge{}, fmt.Errorf("compute pipeline result: %w", err)
	}

	id := uuid.New().String()
	expiresAt := time.Now().Add(v.ChallengeExpiry)

	v.mu.Lock()
	v.pending[id] = pendingChallenge{expected: expected, expiresAt: expiresAt}
	v.mu.Unlock()

	// Build the operations list for the client.
	opsData := make([]map[string]string, len(ops))
	for i, op := range ops {
		opsData[i] = map[string]string{"op": op}
	}

	ch := Challenge{
		ID:   id,
		Type: "pipeline",
		Data: map[string]any{
			"seed":       seed,
			"operations": opsData,
		},
		ExpiresAt: expiresAt,
	}

	return ch, nil
}

func (v *PipelineVerifier) VerifyResponse(_ context.Context, challengeID string, response any) (bool, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	p, ok := v.pending[challengeID]
	if !ok {
		return false, ErrChallengeNotFound
	}

	if time.Now().After(p.expiresAt) {
		delete(v.pending, challengeID)
		return false, ErrChallengeExpired
	}

	delete(v.pending, challengeID)

	// Extract the response string.
	resp, ok := extractResponseString(response)
	if !ok {
		return false, nil
	}

	return resp == p.expected, nil
}

// extractResponseString pulls the "response" field from the verification response.
func extractResponseString(response any) (string, bool) {
	switch v := response.(type) {
	case string:
		return v, true
	case map[string]any:
		if r, ok := v["response"]; ok {
			if s, ok := r.(string); ok {
				return s, true
			}
		}
	}
	return "", false
}

// generateOperations produces a random pipeline of 8 operations.
func generateOperations() []string {
	n := 8
	ops := make([]string, n)
	for i := range ops {
		// 70% chance of a simple op, 30% chance of prepend/append.
		if randomInt(1, 10) <= 7 {
			ops[i] = simpleOps[randomInt(0, len(simpleOps)-1)]
		} else {
			suffix := randomHex(4)
			if randomInt(0, 1) == 0 {
				ops[i] = "prepend:" + suffix
			} else {
				ops[i] = "append:" + suffix
			}
		}
	}
	return ops
}

// executePipeline applies the operation chain to the seed.
func executePipeline(seed string, ops []string) (string, error) {
	result := seed
	for _, op := range ops {
		switch {
		case op == "reverse":
			runes := []rune(result)
			for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
				runes[i], runes[j] = runes[j], runes[i]
			}
			result = string(runes)

		case op == "base64_encode":
			result = base64.StdEncoding.EncodeToString([]byte(result))

		case op == "base64_decode":
			decoded, err := base64.StdEncoding.DecodeString(result)
			if err != nil {
				return "", fmt.Errorf("base64_decode: %w", err)
			}
			result = string(decoded)

		case op == "hex_encode":
			result = hex.EncodeToString([]byte(result))

		case op == "sha256":
			h := sha256.Sum256([]byte(result))
			result = hex.EncodeToString(h[:])

		case op == "uppercase":
			result = strings.ToUpper(result)

		case op == "lowercase":
			result = strings.ToLower(result)

		case op == "rot13":
			result = rot13(result)

		case strings.HasPrefix(op, "prepend:"):
			result = op[8:] + result

		case strings.HasPrefix(op, "append:"):
			result = result + op[7:]

		default:
			return "", fmt.Errorf("unknown operation: %s", op)
		}
	}
	return result, nil
}

func rot13(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'A' && r <= 'Z':
			return 'A' + (r-'A'+13)%26
		case r >= 'a' && r <= 'z':
			return 'a' + (r-'a'+13)%26
		default:
			return r
		}
	}, s)
}

func randomHex(nBytes int) string {
	b := make([]byte, nBytes)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func randomInt(min, max int) int {
	n, _ := rand.Int(rand.Reader, big.NewInt(int64(max-min+1)))
	return int(n.Int64()) + min
}
