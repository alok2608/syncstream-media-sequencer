package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/syncstream/media-sequencer/internal/models"
)

// MediaRepository reads and writes the shared media library.
type MediaRepository struct{ pool *pgxpool.Pool }

const mediaColumns = `id, name, type, url, duration_seconds, created_at`

// List returns the whole media library, newest last.
func (r *MediaRepository) List(ctx context.Context) ([]models.Media, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+mediaColumns+` FROM media ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list media: %w", err)
	}
	defer rows.Close()

	items := make([]models.Media, 0)
	for rows.Next() {
		var m models.Media
		if err := rows.Scan(&m.ID, &m.Name, &m.Type, &m.URL, &m.DurationSeconds, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan media: %w", err)
		}
		items = append(items, m)
	}
	return items, rows.Err()
}

// Get returns one media item, or models.ErrNotFound.
func (r *MediaRepository) Get(ctx context.Context, id int64) (models.Media, error) {
	var m models.Media
	err := r.pool.QueryRow(ctx, `SELECT `+mediaColumns+` FROM media WHERE id = $1`, id).
		Scan(&m.ID, &m.Name, &m.Type, &m.URL, &m.DurationSeconds, &m.CreatedAt)
	if err != nil {
		return models.Media{}, translateNoRows(err)
	}
	return m, nil
}

// Create inserts a media item.
func (r *MediaRepository) Create(ctx context.Context, in models.MediaInput) (models.Media, error) {
	var m models.Media
	err := r.pool.QueryRow(ctx,
		`INSERT INTO media (name, type, url, duration_seconds)
		 VALUES ($1, $2, $3, $4) RETURNING `+mediaColumns,
		in.Name, string(in.Type), in.URL, in.DurationSeconds).
		Scan(&m.ID, &m.Name, &m.Type, &m.URL, &m.DurationSeconds, &m.CreatedAt)
	if err != nil {
		return models.Media{}, fmt.Errorf("create media: %w", err)
	}
	return m, nil
}
