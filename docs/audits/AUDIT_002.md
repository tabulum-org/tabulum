# Tabulum — Observation API Audit (AUDIT_002)

**Auditor:** Claude Opus 4.6 (Session 3)  
**Date:** March 18, 2026  
**Scope:** Full review of the observation API implementation against build instructions, security requirements, and safety policy  
**Result:** 55 tests passing. Architecture is correct. Three issues found, one safety-critical.

---

## Findings

### S1 (Medium — Safety): Message content not redacted in events endpoints

**Files:** `internal/observe/handlers.go` (StreamEvents), `internal/observe/websocket.go` (broadcastNewEvents)

**Problem:** The `ReadState` handler correctly checks `transparency.IsRedacted()` before serving state values — if a key has been removed under the safety policy, the observation API returns a redaction notice instead of the content. Good.

However, the `StreamEvents` REST endpoint and the WebSocket broadcast serve event data including full `message_sent` content with no redaction check. If a message is redacted via the transparency log (action: `message_redacted`), the observation API still serves the raw message content through the events stream. This means redacted illegal content remains accessible through the observation API's event endpoints even after it has been "removed."

**Fix:**

1. Update `TransparencyLog` to also track redacted message IDs:

```go
type TransparencyLog struct {
    mu             sync.RWMutex
    path           string
    removals       []Removal
    redactedKeys   map[string]string // key -> reason
    redactedMsgIDs map[string]string // message_id -> reason  // ADD THIS
}
```

In `load()`, also index `message_redacted` entries:
```go
if r.MessageID != "" && r.Action == "message_redacted" {
    t.redactedMsgIDs[r.MessageID] = r.Reason
}
```

Add a method:
```go
func (t *TransparencyLog) IsMessageRedacted(messageID string) (bool, string) {
    t.mu.RLock()
    defer t.mu.RUnlock()
    reason, ok := t.redactedMsgIDs[messageID]
    return ok, reason
}
```

2. In `StreamEvents` (handlers.go), after querying events, filter them before serving:

```go
for i, ev := range events {
    if ev.Type == "message_sent" {
        data := extractMap(ev.Data)
        msgID, _ := data["message_id"].(string)
        if redacted, reason := h.transparency.IsMessageRedacted(msgID); redacted {
            data["content"] = "[REDACTED — see /observe/transparency]"
            data["redacted"] = true
            data["redacted_reason"] = reason
            events[i].Data = data
        }
    }
}
```

3. In `broadcastNewEvents` (websocket.go), apply the same filter before writing to each WebSocket client. The WebSocket hub needs a reference to the transparency log:

```go
type WebSocketHub struct {
    events         *EventReader
    transparency   *TransparencyLog  // ADD THIS
    // ... rest of fields
}
```

Pass it in `NewWebSocketHub` and check each event before `WriteJSON`.

---

### S2 (Low): CORS headers missing on REST endpoints

**File:** `internal/observe/routes.go`

**Problem:** The build instructions specified `Access-Control-Allow-Origin: *` for the observation API since observation tools may run in browsers. The WebSocket upgrader allows cross-origin connections (`CheckOrigin` returns true), but REST endpoints have no CORS headers. Browser-based tools cannot call the REST API.

**Fix:** Add CORS middleware to the observation API route chain:

```go
func CORSMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Access-Control-Allow-Origin", "*")
        w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
        w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
        if r.Method == "OPTIONS" {
            w.WriteHeader(http.StatusOK)
            return
        }
        next.ServeHTTP(w, r)
    })
}
```

Add to the middleware chain in `NewRouter`:
```go
handler = CORSMiddleware(handler)
handler = RateLimitMiddleware(limiter)(handler)
handler = RequestLogger(handler)
```

---

### S3 (Medium — Operational): Content removal CLI not built

**Problem:** The build instructions specified `cmd/remove/main.go` as a CLI tool for the infrastructure operator to remove content from shared state and redact messages from the event log, with all removals recorded in the transparency log. This tool does not exist in the codebase.

The transparency log *reader* is implemented (the observation API reads it), but there is no tool to *write* to it. Currently, if illegal content is discovered, the operator would need to manually edit bbolt databases and hand-write JSON lines to the transparency log — error-prone and unacceptable under time pressure.

**Fix:** Build the removal CLI as specified in the original build instructions (`BUILD_SESSION_OBSERVE_AND_SAFETY.md`, Section 2). Key requirements:

- `./bin/remove state --key "key" --reason "CSAM" --data-dir ./data` — deletes a state key from bbolt, writes transparency log entry
- `./bin/remove message --id "uuid" --reason "CSAM" --data-dir ./data` — redacts message content in the event log, writes transparency log entry
- `./bin/remove list --data-dir ./data` — displays all removal records
- Confirmation prompt before each removal
- The kernel must be stopped (bbolt exclusive lock). Print clear error if DB is locked.
- Write SHA-256 hash of removed content to the transparency log

File structure:
```
cmd/remove/main.go
internal/remove/
  state.go
  message.go
  transparency.go
```

---

## What passed audit:

- ✅ Separate process isolation (kernel :8080, observe :8090)
- ✅ bbolt read-only with timeout and graceful degradation
- ✅ IP-based rate limiting (300/min) with stale entry cleanup
- ✅ WebSocket hardening (max connections, write timeout, ping/pong, max duration, read limit)
- ✅ State key redaction via transparency log
- ✅ No credential leakage (zero references to hashes, keys, or tokens in observe package)
- ✅ Event log corruption handling (skip unparseable lines)
- ✅ Gitignore covers security notes and marketing doc
- ✅ Operator guide has TLS deployment note and privacy section
- ✅ Safety policy committed to docs/
- ✅ All 16 observation tests pass with race detector

---

## Fix Priority

1. **S1** — Message redaction in events endpoints (safety-critical — redacted content still accessible)
2. **S3** — Content removal CLI (operationally critical — no tool to perform removals)
3. **S2** — CORS headers (quality-of-life for browser tools)

---

*Place this file at `docs/audits/AUDIT_002.md` in the repository.*
