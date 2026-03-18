package observe

import (
	"bufio"
	"encoding/json"
	"os"
	"sync"
	"time"
)

// Removal represents a content removal entry in the transparency log.
type Removal struct {
	Timestamp   time.Time `json:"ts"`
	Action      string    `json:"action"`
	Key         string    `json:"key,omitempty"`
	MessageID   string    `json:"message_id,omitempty"`
	Reason      string    `json:"reason"`
	PerformedBy string    `json:"performed_by"`
	ContentHash string    `json:"content_hash"`
}

// TransparencyLog reads the append-only transparency log file.
type TransparencyLog struct {
	mu       sync.RWMutex
	path     string
	removals []Removal
	// Set of redacted state keys for fast lookup
	redactedKeys map[string]string // key -> reason
	// Set of redacted message IDs for fast lookup
	redactedMsgIDs map[string]string // message_id -> reason
}

func NewTransparencyLog(path string) (*TransparencyLog, error) {
	t := &TransparencyLog{
		path:           path,
		redactedKeys:   make(map[string]string),
		redactedMsgIDs: make(map[string]string),
	}

	if err := t.load(); err != nil {
		return nil, err
	}

	return t, nil
}

func (t *TransparencyLog) load() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	f, err := os.Open(t.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no removals yet
		}
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var r Removal
		if err := json.Unmarshal(line, &r); err != nil {
			continue
		}

		t.removals = append(t.removals, r)
		if r.Key != "" && r.Action == "state_key_removed" {
			t.redactedKeys[r.Key] = r.Reason
		}
		if r.MessageID != "" && r.Action == "message_redacted" {
			t.redactedMsgIDs[r.MessageID] = r.Reason
		}
	}
	return scanner.Err()
}

// GetRemovals returns all removal entries.
func (t *TransparencyLog) GetRemovals() ([]Removal, int) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	result := make([]Removal, len(t.removals))
	copy(result, t.removals)
	return result, len(result)
}

// IsRedacted checks if a state key has been redacted.
func (t *TransparencyLog) IsRedacted(key string) (bool, string) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	reason, ok := t.redactedKeys[key]
	return ok, reason
}

// IsMessageRedacted checks if a message has been redacted.
func (t *TransparencyLog) IsMessageRedacted(messageID string) (bool, string) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	reason, ok := t.redactedMsgIDs[messageID]
	return ok, reason
}

// Refresh re-reads the transparency log.
func (t *TransparencyLog) Refresh() error {
	t.mu.Lock()
	t.removals = nil
	t.redactedKeys = make(map[string]string)
	t.redactedMsgIDs = make(map[string]string)
	t.mu.Unlock()

	return t.load()
}
