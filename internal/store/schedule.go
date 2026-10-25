package store

import (
	"context"
	"time"
)

// Exact air times, from a schedule source (search.airTimes).
//
// TMDB dates an episode by its day. The schedule source says how a series
// airs — broadcast on a network, or streamed — and, when it knows the hour,
// the moment each episode airs and how long it runs: kept on its target
// (air_stamp, air_runtime). Of a broadcast, an automatic grab takes nothing
// published before it ended, and waits a while after; of a streamed episode,
// nothing published more than half an hour before it streams. Until a
// series' air times are read, an automatic grab takes nothing of it. A
// series' show id at the source is kept on its title, so it is looked up
// once.

// How the schedule source says a series airs (titles.schedule_kind).
const (
	ScheduleBroadcast = "broadcast"
	ScheduleStreaming = "streaming"
)

// ScheduledShow is a series as the schedule source has it: its show id
// there, how it airs (ScheduleBroadcast, ScheduleStreaming, ""), the country
// of its network or service, and its episodes.
type ScheduledShow struct {
	ID       int64
	Kind     string
	Country  string
	Episodes []ScheduledEpisode
}

// ScheduledEpisode is one episode as the schedule source lists it: its
// numbers, the day it airs where it airs, the moment it airs when the source
// knows the hour, and how long it runs in minutes (0: not known).
type ScheduledEpisode struct {
	Season, Episode int
	AirDate         *time.Time
	AirStamp        *time.Time
	Runtime         int
}

// ApplySchedule records a series' show at the schedule source — its id, how
// it airs, its country — and the moment each of its episodes airs and how
// long it runs, on the target of the same numbers when the source dates it
// within a day of TMDB: numbers the two count differently are not taken for
// the same episode. An episode the source gives no hour for any more, or
// dates elsewhere, keeps none: it is told by its air date. stamped is how
// many of the series' targets carry an air time now.
func (s *Store) ApplySchedule(ctx context.Context, titleID string, show ScheduledShow) (stamped int, err error) {
	seasons, numbers, runtimes := []int32{}, []int32{}, []int32{}
	stamps, days := []time.Time{}, []time.Time{}
	for _, e := range show.Episodes {
		if e.AirStamp == nil || e.AirDate == nil || e.Season < 1 || e.Episode < 1 {
			continue
		}
		seasons, numbers = append(seasons, int32(e.Season)), append(numbers, int32(e.Episode))
		stamps, days = append(stamps, e.AirStamp.UTC()), append(days, e.AirDate.UTC())
		runtimes = append(runtimes, int32(max(e.Runtime, 0)))
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		UPDATE titles SET schedule_id = $2, schedule_at = now(), schedule_error = '', schedule_kind = $3, schedule_country = $4,
		       updated_at = now()
		 WHERE id = $1`,
		titleID, show.ID, show.Kind, show.Country); err != nil {
		return 0, err
	}
	const listed = `unnest($2::int[], $3::int[], $4::timestamptz[], $5::date[], $6::int[]) AS s(season, episode, stamp, day, runtime)`
	if _, err := tx.Exec(ctx, `
		UPDATE acquisition_targets a SET air_stamp = s.stamp, air_runtime = s.runtime, updated_at = now()
		  FROM `+listed+`
		 WHERE a.title_id = $1 AND a.kind = 'episode' AND a.season_number = s.season AND a.episode_number = s.episode
		   AND a.air_date IS NOT NULL AND abs(a.air_date - s.day) <= 1
		   AND (a.air_stamp IS DISTINCT FROM s.stamp OR a.air_runtime <> s.runtime)`,
		titleID, seasons, numbers, stamps, days, runtimes); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE acquisition_targets a SET air_stamp = NULL, air_runtime = 0, updated_at = now()
		 WHERE a.title_id = $1 AND a.air_stamp IS NOT NULL
		   AND NOT EXISTS (SELECT 1 FROM `+listed+`
		                    WHERE s.season = a.season_number AND s.episode = a.episode_number
		                      AND a.air_date IS NOT NULL AND abs(a.air_date - s.day) <= 1)`,
		titleID, seasons, numbers, stamps, days, runtimes); err != nil {
		return 0, err
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM acquisition_targets WHERE title_id = $1 AND air_stamp IS NOT NULL`,
		titleID).Scan(&stamped); err != nil {
		return 0, err
	}
	return stamped, tx.Commit(ctx)
}

// ScheduleFailure is why a series' air times could not be read
// (ScheduleFailed).
type ScheduleFailure int

const (
	// ScheduleDown: the source did not answer as it should — out of reach,
	// failing, limiting. Nothing it gave before is changed; a series never
	// read stays so, and is read again on the next run.
	ScheduleDown ScheduleFailure = iota
	// ScheduleUnknown: the source does not know the series by its ids, or
	// it has none to be asked by. Its show id is forgotten, and it is looked
	// up again after a week; the air times it gave stay.
	ScheduleUnknown
	// ScheduleUnreadable: the source answered what cannot be read. It is
	// read again as a series read is.
	ScheduleUnreadable
	// ScheduleOtherShow: the show the source answers by the series' ids is
	// another — a name neither the series' nor one of its aliases: its show
	// id, how it airs and every air time taken from it are dropped, and it
	// is looked up again after a week.
	ScheduleOtherShow
)

// ScheduleFailed records why a series' air times could not be read, as how
// says (ScheduleFailure). Of all but ScheduleDown, the series counts as read
// now — told by its air dates, as far as the source gives it none.
func (s *Store) ScheduleFailed(ctx context.Context, titleID, reason string, how ScheduleFailure) error {
	other := how == ScheduleOtherShow
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		UPDATE titles SET schedule_error = $2,
		       schedule_id = CASE WHEN $4 THEN NULL ELSE schedule_id END,
		       schedule_kind = CASE WHEN $5 THEN '' ELSE schedule_kind END,
		       schedule_country = CASE WHEN $5 THEN '' ELSE schedule_country END,
		       schedule_at = CASE WHEN $3 THEN now() ELSE schedule_at END, updated_at = now()
		 WHERE id = $1`, titleID, clipText(reason), how != ScheduleDown, how == ScheduleUnknown || other, other); err != nil {
		return err
	}
	if other {
		if _, err := tx.Exec(ctx, `
			UPDATE acquisition_targets SET air_stamp = NULL, air_runtime = 0, updated_at = now()
			 WHERE title_id = $1 AND (air_stamp IS NOT NULL OR air_runtime <> 0)`, titleID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// scheduleTrackedSQL is "the series t's air times matter": a series
// monitored, not held for an admin's approval, whose releases name the season
// and episode acquire tracks (handsOffSQL) — what an automatic grab may take.
const scheduleTrackedSQL = `t.kind = 'series' AND t.monitored AND NOT ` + heldForApprovalSQL + ` AND NOT ` + handsOffSQL

// nearAirSQL is "an episode of the series t aired in the last 14 days or
// airs in the next 7": what an automatic grab takes soonest.
const nearAirSQL = `EXISTS (SELECT 1 FROM acquisition_targets x WHERE x.title_id = t.id AND x.kind = 'episode'
	AND x.air_date BETWEEN (now() AT TIME ZONE 'UTC')::date - 14 AND (now() AT TIME ZONE 'UTC')::date + 7)`

// ScheduleEvery is how often a series' air times are read again: one that
// runs (runningSQL) every Running, a show the source did not know or knew
// under another name after Unknown, any other every Other.
type ScheduleEvery struct {
	Running, Unknown, Other time.Duration
}

// SchedulesDue are the series whose air times are due to be read
// (scheduleTrackedSQL): never read first — of them, the ones with an episode
// near its air date (nearAirSQL) —, then the ones that run, then the ones
// read longest ago. failed narrows them to the series never read because the
// source did not answer when it was last asked.
func (s *Store) SchedulesDue(ctx context.Context, every ScheduleEvery, limit int, failed bool) ([]Title, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+titleCols+`
		  FROM titles t
		  CROSS JOIN LATERAL (SELECT `+runningSQL+` AS running) r
		 WHERE `+scheduleTrackedSQL+`
		   AND (CASE WHEN t.schedule_at IS NULL THEN NOT $5 OR t.schedule_error <> ''
		             WHEN $5 THEN false
		             WHEN t.schedule_id IS NULL THEN t.schedule_at < now() - make_interval(secs => $3::float8)
		             WHEN r.running THEN t.schedule_at < now() - make_interval(secs => $2::float8)
		             ELSE t.schedule_at < now() - make_interval(secs => $4::float8) END)
		 ORDER BY t.schedule_at IS NULL DESC, `+nearAirSQL+` DESC, r.running DESC, t.schedule_at NULLS FIRST, t.id
		 LIMIT $1`, limit, every.Running.Seconds(), every.Unknown.Seconds(), every.Other.Seconds(), failed)
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

// AirTimes is how far the schedule source's air times are of the series that
// run (runningSQL) whose air times matter (scheduleTrackedSQL): of Running,
// Read are read; Unknown are told by their air dates alone — the source does
// not know them, or knows them under another name; Pending are not read yet,
// and an automatic grab takes nothing of them until they are. LastError is
// why the source last failed to give a pending one's.
type AirTimes struct {
	Running   int    `json:"running"`
	Read      int    `json:"read"`
	Unknown   int    `json:"unknown"`
	Pending   int    `json:"pending"`
	LastError string `json:"lastError,omitempty"`
}

// AirTimesOfRunning counts them.
func (s *Store) AirTimesOfRunning(ctx context.Context) (AirTimes, error) {
	var a AirTimes
	err := s.pool.QueryRow(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE t.schedule_at IS NOT NULL AND t.schedule_id IS NOT NULL),
		       count(*) FILTER (WHERE t.schedule_at IS NOT NULL AND t.schedule_id IS NULL),
		       count(*) FILTER (WHERE t.schedule_at IS NULL),
		       COALESCE((array_agg(t.schedule_error ORDER BY t.updated_at DESC)
		                   FILTER (WHERE t.schedule_at IS NULL AND t.schedule_error <> ''))[1], '')
		  FROM titles t
		 WHERE `+scheduleTrackedSQL+` AND `+runningSQL).Scan(&a.Running, &a.Read, &a.Unknown, &a.Pending, &a.LastError)
	return a, err
}
