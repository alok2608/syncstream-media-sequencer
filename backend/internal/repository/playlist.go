package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/syncstream/media-sequencer/internal/models"
)

// PlaylistRepository maintains each window's ordered playlist. Positions are
// kept contiguous (0..n-1) so the playback clock can rely on a dense timeline.
type PlaylistRepository struct{ pool *pgxpool.Pool }

const playlistSelect = `
	SELECT pi.id, pi.window_id, pi.position, pi.created_at,
	       m.id, m.name, m.type, m.url, m.duration_seconds, m.created_at
	FROM playlist_items pi
	JOIN media m ON m.id = pi.media_id`

// ListByWindow returns one window's playlist in play order.
func (r *PlaylistRepository) ListByWindow(ctx context.Context, windowID int64) ([]models.PlaylistItem, error) {
	rows, err := r.pool.Query(ctx, playlistSelect+`
		WHERE pi.window_id = $1
		ORDER BY pi.position`, windowID)
	if err != nil {
		return nil, fmt.Errorf("list playlist: %w", err)
	}
	defer rows.Close()
	return scanPlaylistRows(rows)
}

// ListAll returns every playlist item across all windows, grouped by window id.
// One round-trip keeps the bootstrap snapshot cheap.
func (r *PlaylistRepository) ListAll(ctx context.Context) (map[int64][]models.PlaylistItem, error) {
	rows, err := r.pool.Query(ctx, playlistSelect+` ORDER BY pi.window_id, pi.position`)
	if err != nil {
		return nil, fmt.Errorf("list all playlists: %w", err)
	}
	defer rows.Close()

	items, err := scanPlaylistRows(rows)
	if err != nil {
		return nil, err
	}
	byWindow := make(map[int64][]models.PlaylistItem)
	for _, it := range items {
		byWindow[it.WindowID] = append(byWindow[it.WindowID], it)
	}
	return byWindow, nil
}

func scanPlaylistRows(rows pgx.Rows) ([]models.PlaylistItem, error) {
	items := make([]models.PlaylistItem, 0)
	for rows.Next() {
		var it models.PlaylistItem
		if err := rows.Scan(
			&it.ID, &it.WindowID, &it.Position, &it.CreatedAt,
			&it.Media.ID, &it.Media.Name, &it.Media.Type, &it.Media.URL,
			&it.Media.DurationSeconds, &it.Media.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan playlist item: %w", err)
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// Append adds a media item to the end of a window's playlist.
func (r *PlaylistRepository) Append(ctx context.Context, windowID, mediaID int64) (models.PlaylistItem, error) {
	var item models.PlaylistItem
	err := r.inTx(ctx, windowID, func(tx pgx.Tx) error {
		var id int64
		err := tx.QueryRow(ctx, `
			INSERT INTO playlist_items (window_id, media_id, position)
			VALUES ($1, $2, COALESCE((SELECT max(position) + 1 FROM playlist_items WHERE window_id = $1), 0))
			RETURNING id`, windowID, mediaID).Scan(&id)
		if err != nil {
			return fmt.Errorf("insert playlist item: %w", err)
		}
		item, err = r.getInTx(ctx, tx, windowID, id)
		return err
	})
	if err != nil {
		return models.PlaylistItem{}, err
	}
	return item, nil
}

// Remove deletes an item and closes the gap it leaves behind.
func (r *PlaylistRepository) Remove(ctx context.Context, windowID, itemID int64) error {
	return r.inTx(ctx, windowID, func(tx pgx.Tx) error {
		var position int
		err := tx.QueryRow(ctx,
			`DELETE FROM playlist_items WHERE id = $1 AND window_id = $2 RETURNING position`,
			itemID, windowID).Scan(&position)
		if err != nil {
			return translateNoRows(err)
		}
		if _, err := tx.Exec(ctx,
			`UPDATE playlist_items SET position = position - 1
			 WHERE window_id = $1 AND position > $2`, windowID, position); err != nil {
			return fmt.Errorf("compact playlist positions: %w", err)
		}
		return nil
	})
}

// Move repositions an item, shifting the items in between. The target position
// is clamped into range, so a client can send 0 or a large number to mean
// "first" or "last".
func (r *PlaylistRepository) Move(ctx context.Context, windowID, itemID int64, target int) (models.PlaylistItem, error) {
	var item models.PlaylistItem
	err := r.inTx(ctx, windowID, func(tx pgx.Tx) error {
		var current, count int
		if err := tx.QueryRow(ctx,
			`SELECT position FROM playlist_items WHERE id = $1 AND window_id = $2`,
			itemID, windowID).Scan(&current); err != nil {
			return translateNoRows(err)
		}
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM playlist_items WHERE window_id = $1`, windowID).Scan(&count); err != nil {
			return fmt.Errorf("count playlist items: %w", err)
		}

		target = clamp(target, 0, count-1)
		if target != current {
			// Slide the displaced range one step towards the vacated slot. The
			// unique(window_id, position) constraint is deferred, so the
			// intermediate collision inside this transaction is fine.
			var shift string
			if target > current {
				shift = `UPDATE playlist_items SET position = position - 1
				         WHERE window_id = $1 AND position > $2 AND position <= $3`
			} else {
				shift = `UPDATE playlist_items SET position = position + 1
				         WHERE window_id = $1 AND position >= $3 AND position < $2`
			}
			if _, err := tx.Exec(ctx, shift, windowID, current, target); err != nil {
				return fmt.Errorf("shift playlist positions: %w", err)
			}
			if _, err := tx.Exec(ctx,
				`UPDATE playlist_items SET position = $3 WHERE id = $1 AND window_id = $2`,
				itemID, windowID, target); err != nil {
				return fmt.Errorf("move playlist item: %w", err)
			}
		}

		var err error
		item, err = r.getInTx(ctx, tx, windowID, itemID)
		return err
	})
	if err != nil {
		return models.PlaylistItem{}, err
	}
	return item, nil
}

func (r *PlaylistRepository) getInTx(ctx context.Context, tx pgx.Tx, windowID, itemID int64) (models.PlaylistItem, error) {
	var it models.PlaylistItem
	err := tx.QueryRow(ctx, playlistSelect+` WHERE pi.id = $1 AND pi.window_id = $2`, itemID, windowID).
		Scan(&it.ID, &it.WindowID, &it.Position, &it.CreatedAt,
			&it.Media.ID, &it.Media.Name, &it.Media.Type, &it.Media.URL,
			&it.Media.DurationSeconds, &it.Media.CreatedAt)
	if err != nil {
		return models.PlaylistItem{}, translateNoRows(err)
	}
	return it, nil
}

// inTx runs fn in a transaction that holds a row lock on the window, which
// serialises concurrent playlist edits and keeps positions contiguous.
func (r *PlaylistRepository) inTx(ctx context.Context, windowID int64, fn func(pgx.Tx) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var exists int64
	if err := tx.QueryRow(ctx, `SELECT id FROM windows WHERE id = $1 FOR UPDATE`, windowID).Scan(&exists); err != nil {
		return translateNoRows(err)
	}
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
