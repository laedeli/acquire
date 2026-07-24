// Package store is acquire's Postgres data layer for wanted_items + grabs.
package store

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var schema string

var ErrNotFound = errors.New("not found")

type Store struct{ pool *pgxpool.Pool }

func New(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	s := &Store{pool: pool}
	if _, err := pool.Exec(ctx, schema); err != nil {
		pool.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return s, nil
}

func (s *Store) Close() {
	if s.pool != nil {
		s.pool.Close()
	}
}

func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// Wanted is one requested title.
type Wanted struct {
	ID          string    `json:"id"`
	TMDBID      int64     `json:"tmdbId"`
	MediaType   string    `json:"mediaType"`
	Title       string    `json:"title"`
	Year        int       `json:"year"`
	PosterURL   string    `json:"posterUrl"`
	RequestedBy string    `json:"requestedBy"`
	RequestedAt time.Time `json:"requestedAt"`
	Status      string    `json:"status"`
	Detail      string    `json:"detail"`
	ItemID      string    `json:"itemId"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

const wantedCols = `id, tmdb_id, media_type, title, year, poster_url, requested_by, requested_at, status, detail, item_id, updated_at`

func scanWanted(r interface{ Scan(...any) error }) (Wanted, error) {
	var w Wanted
	var tmdb, year *int64
	err := r.Scan(&w.ID, &tmdb, &w.MediaType, &w.Title, &year, &w.PosterURL,
		&w.RequestedBy, &w.RequestedAt, &w.Status, &w.Detail, &w.ItemID, &w.UpdatedAt)
	if tmdb != nil {
		w.TMDBID = *tmdb
	}
	if year != nil {
		w.Year = int(*year)
	}
	return w, err
}

func (s *Store) CreateWanted(ctx context.Context, w Wanted) error {
	var tmdb, year *int64
	if w.TMDBID != 0 {
		tmdb = &w.TMDBID
	}
	if w.Year != 0 {
		y := int64(w.Year)
		year = &y
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO wanted_items (id, tmdb_id, media_type, title, year, poster_url, requested_by, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,'pending')`,
		w.ID, tmdb, w.MediaType, w.Title, year, w.PosterURL, w.RequestedBy)
	return err
}

func (s *Store) ListWanted(ctx context.Context) ([]Wanted, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+wantedCols+` FROM wanted_items ORDER BY requested_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Wanted
	for rows.Next() {
		w, err := scanWanted(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *Store) GetWanted(ctx context.Context, id string) (Wanted, error) {
	w, err := scanWanted(s.pool.QueryRow(ctx, `SELECT `+wantedCols+` FROM wanted_items WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Wanted{}, ErrNotFound
	}
	return w, err
}

// FindWantedByTMDB returns the newest non-terminal request for a tmdb id (used
// by the slot status feed + fulfillment matching).
func (s *Store) FindWantedByTMDB(ctx context.Context, tmdbID int64) (Wanted, error) {
	w, err := scanWanted(s.pool.QueryRow(ctx,
		`SELECT `+wantedCols+` FROM wanted_items WHERE tmdb_id=$1 ORDER BY requested_at DESC LIMIT 1`, tmdbID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Wanted{}, ErrNotFound
	}
	return w, err
}

// FindWantedByItemID resolves the request a catalog item belongs to (set during
// ingest) — used to mark fulfillment on catalog.item.packaged.
func (s *Store) FindWantedByItemID(ctx context.Context, itemID string) (Wanted, error) {
	w, err := scanWanted(s.pool.QueryRow(ctx,
		`SELECT `+wantedCols+` FROM wanted_items WHERE item_id=$1 ORDER BY updated_at DESC LIMIT 1`, itemID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Wanted{}, ErrNotFound
	}
	return w, err
}

func (s *Store) DeleteWanted(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM wanted_items WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetStatus updates status + detail (+ optional item id) and stamps updated_at.
func (s *Store) SetStatus(ctx context.Context, id, status, detail string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE wanted_items SET status=$2, detail=$3, updated_at=now() WHERE id=$1`, id, status, detail)
	return err
}

func (s *Store) SetItemID(ctx context.Context, id, itemID string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE wanted_items SET item_id=$2, updated_at=now() WHERE id=$1`, id, itemID)
	return err
}

// FindWantedByClientJob resolves a wanted id from a gateway job id (download
// events carry client_job_id, not the wanted id — grabs bridges them).
func (s *Store) FindWantedByClientJob(ctx context.Context, clientJobID string) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx,
		`SELECT wanted_id FROM grabs WHERE client_job_id=$1 ORDER BY created_at DESC LIMIT 1`, clientJobID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return id, err
}

func (s *Store) RecordGrab(ctx context.Context, wantedID, adapter, clientJobID, source string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO grabs (wanted_id, adapter, client_job_id, source) VALUES ($1,$2,$3,$4)
		 ON CONFLICT DO NOTHING`, wantedID, adapter, clientJobID, source)
	return err
}
