// Package testsupport provides in-memory implementations of the service's
// storage interfaces and a recording publisher, shared by the service and
// handler test suites so neither has to duplicate a fake.
package testsupport

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/syncstream/media-sequencer/internal/models"
)

// MemoryStores is an in-memory implementation of the service storage
// interfaces. It mirrors the invariants the SQL schema enforces - contiguous
// playlist positions and a single live sync override - so service tests
// exercise realistic behaviour without a database.
type MemoryStores struct {
	mu sync.Mutex

	windows  []models.Window
	media    []models.Media
	playlist []models.PlaylistItem
	syncs    []models.SyncEvent
	nextID   int64
}

func NewMemoryStores() *MemoryStores { return &MemoryStores{nextID: 1} }

func (m *MemoryStores) id() int64 {
	id := m.nextID
	m.nextID++
	return id
}

// ---- WindowStore ----

func (m *MemoryStores) List(ctx context.Context) ([]models.Window, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]models.Window(nil), m.windows...), nil
}

func (m *MemoryStores) Get(ctx context.Context, id int64) (models.Window, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, w := range m.windows {
		if w.ID == id {
			return w, nil
		}
	}
	return models.Window{}, models.ErrNotFound
}

func (m *MemoryStores) Create(ctx context.Context, name string, anchor time.Time) (models.Window, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w := models.Window{ID: m.id(), Name: name, CycleAnchor: anchor, CreatedAt: anchor, UpdatedAt: anchor}
	m.windows = append(m.windows, w)
	return w, nil
}

// ---- MediaStore ----

func (m *MemoryStores) mediaList(ctx context.Context) ([]models.Media, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]models.Media(nil), m.media...), nil
}

func (m *MemoryStores) mediaGet(ctx context.Context, id int64) (models.Media, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, md := range m.media {
		if md.ID == id {
			return md, nil
		}
	}
	return models.Media{}, models.ErrNotFound
}

func (m *MemoryStores) mediaCreate(ctx context.Context, in models.MediaInput) (models.Media, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	md := models.Media{
		ID: m.id(), Name: in.Name, Type: in.Type, URL: in.URL,
		DurationSeconds: in.DurationSeconds, CreatedAt: time.Now().UTC(),
	}
	m.media = append(m.media, md)
	return md, nil
}

// ---- PlaylistStore ----

func (m *MemoryStores) ListByWindow(ctx context.Context, windowID int64) ([]models.PlaylistItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.itemsFor(windowID), nil
}

func (m *MemoryStores) ListAll(ctx context.Context) (map[int64][]models.PlaylistItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[int64][]models.PlaylistItem)
	for _, w := range m.windows {
		if items := m.itemsFor(w.ID); len(items) > 0 {
			out[w.ID] = items
		}
	}
	return out, nil
}

// itemsFor returns a window's items in position order. Caller holds the lock.
func (m *MemoryStores) itemsFor(windowID int64) []models.PlaylistItem {
	var items []models.PlaylistItem
	for _, it := range m.playlist {
		if it.WindowID == windowID {
			items = append(items, it)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Position < items[j].Position })
	return items
}

func (m *MemoryStores) Append(ctx context.Context, windowID, mediaID int64) (models.PlaylistItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var media models.Media
	for _, md := range m.media {
		if md.ID == mediaID {
			media = md
		}
	}
	item := models.PlaylistItem{
		ID: m.id(), WindowID: windowID, Position: len(m.itemsFor(windowID)),
		CreatedAt: time.Now().UTC(), Media: media,
	}
	m.playlist = append(m.playlist, item)
	return item, nil
}

func (m *MemoryStores) Remove(ctx context.Context, windowID, itemID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, it := range m.playlist {
		if it.ID == itemID && it.WindowID == windowID {
			m.playlist = append(m.playlist[:i], m.playlist[i+1:]...)
			m.renumber(windowID)
			return nil
		}
	}
	return models.ErrNotFound
}

func (m *MemoryStores) Move(ctx context.Context, windowID, itemID int64, position int) (models.PlaylistItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ordered := m.itemsFor(windowID)
	from := -1
	for i, it := range ordered {
		if it.ID == itemID {
			from = i
		}
	}
	if from == -1 {
		return models.PlaylistItem{}, models.ErrNotFound
	}
	if position >= len(ordered) {
		position = len(ordered) - 1
	}
	if position < 0 {
		position = 0
	}

	moved := ordered[from]
	ordered = append(ordered[:from], ordered[from+1:]...)
	ordered = append(ordered[:position], append([]models.PlaylistItem{moved}, ordered[position:]...)...)
	for i := range ordered {
		ordered[i].Position = i
		m.replace(ordered[i])
	}
	return ordered[position], nil
}

// renumber restores contiguous positions. Caller holds the lock.
func (m *MemoryStores) renumber(windowID int64) {
	for i, it := range m.itemsFor(windowID) {
		it.Position = i
		m.replace(it)
	}
}

func (m *MemoryStores) replace(item models.PlaylistItem) {
	for i := range m.playlist {
		if m.playlist[i].ID == item.ID {
			m.playlist[i] = item
			return
		}
	}
}

// ---- SyncStore ----

func (m *MemoryStores) syncCreate(ctx context.Context, mediaID int64, startAt, endAt, now time.Time) (models.SyncEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// A new override supersedes anything still live, as the SQL does.
	for i := range m.syncs {
		if m.syncs[i].CancelledAt == nil && m.syncs[i].EndAt.After(now) {
			cancelled := now
			m.syncs[i].CancelledAt = &cancelled
		}
	}
	var media models.Media
	for _, md := range m.media {
		if md.ID == mediaID {
			media = md
		}
	}
	event := models.SyncEvent{ID: m.id(), Media: media, StartAt: startAt, EndAt: endAt, CreatedAt: now}
	m.syncs = append(m.syncs, event)
	return event, nil
}

func (m *MemoryStores) syncLive(ctx context.Context, now time.Time) (models.SyncEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := len(m.syncs) - 1; i >= 0; i-- {
		e := m.syncs[i]
		if e.CancelledAt == nil && e.EndAt.After(now) {
			return e, nil
		}
	}
	return models.SyncEvent{}, models.ErrNotFound
}

func (m *MemoryStores) syncCancelLive(ctx context.Context, now time.Time) (models.SyncEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := len(m.syncs) - 1; i >= 0; i-- {
		if m.syncs[i].CancelledAt == nil && m.syncs[i].EndAt.After(now) {
			cancelled := now
			m.syncs[i].CancelledAt = &cancelled
			return m.syncs[i], nil
		}
	}
	return models.SyncEvent{}, models.ErrNotFound
}

// MediaAdapter and SyncAdapter resolve the method-name collisions that arise
// from one struct implementing four interfaces.
type MediaAdapter struct{ *MemoryStores }

func (a MediaAdapter) List(ctx context.Context) ([]models.Media, error) { return a.mediaList(ctx) }
func (a MediaAdapter) Get(ctx context.Context, id int64) (models.Media, error) {
	return a.mediaGet(ctx, id)
}
func (a MediaAdapter) Create(ctx context.Context, in models.MediaInput) (models.Media, error) {
	return a.mediaCreate(ctx, in)
}

type SyncAdapter struct{ *MemoryStores }

func (a SyncAdapter) Create(ctx context.Context, mediaID int64, startAt, endAt, now time.Time) (models.SyncEvent, error) {
	return a.syncCreate(ctx, mediaID, startAt, endAt, now)
}
func (a SyncAdapter) Live(ctx context.Context, now time.Time) (models.SyncEvent, error) {
	return a.syncLive(ctx, now)
}
func (a SyncAdapter) CancelLive(ctx context.Context, now time.Time) (models.SyncEvent, error) {
	return a.syncCancelLive(ctx, now)
}

// Recorder captures published events so tests can assert on broadcasts.
type Recorder struct {
	mu     sync.Mutex
	events []RecordedEvent
}

type RecordedEvent struct {
	Type    string
	Payload any
}

func (r *Recorder) Publish(eventType string, payload any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, RecordedEvent{Type: eventType, Payload: payload})
}

func (r *Recorder) TypesPublished() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.events))
	for _, e := range r.events {
		out = append(out, e.Type)
	}
	return out
}

func (r *Recorder) CountOf(eventType string) int {
	n := 0
	for _, t := range r.TypesPublished() {
		if t == eventType {
			n++
		}
	}
	return n
}

// MediaStore returns the media-shaped view of the fake.
func (m *MemoryStores) MediaStore() MediaAdapter { return MediaAdapter{m} }

// SyncStore returns the sync-shaped view of the fake.
func (m *MemoryStores) SyncStore() SyncAdapter { return SyncAdapter{m} }
