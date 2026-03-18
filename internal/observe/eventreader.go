package observe

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Event mirrors the kernel's event log format.
type Event struct {
	Sequence     int64     `json:"seq"`
	Timestamp    time.Time `json:"ts"`
	Type         string    `json:"type"`
	AgentAddress string    `json:"agent,omitempty"`
	Data         any       `json:"data"`
}

// AgentStats tracks per-agent activity counters.
type AgentStats struct {
	Address          string    `json:"address"`
	RegisteredAt     time.Time `json:"registered_at"`
	MessagesSent     int       `json:"messages_sent"`
	MessagesReceived int       `json:"messages_received"`
	StateWrites      int       `json:"state_writes"`
	LastActive       time.Time `json:"last_active"`
}

// NetworkEdge represents a communication link between two agents.
type NetworkEdge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Count int    `json:"count"`
}

// EventReader reads event log files and maintains an in-memory index.
type EventReader struct {
	mu          sync.RWMutex
	dataDir     string
	events      []Event
	agentStats  map[string]*AgentStats
	edges       map[string]*NetworkEdge // key: "from->to"
	lastSeq     int64
}

func NewEventReader(dataDir string) (*EventReader, error) {
	r := &EventReader{
		dataDir:    dataDir,
		agentStats: make(map[string]*AgentStats),
		edges:      make(map[string]*NetworkEdge),
	}

	if err := r.loadAll(); err != nil {
		return nil, err
	}

	return r, nil
}

func (r *EventReader) loadAll() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	files, err := r.logFiles()
	if err != nil {
		return err
	}

	for _, name := range files {
		path := filepath.Join(r.dataDir, name)
		if err := r.loadFile(path); err != nil {
			return err
		}
	}

	return nil
}

func (r *EventReader) logFiles() ([]string, error) {
	entries, err := os.ReadDir(r.dataDir)
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

func (r *EventReader) loadFile(path string) error {
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
			continue // skip corrupted lines
		}

		r.events = append(r.events, ev)
		r.indexEvent(&ev)

		if ev.Sequence > r.lastSeq {
			r.lastSeq = ev.Sequence
		}
	}
	return scanner.Err()
}

func (r *EventReader) indexEvent(ev *Event) {
	// Update agent stats based on event type
	switch ev.Type {
	case "agent_registered":
		data := extractMap(ev.Data)
		addr, _ := data["address"].(string)
		if addr != "" {
			if _, ok := r.agentStats[addr]; !ok {
				r.agentStats[addr] = &AgentStats{
					Address:      addr,
					RegisteredAt: ev.Timestamp,
				}
			}
		}

	case "message_sent":
		data := extractMap(ev.Data)
		from, _ := data["from"].(string)
		to, _ := data["to"].(string)

		if from != "" {
			s := r.getOrCreateStats(from)
			s.MessagesSent++
			s.LastActive = ev.Timestamp
		}
		if to != "" {
			s := r.getOrCreateStats(to)
			s.MessagesReceived++
		}
		if from != "" && to != "" {
			key := from + "->" + to
			edge, ok := r.edges[key]
			if !ok {
				edge = &NetworkEdge{From: from, To: to}
				r.edges[key] = edge
			}
			edge.Count++
		}

	case "state_written":
		data := extractMap(ev.Data)
		writtenBy, _ := data["written_by"].(string)
		if writtenBy != "" {
			s := r.getOrCreateStats(writtenBy)
			s.StateWrites++
			s.LastActive = ev.Timestamp
		}

	case "state_deleted", "state_read", "registry_read":
		if ev.AgentAddress != "" {
			s := r.getOrCreateStats(ev.AgentAddress)
			s.LastActive = ev.Timestamp
		}
	}
}

func (r *EventReader) getOrCreateStats(addr string) *AgentStats {
	s, ok := r.agentStats[addr]
	if !ok {
		s = &AgentStats{Address: addr}
		r.agentStats[addr] = s
	}
	return s
}

func extractMap(data any) map[string]any {
	if m, ok := data.(map[string]any); ok {
		return m
	}
	// Try re-marshaling (e.g., from typed struct)
	raw, err := json.Marshal(data)
	if err != nil {
		return nil
	}
	var m map[string]any
	json.Unmarshal(raw, &m)
	return m
}

// Refresh re-reads new events from the log files since last load.
func (r *EventReader) Refresh() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	files, err := r.logFiles()
	if err != nil {
		return err
	}

	// Re-read all files but skip events we already have
	prevLastSeq := r.lastSeq
	for _, name := range files {
		path := filepath.Join(r.dataDir, name)
		if err := r.loadFileAfter(path, prevLastSeq); err != nil {
			return err
		}
	}
	return nil
}

func (r *EventReader) loadFileAfter(path string, afterSeq int64) error {
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
			continue
		}

		if ev.Sequence <= afterSeq {
			continue
		}

		r.events = append(r.events, ev)
		r.indexEvent(&ev)

		if ev.Sequence > r.lastSeq {
			r.lastSeq = ev.Sequence
		}
	}
	return scanner.Err()
}

// QueryEvents returns events matching the filter criteria.
func (r *EventReader) QueryEvents(since int64, types []string, agent string, limit int) (events []Event, nextCursor int64, hasMore bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	typeSet := make(map[string]bool)
	for _, t := range types {
		typeSet[t] = true
	}

	for _, ev := range r.events {
		if ev.Sequence <= since {
			continue
		}
		if len(typeSet) > 0 && !typeSet[ev.Type] {
			continue
		}
		if agent != "" && ev.AgentAddress != agent {
			continue
		}

		events = append(events, ev)
		if len(events) >= limit {
			break
		}
	}

	if len(events) > 0 {
		nextCursor = events[len(events)-1].Sequence
	}

	// Check if there are more events after the last returned one
	if len(events) == limit {
		for _, ev := range r.events {
			if ev.Sequence > nextCursor {
				if len(typeSet) > 0 && !typeSet[ev.Type] {
					continue
				}
				if agent != "" && ev.AgentAddress != agent {
					continue
				}
				hasMore = true
				break
			}
		}
	}

	return
}

// GetAgentStats returns stats for all agents.
func (r *EventReader) GetAgentStats() []AgentStats {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]AgentStats, 0, len(r.agentStats))
	for _, s := range r.agentStats {
		result = append(result, *s)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].RegisteredAt.Before(result[j].RegisteredAt)
	})
	return result
}

// GetAgentDetail returns stats for a specific agent.
func (r *EventReader) GetAgentDetail(address string) (*AgentStats, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	s, ok := r.agentStats[address]
	if !ok {
		return nil, false
	}
	copy := *s
	return &copy, true
}

// GetNetwork returns the communication graph.
func (r *EventReader) GetNetwork() (nodes []AgentStats, edges []NetworkEdge) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	nodes = make([]AgentStats, 0, len(r.agentStats))
	for _, s := range r.agentStats {
		nodes = append(nodes, *s)
	}

	edges = make([]NetworkEdge, 0, len(r.edges))
	for _, e := range r.edges {
		edges = append(edges, *e)
	}

	return
}

// LastSequence returns the highest sequence number seen.
func (r *EventReader) LastSequence() int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lastSeq
}

// NewEventsAfter returns events added after the given sequence.
// Used by the WebSocket streamer to detect new events.
func (r *EventReader) NewEventsAfter(since int64, types []string, agent string) []Event {
	r.mu.RLock()
	defer r.mu.RUnlock()

	typeSet := make(map[string]bool)
	for _, t := range types {
		typeSet[t] = true
	}

	var result []Event
	for i := len(r.events) - 1; i >= 0; i-- {
		if r.events[i].Sequence <= since {
			break
		}
	}

	for _, ev := range r.events {
		if ev.Sequence <= since {
			continue
		}
		if len(typeSet) > 0 && !typeSet[ev.Type] {
			continue
		}
		if agent != "" && ev.AgentAddress != agent {
			continue
		}
		result = append(result, ev)
	}

	return result
}

// ParseEventTypes splits a comma-separated type filter string.
func ParseEventTypes(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
