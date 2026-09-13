package handlers_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/syncstream/media-sequencer/internal/config"
	"github.com/syncstream/media-sequencer/internal/handlers"
	"github.com/syncstream/media-sequencer/internal/service"
	"github.com/syncstream/media-sequencer/internal/testsupport"
)

// testServer spins the full HTTP stack (router, middleware, handlers, service)
// over in-memory storage, so these tests cover real routing and status codes.
type testServer struct {
	*httptest.Server
	events *testsupport.Recorder
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()

	mem := testsupport.NewMemoryStores()
	events := &testsupport.Recorder{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	sequencer := service.New(service.Stores{
		Windows: mem, Media: mem.MediaStore(), Playlist: mem, Sync: mem.SyncStore(),
	}, events, logger, 500*time.Millisecond)
	t.Cleanup(sequencer.Shutdown)

	cfg := config.Config{
		Port:           "0",
		AllowedOrigins: []string{"http://localhost:5173"},
		SyncLeadTime:   500 * time.Millisecond,
	}
	api := handlers.NewAPI(sequencer, logger, func() int { return 0 })
	notImplemented := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})

	srv := httptest.NewServer(handlers.Router(api, notImplemented, cfg, logger))
	t.Cleanup(srv.Close)
	return &testServer{Server: srv, events: events}
}

// do performs a request and decodes the JSON envelope.
func (ts *testServer) do(t *testing.T, method, path string, body any) (int, map[string]any) {
	t.Helper()

	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, ts.URL+path, reader)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()

	var decoded map[string]any
	raw, _ := io.ReadAll(resp.Body)
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("%s %s returned non-JSON body %q", method, path, string(raw))
		}
	}
	return resp.StatusCode, decoded
}

// createWindow seeds a window with the given item durations and returns its id.
func (ts *testServer) createWindow(t *testing.T, name string, durations ...int) float64 {
	t.Helper()
	status, body := ts.do(t, http.MethodPost, "/api/windows", map[string]any{"name": name})
	if status != http.StatusCreated {
		t.Fatalf("create window: status %d body %v", status, body)
	}
	id := body["data"].(map[string]any)["id"].(float64)

	for _, d := range durations {
		status, body := ts.do(t, http.MethodPost, "/api/windows/"+itoa(id)+"/playlist", map[string]any{
			"media": map[string]any{
				"name": "item", "type": "image",
				"url": "https://example.test/a.png", "durationSeconds": d,
			},
		})
		if status != http.StatusCreated {
			t.Fatalf("add playlist item: status %d body %v", status, body)
		}
	}
	return id
}

// itoa renders an id decoded from JSON (always a float64) as a path segment.
func itoa(id float64) string { return strconv.FormatInt(int64(id), 10) }

func TestHealthEndpoint(t *testing.T) {
	ts := newTestServer(t)
	status, body := ts.do(t, http.MethodGet, "/health", nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	data := body["data"].(map[string]any)
	if data["status"] != "ok" {
		t.Errorf("status field = %v", data["status"])
	}
	if data["cycleMillis"].(float64) != 18_000_000 {
		t.Errorf("cycleMillis = %v, want 18000000", data["cycleMillis"])
	}
}

func TestStateEndpointReturnsBootstrapSnapshot(t *testing.T) {
	ts := newTestServer(t)
	ts.createWindow(t, "Window 1", 10, 20, 30)

	status, body := ts.do(t, http.MethodGet, "/api/state", nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	data := body["data"].(map[string]any)
	for _, key := range []string{"serverTime", "cycleMillis", "windows", "media", "activeSync"} {
		if _, ok := data[key]; !ok {
			t.Errorf("snapshot is missing %q", key)
		}
	}
	if len(data["windows"].([]any)) != 1 {
		t.Errorf("expected 1 window")
	}
}

func TestWindowAndPlaylistLifecycle(t *testing.T) {
	ts := newTestServer(t)
	id := ts.createWindow(t, "Window 1", 10, 20, 30)
	path := "/api/windows/" + itoa(id)

	status, body := ts.do(t, http.MethodGet, path+"/playlist", nil)
	if status != http.StatusOK {
		t.Fatalf("get playlist: %d", status)
	}
	items := body["data"].([]any)
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}
	last := items[2].(map[string]any)["id"].(float64)

	// Reorder the last item to the front.
	status, body = ts.do(t, http.MethodPatch, path+"/playlist/"+itoa(last), map[string]any{"position": 0})
	if status != http.StatusOK {
		t.Fatalf("reorder: %d body %v", status, body)
	}
	reordered := body["data"].(map[string]any)["playlist"].([]any)
	if reordered[0].(map[string]any)["id"].(float64) != last {
		t.Error("reorder did not move the item to the front")
	}

	// Delete it.
	status, body = ts.do(t, http.MethodDelete, path+"/playlist/"+itoa(last), nil)
	if status != http.StatusOK {
		t.Fatalf("delete: %d body %v", status, body)
	}
	if len(body["data"].(map[string]any)["playlist"].([]any)) != 2 {
		t.Error("delete did not shrink the playlist")
	}
}

func TestCurrentPlaybackEndpoint(t *testing.T) {
	ts := newTestServer(t)
	id := ts.createWindow(t, "Window 1", 10, 20, 30)

	status, body := ts.do(t, http.MethodGet, "/api/windows/"+itoa(id)+"/current", nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	data := body["data"].(map[string]any)
	if data["cycleMillis"].(float64) != 18_000_000 {
		t.Errorf("cycleMillis = %v", data["cycleMillis"])
	}
	state, ok := data["state"].(map[string]any)
	if !ok {
		t.Fatalf("no playback state returned: %v", data)
	}
	// A window created moments ago is at the very start of its first cycle.
	if state["index"].(float64) != 0 || state["cycleIndex"].(float64) != 0 {
		t.Errorf("unexpected state: %v", state)
	}
	if data["syncOverride"] != nil {
		t.Error("no sync should be active")
	}
}

func TestSyncEndpoints(t *testing.T) {
	ts := newTestServer(t)
	id := ts.createWindow(t, "Window 1", 10, 20)

	_, body := ts.do(t, http.MethodGet, "/api/windows/"+itoa(id)+"/playlist", nil)
	mediaID := body["data"].([]any)[1].(map[string]any)["media"].(map[string]any)["id"].(float64)

	// No sync yet.
	status, body := ts.do(t, http.MethodGet, "/api/sync/active", nil)
	if status != http.StatusOK || body["data"] != nil {
		t.Fatalf("expected a null active sync, got %d %v", status, body)
	}

	// Cancelling nothing is a 404, not a 500.
	if status, _ = ts.do(t, http.MethodPost, "/api/sync/cancel", nil); status != http.StatusNotFound {
		t.Errorf("cancel with no sync = %d, want 404", status)
	}

	status, body = ts.do(t, http.MethodPost, "/api/sync", map[string]any{
		"mediaId": mediaID, "durationSeconds": 30,
	})
	if status != http.StatusCreated {
		t.Fatalf("start sync: %d body %v", status, body)
	}
	event := body["data"].(map[string]any)
	startAt, err := time.Parse(time.RFC3339Nano, event["startAt"].(string))
	if err != nil {
		t.Fatalf("parse startAt: %v", err)
	}
	endAt, err := time.Parse(time.RFC3339Nano, event["endAt"].(string))
	if err != nil {
		t.Fatalf("parse endAt: %v", err)
	}
	if endAt.Sub(startAt) != 30*time.Second {
		t.Errorf("sync window = %s, want 30s", endAt.Sub(startAt))
	}
	if !startAt.After(time.Now().UTC()) {
		t.Error("startAt should be scheduled slightly in the future")
	}

	// A client that refreshes now must see it.
	status, body = ts.do(t, http.MethodGet, "/api/sync/active", nil)
	if status != http.StatusOK || body["data"] == nil {
		t.Fatalf("active sync not visible after start: %d %v", status, body)
	}

	if status, _ = ts.do(t, http.MethodPost, "/api/sync/cancel", nil); status != http.StatusOK {
		t.Errorf("cancel = %d, want 200", status)
	}
}

func TestValidationErrorsUseUnprocessableEntity(t *testing.T) {
	ts := newTestServer(t)
	id := ts.createWindow(t, "Window 1", 10)

	cases := []struct {
		name   string
		method string
		path   string
		body   any
		status int
	}{
		{"media without a url", http.MethodPost, "/api/media",
			map[string]any{"name": "x", "type": "image", "durationSeconds": 5}, http.StatusUnprocessableEntity},
		{"media with an unknown type", http.MethodPost, "/api/media",
			map[string]any{"name": "x", "type": "pdf", "url": "https://a.test/x", "durationSeconds": 5}, http.StatusUnprocessableEntity},
		{"sync with an unknown media", http.MethodPost, "/api/sync",
			map[string]any{"mediaId": 987654, "durationSeconds": 10}, http.StatusUnprocessableEntity},
		{"sync with a zero duration", http.MethodPost, "/api/sync",
			map[string]any{"mediaId": 1, "durationSeconds": 0}, http.StatusUnprocessableEntity},
		{"playlist item with no media", http.MethodPost, "/api/windows/" + itoa(id) + "/playlist",
			map[string]any{}, http.StatusUnprocessableEntity},
		{"unknown window", http.MethodGet, "/api/windows/424242", nil, http.StatusNotFound},
		{"non numeric window id", http.MethodGet, "/api/windows/abc", nil, http.StatusBadRequest},
		{"unknown route", http.MethodGet, "/api/nope", nil, http.StatusNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			status, body := ts.do(t, c.method, c.path, c.body)
			if status != c.status {
				t.Fatalf("status = %d, want %d (body %v)", status, c.status, body)
			}
			errBody, ok := body["error"].(map[string]any)
			if !ok {
				t.Fatalf("expected an error envelope, got %v", body)
			}
			if errBody["message"] == "" {
				t.Error("error message is empty")
			}
		})
	}
}

func TestMalformedJSONIsRejectedCleanly(t *testing.T) {
	ts := newTestServer(t)
	resp, err := ts.Client().Post(ts.URL+"/api/media", "application/json", strings.NewReader("{not json"))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestUnknownFieldsAreRejected(t *testing.T) {
	ts := newTestServer(t)
	status, _ := ts.do(t, http.MethodPost, "/api/windows", map[string]any{"name": "x", "colour": "red"})
	if status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for an unknown field", status)
	}
}

func TestCORSHeaders(t *testing.T) {
	ts := newTestServer(t)

	t.Run("preflight from an allowed origin", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodOptions, ts.URL+"/api/windows", nil)
		req.Header.Set("Origin", "http://localhost:5173")
		req.Header.Set("Access-Control-Request-Method", "POST")
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatalf("preflight: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNoContent {
			t.Errorf("status = %d, want 204", resp.StatusCode)
		}
		if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
			t.Errorf("allow-origin = %q", got)
		}
		if !strings.Contains(resp.Header.Get("Access-Control-Allow-Methods"), "PATCH") {
			t.Errorf("allow-methods = %q", resp.Header.Get("Access-Control-Allow-Methods"))
		}
	})

	t.Run("disallowed origin gets no CORS header", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/windows", nil)
		req.Header.Set("Origin", "https://evil.test")
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		defer resp.Body.Close()
		if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("allow-origin = %q, want empty", got)
		}
	})
}

func TestTimeEndpoint(t *testing.T) {
	ts := newTestServer(t)
	status, body := ts.do(t, http.MethodGet, "/api/time", nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	data := body["data"].(map[string]any)
	millis := int64(data["serverTimeMillis"].(float64))
	if delta := time.Since(time.UnixMilli(millis)); delta > 5*time.Second || delta < -5*time.Second {
		t.Errorf("serverTimeMillis is %s away from now", delta)
	}
}

func TestBundledAssetsAreServedWithRangeSupport(t *testing.T) {
	ts := newTestServer(t)

	// The seeded demo videos are served by the backend itself, so the demo has
	// no third-party media dependency.
	resp, err := ts.Client().Get(ts.URL + "/api/assets/bunny-clip.mp4")
	if err != nil {
		t.Fatalf("get asset: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "video/") {
		t.Errorf("content-type = %q, want a video type", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	if len(body) < 1000 {
		t.Errorf("asset is only %d bytes", len(body))
	}

	// Range support is what lets a browser seek within the video.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/assets/bunny-clip.mp4", nil)
	req.Header.Set("Range", "bytes=0-99")
	ranged, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("range request: %v", err)
	}
	defer ranged.Body.Close()
	if ranged.StatusCode != http.StatusPartialContent {
		t.Errorf("range status = %d, want 206", ranged.StatusCode)
	}
	partial, _ := io.ReadAll(ranged.Body)
	if len(partial) != 100 {
		t.Errorf("range returned %d bytes, want 100", len(partial))
	}
}

func TestAssetPathTraversalIsRejected(t *testing.T) {
	ts := newTestServer(t)

	for _, name := range []string{"..%2f..%2fgo.mod", "nope.mp4", ".hidden"} {
		resp, err := ts.Client().Get(ts.URL + "/api/assets/" + name)
		if err != nil {
			t.Fatalf("get %s: %v", name, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s returned %d, want 400 or 404", name, resp.StatusCode)
		}
	}
}
