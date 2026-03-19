package observe

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/tabulum/tabulum/internal/remove"
)

// AdminHandlers provides HTTP handlers for admin content removal.
type AdminHandlers struct {
	eventLogDir      string
	transparencyPath string
	transparency     *TransparencyLog
}

func NewAdminHandlers(eventLogDir, transparencyPath string, transparency *TransparencyLog) *AdminHandlers {
	return &AdminHandlers{
		eventLogDir:      eventLogDir,
		transparencyPath: transparencyPath,
		transparency:     transparency,
	}
}

type adminRemoveRequest struct {
	Type      string `json:"type"`
	Key       string `json:"key,omitempty"`
	MessageID string `json:"message_id,omitempty"`
	Reason    string `json:"reason"`
}

// AdminRemove handles POST /admin/remove for state key removal and message redaction.
func (a *AdminHandlers) AdminRemove(w http.ResponseWriter, r *http.Request) {
	var req adminRemoveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid JSON body")
		return
	}

	if req.Reason == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Field 'reason' is required")
		return
	}

	switch req.Type {
	case "state":
		a.removeStateKey(w, req)
	case "message":
		a.redactMessage(w, req)
	default:
		writeError(w, http.StatusBadRequest, "bad_request", "Field 'type' must be 'state' or 'message'")
	}
}

func (a *AdminHandlers) removeStateKey(w http.ResponseWriter, req adminRemoveRequest) {
	if req.Key == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Field 'key' is required for state removal")
		return
	}

	// Find the most recent state_written event for this key in the event log
	value, found, err := a.findStateValueInEventLog(req.Key)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", fmt.Sprintf("Error scanning event log: %v", err))
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("Key %q not found in event log", req.Key))
		return
	}

	contentHash := remove.HashContent(value)

	// Append transparency entry
	entry := remove.TransparencyEntry{
		Timestamp:   time.Now().UTC(),
		Action:      "state_key_removed",
		Key:         req.Key,
		Reason:      req.Reason,
		PerformedBy: "admin_api",
		ContentHash: contentHash,
	}
	if err := remove.AppendTransparencyEntry(a.transparencyPath, entry); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", fmt.Sprintf("Failed to write transparency entry: %v", err))
		return
	}

	// Refresh in-memory redaction sets
	a.transparency.Refresh()

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":       "removed",
		"action":       "state_key_removed",
		"key":          req.Key,
		"reason":       req.Reason,
		"content_hash": contentHash,
		"note":         "Content redacted in observation API. State DB cleanup requires kernel restart with CLI tool.",
	})
}

func (a *AdminHandlers) redactMessage(w http.ResponseWriter, req adminRemoveRequest) {
	if req.MessageID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Field 'message_id' is required for message redaction")
		return
	}

	// Use the remove package to redact the message in the event log and write transparency entry
	err := remove.RedactMessage(a.eventLogDir, a.transparencyPath, req.MessageID, req.Reason, "admin_api")
	if err != nil {
		if err.Error() == fmt.Sprintf("message %q not found in event log", req.MessageID) {
			writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("Message %q not found in event log", req.MessageID))
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", fmt.Sprintf("Failed to redact message: %v", err))
		return
	}

	// Refresh in-memory redaction sets
	a.transparency.Refresh()

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":     "removed",
		"action":     "message_redacted",
		"message_id": req.MessageID,
		"reason":     req.Reason,
		"note":       "Message content redacted in event log and observation API.",
	})
}

// AdminListRemovals handles GET /admin/removals.
func (a *AdminHandlers) AdminListRemovals(w http.ResponseWriter, _ *http.Request) {
	a.transparency.Refresh()

	removals, total := a.transparency.GetRemovals()
	if removals == nil {
		removals = []Removal{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"removals": removals,
		"total":    total,
	})
}

// findStateValueInEventLog scans event log files for the most recent state_written event
// with the given key and returns its value. This avoids opening bbolt (which is locked by the kernel).
func (a *AdminHandlers) findStateValueInEventLog(key string) (string, bool, error) {
	entries, err := os.ReadDir(a.eventLogDir)
	if err != nil {
		return "", false, err
	}

	var files []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".jsonl" {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	// Scan in reverse order to find the most recent write
	var lastValue string
	var found bool

	for i := len(files) - 1; i >= 0; i-- {
		path := filepath.Join(a.eventLogDir, files[i])
		v, ok, err := a.scanFileForStateKey(path, key)
		if err != nil {
			return "", false, err
		}
		if ok {
			lastValue = v
			found = true
			break
		}
	}

	return lastValue, found, nil
}

type eventEntry struct {
	Sequence  int64     `json:"seq"`
	Timestamp time.Time `json:"ts"`
	Type      string    `json:"type"`
	Agent     string    `json:"agent,omitempty"`
	Data      any       `json:"data"`
}

func (a *AdminHandlers) scanFileForStateKey(path, key string) (string, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer f.Close()

	var lastValue string
	var found bool

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 2*1024*1024), 2*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var ev eventEntry
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}

		if ev.Type != "state_written" {
			continue
		}

		data := extractAdminMap(ev.Data)
		if data == nil {
			continue
		}

		if k, _ := data["key"].(string); k == key {
			if v, ok := data["value"].(string); ok {
				lastValue = v
				found = true
			}
		}
	}

	return lastValue, found, scanner.Err()
}

func extractAdminMap(data any) map[string]any {
	if m, ok := data.(map[string]any); ok {
		return m
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return nil
	}
	var m map[string]any
	json.Unmarshal(raw, &m)
	return m
}
