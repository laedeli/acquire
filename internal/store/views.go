package store

import (
	"context"
	"time"
)

// SeriesRow is one series with its acquisition progress, for the Series view.
type SeriesRow struct {
	TitleID   string `json:"titleId"`
	TMDBID    int64  `json:"tmdbId"`
	TVDBID    int64  `json:"tvdbId"`
	Title     string `json:"title"`
	Year      int    `json:"year"`
	Status    string `json:"status"`
	Type      string `json:"type"`
	Monitored bool   `json:"monitored"`
	Episodes  int    `json:"episodes"`
	Held      int    `json:"held"`
	Missing   int    `json:"missing"`  // aired, monitored, not held — holdings known
	InFlight  int    `json:"inFlight"` // of the missing: grabbed, downloaded or waiting
	Unaired   int    `json:"unaired"`
	// HoldingsKnown is false until acquire has learned what the library holds
	// of the series; Missing is 0 until then, whatever is on disk.
	HoldingsKnown bool       `json:"holdingsKnown"`
	HoldingsAt    *time.Time `json:"holdingsAt"`
	HoldingsError string     `json:"holdingsError,omitempty"`
	// Followed is set when a request follows the series.
	Followed bool `json:"followed"`
}

// SeriesOverview aggregates the WANT model per series in ONE query.
//
// Doing this per series in Go would be an N+1 across 402 rows; the counts are
// what the view is for, so they are computed where the data is.
func (s *Store) SeriesOverview(ctx context.Context) ([]SeriesRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT t.id, t.tmdb_id, COALESCE(t.tvdb_id,0), t.title, COALESCE(t.year,0),
		       t.status, t.series_type, t.monitored,
		       count(a.id) FILTER (WHERE a.tmdb_gone_at IS NULL OR a.state = 'held'),
		       count(*) FILTER (WHERE a.state = 'held'),
		       count(*) FILTER (WHERE `+missingSQL+`),
		       count(*) FILTER (WHERE `+missingSQL+` AND a.state IN ('grabbed','downloaded','waiting')),
		       count(*) FILTER (WHERE a.id IS NOT NULL AND a.tmdb_gone_at IS NULL AND NOT `+airedSQL+`),
		       t.holdings_at, t.holdings_error, `+followedSQL+`
		  FROM titles t
		  LEFT JOIN acquisition_targets a ON a.title_id = t.id
		 WHERE t.kind = 'series'
		 GROUP BY t.id, t.tmdb_id, t.tvdb_id, t.title, t.year, t.status, t.series_type, t.monitored,
		          t.holdings_at, t.holdings_error, t.followed_at, t.unfollowed_at
		 ORDER BY t.sort_title, t.title`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SeriesRow{}
	for rows.Next() {
		var r SeriesRow
		if err := rows.Scan(&r.TitleID, &r.TMDBID, &r.TVDBID, &r.Title, &r.Year,
			&r.Status, &r.Type, &r.Monitored,
			&r.Episodes, &r.Held, &r.Missing, &r.InFlight, &r.Unaired,
			&r.HoldingsAt, &r.HoldingsError, &r.Followed); err != nil {
			return nil, err
		}
		r.HoldingsKnown = r.HoldingsAt != nil
		out = append(out, r)
	}
	return out, rows.Err()
}

// CalendarRow is one upcoming or recently-aired episode.
type CalendarRow struct {
	TargetID string     `json:"targetId"`
	Title    string     `json:"title"`
	Season   *int       `json:"season"`
	Episode  *int       `json:"episode"`
	AirDate  *time.Time `json:"airDate"`
	State    string     `json:"state"`
	Held     bool       `json:"held"`
}

// Calendar returns episodes airing in a window around now. Air-date awareness
// is a capability the current system has none of — nothing anywhere persists an
// air date today.
func (s *Store) Calendar(ctx context.Context, from, to time.Time) ([]CalendarRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT a.id, t.title, a.season_number, a.episode_number, a.air_date,
		       a.state, a.held_item_id <> ''
		  FROM acquisition_targets a
		  JOIN titles t ON t.id = a.title_id
		 WHERE a.air_date BETWEEN $1::date AND $2::date
		 ORDER BY a.air_date, t.sort_title, a.season_number, a.episode_number`,
		from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CalendarRow{}
	for rows.Next() {
		var r CalendarRow
		if err := rows.Scan(&r.TargetID, &r.Title, &r.Season, &r.Episode,
			&r.AirDate, &r.State, &r.Held); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MissingRow is one thing we want and do not have.
type MissingRow struct {
	TargetID string     `json:"targetId"`
	TitleID  string     `json:"titleId"`
	Title    string     `json:"title"`
	Kind     string     `json:"kind"`
	Season   *int       `json:"season"`
	Episode  *int       `json:"episode"`
	AirDate  *time.Time `json:"airDate"`
	// State is wanted or failed, or grabbed, downloaded or waiting while a
	// grab for it is under way; Detail says why.
	State  string `json:"state"`
	Detail string `json:"detail"`
	// The last search: found | none | error, what it said, when, and when
	// the sweep looks again (backoffUntil, after misses).
	SearchOutcome string     `json:"searchOutcome"`
	SearchDetail  string     `json:"searchDetail"`
	LastSearchAt  *time.Time `json:"lastSearchAt"`
	Failures      int        `json:"searchFailures"`
	BackoffTil    *time.Time `json:"backoffUntil"`
	// GrabQueued is set while a grab an admin asked for waits for the worker.
	GrabQueued bool `json:"grabQueued"`
	// Searchable is false when the title has no id an indexer will accept, so
	// the row can say WHY it is stuck rather than just sitting there.
	Searchable bool `json:"searchable"`
}

// Missing lists the backlog: aired, monitored targets the library does not
// hold, of titles whose holdings acquire knows — with what is being done about
// each (a grab under way, the last failure). Rows in search backoff are
// included, flagged, rather than hidden: a backlog view that silently omits
// everything failing is the least useful version of itself. A title whose
// holdings are unknown has no rows here: nothing says its episodes are not on
// disk (Counts says how many are left out).
func (s *Store) Missing(ctx context.Context, limit int) ([]MissingRow, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := s.pool.Query(ctx, `
		SELECT a.id, a.title_id, t.title, a.kind, a.season_number, a.episode_number, a.air_date,
		       a.state, a.detail, a.search_outcome, a.search_detail, a.last_search_at,
		       a.search_failures,
		       CASE WHEN a.search_failures > 0 THEN a.search_backoff_until END,
		       a.grab_requested_at IS NOT NULL,
		       CASE WHEN t.kind = 'series' THEN t.tvdb_id IS NOT NULL
		            ELSE t.imdb_id <> '' OR t.tmdb_id > 0 END
		  FROM acquisition_targets a
		  JOIN titles t ON t.id = a.title_id
		 WHERE `+missingSQL+`
		 ORDER BY a.air_date DESC NULLS LAST, t.sort_title, a.season_number, a.episode_number
		 LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MissingRow{}
	for rows.Next() {
		var r MissingRow
		if err := rows.Scan(&r.TargetID, &r.TitleID, &r.Title, &r.Kind, &r.Season, &r.Episode, &r.AirDate,
			&r.State, &r.Detail, &r.SearchOutcome, &r.SearchDetail, &r.LastSearchAt,
			&r.Failures, &r.BackoffTil, &r.GrabQueued, &r.Searchable); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Counts is the headline summary the console and /metrics both want.
type Counts struct {
	Titles  int `json:"titles"`
	Series  int `json:"series"`
	Movies  int `json:"movies"`
	Targets int `json:"targets"`
	Held    int `json:"held"`
	// Missing is the Missing view's row count; InFlight and Failed are the
	// part of it a grab is under way for, and the part whose last grab failed.
	Missing  int `json:"missing"`
	InFlight int `json:"inFlight"`
	Failed   int `json:"failed"`
	Unaired  int `json:"unaired"`
	// InBackoff are wanted targets whose searches keep finding nothing.
	InBackoff int `json:"inBackoff"`
	// HoldingsUnknown counts the monitored titles whose holdings acquire does
	// not know, and UnknownTargets their aired targets that are not held: what
	// Missing leaves out because nothing says it is not on disk.
	HoldingsUnknown int `json:"holdingsUnknown"`
	UnknownTargets  int `json:"unknownTargets"`
}

func (s *Store) Counts(ctx context.Context) (Counts, error) {
	var c Counts
	err := s.pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM titles),
		       (SELECT count(*) FROM titles WHERE kind='series'),
		       (SELECT count(*) FROM titles WHERE kind='movie'),
		       (SELECT count(*) FROM acquisition_targets),
		       (SELECT count(*) FROM acquisition_targets WHERE state='held'),
		       count(*) FILTER (WHERE `+missingSQL+`),
		       count(*) FILTER (WHERE `+missingSQL+` AND a.state IN ('grabbed','downloaded','waiting')),
		       count(*) FILTER (WHERE `+missingSQL+` AND a.state = 'failed'),
		       count(*) FILTER (WHERE NOT `+airedSQL+`),
		       count(*) FILTER (WHERE a.state = 'wanted' AND a.search_failures > 0
		                          AND a.search_backoff_until > now()),
		       (SELECT count(*) FROM titles WHERE monitored AND holdings_at IS NULL),
		       count(*) FILTER (WHERE a.state <> 'held' AND a.monitored AND t.monitored
		                          AND NOT (`+knownSQL+`) AND `+airedSQL+`)
		  FROM acquisition_targets a
		  JOIN titles t ON t.id = a.title_id`).
		Scan(&c.Titles, &c.Series, &c.Movies, &c.Targets, &c.Held, &c.Missing, &c.InFlight, &c.Failed,
			&c.Unaired, &c.InBackoff, &c.HoldingsUnknown, &c.UnknownTargets)
	return c, err
}

// HoldingsSummary is what acquire knows of the library's holdings, for the
// Missing view: how many monitored titles' holdings are known and unknown,
// why the unknown ones are (the reason most of them give), and when a title's
// holdings were last learned.
type HoldingsSummary struct {
	Known   int        `json:"known"`
	Unknown int        `json:"unknown"`
	Reason  string     `json:"reason,omitempty"`
	LastAt  *time.Time `json:"lastAt"`
}

func (s *Store) HoldingsSummary(ctx context.Context) (HoldingsSummary, error) {
	var h HoldingsSummary
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE holdings_at IS NOT NULL),
		       count(*) FILTER (WHERE holdings_at IS NULL),
		       COALESCE((SELECT holdings_error FROM titles
		                  WHERE monitored AND holdings_at IS NULL AND holdings_error <> ''
		                  GROUP BY holdings_error ORDER BY count(*) DESC, holdings_error LIMIT 1), ''),
		       max(holdings_at)
		  FROM titles WHERE monitored`).Scan(&h.Known, &h.Unknown, &h.Reason, &h.LastAt)
	return h, err
}

// HistoryEntry is one thing that happened. History has no foreign key on
// purpose — it must outlive its subject.
type HistoryEntry struct {
	Kind     string
	Subject  string
	Title    string
	Indexer  string
	Protocol string
	SizeMb   int64
	Score    int
	Reason   string
}

func (s *Store) RecordHistory(ctx context.Context, h HistoryEntry) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO history (kind, subject, title, indexer, protocol, size_mb, score, reason)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		h.Kind, h.Subject, h.Title, h.Indexer, h.Protocol, h.SizeMb, h.Score, h.Reason)
	return err
}

// RecentHistory powers the console's activity view.
func (s *Store) RecentHistory(ctx context.Context, limit int) ([]map[string]any, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT at, kind, subject, title, indexer, protocol, size_mb, score, reason
		  FROM history ORDER BY at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var at time.Time
		var kind, subject, title, indexer, protocol, reason string
		var sizeMb int64
		var score int
		if err := rows.Scan(&at, &kind, &subject, &title, &indexer, &protocol, &sizeMb, &score, &reason); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"at": at, "kind": kind, "subject": subject, "title": title,
			"indexer": indexer, "protocol": protocol, "sizeMb": sizeMb,
			"score": score, "reason": reason,
		})
	}
	return out, rows.Err()
}

// RetryableTargets are wanted targets whose searches found nothing or failed
// and whose backoff has expired — the ones the sweep may look at, as
// DueTargets says.
func (s *Store) RetryableTargets(ctx context.Context, limit int) ([]Target, error) {
	return s.RetryableTargetsBy(ctx, limit, DueOrder{})
}

// RetryableTargetsBy is RetryableTargets in the order the policy asks for, as
// DueTargetsBy orders them.
func (s *Store) RetryableTargetsBy(ctx context.Context, limit int, o DueOrder) ([]Target, error) {
	order := `a.search_backoff_until`
	args := o.args(limit)
	if o.NewWithinDays > 0 {
		order = newFirstSQL("$5") + order
		args = append(args, o.NewWithinDays)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+targetCols+`
		  FROM acquisition_targets a
		  JOIN titles t ON t.id = a.title_id
		 WHERE `+sweepableSQL(dueParams)+`
		   AND a.search_failures > 0
		   AND a.search_backoff_until IS NOT NULL AND a.search_backoff_until <= now()
		 ORDER BY `+order+`
		 LIMIT $1`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTargets(rows)
}

// ActiveDownloads counts in-flight work, so a sweep cannot start unbounded
// grabs. Terminal states are excluded; anything else is still consuming disk.
// A target grab handed over whose download the gateway has not reported yet
// counts too (GrabsInFlight): without it a run of grabs sees the same count
// for each and overshoots the limit.
func (s *Store) ActiveDownloads(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM downloads WHERE state NOT IN ('completed','failed','cancelled')`).Scan(&n)
	if err != nil {
		return 0, err
	}
	pending, err := s.GrabsInFlight(ctx)
	return n + pending, err
}
