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

// validTitle checks what a request says of a title, and names it.
func validTitle(t Title) (Title, error) {
	if t.TMDBID <= 0 {
		return t, fmt.Errorf("title %q has no tmdb id", t.Title)
	}
	if t.Kind != "series" && t.Kind != "movie" {
		return t, fmt.Errorf("title %q is neither a series nor a movie", t.Title)
	}
	if t.ID == "" {
		t.ID = TitleID(t.Kind, t.TMDBID)
	}
	return t, nil
}

// RequestTitle is the title a request awaiting an admin's approval asks for:
// the one acquire tracks, untouched, or — when it tracks none — one created
// from what the request says of it, origin request, monitored by nobody and
// followed by nobody until a request for it is approved (FollowTitle).
// created reports whether the title is new.
func (s *Store) RequestTitle(ctx context.Context, t Title) (Title, bool, error) {
	t, err := validTitle(t)
	if err != nil {
		return Title{}, false, err
	}
	var created bool
	err = s.pool.QueryRow(ctx, `
		INSERT INTO titles (id, tmdb_id, kind, title, sort_title, year, monitored, monitor_new, origin)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6::int, 0), false, false, $7)
		ON CONFLICT (tmdb_id, kind) DO NOTHING
		RETURNING true`,
		t.ID, t.TMDBID, t.Kind, t.Title, SortTitle(t.Title), t.Year, OriginRequest).Scan(&created)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Title{}, false, err
	}
	out, err := scanTitle(s.pool.QueryRow(ctx, `SELECT `+titleCols+` FROM titles t WHERE t.tmdb_id = $1 AND t.kind = $2`, t.TMDBID, t.Kind))
	return out, created, err
}

// FollowTitle records that an approved request follows a title. A title
// acquire does not track yet is created from what the request says of it —
// monitored, origin request; TMDB fills in the rest when the title is first
// read. One acquire tracks already is not made twice: it is monitored, with
// the seasons to come, while the follow lasts — what its monitoring was
// before is kept, and given back when the follow ends (UnfollowTitle) —, and
// it is followed from the first follow on, even one ended since. What a
// follow before monitored of its seasons and episodes is monitored again; a
// movie's one target is monitored, the follow's. created reports whether the
// title is new.
func (s *Store) FollowTitle(ctx context.Context, t Title) (Title, bool, error) {
	t, err := validTitle(t)
	if err != nil {
		return Title{}, false, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Title{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var created bool
	out, err := scanTitle(tx.QueryRow(ctx, `
		INSERT INTO titles AS t (id, tmdb_id, kind, title, sort_title, year, monitored, monitor_new, followed_at, origin)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6::int, 0), true, true, now(), $7)
		ON CONFLICT (tmdb_id, kind) DO UPDATE
		   SET pre_monitored = CASE WHEN `+followedSQL+` THEN t.pre_monitored ELSE t.monitored END,
		       pre_monitor_new = CASE WHEN `+followedSQL+` THEN t.pre_monitor_new ELSE t.monitor_new END,
		       monitored = true, monitor_new = true,
		       followed_at = COALESCE(t.followed_at, now()), unfollowed_at = NULL, updated_at = now()
		RETURNING `+titleCols+`, (t.xmax = 0)`,
		t.ID, t.TMDBID, t.Kind, t.Title, SortTitle(t.Title), t.Year, OriginRequest), &created)
	if err != nil {
		return Title{}, false, err
	}
	for _, q := range []string{
		`UPDATE seasons SET monitored = true WHERE title_id = $1 AND follow_monitored AND NOT monitored`,
		`UPDATE acquisition_targets SET monitored = true, updated_at = now() WHERE title_id = $1 AND follow_monitored AND NOT monitored`,
		`UPDATE acquisition_targets SET monitored = true, follow_monitored = true, updated_at = now()
		  WHERE title_id = $1 AND kind = 'movie' AND NOT monitored`,
	} {
		if _, err := tx.Exec(ctx, q, out.ID); err != nil {
			return Title{}, false, err
		}
	}
	return out, created, tx.Commit(ctx)
}

// UnfollowTitle ends a follow once no approved request follows the title any
// more: it is followed no longer, and its monitoring is what it was before
// the follow began — a title a request created is not monitored any more,
// nothing of it hunted and TMDB not read for it; one an admin had stopped
// monitoring is stopped again. Monitoring an admin turned off while the
// follow lasted stays off. What the follow monitored of its seasons and
// episodes is unmonitored.
func (s *Store) UnfollowTitle(ctx context.Context, titleID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `
		UPDATE titles t
		   SET unfollowed_at = now(),
		       monitored = t.monitored AND COALESCE(t.pre_monitored, t.origin <> $2),
		       monitor_new = t.monitor_new AND COALESCE(t.pre_monitor_new, t.origin <> $2),
		       pre_monitored = NULL, pre_monitor_new = NULL, updated_at = now()
		 WHERE t.id = $1 AND `+followedSQL+`
		   AND NOT EXISTS (SELECT 1 FROM wanted_items w WHERE w.title_id = t.id AND w.approval = $3)`,
		titleID, OriginRequest, ApprovalApproved)
	if err != nil || tag.RowsAffected() == 0 {
		return err
	}
	for _, q := range []string{
		`UPDATE seasons SET monitored = false WHERE title_id = $1 AND follow_monitored`,
		`UPDATE acquisition_targets SET monitored = false, updated_at = now() WHERE title_id = $1 AND follow_monitored`,
	} {
		if _, err := tx.Exec(ctx, q, titleID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
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

// EpisodeRead is one episode as a read of TMDB lists it: TMDB's id of it
// (0 when unknown), its numbers, its title and its air date.
type EpisodeRead struct {
	TMDBID          int64
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
	// Countries are the countries it comes from; Genres TMDB's genre ids of
	// it, and Type its kind of show.
	Countries []string
	Genres    []int
	Type      string
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
	// TMDB no longer lists them: Gone for the first time (kept, grabbed by
	// nobody); Removed as a read a day or more later agrees; Kept as they
	// hold something — the library's file, a grab, what grabs did.
	Gone    int `json:"gone"`
	Removed int `json:"removed"`
	Kept    int `json:"kept"`
	// Renumbered are episodes TMDB numbers anew whose holding went with
	// them to their new number.
	Renumbered int `json:"renumbered"`
}

// tmdbAliasSource is where the titles TMDB knows a title by come from.
const tmdbAliasSource = "tmdb"

// ApplySeriesInventory makes a series' targets what a read of TMDB says, in
// one transaction: its TVDB and IMDb ids where it had none (an id the import
// resolved is never replaced), TMDB's status, the titles TMDB knows it by as
// aliases, its seasons, and one target per episode of the seasons read — new
// ones added, the air dates and titles of the others updated. Monitoring and
// state are never touched: a read of TMDB is no decision of anyone's — but an
// episode TMDB numbers anew (its TMDB id under other numbers) that the library
// holds is held at its new number too, never wanted again.
//
// An episode TMDB no longer lists — in a season read, or of a season TMDB no
// longer lists — is gone: marked so the first time, and grabbed by nobody; it
// is removed once a read a day or more later agrees — an answer of TMDB's
// that lists a season short is a fault, not the series — unless it holds
// anything: the library's file, a grab under way, what its grabs did. A title
// a request created takes TMDB's name and year; one the import tracks keeps
// its own, and TMDB's name is one of its aliases.
func (s *Store) ApplySeriesInventory(ctx context.Context, titleID string, r SeriesRead) (InventoryChange, error) {
	var ch InventoryChange
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ch, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var monitorNew, followed bool
	var preMonitored, preMonitorNew *bool
	var grace int
	var name, origin string
	err = tx.QueryRow(ctx, `SELECT monitor_new, air_grace_hours, title, origin, followed_at IS NOT NULL AND unfollowed_at IS NULL,
		       pre_monitored, pre_monitor_new
		  FROM titles WHERE id = $1 FOR UPDATE`, titleID).
		Scan(&monitorNew, &grace, &name, &origin, &followed, &preMonitored, &preMonitorNew)
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
	if err := applyShowRead(ctx, tx, titleID, r); err != nil {
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
	// What the follow monitors is what would not be monitored without it:
	// the title's own monitoring before the follow began (natural), as a
	// derivation monitors a first read and the title its new seasons.
	natTitle := followed && preMonitored != nil && *preMonitored
	natAll := natTitle && known == 0
	natNew := preMonitorNew != nil && *preMonitorNew
	seasonFollow := followed && (monitorAll || monitorNew) && !(natTitle && (natAll || natNew))

	seasons := make([]int32, 0, len(r.Seasons))
	counts := make([]int32, 0, len(r.Seasons))
	for n, c := range r.Seasons {
		if n >= 1 {
			seasons, counts = append(seasons, int32(n)), append(counts, int32(c))
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO seasons (title_id, season_number, episode_count, monitored, follow_monitored)
		SELECT $1, x.s, x.c, $4, $5 FROM unnest($2::int[], $3::int[]) AS x(s, c)
		ON CONFLICT (title_id, season_number) DO UPDATE SET episode_count = EXCLUDED.episode_count`,
		titleID, seasons, counts, monitorAll || monitorNew, seasonFollow); err != nil {
		return ch, fmt.Errorf("seasons of %s: %w", titleID, err)
	}

	// The episodes read, each once.
	seen := map[[2]int]bool{}
	ids, names := []string{}, []string{}
	es, en := []int32{}, []int32{}
	tids := []int64{}
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
		tids = append(tids, e.TMDBID)
	}
	// What the library holds of episodes TMDB lists under other numbers now,
	// by TMDB's id of each: their holdings go with them (renumbered below).
	moves, err := heldMoves(ctx, tx, titleID, es, en, tids)
	if err != nil {
		return ch, err
	}
	// Listed again: not gone; TMDB's id of each, as it says now.
	if _, err := tx.Exec(ctx, `
		UPDATE acquisition_targets a SET tmdb_gone_at = NULL, tmdb_episode_id = COALESCE(NULLIF(e.tid, 0), a.tmdb_episode_id)
		  FROM unnest($2::int[], $3::int[], $4::bigint[]) AS e(season, episode, tid)
		 WHERE a.title_id = $1 AND a.season_number = e.season AND a.episode_number = e.episode
		   AND (a.tmdb_gone_at IS NOT NULL OR a.tmdb_episode_id IS DISTINCT FROM COALESCE(NULLIF(e.tid, 0), a.tmdb_episode_id))`,
		titleID, es, en, tids); err != nil {
		return ch, err
	}
	rows, err := tx.Query(ctx, `
		WITH e AS (
		  SELECT * FROM unnest($2::text[], $3::int[], $4::int[], $5::text[], $6::timestamptz[], $9::bigint[]) AS e(id, season, episode, name, air, tid))
		INSERT INTO acquisition_targets AS a (id, title_id, kind, season_number, episode_number, air_date,
		                                      air_window_opens_at, episode_title, monitored, state, added_at, tmdb_episode_id,
		                                      follow_monitored)
		SELECT e.id, $1, 'episode', e.season, e.episode, (e.air AT TIME ZONE 'UTC')::date,
		       e.air + make_interval(hours => $7), e.name, $8 OR COALESCE(s.monitored, true), 'wanted', now(), NULLIF(e.tid, 0),
		       -- monitored by the follow: monitored, and not without it — its
		       -- season the follow's, or every episode of a first read.
		       $10 AND ($8 OR COALESCE(s.monitored, true))
		           AND NOT ($11 AND ($12 OR (COALESCE(s.monitored, true) AND NOT COALESCE(s.follow_monitored, false))))
		  FROM e LEFT JOIN seasons s ON s.title_id = $1 AND s.season_number = e.season
		ON CONFLICT (title_id, season_number, episode_number) DO UPDATE
		   SET air_date = EXCLUDED.air_date, air_window_opens_at = EXCLUDED.air_window_opens_at,
		       episode_title = EXCLUDED.episode_title,
		       -- An air time is the source's for a day: TMDB moving the day
		       -- further than one makes it no one's, until the source is read.
		       air_stamp = CASE WHEN EXCLUDED.air_date IS NULL
		                          OR abs(EXCLUDED.air_date - (a.air_stamp AT TIME ZONE 'UTC')::date) > 1 THEN NULL
		                        ELSE a.air_stamp END,
		       updated_at = now()
		 WHERE a.air_date IS DISTINCT FROM EXCLUDED.air_date OR a.episode_title <> EXCLUDED.episode_title
		    OR a.air_window_opens_at IS DISTINCT FROM EXCLUDED.air_window_opens_at
		RETURNING (a.xmax = 0), COALESCE(a.air_window_opens_at <= now(), false)`,
		titleID, nonNil(ids), es, en, nonNil(names), airs, grace, monitorAll, tids, followed, natTitle, natAll)
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

	// An episode the library holds that TMDB numbers anew is held at its new
	// number, by the same file, never wanted again; the number it leaves holds
	// it no more — it is another episode now —, unless another holding moves
	// there.
	if ch.Renumbered, err = applyMoves(ctx, tx, titleID, moves); err != nil {
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
		tag, err := tx.Exec(ctx, `UPDATE acquisition_targets a SET tmdb_gone_at = now(), updated_at = now()
		 WHERE `+gone+` AND a.tmdb_gone_at IS NULL`, titleID, read, es, en, seasons)
		if err != nil {
			return ch, err
		}
		ch.Gone = int(tag.RowsAffected())
		var all int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM acquisition_targets a WHERE `+gone+`
		   AND a.tmdb_gone_at <= now() - interval '1 day'`, titleID, read, es, en, seasons).Scan(&all); err != nil {
			return ch, err
		}
		// Two reads a day apart agree, and it holds nothing: no file, no
		// grab under way, nothing its grabs did.
		tag, err = tx.Exec(ctx, `DELETE FROM acquisition_targets a WHERE `+gone+`
		   AND a.tmdb_gone_at <= now() - interval '1 day'
		   AND a.state IN ('wanted','failed') AND a.reserved_by = '' AND a.grab_id = '' AND NOT a.ever_held AND a.grab_failures = 0`,
			titleID, read, es, en, seasons)
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

// heldMove is a holding of an episode TMDB lists under other numbers now:
// the target it was held at, the number it moves to, and what it held.
type heldMove struct {
	fromID, as                          string
	from, to                            [2]int
	item, via, release, profile, source string
	playable                            bool
	quality                             []byte
	score                               int
	scoredAt, at                        *time.Time
}

// heldMoves reads, before a read of TMDB is applied, the holdings of the
// episodes it lists under other numbers than the targets that hold them —
// by TMDB's id of the episode, which a renumbering keeps.
func heldMoves(ctx context.Context, tx pgx.Tx, titleID string, es, en []int32, tids []int64) ([]heldMove, error) {
	rows, err := tx.Query(ctx, `
		SELECT o.id, o.season_number, o.episode_number, l.season, l.episode, o.held_item_id, o.held_via, o.held_playable,
		       o.held_release, o.held_quality, o.held_score, o.held_score_profile, o.held_source, o.held_scored_at, o.held_at, o.held_as
		  FROM acquisition_targets o
		  JOIN unnest($2::int[], $3::int[], $4::bigint[]) AS l(season, episode, tid) ON l.tid <> 0 AND l.tid = o.tmdb_episode_id
		 WHERE o.title_id = $1 AND o.kind = 'episode' AND o.state = 'held'
		   AND (l.season, l.episode) <> (o.season_number, o.episode_number)`, titleID, es, en, tids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []heldMove
	for rows.Next() {
		var m heldMove
		if err := rows.Scan(&m.fromID, &m.from[0], &m.from[1], &m.to[0], &m.to[1], &m.item, &m.via, &m.playable,
			&m.release, &m.quality, &m.score, &m.profile, &m.source, &m.scoredAt, &m.at, &m.as); err != nil {
			return nil, err
		}
		if m.as == "" {
			// The catalog knows the file by the number it was held at.
			m.as = fmt.Sprintf("S%02dE%02d", m.from[0], m.from[1])
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// applyMoves holds each moved holding at its new number, and releases the
// numbers they left that no holding moved to: those are other episodes now.
func applyMoves(ctx context.Context, tx pgx.Tx, titleID string, moves []heldMove) (int, error) {
	to := map[[2]int]bool{}
	moved := 0
	for _, m := range moves {
		to[m.to] = true
		tag, err := tx.Exec(ctx, `
			UPDATE acquisition_targets
			   SET state = 'held', ever_held = true, held_item_id = $4, held_via = $5, held_playable = $6, held_release = $7,
			       held_quality = $8, held_score = $9, held_score_profile = $10, held_source = $11, held_scored_at = $12,
			       held_at = $13, held_as = $14, detail = $15, grab_requested_at = NULL, grab_claimed_until = NULL,
			       grab_failures = 0, retry_at = NULL, updated_at = now()
			 WHERE title_id = $1 AND season_number = $2 AND episode_number = $3 AND reserved_by = ''
			   AND state IN ('wanted', 'failed', 'held')`,
			titleID, m.to[0], m.to[1], m.item, m.via, m.playable, m.release, m.quality, m.score, m.profile, m.source,
			m.scoredAt, m.at, m.as, clipText(fmt.Sprintf("in the library, as %s before TMDB numbered it anew", m.as)))
		if err != nil {
			return moved, err
		}
		moved += int(tag.RowsAffected())
	}
	for _, m := range moves {
		if to[m.from] {
			continue
		}
		// Another episode now, of which the library holds nothing: not held
		// once either.
		if _, err := tx.Exec(ctx, `
			UPDATE acquisition_targets
			   SET held_item_id = '', held_via = '', held_playable = false, held_release = '', held_as = '',
			       held_quality = '{}'::jsonb, held_score = 0, held_score_profile = '', held_source = '',
			       held_scored_at = NULL, held_at = NULL, state = 'wanted', ever_held = false,
			       detail = $3, updated_at = now()
			 WHERE id = $1 AND state = 'held' AND held_item_id = $2`, m.fromID, m.item,
			clipText(fmt.Sprintf("TMDB numbers the episode the library held here %s now: this is another episode",
				fmt.Sprintf("S%02dE%02d", m.to[0], m.to[1])))); err != nil {
			return moved, err
		}
	}
	return moved, nil
}

// ApplySeriesHead records what a read of TMDB's answer says of a series —
// its ids, names, status, countries, genres and how many seasons it lists —
// and nothing of its seasons or episodes: all a request awaiting an admin's
// approval reads of a title nobody monitors. Its episodes are made once a
// request is approved (ApplySeriesInventory).
func (s *Store) ApplySeriesHead(ctx context.Context, titleID string, r SeriesRead) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var name, origin string
	err = tx.QueryRow(ctx, `SELECT title, origin FROM titles WHERE id = $1 FOR UPDATE`, titleID).Scan(&name, &origin)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	status := "ended"
	if r.Running {
		status = "continuing"
	}
	if err := applyTitleRead(ctx, tx, titleID, r.TVDBID, r.IMDBID, r.Status, r.Name, r.Year, status, r.Countries); err != nil {
		return err
	}
	if err := applyShowRead(ctx, tx, titleID, r); err != nil {
		return err
	}
	if err := replaceAliasesTx(ctx, tx, titleID, aliasesOf(name, origin, r.Name, r.Aliases), tmdbAliasSource); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// applyShowRead records what TMDB says of a series beyond any title's: its
// genres, its kind of show and how many seasons it lists.
func applyShowRead(ctx context.Context, tx pgx.Tx, titleID string, r SeriesRead) error {
	genres := make([]int32, 0, len(r.Genres))
	for _, g := range r.Genres {
		genres = append(genres, int32(g))
	}
	listed := 0
	for n := range r.Seasons {
		if n >= 1 {
			listed++
		}
	}
	_, err := tx.Exec(ctx, `UPDATE titles SET genres = $2, tmdb_type = $3, tmdb_seasons = $4 WHERE id = $1`,
		titleID, genres, strings.TrimSpace(r.Type), listed)
	return err
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
	// Country is where its release to watch at home comes out.
	Country string
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
		                                      air_window_opens_at, release_pending, release_kind, release_country, follow_monitored)
		SELECT $2, t.id, 'movie', t.monitored, 'wanted', now(), (COALESCE($5::timestamptz, $3::timestamptz) AT TIME ZONE 'UTC')::date,
		       $3::timestamptz, $3::timestamptz IS NULL, $4, $6,
		       `+followedSQL+` AND t.monitored AND NOT COALESCE(t.pre_monitored, false)
		  FROM titles t WHERE t.id = $1
		ON CONFLICT (title_id, season_number, episode_number) DO UPDATE
		   SET air_date = EXCLUDED.air_date, air_window_opens_at = EXCLUDED.air_window_opens_at,
		       release_pending = EXCLUDED.release_pending, release_kind = EXCLUDED.release_kind,
		       release_country = EXCLUDED.release_country, updated_at = now()
		 WHERE a.added_at IS NOT NULL`,
		titleID, TargetID(titleID, nil, nil), r.Window, r.Kind, r.Released, strings.ToUpper(strings.TrimSpace(r.Country))); err != nil {
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
		  inventory_at = now(), inventory_error = '', inventory_retry_at = NULL, updated_at = now()
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

// InventoryUnknownAgain is how long a title TMDB does not know waits before
// it is read again.
const InventoryUnknownAgain = 7 * 24 * time.Hour

// InventoryFailed records why a read of TMDB for a title failed. read is set
// when the title itself was the fault — TMDB does not know it, or answers
// nothing readable —: it counts as read, and is read again only
// InventoryUnknownAgain later (inventory_retry_at), whatever its kind.
func (s *Store) InventoryFailed(ctx context.Context, titleID, reason string, read bool) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE titles SET inventory_error = $2, inventory_at = CASE WHEN $3 THEN now() ELSE inventory_at END,
		       inventory_retry_at = CASE WHEN $3 THEN now() + make_interval(secs => $4::float8) ELSE inventory_retry_at END,
		       updated_at = now()
		 WHERE id = $1`, titleID, clipText(reason), read, InventoryUnknownAgain.Seconds())
	return err
}

// inventoryDueSQL is "a read of TMDB is due for the title t" for one read
// every interval (the parameter named, in seconds): never read, read longer
// ago than that — or, of a title TMDB did not know, its week is over.
func inventoryDueSQL(every string) string {
	return `(CASE WHEN t.inventory_retry_at IS NOT NULL THEN t.inventory_retry_at <= now()
	              ELSE t.inventory_at IS NULL OR t.inventory_at < now() - make_interval(secs => ` + every + `::float8) END)`
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

// Which series SeriesInventoryDue lists: all, the ones that run, or the
// others — ended, or never read.
const (
	SeriesAll     = ""
	SeriesRunning = "running"
	SeriesOther   = "other"
)

// SeriesInventoryDue are the monitored series whose read of TMDB is due: one
// never read, one that runs and was read longer ago than running, any other
// read longer ago than ended — of which (SeriesAll, SeriesRunning,
// SeriesOther). A followed series never read comes first, then the ones that
// run, then the ones read longest ago.
func (s *Store) SeriesInventoryDue(ctx context.Context, running, ended time.Duration, limit int, which string) ([]DueTitle, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+titleCols+`, r.running
		  FROM titles t
		  CROSS JOIN LATERAL (SELECT `+runningSQL+` AS running) r
		 WHERE t.kind = 'series' AND t.monitored
		   AND `+inventoryDueSQL("CASE WHEN r.running THEN $1::float8 ELSE $2::float8 END")+`
		   AND ($4 = '' OR ($4 = 'running') = (r.running AND t.inventory_at IS NOT NULL))
		 ORDER BY (`+followedSQL+` AND t.inventory_at IS NULL) DESC, r.running DESC, t.inventory_at NULLS FIRST, t.id
		 LIMIT $3`, running.Seconds(), ended.Seconds(), limit, which)
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

// RequestedNotRead are the titles of open requests — followed, or awaiting
// an admin's approval — that TMDB could not be read for: out of reach or
// failing, read again soon, not on the next refresh hours away; and one TMDB
// did not know, once its week is over — no refresh reads one awaiting
// approval.
func (s *Store) RequestedNotRead(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT t.id FROM titles t
		 WHERE t.inventory_error <> ''
		   AND (t.inventory_at IS NULL OR t.inventory_retry_at <= now())
		   AND EXISTS (SELECT 1 FROM wanted_items w WHERE w.title_id = t.id AND w.approval <> 'declined')
		 ORDER BY t.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
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
		   AND `+inventoryDueSQL("$1")+`
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
// changed, the last one, one with an episode that aired in the last 60 days,
// airs in the next 14 or has no air date yet — and one with an episode the
// last read of it did not list, so that what TMDB dropped goes once a read a
// day later agrees, and what it lists again comes back.
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
		   AND (air_date IS NULL OR air_date BETWEEN (now() AT TIME ZONE 'UTC')::date - 60 AND (now() AT TIME ZONE 'UTC')::date + 14
		        OR tmdb_gone_at IS NOT NULL)`, titleID)
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
	TitleID    string `json:"-"`
	Kind       string `json:"kind"`
	TMDBStatus string `json:"tmdbStatus,omitempty"`
	Running    bool   `json:"running"`
	// HandsOff is a series no automatic grab takes anything of
	// (Title.HandsOff). FromRequest is a title a request created; Seasons
	// how many seasons TMDB lists of a series.
	HandsOff    bool `json:"handsOff,omitempty"`
	FromRequest bool `json:"fromRequest,omitempty"`
	Seasons     int  `json:"seasons,omitempty"`
	// EpisodesRead is set once the episodes of a series were read: not
	// while a request awaits approval, nor until its first read after.
	EpisodesRead bool       `json:"episodesRead,omitempty"`
	InventoryAt  *time.Time `json:"inventoryAt"`
	InventoryErr string     `json:"inventoryError,omitempty"`
	// InventoryRetryAt is when a title TMDB did not know is read again.
	InventoryRetryAt *time.Time `json:"inventoryRetryAt,omitempty"`
	HoldingsKnown    bool       `json:"holdingsKnown"`
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
		SELECT t.id, t.kind, t.tmdb_status, `+runningSQL+`, `+handsOffSQL+`, t.origin = 'request', t.tmdb_seasons,
		       EXISTS (SELECT 1 FROM seasons s WHERE s.title_id = t.id),
		       t.inventory_at, t.inventory_error, t.inventory_retry_at, t.holdings_at IS NOT NULL,
		       count(a.id) FILTER (WHERE a.monitored AND (a.tmdb_gone_at IS NULL OR a.state = 'held')),
		       count(*) FILTER (WHERE a.monitored AND a.state = 'held'),
		       count(*) FILTER (WHERE a.monitored AND a.state = 'held' AND a.held_playable),
		       count(*) FILTER (WHERE `+missingSQL+` AND a.state IN ('grabbed','downloaded','waiting')),
		       count(*) FILTER (WHERE `+missingSQL+` AND a.state IN ('wanted','failed')),
		       count(*) FILTER (WHERE `+missingSQL+` AND a.state = 'failed'),
		       count(*) FILTER (WHERE a.monitored AND a.state <> 'held' AND a.tmdb_gone_at IS NULL AND NOT `+airedSQL+`),
		       n.season_number, n.episode_number, n.episode_title, n.air_window_opens_at
		  FROM titles t
		  LEFT JOIN acquisition_targets a ON a.title_id = t.id
		  LEFT JOIN LATERAL (
		    SELECT x.season_number, x.episode_number, x.episode_title, x.air_window_opens_at FROM acquisition_targets x
		     WHERE x.title_id = t.id AND x.kind = 'episode' AND x.monitored AND x.tmdb_gone_at IS NULL AND x.air_window_opens_at > now()
		     ORDER BY x.air_window_opens_at, x.season_number, x.episode_number LIMIT 1) n ON true
		 WHERE t.id = ANY($1)
		 GROUP BY t.id, t.kind, t.tmdb_status, t.status, t.inventory_at, t.inventory_error, t.inventory_retry_at, t.holdings_at,
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
		if err := rows.Scan(&p.TitleID, &p.Kind, &p.TMDBStatus, &p.Running, &p.HandsOff, &p.FromRequest, &p.Seasons, &p.EpisodesRead,
			&p.InventoryAt, &p.InventoryErr, &p.InventoryRetryAt, &p.HoldingsKnown,
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
// before its window is open to an automatic grab (DueOrder's AirDelay,
// StampDelay and Stamps).
type FeedsWant struct {
	All           bool
	NewWithinDays int
	DueOrder
}

// WantedForFeeds counts the targets a release in a source's newest releases
// could be grabbed for now, episodes and movies: wanted, aired and past the
// air delay, monitored with their title, of holdings acquire knows, not
// reserved — every one, or only the new things (newSQL) —, and no requested
// movie whose window is set from its theatrical release: no automatic grab
// takes one.
func (s *Store) WantedForFeeds(ctx context.Context, w FeedsWant) (episodes, movies int, err error) {
	args := append(w.DueOrder.window(), w.All, w.NewWithinDays)
	err = s.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE a.kind = 'episode'), count(*) FILTER (WHERE a.kind = 'movie')
		  FROM acquisition_targets a JOIN titles t ON t.id = a.title_id
		 WHERE `+sweepableSQL(openParams{delay: "$1", stampDelay: "$2", stamps: "$3"})+` AND ($4 OR `+newSQL("$5")+`)
		   AND NOT (a.kind = 'movie' AND a.release_kind = 'theatrical') AND NOT `+handsOffSQL,
		args...).Scan(&episodes, &movies)
	return episodes, movies, err
}

// RSSSeenMax is how many releases rss-sync keeps of a feed it looked at: the
// last it saw, by acquire's clock. A read reads at most three pages of a
// hundred, so a release drops out only long after it left the pages read.
const RSSSeenMax = 1000

// RSSCursor is what rss-sync has looked at of one feed of a source: the
// releases, by an id of each (RSSID), with when acquire first saw each — by
// its own clock, never the source's, which may run ahead, date a release
// indexed late long before, or date one years ahead. A release is looked at
// once, whenever the source lists it, however it dates it.
type RSSCursor struct {
	IndexerID int64
	Feed      string // tv | movie | search
	Seen      map[int64]int64
	ReadAt    *time.Time
	Detail    string
	// order are the releases kept before this read, the last seen first;
	// added those this read saw, in the order it saw them.
	order, added []int64
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

// Add records that a release was looked at, first seen at — acquire's
// clock. One seen before keeps when it was first seen.
func (c *RSSCursor) Add(key string, at time.Time) {
	if c.Seen == nil {
		c.Seen = map[int64]int64{}
	}
	id := RSSID(key)
	if _, ok := c.Seen[id]; ok {
		return
	}
	c.Seen[id] = at.Unix()
	c.added = append(c.added, id)
}

// Read reports whether the feed was read before.
func (c RSSCursor) Read() bool { return c.ReadAt != nil || len(c.Seen) > 0 }

// kept are the releases the cursor keeps: the last RSSSeenMax it saw, this
// read's first, as it saw them, then those of the reads before.
func (c RSSCursor) kept() (ids, at []int64) {
	ids, at = []int64{}, []int64{}
	seen := map[int64]bool{}
	for _, part := range [][]int64{c.added, c.order} {
		for _, id := range part {
			if seen[id] || len(ids) == RSSSeenMax {
				continue
			}
			seen[id] = true
			ids, at = append(ids, id), append(at, c.Seen[id])
		}
	}
	return ids, at
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
			c.order = append(c.order, ids[i])
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
