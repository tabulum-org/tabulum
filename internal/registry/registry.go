package registry

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/tabulum/tabulum/internal/logging"
	"github.com/tabulum/tabulum/internal/verification"
	bolt "go.etcd.io/bbolt"
	"golang.org/x/crypto/bcrypt"
)

var (
	operatorBucket      = []byte("operators")
	agentBucket         = []byte("agents")
	operatorIndexBucket = []byte("operator_key_index")
	agentIndexBucket    = []byte("agent_token_index")

	ErrOperatorNotFound = errors.New("operator not found")
	ErrAgentNotFound    = errors.New("agent not found")
	ErrInvalidAPIKey    = errors.New("invalid API key")
	ErrInvalidToken     = errors.New("invalid agent token")
)

// tokenPrefix extracts the first 8 hex chars after the prefix (e.g. "sk_live_" or "at_live_")
// for use as a fast lookup index. This is not secret — it only narrows the search.
func tokenPrefix(token string) string {
	// Skip the prefix ("sk_live_" or "at_live_" = 8 chars)
	if len(token) > 16 {
		return token[8:16]
	}
	return token
}

type Operator struct {
	ID          string    `json:"id"`
	APIKeyHash  string    `json:"-"`
	ContactHash string    `json:"contact_hash"`
	CreatedAt   time.Time `json:"created_at"`
}

// operatorRecord is the internal stored format (includes hash).
type operatorRecord struct {
	ID          string    `json:"id"`
	APIKeyHash  string    `json:"api_key_hash"`
	ContactHash string    `json:"contact_hash"`
	CreatedAt   time.Time `json:"created_at"`
}

type Agent struct {
	Address      string    `json:"address"`
	OperatorID   string    `json:"operator_id"`
	TokenHash    string    `json:"-"`
	WebhookURL   string    `json:"webhook_url"`
	RegisteredAt time.Time `json:"registered_at"`
}

// agentRecord is the internal stored format (includes hash).
type agentRecord struct {
	Address      string    `json:"address"`
	OperatorID   string    `json:"operator_id"`
	TokenHash    string    `json:"token_hash"`
	WebhookURL   string    `json:"webhook_url"`
	RegisteredAt time.Time `json:"registered_at"`
}

type AgentInfo struct {
	Address      string    `json:"address"`
	RegisteredAt time.Time `json:"registered_at"`
}

type Registry struct {
	db       *bolt.DB
	eventLog *logging.EventLog
	verifier verification.Verifier
}

func NewRegistry(dataDir string) (*Registry, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create registry dir: %w", err)
	}

	dbPath := filepath.Join(dataDir, "registry.db")
	db, err := bolt.Open(dbPath, 0o644, &bolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("open registry db: %w", err)
	}

	if err := db.Update(func(tx *bolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists(operatorBucket); err != nil {
			return err
		}
		if _, err := tx.CreateBucketIfNotExists(agentBucket); err != nil {
			return err
		}
		if _, err := tx.CreateBucketIfNotExists(operatorIndexBucket); err != nil {
			return err
		}
		_, err := tx.CreateBucketIfNotExists(agentIndexBucket)
		return err
	}); err != nil {
		db.Close()
		return nil, err
	}

	return &Registry{db: db}, nil
}

func (r *Registry) SetEventLog(log interface{}) {
	if el, ok := log.(*logging.EventLog); ok {
		r.eventLog = el
	}
}

func (r *Registry) SetVerifier(v interface{}) {
	if ver, ok := v.(verification.Verifier); ok {
		r.verifier = ver
	}
}

func (r *Registry) GetVerifier() verification.Verifier {
	return r.verifier
}

// --- Operator operations ---

func (r *Registry) CreateOperator(ctx context.Context, contactHash string) (*Operator, string, error) {
	id := uuid.New().String()

	// Generate API key: sk_live_<32 hex chars>
	rawKey := make([]byte, 32)
	if _, err := rand.Read(rawKey); err != nil {
		return nil, "", fmt.Errorf("generate API key: %w", err)
	}
	apiKey := "sk_live_" + hex.EncodeToString(rawKey)

	hash, err := bcrypt.GenerateFromPassword([]byte(apiKey), bcrypt.DefaultCost)
	if err != nil {
		return nil, "", fmt.Errorf("hash API key: %w", err)
	}

	now := time.Now().UTC()
	rec := operatorRecord{
		ID:          id,
		APIKeyHash:  string(hash),
		ContactHash: contactHash,
		CreatedAt:   now,
	}

	// Log event BEFORE writing to db
	if r.eventLog != nil {
		if err := r.eventLog.Append(ctx, logging.Event{
			Type: logging.EventOperatorCreated,
			Data: logging.OperatorCreatedData{
				OperatorID:    id,
				ContactHash:   contactHash,
				AcceptedTerms: true,
			},
		}); err != nil {
			return nil, "", fmt.Errorf("log operator creation: %w", err)
		}
	}

	data, err := json.Marshal(rec)
	if err != nil {
		return nil, "", err
	}

	prefix := tokenPrefix(apiKey)
	if err := r.db.Update(func(tx *bolt.Tx) error {
		if err := tx.Bucket(operatorBucket).Put([]byte(id), data); err != nil {
			return err
		}
		// Store prefix -> operator ID index for fast auth lookup
		return tx.Bucket(operatorIndexBucket).Put([]byte(prefix), []byte(id))
	}); err != nil {
		return nil, "", err
	}

	op := &Operator{
		ID:          id,
		ContactHash: contactHash,
		CreatedAt:   now,
	}
	return op, apiKey, nil
}

func (r *Registry) AuthenticateOperator(_ context.Context, apiKey string) (*Operator, error) {
	var found *Operator
	prefix := tokenPrefix(apiKey)

	err := r.db.View(func(tx *bolt.Tx) error {
		// Fast path: look up by prefix index
		opID := tx.Bucket(operatorIndexBucket).Get([]byte(prefix))
		if opID != nil {
			data := tx.Bucket(operatorBucket).Get(opID)
			if data != nil {
				var rec operatorRecord
				if err := json.Unmarshal(data, &rec); err == nil {
					if bcrypt.CompareHashAndPassword([]byte(rec.APIKeyHash), []byte(apiKey)) == nil {
						found = &Operator{
							ID:          rec.ID,
							ContactHash: rec.ContactHash,
							CreatedAt:   rec.CreatedAt,
						}
						return nil
					}
				}
			}
		}

		// Slow fallback: scan all records (handles prefix collisions or missing index)
		b := tx.Bucket(operatorBucket)
		c := b.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var rec operatorRecord
			if err := json.Unmarshal(v, &rec); err != nil {
				continue
			}
			if bcrypt.CompareHashAndPassword([]byte(rec.APIKeyHash), []byte(apiKey)) == nil {
				found = &Operator{
					ID:          rec.ID,
					ContactHash: rec.ContactHash,
					CreatedAt:   rec.CreatedAt,
				}
				return nil
			}
		}
		return ErrInvalidAPIKey
	})

	if err != nil {
		return nil, err
	}
	return found, nil
}

// --- Agent operations ---

const alphanumeric = "abcdefghijklmnopqrstuvwxyz0123456789"

func generateAgentAddress() (string, error) {
	b := make([]byte, 20)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphanumeric))))
		if err != nil {
			return "", err
		}
		b[i] = alphanumeric[n.Int64()]
	}
	return "tab_" + string(b), nil
}

func (r *Registry) RegisterAgent(ctx context.Context, operatorID string, webhookURL string) (*Agent, string, error) {
	address, err := generateAgentAddress()
	if err != nil {
		return nil, "", fmt.Errorf("generate address: %w", err)
	}

	// Generate agent token: at_live_<32 hex chars>
	rawToken := make([]byte, 32)
	if _, err := rand.Read(rawToken); err != nil {
		return nil, "", fmt.Errorf("generate agent token: %w", err)
	}
	agentToken := "at_live_" + hex.EncodeToString(rawToken)

	hash, err := bcrypt.GenerateFromPassword([]byte(agentToken), bcrypt.DefaultCost)
	if err != nil {
		return nil, "", fmt.Errorf("hash agent token: %w", err)
	}

	now := time.Now().UTC()
	rec := agentRecord{
		Address:      address,
		OperatorID:   operatorID,
		TokenHash:    string(hash),
		WebhookURL:   webhookURL,
		RegisteredAt: now,
	}

	// Log event BEFORE writing to db
	if r.eventLog != nil {
		if err := r.eventLog.Append(ctx, logging.Event{
			Type:         logging.EventAgentRegistered,
			AgentAddress: address,
			Data: logging.AgentRegisteredData{
				Address:    address,
				OperatorID: operatorID,
				HasWebhook: webhookURL != "",
			},
		}); err != nil {
			return nil, "", fmt.Errorf("log agent registration: %w", err)
		}
	}

	data, err := json.Marshal(rec)
	if err != nil {
		return nil, "", err
	}

	prefix := tokenPrefix(agentToken)
	if err := r.db.Update(func(tx *bolt.Tx) error {
		if err := tx.Bucket(agentBucket).Put([]byte(address), data); err != nil {
			return err
		}
		// Store prefix -> agent address index for fast auth lookup
		return tx.Bucket(agentIndexBucket).Put([]byte(prefix), []byte(address))
	}); err != nil {
		return nil, "", err
	}

	agent := &Agent{
		Address:      address,
		OperatorID:   operatorID,
		WebhookURL:   webhookURL,
		RegisteredAt: now,
	}
	return agent, agentToken, nil
}

func (r *Registry) AuthenticateAgent(_ context.Context, agentToken string) (*Agent, error) {
	var found *Agent
	prefix := tokenPrefix(agentToken)

	err := r.db.View(func(tx *bolt.Tx) error {
		// Fast path: look up by prefix index
		addr := tx.Bucket(agentIndexBucket).Get([]byte(prefix))
		if addr != nil {
			data := tx.Bucket(agentBucket).Get(addr)
			if data != nil {
				var rec agentRecord
				if err := json.Unmarshal(data, &rec); err == nil {
					if bcrypt.CompareHashAndPassword([]byte(rec.TokenHash), []byte(agentToken)) == nil {
						found = &Agent{
							Address:      rec.Address,
							OperatorID:   rec.OperatorID,
							WebhookURL:   rec.WebhookURL,
							RegisteredAt: rec.RegisteredAt,
						}
						return nil
					}
				}
			}
		}

		// Slow fallback: scan all records (handles prefix collisions or missing index)
		b := tx.Bucket(agentBucket)
		c := b.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var rec agentRecord
			if err := json.Unmarshal(v, &rec); err != nil {
				continue
			}
			if bcrypt.CompareHashAndPassword([]byte(rec.TokenHash), []byte(agentToken)) == nil {
				found = &Agent{
					Address:      rec.Address,
					OperatorID:   rec.OperatorID,
					WebhookURL:   rec.WebhookURL,
					RegisteredAt: rec.RegisteredAt,
				}
				return nil
			}
		}
		return ErrInvalidToken
	})

	if err != nil {
		return nil, err
	}
	return found, nil
}

func (r *Registry) GetAgent(_ context.Context, address string) (*Agent, error) {
	var agent *Agent

	err := r.db.View(func(tx *bolt.Tx) error {
		data := tx.Bucket(agentBucket).Get([]byte(address))
		if data == nil {
			return ErrAgentNotFound
		}
		var rec agentRecord
		if err := json.Unmarshal(data, &rec); err != nil {
			return err
		}
		agent = &Agent{
			Address:      rec.Address,
			OperatorID:   rec.OperatorID,
			WebhookURL:   rec.WebhookURL,
			RegisteredAt: rec.RegisteredAt,
		}
		return nil
	})
	return agent, err
}

func (r *Registry) ListAgents(ctx context.Context, cursor string, limit int, readBy string) (agents []AgentInfo, nextCursor string, total int, err error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	// Collect all agents and sort by registration time (oldest first)
	var allAgents []AgentInfo
	err = r.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(agentBucket)
		total = b.Stats().KeyN

		c := b.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var rec agentRecord
			if err := json.Unmarshal(v, &rec); err != nil {
				continue
			}
			allAgents = append(allAgents, AgentInfo{
				Address:      rec.Address,
				RegisteredAt: rec.RegisteredAt,
			})
		}
		return nil
	})
	if err != nil {
		return
	}

	// Sort by registration time (oldest first)
	sort.Slice(allAgents, func(i, j int) bool {
		return allAgents[i].RegisteredAt.Before(allAgents[j].RegisteredAt)
	})

	// Apply cursor-based pagination
	startIdx := 0
	if cursor != "" {
		for i, a := range allAgents {
			if a.Address == cursor {
				startIdx = i + 1
				break
			}
		}
	}

	end := startIdx + limit
	if end > len(allAgents) {
		end = len(allAgents)
	}
	agents = allAgents[startIdx:end]

	if end < len(allAgents) && len(agents) > 0 {
		nextCursor = agents[len(agents)-1].Address
	}

	// Log registry read
	if err == nil && r.eventLog != nil {
		_ = r.eventLog.Append(ctx, logging.Event{
			Type:         logging.EventRegistryRead,
			AgentAddress: readBy,
			Data: logging.RegistryReadData{
				ReadBy: readBy,
			},
		})
	}

	return
}

func (r *Registry) AgentExists(_ context.Context, address string) bool {
	exists := false
	r.db.View(func(tx *bolt.Tx) error {
		if tx.Bucket(agentBucket).Get([]byte(address)) != nil {
			exists = true
		}
		return nil
	})
	return exists
}

func (r *Registry) Close() error {
	if r.db != nil {
		return r.db.Close()
	}
	return nil
}

// --- Recovery ---

func (r *Registry) ApplyOperatorCreated(op Operator) error {
	rec := operatorRecord{
		ID:          op.ID,
		APIKeyHash:  op.APIKeyHash,
		ContactHash: op.ContactHash,
		CreatedAt:   op.CreatedAt,
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return r.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(operatorBucket).Put([]byte(op.ID), data)
	})
}

func (r *Registry) ApplyAgentRegistered(agent Agent) error {
	rec := agentRecord{
		Address:      agent.Address,
		OperatorID:   agent.OperatorID,
		TokenHash:    agent.TokenHash,
		WebhookURL:   agent.WebhookURL,
		RegisteredAt: agent.RegisteredAt,
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return r.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(agentBucket).Put([]byte(agent.Address), data)
	})
}
