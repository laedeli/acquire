package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
)

// Target is one acquisition intent: a movie, or one episode of a series.
type Target struct {
	ID            string
	TitleID       string
	Kind          string // movie | episode
	SeasonNumber  *int
	EpisodeNumber *int
	AirDate       *time.Time
	Monitored     bool
	State         string

	// The HAVE side, projected from katalog.
	HeldItemID  string
	HeldRelease string
	HeldScore   int
	HeldSource  string // grab | derived
}

// UpsertTarget creates or updates a target by its coordinates. The unique index
// is (title_id, season_number, episode_number) NULLS NOT DISTINCT, so a movie —
// both coordinates NULL — collides with itself instead of duplicating.
//
// It deliberately does NOT touch the held_* columns: those are owned by the
// reconciler, and an intent import must never overwrite what we actually have.
func (s *Store) UpsertTarget(ctx context.Context, t Target) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO acquisition_targets
		  (id, title_id, kind, season_number, episode_number, air_date, monitored, state)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (title_id, season_number, episode_number) DO UPDATE
		   SET air_date  = EXCLUDED.air_date,
		       monitored = EXCLUDED.monitored,
		       updated_at = now()`,
		t.ID, t.TitleID, t.Kind, t.SeasonNumber, t.EpisodeNumber, t.AirDate, t.Monitored, t.State)
	return err
}

// ApplyHolding records that katalog holds a file for this target.
//
// This is the HAVE→WANT projection, and it is the only writer of held_*. The
// score is stored together with the profile it was computed under, so a profile
// change can invalidate it; without that pairing the cutoff backlog goes stale
// silently and nothing detects it.
//
// held_source distinguishes a score derived from a real grab from one inferred
// off a pre-existing file. The 16,169 legacy episode files have no grab row, so
// their quality is guessed from assets and path — an upgrade must not fire on a
// guess, and P5 decides that policy knowing which rows are which.
func (s *Store) ApplyHolding(ctx context.Context, targetID, itemID, release, profileID, source string, quality any, score int) error {
	q, err := json.Marshal(quality)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		UPDATE acquisition_targets
		   SET held_item_id = $2, held_release = $3, held_quality = $4,
		       held_score = $5, held_score_profile = $6, held_source = $7,
		       held_scored_at = now(),
		       held_at = COALESCE(held_at, now()),
		       state = 'held',
		       updated_at = now()
		 WHERE id = $1`,
		targetID, itemID, release, q, score, profileID, source)
	return err
}

// ClearHolding reverses it when katalog says the file is gone. A target whose
// file was deleted becomes wanted again — that is the whole point of modelling
// WANT separately from HAVE.
func (s *Store) ClearHolding(ctx context.Context, itemID string) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE acquisition_targets
		   SET held_item_id = '', held_release = '', held_quality = '{}'::jsonb,
		       held_score = 0, held_score_profile = '', held_source = '',
		       held_scored_at = NULL, held_at = NULL,
		       state = 'wanted', updated_at = now()
		 WHERE held_item_id = $1`, itemID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// InvalidateHeldScores marks every cached score computed under a profile as
// stale. SaveProfile must call this, or the cutoff sweep keeps comparing new
// preferences against scores computed under the old ones.
func (s *Store) InvalidateHeldScores(ctx context.Context, profileID string) (int64, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE acquisition_targets SET held_scored_at = NULL WHERE held_score_profile = $1`,
		profileID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// DueTargets returns monitored, wanted targets whose air window has opened and
// whose retry backoff has expired. This is the backlog sweep's only query, and
// it is served by targets_missing_idx.
//
// Unaired episodes are excluded by air_window_opens_at rather than filtered in
// Go: searching for an episode that has not aired burns indexer quota against
// releases that cannot exist, and indexer quota is the scarce resource — three
// capability sweeps were enough to rate-limit the most important indexer.
func (s *Store) DueTargets(ctx context.Context, limit int) ([]Target, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, title_id, kind, season_number, episode_number, air_date, monitored, state
		  FROM acquisition_targets
		 WHERE state = 'wanted'
		   AND monitored
		   AND (air_window_opens_at IS NULL OR air_window_opens_at <= now())
		   AND (search_backoff_until IS NULL OR search_backoff_until <= now())
		 ORDER BY air_window_opens_at NULLS FIRST, id
		 LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTargets(rows)
}

// CutoffUnmet returns held targets whose score is below what the profile now
// wants. One index scan on targets_cutoff_idx — the reason held_score is
// materialised rather than asked of katalog per row.
func (s *Store) CutoffUnmet(ctx context.Context, cutoff int, limit int) ([]Target, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, title_id, kind, season_number, episode_number, air_date, monitored, state
		  FROM acquisition_targets
		 WHERE state = 'held' AND monitored
		   AND held_score < $1
		   AND held_source = 'grab'   -- never upgrade off a guessed legacy score
		 ORDER BY held_score
		 LIMIT $2`, cutoff, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTargets(rows)
}

// BackoffSearch records a failed search and pushes the next attempt out.
// Exponential with a ceiling: a title nobody seeds should not be retried every
// sweep forever, and the ceiling stops the interval growing past usefulness.
func (s *Store) BackoffSearch(ctx context.Context, targetID string, base, max time.Duration) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE acquisition_targets
		   SET search_failures = search_failures + 1,
		       last_search_at = now(),
		       search_backoff_until = now() + LEAST(
		         $2::interval * POWER(2, LEAST(search_failures, 10)),
		         $3::interval),
		       updated_at = now()
		 WHERE id = $1`, targetID, base.String(), max.String())
	return err
}

func scanTargets(rows pgx.Rows) ([]Target, error) {
	var out []Target
	for rows.Next() {
		var t Target
		if err := rows.Scan(&t.ID, &t.TitleID, &t.Kind, &t.SeasonNumber, &t.EpisodeNumber,
			&t.AirDate, &t.Monitored, &t.State); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
