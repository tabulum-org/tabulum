package observe

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tabulum/tabulum/internal/version"
	bolt "go.etcd.io/bbolt"
)

type Handlers struct {
	events       *EventReader
	stateDB      *bolt.DB
	registryDB   *bolt.DB
	transparency *TransparencyLog
}

func NewHandlers(events *EventReader, stateDB *bolt.DB, registryDB *bolt.DB, transparency *TransparencyLog) *Handlers {
	return &Handlers{
		events:       events,
		stateDB:      stateDB,
		registryDB:   registryDB,
		transparency: transparency,
	}
}

// --- Health ---

func (h *Handlers) Health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"version": version.Version,
	})
}

// --- Events ---

func (h *Handlers) StreamEvents(w http.ResponseWriter, r *http.Request) {
	// Refresh to pick up new events
	h.events.Refresh()

	since := int64(0)
	if s := r.URL.Query().Get("since"); s != "" {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			since = n
		}
	}

	types := ParseEventTypes(r.URL.Query().Get("type"))
	agent := r.URL.Query().Get("agent")

	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}

	events, nextCursor, hasMore := h.events.QueryEvents(since, types, agent, limit)
	if events == nil {
		events = []Event{}
	}

	// Redact message content for messages flagged in the transparency log
	for i, ev := range events {
		if ev.Type == "message_sent" {
			data := extractMap(ev.Data)
			if data == nil {
				continue
			}
			msgID, _ := data["message_id"].(string)
			if redacted, reason := h.transparency.IsMessageRedacted(msgID); redacted {
				data["content"] = "[REDACTED — see /observe/transparency]"
				data["redacted"] = true
				data["redacted_reason"] = reason
				events[i].Data = data
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"events":      events,
		"next_cursor": nextCursor,
		"has_more":    hasMore,
	})
}

// --- State ---

var stateBucket = []byte("state")

type stateEntry struct {
	Key            string    `json:"key"`
	Value          string    `json:"value"`
	LastModifiedBy string    `json:"last_modified_by"`
	LastModifiedAt time.Time `json:"last_modified_at"`
}

func (h *Handlers) ListState(w http.ResponseWriter, r *http.Request) {
	prefix := r.URL.Query().Get("prefix")
	cursor := r.URL.Query().Get("cursor")
	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}

	if h.stateDB == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"keys":        []interface{}{},
			"next_cursor": nil,
			"total":       0,
		})
		return
	}

	type keyMeta struct {
		Key            string    `json:"key"`
		LastModifiedBy string    `json:"last_modified_by"`
		LastModifiedAt time.Time `json:"last_modified_at"`
		ValueLength    int       `json:"value_length"`
	}

	var keys []keyMeta
	var total int
	var nextCursor string

	h.stateDB.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(stateBucket)
		if b == nil {
			return nil
		}
		c := b.Cursor()

		// Count total
		if prefix == "" {
			total = b.Stats().KeyN
		} else {
			prefixBytes := []byte(prefix)
			for k, _ := c.Seek(prefixBytes); k != nil && hasPrefix(k, prefixBytes); k, _ = c.Next() {
				total++
			}
		}

		// Navigate to start
		var k, v []byte
		if cursor != "" {
			k, v = c.Seek([]byte(cursor))
			if k != nil && string(k) == cursor {
				k, v = c.Next()
			}
		} else if prefix != "" {
			k, v = c.Seek([]byte(prefix))
		} else {
			k, v = c.First()
		}

		prefixBytes := []byte(prefix)
		for ; k != nil && len(keys) < limit; k, v = c.Next() {
			if prefix != "" && !hasPrefix(k, prefixBytes) {
				break
			}

			var entry stateEntry
			if err := json.Unmarshal(v, &entry); err != nil {
				continue
			}

			keys = append(keys, keyMeta{
				Key:            string(k),
				LastModifiedBy: entry.LastModifiedBy,
				LastModifiedAt: entry.LastModifiedAt,
				ValueLength:    len(entry.Value),
			})
		}

		if k != nil && (prefix == "" || hasPrefix(k, prefixBytes)) && len(keys) > 0 {
			nextCursor = keys[len(keys)-1].Key
		}

		return nil
	})

	if keys == nil {
		keys = []keyMeta{}
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

func (h *Handlers) ReadState(w http.ResponseWriter, r *http.Request) {
	key := extractObserveStateKey(r)
	if key == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Key is required")
		return
	}

	// Check redaction
	if redacted, reason := h.transparency.IsRedacted(key); redacted {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"key":      key,
			"value":    "[REDACTED — see /observe/transparency]",
			"redacted": true,
			"reason":   reason,
		})
		return
	}

	if h.stateDB == nil {
		writeError(w, http.StatusNotFound, "not_found", "Key does not exist")
		return
	}

	var entry stateEntry
	var found bool

	h.stateDB.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(stateBucket)
		if b == nil {
			return nil
		}
		data := b.Get([]byte(key))
		if data == nil {
			return nil
		}
		if err := json.Unmarshal(data, &entry); err != nil {
			return nil
		}
		found = true
		return nil
	})

	if !found {
		writeError(w, http.StatusNotFound, "not_found", "Key does not exist")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"key":              entry.Key,
		"value":            entry.Value,
		"last_modified_by": entry.LastModifiedBy,
		"last_modified_at": entry.LastModifiedAt.Format(time.RFC3339Nano),
	})
}

// --- Agents ---

func (h *Handlers) ListAgents(w http.ResponseWriter, _ *http.Request) {
	h.events.Refresh()

	stats := h.events.GetAgentStats()
	if stats == nil {
		stats = []AgentStats{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"agents": stats,
		"total":  len(stats),
	})
}

func (h *Handlers) AgentDetail(w http.ResponseWriter, r *http.Request) {
	h.events.Refresh()

	address := extractAgentAddress(r)
	if address == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Agent address is required")
		return
	}

	stats, ok := h.events.GetAgentDetail(address)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "Agent not found")
		return
	}

	writeJSON(w, http.StatusOK, stats)
}

// --- Network ---

func (h *Handlers) Network(w http.ResponseWriter, _ *http.Request) {
	h.events.Refresh()

	nodes, edges := h.events.GetNetwork()

	type nodeInfo struct {
		Address          string `json:"address"`
		MessagesSent     int    `json:"messages_sent"`
		MessagesReceived int    `json:"messages_received"`
	}

	nodeList := make([]nodeInfo, len(nodes))
	for i, n := range nodes {
		nodeList[i] = nodeInfo{
			Address:          n.Address,
			MessagesSent:     n.MessagesSent,
			MessagesReceived: n.MessagesReceived,
		}
	}
	if nodeList == nil {
		nodeList = []nodeInfo{}
	}
	if edges == nil {
		edges = []NetworkEdge{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"nodes": nodeList,
		"edges": edges,
	})
}

// --- Transparency ---

func (h *Handlers) Transparency(w http.ResponseWriter, _ *http.Request) {
	h.transparency.Refresh()

	removals, total := h.transparency.GetRemovals()
	if removals == nil {
		removals = []Removal{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"removals": removals,
		"total":    total,
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

func extractObserveStateKey(r *http.Request) string {
	path := r.URL.Path
	prefix := "/observe/state/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	key := strings.TrimPrefix(path, prefix)
	decoded, err := url.PathUnescape(key)
	if err != nil {
		return key
	}
	return decoded
}

func extractAgentAddress(r *http.Request) string {
	path := r.URL.Path
	prefix := "/observe/agents/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	return strings.TrimPrefix(path, prefix)
}

func hasPrefix(s, prefix []byte) bool {
	if len(s) < len(prefix) {
		return false
	}
	for i := range prefix {
		if s[i] != prefix[i] {
			return false
		}
	}
	return true
}
