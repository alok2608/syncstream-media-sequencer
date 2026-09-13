// Command server runs the media sequencer API: REST configuration endpoints, a
// WebSocket channel for realtime playlist and sync events, and PostgreSQL
// persistence.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/syncstream/media-sequencer/internal/config"
	"github.com/syncstream/media-sequencer/internal/database"
	"github.com/syncstream/media-sequencer/internal/handlers"
	"github.com/syncstream/media-sequencer/internal/repository"
	"github.com/syncstream/media-sequencer/internal/seed"
	"github.com/syncstream/media-sequencer/internal/service"
	ws "github.com/syncstream/media-sequencer/internal/websocket"
)

// shutdownGrace is how long in-flight requests get to finish on SIGTERM.
const shutdownGrace = 15 * time.Second

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// Signals cancel this context, which unwinds the whole startup stack.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := database.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	logger.Info("connected to postgres")

	if err := database.Migrate(ctx, pool); err != nil {
		return err
	}
	logger.Info("database schema is up to date")

	store := repository.New(pool)

	if cfg.AutoSeed {
		if err := seed.Run(ctx, store, logger, false); err != nil {
			return err
		}
	}

	// The hub's greeting needs the service, and the service needs to publish
	// through the hub, so the greeting is injected as a closure.
	var sequencer *service.Sequencer
	hub := ws.NewHub(logger, func() any {
		greetCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		snapshot, err := sequencer.Snapshot(greetCtx)
		if err != nil {
			logger.Error("could not build websocket greeting", "error", err)
			return map[string]any{"activeSync": nil}
		}
		// The greeting carries the full configuration so a reconnecting client
		// is immediately consistent without an extra REST round trip.
		return snapshot
	})
	go hub.Run()
	defer hub.Close()

	sequencer = service.New(service.StoresFrom(store), hub, logger, cfg.SyncLeadTime)
	defer sequencer.Shutdown()
	// A redeploy in the middle of a sync should still announce the end.
	sequencer.RestoreSyncTimer(ctx)

	api := handlers.NewAPI(sequencer, logger, hub.ClientCount)
	router := handlers.Router(api, hub.Handler(cfg.OriginAllowed), cfg, logger)

	server := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: router,
		// No WriteTimeout: it would cut long-lived WebSocket connections. The
		// per-message write deadlines in the websocket package cover those.
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Bind before announcing, so a port clash surfaces as a startup error
	// rather than as a "listening" line followed by a failure.
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", server.Addr, err)
	}
	logger.Info("listening", "port", cfg.Port, "allowedOrigins", cfg.AllowedOrigins)

	serverErr := make(chan error, 1)
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	select {
	case err := <-serverErr:
		return err
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return err
	}
	logger.Info("shutdown complete")
	return nil
}
