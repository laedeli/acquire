package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
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
	ProfileID  string

	// HoldingsAt is when acquire last learned what the library holds of the
	// title; nil while that is unknown, and then none of its targets is
	// missing or grabbed automatically. HoldingsError is why the last attempt
	// to learn it failed. CatalogID is the catalog's own id for it.
	HoldingsAt    *time.Time
	HoldingsError string
	CatalogID     string

	// FollowedAt is when a request first made acquire follow the title; nil
	// for a title only the import tracks. UnfollowedAt is when its last
	// request went, nil while one follows it. Origin is OriginRequest for a
	// title a request created. TMDBStatus is TMDB's word on it (Returning
	// Series, Ended, Released …); InventoryAt is when TMDB was last read for
	// it, nil while never, and InventoryError why the last read failed.
	FollowedAt     *time.Time
	UnfollowedAt   *time.Time
	Origin         string
	TMDBStatus     string
	InventoryAt    *time.Time
	InventoryError string
	// Countries are the countries TMDB says it comes from (ISO 3166-1);
	// none before TMDB was read for it.
	Countries []string
	// PreMonitored is whether it was monitored before a follow began; nil
	// for a title a request created, or one not followed. Approved is set
	// when an admin approved a request that follows it, or one was approved
	// at once.
	PreMonitored *bool
	Approved     bool
	// ScheduleID is the series' show id at the schedule source (0: not
	// looked up, or not known there); ScheduleAt when its air times were
	// last read — nil while never —, and ScheduleError why the last read
	// failed. ScheduleKind is how the source says it airs (ScheduleBroadcast,
	// ScheduleStreaming; "" when it says neither), ScheduleCountry the
	// country of its network or streaming service.
	ScheduleID      int64
	ScheduleAt      *time.Time
	ScheduleError   string
	ScheduleKind    string
	ScheduleCountry string
	// ScheduleVersion is the version of the rules its air times were read
	// by (ScheduleVersion; 0: before them).
	ScheduleVersion int
	// Genres are TMDB's genre ids of a series, TMDBType its kind of show
	// (Scripted, News, Talk Show …); LongSeason is set when one of its
	// seasons holds more than longSeason episodes.
	Genres     []int
	TMDBType   string
	LongSeason bool
}

// longSeason is the most episodes a season holds of a series whose releases
// name the season and episode: one of more is numbered as a single run, or
// named by its dates.
const longSeason = 100

// HandsOff reports whether no automatic grab takes anything of a series: its
// releases name no season and episode acquire can tell. The import says it is
// anime — numbered by an absolute episode number — or daily — named by its
// date —; TMDB files it as animation from Japan, or as a talk or news show;
// or a season of it holds more than longSeason episodes. It is searched, and
// its episodes are grabbed from the missing tab.
func (t Title) HandsOff() bool {
	if t.Kind != "series" {
		return false
	}
	switch {
	case t.SeriesType == "anime", t.SeriesType == "daily":
		return true
	case t.TMDBType == "Talk Show", t.TMDBType == "News":
		return true
	case t.LongSeason:
		return true
	}
	animation, japan := false, false
	for _, g := range t.Genres {
		animation = animation || g == genreAnimation
	}
	for _, c := range t.Countries {
		japan = japan || c == "JP"
	}
	return animation && japan
}

// genreAnimation is TMDB's genre id of animation.
const genreAnimation = 16

// longSeasonSQL is Title.LongSeason of the title t (longSeason).
const longSeasonSQL = `EXISTS (SELECT 1 FROM seasons s WHERE s.title_id = t.id AND s.episode_count > 100)`

// handsOffSQL is Title.HandsOff of the title t.
const handsOffSQL = `(t.kind = 'series' AND (t.series_type IN ('anime', 'daily') OR t.tmdb_type IN ('Talk Show', 'News')
	OR (16 = ANY(t.genres) AND 'JP' = ANY(t.origin_country)) OR ` + longSeasonSQL + `))`

// HeldForApproval reports whether nothing of a title may be grabbed — on its
// own, or from the missing tab — until an admin approves a request for it: a
// request created it, and none of its requests is approved. A title acquire
// tracked before is the admin's: a request awaiting approval changes nothing
// of it.
func (t Title) HeldForApproval() bool { return t.Origin == OriginRequest && !t.Approved }

// approvedSQL is Title.Approved of the title t.
const approvedSQL = `EXISTS (SELECT 1 FROM wanted_items w WHERE w.title_id = t.id AND w.approval = 'approved')`

// heldForApprovalSQL is Title.HeldForApproval of the title t.
const heldForApprovalSQL = `(t.origin = 'request' AND NOT ` + approvedSQL + `)`

// OriginRequest is the origin of a title a request created.
const OriginRequest = "request"

// HoldingsKnown reports whether acquire knows what the library holds of it.
func (t Title) HoldingsKnown() bool { return t.HoldingsAt != nil }

// Followed reports whether a request follows the title now.
func (t Title) Followed() bool { return t.FollowedAt != nil && t.UnfollowedAt == nil }

// followedSQL is Title.Followed of the title t.
const followedSQL = `(t.followed_at IS NOT NULL AND t.unfollowed_at IS NULL)`

// titleCols are the columns scanTitle reads, under the alias t.
const titleCols = `t.id, t.tmdb_id, COALESCE(t.tvdb_id,0), t.imdb_id, t.kind, t.title, t.sort_title,
	COALESCE(t.year,0), t.status, t.series_type, t.monitored, t.monitor_new, t.profile_id,
	t.holdings_at, t.holdings_error, t.catalog_id,
	t.followed_at, t.unfollowed_at, t.origin, t.tmdb_status, t.inventory_at, t.inventory_error, t.origin_country,
	t.pre_monitored, ` + approvedSQL + `, COALESCE(t.schedule_id, 0), t.schedule_at, t.schedule_error,
	t.schedule_kind, t.schedule_country, t.schedule_version, t.genres, t.tmdb_type, ` + longSeasonSQL

func scanTitle(r interface{ Scan(...any) error }, extra ...any) (Title, error) {
	var t Title
	err := r.Scan(append([]any{&t.ID, &t.TMDBID, &t.TVDBID, &t.IMDBID, &t.Kind, &t.Title, &t.SortTitle,
		&t.Year, &t.Status, &t.SeriesType, &t.Monitored, &t.MonitorNew, &t.ProfileID,
		&t.HoldingsAt, &t.HoldingsError, &t.CatalogID,
		&t.FollowedAt, &t.UnfollowedAt, &t.Origin, &t.TMDBStatus, &t.InventoryAt, &t.InventoryError, &t.Countries,
		&t.PreMonitored, &t.Approved, &t.ScheduleID, &t.ScheduleAt, &t.ScheduleError,
		&t.ScheduleKind, &t.ScheduleCountry, &t.ScheduleVersion, &t.Genres, &t.TMDBType, &t.LongSeason}, extra...)...)
	return t, err
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
	q := `SELECT ` + titleCols + ` FROM titles t WHERE t.kind = $1`
	if monitoredOnly {
		q += ` AND t.monitored`
	}
	q += ` ORDER BY t.sort_title, t.title`
	rows, err := s.pool.Query(ctx, q, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Title
	for rows.Next() {
		t, err := scanTitle(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// TitleByID reads one title, or ErrNotFound.
func (s *Store) TitleByID(ctx context.Context, id string) (Title, error) {
	t, err := scanTitle(s.pool.QueryRow(ctx, `SELECT `+titleCols+` FROM titles t WHERE t.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Title{}, ErrNotFound
	}
	return t, err
}

// TitleByIdentity finds the tracked title of a kind (movie | series) that one
// of the identities names: tmdb first, then tvdb, then imdb. ErrNotFound when
// none does.
func (s *Store) TitleByIdentity(ctx context.Context, kind string, tmdb, tvdb int64, imdb string) (Title, error) {
	t, err := scanTitle(s.pool.QueryRow(ctx, `
		SELECT `+titleCols+` FROM titles t
		 WHERE t.kind = $1 AND (($2::bigint > 0 AND t.tmdb_id = $2) OR ($3::bigint > 0 AND t.tvdb_id = $3)
		                        OR ($4::text <> '' AND t.imdb_id = $4))
		 ORDER BY t.tmdb_id = $2 DESC, COALESCE(t.tvdb_id = $3, false) DESC, t.id
		 LIMIT 1`, kind, tmdb, tvdb, imdb))
	if errors.Is(err, pgx.ErrNoRows) {
		return Title{}, ErrNotFound
	}
	return t, err
}

// TargetWithTitle reads a target together with the title it belongs to, which
// is always what a search needs — the ids and aliases live on the title.
func (s *Store) TargetWithTitle(ctx context.Context, targetID string) (Target, Title, error) {
	t, err := s.GetTarget(ctx, targetID)
	if err != nil {
		return t, Title{}, err
	}
	ti, err := s.TitleByID(ctx, t.TitleID)
	return t, ti, err
}

// TargetAt reads a title's target at a coordinate: an episode, or — season and
// episode nil — the movie. ErrNotFound when it has none there.
func (s *Store) TargetAt(ctx context.Context, titleID string, season, episode *int) (Target, error) {
	t, err := scanTarget(s.pool.QueryRow(ctx, `SELECT `+targetCols+` FROM acquisition_targets a
		 WHERE a.title_id = $1 AND a.season_number IS NOT DISTINCT FROM $2::int
		   AND a.episode_number IS NOT DISTINCT FROM $3::int`, titleID, season, episode))
	if errors.Is(err, pgx.ErrNoRows) {
		return Target{}, ErrNotFound
	}
	return t, err
}

// AliasesFor returns every known alias, across sources. Production matched 119
// of 500 grabs on an alias rather than the title.
func (s *Store) AliasesFor(ctx context.Context, titleID string) ([]string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT alias FROM title_aliases WHERE title_id = $1`, titleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// AllAliases returns every title's aliases, by title id.
func (s *Store) AllAliases(ctx context.Context) (map[string][]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT title_id, alias FROM title_aliases ORDER BY title_id, alias`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var id, a string
		if err := rows.Scan(&id, &a); err != nil {
			return nil, err
		}
		out[id] = append(out[id], a)
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
