package search

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/xtawa/tunebridge/internal/model"
)

type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, errors.New("search result store requires database")
	}
	return &Store{db: db}, nil
}

func (s *Store) Add(ctx context.Context, identity model.TrackIdentity) error {
	if err := identity.Validate(); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO sources(id, kind, display_name) VALUES (?, ?, ?) ON CONFLICT(id) DO NOTHING`, identity.SourceID, identity.SourceID, identity.SourceID)
	if err != nil {
		return fmt.Errorf("ensure search result source: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO search_results(source_id, upstream_track_id, position) VALUES (?, ?, COALESCE((SELECT MAX(position) + 1 FROM search_results), 1)) ON CONFLICT(source_id, upstream_track_id) DO NOTHING`, identity.SourceID, identity.TrackID)
	if err != nil {
		return fmt.Errorf("add search result: %w", err)
	}
	return nil
}
func (s *Store) List(ctx context.Context, sourceID string) ([]model.TrackIdentity, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT upstream_track_id FROM search_results WHERE source_id = ? ORDER BY position, created_at, upstream_track_id`, sourceID)
	if err != nil {
		return nil, fmt.Errorf("list search results: %w", err)
	}
	defer rows.Close()
	items := []model.TrackIdentity{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		items = append(items, model.TrackIdentity{SourceID: sourceID, TrackID: id})
	}
	return items, rows.Err()
}
func (s *Store) Delete(ctx context.Context, identity model.TrackIdentity) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM search_results WHERE source_id = ? AND upstream_track_id = ?`, identity.SourceID, identity.TrackID)
	return err
}
func (s *Store) Clear(ctx context.Context, sourceID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM search_results WHERE source_id = ?`, sourceID)
	return err
}
