package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Exact air times, from a schedule source (search.airTimes).
//
// TMDB dates an episode by its day. The schedule source says how a series
// airs — broadcast on a network, or streamed — and, when it knows the hour,
// the moment each episode airs and how long it runs: kept on its target
// (air_stamp, air_runtime). Of a broadcast, an automatic grab takes nothing
// published before it ended, and waits a while after; of a streamed episode,
// nothing published more than half an hour before it streams. An episode the
// source dates more than a day after TMDB is told by the source's date and
// hour (schedule_day): TMDB has not caught up with a move. Of a broadcast the
// source gives no hour for, the hour is estimated from its usual one
// (air_stamp_estimated). An episode the two date apart that cannot be told
// the same, or of a broadcast without an hour to estimate, is held: no
// automatic grab takes it (schedule_hold). Until a series' air times are read
// by the rules of this version (schedule_version), an automatic grab takes
// nothing of it. A series' show id at the source is kept on its title, so it
// is looked up once.

// How the schedule source says a series airs (titles.schedule_kind).
const (
	ScheduleBroadcast = "broadcast"
	ScheduleStreaming = "streaming"
)

// ScheduleVersion is the version of the rules a series' air times are read
// by: a series read by an earlier one is due first, and nothing of it is
// grabbed on its own until it is read again (titles.schedule_version).
const ScheduleVersion = 1

// scheduleReadSQL is "the series t's air times were read, by these rules".
var scheduleReadSQL = fmt.Sprintf(`(t.schedule_at IS NOT NULL AND t.schedule_version >= %d)`, ScheduleVersion)

// ScheduledShow is a series as the schedule source has it: its show id
// there, how it airs (ScheduleBroadcast, ScheduleStreaming, ""), the country
// of its network or service, and what the read makes of each of its episode
// targets.
type ScheduledShow struct {
	ID      int64
	Kind    string
	Country string
	Targets []ScheduledTarget
}

// ScheduledTarget is what a read of the schedule source makes of an episode
// target: TargetID; AirDate, its day at TMDB as the read saw it — nothing is
// written of a target whose day has changed since —; EpisodeID, the source's
// episode taken for it (0: none); Stamp and Runtime, the moment it airs and
// how long it runs, as the source says, or Estimated from the usual hour of
// the series' broadcasts; Day, the source's date of it when more than a day
// after TMDB's; Hold, why no automatic grab takes it ("": none).
type ScheduledTarget struct {
	TargetID  string
	AirDate   *time.Time
	EpisodeID int64
	Stamp     *time.Time
	Runtime   int
	Estimated bool
	Day       *time.Time
	Hold      string
}

// ApplySchedule records a series' show at the schedule source — its id, how
// it airs, its country — and what the read made of each of its episodes, in
// one transaction; the series counts as read by these rules
// (ScheduleVersion). An episode target the read names nothing of keeps no air
// time. A hold is said on the target (its detail) while it lasts. stamped is
// how many of the series' targets carry an air time now.
func (s *Store) ApplySchedule(ctx context.Context, titleID string, show ScheduledShow) (stamped int, err error) {
	ids, days, holds := []string{}, []*time.Time{}, []string{}
	episodes, runtimes := []int64{}, []int32{}
	stamps, sdays := []*time.Time{}, []*time.Time{}
	estimated := []bool{}
	for _, t := range show.Targets {
		ids, days, holds = append(ids, t.TargetID), append(days, t.AirDate), append(holds, clipText(t.Hold))
		episodes, runtimes = append(episodes, t.EpisodeID), append(runtimes, int32(max(t.Runtime, 0)))
		stamps, sdays, estimated = append(stamps, t.Stamp), append(sdays, t.Day), append(estimated, t.Estimated && t.Stamp != nil)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		UPDATE titles SET schedule_id = $2, schedule_at = now(), schedule_error = '', schedule_kind = $3, schedule_country = $4,
		       schedule_version = $5, schedule_down = false, updated_at = now()
		 WHERE id = $1`,
		titleID, show.ID, show.Kind, show.Country, ScheduleVersion); err != nil {
		return 0, err
	}
	const listed = `unnest($2::text[], $3::date[], $4::bigint[], $5::timestamptz[], $6::int[], $7::boolean[], $8::date[], $9::text[])
		AS s(id, tmdb_day, episode, stamp, runtime, estimated, day, hold)`
	if _, err := tx.Exec(ctx, `
		UPDATE acquisition_targets a
		   SET schedule_episode_id = NULLIF(s.episode, 0), air_stamp = s.stamp, air_runtime = CASE WHEN s.stamp IS NULL THEN 0 ELSE s.runtime END,
		       air_stamp_estimated = s.estimated, schedule_day = s.day, schedule_hold = s.hold,
		       detail = CASE WHEN s.hold <> '' AND s.hold <> a.schedule_hold THEN s.hold
		                     WHEN s.hold = '' AND a.schedule_hold <> '' AND a.detail = a.schedule_hold THEN ''
		                     ELSE a.detail END,
		       updated_at = now()
		  FROM `+listed+`
		 WHERE a.id = s.id AND a.title_id = $1 AND a.kind = 'episode' AND a.air_date IS NOT DISTINCT FROM s.tmdb_day
		   AND (a.schedule_episode_id IS DISTINCT FROM NULLIF(s.episode, 0) OR a.air_stamp IS DISTINCT FROM s.stamp
		        OR a.air_runtime <> CASE WHEN s.stamp IS NULL THEN 0 ELSE s.runtime END OR a.air_stamp_estimated <> s.estimated
		        OR a.schedule_day IS DISTINCT FROM s.day OR a.schedule_hold <> s.hold)`,
		titleID, ids, days, episodes, stamps, runtimes, estimated, sdays, holds); err != nil {
		return 0, err
	}
	// What the read named nothing of keeps nothing of the source's.
	if _, err := tx.Exec(ctx, `
		UPDATE acquisition_targets a
		   SET schedule_episode_id = NULL, air_stamp = NULL, air_runtime = 0, air_stamp_estimated = false, schedule_day = NULL,
		       schedule_hold = '', detail = CASE WHEN a.schedule_hold <> '' AND a.detail = a.schedule_hold THEN '' ELSE a.detail END,
		       updated_at = now()
		 WHERE a.title_id = $1 AND a.kind = 'episode' AND NOT (a.id = ANY($2::text[]))
		   AND (a.schedule_episode_id IS NOT NULL OR a.air_stamp IS NOT NULL OR a.schedule_day IS NOT NULL OR a.schedule_hold <> '')`,
		titleID, ids); err != nil {
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
	// failing, limiting, refusing, or answering what cannot be read. Nothing
	// it gave before is changed; a series never read stays so, and is read
	// again soon.
	ScheduleDown ScheduleFailure = iota
	// ScheduleUnknown: the source does not know the series by its ids, or
	// it has none to be asked by. Its show id is forgotten, and it is looked
	// up again after a week; the air times it gave stay, and no episode of
	// it is held for the source.
	ScheduleUnknown
	// ScheduleOtherShow: the show the source answers by the series' ids is
	// another — a name neither the series' nor one of its aliases: its show
	// id, how it airs and everything taken from it are dropped, and it is
	// looked up again after a week.
	ScheduleOtherShow
)

// ScheduleFailed records why a series' air times could not be read, as how
// says (ScheduleFailure). Of all but ScheduleDown, the series counts as read
// now, by these rules — told by its air dates, as far as the source gives it
// none.
func (s *Store) ScheduleFailed(ctx context.Context, titleID, reason string, how ScheduleFailure) error {
	other := how == ScheduleOtherShow
	read := how != ScheduleDown
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
		       schedule_version = CASE WHEN $3 THEN $6 ELSE schedule_version END,
		       schedule_at = CASE WHEN $3 THEN now() ELSE schedule_at END, schedule_down = NOT $3, updated_at = now()
		 WHERE id = $1`, titleID, clipText(reason), read, read, other, ScheduleVersion); err != nil {
		return err
	}
	if read {
		// Without the source, nothing is held for it; of another show, nothing
		// taken from it stays.
		if _, err := tx.Exec(ctx, `
			UPDATE acquisition_targets a
			   SET air_stamp = CASE WHEN $2 OR a.air_stamp_estimated THEN NULL ELSE a.air_stamp END,
			       air_runtime = CASE WHEN $2 OR a.air_stamp_estimated THEN 0 ELSE a.air_runtime END,
			       air_stamp_estimated = false,
			       schedule_episode_id = CASE WHEN $2 THEN NULL ELSE a.schedule_episode_id END,
			       schedule_day = NULL, schedule_hold = '',
			       detail = CASE WHEN a.schedule_hold <> '' AND a.detail = a.schedule_hold THEN '' ELSE a.detail END,
			       updated_at = now()
			 WHERE a.title_id = $1 AND (a.air_stamp_estimated OR a.schedule_day IS NOT NULL OR a.schedule_hold <> ''
			       OR ($2 AND (a.air_stamp IS NOT NULL OR a.schedule_episode_id IS NOT NULL)))`, titleID, other); err != nil {
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

// movedHoldSQL is "an episode of the series t is held after TMDB moved it,
// until the source is read again" (ScheduleMovedHold).
var movedHoldSQL = `EXISTS (SELECT 1 FROM acquisition_targets x WHERE x.title_id = t.id AND x.schedule_hold = '` +
	strings.ReplaceAll(ScheduleMovedHold, "'", "''") + `')`

// SchedulesDue are the series whose air times are due to be read
// (scheduleTrackedSQL): never read — or read by an earlier version of the
// rules (ScheduleVersion) —, or with an episode held after TMDB moved it,
// first; of them the ones the source has not failed for before the ones it
// has (schedule_down), and the ones with an episode near its air date
// (nearAirSQL) first; then the ones that run, then the ones read longest ago.
// failed narrows them to the series to try again soon: not read because the
// source did not answer when it was last asked, or with an episode held after
// a move.
func (s *Store) SchedulesDue(ctx context.Context, every ScheduleEvery, limit int, failed bool) ([]Title, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+titleCols+`
		  FROM titles t
		  CROSS JOIN LATERAL (SELECT `+runningSQL+` AS running, `+movedHoldSQL+` AS moved) r
		 WHERE `+scheduleTrackedSQL+`
		   AND (CASE WHEN r.moved THEN true
		             WHEN NOT `+scheduleReadSQL+` THEN NOT $5 OR t.schedule_down OR t.schedule_error <> ''
		             WHEN $5 THEN false
		             WHEN t.schedule_id IS NULL THEN t.schedule_at < now() - make_interval(secs => $3::float8)
		             WHEN r.running THEN t.schedule_at < now() - make_interval(secs => $2::float8)
		             ELSE t.schedule_at < now() - make_interval(secs => $4::float8) END)
		 ORDER BY (`+scheduleReadSQL+` AND NOT r.moved), t.schedule_down, `+nearAirSQL+` DESC, r.running DESC, t.schedule_at NULLS FIRST, t.id
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
// not know them, they have no id to be asked by, or the source knows them
// under another name; Pending are not read yet by these rules, and an
// automatic grab takes nothing of them until they are — Earlier of them were
// read by an earlier version of the rules, and are read again. LastError is
// why the source last failed to give a pending one's. Of every series whose
// air times matter, running or not: FailingEnded are the ones that do not
// run whose last read the source did not answer (schedule_down); Held are
// their episodes no automatic grab takes until TMDB and the source agree, or
// it has an air time — HeldMoved of them after TMDB moved them, until the
// source is read again.
type AirTimes struct {
	Running      int    `json:"running"`
	Read         int    `json:"read"`
	Unknown      int    `json:"unknown"`
	Pending      int    `json:"pending"`
	Earlier      int    `json:"earlier"`
	LastError    string `json:"lastError,omitempty"`
	FailingEnded int    `json:"failingEnded"`
	Held         int    `json:"held"`
	HeldMoved    int    `json:"heldMoved"`
}

// AirTimesOfRunning counts them.
func (s *Store) AirTimesOfRunning(ctx context.Context) (AirTimes, error) {
	var a AirTimes
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE r.running),
		       count(*) FILTER (WHERE r.running AND `+scheduleReadSQL+` AND t.schedule_id IS NOT NULL),
		       count(*) FILTER (WHERE r.running AND `+scheduleReadSQL+` AND t.schedule_id IS NULL),
		       count(*) FILTER (WHERE r.running AND NOT `+scheduleReadSQL+`),
		       count(*) FILTER (WHERE r.running AND NOT `+scheduleReadSQL+` AND t.schedule_at IS NOT NULL),
		       COALESCE((array_agg(t.schedule_error ORDER BY t.updated_at DESC)
		                   FILTER (WHERE r.running AND NOT `+scheduleReadSQL+` AND t.schedule_error <> ''))[1], ''),
		       count(*) FILTER (WHERE NOT r.running AND t.schedule_down),
		       COALESCE(sum(h.held), 0), COALESCE(sum(h.moved), 0)
		  FROM titles t
		  CROSS JOIN LATERAL (SELECT `+runningSQL+` AS running) r
		  CROSS JOIN LATERAL (SELECT count(*) AS held, count(*) FILTER (WHERE x.schedule_hold = $1) AS moved
		                        FROM acquisition_targets x
		                       WHERE x.title_id = t.id AND x.schedule_hold <> '' AND x.monitored AND x.state <> 'held') h
		 WHERE `+scheduleTrackedSQL, ScheduleMovedHold).Scan(&a.Running, &a.Read, &a.Unknown, &a.Pending, &a.Earlier, &a.LastError,
		&a.FailingEnded, &a.Held, &a.HeldMoved)
	return a, err
}
