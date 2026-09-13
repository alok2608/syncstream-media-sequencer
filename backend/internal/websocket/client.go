package websocket

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// pongWait is how long a client may go silent before it is disconnected.
	pongWait = 60 * time.Second
	// pingPeriod must be comfortably below pongWait.
	pingPeriod = 25 * time.Second
	// writeWait bounds a single write so a wedged socket cannot leak a goroutine.
	writeWait = 10 * time.Second
	// maxMessageSize caps inbound frames; clients only ever send small pings.
	maxMessageSize = 1024
)

// Client is one browser connection.
type Client struct {
	hub    *Hub
	conn   *websocket.Conn
	send   chan []byte
	logger *slog.Logger
}

// Handler returns an http.Handler that upgrades requests to WebSocket.
// originAllowed is the same allow-list used for CORS, so the realtime channel
// is not more permissive than the REST API.
func (h *Hub) Handler(originAllowed func(origin string) bool) http.HandlerFunc {
	upgrader := websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 4096,
		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			// Non-browser clients (curl, tests, health probes) send no Origin.
			if origin == "" {
				return true
			}
			return originAllowed(origin)
		},
	}

	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			// Upgrade already wrote an error response.
			h.logger.Warn("websocket upgrade failed", "error", err)
			return
		}

		client := &Client{hub: h, conn: conn, send: make(chan []byte, clientSendBuffer), logger: h.logger}

		select {
		case h.register <- client:
		case <-h.done:
			_ = conn.Close()
			return
		}

		client.greet()
		go client.writePump()
		go client.readPump()
	}
}

// greet queues the HELLO event carrying the server clock and any live sync.
func (c *Client) greet() {
	data, err := json.Marshal(Event{
		Type:       EventHello,
		ServerTime: time.Now().UTC(),
		Payload:    c.hub.hello(),
	})
	if err != nil {
		c.logger.Error("encode hello event", "error", err)
		return
	}
	select {
	case c.send <- data:
	default:
	}
}

// readPump consumes client messages and keeps the connection alive. It is the
// only goroutine that reads from the connection.
func (c *Client) readPump() {
	defer func() {
		c.hub.unregister <- c
		_ = c.conn.Close()
	}()

	c.conn.SetReadLimit(maxMessageSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	for {
		_, raw, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				c.logger.Debug("websocket read ended", "error", err)
			}
			return
		}
		_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))

		var msg clientMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue // Ignore malformed frames rather than dropping the client.
		}
		if msg.Type == clientMessagePing {
			c.replyPong(msg.ClientTime)
		}
	}
}

// replyPong echoes the client's timestamp alongside the server's, which is how
// the browser estimates its clock offset.
func (c *Client) replyPong(clientTime int64) {
	data, err := json.Marshal(Event{
		Type:       EventPong,
		ServerTime: time.Now().UTC(),
		Payload:    map[string]int64{"clientTime": clientTime},
	})
	if err != nil {
		return
	}
	select {
	case c.send <- data:
	default: // Queue is full; the next ping will succeed.
	}
}

// writePump is the only goroutine that writes to the connection, which is what
// makes concurrent broadcasts safe.
func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		_ = c.conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// The hub closed this client.
				_ = c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}

		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
