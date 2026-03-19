package remove

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type eventEntry struct {
	Sequence  int64     `json:"seq"`
	Timestamp time.Time `json:"ts"`
	Type      string    `json:"type"`
	Agent     string    `json:"agent,omitempty"`
	Data      any       `json:"data"`
}

// RedactMessage finds a message by ID in the event log files, replaces its content
// with a redaction notice, and writes a transparency log entry.
// The kernel must be stopped (event log must not be actively written).
func RedactMessage(eventLogDir, transparencyPath, messageID, reason, performedBy string) error {
	files, err := logFiles(eventLogDir)
	if err != nil {
		return fmt.Errorf("read event log dir: %w", err)
	}

	for _, name := range files {
		path := filepath.Join(eventLogDir, name)
		found, err := redactInFile(path, messageID, transparencyPath, reason, performedBy)
		if err != nil {
			return err
		}
		if found {
			return nil
		}
	}

	return fmt.Errorf("message %q not found in event log", messageID)
}

func logFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var files []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".jsonl" {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)
	return files, nil
}

func redactInFile(path, messageID, transparencyPath, reason, performedBy string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()

	var lines [][]byte
	var found bool
	var originalContent string

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 2*1024*1024), 2*1024*1024)
	for scanner.Scan() {
		line := make([]byte, len(scanner.Bytes()))
		copy(line, scanner.Bytes())

		if !found && len(line) > 0 {
			var ev eventEntry
			if err := json.Unmarshal(line, &ev); err == nil && ev.Type == "message_sent" {
				data := extractMap(ev.Data)
				if data != nil {
					if mid, _ := data["message_id"].(string); mid == messageID {
						originalContent, _ = data["content"].(string)
						data["content"] = "[REDACTED]"
						data["content_length"] = 0
						ev.Data = data
						redacted, err := json.Marshal(ev)
						if err != nil {
							return false, fmt.Errorf("marshal redacted event: %w", err)
						}
						line = redacted
						found = true
					}
				}
			}
		}

		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		return false, err
	}

	if !found {
		return false, nil
	}

	// Write the modified file back
	tmp := path + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return false, fmt.Errorf("create temp file: %w", err)
	}

	for _, line := range lines {
		out.Write(line)
		out.Write([]byte("\n"))
	}
	if err := out.Sync(); err != nil {
		out.Close()
		os.Remove(tmp)
		return false, fmt.Errorf("fsync temp file: %w", err)
	}
	out.Close()

	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return false, fmt.Errorf("rename temp file: %w", err)
	}

	// Write transparency entry
	entry := TransparencyEntry{
		Timestamp:   time.Now().UTC(),
		Action:      "message_redacted",
		MessageID:   messageID,
		Reason:      reason,
		PerformedBy: performedBy,
		ContentHash: HashContent(originalContent),
	}

	return true, AppendTransparencyEntry(transparencyPath, entry)
}

func extractMap(data any) map[string]any {
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
