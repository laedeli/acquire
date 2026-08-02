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
	Missing   int    `json:"missing"` // aired, monitored, still wanted
	Unaired   int    `json:"unaired"`
}

// SeriesOverview aggregates the WANT model per series in ONE query.
//
// Doing this per series in Go would be an N+1 across 402 rows; the counts are
// what the view is for, so they are computed where the data is.
func (s *Store) SeriesOverview(ctx context.Context) ([]SeriesRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT t.id, t.tmdb_id, COALESCE(t.tvdb_id,0), t.title, COALESCE(t.year,0),
		       t.status, t.series_type, t.monitored,
		       count(a.id),
		       count(*) FILTER (WHERE a.state = 'held'),
		       count(*) FILTER (WHERE a.state = 'wanted' AND a.monitored
		                          AND a.air_window_opens_at IS NOT NULL
		                          AND a.air_window_opens_at <= now()),
		       count(*) FILTER (WHERE a.air_window_opens_at IS NULL
		                           OR a.air_window_opens_at > now())
		  FROM titles t
		  LEFT JOIN acquisition_targets a ON a.title_id = t.id
		 WHERE t.kind = 'series'
		 GROUP BY t.id, t.tmdb_id, t.tvdb_id, t.title, t.year, t.status, t.series_type, t.monitored
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
			&r.Episodes, &r.Held, &r.Missing, &r.Unaired); err != nil {
			return nil, err
		}
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
	TargetID   string     `json:"targetId"`
	Title      string     `json:"title"`
	Kind       string     `json:"kind"`
	Season     *int       `json:"season"`
	Episode    *int       `json:"episode"`
	AirDate    *time.Time `json:"airDate"`
	Failures   int        `json:"searchFailures"`
	BackoffTil *time.Time `json:"backoffUntil"`
	// Searchable is false when the title has no id an indexer will accept, so
	// the row can say WHY it is stuck rather than just sitting there.
	Searchable bool `json:"searchable"`
}

// Missing lists monitored, aired, still-wanted targets — the backlog. It
// includes rows currently in backoff, flagged, rather than hiding them: a
// backlog view that silently omits everything failing is the least useful
// version of itself.
func (s *Store) Missing(ctx context.Context, limit int) ([]MissingRow, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := s.pool.Query(ctx, `
		SELECT a.id, t.title, a.kind, a.season_number, a.episode_number, a.air_date,
		       a.search_failures, a.search_backoff_until,
		       CASE WHEN t.kind = 'series' THEN t.tvdb_id IS NOT NULL
		            ELSE t.imdb_id <> '' OR t.tmdb_id > 0 END
		  FROM acquisition_targets a
		  JOIN titles t ON t.id = a.title_id
		 WHERE a.state = 'wanted' AND a.monitored
		   AND (a.air_window_opens_at IS NULL OR a.air_window_opens_at <= now())
		 ORDER BY a.air_date DESC NULLS LAST, t.sort_title
		 LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MissingRow{}
	for rows.Next() {
		var r MissingRow
		if err := rows.Scan(&r.TargetID, &r.Title, &r.Kind, &r.Season, &r.Episode,
			&r.AirDate, &r.Failures, &r.BackoffTil, &r.Searchable); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Counts is the headline summary the console and /metrics both want.
type Counts struct {
	Titles    int `json:"titles"`
	Series    int `json:"series"`
	Movies    int `json:"movies"`
	Targets   int `json:"targets"`
	Held      int `json:"held"`
	Missing   int `json:"missing"`
	Unaired   int `json:"unaired"`
	InBackoff int `json:"inBackoff"`
}

func (s *Store) Counts(ctx context.Context) (Counts, error) {
	var c Counts
	err := s.pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM titles),
		       (SELECT count(*) FROM titles WHERE kind='series'),
		       (SELECT count(*) FROM titles WHERE kind='movie'),
		       (SELECT count(*) FROM acquisition_targets),
		       (SELECT count(*) FROM acquisition_targets WHERE state='held'),
		       (SELECT count(*) FROM acquisition_targets
		         WHERE state='wanted' AND monitored
		           AND (air_window_opens_at IS NULL OR air_window_opens_at <= now())),
		       (SELECT count(*) FROM acquisition_targets
		         WHERE air_window_opens_at > now()),
		       (SELECT count(*) FROM acquisition_targets
		         WHERE search_backoff_until > now())`).
		Scan(&c.Titles, &c.Series, &c.Movies, &c.Targets, &c.Held, &c.Missing, &c.Unaired, &c.InBackoff)
	return c, err
}
