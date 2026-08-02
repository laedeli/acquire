package store

import (
	"context"
	"fmt"
	"time"
)

// Title is a movie or series we track intent for.
type Title struct {
	ID         string
	TMDBID     int64
	TVDBID     int64
	IMDBID     string
	Kind       string // movie | series
	Title      string
	SortTitle  string
	Year       int
	Status     string
	SeriesType string
	Monitored  bool
	MonitorNew bool
}

// TitleID is the deterministic id for a title. Deriving it from the identity
// rather than generating one means a re-import updates the same row instead of
// creating a second copy — which is what makes the import safe to re-run.
func TitleID(kind string, tmdbID int64) string {
	return fmt.Sprintf("%s:%d", kind, tmdbID)
}

// UpsertTitle writes intent. It deliberately does NOT touch profile_id or
// air_grace_hours: those are operator settings, and an import is not entitled
// to overwrite a decision somebody made in the console.
func (s *Store) UpsertTitle(ctx context.Context, t Title) error {
	if t.TMDBID == 0 {
		return fmt.Errorf("title %q has no tmdb id", t.Title)
	}
	if t.ID == "" {
		t.ID = TitleID(t.Kind, t.TMDBID)
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO titles
		  (id, tmdb_id, kind, title, sort_title, year, tvdb_id, imdb_id,
		   status, series_type, monitored, monitor_new)
		VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,0),$8,$9,$10,$11,$12)
		ON CONFLICT (tmdb_id, kind) DO UPDATE
		   SET title = EXCLUDED.title, sort_title = EXCLUDED.sort_title,
		       year = EXCLUDED.year,
		       -- never blank an id we already resolved with one we did not
		       tvdb_id = COALESCE(EXCLUDED.tvdb_id, titles.tvdb_id),
		       imdb_id = CASE WHEN EXCLUDED.imdb_id <> '' THEN EXCLUDED.imdb_id ELSE titles.imdb_id END,
		       status = EXCLUDED.status, series_type = EXCLUDED.series_type,
		       monitored = EXCLUDED.monitored, monitor_new = EXCLUDED.monitor_new,
		       updated_at = now()`,
		t.ID, t.TMDBID, t.Kind, t.Title, t.SortTitle, t.Year, t.TVDBID, t.IMDBID,
		t.Status, t.SeriesType, t.Monitored, t.MonitorNew)
	return err
}

// ReplaceAliases makes the stored alias set match the supplied one. Additive
// merging would accumulate stale aliases forever with no way to remove one.
func (s *Store) ReplaceAliases(ctx context.Context, titleID string, aliases []string, source string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx,
		`DELETE FROM title_aliases WHERE title_id = $1 AND source = $2`, titleID, source); err != nil {
		return err
	}
	for _, a := range aliases {
		if a == "" {
			continue
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO title_aliases (title_id, alias, source) VALUES ($1,$2,$3)
			 ON CONFLICT (title_id, alias) DO NOTHING`, titleID, a, source); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// UpsertSeason records a season without disturbing its monitored flag.
func (s *Store) UpsertSeason(ctx context.Context, titleID string, number, episodeCount int) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO seasons (title_id, season_number, episode_count)
		VALUES ($1,$2,$3)
		ON CONFLICT (title_id, season_number) DO UPDATE
		   SET episode_count = EXCLUDED.episode_count`,
		titleID, number, episodeCount)
	return err
}

// TitlesOfKind lists tracked titles, optionally only the monitored ones.
func (s *Store) TitlesOfKind(ctx context.Context, kind string, monitoredOnly bool) ([]Title, error) {
	q := `SELECT id, tmdb_id, COALESCE(tvdb_id,0), imdb_id, kind, title, sort_title,
	             COALESCE(year,0), status, series_type, monitored, monitor_new
	        FROM titles WHERE kind = $1`
	if monitoredOnly {
		q += ` AND monitored`
	}
	q += ` ORDER BY sort_title, title`
	rows, err := s.pool.Query(ctx, q, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Title
	for rows.Next() {
		var t Title
		if err := rows.Scan(&t.ID, &t.TMDBID, &t.TVDBID, &t.IMDBID, &t.Kind, &t.Title,
			&t.SortTitle, &t.Year, &t.Status, &t.SeriesType, &t.Monitored, &t.MonitorNew); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// SetAirWindow computes when a target becomes searchable: air time plus the
// title's grace hours. Searching the instant an episode airs mostly finds
// nothing and spends indexer quota, so the grace period is a real setting
// rather than a constant.
//
// The window is computed from the full TIMESTAMP, not from air_date. Deriving
// it from the date column truncates to midnight, so a 21:00 broadcast with six
// hours of grace would open its window at 06:00 the same day — fifteen hours
// before the episode exists. That is the opposite of what the grace period is
// for. air_date stays a date because that is the column type and what the
// calendar view wants — but it is derived FROM the timestamp, not the other way
// round.
//
// Every reference to $2 must be ::timestamptz. Postgres infers a parameter's
// type from its first cast, so writing `air_date = $2::date` earlier in the
// statement types the whole parameter as date; the later `$2::timestamptz` then
// widens an already-truncated value and the bug survives the fix.
func (s *Store) SetAirWindow(ctx context.Context, targetID string, air *time.Time, graceHours int) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE acquisition_targets
		   SET air_date = ($2::timestamptz)::date,
		       air_window_opens_at = CASE WHEN $2::timestamptz IS NULL THEN NULL
		                                  ELSE $2::timestamptz + make_interval(hours => $3) END,
		       updated_at = now()
		 WHERE id = $1`, targetID, air, graceHours)
	return err
}

// TargetID is the deterministic id for a coordinate, so re-deriving an
// inventory updates rows rather than duplicating them.
func TargetID(titleID string, season, episode *int) string {
	if season == nil || episode == nil {
		return titleID + ":movie"
	}
	return fmt.Sprintf("%s:s%02de%03d", titleID, *season, *episode)
}
