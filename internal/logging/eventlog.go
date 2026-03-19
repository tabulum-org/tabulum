package logging

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type EventType string

const (
	EventAgentRegistered  EventType = "agent_registered"
	EventMessageSent      EventType = "message_sent"
	EventMessageDelivered EventType = "message_delivered"
	EventStateWritten     EventType = "state_written"
	EventStateDeleted     EventType = "state_deleted"
	EventStateRead        EventType = "state_read"
	EventRegistryRead     EventType = "registry_read"
	EventOperatorCreated  EventType = "operator_created"
)

type Event struct {
	Sequence     int64     `json:"seq"`
	Timestamp    time.Time `json:"ts"`
	Type         EventType `json:"type"`
	AgentAddress string    `json:"agent,omitempty"`
	Data         any       `json:"data"`
}

type AgentRegisteredData struct {
	Address    string `json:"address"`
	OperatorID string `json:"operator_id"`
	HasWebhook bool   `json:"has_webhook"`
}

type MessageSentData struct {
	MessageID string `json:"message_id"`
	From      string `json:"from"`
	To        string `json:"to"`
	Content   string `json:"content"`
	Length    int    `json:"content_length"`
}

type MessageDeliveredData struct {
	MessageID      string `json:"message_id"`
	To             string `json:"to"`
	DeliveryMethod string `json:"delivery_method"`
}

type StateWrittenData struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	Length    int    `json:"value_length"`
	WrittenBy string `json:"written_by"`
}

type StateDeletedData struct {
	Key       string `json:"key"`
	DeletedBy string `json:"deleted_by"`
}

type StateReadData struct {
	Key    string `json:"key"`
	ReadBy string `json:"read_by"`
	Found  bool   `json:"found"`
}

type RegistryReadData struct {
	ReadBy string `json:"read_by"`
}

type OperatorCreatedData struct {
	OperatorID   string `json:"operator_id"`
	ContactHash  string `json:"contact_hash"`
	AcceptedTerms bool  `json:"accepted_terms"`
}

type EventLog struct {
	mu          sync.Mutex
	dataDir     string
	maxFileSize int64
	file        *os.File
	fileSize    int64
	seq         int64
}

func NewEventLog(dataDir string, maxFileSize int64) (*EventLog, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create event log dir: %w", err)
	}

	l := &EventLog{
		dataDir:     dataDir,
		maxFileSize: maxFileSize,
	}

	// Find the highest sequence number from existing logs
	if err := l.scanExistingLogs(); err != nil {
		return nil, err
	}

	// Open current log file
	if err := l.openCurrentFile(); err != nil {
		return nil, err
	}

	return l, nil
}

func (l *EventLog) scanExistingLogs() error {
	files, err := l.logFiles()
	if err != nil {
		return err
	}

	for _, f := range files {
		path := filepath.Join(l.dataDir, f)
		if err := l.scanFileForSeq(path); err != nil {
			return err
		}
	}
	return nil
}

func (l *EventLog) scanFileForSeq(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	for scanner.Scan() {
		var ev Event
		if err := json.Unmarshal(scanner.Bytes(), &ev); err == nil {
			if ev.Sequence > l.seq {
				l.seq = ev.Sequence
			}
		}
	}
	return scanner.Err()
}

func (l *EventLog) logFiles() ([]string, error) {
	entries, err := os.ReadDir(l.dataDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
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

func (l *EventLog) openCurrentFile() error {
	path := filepath.Join(l.dataDir, "events.jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open event log: %w", err)
	}

	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}

	l.file = f
	l.fileSize = info.Size()
	return nil
}

func (l *EventLog) Append(_ context.Context, event Event) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.seq++
	event.Sequence = l.seq
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}

	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	data = append(data, '\n')

	n, err := l.file.Write(data)
	if err != nil {
		return fmt.Errorf("write event: %w", err)
	}

	if err := l.file.Sync(); err != nil {
		return fmt.Errorf("fsync event log: %w", err)
	}

	l.fileSize += int64(n)

	// Rotate if needed
	if l.maxFileSize > 0 && l.fileSize >= l.maxFileSize {
		if err := l.rotate(); err != nil {
			return fmt.Errorf("rotate event log: %w", err)
		}
	}

	return nil
}

func (l *EventLog) rotate() error {
	l.file.Close()

	oldPath := filepath.Join(l.dataDir, "events.jsonl")
	newPath := filepath.Join(l.dataDir, fmt.Sprintf("events_%d.jsonl", time.Now().UnixNano()))
	if err := os.Rename(oldPath, newPath); err != nil {
		return err
	}

	return l.openCurrentFile()
}

func (l *EventLog) Replay(_ context.Context, fn func(Event) error) error {
	files, err := l.logFiles()
	if err != nil {
		return err
	}

	for _, name := range files {
		path := filepath.Join(l.dataDir, name)
		if err := l.replayFile(path, fn); err != nil {
			return err
		}
	}
	return nil
}

func (l *EventLog) replayFile(path string, fn func(Event) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 2*1024*1024), 2*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var ev Event
		if err := json.Unmarshal(line, &ev); err != nil {
			// Skip corrupted lines instead of halting replay
			continue
		}

		// Re-parse Data into the correct type based on event Type.
		// json.Unmarshal will have decoded Data as map[string]any.
		// We need to re-marshal and unmarshal into the correct struct
		// for consumers to type-assert.
		if ev.Data != nil {
			rawData, err := json.Marshal(ev.Data)
			if err != nil {
				return err
			}
			switch ev.Type {
			case EventAgentRegistered:
				var d AgentRegisteredData
				if err := json.Unmarshal(rawData, &d); err == nil {
					ev.Data = d
				}
			case EventMessageSent:
				var d MessageSentData
				if err := json.Unmarshal(rawData, &d); err == nil {
					ev.Data = d
				}
			case EventMessageDelivered:
				var d MessageDeliveredData
				if err := json.Unmarshal(rawData, &d); err == nil {
					ev.Data = d
				}
			case EventStateWritten:
				var d StateWrittenData
				if err := json.Unmarshal(rawData, &d); err == nil {
					ev.Data = d
				}
			case EventStateDeleted:
				var d StateDeletedData
				if err := json.Unmarshal(rawData, &d); err == nil {
					ev.Data = d
				}
			case EventStateRead:
				var d StateReadData
				if err := json.Unmarshal(rawData, &d); err == nil {
					ev.Data = d
				}
			case EventRegistryRead:
				var d RegistryReadData
				if err := json.Unmarshal(rawData, &d); err == nil {
					ev.Data = d
				}
			case EventOperatorCreated:
				var d OperatorCreatedData
				if err := json.Unmarshal(rawData, &d); err == nil {
					ev.Data = d
				}
			}
		}

		if err := fn(ev); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func (l *EventLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		return l.file.Close()
	}
	return nil
}
