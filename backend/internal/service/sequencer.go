package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/syncstream/media-sequencer/internal/models"
	"github.com/syncstream/media-sequencer/internal/playback"
	ws "github.com/syncstream/media-sequencer/internal/websocket"
)

// Publisher pushes realtime events. The hub satisfies it; tests use a spy.
type Publisher interface {
	Publish(eventType string, payload any)
}

// Clock lets tests drive time deterministically.
type Clock func() time.Time

// Sequencer is the application service. It owns configuration state (windows,
// media, playlists) and the global sync override; it deliberately does not
// drive playback, which the clients compute from the cycle clock.
type Sequencer struct {
	store     Stores
	publisher Publisher
	logger    *slog.Logger
	now       Clock

	// syncLead is how far in the future a new sync override is scheduled, so
	// that every connected client has time to receive it and preload the media
	// before the shared start instant arrives.
	syncLead time.Duration

	// endTimer fires SYNC_ENDED when the live override finishes. Guarded by
	// syncMu because any request goroutine may replace it.
	syncMu    syncMutex
	endTimer  *time.Timer
	endSyncID int64
}

// New builds a Sequencer.
func New(store Stores, publisher Publisher, logger *slog.Logger, syncLead time.Duration) *Sequencer {
	return &Sequencer{
		store:     store,
		publisher: publisher,
		logger:    logger,
		now:       func() time.Time { return time.Now().UTC() },
		syncLead:  syncLead,
	}
}

// Snapshot is everything a client needs to start rendering: the full playback
// configuration, the live sync override and the server clock.
type Snapshot struct {
	ServerTime  time.Time                   `json:"serverTime"`
	CycleMillis int64                       `json:"cycleMillis"`
	Windows     []models.WindowWithPlaylist `json:"windows"`
	Media       []models.Media              `json:"media"`
	ActiveSync  *models.SyncEvent           `json:"activeSync"`
}

// Snapshot loads the bootstrap state in four queries: windows, all playlist
// items, the media library and the live sync override.
func (s *Sequencer) Snapshot(ctx context.Context) (Snapshot, error) {
	windows, err := s.WindowsWithPlaylists(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	media, err := s.store.Media.List(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	active, err := s.LiveSync(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{
		ServerTime:  s.now(),
		CycleMillis: playback.CycleMillis,
		Windows:     windows,
		Media:       media,
		ActiveSync:  active,
	}, nil
}

// WindowsWithPlaylists returns every window together with its playlist.
func (s *Sequencer) WindowsWithPlaylists(ctx context.Context) ([]models.WindowWithPlaylist, error) {
	windows, err := s.store.Windows.List(ctx)
	if err != nil {
		return nil, err
	}
	playlists, err := s.store.Playlist.ListAll(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]models.WindowWithPlaylist, 0, len(windows))
	for _, w := range windows {
		out = append(out, buildWindowWithPlaylist(w, playlists[w.ID]))
	}
	return out, nil
}

// WindowWithPlaylist returns one window and its playlist.
func (s *Sequencer) WindowWithPlaylist(ctx context.Context, windowID int64) (models.WindowWithPlaylist, error) {
	window, err := s.store.Windows.Get(ctx, windowID)
	if err != nil {
		return models.WindowWithPlaylist{}, err
	}
	items, err := s.store.Playlist.ListByWindow(ctx, windowID)
	if err != nil {
		return models.WindowWithPlaylist{}, err
	}
	return buildWindowWithPlaylist(window, items), nil
}

func buildWindowWithPlaylist(w models.Window, items []models.PlaylistItem) models.WindowWithPlaylist {
	if items == nil {
		items = []models.PlaylistItem{}
	}
	var total int64
	for _, it := range items {
		if it.Media.DurationSeconds > 0 {
			total += it.Media.DurationMillis()
		}
	}
	return models.WindowWithPlaylist{Window: w, Playlist: items, PlaylistDurationMillis: total}
}

// CreateWindow adds a display window and announces it.
func (s *Sequencer) CreateWindow(ctx context.Context, name string) (models.WindowWithPlaylist, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return models.WindowWithPlaylist{}, invalid("name", "name is required")
	}
	if len(name) > 120 {
		return models.WindowWithPlaylist{}, invalid("name", "name must be at most 120 characters")
	}

	window, err := s.store.Windows.Create(ctx, name, s.now())
	if err != nil {
		return models.WindowWithPlaylist{}, err
	}
	result := buildWindowWithPlaylist(window, nil)
	s.publisher.Publish(ws.EventWindowCreated, result)
	return result, nil
}

// ListMedia returns the media library.
func (s *Sequencer) ListMedia(ctx context.Context) ([]models.Media, error) {
	return s.store.Media.List(ctx)
}

// CreateMedia validates and stores a media item.
func (s *Sequencer) CreateMedia(ctx context.Context, in models.MediaInput) (models.Media, error) {
	in.Normalize()
	if errs := in.Validate(); len(errs) > 0 {
		return models.Media{}, &ValidationFailure{Errors: errs}
	}
	media, err := s.store.Media.Create(ctx, in)
	if err != nil {
		return models.Media{}, err
	}
	s.publisher.Publish(ws.EventMediaCreated, media)
	return media, nil
}

// Playlist returns one window's playlist.
func (s *Sequencer) Playlist(ctx context.Context, windowID int64) ([]models.PlaylistItem, error) {
	if _, err := s.store.Windows.Get(ctx, windowID); err != nil {
		return nil, err
	}
	return s.store.Playlist.ListByWindow(ctx, windowID)
}

// AddPlaylistItemInput either references existing media by id, or supplies a
// new media definition inline. Inline creation is what the "Add Media" form in
// the UI uses, so an operator adds a window's next item in one request.
type AddPlaylistItemInput struct {
	MediaID *int64             `json:"mediaId"`
	Media   *models.MediaInput `json:"media"`
}

// AddPlaylistItem appends media to a window's playlist and broadcasts the new
// playlist so every client picks the change up without a reload.
func (s *Sequencer) AddPlaylistItem(ctx context.Context, windowID int64, in AddPlaylistItemInput) (models.WindowWithPlaylist, error) {
	if _, err := s.store.Windows.Get(ctx, windowID); err != nil {
		return models.WindowWithPlaylist{}, err
	}

	mediaID, err := s.resolveMedia(ctx, in)
	if err != nil {
		return models.WindowWithPlaylist{}, err
	}
	if _, err := s.store.Playlist.Append(ctx, windowID, mediaID); err != nil {
		return models.WindowWithPlaylist{}, err
	}
	return s.publishPlaylist(ctx, windowID)
}

// resolveMedia turns either half of AddPlaylistItemInput into a media id.
func (s *Sequencer) resolveMedia(ctx context.Context, in AddPlaylistItemInput) (int64, error) {
	switch {
	case in.MediaID != nil && in.Media != nil:
		return 0, invalid("media", "provide either mediaId or an inline media object, not both")

	case in.MediaID != nil:
		media, err := s.store.Media.Get(ctx, *in.MediaID)
		if err != nil {
			if errors.Is(err, models.ErrNotFound) {
				return 0, invalid("mediaId", fmt.Sprintf("media %d does not exist", *in.MediaID))
			}
			return 0, err
		}
		return media.ID, nil

	case in.Media != nil:
		media, err := s.CreateMedia(ctx, *in.Media)
		if err != nil {
			return 0, err
		}
		return media.ID, nil

	default:
		return 0, invalid("media", "mediaId or an inline media object is required")
	}
}

// RemovePlaylistItem deletes an item and rebroadcasts the playlist.
func (s *Sequencer) RemovePlaylistItem(ctx context.Context, windowID, itemID int64) (models.WindowWithPlaylist, error) {
	if err := s.store.Playlist.Remove(ctx, windowID, itemID); err != nil {
		return models.WindowWithPlaylist{}, err
	}
	return s.publishPlaylist(ctx, windowID)
}

// MovePlaylistItem reorders an item and rebroadcasts the playlist.
func (s *Sequencer) MovePlaylistItem(ctx context.Context, windowID, itemID int64, position int) (models.WindowWithPlaylist, error) {
	if position < 0 {
		return models.WindowWithPlaylist{}, invalid("position", "position must be zero or greater")
	}
	if _, err := s.store.Playlist.Move(ctx, windowID, itemID, position); err != nil {
		return models.WindowWithPlaylist{}, err
	}
	return s.publishPlaylist(ctx, windowID)
}

// publishPlaylist reloads a window and announces the change.
func (s *Sequencer) publishPlaylist(ctx context.Context, windowID int64) (models.WindowWithPlaylist, error) {
	window, err := s.WindowWithPlaylist(ctx, windowID)
	if err != nil {
		return models.WindowWithPlaylist{}, err
	}
	s.publisher.Publish(ws.EventPlaylistUpdated, window)
	return window, nil
}

// CurrentPlayback resolves what a window should be showing right now using the
// server's own copy of the playback clock. The frontend does not depend on
// this endpoint (it computes playback locally) but it makes the 5-hour cycle
// observable and verifiable with a single curl.
type CurrentPlayback struct {
	WindowID    int64           `json:"windowId"`
	ServerTime  time.Time       `json:"serverTime"`
	CycleAnchor time.Time       `json:"cycleAnchor"`
	CycleMillis int64           `json:"cycleMillis"`
	State       *playback.State `json:"state"`
	Media       *models.Media   `json:"media"`
	// SyncOverride is set when a global sync is currently displacing the
	// window's own item. The underlying playlist state above keeps advancing.
	SyncOverride *models.SyncEvent `json:"syncOverride"`
}

// CurrentPlayback computes the reference playback state for a window.
func (s *Sequencer) CurrentPlayback(ctx context.Context, windowID int64) (CurrentPlayback, error) {
	window, err := s.WindowWithPlaylist(ctx, windowID)
	if err != nil {
		return CurrentPlayback{}, err
	}
	now := s.now()

	byID := make(map[int64]models.Media, len(window.Playlist))
	items := make([]playback.Item, 0, len(window.Playlist))
	for _, it := range window.Playlist {
		byID[it.Media.ID] = it.Media
		items = append(items, playback.Item{
			PlaylistItemID: it.ID,
			MediaID:        it.Media.ID,
			DurationMillis: it.Media.DurationMillis(),
		})
	}

	result := CurrentPlayback{
		WindowID:    windowID,
		ServerTime:  now,
		CycleAnchor: window.CycleAnchor,
		CycleMillis: playback.CycleMillis,
	}
	if state, ok := playback.Resolve(
		playback.NewTimeline(items), window.CycleAnchor.UnixMilli(), now.UnixMilli(),
	); ok {
		media := byID[state.Item.MediaID]
		result.State = &state
		result.Media = &media
	}

	active, err := s.LiveSync(ctx)
	if err != nil {
		return CurrentPlayback{}, err
	}
	if active != nil && !now.Before(active.StartAt) && now.Before(active.EndAt) {
		result.SyncOverride = active
	}
	return result, nil
}
