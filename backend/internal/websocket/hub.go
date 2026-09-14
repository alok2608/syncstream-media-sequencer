package websocket

import (
	"encoding/json"
	"log/slog"
	"time"
)

// clientSendBuffer is how many messages may queue for one slow client before
// it is dropped. Dropping a stalled client protects every other client from
// being blocked behind it.
const clientSendBuffer = 32

// Hub fans messages out to connected clients. All shared state lives in the
// single run() goroutine and is reached only through channels, so there is no
// lock to forget and no data race to introduce.
type Hub struct {
	clients    map[*Client]struct{}
	register   chan *Client
	unregister chan *Client
	broadcast  chan []byte
	countReq   chan chan int
	done       chan struct{}

	// hello builds the greeting payload for a newly connected client. It is
	// supplied by the service layer so the hub stays free of domain logic.
	hello  func() any
	logger *slog.Logger
}

// NewHub creates a hub. helloPayload is invoked per connection.
func NewHub(logger *slog.Logger, helloPayload func() any) *Hub {
	return &Hub{
		clients:    make(map[*Client]struct{}),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		broadcast:  make(chan []byte, 64),
		countReq:   make(chan chan int),
		done:       make(chan struct{}),
		hello:      helloPayload,
		logger:     logger,
	}
}

// Run owns the hub's state until ctx-driven Close is called.
func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.clients[client] = struct{}{}

		case client := <-h.unregister:
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				client.close()
			}

		case message := <-h.broadcast:
			for client := range h.clients {
				select {
				case client.send <- message:
				default:
					// The client is not draining its queue; drop it rather than
					// letting one dead connection stall the broadcast.
					h.logger.Warn("dropping unresponsive websocket client")
					delete(h.clients, client)
					client.close()
				}
			}

		case reply := <-h.countReq:
			reply <- len(h.clients)

		case <-h.done:
			for client := range h.clients {
				delete(h.clients, client)
				client.close()
			}
			return
		}
	}
}

// Close stops the hub and disconnects every client.
func (h *Hub) Close() {
	select {
	case <-h.done:
	default:
		close(h.done)
	}
}

// Publish encodes and fans out an event. It never blocks the caller for long
// and never panics, so a failing client can never break a request handler.
func (h *Hub) Publish(eventType string, payload any) {
	data, err := json.Marshal(Event{
		Type:       eventType,
		ServerTime: time.Now().UTC(),
		Payload:    payload,
	})
	if err != nil {
		h.logger.Error("encode websocket event", "type", eventType, "error", err)
		return
	}
	select {
	case h.broadcast <- data:
	case <-h.done:
	case <-time.After(time.Second):
		h.logger.Warn("websocket broadcast queue is full, dropping event", "type", eventType)
	}
}

// ClientCount reports the number of connected clients, for /health.
func (h *Hub) ClientCount() int {
	reply := make(chan int, 1)
	select {
	case h.countReq <- reply:
		return <-reply
	case <-h.done:
		return 0
	case <-time.After(time.Second):
		return 0
	}
}
