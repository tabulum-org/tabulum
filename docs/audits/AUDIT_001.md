# Tabulum Kernel — Audit Tracking

This document contains the findings from the Session 3 code audit and the agreed-upon fix plan. Place this file at `docs/audits/AUDIT_001.md` in the repository.

---

## Audit Context

- **Auditor:** Claude Opus 4.6 (Session 3 — a separate session from the build session)
- **Date:** March 17, 2026
- **Scope:** Full code review of the initial kernel build against the OpenAPI spec, design document, and DECISIONS.md
- **Method:** Every implementation file was read and evaluated. The auditor had no involvement in writing the code.

---

## Findings

### CRITICAL

**C1: Recovery overwrites valid credentials after restart**

- **Files:** `internal/kernel/kernel.go`, `internal/registry/registry.go`
- **Problem:** On startup, the kernel replays the event log and calls `ApplyOperatorCreated` and `ApplyAgentRegistered`, which write new records to bbolt with empty credential hashes — overwriting the valid hashed credentials that bbolt already persisted from the original registration. After any restart, operators and agents cannot authenticate.
- **Root cause:** The recovery architecture treats the event log as the source of truth for all state, but the event log deliberately does not store credential hashes (they shouldn't be in an observable log). bbolt already persists the complete records including hashes. The replay is redundant for bbolt-backed stores and destructive for credentials.
- **Agreed fix:** Shift to bbolt-primary recovery. bbolt is the source of truth for durable state (registry, key-value store). Event log replay is only used to reconstruct in-memory structures (message queues). Skip registry and state events during replay. Add existence checks in `Apply*` methods as a defensive fallback. The event log continues to record all events for observation — it just isn't used to rebuild stores that bbolt already persists.

### HIGH

**H1: State store and registry share the same data directory**

- **File:** `internal/kernel/kernel.go` (line 54)
- **Problem:** Both use `cfg.State.DataDir`. Works (different filenames) but fragile and unclear.
- **Fix:** Add a `Registry.DataDir` config field, or document the shared directory as intentional.

**H2: Capacity check in state.Set() is not atomic with the write**

- **File:** `internal/state/state.go` (lines 128-137)
- **Problem:** `GetCapacity()` does a full cursor scan, then the write happens in a separate transaction. Under concurrency, the store can exceed `maxCapacity`. Also O(n) on every write.
- **Fix:** Maintain a running byte counter updated on writes/deletes. Do the capacity check inside the same bbolt transaction as the write.

**H3: extractStateKey does not URL-decode the key**

- **File:** `internal/api/handlers.go` (lines 502-512)
- **Problem:** Comment says "URL-decode the key" but the code doesn't call `url.PathUnescape()`. Keys with encoded characters are stored in encoded form.
- **Fix:** Add `url.PathUnescape(key)` after extraction.

**H4 (found by build session): Auth lookup is O(n) bcrypt comparisons**

- **File:** `internal/registry/registry.go` (lines 174-201, 281-309)
- **Problem:** `AuthenticateOperator` and `AuthenticateAgent` iterate every record and run `bcrypt.CompareHashAndPassword` against each. Bcrypt is ~100ms per comparison. With 50 operators, a single auth check takes ~5 seconds.
- **Fix:** Add a prefix-based index — store a short non-secret prefix of the raw key mapped to the record ID. Auth becomes O(1): look up prefix → retrieve the one candidate → bcrypt compare.

### MEDIUM

**M1: Rate limit headers missing on some endpoints**

- **Files:** `internal/api/routes.go`, `internal/api/middleware.go`
- **Problem:** `GET /v1/messages` and `GET /v1/agents/me` have no rate limit middleware. The OpenAPI spec says rate limit headers are on every response.
- **Fix:** Add rate limiting to these endpoints.

**M2: GET /v1/state/_capacity is not rate-limited**

- **File:** `internal/api/routes.go` (lines 60-62)
- **Problem:** No rate limit on the capacity endpoint. Combined with H2 (full cursor scan), this is a performance risk.
- **Fix:** Add `RateLimit(limiter, ratelimit.CategoryStateRead, ...)`.

**M3: Webhook agents receive messages twice (pull queue + webhook)**

- **File:** `internal/messaging/messaging.go` (lines 96-102)
- **Problem:** Messages are always queued for pull delivery, then also sent via webhook if the recipient has one. Successful webhook delivery doesn't remove the message from the pull queue. The recipient can retrieve the same message twice.
- **Fix:** Don't queue for pull when the recipient has a webhook. The webhook delivery's fallback mechanism already handles queuing for pull on failure.

**M4: readJSON reads entire body into memory before parsing**

- **File:** `internal/api/handlers.go` (lines 494-500)
- **Problem:** Uses `io.ReadAll` + `json.Unmarshal` instead of `json.NewDecoder(r.Body).Decode(v)`. Allocates up to 2MB per request before validation.
- **Fix:** Use `json.NewDecoder(r.Body).Decode(v)`.

**M5: Event log replay fails entirely on corrupted lines**

- **File:** `internal/logging/eventlog.go` (lines 266-269)
- **Problem:** If the kernel crashes mid-write, the log has a truncated JSON line. On replay, `json.Unmarshal` fails and `replayFile` returns an error, halting recovery. The kernel won't start.
- **Fix:** Log a warning for unparseable lines and skip them. At most one event (the one being written during the crash) is lost. The design document accepts this as analogous to packet loss.

**M6 (found by build session): TestRecovery_UndeliveredMessagesRecovered doesn't verify recovery**

- **File:** `test/integration_test.go` (lines 674-707)
- **Problem:** The test sends a message, restarts the kernel, but only checks the health endpoint — never verifies the message was recovered. This is a symptom of C1 (can't authenticate after restart). Once C1 is fixed, this test should authenticate with the original agent token and verify message retrieval.
- **Fix:** Update the test after C1 is resolved.

### LOW

**L1: NewWithConfig duplicates ~175 lines from New**

- **File:** `internal/kernel/kernel.go`
- **Fix:** Have `New` call `config.Load()` then delegate to `NewWithConfig`.

**L2: Agent address uses only lowercase alphanumeric (36 chars, not 62)**

- **File:** `internal/registry/registry.go` (line 205)
- **Problem:** ~103 bits of entropy instead of ~119 bits. Still sufficient.
- **Fix:** Either add uppercase or update DECISIONS.md.

**L3: Health endpoint hardcodes version instead of calling kernel.Version()**

- **File:** `internal/api/handlers.go` (line 475)
- **Fix:** Call `kernel.Version()` or pass version through the Handlers struct.

**L4 (found by build session): ListKeys total count may be misleading with prefix + cursor**

- **File:** `internal/state/state.go` (lines 157-167)
- **Problem:** `total` reflects all prefix-matching keys, not the remaining count after cursor. Arguably correct but could confuse pagination consumers.
- **Fix:** Clarify in API docs or return both `total` and `remaining`.

---

## Agreed Fix Priority

1. **C1** — Shift to bbolt-primary recovery. Skip registry and state replay during log recovery. Only replay message events for queue reconstruction. Add existence checks in Apply* methods.
2. **M5** — Skip corrupted log lines during replay instead of failing.
3. **H4** — Add prefix index to registry auth (do while registry is open for C1).
4. **H3** — URL-decode state keys.
5. **M3** — Don't double-queue for webhook agents.
6. **H2** — Atomic capacity check with running byte counter.
7. **M1/M2** — Add missing rate limits.
8. **L1** — Deduplicate New/NewWithConfig.
9. **M4** — Use json.NewDecoder.
10. **M6** — Fix recovery test after C1.
11. Remaining low-priority items in any order.

---

## Recovery Architecture Decision

**Decision:** bbolt is the source of truth for durable state. The event log is the source of truth for observation and for recovering in-memory structures only.

**Rationale:** The event log deliberately excludes credential hashes (they shouldn't be in an observable log). bbolt already persists complete records including hashes across restarts. Replaying the event log on top of bbolt is redundant for most state and destructive for credentials. Separating the responsibilities — bbolt for persistence, event log for observation and in-memory recovery — is the clean architecture.

**What changes:**
- Startup recovery only replays `EventMessageSent` and `EventMessageDelivered` to rebuild in-memory message queues.
- `EventOperatorCreated`, `EventAgentRegistered`, `EventStateWritten`, and `EventStateDeleted` are skipped during replay (bbolt has them).
- The event log continues recording all events for the observation layer.
- `ApplyStateWrite` and `ApplyStateDelete` methods can be removed or retained as dead code.
- `ApplyOperatorCreated` and `ApplyAgentRegistered` should check for existing records before writing, as a defensive measure in case someone manually triggers a full replay.

**Observation layer contract:** The event log stores full message content and state values (per DECISIONS.md D9) but NOT credential hashes. This is correct and should not change. The observation layer needs to see what agents are doing — it does not need the ability to authenticate as agents.

---

*After fixes are applied, re-upload the codebase to Session 3 for a focused re-audit on the changed code.*
