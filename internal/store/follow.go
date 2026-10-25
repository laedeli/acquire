package store

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Following a requested title, and reading TMDB again for it on the clock.
//
// A request for a series or a movie makes acquire follow the title: tracked as
// an imported one is (FollowTitle), its targets derived from TMDB and read
// again while it runs (ApplySeriesInventory, ApplyMovieInventory). The import
// remains the other way in; a title it tracks that is then requested is
// followed too, and keeps everything it had.

// FollowTitle records that a request follows a title. A title acquire does
// not track yet is created from what the request says of it — monitored,
// origin request; TMDB fills in the rest when the title is first read. One
// acquire tracks already is not made twice: it is monitored, with the seasons
// to come, while the follow lasts — what its monitoring was before is kept,
// and given back when the last request goes (UnfollowTitle) —, and it is
// followed from the first request on, even one removed since. created
// reports whether the title is new.
func (s *Store) FollowTitle(ctx context.Context, t Title) (Title, bool, error) {
	if t.TMDBID <= 0 {
		return Title{}, false, fmt.Errorf("title %q has no tmdb id", t.Title)
	}
	if t.Kind != "series" && t.Kind != "movie" {
		return Title{}, false, fmt.Errorf("title %q is neither a series nor a movie", t.Title)
	}
	if t.ID == "" {
		t.ID = TitleID(t.Kind, t.TMDBID)
	}
	var created bool
	out, err := scanTitle(s.pool.QueryRow(ctx, `
		INSERT INTO titles AS t (id, tmdb_id, kind, title, sort_title, year, monitored, monitor_new, followed_at, origin)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6::int, 0), true, true, now(), $7)
		ON CONFLICT (tmdb_id, kind) DO UPDATE
		   SET pre_monitored = CASE WHEN `+followedSQL+` THEN t.pre_monitored ELSE t.monitored END,
		       pre_monitor_new = CASE WHEN `+followedSQL+` THEN t.pre_monitor_new ELSE t.monitor_new END,
		       monitored = true, monitor_new = true,
		       followed_at = COALESCE(t.followed_at, now()), unfollowed_at = NULL, updated_at = now()
		RETURNING `+titleCols+`, (t.xmax = 0)`,
		t.ID, t.TMDBID, t.Kind, t.Title, SortTitle(t.Title), t.Year, OriginRequest), &created)
	return out, created, err
}

// UnfollowTitle ends a follow once no request follows the title any more: it
// is followed no longer, and its monitoring is what it was before the follow
// began — a title a request created is not monitored any more, nothing of it
// hunted and TMDB not read for it; one an admin had stopped monitoring is
// stopped again. Monitoring an admin turned off while the follow lasted stays
// off.
func (s *Store) UnfollowTitle(ctx context.Context, titleID string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE titles t
		   SET unfollowed_at = now(),
		       monitored = t.monitored AND COALESCE(t.pre_monitored, t.origin <> $2),
		       monitor_new = t.monitor_new AND COALESCE(t.pre_monitor_new, t.origin <> $2),
		       pre_monitored = NULL, pre_monitor_new = NULL, updated_at = now()
		 WHERE t.id = $1 AND `+followedSQL+`
		   AND NOT EXISTS (SELECT 1 FROM wanted_items w WHERE w.title_id = t.id)`, titleID, OriginRequest)
	return err
}

// SortTitle is how a title a request created sorts: its name in lower case,
// without a leading article.
func SortTitle(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	for _, a := range []string{"the ", "a ", "an "} {
		if rest, ok := strings.CutPrefix(s, a); ok && rest != "" {
			return rest
		}
	}
	return s
}

// ── reading TMDB ───────────────────────────────────────────────────────────

// EpisodeRead is one episode as a read of TMDB lists it.
type EpisodeRead struct {
	Season, Episode int
	Title           string
	AirDate         *time.Time
}

// SeriesRead is what one read of TMDB says of a series.
type SeriesRead struct {
	TVDBID  int64
	IMDBID  string
	Name    string
	Year    int
	Status  string // TMDB's: Returning Series, Ended …
	Running bool   // the status says episodes are still to come
	Aliases []string
	// Countries are the countries it comes from.
	Countries []string
	// Seasons are every season TMDB lists, season 1 on, with its episode
	// count; Read are the seasons whose episodes were read this time, and
	// Episodes those episodes.
	Seasons  map[int]int
	Read     []int
	Episodes []EpisodeRead
	// MonitorAll is set for a title a request follows that is read for the
	// first time: every target the read adds is monitored. Otherwise a new
	// season is monitored when its title monitors new seasons, and a new
	// episode as its season is.
	MonitorAll bool
}

// InventoryChange is what applying a read of TMDB changed.
type InventoryChange struct {
	Added      int `json:"added"`      // targets new
	AddedAired int `json:"addedAired"` // of them, aired already
	Updated    int `json:"updated"`    // an air date or an episode's title changed
	Removed    int `json:"removed"`    // TMDB no longer lists them: removed
	Kept       int `json:"kept"`       // TMDB no longer lists them, kept: held, or a grab under way
}

// tmdbAliasSource is where the titles TMDB knows a title by come from.
const tmdbAliasSource = "tmdb"

// ApplySeriesInventory makes a series' targets what a read of TMDB says, in
// one transaction: its TVDB and IMDb ids where it had none (an id the import
// resolved is never replaced), TMDB's status, the titles TMDB knows it by as
// aliases, its seasons, and one target per episode of the seasons read — new
// ones added, the air dates and titles of the others updated. Monitoring and
// state are never touched: a read of TMDB is no decision of anyone's. An
// episode TMDB no longer lists — in a season read, or of a season TMDB no
// longer lists — is removed when nothing holds it: one the library holds, or
// a grab is under way for, is kept. A title a request created takes TMDB's
// name and year; one the import tracks keeps its own, and TMDB's name is one
// of its aliases.
func (s *Store) ApplySeriesInventory(ctx context.Context, titleID string, r SeriesRead) (InventoryChange, error) {
	var ch InventoryChange
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ch, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var monitorNew bool
	var grace int
	var name, origin string
	err = tx.QueryRow(ctx, `SELECT monitor_new, air_grace_hours, title, origin FROM titles WHERE id = $1 FOR UPDATE`, titleID).
		Scan(&monitorNew, &grace, &name, &origin)
	if errors.Is(err, pgx.ErrNoRows) {
		return ch, ErrNotFound
	}
	if err != nil {
		return ch, err
	}
	status := "ended"
	if r.Running {
		status = "continuing"
	}
	if err := applyTitleRead(ctx, tx, titleID, r.TVDBID, r.IMDBID, r.Status, r.Name, r.Year, status, r.Countries); err != nil {
		return ch, err
	}
	if err := replaceAliasesTx(ctx, tx, titleID, aliasesOf(name, origin, r.Name, r.Aliases), tmdbAliasSource); err != nil {
		return ch, err
	}
	// The first read of a series is its inventory, monitored as a derivation
	// monitors it; a season that comes later is a new season.
	var known int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM seasons WHERE title_id = $1`, titleID).Scan(&known); err != nil {
		return ch, err
	}
	monitorAll := r.MonitorAll || known == 0

	seasons := make([]int32, 0, len(r.Seasons))
	counts := make([]int32, 0, len(r.Seasons))
	for n, c := range r.Seasons {
		if n >= 1 {
			seasons, counts = append(seasons, int32(n)), append(counts, int32(c))
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO seasons (title_id, season_number, episode_count, monitored)
		SELECT $1, x.s, x.c, $4 FROM unnest($2::int[], $3::int[]) AS x(s, c)
		ON CONFLICT (title_id, season_number) DO UPDATE SET episode_count = EXCLUDED.episode_count`,
		titleID, seasons, counts, monitorAll || monitorNew); err != nil {
		return ch, fmt.Errorf("seasons of %s: %w", titleID, err)
	}

	// The episodes read, each once.
	seen := map[[2]int]bool{}
	ids, names := []string{}, []string{}
	es, en := []int32{}, []int32{}
	airs := []*time.Time{}
	for _, e := range r.Episodes {
		key := [2]int{e.Season, e.Episode}
		if e.Season < 1 || e.Episode < 1 || seen[key] {
			continue
		}
		seen[key] = true
		season, ep := e.Season, e.Episode
		ids = append(ids, TargetID(titleID, &season, &ep))
		es, en = append(es, int32(e.Season)), append(en, int32(e.Episode))
		names, airs = append(names, clipText(strings.TrimSpace(e.Title))), append(airs, e.AirDate)
	}
	rows, err := tx.Query(ctx, `
		WITH e AS (
		  SELECT * FROM unnest($2::text[], $3::int[], $4::int[], $5::text[], $6::timestamptz[]) AS e(id, season, episode, name, air))
		INSERT INTO acquisition_targets AS a (id, title_id, kind, season_number, episode_number, air_date,
		                                      air_window_opens_at, episode_title, monitored, state, added_at)
		SELECT e.id, $1, 'episode', e.season, e.episode, (e.air AT TIME ZONE 'UTC')::date,
		       e.air + make_interval(hours => $7), e.name, $8 OR COALESCE(s.monitored, true), 'wanted', now()
		  FROM e LEFT JOIN seasons s ON s.title_id = $1 AND s.season_number = e.season
		ON CONFLICT (title_id, season_number, episode_number) DO UPDATE
		   SET air_date = EXCLUDED.air_date, air_window_opens_at = EXCLUDED.air_window_opens_at,
		       episode_title = EXCLUDED.episode_title, updated_at = now()
		 WHERE a.air_date IS DISTINCT FROM EXCLUDED.air_date OR a.episode_title <> EXCLUDED.episode_title
		    OR a.air_window_opens_at IS DISTINCT FROM EXCLUDED.air_window_opens_at
		RETURNING (a.xmax = 0), COALESCE(a.air_window_opens_at <= now(), false)`,
		titleID, nonNil(ids), es, en, nonNil(names), airs, grace, monitorAll)
	if err != nil {
		return ch, fmt.Errorf("targets of %s: %w", titleID, err)
	}
	for rows.Next() {
		var inserted, aired bool
		if err := rows.Scan(&inserted, &aired); err != nil {
			rows.Close()
			return ch, err
		}
		switch {
		case inserted:
			ch.Added++
			if aired {
				ch.AddedAired++
			}
		default:
			ch.Updated++
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return ch, err
	}

	// What TMDB no longer lists. Without a season listed at all nothing is
	// taken for gone: an answer that short is a fault, not the series.
	if len(seasons) > 0 {
		read := make([]int32, 0, len(r.Read))
		for _, n := range r.Read {
			read = append(read, int32(n))
		}
		gone := `a.title_id = $1 AND a.kind = 'episode' AND a.season_number >= 1
		   AND ((a.season_number = ANY($2::int[]) AND NOT EXISTS (
		          SELECT 1 FROM unnest($3::int[], $4::int[]) AS e(s, n) WHERE e.s = a.season_number AND e.n = a.episode_number))
		        OR NOT (a.season_number = ANY($5::int[])))`
		var all int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM acquisition_targets a WHERE `+gone,
			titleID, read, es, en, seasons).Scan(&all); err != nil {
			return ch, err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM acquisition_targets a WHERE `+gone+`
		   AND a.state IN ('wanted','failed') AND a.reserved_by = ''`, titleID, read, es, en, seasons)
		if err != nil {
			return ch, err
		}
		ch.Removed = int(tag.RowsAffected())
		ch.Kept = all - ch.Removed
		if _, err := tx.Exec(ctx, `
			DELETE FROM seasons s WHERE s.title_id = $1 AND s.season_number >= 1 AND NOT (s.season_number = ANY($2::int[]))
			   AND NOT EXISTS (SELECT 1 FROM acquisition_targets a WHERE a.title_id = s.title_id AND a.season_number = s.season_number)`,
			titleID, seasons); err != nil {
			return ch, err
		}
	}
	return ch, tx.Commit(ctx)
}

// MovieRead is what one read of TMDB says of a movie.
type MovieRead struct {
	IMDBID    string
	Name      string
	Year      int
	Status    string // TMDB's: Released, Post Production …
	Aliases   []string
	Countries []string
	// Window is when the movie can have a release — its first release to
	// watch at home, or one set from its theatrical release (Kind says
	// which) —, nil while TMDB dates none. Released is the day it came out
	// as Kind says — at home, or in cinemas for one whose window is set from
	// that —: a release published before it cannot be of the movie.
	Window   *time.Time
	Released *time.Time
	Kind     string
}

// ApplyMovieInventory records what a read of TMDB says of a movie a request
// follows: its IMDb id where it had none, TMDB's status, its other titles as
// aliases, and its one target — wanted, with its release as the window it is
// searched from and the day it came out as its air date, or waiting for one
// while TMDB dates none. A movie's target the import made (released, by the
// import's word) keeps the window it has.
func (s *Store) ApplyMovieInventory(ctx context.Context, titleID string, r MovieRead) (Target, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Target{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var name, origin string
	err = tx.QueryRow(ctx, `SELECT title, origin FROM titles WHERE id = $1 FOR UPDATE`, titleID).Scan(&name, &origin)
	if errors.Is(err, pgx.ErrNoRows) {
		return Target{}, ErrNotFound
	}
	if err != nil {
		return Target{}, err
	}
	status := ""
	if strings.EqualFold(r.Status, "Released") {
		status = "released"
	}
	if err := applyTitleRead(ctx, tx, titleID, 0, r.IMDBID, r.Status, r.Name, r.Year, status, r.Countries); err != nil {
		return Target{}, err
	}
	if err := replaceAliasesTx(ctx, tx, titleID, aliasesOf(name, origin, r.Name, r.Aliases), tmdbAliasSource); err != nil {
		return Target{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO acquisition_targets AS a (id, title_id, kind, monitored, state, added_at, air_date,
		                                      air_window_opens_at, release_pending, release_kind)
		SELECT $2, t.id, 'movie', t.monitored, 'wanted', now(), (COALESCE($5::timestamptz, $3::timestamptz) AT TIME ZONE 'UTC')::date,
		       $3::timestamptz, $3::timestamptz IS NULL, $4
		  FROM titles t WHERE t.id = $1
		ON CONFLICT (title_id, season_number, episode_number) DO UPDATE
		   SET air_date = EXCLUDED.air_date, air_window_opens_at = EXCLUDED.air_window_opens_at,
		       release_pending = EXCLUDED.release_pending, release_kind = EXCLUDED.release_kind, updated_at = now()
		 WHERE a.added_at IS NOT NULL`,
		titleID, TargetID(titleID, nil, nil), r.Window, r.Kind, r.Released); err != nil {
		return Target{}, fmt.Errorf("the target of %s: %w", titleID, err)
	}
	t, err := scanTarget(tx.QueryRow(ctx, `SELECT `+targetCols+` FROM acquisition_targets a
		 WHERE a.title_id = $1 AND a.season_number IS NULL AND a.episode_number IS NULL`, titleID))
	if err != nil {
		return Target{}, err
	}
	return t, tx.Commit(ctx)
}

// applyTitleRead records on a title what a read of TMDB says of it. status
// is acquire's own word for a title a request created (continuing, ended,
// released); the import's word stays on one it tracks.
func applyTitleRead(ctx context.Context, tx pgx.Tx, titleID string, tvdb int64, imdb, tmdbStatus, name string, year int, status string, countries []string) error {
	_, err := tx.Exec(ctx, `
		UPDATE titles SET
		  tvdb_id = COALESCE(tvdb_id, NULLIF($2::bigint, 0)),
		  imdb_id = CASE WHEN imdb_id = '' THEN $3 ELSE imdb_id END,
		  tmdb_status = $4,
		  title = CASE WHEN origin = $9 AND $5 <> '' THEN $5 ELSE title END,
		  sort_title = CASE WHEN origin = $9 AND $5 <> '' THEN $6 ELSE sort_title END,
		  year = CASE WHEN origin = $9 AND $7::int > 0 THEN $7::int ELSE year END,
		  status = CASE WHEN origin = $9 THEN $8 ELSE status END,
		  origin_country = $10,
		  inventory_at = now(), inventory_error = '', updated_at = now()
		 WHERE id = $1`,
		titleID, tvdb, strings.TrimSpace(imdb), tmdbStatus, strings.TrimSpace(name), SortTitle(name), year, status, OriginRequest,
		nonNil(countries))
	return err
}

// aliasesOf are the aliases a read of TMDB gives a title: TMDB's other
// titles, and — for a title the import tracks under a name of its own —
// TMDB's name, never the title's own name.
func aliasesOf(stored, origin, tmdbName string, other []string) []string {
	own := strings.ToLower(strings.TrimSpace(stored))
	if origin == OriginRequest && strings.TrimSpace(tmdbName) != "" {
		own = strings.ToLower(strings.TrimSpace(tmdbName))
	}
	seen := map[string]bool{own: true}
	var out []string
	for _, a := range append([]string{tmdbName}, other...) {
		a = strings.TrimSpace(a)
		if k := strings.ToLower(a); a != "" && !seen[k] {
			seen[k] = true
			out = append(out, a)
		}
	}
	return out
}

// replaceAliasesTx is ReplaceAliases inside a transaction.
func replaceAliasesTx(ctx context.Context, tx pgx.Tx, titleID string, aliases []string, source string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM title_aliases WHERE title_id = $1 AND source = $2`, titleID, source); err != nil {
		return err
	}
	if len(aliases) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO title_aliases (title_id, alias, source)
		SELECT $1, x, $3 FROM unnest($2::text[]) AS x
		ON CONFLICT (title_id, alias) DO NOTHING`, titleID, aliases, source)
	return err
}

// InventoryFailed records why a read of TMDB for a title failed. read is set
// when the title itself was the fault — TMDB does not know it —: it counts as
// read, and is due again only after its interval, not on the next run.
func (s *Store) InventoryFailed(ctx context.Context, titleID, reason string, read bool) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE titles SET inventory_error = $2, inventory_at = CASE WHEN $3 THEN now() ELSE inventory_at END, updated_at = now()
		 WHERE id = $1`, titleID, clipText(reason), read)
	return err
}

// DueTitle is a title whose read of TMDB is due, and whether it runs.
type DueTitle struct {
	Title   Title
	Running bool
}

// runningSQL is "this series still runs": TMDB's status says episodes are to
// come (tmdb.Running), or — before TMDB was read for it — the import's says it
// continues, or an episode aired in the last 60 days or airs in the next 14.
const runningSQL = `(t.tmdb_status IN ('Returning Series', 'In Production', 'Planned')
	OR (t.tmdb_status = '' AND t.status IN ('continuing', 'upcoming'))
	OR EXISTS (SELECT 1 FROM acquisition_targets x WHERE x.title_id = t.id AND x.kind = 'episode'
	           AND x.air_date BETWEEN (now() AT TIME ZONE 'UTC')::date - 60 AND (now() AT TIME ZONE 'UTC')::date + 14))`

// SeriesInventoryDue are the monitored series whose read of TMDB is due: one
// never read, one that runs and was read longer ago than running, any other
// read longer ago than ended. A followed series never read comes first, then
// the ones that run, then the ones read longest ago.
func (s *Store) SeriesInventoryDue(ctx context.Context, running, ended time.Duration, limit int) ([]DueTitle, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+titleCols+`, r.running
		  FROM titles t
		  CROSS JOIN LATERAL (SELECT `+runningSQL+` AS running) r
		 WHERE t.kind = 'series' AND t.monitored
		   AND (t.inventory_at IS NULL
		        OR t.inventory_at < now() - make_interval(secs => CASE WHEN r.running THEN $1::float8 ELSE $2::float8 END))
		 ORDER BY (`+followedSQL+` AND t.inventory_at IS NULL) DESC, r.running DESC, t.inventory_at NULLS FIRST, t.id
		 LIMIT $3`, running.Seconds(), ended.Seconds(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DueTitle
	for rows.Next() {
		var d DueTitle
		if d.Title, err = scanTitle(rows, &d.Running); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// MovieInventoryDue are the requested movies whose read of TMDB is due: one
// never read, and one that waits for its release — TMDB dates none yet, a
// date to come, which TMDB may move, or only its theatrical release, which
// the window is a guess from until TMDB dates a release to watch at home —
// read longer ago than every.
func (s *Store) MovieInventoryDue(ctx context.Context, every time.Duration, limit int) ([]Title, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+titleCols+`
		  FROM titles t
		  LEFT JOIN acquisition_targets a ON a.title_id = t.id AND a.season_number IS NULL AND a.episode_number IS NULL
		 WHERE t.kind = 'movie' AND t.monitored AND `+followedSQL+`
		   AND (a.id IS NULL OR t.inventory_at IS NULL
		        OR (a.state <> 'held' AND a.added_at IS NOT NULL
		            AND (a.release_pending OR a.air_window_opens_at > now() OR a.release_kind = 'theatrical')))
		   AND (t.inventory_at IS NULL OR t.inventory_at < now() - make_interval(secs => $1::float8))
		 ORDER BY t.inventory_at NULLS FIRST, t.id
		 LIMIT $2`, every.Seconds(), limit)
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

// SeasonsToRead says which seasons of a series a read of TMDB looks at again,
// of the seasons TMDB lists (listed, with their episode counts): every one
// when never read; else one acquire does not know, one whose episode count
// changed, the last one, and one with an episode that aired in the last 60
// days, airs in the next 14 or has no air date yet.
func (s *Store) SeasonsToRead(ctx context.Context, titleID string, listed map[int]int, full bool) ([]int, error) {
	var out []int
	if full {
		for n := range listed {
			out = append(out, n)
		}
		sort.Ints(out)
		return out, nil
	}
	stored := map[int]int{}
	rows, err := s.pool.Query(ctx, `SELECT season_number, episode_count FROM seasons WHERE title_id = $1`, titleID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var n, c int
		if err := rows.Scan(&n, &c); err != nil {
			rows.Close()
			return nil, err
		}
		stored[n] = c
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	watch := map[int]bool{}
	rows, err = s.pool.Query(ctx, `
		SELECT DISTINCT season_number FROM acquisition_targets
		 WHERE title_id = $1 AND kind = 'episode'
		   AND (air_date IS NULL OR air_date BETWEEN (now() AT TIME ZONE 'UTC')::date - 60 AND (now() AT TIME ZONE 'UTC')::date + 14)`, titleID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return nil, err
		}
		watch[n] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	last := 0
	for n := range listed {
		last = max(last, n)
	}
	for n, count := range listed {
		if c, known := stored[n]; !known || c != count || n == last || watch[n] {
			out = append(out, n)
		}
	}
	sort.Ints(out)
	return out, nil
}

// ── what a request shows ───────────────────────────────────────────────────

// Progress is how far acquire is with a title a request follows.
type Progress struct {
	TitleID       string     `json:"-"`
	Kind          string     `json:"kind"`
	TMDBStatus    string     `json:"tmdbStatus,omitempty"`
	Running       bool       `json:"running"`
	InventoryAt   *time.Time `json:"inventoryAt"`
	InventoryErr  string     `json:"inventoryError,omitempty"`
	HoldingsKnown bool       `json:"holdingsKnown"`
	// Of a series' monitored episodes: all of them, the ones the library
	// holds (and plays), on their way, missing, whose last grab failed, and
	// not aired yet.
	Episodes int `json:"episodes"`
	Held     int `json:"held"`
	Playable int `json:"playable"`
	OnTheWay int `json:"onTheWay"`
	Missing  int `json:"missing"`
	Failed   int `json:"failed"`
	Unaired  int `json:"unaired"`
	// Next is the next episode to air, of a series.
	Next *NextEpisode `json:"next,omitempty"`
	// Movie is a movie's one target.
	Movie *Target `json:"movie,omitempty"`
}

// NextEpisode is the next episode of a series to air.
type NextEpisode struct {
	Season  int       `json:"season"`
	Episode int       `json:"episode"`
	Title   string    `json:"title,omitempty"`
	AirDate time.Time `json:"airDate"`
}

// TitleProgress reads the progress of the titles named, by id, in one query:
// what a request that follows one of them shows.
func (s *Store) TitleProgress(ctx context.Context, ids []string) (map[string]Progress, error) {
	out := map[string]Progress{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT t.id, t.kind, t.tmdb_status, `+runningSQL+`, t.inventory_at, t.inventory_error, t.holdings_at IS NOT NULL,
		       count(a.id) FILTER (WHERE a.monitored),
		       count(*) FILTER (WHERE a.monitored AND a.state = 'held'),
		       count(*) FILTER (WHERE a.monitored AND a.state = 'held' AND a.held_playable),
		       count(*) FILTER (WHERE `+missingSQL+` AND a.state IN ('grabbed','downloaded','waiting')),
		       count(*) FILTER (WHERE `+missingSQL+` AND a.state IN ('wanted','failed')),
		       count(*) FILTER (WHERE `+missingSQL+` AND a.state = 'failed'),
		       count(*) FILTER (WHERE a.monitored AND a.state <> 'held' AND NOT `+airedSQL+`),
		       n.season_number, n.episode_number, n.episode_title, n.air_window_opens_at
		  FROM titles t
		  LEFT JOIN acquisition_targets a ON a.title_id = t.id
		  LEFT JOIN LATERAL (
		    SELECT x.season_number, x.episode_number, x.episode_title, x.air_window_opens_at FROM acquisition_targets x
		     WHERE x.title_id = t.id AND x.kind = 'episode' AND x.monitored AND x.air_window_opens_at > now()
		     ORDER BY x.air_window_opens_at, x.season_number, x.episode_number LIMIT 1) n ON true
		 WHERE t.id = ANY($1)
		 GROUP BY t.id, t.kind, t.tmdb_status, t.status, t.inventory_at, t.inventory_error, t.holdings_at,
		          n.season_number, n.episode_number, n.episode_title, n.air_window_opens_at`, nonNil(ids))
	if err != nil {
		return nil, err
	}
	var movies []string
	for rows.Next() {
		var p Progress
		var season, episode *int
		var title *string
		var at *time.Time
		if err := rows.Scan(&p.TitleID, &p.Kind, &p.TMDBStatus, &p.Running, &p.InventoryAt, &p.InventoryErr, &p.HoldingsKnown,
			&p.Episodes, &p.Held, &p.Playable, &p.OnTheWay, &p.Missing, &p.Failed, &p.Unaired,
			&season, &episode, &title, &at); err != nil {
			rows.Close()
			return nil, err
		}
		if season != nil && episode != nil && at != nil {
			p.Next = &NextEpisode{Season: *season, Episode: *episode, AirDate: *at}
			if title != nil {
				p.Next.Title = *title
			}
		}
		if p.Kind == "movie" {
			movies = append(movies, p.TitleID)
		}
		out[p.TitleID] = p
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(movies) > 0 {
		rows, err := s.pool.Query(ctx, `SELECT `+targetCols+` FROM acquisition_targets a
			 WHERE a.title_id = ANY($1) AND a.season_number IS NULL AND a.episode_number IS NULL`, movies)
		if err != nil {
			return nil, err
		}
		targets, err := scanTargets(rows)
		rows.Close()
		if err != nil {
			return nil, err
		}
		for i := range targets {
			p := out[targets[i].TitleID]
			p.Movie = &targets[i]
			out[targets[i].TitleID] = p
		}
	}
	return out, nil
}

// ── the sources' newest releases ───────────────────────────────────────────

// FeedsWant is what the sources' newest releases may be grabbed for: every
// target (All) or the new things (aired in the last NewWithinDays days), none
// before AirDelay has passed since its air date.
type FeedsWant struct {
	All           bool
	NewWithinDays int
	AirDelay      time.Duration
}

// WantedForFeeds counts the targets a release in a source's newest releases
// could be grabbed for now, episodes and movies: wanted, aired and past the
// air delay, monitored with their title, of holdings acquire knows, not
// reserved — every one, or only the new things (newSQL).
func (s *Store) WantedForFeeds(ctx context.Context, w FeedsWant) (episodes, movies int, err error) {
	err = s.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE a.kind = 'episode'), count(*) FILTER (WHERE a.kind = 'movie')
		  FROM acquisition_targets a JOIN titles t ON t.id = a.title_id
		 WHERE `+sweepableSQL("$3")+` AND ($1 OR `+newSQL("$2")+`)`, w.All, w.NewWithinDays, w.AirDelay.Seconds()).Scan(&episodes, &movies)
	return episodes, movies, err
}

// What rss-sync keeps of a feed it looked at: the last RSSSeenMax releases of
// the last RSSSeenFor, by the source's clock.
const (
	RSSSeenMax = 1000
	RSSSeenFor = 7 * 24 * time.Hour
)

// RSSCursor is what rss-sync has looked at of one feed of a source: the
// releases, by an id of each (RSSID), with when the source says it published
// each — in the source's own clock, which may run ahead of acquire's or date a
// release indexed late long before. A release is looked at once, whenever the
// source lists it, however it dates it.
type RSSCursor struct {
	IndexerID int64
	Feed      string // tv | movie | search
	Seen      map[int64]int64
	ReadAt    *time.Time
	Detail    string
}

// RSSID is the id a feed's release is kept by: a 64-bit hash of its guid,
// else of its name (the caller's key).
func RSSID(key string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	return int64(h.Sum64())
}

// Has reports whether a release was looked at before.
func (c RSSCursor) Has(key string) bool {
	_, ok := c.Seen[RSSID(key)]
	return ok
}

// Add records that a release was looked at, published at as the source dates
// it.
func (c *RSSCursor) Add(key string, at time.Time) {
	if c.Seen == nil {
		c.Seen = map[int64]int64{}
	}
	c.Seen[RSSID(key)] = at.Unix()
}

// Read reports whether the feed was read before.
func (c RSSCursor) Read() bool { return c.ReadAt != nil || len(c.Seen) > 0 }

// kept are the releases the cursor keeps: the last RSSSeenMax of the last
// RSSSeenFor before the newest, by the source's clock; newest first.
func (c RSSCursor) kept() (ids, at []int64) {
	type entry struct{ id, at int64 }
	all := make([]entry, 0, len(c.Seen))
	var newest int64
	for id, t := range c.Seen {
		all = append(all, entry{id, t})
		newest = max(newest, t)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].at != all[j].at {
			return all[i].at > all[j].at
		}
		return all[i].id < all[j].id
	})
	since := newest - int64(RSSSeenFor.Seconds())
	for _, e := range all {
		if e.at < since || len(ids) == RSSSeenMax {
			break
		}
		ids, at = append(ids, e.id), append(at, e.at)
	}
	return nonNilInt64(ids), nonNilInt64(at)
}

func nonNilInt64(v []int64) []int64 {
	if v == nil {
		return []int64{}
	}
	return v
}

// GetRSSCursor reads a feed's cursor; one never read is the zero cursor.
func (s *Store) GetRSSCursor(ctx context.Context, indexerID int64, feed string) (RSSCursor, error) {
	c := RSSCursor{IndexerID: indexerID, Feed: feed, Seen: map[int64]int64{}}
	var ids, at []int64
	err := s.pool.QueryRow(ctx, `
		SELECT seen_ids, seen_at, read_at, detail FROM rss_cursors WHERE indexer_id = $1 AND feed = $2`,
		indexerID, feed).Scan(&ids, &at, &c.ReadAt, &c.Detail)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	for i := range ids {
		if i < len(at) {
			c.Seen[ids[i]] = at[i]
		}
	}
	return c, nil
}

// SaveRSSCursor records what was looked at of a feed — the releases it keeps
// (kept) — and when it was read.
func (s *Store) SaveRSSCursor(ctx context.Context, c RSSCursor) error {
	ids, at := c.kept()
	_, err := s.pool.Exec(ctx, `
		INSERT INTO rss_cursors (indexer_id, feed, seen_ids, seen_at, read_at, detail)
		VALUES ($1, $2, $3, $4, now(), $5)
		ON CONFLICT (indexer_id, feed) DO UPDATE
		   SET seen_ids = EXCLUDED.seen_ids, seen_at = EXCLUDED.seen_at,
		       read_at = EXCLUDED.read_at, detail = EXCLUDED.detail`,
		c.IndexerID, c.Feed, ids, at, clipText(c.Detail))
	return err
}
