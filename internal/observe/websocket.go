package observe

import (
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 4096,
	// No CORS — observation API is not browser-facing by default.
	// Operators who want browser access should put a reverse proxy in front.
	CheckOrigin: func(r *http.Request) bool { return true },
}

// WebSocketHub manages active WebSocket connections and broadcasts events.
type WebSocketHub struct {
	events         *EventReader
	transparency   *TransparencyLog
	maxConnections int
	writeTimeout   time.Duration
	pingInterval   time.Duration
	maxDuration    time.Duration
	activeConns    atomic.Int64
	mu             sync.Mutex
	clients        map[*wsClient]struct{}
	done           chan struct{}
}

type wsClient struct {
	conn    *websocket.Conn
	types   []string
	agent   string
	lastSeq int64
}

func NewWebSocketHub(events *EventReader, transparency *TransparencyLog, cfg WebSocketConfig) *WebSocketHub {
	h := &WebSocketHub{
		events:         events,
		transparency:   transparency,
		maxConnections: cfg.MaxConnections,
		writeTimeout:   cfg.WriteTimeout,
		pingInterval:   cfg.PingInterval,
		maxDuration:    cfg.MaxDuration,
		clients:        make(map[*wsClient]struct{}),
		done:           make(chan struct{}),
	}
	go h.broadcastLoop()
	return h
}

func (h *WebSocketHub) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	if int(h.activeConns.Load()) >= h.maxConnections {
		writeError(w, http.StatusServiceUnavailable, "too_many_connections", "Maximum WebSocket connections reached")
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[observe] WebSocket upgrade error: %v", err)
		return
	}

	h.activeConns.Add(1)

	types := ParseEventTypes(r.URL.Query().Get("type"))
	agent := r.URL.Query().Get("agent")

	client := &wsClient{
		conn:    conn,
		types:   types,
		agent:   agent,
		lastSeq: h.events.LastSequence(),
	}

	h.mu.Lock()
	h.clients[client] = struct{}{}
	h.mu.Unlock()

	// Set up read pump (handles pongs and detects disconnects)
	go h.readPump(client)
}

func (h *WebSocketHub) readPump(client *wsClient) {
	defer func() {
		h.mu.Lock()
		delete(h.clients, client)
		h.mu.Unlock()
		h.activeConns.Add(-1)
		client.conn.Close()
	}()

	client.conn.SetReadLimit(512) // clients only send pong frames
	client.conn.SetReadDeadline(time.Now().Add(h.maxDuration))
	client.conn.SetPongHandler(func(string) error {
		// Reset read deadline on pong
		remaining := time.Until(time.Now().Add(h.maxDuration))
		if remaining > h.pingInterval*2 {
			client.conn.SetReadDeadline(time.Now().Add(h.pingInterval * 2))
		}
		return nil
	})

	for {
		_, _, err := client.conn.ReadMessage()
		if err != nil {
			break
		}
	}
}

func (h *WebSocketHub) broadcastLoop() {
	ticker := time.NewTicker(500 * time.Millisecond)
	pingTicker := time.NewTicker(h.pingInterval)
	defer ticker.Stop()
	defer pingTicker.Stop()

	for {
		select {
		case <-ticker.C:
			h.events.Refresh()
			h.broadcastNewEvents()

		case <-pingTicker.C:
			h.pingAll()

		case <-h.done:
			return
		}
	}
}

func (h *WebSocketHub) broadcastNewEvents() {
	h.mu.Lock()
	clients := make([]*wsClient, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.Unlock()

	for _, client := range clients {
		events := h.events.NewEventsAfter(client.lastSeq, client.types, client.agent)
		for _, ev := range events {
			// Redact message content for messages flagged in the transparency log
			if ev.Type == "message_sent" {
				data := extractMap(ev.Data)
				if data != nil {
					msgID, _ := data["message_id"].(string)
					if redacted, reason := h.transparency.IsMessageRedacted(msgID); redacted {
						data["content"] = "[REDACTED — see /observe/transparency]"
						data["redacted"] = true
						data["redacted_reason"] = reason
						ev.Data = data
					}
				}
			}

			client.conn.SetWriteDeadline(time.Now().Add(h.writeTimeout))
			if err := client.conn.WriteJSON(ev); err != nil {
				client.conn.Close()
				break
			}
			client.lastSeq = ev.Sequence
		}
	}
}

func (h *WebSocketHub) pingAll() {
	h.mu.Lock()
	clients := make([]*wsClient, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.Unlock()

	for _, client := range clients {
		client.conn.SetWriteDeadline(time.Now().Add(h.writeTimeout))
		if err := client.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
			client.conn.Close()
		}
	}
}

func (h *WebSocketHub) Close() {
	close(h.done)
	h.mu.Lock()
	for c := range h.clients {
		c.conn.Close()
	}
	h.mu.Unlock()
}
