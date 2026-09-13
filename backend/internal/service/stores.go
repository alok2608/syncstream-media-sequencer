package service

import (
	"context"
	"time"

	"github.com/syncstream/media-sequencer/internal/models"
	"github.com/syncstream/media-sequencer/internal/repository"
)

// The service declares the narrow storage interfaces it needs. The PostgreSQL
// repositories satisfy them in production; tests substitute in-memory fakes.

// WindowStore persists display windows.
type WindowStore interface {
	List(ctx context.Context) ([]models.Window, error)
	Get(ctx context.Context, id int64) (models.Window, error)
	Create(ctx context.Context, name string, cycleAnchor time.Time) (models.Window, error)
}

// MediaStore persists the media library.
type MediaStore interface {
	List(ctx context.Context) ([]models.Media, error)
	Get(ctx context.Context, id int64) (models.Media, error)
	Create(ctx context.Context, in models.MediaInput) (models.Media, error)
}

// PlaylistStore persists per-window playlists with contiguous positions.
type PlaylistStore interface {
	ListByWindow(ctx context.Context, windowID int64) ([]models.PlaylistItem, error)
	ListAll(ctx context.Context) (map[int64][]models.PlaylistItem, error)
	Append(ctx context.Context, windowID, mediaID int64) (models.PlaylistItem, error)
	Remove(ctx context.Context, windowID, itemID int64) error
	Move(ctx context.Context, windowID, itemID int64, position int) (models.PlaylistItem, error)
}

// SyncStore persists global sync overrides.
type SyncStore interface {
	Create(ctx context.Context, mediaID int64, startAt, endAt, now time.Time) (models.SyncEvent, error)
	Live(ctx context.Context, now time.Time) (models.SyncEvent, error)
	CancelLive(ctx context.Context, now time.Time) (models.SyncEvent, error)
}

// Stores is the storage dependency set of the Sequencer.
type Stores struct {
	Windows  WindowStore
	Media    MediaStore
	Playlist PlaylistStore
	Sync     SyncStore
}

// StoresFrom adapts the PostgreSQL repositories to the service interfaces.
func StoresFrom(s *repository.Store) Stores {
	return Stores{
		Windows:  s.Windows,
		Media:    s.Media,
		Playlist: s.Playlist,
		Sync:     s.Sync,
	}
}
