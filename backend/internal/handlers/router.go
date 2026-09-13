package handlers

import (
	"log/slog"
	"net/http"

	"github.com/syncstream/media-sequencer/internal/config"
	"github.com/syncstream/media-sequencer/internal/httpx"
)

// Router wires every route. It uses the standard library multiplexer with
// method+pattern routing, so the service pulls in no router dependency.
func Router(api *API, wsHandler http.Handler, cfg config.Config, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", api.Health)
	mux.HandleFunc("GET /api/time", api.Time)
	mux.HandleFunc("GET /api/state", api.State)

	mux.HandleFunc("GET /api/windows", api.ListWindows)
	mux.HandleFunc("POST /api/windows", api.CreateWindow)
	mux.HandleFunc("GET /api/windows/{id}", api.GetWindow)
	mux.HandleFunc("GET /api/windows/{id}/playlist", api.GetPlaylist)
	mux.HandleFunc("POST /api/windows/{id}/playlist", api.AddPlaylistItem)
	mux.HandleFunc("PATCH /api/windows/{id}/playlist/{itemId}", api.MovePlaylistItem)
	mux.HandleFunc("DELETE /api/windows/{id}/playlist/{itemId}", api.DeletePlaylistItem)
	mux.HandleFunc("GET /api/windows/{id}/current", api.CurrentPlayback)

	mux.HandleFunc("GET /api/media", api.ListMedia)
	mux.HandleFunc("POST /api/media", api.CreateMedia)

	mux.HandleFunc("POST /api/sync", api.StartSync)
	mux.HandleFunc("GET /api/sync/active", api.ActiveSync)
	mux.HandleFunc("POST /api/sync/cancel", api.CancelSync)

	// Demo media bundled into the binary, so the seed data depends on no
	// third-party host. Range requests are supported so videos can seek.
	mux.HandleFunc("GET /api/assets/{name}", api.ServeAsset)

	mux.Handle("GET /ws", wsHandler)

	// Anything else is a 404 in the same JSON envelope as every other error.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteError(w, http.StatusNotFound, httpx.CodeNotFound, "no route matches "+r.Method+" "+r.URL.Path)
	})

	return httpx.Chain(mux,
		httpx.Recoverer(logger),
		httpx.RequestLogger(logger, cfg.LogRequests),
		httpx.CORS(cfg.OriginAllowed, cfg.AllowsAnyOrigin()),
	)
}
