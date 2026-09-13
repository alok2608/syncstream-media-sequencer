// Package handlers exposes the REST API. Handlers stay thin: decode, delegate
// to the service, encode.
package handlers

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/syncstream/media-sequencer/internal/httpx"
	"github.com/syncstream/media-sequencer/internal/models"
	"github.com/syncstream/media-sequencer/internal/service"
)

// API bundles the handler dependencies.
type API struct {
	sequencer *service.Sequencer
	logger    *slog.Logger
	// wsClients reports the number of live WebSocket clients for /health.
	wsClients func() int
}

// NewAPI builds the handler set.
func NewAPI(sequencer *service.Sequencer, logger *slog.Logger, wsClients func() int) *API {
	return &API{sequencer: sequencer, logger: logger, wsClients: wsClients}
}

// Health reports liveness plus a little operational context.
func (a *API) Health(w http.ResponseWriter, r *http.Request) {
	snapshot, err := a.sequencer.Snapshot(r.Context())
	if err != nil {
		// The process is up but its database is not; say so explicitly.
		a.logger.Error("health check could not reach the database", "error", err)
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "database is unreachable")
		return
	}
	httpx.WriteData(w, http.StatusOK, map[string]any{
		"status":           "ok",
		"serverTime":       snapshot.ServerTime,
		"cycleMillis":      snapshot.CycleMillis,
		"windows":          len(snapshot.Windows),
		"mediaItems":       len(snapshot.Media),
		"websocketClients": a.wsClients(),
		"globalSyncActive": snapshot.ActiveSync != nil,
		"bundledAssets":    assetNames(),
	})
}

// Time is the lightweight clock endpoint clients use to estimate their offset
// from the server before the WebSocket is up.
func (a *API) Time(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	httpx.WriteData(w, http.StatusOK, map[string]any{
		"serverTime":       now,
		"serverTimeMillis": now.UnixMilli(),
	})
}

// State returns the full bootstrap snapshot in one request.
func (a *API) State(w http.ResponseWriter, r *http.Request) {
	snapshot, err := a.sequencer.Snapshot(r.Context())
	if err != nil {
		httpx.WriteServiceError(w, a.logger, err)
		return
	}
	httpx.WriteData(w, http.StatusOK, snapshot)
}

// ListWindows returns every window with its playlist.
func (a *API) ListWindows(w http.ResponseWriter, r *http.Request) {
	windows, err := a.sequencer.WindowsWithPlaylists(r.Context())
	if err != nil {
		httpx.WriteServiceError(w, a.logger, err)
		return
	}
	httpx.WriteData(w, http.StatusOK, windows)
}

// GetWindow returns one window with its playlist.
func (a *API) GetWindow(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, err.Error())
		return
	}
	window, err := a.sequencer.WindowWithPlaylist(r.Context(), id)
	if err != nil {
		httpx.WriteServiceError(w, a.logger, err)
		return
	}
	httpx.WriteData(w, http.StatusOK, window)
}

// CreateWindow adds a display window.
func (a *API) CreateWindow(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, err.Error())
		return
	}
	window, err := a.sequencer.CreateWindow(r.Context(), body.Name)
	if err != nil {
		httpx.WriteServiceError(w, a.logger, err)
		return
	}
	httpx.WriteData(w, http.StatusCreated, window)
}

// GetPlaylist returns one window's playlist in play order.
func (a *API) GetPlaylist(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, err.Error())
		return
	}
	items, err := a.sequencer.Playlist(r.Context(), id)
	if err != nil {
		httpx.WriteServiceError(w, a.logger, err)
		return
	}
	httpx.WriteData(w, http.StatusOK, items)
}

// AddPlaylistItem appends media to a window's playlist, creating the media
// inline when the request supplies a media object instead of an id.
func (a *API) AddPlaylistItem(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, err.Error())
		return
	}
	var body service.AddPlaylistItemInput
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, err.Error())
		return
	}
	window, err := a.sequencer.AddPlaylistItem(r.Context(), id, body)
	if err != nil {
		httpx.WriteServiceError(w, a.logger, err)
		return
	}
	httpx.WriteData(w, http.StatusCreated, window)
}

// MovePlaylistItem reorders an item within a window's playlist.
func (a *API) MovePlaylistItem(w http.ResponseWriter, r *http.Request) {
	windowID, itemID, ok := a.playlistItemIDs(w, r)
	if !ok {
		return
	}
	var body struct {
		Position *int `json:"position"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, err.Error())
		return
	}
	if body.Position == nil {
		httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.CodeValidation,
			"the request contains invalid values",
			models.ValidationError{Field: "position", Message: "position is required"})
		return
	}
	window, err := a.sequencer.MovePlaylistItem(r.Context(), windowID, itemID, *body.Position)
	if err != nil {
		httpx.WriteServiceError(w, a.logger, err)
		return
	}
	httpx.WriteData(w, http.StatusOK, window)
}

// DeletePlaylistItem removes an item and closes the gap.
func (a *API) DeletePlaylistItem(w http.ResponseWriter, r *http.Request) {
	windowID, itemID, ok := a.playlistItemIDs(w, r)
	if !ok {
		return
	}
	window, err := a.sequencer.RemovePlaylistItem(r.Context(), windowID, itemID)
	if err != nil {
		httpx.WriteServiceError(w, a.logger, err)
		return
	}
	httpx.WriteData(w, http.StatusOK, window)
}

func (a *API) playlistItemIDs(w http.ResponseWriter, r *http.Request) (int64, int64, bool) {
	windowID, err := httpx.PathID(r, "id")
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, err.Error())
		return 0, 0, false
	}
	itemID, err := httpx.PathID(r, "itemId")
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, err.Error())
		return 0, 0, false
	}
	return windowID, itemID, true
}

// CurrentPlayback exposes the server's reference playback resolution, which
// makes the 5-hour cycle observable from the command line.
func (a *API) CurrentPlayback(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, err.Error())
		return
	}
	current, err := a.sequencer.CurrentPlayback(r.Context(), id)
	if err != nil {
		httpx.WriteServiceError(w, a.logger, err)
		return
	}
	httpx.WriteData(w, http.StatusOK, current)
}

// ListMedia returns the media library.
func (a *API) ListMedia(w http.ResponseWriter, r *http.Request) {
	media, err := a.sequencer.ListMedia(r.Context())
	if err != nil {
		httpx.WriteServiceError(w, a.logger, err)
		return
	}
	httpx.WriteData(w, http.StatusOK, media)
}

// CreateMedia adds an item to the media library.
func (a *API) CreateMedia(w http.ResponseWriter, r *http.Request) {
	var body models.MediaInput
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, err.Error())
		return
	}
	media, err := a.sequencer.CreateMedia(r.Context(), body)
	if err != nil {
		httpx.WriteServiceError(w, a.logger, err)
		return
	}
	httpx.WriteData(w, http.StatusCreated, media)
}

// StartSync triggers the global override.
func (a *API) StartSync(w http.ResponseWriter, r *http.Request) {
	var body service.SyncRequest
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, err.Error())
		return
	}
	event, err := a.sequencer.StartSync(r.Context(), body)
	if err != nil {
		httpx.WriteServiceError(w, a.logger, err)
		return
	}
	httpx.WriteData(w, http.StatusCreated, event)
}

// ActiveSync returns the live override, or null. A client that refreshes or
// connects mid-sync calls this and joins at the right offset.
func (a *API) ActiveSync(w http.ResponseWriter, r *http.Request) {
	event, err := a.sequencer.LiveSync(r.Context())
	if err != nil {
		httpx.WriteServiceError(w, a.logger, err)
		return
	}
	httpx.WriteData(w, http.StatusOK, event)
}

// CancelSync ends the live override early.
func (a *API) CancelSync(w http.ResponseWriter, r *http.Request) {
	event, err := a.sequencer.CancelSync(r.Context())
	if err != nil {
		httpx.WriteServiceError(w, a.logger, err)
		return
	}
	if event == nil {
		httpx.WriteError(w, http.StatusNotFound, httpx.CodeNotFound, "there is no active sync to cancel")
		return
	}
	httpx.WriteData(w, http.StatusOK, event)
}
