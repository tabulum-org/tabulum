//go:build integration

package test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/tabulum/tabulum/internal/config"
	"github.com/tabulum/tabulum/internal/kernel"
	"github.com/tabulum/tabulum/internal/observe"
	bolt "go.etcd.io/bbolt"
)

func setupObserveOnly(t *testing.T) (obsURL string, eventLogDir string, cleanup func()) {
	t.Helper()

	tmpDir := t.TempDir()
	eventLogDir = filepath.Join(tmpDir, "eventlog")
	os.MkdirAll(eventLogDir, 0o755)

	cfg := observe.Config{
		Server:    observe.ServerConfig{ListenAddr: ":0", ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second},
		Kernel:    observe.KernelDataConfig{EventLogDir: eventLogDir, TransparencyLogPath: filepath.Join(tmpDir, "transparency.jsonl")},
		RateLimit: observe.RateLimitConfig{RequestsPerMinPerIP: 300},
		WebSocket: observe.WebSocketConfig{MaxConnections: 10, WriteTimeout: 10 * time.Second, PingInterval: 30 * time.Second, MaxDuration: 24 * time.Hour},
	}

	srv, err := observe.NewServer(cfg)
	if err != nil {
		t.Fatalf("Failed to create observe server: %v", err)
	}

	ts := httptest.NewServer(srv.Handler())
	return ts.URL, eventLogDir, func() {
		ts.Close()
		srv.Shutdown(context.Background())
	}
}

func setupObserveWithKernelTest(t *testing.T) (kernelURL string, obsURL string, cleanup func()) {
	t.Helper()

	tmpDir := t.TempDir()

	cfg := config.DefaultConfig()
	cfg.State.DataDir = tmpDir
	cfg.EventLog.DataDir = tmpDir
	cfg.RateLimits.MessageSendPerMin = 1000
	cfg.RateLimits.StateReadPerMin = 1000
	cfg.RateLimits.StateWritePerMin = 1000
	cfg.RateLimits.RegistryReadPerMin = 1000
	cfg.RateLimits.OperatorRegPerHourPerIP = 1000
	cfg.RateLimits.WebhookOutboundPerMin = 1000

	k, err := kernel.NewWithConfig(cfg)
	if err != nil {
		t.Fatalf("Failed to create kernel: %v", err)
	}
	kServer := httptest.NewServer(k.Handler())

	// Note: bbolt uses exclusive file locks, so the observe server cannot open
	// the same DB files the kernel has open. Event-based endpoints work without bbolt.
	obsCfg := observe.Config{
		Server: observe.ServerConfig{ListenAddr: ":0", ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second},
		Kernel: observe.KernelDataConfig{
			EventLogDir:         tmpDir,
			TransparencyLogPath: filepath.Join(tmpDir, "transparency.jsonl"),
		},
		RateLimit: observe.RateLimitConfig{RequestsPerMinPerIP: 300},
		WebSocket: observe.WebSocketConfig{MaxConnections: 10, WriteTimeout: 10 * time.Second, PingInterval: 30 * time.Second, MaxDuration: 24 * time.Hour},
	}

	obsSrv, err := observe.NewServer(obsCfg)
	if err != nil {
		t.Fatalf("Failed to create observe server: %v", err)
	}
	oServer := httptest.NewServer(obsSrv.Handler())

	return kServer.URL, oServer.URL, func() {
		oServer.Close()
		obsSrv.Shutdown(context.Background())
		kServer.Close()
		k.Shutdown(t.Context())
	}
}

// --- Tests ---

func TestObserve_Health(t *testing.T) {
	obsURL, _, cleanup := setupObserveOnly(t)
	defer cleanup()

	resp, err := http.Get(obsURL + "/observe/health")
	if err != nil {
		t.Fatalf("Health check failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("Expected 200, got %d", resp.StatusCode)
	}

	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	if body["status"] != "ok" {
		t.Errorf("Expected status ok, got %s", body["status"])
	}
	if body["version"] == "" {
		t.Error("Expected non-empty version")
	}
}

func TestObserve_EmptyEvents(t *testing.T) {
	obsURL, _, cleanup := setupObserveOnly(t)
	defer cleanup()

	resp, err := http.Get(obsURL + "/observe/events")
	if err != nil {
		t.Fatalf("Events request failed: %v", err)
	}
	defer resp.Body.Close()

	var body map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&body)
	events := body["events"].([]interface{})
	if len(events) != 0 {
		t.Errorf("Expected empty events, got %d", len(events))
	}
}

func TestObserve_EmptyState(t *testing.T) {
	obsURL, _, cleanup := setupObserveOnly(t)
	defer cleanup()

	resp, err := http.Get(obsURL + "/observe/state")
	if err != nil {
		t.Fatalf("State request failed: %v", err)
	}
	defer resp.Body.Close()

	var body map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&body)
	keys := body["keys"].([]interface{})
	if len(keys) != 0 {
		t.Errorf("Expected empty keys, got %d", len(keys))
	}
}

func TestObserve_EmptyAgents(t *testing.T) {
	obsURL, _, cleanup := setupObserveOnly(t)
	defer cleanup()

	resp, err := http.Get(obsURL + "/observe/agents")
	if err != nil {
		t.Fatalf("Agents request failed: %v", err)
	}
	defer resp.Body.Close()

	var body map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&body)
	agents := body["agents"].([]interface{})
	if len(agents) != 0 {
		t.Errorf("Expected empty agents, got %d", len(agents))
	}
}

func TestObserve_EmptyNetwork(t *testing.T) {
	obsURL, _, cleanup := setupObserveOnly(t)
	defer cleanup()

	resp, err := http.Get(obsURL + "/observe/network")
	if err != nil {
		t.Fatalf("Network request failed: %v", err)
	}
	defer resp.Body.Close()

	var body map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&body)
	if len(body["nodes"].([]interface{})) != 0 {
		t.Error("Expected empty nodes")
	}
	if len(body["edges"].([]interface{})) != 0 {
		t.Error("Expected empty edges")
	}
}

func TestObserve_EmptyTransparency(t *testing.T) {
	obsURL, _, cleanup := setupObserveOnly(t)
	defer cleanup()

	resp, err := http.Get(obsURL + "/observe/transparency")
	if err != nil {
		t.Fatalf("Transparency request failed: %v", err)
	}
	defer resp.Body.Close()

	var body map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&body)
	if int(body["total"].(float64)) != 0 {
		t.Error("Expected 0 total removals")
	}
}

func TestObserve_EventsFromKernel(t *testing.T) {
	kernelURL, obsURL, cleanup := setupObserveWithKernelTest(t)
	defer cleanup()

	apiKey := registerOperator(t, kernelURL)
	registerAgent(t, kernelURL, apiKey)

	resp, err := http.Get(obsURL + "/observe/events")
	if err != nil {
		t.Fatalf("Events request failed: %v", err)
	}
	defer resp.Body.Close()

	var body map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&body)
	events := body["events"].([]interface{})

	if len(events) < 2 {
		t.Errorf("Expected at least 2 events, got %d", len(events))
	}

	typesSeen := make(map[string]bool)
	for _, ev := range events {
		evMap := ev.(map[string]interface{})
		typesSeen[evMap["type"].(string)] = true
	}
	if !typesSeen["operator_created"] {
		t.Error("Expected operator_created event")
	}
	if !typesSeen["agent_registered"] {
		t.Error("Expected agent_registered event")
	}
}

func TestObserve_EventsFilterByType(t *testing.T) {
	kernelURL, obsURL, cleanup := setupObserveWithKernelTest(t)
	defer cleanup()

	apiKey := registerOperator(t, kernelURL)
	registerAgent(t, kernelURL, apiKey)

	resp, err := http.Get(obsURL + "/observe/events?type=agent_registered")
	if err != nil {
		t.Fatalf("Events request failed: %v", err)
	}
	defer resp.Body.Close()

	var body map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&body)
	events := body["events"].([]interface{})

	for _, ev := range events {
		evMap := ev.(map[string]interface{})
		if evMap["type"].(string) != "agent_registered" {
			t.Errorf("Expected only agent_registered events, got %s", evMap["type"])
		}
	}
}

func TestObserve_AgentsFromKernel(t *testing.T) {
	kernelURL, obsURL, cleanup := setupObserveWithKernelTest(t)
	defer cleanup()

	apiKey := registerOperator(t, kernelURL)
	registerAgent(t, kernelURL, apiKey)

	resp, err := http.Get(obsURL + "/observe/agents")
	if err != nil {
		t.Fatalf("Agents request failed: %v", err)
	}
	defer resp.Body.Close()

	var body map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&body)
	agents := body["agents"].([]interface{})
	if len(agents) != 1 {
		t.Errorf("Expected 1 agent, got %d", len(agents))
	}
}

func TestObserve_StateReadFromDB(t *testing.T) {
	// Test state reading with a standalone bbolt DB (not shared with kernel).
	// In production, the observe API reads state when the kernel is stopped.
	tmpDir := t.TempDir()
	eventLogDir := filepath.Join(tmpDir, "eventlog")
	os.MkdirAll(eventLogDir, 0o755)

	// Create a bbolt DB with a test key
	stateDBPath := filepath.Join(tmpDir, "state.db")
	db, err := bolt.Open(stateDBPath, 0o644, nil)
	if err != nil {
		t.Fatalf("Failed to create state DB: %v", err)
	}
	db.Update(func(tx *bolt.Tx) error {
		b, _ := tx.CreateBucketIfNotExists([]byte("state"))
		entry := `{"key":"observe_test_key","value":"test_value","last_modified_by":"tab_test","last_modified_at":"2026-03-18T00:00:00Z"}`
		return b.Put([]byte("observe_test_key"), []byte(entry))
	})
	db.Close()

	cfg := observe.Config{
		Server: observe.ServerConfig{ListenAddr: ":0", ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second},
		Kernel: observe.KernelDataConfig{
			EventLogDir:         eventLogDir,
			StateDBPath:         stateDBPath,
			TransparencyLogPath: filepath.Join(tmpDir, "transparency.jsonl"),
		},
		RateLimit: observe.RateLimitConfig{RequestsPerMinPerIP: 300},
		WebSocket: observe.WebSocketConfig{MaxConnections: 10, WriteTimeout: 10 * time.Second, PingInterval: 30 * time.Second, MaxDuration: 24 * time.Hour},
	}

	srv, err := observe.NewServer(cfg)
	if err != nil {
		t.Fatalf("Failed to create observe server: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer func() { ts.Close(); srv.Shutdown(context.Background()) }()

	resp, err := http.Get(ts.URL + "/observe/state/observe_test_key")
	if err != nil {
		t.Fatalf("State read failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("Expected 200, got %d", resp.StatusCode)
	}

	var body map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&body)
	if body["value"] != "test_value" {
		t.Errorf("Expected test_value, got %v", body["value"])
	}
}

func TestObserve_StateNotFound(t *testing.T) {
	_, obsURL, cleanup := setupObserveWithKernelTest(t)
	defer cleanup()

	resp, err := http.Get(obsURL + "/observe/state/nonexistent_key")
	if err != nil {
		t.Fatalf("State read failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 404 {
		t.Errorf("Expected 404, got %d", resp.StatusCode)
	}
}

func TestObserve_NetworkFromKernel(t *testing.T) {
	kernelURL, obsURL, cleanup := setupObserveWithKernelTest(t)
	defer cleanup()

	apiKey := registerOperator(t, kernelURL)
	agent1Addr, agent1Token := registerAgent(t, kernelURL, apiKey)
	agent2Addr, _ := registerAgent(t, kernelURL, apiKey)
	_ = agent1Addr

	resp := doRequest(t, "POST", kernelURL+"/v1/messages", agent1Token, map[string]string{
		"to":      agent2Addr,
		"content": "hello from agent1",
	})
	resp.Body.Close()

	resp, err := http.Get(obsURL + "/observe/network")
	if err != nil {
		t.Fatalf("Network request failed: %v", err)
	}
	defer resp.Body.Close()

	var body map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&body)
	edges := body["edges"].([]interface{})
	if len(edges) == 0 {
		t.Error("Expected at least one edge in network graph")
	}
}

func TestObserve_TransparencyWithEntries(t *testing.T) {
	tmpDir := t.TempDir()
	eventLogDir := filepath.Join(tmpDir, "eventlog")
	os.MkdirAll(eventLogDir, 0o755)

	transparencyPath := filepath.Join(tmpDir, "transparency.jsonl")
	entry := `{"ts":"2026-03-18T06:00:00Z","action":"state_key_removed","key":"bad_key","reason":"CSAM","performed_by":"infrastructure_operator","content_hash":"abc123"}` + "\n"
	os.WriteFile(transparencyPath, []byte(entry), 0o644)

	cfg := observe.Config{
		Server:    observe.ServerConfig{ListenAddr: ":0", ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second},
		Kernel:    observe.KernelDataConfig{EventLogDir: eventLogDir, TransparencyLogPath: transparencyPath},
		RateLimit: observe.RateLimitConfig{RequestsPerMinPerIP: 300},
		WebSocket: observe.WebSocketConfig{MaxConnections: 10, WriteTimeout: 10 * time.Second, PingInterval: 30 * time.Second, MaxDuration: 24 * time.Hour},
	}

	srv, err := observe.NewServer(cfg)
	if err != nil {
		t.Fatalf("Failed to create observe server: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer func() { ts.Close(); srv.Shutdown(context.Background()) }()

	resp, err := http.Get(ts.URL + "/observe/transparency")
	if err != nil {
		t.Fatalf("Transparency request failed: %v", err)
	}
	defer resp.Body.Close()

	var body map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&body)

	if int(body["total"].(float64)) != 1 {
		t.Errorf("Expected 1 removal, got %v", body["total"])
	}

	removals := body["removals"].([]interface{})
	removal := removals[0].(map[string]interface{})
	if removal["key"] != "bad_key" {
		t.Errorf("Expected key bad_key, got %v", removal["key"])
	}
}

func TestObserve_WebSocketConnection(t *testing.T) {
	tmpDir := t.TempDir()
	eventLogDir := filepath.Join(tmpDir, "eventlog")
	os.MkdirAll(eventLogDir, 0o755)

	cfg := observe.Config{
		Server:    observe.ServerConfig{ListenAddr: ":0", ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second},
		Kernel:    observe.KernelDataConfig{EventLogDir: eventLogDir, TransparencyLogPath: filepath.Join(tmpDir, "transparency.jsonl")},
		RateLimit: observe.RateLimitConfig{RequestsPerMinPerIP: 300},
		WebSocket: observe.WebSocketConfig{MaxConnections: 10, WriteTimeout: 10 * time.Second, PingInterval: 30 * time.Second, MaxDuration: 24 * time.Hour},
	}

	srv, err := observe.NewServer(cfg)
	if err != nil {
		t.Fatalf("Failed to create observe server: %v", err)
	}

	// Use a real TCP listener so http.Hijacker works for WebSocket upgrade
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to listen: %v", err)
	}
	httpSrv := &http.Server{Handler: srv.Handler()}
	go httpSrv.Serve(ln)
	defer func() { httpSrv.Close(); srv.Shutdown(context.Background()) }()

	obsURL := "http://" + ln.Addr().String()
	wsURL := "ws://" + ln.Addr().String() + "/observe/events/stream"

	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket connect failed: %v", err)
	}
	defer ws.Close()

	// Write an event to the log
	eventLine := fmt.Sprintf(`{"seq":1,"ts":"%s","type":"message_sent","agent":"tab_test","data":{"message_id":"m1","from":"tab_test","to":"tab_other","content":"hello","content_length":5}}`, time.Now().UTC().Format(time.RFC3339Nano))
	os.WriteFile(filepath.Join(eventLogDir, "events.jsonl"), []byte(eventLine+"\n"), 0o644)

	// Read from WebSocket (with timeout)
	ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, message, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("WebSocket read failed: %v", err)
	}

	var ev map[string]interface{}
	json.Unmarshal(message, &ev)
	if ev["type"] != "message_sent" {
		t.Errorf("Expected message_sent event, got %v", ev["type"])
	}
	_ = obsURL
}

func TestObserve_EventsPagination(t *testing.T) {
	tmpDir := t.TempDir()
	eventLogDir := filepath.Join(tmpDir, "eventlog")
	os.MkdirAll(eventLogDir, 0o755)

	var lines string
	for i := 1; i <= 5; i++ {
		lines += fmt.Sprintf(`{"seq":%d,"ts":"%s","type":"state_read","agent":"tab_test","data":{"key":"k%d","read_by":"tab_test","found":true}}`, i, time.Now().UTC().Format(time.RFC3339Nano), i) + "\n"
	}
	os.WriteFile(filepath.Join(eventLogDir, "events.jsonl"), []byte(lines), 0o644)

	cfg := observe.Config{
		Server:    observe.ServerConfig{ListenAddr: ":0", ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second},
		Kernel:    observe.KernelDataConfig{EventLogDir: eventLogDir, TransparencyLogPath: filepath.Join(tmpDir, "transparency.jsonl")},
		RateLimit: observe.RateLimitConfig{RequestsPerMinPerIP: 300},
		WebSocket: observe.WebSocketConfig{MaxConnections: 10, WriteTimeout: 10 * time.Second, PingInterval: 30 * time.Second, MaxDuration: 24 * time.Hour},
	}

	srv, _ := observe.NewServer(cfg)
	ts := httptest.NewServer(srv.Handler())
	defer func() { ts.Close(); srv.Shutdown(context.Background()) }()

	// Page 1
	resp, _ := http.Get(ts.URL + "/observe/events?limit=2")
	var body map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()

	events := body["events"].([]interface{})
	if len(events) != 2 {
		t.Fatalf("Expected 2 events, got %d", len(events))
	}
	if body["has_more"] != true {
		t.Error("Expected has_more=true")
	}

	cursor := int64(body["next_cursor"].(float64))

	// Page 2
	resp, _ = http.Get(fmt.Sprintf("%s/observe/events?limit=2&since=%d", ts.URL, cursor))
	json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()

	events = body["events"].([]interface{})
	if len(events) != 2 {
		t.Fatalf("Expected 2 events on page 2, got %d", len(events))
	}

	// Page 3 (last)
	cursor = int64(body["next_cursor"].(float64))
	resp, _ = http.Get(fmt.Sprintf("%s/observe/events?limit=2&since=%d", ts.URL, cursor))
	json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()

	events = body["events"].([]interface{})
	if len(events) != 1 {
		t.Fatalf("Expected 1 event on last page, got %d", len(events))
	}
	if body["has_more"] != false {
		t.Error("Expected has_more=false on last page")
	}
}
