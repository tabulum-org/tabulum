package remove

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// TransparencyEntry is an entry written to the transparency log when content is removed.
type TransparencyEntry struct {
	Timestamp   time.Time `json:"ts"`
	Action      string    `json:"action"`
	Key         string    `json:"key,omitempty"`
	MessageID   string    `json:"message_id,omitempty"`
	Reason      string    `json:"reason"`
	PerformedBy string    `json:"performed_by"`
	ContentHash string    `json:"content_hash"`
}

// AppendTransparencyEntry writes a removal record to the transparency log.
func AppendTransparencyEntry(path string, entry TransparencyEntry) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open transparency log: %w", err)
	}
	defer f.Close()

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal entry: %w", err)
	}
	data = append(data, '\n')

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write entry: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("fsync transparency log: %w", err)
	}

	return nil
}

// HashContent returns the SHA-256 hex string of the given content.
func HashContent(content string) string {
	h := sha256.Sum256([]byte(content))
	return fmt.Sprintf("%x", h)
}

// ListRemovals reads and returns all entries from the transparency log.
func ListRemovals(path string) ([]TransparencyEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var entries []TransparencyEntry
	for _, line := range splitLines(data) {
		if len(line) == 0 {
			continue
		}
		var e TransparencyEntry
		if err := json.Unmarshal(line, &e); err != nil {
			continue
		}
		entries = append(entries, e)
	}
	return entries, nil
}

func splitLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			lines = append(lines, data[start:i])
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}
