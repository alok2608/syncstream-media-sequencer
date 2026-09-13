// Package websocket implements the realtime channel used to push playlist
// changes and global sync commands to every connected browser.
package websocket

import "time"

// Event types pushed from the server to clients.
const (
	// EventHello is sent once per connection with the server clock and the
	// currently live sync override, so a page that loads mid-sync joins it.
	EventHello = "HELLO"
	// EventPong answers a client PING and carries the server clock, which the
	// client uses to estimate its clock offset.
	EventPong = "PONG"

	EventPlaylistUpdated = "PLAYLIST_UPDATED"
	EventWindowCreated   = "WINDOW_CREATED"
	EventMediaCreated    = "MEDIA_CREATED"

	// EventSyncStarted carries backend-generated startAt/endAt timestamps. The
	// client derives the sync state from those timestamps, never from the
	// moment the message happened to arrive.
	EventSyncStarted   = "SYNC_STARTED"
	EventSyncEnded     = "SYNC_ENDED"
	EventSyncCancelled = "SYNC_CANCELLED"
)

// Message types a client may send.
const clientMessagePing = "PING"

// Event is the envelope for every server-to-client message. ServerTime is
// always present so clients can continuously refine their clock offset.
type Event struct {
	Type       string    `json:"type"`
	ServerTime time.Time `json:"serverTime"`
	Payload    any       `json:"payload,omitempty"`
}

// clientMessage is the envelope for client-to-server messages.
type clientMessage struct {
	Type string `json:"type"`
	// ClientTime is echoed back in the PONG so the client can measure the
	// round trip and halve it when estimating the offset.
	ClientTime int64 `json:"clientTime"`
}
