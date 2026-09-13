package seed

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/syncstream/media-sequencer/internal/models"
	"github.com/syncstream/media-sequencer/internal/repository"
)

// mediaSeed is one entry of the demo media library.
type mediaSeed struct {
	key string // referenced by windowSeed.items
	in  models.MediaInput
}

// windowSeed is one demo window and the media keys it plays, in order.
type windowSeed struct {
	name  string
	items []string
}

// mediaLibrary is the seeded media. It deliberately covers all three supported
// types, including one explicitly configured blank item (M7) which is the only
// way blank playback is ever reached.
func mediaLibrary() []mediaSeed {
	return []mediaSeed{
		{"M1", models.MediaInput{Name: "M1 - Sunrise Board", Type: models.MediaTypeImage, DurationSeconds: 10,
			URL: imageDataURI("M1", "Image  -  10s", "#f97316", "#db2777")}},
		{"M2", models.MediaInput{Name: "M2 - Product Promo", Type: models.MediaTypeImage, DurationSeconds: 20,
			URL: imageDataURI("M2", "Image  -  20s  -  try SYNC with this one", "#2563eb", "#7c3aed")}},
		{"M3", models.MediaInput{Name: "M3 - Brand Film", Type: models.MediaTypeVideo, DurationSeconds: 30,
			URL: videoURL("bunny-clip.mp4")}},
		{"M4", models.MediaInput{Name: "M4 - Road Trip Clip", Type: models.MediaTypeVideo, DurationSeconds: 15,
			URL: videoURL("sintel-clip.mp4")}},
		{"M5", models.MediaInput{Name: "M5 - Seasonal Offer", Type: models.MediaTypeImage, DurationSeconds: 12,
			URL: imageDataURI("M5", "Image  -  12s", "#0891b2", "#16a34a")}},
		{"M6", models.MediaInput{Name: "M6 - Store Notice", Type: models.MediaTypeImage, DurationSeconds: 8,
			URL: imageDataURI("M6", "Image  -  8s", "#7c3aed", "#0ea5e9")}},
		{"M7", models.MediaInput{Name: "M7 - Intermission (blank)", Type: models.MediaTypeBlank, DurationSeconds: 5}},
		{"M8", models.MediaInput{Name: "M8 - Highlights Reel", Type: models.MediaTypeVideo, DurationSeconds: 20,
			URL: videoURL("bunny-clip.mp4")}},
	}
}

// windowLayout matches the example in the assignment brief.
func windowLayout() []windowSeed {
	return []windowSeed{
		{"Window 1 - Lobby", []string{"M1", "M2", "M3"}},     // 60s loop
		{"Window 2 - Cafeteria", []string{"M4", "M5"}},       // 27s loop
		{"Window 3 - Reception", []string{"M6", "M7", "M8"}}, // 33s loop, includes blank
	}
}

// Run installs the demo dataset. It is a no-op when windows already exist, so
// it is safe to call on every boot; pass force to seed regardless.
func Run(ctx context.Context, store *repository.Store, logger *slog.Logger, force bool) error {
	count, err := store.Windows.Count(ctx)
	if err != nil {
		return err
	}
	if count > 0 && !force {
		logger.Info("skipping seed, database already contains windows", "windows", count)
		return nil
	}

	// All seeded windows share one cycle anchor so their 5-hour cycles line up,
	// which makes the cycle behaviour easy to reason about during evaluation.
	anchor := time.Now().UTC()

	mediaIDs := make(map[string]int64)
	for _, m := range mediaLibrary() {
		m.in.Normalize()
		if errs := m.in.Validate(); len(errs) > 0 {
			return fmt.Errorf("seed media %s is invalid: %v", m.key, errs)
		}
		created, err := store.Media.Create(ctx, m.in)
		if err != nil {
			return fmt.Errorf("seed media %s: %w", m.key, err)
		}
		mediaIDs[m.key] = created.ID
	}

	for _, ws := range windowLayout() {
		window, err := store.Windows.Create(ctx, ws.name, anchor)
		if err != nil {
			return fmt.Errorf("seed window %q: %w", ws.name, err)
		}
		for _, key := range ws.items {
			id, ok := mediaIDs[key]
			if !ok {
				return fmt.Errorf("seed window %q references unknown media %q", ws.name, key)
			}
			if _, err := store.Playlist.Append(ctx, window.ID, id); err != nil {
				return fmt.Errorf("seed playlist for %q: %w", ws.name, err)
			}
		}
		logger.Info("seeded window", "name", ws.name, "items", len(ws.items))
	}
	return nil
}
