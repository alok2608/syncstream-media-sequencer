// Command seed installs the demo dataset (three windows, eight media items).
//
// It is a no-op when the database already contains windows; pass -force to
// wipe playback configuration and re-seed from scratch.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"time"

	"github.com/syncstream/media-sequencer/internal/config"
	"github.com/syncstream/media-sequencer/internal/database"
	"github.com/syncstream/media-sequencer/internal/repository"
	"github.com/syncstream/media-sequencer/internal/seed"
)

func main() {
	force := flag.Bool("force", false, "delete existing windows, media and playlists, then re-seed")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if err := run(logger, *force); err != nil {
		logger.Error("seeding failed", "error", err)
		os.Exit(1)
	}
	logger.Info("seeding complete")
}

func run(logger *slog.Logger, force bool) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	pool, err := database.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := database.Migrate(ctx, pool); err != nil {
		return err
	}

	if force {
		logger.Warn("-force: clearing existing playback configuration")
		// Ordered by dependency; playlist_items also cascades from windows.
		for _, stmt := range []string{
			`DELETE FROM sync_events`,
			`DELETE FROM playlist_items`,
			`DELETE FROM windows`,
			`DELETE FROM media`,
		} {
			if _, err := pool.Exec(ctx, stmt); err != nil {
				return err
			}
		}
	}

	return seed.Run(ctx, repository.New(pool), logger, force)
}
