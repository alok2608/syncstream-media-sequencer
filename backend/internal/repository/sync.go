package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/syncstream/media-sequencer/internal/models"
)

// SyncRepository persists global sync overrides. Sync events are stored so that
// a client which refreshes (or connects for the first time) mid-sync can fetch
// the active override and join it at the right offset.
type SyncRepository struct{ pool *pgxpool.Pool }

const syncSelect = `
	SELECT s.id, s.start_at, s.end_at, s.cancelled_at, s.created_at,
	       m.id, m.name, m.type, m.url, m.duration_seconds, m.created_at
	FROM sync_events s
	JOIN media m ON m.id = s.media_id`

// Create supersedes any live override and stores the new one atomically, so
// two operators pressing SYNC at once can never leave two overrides live.
func (r *SyncRepository) Create(ctx context.Context, mediaID int64, startAt, endAt, now time.Time) (models.SyncEvent, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return models.SyncEvent{}, fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if _, err := tx.Exec(ctx,
		`UPDATE sync_events SET cancelled_at = $1 WHERE cancelled_at IS NULL AND end_at > $1`, now,
	); err != nil {
		return models.SyncEvent{}, fmt.Errorf("supersede active sync: %w", err)
	}

	var id int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO sync_events (media_id, start_at, end_at) VALUES ($1, $2, $3) RETURNING id`,
		mediaID, startAt, endAt).Scan(&id); err != nil {
		return models.SyncEvent{}, fmt.Errorf("insert sync event: %w", err)
	}

	event, err := scanSyncRow(tx.QueryRow(ctx, syncSelect+` WHERE s.id = $1`, id))
	if err != nil {
		return models.SyncEvent{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return models.SyncEvent{}, fmt.Errorf("commit transaction: %w", err)
	}
	return event, nil
}

// Live returns the newest override that has not finished or been cancelled.
// It includes an override whose start is still a few hundred milliseconds in
// the future, so a client joining during the lead-in can prepare in time.
func (r *SyncRepository) Live(ctx context.Context, now time.Time) (models.SyncEvent, error) {
	return scanSyncRow(r.pool.QueryRow(ctx, syncSelect+`
		WHERE s.cancelled_at IS NULL AND s.end_at > $1
		ORDER BY s.id DESC
		LIMIT 1`, now))
}

// CancelLive ends the current override immediately and returns it.
func (r *SyncRepository) CancelLive(ctx context.Context, now time.Time) (models.SyncEvent, error) {
	var id int64
	if err := r.pool.QueryRow(ctx,
		`UPDATE sync_events SET cancelled_at = $1
		 WHERE id = (
			SELECT id FROM sync_events
			WHERE cancelled_at IS NULL AND end_at > $1
			ORDER BY id DESC LIMIT 1
		 )
		 RETURNING id`, now).Scan(&id); err != nil {
		return models.SyncEvent{}, translateNoRows(err)
	}
	return scanSyncRow(r.pool.QueryRow(ctx, syncSelect+` WHERE s.id = $1`, id))
}

func scanSyncRow(row pgx.Row) (models.SyncEvent, error) {
	var e models.SyncEvent
	err := row.Scan(&e.ID, &e.StartAt, &e.EndAt, &e.CancelledAt, &e.CreatedAt,
		&e.Media.ID, &e.Media.Name, &e.Media.Type, &e.Media.URL,
		&e.Media.DurationSeconds, &e.Media.CreatedAt)
	if err != nil {
		return models.SyncEvent{}, translateNoRows(err)
	}
	return e, nil
}
