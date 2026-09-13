package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/syncstream/media-sequencer/internal/models"
	"github.com/syncstream/media-sequencer/internal/playback"
	"github.com/syncstream/media-sequencer/internal/testsupport"
	ws "github.com/syncstream/media-sequencer/internal/websocket"
)

// fixedTime anchors every service test so results never depend on wall clock.
var fixedTime = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

type harness struct {
	svc    *Sequencer
	mem    *testsupport.MemoryStores
	events *testsupport.Recorder
	// now is what the service sees as "now"; tests advance it directly.
	now time.Time
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	mem := testsupport.NewMemoryStores()
	events := &testsupport.Recorder{}

	h := &harness{mem: mem, events: events, now: fixedTime}
	stores := Stores{Windows: mem, Media: mem.MediaStore(), Playlist: mem, Sync: mem.SyncStore()}
	h.svc = New(stores, events, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second)
	h.svc.now = func() time.Time { return h.now }
	t.Cleanup(h.svc.Shutdown)
	return h
}

// seedWindow creates a window whose cycle anchor is the harness's start time,
// plus one media item per duration given, appended in order.
func (h *harness) seedWindow(t *testing.T, name string, durations ...int) models.WindowWithPlaylist {
	t.Helper()
	ctx := context.Background()
	window, err := h.svc.CreateWindow(ctx, name)
	if err != nil {
		t.Fatalf("create window: %v", err)
	}
	for i, d := range durations {
		media := models.MediaInput{
			Name:            name + " item",
			Type:            models.MediaTypeImage,
			URL:             "https://example.test/image.png",
			DurationSeconds: d,
		}
		if _, err := h.svc.AddPlaylistItem(ctx, window.ID, AddPlaylistItemInput{Media: &media}); err != nil {
			t.Fatalf("add item %d: %v", i, err)
		}
	}
	updated, err := h.svc.WindowWithPlaylist(ctx, window.ID)
	if err != nil {
		t.Fatalf("reload window: %v", err)
	}
	return updated
}

func TestCreateWindowValidation(t *testing.T) {
	h := newHarness(t)
	for _, name := range []string{"", "   "} {
		if _, err := h.svc.CreateWindow(context.Background(), name); err == nil {
			t.Errorf("expected a validation error for name %q", name)
		}
	}
}

func TestCreateMediaValidation(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	cases := []struct {
		name     string
		in       models.MediaInput
		badField string
	}{
		{"missing name", models.MediaInput{Type: models.MediaTypeImage, URL: "https://x.test/a.png", DurationSeconds: 5}, "name"},
		{"unknown type", models.MediaInput{Name: "x", Type: "gif", URL: "https://x.test/a.gif", DurationSeconds: 5}, "type"},
		{"image without url", models.MediaInput{Name: "x", Type: models.MediaTypeImage, DurationSeconds: 5}, "url"},
		{"video with bad url", models.MediaInput{Name: "x", Type: models.MediaTypeVideo, URL: "not a url", DurationSeconds: 5}, "url"},
		{"zero duration", models.MediaInput{Name: "x", Type: models.MediaTypeBlank, DurationSeconds: 0}, "durationSeconds"},
		{"duration too long", models.MediaInput{Name: "x", Type: models.MediaTypeBlank, DurationSeconds: 99999}, "durationSeconds"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := h.svc.CreateMedia(ctx, c.in)
			var failure *ValidationFailure
			if !errors.As(err, &failure) {
				t.Fatalf("expected a validation failure, got %v", err)
			}
			for _, fe := range failure.Errors {
				if fe.Field == c.badField {
					return
				}
			}
			t.Fatalf("expected a failure on field %q, got %v", c.badField, failure.Errors)
		})
	}
}

func TestBlankMediaNeedsNoURL(t *testing.T) {
	h := newHarness(t)
	media, err := h.svc.CreateMedia(context.Background(), models.MediaInput{
		Name: "Intermission", Type: models.MediaTypeBlank, URL: "https://ignored.test/x.png", DurationSeconds: 5,
	})
	if err != nil {
		t.Fatalf("create blank media: %v", err)
	}
	if media.URL != "" {
		t.Errorf("blank media should carry no url, got %q", media.URL)
	}
}

func TestAddPlaylistItemBroadcastsAndAppends(t *testing.T) {
	h := newHarness(t)
	window := h.seedWindow(t, "Window 1", 10, 20, 30)

	if len(window.Playlist) != 3 {
		t.Fatalf("expected 3 items, got %d", len(window.Playlist))
	}
	for i, item := range window.Playlist {
		if item.Position != i {
			t.Errorf("item %d has position %d", i, item.Position)
		}
	}
	if window.PlaylistDurationMillis != 60_000 {
		t.Errorf("playlist duration = %dms, want 60000", window.PlaylistDurationMillis)
	}
	if h.events.CountOf(ws.EventPlaylistUpdated) != 3 {
		t.Errorf("expected 3 PLAYLIST_UPDATED events, got %v", h.events.TypesPublished())
	}
}

func TestAddPlaylistItemRejectsUnknownWindowAndMedia(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.seedWindow(t, "Window 1", 10)

	missing := int64(999)
	if _, err := h.svc.AddPlaylistItem(ctx, missing, AddPlaylistItemInput{MediaID: &missing}); !errors.Is(err, models.ErrNotFound) {
		t.Errorf("expected not found for an unknown window, got %v", err)
	}

	windows, _ := h.svc.WindowsWithPlaylists(ctx)
	_, err := h.svc.AddPlaylistItem(ctx, windows[0].ID, AddPlaylistItemInput{MediaID: &missing})
	var failure *ValidationFailure
	if !errors.As(err, &failure) {
		t.Errorf("expected a validation failure for unknown media, got %v", err)
	}
}

func TestAddPlaylistItemRequiresExactlyOneMediaSource(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	window := h.seedWindow(t, "Window 1", 10)
	mediaID := window.Playlist[0].Media.ID
	inline := models.MediaInput{Name: "x", Type: models.MediaTypeBlank, DurationSeconds: 4}

	if _, err := h.svc.AddPlaylistItem(ctx, window.ID, AddPlaylistItemInput{}); err == nil {
		t.Error("expected an error when neither mediaId nor media is supplied")
	}
	if _, err := h.svc.AddPlaylistItem(ctx, window.ID, AddPlaylistItemInput{MediaID: &mediaID, Media: &inline}); err == nil {
		t.Error("expected an error when both mediaId and media are supplied")
	}
}

func TestRemoveAndReorderKeepPositionsContiguous(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	window := h.seedWindow(t, "Window 1", 10, 20, 30)
	first, second, third := window.Playlist[0], window.Playlist[1], window.Playlist[2]

	moved, err := h.svc.MovePlaylistItem(ctx, window.ID, third.ID, 0)
	if err != nil {
		t.Fatalf("move: %v", err)
	}
	gotOrder := []int64{moved.Playlist[0].ID, moved.Playlist[1].ID, moved.Playlist[2].ID}
	wantOrder := []int64{third.ID, first.ID, second.ID}
	for i := range wantOrder {
		if gotOrder[i] != wantOrder[i] {
			t.Fatalf("order after move = %v, want %v", gotOrder, wantOrder)
		}
	}

	after, err := h.svc.RemovePlaylistItem(ctx, window.ID, first.ID)
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(after.Playlist) != 2 {
		t.Fatalf("expected 2 items after removal, got %d", len(after.Playlist))
	}
	for i, item := range after.Playlist {
		if item.Position != i {
			t.Errorf("position %d is %d after removal", i, item.Position)
		}
	}
}

func TestCurrentPlaybackFollowsTheCycleClock(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	window := h.seedWindow(t, "Window 1", 10, 20, 30)

	cases := []struct {
		offset time.Duration
		want   int // expected playlist index
	}{
		{0, 0},
		{9 * time.Second, 0},
		{10 * time.Second, 1},
		{35 * time.Second, 2},
		{60 * time.Second, 0},  // loops with no gap
		{135 * time.Second, 1}, // still looping much later (135s = 2 passes + 15s)
	}
	for _, c := range cases {
		h.now = fixedTime.Add(c.offset)
		current, err := h.svc.CurrentPlayback(ctx, window.ID)
		if err != nil {
			t.Fatalf("current playback: %v", err)
		}
		if current.State == nil {
			t.Fatalf("no state at +%s", c.offset)
		}
		if current.State.Index != c.want {
			t.Errorf("at +%s index = %d, want %d", c.offset, current.State.Index, c.want)
		}
		if current.Media == nil {
			t.Errorf("at +%s media was not resolved", c.offset)
		}
	}
}

func TestCurrentPlaybackAtTheFiveHourBoundary(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	window := h.seedWindow(t, "Window 1", 10, 20, 30)

	// One millisecond before the boundary: still looping real media.
	h.now = fixedTime.Add(time.Duration(playback.CycleMillis-1) * time.Millisecond)
	before, err := h.svc.CurrentPlayback(ctx, window.ID)
	if err != nil {
		t.Fatalf("current playback: %v", err)
	}
	if before.State == nil {
		t.Fatal("playback went blank at the end of the cycle")
	}
	if before.State.CycleIndex != 0 || before.State.LoopIteration != 299 {
		t.Errorf("before boundary: cycle %d loop %d", before.State.CycleIndex, before.State.LoopIteration)
	}

	// At the boundary: a new cycle starts cleanly at playlist position 0.
	h.now = fixedTime.Add(time.Duration(playback.CycleMillis) * time.Millisecond)
	after, err := h.svc.CurrentPlayback(ctx, window.ID)
	if err != nil {
		t.Fatalf("current playback: %v", err)
	}
	if after.State.CycleIndex != 1 || after.State.Index != 0 || after.State.ElapsedInItemMillis != 0 {
		t.Errorf("after boundary: %+v", after.State)
	}
}

func TestCurrentPlaybackWithEmptyPlaylist(t *testing.T) {
	h := newHarness(t)
	window := h.seedWindow(t, "Empty Window")

	current, err := h.svc.CurrentPlayback(context.Background(), window.ID)
	if err != nil {
		t.Fatalf("current playback: %v", err)
	}
	if current.State != nil || current.Media != nil {
		t.Error("an empty playlist must resolve to no state, so the window renders its fallback")
	}
}

func TestSnapshotCarriesEverythingAClientNeeds(t *testing.T) {
	h := newHarness(t)
	h.seedWindow(t, "Window 1", 10, 20)
	h.seedWindow(t, "Window 2", 15)

	snapshot, err := h.svc.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(snapshot.Windows) != 2 {
		t.Errorf("expected 2 windows, got %d", len(snapshot.Windows))
	}
	if len(snapshot.Media) != 3 {
		t.Errorf("expected 3 media items, got %d", len(snapshot.Media))
	}
	if snapshot.CycleMillis != playback.CycleMillis {
		t.Errorf("cycleMillis = %d", snapshot.CycleMillis)
	}
	if snapshot.ActiveSync != nil {
		t.Error("no sync should be active")
	}
	if !snapshot.ServerTime.Equal(fixedTime) {
		t.Errorf("serverTime = %s, want %s", snapshot.ServerTime, fixedTime)
	}
}
