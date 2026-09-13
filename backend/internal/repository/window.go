package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/syncstream/media-sequencer/internal/models"
)

// WindowRepository reads and writes display windows.
type WindowRepository struct{ pool *pgxpool.Pool }

const windowColumns = `id, name, cycle_anchor, created_at, updated_at`

// List returns every window ordered by id.
func (r *WindowRepository) List(ctx context.Context) ([]models.Window, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+windowColumns+` FROM windows ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list windows: %w", err)
	}
	defer rows.Close()

	windows := make([]models.Window, 0)
	for rows.Next() {
		var w models.Window
		if err := rows.Scan(&w.ID, &w.Name, &w.CycleAnchor, &w.CreatedAt, &w.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan window: %w", err)
		}
		windows = append(windows, w)
	}
	return windows, rows.Err()
}

// Get returns one window, or models.ErrNotFound.
func (r *WindowRepository) Get(ctx context.Context, id int64) (models.Window, error) {
	var w models.Window
	err := r.pool.QueryRow(ctx, `SELECT `+windowColumns+` FROM windows WHERE id = $1`, id).
		Scan(&w.ID, &w.Name, &w.CycleAnchor, &w.CreatedAt, &w.UpdatedAt)
	if err != nil {
		return models.Window{}, translateNoRows(err)
	}
	return w, nil
}

// Create inserts a window with an explicit 5-hour cycle anchor. The anchor is
// persisted rather than derived, so playback survives restarts and every client
// resolves the same item for the same window.
func (r *WindowRepository) Create(ctx context.Context, name string, cycleAnchor time.Time) (models.Window, error) {
	var w models.Window
	err := r.pool.QueryRow(ctx,
		`INSERT INTO windows (name, cycle_anchor) VALUES ($1, $2) RETURNING `+windowColumns,
		name, cycleAnchor).
		Scan(&w.ID, &w.Name, &w.CycleAnchor, &w.CreatedAt, &w.UpdatedAt)
	if err != nil {
		return models.Window{}, fmt.Errorf("create window: %w", err)
	}
	return w, nil
}

// Count reports how many windows exist; used to decide whether to seed.
func (r *WindowRepository) Count(ctx context.Context) (int, error) {
	var n int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM windows`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count windows: %w", err)
	}
	return n, nil
}
