// Package repository is the only place that speaks SQL. Every method takes a
// context and returns domain models from internal/models.
package repository

import (
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/syncstream/media-sequencer/internal/models"
)

// Store bundles the repositories so the service layer receives one dependency.
type Store struct {
	Windows  *WindowRepository
	Media    *MediaRepository
	Playlist *PlaylistRepository
	Sync     *SyncRepository
}

// New builds a Store backed by the given pool.
func New(pool *pgxpool.Pool) *Store {
	return &Store{
		Windows:  &WindowRepository{pool: pool},
		Media:    &MediaRepository{pool: pool},
		Playlist: &PlaylistRepository{pool: pool},
		Sync:     &SyncRepository{pool: pool},
	}
}

// translateNoRows maps pgx's sentinel onto the domain sentinel so that upper
// layers never need to import pgx.
func translateNoRows(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return models.ErrNotFound
	}
	return err
}
