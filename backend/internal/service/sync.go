package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/syncstream/media-sequencer/internal/models"
	ws "github.com/syncstream/media-sequencer/internal/websocket"
)

// syncMutex is a named alias so the Sequencer struct reads clearly.
type syncMutex = sync.Mutex

// SyncRequest asks for a temporary global override.
type SyncRequest struct {
	MediaID         int64 `json:"mediaId"`
	DurationSeconds int   `json:"durationSeconds"`
}

// StartSync puts one media item on every window for a fixed window of time.
//
// Sync is a *render-layer* override:
//   - no playlist is read, written or reordered;
//   - each window's 5-hour cycle clock keeps advancing underneath, so when the
//     override ends every window resumes exactly at the item it would have been
//     showing had the sync never happened.
//
// The shared start instant is generated here, on the server, and pushed to
// clients as an absolute timestamp. Clients do not start a local timer when the
// message arrives - they align to startAt - so the few milliseconds of spread
// in message delivery do not desynchronise the windows.
func (s *Sequencer) StartSync(ctx context.Context, req SyncRequest) (models.SyncEvent, error) {
	if req.DurationSeconds < 1 || req.DurationSeconds > models.MaxSyncDurationSeconds {
		return models.SyncEvent{}, invalid("durationSeconds",
			fmt.Sprintf("sync duration must be between 1 and %d seconds", models.MaxSyncDurationSeconds))
	}

	media, err := s.store.Media.Get(ctx, req.MediaID)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			return models.SyncEvent{}, invalid("mediaId", fmt.Sprintf("media %d does not exist", req.MediaID))
		}
		return models.SyncEvent{}, err
	}

	now := s.now()
	// The lead-in gives every connected client time to receive the event and
	// preload the media before the shared start instant.
	startAt := now.Add(s.syncLead)
	endAt := startAt.Add(time.Duration(req.DurationSeconds) * time.Second)

	// Create() also cancels any override still live, so the newest sync always
	// wins and two overrides can never overlap.
	event, err := s.store.Sync.Create(ctx, media.ID, startAt, endAt, now)
	if err != nil {
		return models.SyncEvent{}, err
	}

	s.scheduleSyncEnd(event)
	s.publisher.Publish(ws.EventSyncStarted, event)
	s.logger.Info("global sync scheduled",
		"syncId", event.ID, "media", media.Name,
		"startAt", event.StartAt, "endAt", event.EndAt)
	return event, nil
}

// LiveSync returns the override that has not finished yet, or nil. An override
// whose start is still inside the lead-in is included, so a client that loads
// during those milliseconds still joins the sync on time.
func (s *Sequencer) LiveSync(ctx context.Context) (*models.SyncEvent, error) {
	event, err := s.store.Sync.Live(ctx, s.now())
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &event, nil
}

// CancelSync ends the live override early. Windows resume their own playlists
// on the next frame.
func (s *Sequencer) CancelSync(ctx context.Context) (*models.SyncEvent, error) {
	event, err := s.store.Sync.CancelLive(ctx, s.now())
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	s.cancelScheduledEnd()
	s.publisher.Publish(ws.EventSyncCancelled, event)
	return &event, nil
}

// scheduleSyncEnd arms a timer that announces the end of the override.
//
// The announcement is a convenience: clients already know endAt and drop the
// override on their own, so a missed or late SYNC_ENDED cannot strand a window
// in sync. It exists so idle clients refresh their status banner promptly.
func (s *Sequencer) scheduleSyncEnd(event models.SyncEvent) {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()

	if s.endTimer != nil {
		s.endTimer.Stop()
	}
	s.endSyncID = event.ID
	delay := time.Until(event.EndAt)
	if delay < 0 {
		delay = 0
	}
	id := event.ID
	s.endTimer = time.AfterFunc(delay, func() {
		s.syncMu.Lock()
		current := s.endSyncID
		s.syncMu.Unlock()
		// A newer sync superseded this one; it owns the announcement now.
		if current != id {
			return
		}
		s.publisher.Publish(ws.EventSyncEnded, map[string]int64{"syncId": id})
	})
}

func (s *Sequencer) cancelScheduledEnd() {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	if s.endTimer != nil {
		s.endTimer.Stop()
		s.endTimer = nil
	}
	s.endSyncID = 0
}

// RestoreSyncTimer re-arms the end announcement after a process restart, so a
// backend that redeploys mid-sync still announces the end to its clients.
func (s *Sequencer) RestoreSyncTimer(ctx context.Context) {
	event, err := s.LiveSync(ctx)
	if err != nil {
		s.logger.Warn("could not restore sync timer", "error", err)
		return
	}
	if event == nil {
		return
	}
	s.scheduleSyncEnd(*event)
	s.logger.Info("restored live sync override after restart", "syncId", event.ID, "endAt", event.EndAt)
}

// Shutdown releases the sync timer.
func (s *Sequencer) Shutdown() { s.cancelScheduledEnd() }
