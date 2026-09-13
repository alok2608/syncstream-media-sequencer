package repository_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/syncstream/media-sequencer/internal/database"
	"github.com/syncstream/media-sequencer/internal/models"
	"github.com/syncstream/media-sequencer/internal/repository"
)

// These tests exercise the real SQL - position bookkeeping, the deferred unique
// constraint used by reordering, and the single-live-sync invariant - none of
// which an in-memory fake can prove.
//
// They run only when TEST_DATABASE_URL points at a disposable database:
//
//	createdb sequencer_test
//	TEST_DATABASE_URL=postgres://localhost:5432/sequencer_test?sslmode=disable go test ./internal/repository/

func newStore(t *testing.T) (*repository.Store, *pgxpool.Pool) {
	t.Helper()

	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run repository integration tests")
	}

	ctx := context.Background()
	pool, err := database.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Each test starts from a clean slate.
	for _, stmt := range []string{
		`DELETE FROM sync_events`, `DELETE FROM playlist_items`,
		`DELETE FROM windows`, `DELETE FROM media`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("reset %q: %v", stmt, err)
		}
	}
	return repository.New(pool), pool
}

func makeMedia(t *testing.T, store *repository.Store, name string, seconds int) models.Media {
	t.Helper()
	media, err := store.Media.Create(context.Background(), models.MediaInput{
		Name: name, Type: models.MediaTypeImage,
		URL: "https://example.test/" + name + ".png", DurationSeconds: seconds,
	})
	if err != nil {
		t.Fatalf("create media %s: %v", name, err)
	}
	return media
}

func positions(items []models.PlaylistItem) []int {
	out := make([]int, len(items))
	for i, it := range items {
		out[i] = it.Position
	}
	return out
}

func names(items []models.PlaylistItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Media.Name
	}
	return out
}

func TestPlaylistPositionsStayContiguous(t *testing.T) {
	store, _ := newStore(t)
	ctx := context.Background()

	window, err := store.Windows.Create(ctx, "Window 1", time.Now().UTC())
	if err != nil {
		t.Fatalf("create window: %v", err)
	}

	var ids []int64
	for _, name := range []string{"M1", "M2", "M3", "M4"} {
		media := makeMedia(t, store, name, 10)
		item, err := store.Playlist.Append(ctx, window.ID, media.ID)
		if err != nil {
			t.Fatalf("append %s: %v", name, err)
		}
		ids = append(ids, item.ID)
	}

	items, err := store.Playlist.ListByWindow(ctx, window.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if got := positions(items); !equalInts(got, []int{0, 1, 2, 3}) {
		t.Fatalf("positions after append = %v", got)
	}

	// Remove the middle item: the tail must close up.
	if err := store.Playlist.Remove(ctx, window.ID, ids[1]); err != nil {
		t.Fatalf("remove: %v", err)
	}
	items, _ = store.Playlist.ListByWindow(ctx, window.ID)
	if got := positions(items); !equalInts(got, []int{0, 1, 2}) {
		t.Fatalf("positions after remove = %v", got)
	}
	if got := names(items); !equalStrings(got, []string{"M1", "M3", "M4"}) {
		t.Fatalf("order after remove = %v", got)
	}

	// Appending again must land at the end, not reuse the vacated slot.
	media := makeMedia(t, store, "M5", 10)
	if _, err := store.Playlist.Append(ctx, window.ID, media.ID); err != nil {
		t.Fatalf("append after remove: %v", err)
	}
	items, _ = store.Playlist.ListByWindow(ctx, window.ID)
	if got := positions(items); !equalInts(got, []int{0, 1, 2, 3}) {
		t.Fatalf("positions after re-append = %v", got)
	}
}

// TestReorderUsesTheDeferredUniqueConstraint moves items in both directions;
// each move transiently collides on (window_id, position) and only succeeds
// because the constraint is DEFERRABLE INITIALLY DEFERRED.
func TestReorderUsesTheDeferredUniqueConstraint(t *testing.T) {
	store, _ := newStore(t)
	ctx := context.Background()

	window, _ := store.Windows.Create(ctx, "Window 1", time.Now().UTC())
	ids := map[string]int64{}
	for _, name := range []string{"M1", "M2", "M3", "M4"} {
		media := makeMedia(t, store, name, 10)
		item, err := store.Playlist.Append(ctx, window.ID, media.ID)
		if err != nil {
			t.Fatalf("append: %v", err)
		}
		ids[name] = item.ID
	}

	cases := []struct {
		item   string
		to     int
		expect []string
	}{
		{"M4", 0, []string{"M4", "M1", "M2", "M3"}},  // last to first
		{"M4", 3, []string{"M1", "M2", "M3", "M4"}},  // back to the end
		{"M2", 2, []string{"M1", "M3", "M2", "M4"}},  // forwards by one
		{"M2", 0, []string{"M2", "M1", "M3", "M4"}},  // backwards to the front
		{"M2", 99, []string{"M1", "M3", "M4", "M2"}}, // clamped to the end
	}
	for _, c := range cases {
		if _, err := store.Playlist.Move(ctx, window.ID, ids[c.item], c.to); err != nil {
			t.Fatalf("move %s to %d: %v", c.item, c.to, err)
		}
		items, _ := store.Playlist.ListByWindow(ctx, window.ID)
		if got := names(items); !equalStrings(got, c.expect) {
			t.Fatalf("after moving %s to %d: %v, want %v", c.item, c.to, got, c.expect)
		}
		if got := positions(items); !equalInts(got, []int{0, 1, 2, 3}) {
			t.Fatalf("positions became %v", got)
		}
	}
}

func TestPlaylistOperationsAreScopedToTheirWindow(t *testing.T) {
	store, _ := newStore(t)
	ctx := context.Background()

	w1, _ := store.Windows.Create(ctx, "Window 1", time.Now().UTC())
	w2, _ := store.Windows.Create(ctx, "Window 2", time.Now().UTC())
	media := makeMedia(t, store, "M1", 10)

	item, err := store.Playlist.Append(ctx, w1.ID, media.ID)
	if err != nil {
		t.Fatalf("append: %v", err)
	}

	// Deleting w1's item through w2 must not work.
	if err := store.Playlist.Remove(ctx, w2.ID, item.ID); !errors.Is(err, models.ErrNotFound) {
		t.Errorf("cross-window delete returned %v, want not found", err)
	}
	if _, err := store.Playlist.Move(ctx, w2.ID, item.ID, 0); !errors.Is(err, models.ErrNotFound) {
		t.Errorf("cross-window move returned %v, want not found", err)
	}
	items, _ := store.Playlist.ListByWindow(ctx, w1.ID)
	if len(items) != 1 {
		t.Errorf("window 1 lost its item")
	}
}

func TestMissingWindowIsReportedAsNotFound(t *testing.T) {
	store, _ := newStore(t)
	ctx := context.Background()

	if _, err := store.Windows.Get(ctx, 999999); !errors.Is(err, models.ErrNotFound) {
		t.Errorf("get = %v, want not found", err)
	}
	if _, err := store.Playlist.Append(ctx, 999999, 1); !errors.Is(err, models.ErrNotFound) {
		t.Errorf("append = %v, want not found", err)
	}
}

// TestOnlyOneSyncIsEverLive proves the supersede rule at the SQL level.
func TestOnlyOneSyncIsEverLive(t *testing.T) {
	store, _ := newStore(t)
	ctx := context.Background()

	first := makeMedia(t, store, "M1", 10)
	second := makeMedia(t, store, "M2", 20)
	now := time.Now().UTC()

	original, err := store.Sync.Create(ctx, first.ID, now, now.Add(time.Minute), now)
	if err != nil {
		t.Fatalf("create sync: %v", err)
	}

	live, err := store.Sync.Live(ctx, now.Add(time.Second))
	if err != nil {
		t.Fatalf("live: %v", err)
	}
	if live.ID != original.ID {
		t.Fatalf("live = %d, want %d", live.ID, original.ID)
	}

	// A second sync while the first is running supersedes it.
	replacement, err := store.Sync.Create(ctx, second.ID, now.Add(2*time.Second), now.Add(12*time.Second), now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("create replacement: %v", err)
	}

	live, err = store.Sync.Live(ctx, now.Add(3*time.Second))
	if err != nil {
		t.Fatalf("live after replace: %v", err)
	}
	if live.ID != replacement.ID {
		t.Errorf("live = %d, want the replacement %d", live.ID, replacement.ID)
	}

	// After the replacement ends nothing is live, even though the original's
	// own end time has not passed yet.
	if _, err := store.Sync.Live(ctx, now.Add(13*time.Second)); !errors.Is(err, models.ErrNotFound) {
		t.Errorf("live after the replacement ended = %v, want not found", err)
	}
}

func TestCancelLiveSync(t *testing.T) {
	store, _ := newStore(t)
	ctx := context.Background()

	media := makeMedia(t, store, "M1", 10)
	now := time.Now().UTC()
	if _, err := store.Sync.Create(ctx, media.ID, now, now.Add(time.Minute), now); err != nil {
		t.Fatalf("create: %v", err)
	}

	cancelled, err := store.Sync.CancelLive(ctx, now.Add(time.Second))
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if cancelled.CancelledAt == nil {
		t.Error("cancelled_at was not recorded")
	}
	if _, err := store.Sync.Live(ctx, now.Add(2*time.Second)); !errors.Is(err, models.ErrNotFound) {
		t.Errorf("sync survived cancellation: %v", err)
	}
	if _, err := store.Sync.CancelLive(ctx, now.Add(3*time.Second)); !errors.Is(err, models.ErrNotFound) {
		t.Errorf("cancelling twice = %v, want not found", err)
	}
}

// TestConfigurationSurvivesAReconnect stands in for an application restart:
// a brand new pool must read back exactly what was written.
func TestConfigurationSurvivesAReconnect(t *testing.T) {
	store, _ := newStore(t)
	ctx := context.Background()

	anchor := time.Now().UTC().Truncate(time.Millisecond)
	window, err := store.Windows.Create(ctx, "Persistent Window", anchor)
	if err != nil {
		t.Fatalf("create window: %v", err)
	}
	media := makeMedia(t, store, "M1", 42)
	if _, err := store.Playlist.Append(ctx, window.ID, media.ID); err != nil {
		t.Fatalf("append: %v", err)
	}

	reconnected, err := database.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	defer reconnected.Close()
	fresh := repository.New(reconnected)

	reloaded, err := fresh.Windows.Get(ctx, window.ID)
	if err != nil {
		t.Fatalf("reload window: %v", err)
	}
	if reloaded.Name != "Persistent Window" {
		t.Errorf("name = %q", reloaded.Name)
	}
	// The cycle anchor must survive exactly, or playback would jump on restart.
	if !reloaded.CycleAnchor.UTC().Equal(anchor) {
		t.Errorf("cycle anchor = %s, want %s", reloaded.CycleAnchor.UTC(), anchor)
	}
	items, err := fresh.Playlist.ListByWindow(ctx, window.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("playlist did not persist: %v (%d items)", err, len(items))
	}
	if items[0].Media.DurationSeconds != 42 {
		t.Errorf("duration = %d, want 42", items[0].Media.DurationSeconds)
	}
}

// TestSchemaRejectsInvalidMedia checks the database-level guards, which are the
// last line of defence if a future caller skips service validation.
func TestSchemaRejectsInvalidMedia(t *testing.T) {
	store, pool := newStore(t)
	_ = store
	ctx := context.Background()

	cases := []struct {
		name string
		sql  string
		args []any
	}{
		{"unknown type", `INSERT INTO media (name, type, url, duration_seconds) VALUES ('x', 'pdf', 'https://a.test/x', 5)`, nil},
		{"image without url", `INSERT INTO media (name, type, url, duration_seconds) VALUES ('x', 'image', '', 5)`, nil},
		{"blank with url", `INSERT INTO media (name, type, url, duration_seconds) VALUES ('x', 'blank', 'https://a.test/x', 5)`, nil},
		{"zero duration", `INSERT INTO media (name, type, url, duration_seconds) VALUES ('x', 'blank', '', 0)`, nil},
		{"blank name", `INSERT INTO media (name, type, url, duration_seconds) VALUES ('  ', 'blank', '', 5)`, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, c.sql, c.args...); err == nil {
				t.Error("the database accepted an invalid row")
			}
		})
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
