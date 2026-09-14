package websocket

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	gorilla "github.com/gorilla/websocket"
)

func newTestHub(t *testing.T) *Hub {
	t.Helper()
	hub := NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)), func() any {
		return map[string]string{"greeting": "hello"}
	})
	go hub.Run()
	t.Cleanup(hub.Close)
	return hub
}

// dial connects a client to a hub served over httptest.
func dial(t *testing.T, hub *Hub, origin string) (*gorilla.Conn, func()) {
	t.Helper()
	srv := httptest.NewServer(hub.Handler(func(o string) bool { return o == "http://allowed.test" }))

	header := http.Header{}
	if origin != "" {
		header.Set("Origin", origin)
	}
	conn, resp, err := gorilla.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), header)
	if err != nil {
		srv.Close()
		t.Fatalf("dial: %v (response %v)", err, resp)
	}
	return conn, func() {
		_ = conn.Close()
		srv.Close()
	}
}

// readEvent reads one event with a deadline.
func readEvent(t *testing.T, conn *gorilla.Conn) Event {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var event Event
	if err := json.Unmarshal(raw, &event); err != nil {
		t.Fatalf("decode %q: %v", raw, err)
	}
	return event
}

func TestClientReceivesHelloOnConnect(t *testing.T) {
	hub := newTestHub(t)
	conn, done := dial(t, hub, "")
	defer done()

	event := readEvent(t, conn)
	if event.Type != EventHello {
		t.Fatalf("first event = %s, want %s", event.Type, EventHello)
	}
	if event.ServerTime.IsZero() {
		t.Error("HELLO carries no server time, so clients cannot estimate their offset")
	}
	payload := event.Payload.(map[string]any)
	if payload["greeting"] != "hello" {
		t.Errorf("payload = %v", payload)
	}
}

func TestBroadcastReachesEveryClient(t *testing.T) {
	hub := newTestHub(t)

	first, done1 := dial(t, hub, "")
	defer done1()
	second, done2 := dial(t, hub, "")
	defer done2()

	readEvent(t, first)  // HELLO
	readEvent(t, second) // HELLO

	waitForClients(t, hub, 2)
	hub.Publish(EventSyncStarted, map[string]any{"id": 7})

	for i, conn := range []*gorilla.Conn{first, second} {
		event := readEvent(t, conn)
		if event.Type != EventSyncStarted {
			t.Errorf("client %d received %s", i, event.Type)
		}
		if event.Payload.(map[string]any)["id"].(float64) != 7 {
			t.Errorf("client %d payload = %v", i, event.Payload)
		}
	}
}

// TestBroadcastSurvivesADisconnectedClient is the reliability requirement: one
// client going away must not stop the others from receiving events.
func TestBroadcastSurvivesADisconnectedClient(t *testing.T) {
	hub := newTestHub(t)

	survivor, done := dial(t, hub, "")
	defer done()
	casualty, closeCasualty := dial(t, hub, "")

	readEvent(t, survivor)
	readEvent(t, casualty)
	waitForClients(t, hub, 2)

	closeCasualty()
	waitForClients(t, hub, 1)

	hub.Publish(EventPlaylistUpdated, map[string]any{"windowId": 1})
	if event := readEvent(t, survivor); event.Type != EventPlaylistUpdated {
		t.Fatalf("survivor received %s", event.Type)
	}
}

func TestPingIsAnsweredWithTheServerClock(t *testing.T) {
	hub := newTestHub(t)
	conn, done := dial(t, hub, "")
	defer done()
	readEvent(t, conn) // HELLO

	sent := time.Now().UnixMilli()
	if err := conn.WriteJSON(map[string]any{"type": "PING", "clientTime": sent}); err != nil {
		t.Fatalf("write ping: %v", err)
	}

	event := readEvent(t, conn)
	if event.Type != EventPong {
		t.Fatalf("event = %s, want %s", event.Type, EventPong)
	}
	if got := int64(event.Payload.(map[string]any)["clientTime"].(float64)); got != sent {
		t.Errorf("echoed clientTime = %d, want %d", got, sent)
	}
	if event.ServerTime.IsZero() {
		t.Error("PONG must carry the server clock")
	}
}

func TestMalformedFrameDoesNotDropTheClient(t *testing.T) {
	hub := newTestHub(t)
	conn, done := dial(t, hub, "")
	defer done()
	readEvent(t, conn) // HELLO

	if err := conn.WriteMessage(gorilla.TextMessage, []byte("{{{not json")); err != nil {
		t.Fatalf("write: %v", err)
	}
	waitForClients(t, hub, 1)

	hub.Publish(EventMediaCreated, map[string]any{"id": 1})
	if event := readEvent(t, conn); event.Type != EventMediaCreated {
		t.Fatalf("client received %s after sending a malformed frame", event.Type)
	}
}

func TestOriginIsEnforced(t *testing.T) {
	hub := newTestHub(t)
	srv := httptest.NewServer(hub.Handler(func(o string) bool { return o == "http://allowed.test" }))
	defer srv.Close()
	url := "ws" + strings.TrimPrefix(srv.URL, "http")

	header := http.Header{}
	header.Set("Origin", "http://blocked.test")
	conn, resp, err := gorilla.DefaultDialer.Dial(url, header)
	if err == nil {
		_ = conn.Close()
		t.Fatal("a blocked origin was allowed to connect")
	}
	if resp != nil && resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}

	header.Set("Origin", "http://allowed.test")
	allowed, _, err := gorilla.DefaultDialer.Dial(url, header)
	if err != nil {
		t.Fatalf("an allowed origin was rejected: %v", err)
	}
	_ = allowed.Close()
}

func TestPublishAfterCloseDoesNotPanic(t *testing.T) {
	hub := NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)), func() any { return nil })
	go hub.Run()
	hub.Close()
	time.Sleep(20 * time.Millisecond)

	// A late broadcast from an in-flight request must be harmless.
	hub.Publish(EventSyncEnded, map[string]any{"syncId": 1})
	if n := hub.ClientCount(); n != 0 {
		t.Errorf("client count after close = %d", n)
	}
}

// waitForClients polls the hub until it reports the expected client count.
func waitForClients(t *testing.T, hub *Hub, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if hub.ClientCount() == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("hub has %d clients, want %d", hub.ClientCount(), want)
}

// TestClientGoroutinesExitAfterHubClose guards a leak that only shows up at
// shutdown: once Run() has returned, nothing drains the unregister channel, so
// a readPump finishing afterwards would block on it forever.
func TestClientGoroutinesExitAfterHubClose(t *testing.T) {
	before := runtime.NumGoroutine()

	hub := NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)), func() any { return nil })
	go hub.Run()

	srv := httptest.NewServer(hub.Handler(func(string) bool { return true }))
	defer srv.Close()

	conns := make([]*gorilla.Conn, 0, 4)
	for i := 0; i < 4; i++ {
		conn, _, err := gorilla.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
		if err != nil {
			t.Fatalf("dial %d: %v", i, err)
		}
		conns = append(conns, conn)
	}
	waitForClients(t, hub, 4)

	// Stop the hub first, then drop the clients: this is the exact ordering
	// that used to strand every readPump on its unregister send.
	hub.Close()
	for _, conn := range conns {
		_ = conn.Close()
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before+2 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("goroutines leaked after hub close: %d before, %d after", before, runtime.NumGoroutine())
}

// TestConcurrentConnectBroadcastAndClose hammers the three goroutines that can
// queue a message for a client - the hub, the HTTP handler's greeting, and a
// client's own PONG - while the hub is being shut down underneath them.
//
// This is the shape that used to panic: the hub closed client.send while those
// senders were still using it.
func TestConcurrentConnectBroadcastAndClose(t *testing.T) {
	for attempt := 0; attempt < 20; attempt++ {
		hub := NewHub(slog.New(slog.NewTextHandler(io.Discard, nil)), func() any {
			return map[string]string{"greeting": "hello"}
		})
		go hub.Run()

		srv := httptest.NewServer(hub.Handler(func(string) bool { return true }))
		url := "ws" + strings.TrimPrefix(srv.URL, "http")

		var wg sync.WaitGroup

		// Clients connecting (each triggers a greeting) and pinging.
		for i := 0; i < 6; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				conn, _, err := gorilla.DefaultDialer.Dial(url, nil)
				if err != nil {
					return // The hub may already be closing; that is the point.
				}
				defer conn.Close()
				for j := 0; j < 5; j++ {
					if err := conn.WriteJSON(map[string]any{"type": "PING", "clientTime": 1}); err != nil {
						return
					}
				}
			}()
		}

		// Broadcasts racing with all of the above.
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 10; j++ {
					hub.Publish(EventPlaylistUpdated, map[string]int{"n": j})
				}
			}()
		}

		// And the shutdown, landing at an unpredictable point in the middle.
		wg.Add(1)
		go func() {
			defer wg.Done()
			time.Sleep(time.Duration(attempt%5) * time.Millisecond)
			hub.Close()
		}()

		wg.Wait()
		srv.Close()
	}
}
