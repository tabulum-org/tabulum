package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tabulum/tabulum/internal/config"
	"github.com/tabulum/tabulum/internal/messaging"
	"github.com/tabulum/tabulum/internal/version"
	"github.com/tabulum/tabulum/internal/ratelimit"
	"github.com/tabulum/tabulum/internal/registry"
	"github.com/tabulum/tabulum/internal/state"
	"github.com/tabulum/tabulum/internal/verification"
	"github.com/tabulum/tabulum/internal/webhook"
)

type Handlers struct {
	Registry     *registry.Registry
	Messaging    *messaging.Router
	State        *state.Store
	Verifier     verification.Verifier
	Limiter      *ratelimit.Limiter
	Config       config.Config
}

func NewHandlers(reg *registry.Registry, msg *messaging.Router, st *state.Store, v verification.Verifier, lim *ratelimit.Limiter, cfg config.Config) *Handlers {
	return &Handlers{
		Registry:  reg,
		Messaging: msg,
		State:     st,
		Verifier:  v,
		Limiter:   lim,
		Config:    cfg,
	}
}

// --- Operator endpoints ---

func (h *Handlers) CreateOperator(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ContactHash string `json:"contact_hash"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}
	if req.ContactHash == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "contact_hash is required")
		return
	}

	op, apiKey, err := h.Registry.CreateOperator(r.Context(), req.ContactHash)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to create operator")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{
		"operator_id": op.ID,
		"api_key":     apiKey,
	})
}

// --- Verification endpoints ---

func (h *Handlers) GetVerificationChallenge(w http.ResponseWriter, r *http.Request) {
	challenge, err := h.Verifier.GenerateChallenge(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to generate challenge")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"challenge_id":   challenge.ID,
		"challenge_type": challenge.Type,
		"challenge_data": challenge.Data,
		"expires_at":     challenge.ExpiresAt.Format(time.RFC3339),
	})
}

// --- Agent endpoints ---

func (h *Handlers) RegisterAgent(w http.ResponseWriter, r *http.Request) {
	op := OperatorFromContext(r.Context())
	if op == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Operator authentication required")
		return
	}

	var req struct {
		VerificationResponse struct {
			ChallengeID string `json:"challenge_id"`
			Response    any    `json:"response"`
		} `json:"verification_response"`
		WebhookURL string `json:"webhook_url"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}

	if req.VerificationResponse.ChallengeID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "verification_response with challenge_id is required")
		return
	}

	// Verify challenge
	passed, err := h.Verifier.VerifyResponse(r.Context(), req.VerificationResponse.ChallengeID, req.VerificationResponse.Response)
	if err != nil {
		if errors.Is(err, verification.ErrChallengeNotFound) || errors.Is(err, verification.ErrChallengeExpired) {
			writeError(w, http.StatusForbidden, "verification_failed", err.Error())
			return
		}
		writeError(w, http.StatusForbidden, "verification_failed", "Verification failed")
		return
	}
	if !passed {
		writeError(w, http.StatusForbidden, "verification_failed", "Verification challenge not passed")
		return
	}

	// Validate webhook URL if provided
	if req.WebhookURL != "" {
		if err := webhook.ValidateWebhookURL(req.WebhookURL); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "Invalid webhook URL: "+err.Error())
			return
		}
	}

	agent, token, err := h.Registry.RegisterAgent(r.Context(), op.ID, req.WebhookURL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to register agent")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{
		"agent_address": agent.Address,
		"agent_token":   token,
	})
}

func (h *Handlers) GetAgentSelf(w http.ResponseWriter, r *http.Request) {
	agent := AgentFromContext(r.Context())
	if agent == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Agent authentication required")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"address":       agent.Address,
		"registered_at": agent.RegisteredAt.Format(time.RFC3339),
	})
}

// --- Registry endpoints ---

func (h *Handlers) ListRegistry(w http.ResponseWriter, r *http.Request) {
	agent := AgentFromContext(r.Context())
	if agent == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Agent authentication required")
		return
	}

	cursor := r.URL.Query().Get("cursor")
	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}

	agents, nextCursor, total, err := h.Registry.ListAgents(r.Context(), cursor, limit, agent.Address)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to list agents")
		return
	}

	if agents == nil {
		agents = []registry.AgentInfo{}
	}

	resp := map[string]interface{}{
		"agents": agents,
		"total":  total,
	}
	if nextCursor != "" {
		resp["next_cursor"] = nextCursor
	} else {
		resp["next_cursor"] = nil
	}

	writeJSON(w, http.StatusOK, resp)
}

// --- Message endpoints ---

func (h *Handlers) SendMessage(w http.ResponseWriter, r *http.Request) {
	agent := AgentFromContext(r.Context())
	if agent == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Agent authentication required")
		return
	}

	var req struct {
		To      string `json:"to"`
		Content string `json:"content"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}

	if req.To == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "to is required")
		return
	}
	if req.Content == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "content is required")
		return
	}

	// Check content size
	if len(req.Content) > h.Config.Messaging.MaxContentLength {
		writeError(w, http.StatusBadRequest, "bad_request", "Message content exceeds maximum size")
		return
	}

	// Verify recipient exists
	if !h.Registry.AgentExists(r.Context(), req.To) {
		writeError(w, http.StatusNotFound, "not_found", "Recipient agent not found")
		return
	}

	msg, err := h.Messaging.Send(r.Context(), agent.Address, req.To, req.Content)
	if err != nil {
		if errors.Is(err, messaging.ErrContentTooLong) {
			writeError(w, http.StatusBadRequest, "bad_request", "Message content exceeds maximum size")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to send message")
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"message_id": msg.ID,
		"from":       msg.From,
		"to":         msg.To,
		"timestamp":  msg.Timestamp.Format(time.RFC3339Nano),
	})
}

func (h *Handlers) GetMessages(w http.ResponseWriter, r *http.Request) {
	agent := AgentFromContext(r.Context())
	if agent == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Agent authentication required")
		return
	}

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}

	messages, remaining, err := h.Messaging.Receive(r.Context(), agent.Address, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to retrieve messages")
		return
	}

	if messages == nil {
		messages = []messaging.Message{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"messages":  messages,
		"remaining": remaining,
	})
}

// --- State endpoints ---

func (h *Handlers) GetState(w http.ResponseWriter, r *http.Request) {
	agent := AgentFromContext(r.Context())
	if agent == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Agent authentication required")
		return
	}

	key := extractStateKey(r)
	if key == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Key is required")
		return
	}

	entry, err := h.State.Get(r.Context(), key, agent.Address)
	if err != nil {
		if errors.Is(err, state.ErrKeyNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "Key does not exist")
			return
		}
		if errors.Is(err, state.ErrKeyTooLong) {
			writeError(w, http.StatusBadRequest, "bad_request", "Key exceeds maximum length")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to read state")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"key":              entry.Key,
		"value":            entry.Value,
		"last_modified_by": entry.LastModifiedBy,
		"last_modified_at": entry.LastModifiedAt.Format(time.RFC3339Nano),
	})
}

func (h *Handlers) SetState(w http.ResponseWriter, r *http.Request) {
	agent := AgentFromContext(r.Context())
	if agent == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Agent authentication required")
		return
	}

	key := extractStateKey(r)
	if key == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Key is required")
		return
	}

	if len(key) > h.Config.State.MaxKeyLength {
		writeError(w, http.StatusBadRequest, "bad_request", "Key exceeds maximum length")
		return
	}

	var req struct {
		Value string `json:"value"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}

	if len(req.Value) > h.Config.State.MaxValueLength {
		writeError(w, http.StatusRequestEntityTooLarge, "value_too_large", "Value exceeds maximum size")
		return
	}

	now := time.Now().UTC()
	err := h.State.Set(r.Context(), key, req.Value, agent.Address)
	if err != nil {
		if errors.Is(err, state.ErrKeyTooLong) {
			writeError(w, http.StatusBadRequest, "bad_request", "Key exceeds maximum length")
			return
		}
		if errors.Is(err, state.ErrValueTooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "value_too_large", "Value exceeds maximum size")
			return
		}
		if errors.Is(err, state.ErrAtCapacity) {
			writeError(w, http.StatusInsufficientStorage, "store_full", "State store at capacity")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to write state")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"key":        key,
		"written_by": agent.Address,
		"written_at": now.Format(time.RFC3339Nano),
	})
}

func (h *Handlers) DeleteState(w http.ResponseWriter, r *http.Request) {
	agent := AgentFromContext(r.Context())
	if agent == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Agent authentication required")
		return
	}

	key := extractStateKey(r)
	if key == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Key is required")
		return
	}

	now := time.Now().UTC()
	err := h.State.Delete(r.Context(), key, agent.Address)
	if err != nil {
		if errors.Is(err, state.ErrKeyNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "Key does not exist")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to delete state")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"key":        key,
		"deleted_by": agent.Address,
		"deleted_at": now.Format(time.RFC3339Nano),
	})
}

func (h *Handlers) ListStateKeys(w http.ResponseWriter, r *http.Request) {
	agent := AgentFromContext(r.Context())
	if agent == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Agent authentication required")
		return
	}

	prefix := r.URL.Query().Get("prefix")
	cursor := r.URL.Query().Get("cursor")
	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}

	keys, nextCursor, total, err := h.State.ListKeys(r.Context(), prefix, cursor, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to list keys")
		return
	}

	if keys == nil {
		keys = []string{}
	}

	resp := map[string]interface{}{
		"keys":  keys,
		"total": total,
	}
	if nextCursor != "" {
		resp["next_cursor"] = nextCursor
	} else {
		resp["next_cursor"] = nil
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *Handlers) GetStateCapacity(w http.ResponseWriter, r *http.Request) {
	agent := AgentFromContext(r.Context())
	if agent == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Agent authentication required")
		return
	}

	cap, err := h.State.GetCapacity(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to get capacity")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"used_bytes":  cap.UsedBytes,
		"total_bytes": cap.TotalBytes,
		"key_count":   cap.KeyCount,
	})
}

// --- Health ---

func (h *Handlers) HealthCheck(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"version": version.Version,
	})
}

// --- Helpers ---

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, code string, message string) {
	writeJSON(w, status, map[string]string{
		"error":   code,
		"message": message,
	})
}

func readJSON(r *http.Request, v any) error {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}

func extractStateKey(r *http.Request) string {
	path := r.URL.Path
	// Expected format: /v1/state/{key}
	prefix := "/v1/state/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	key := strings.TrimPrefix(path, prefix)
	// URL-decode the key
	decoded, err := url.PathUnescape(key)
	if err != nil {
		return key
	}
	return decoded
}
