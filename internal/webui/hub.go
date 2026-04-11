package webui

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"

	"flush-detector/internal/flush"

	"github.com/gorilla/websocket"
)

type Envelope struct {
	Type    string `json:"type"`
	Payload any    `json:"payload,omitempty"`
}

type Hub struct {
	log *slog.Logger

	mu          sync.RWMutex
	upgrader    websocket.Upgrader
	clients     map[*client]struct{}
	history     []flush.Alert
	historySize int
	status      any
	config      any
	watchlist   any
	gappers     any
}

type client struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func NewHub(log *slog.Logger, historySize int) *Hub {
	if historySize <= 0 {
		historySize = 200
	}
	return &Hub{
		log: log,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(*http.Request) bool { return true },
		},
		clients:     make(map[*client]struct{}),
		historySize: historySize,
	}
}

func (h *Hub) SetHistory(alerts []flush.Alert) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.history = append([]flush.Alert(nil), alerts...)
	if len(h.history) > h.historySize {
		h.history = h.history[len(h.history)-h.historySize:]
	}
}

func (h *Hub) History() []flush.Alert {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]flush.Alert, len(h.history))
	copy(out, h.history)
	return out
}

func (h *Hub) SetStatus(payload any) {
	h.mu.Lock()
	h.status = payload
	h.mu.Unlock()
	h.Broadcast("status", payload)
}

func (h *Hub) SetConfig(payload any) {
	h.mu.Lock()
	h.config = payload
	h.mu.Unlock()
	h.Broadcast("config", payload)
}

func (h *Hub) SetWatchlist(payload any) {
	h.mu.Lock()
	h.watchlist = payload
	h.mu.Unlock()
	h.Broadcast("watchlist", payload)
}

func (h *Hub) SetGappers(payload any) {
	h.mu.Lock()
	h.gappers = payload
	h.mu.Unlock()
	h.Broadcast("gappers", payload)
}

func (h *Hub) AddAlert(alert flush.Alert) {
	h.mu.Lock()
	h.history = append(h.history, alert)
	if len(h.history) > h.historySize {
		h.history = h.history[len(h.history)-h.historySize:]
	}
	h.mu.Unlock()
	h.Broadcast("flush_alert", alert)
}

func (h *Hub) ReplaceHistory(alerts []flush.Alert) {
	h.SetHistory(alerts)
	h.Broadcast("history", h.History())
}

func (h *Hub) Broadcast(messageType string, payload any) {
	msg, err := json.Marshal(Envelope{Type: messageType, Payload: payload})
	if err != nil {
		h.log.Error("marshal websocket payload", "error", err)
		return
	}

	h.mu.RLock()
	clients := make([]*client, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.RUnlock()

	for _, c := range clients {
		if err := c.write(websocket.TextMessage, msg); err != nil {
			h.removeClient(c)
		}
	}
}

func (h *Hub) HandleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.log.Error("upgrade websocket", "error", err)
		return
	}
	c := &client{conn: conn}
	h.mu.Lock()
	h.clients[c] = struct{}{}
	status := h.status
	config := h.config
	watchlist := h.watchlist
	gappers := h.gappers
	history := append([]flush.Alert(nil), h.history...)
	h.mu.Unlock()

	if status != nil {
		_ = c.writeJSON(Envelope{Type: "status", Payload: status})
	}
	if config != nil {
		_ = c.writeJSON(Envelope{Type: "config", Payload: config})
	}
	if watchlist != nil {
		_ = c.writeJSON(Envelope{Type: "watchlist", Payload: watchlist})
	}
	if gappers != nil {
		_ = c.writeJSON(Envelope{Type: "gappers", Payload: gappers})
	}
	_ = c.writeJSON(Envelope{Type: "history", Payload: history})

	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}
	h.removeClient(c)
}

func (h *Hub) removeClient(c *client) {
	h.mu.Lock()
	if _, ok := h.clients[c]; ok {
		delete(h.clients, c)
		_ = c.conn.Close()
	}
	h.mu.Unlock()
}

func (c *client) writeJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.write(websocket.TextMessage, data)
}

func (c *client) write(messageType int, data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.WriteMessage(messageType, data)
}
