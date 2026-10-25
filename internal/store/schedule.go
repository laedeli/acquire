package store

import (
	"context"
	"time"
)

// Exact air times, from a schedule source (search.airTimes).
//
// TMDB dates an episode by its day. When the schedule source knows the hour,
// the moment the episode airs is kept on its target (air_stamp): an automatic
// grab takes nothing published more than half an hour before it, and waits a
// while after it. A series' show id at the source is kept on its title, so it
// is looked up once.

// ScheduledEpisode is one episode as the schedule source lists it: its
// numbers, the day it airs where it airs, and the moment it airs, when the
// source knows the hour.
type ScheduledEpisode struct {
	Season, Episode int
	AirDate         *time.Time
	AirStamp        *time.Time
}

// ApplySchedule records a series' show id at the schedule source and the
// moment each of its episodes airs, on the target of the same numbers when
// the source dates it within a day of TMDB — numbers the two count
// differently are not taken for the same episode. An episode the source gives
// no hour for any more, or dates elsewhere, keeps none: it is told by its air
// date. stamped is how many of the series' targets carry an air time now.
func (s *Store) ApplySchedule(ctx context.Context, titleID string, showID int64, eps []ScheduledEpisode) (stamped int, err error) {
	seasons, numbers := []int32{}, []int32{}
	stamps, days := []time.Time{}, []time.Time{}
	for _, e := range eps {
		if e.AirStamp == nil || e.AirDate == nil || e.Season < 1 || e.Episode < 1 {
			continue
		}
		seasons, numbers = append(seasons, int32(e.Season)), append(numbers, int32(e.Episode))
		stamps, days = append(stamps, e.AirStamp.UTC()), append(days, e.AirDate.UTC())
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		UPDATE titles SET schedule_id = $2, schedule_at = now(), schedule_error = '', updated_at = now() WHERE id = $1`,
		titleID, showID); err != nil {
		return 0, err
	}
	const listed = `unnest($2::int[], $3::int[], $4::timestamptz[], $5::date[]) AS s(season, episode, stamp, day)`
	if _, err := tx.Exec(ctx, `
		UPDATE acquisition_targets a SET air_stamp = s.stamp, updated_at = now()
		  FROM `+listed+`
		 WHERE a.title_id = $1 AND a.kind = 'episode' AND a.season_number = s.season AND a.episode_number = s.episode
		   AND a.air_date IS NOT NULL AND abs(a.air_date - s.day) <= 1 AND a.air_stamp IS DISTINCT FROM s.stamp`,
		titleID, seasons, numbers, stamps, days); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE acquisition_targets a SET air_stamp = NULL, updated_at = now()
		 WHERE a.title_id = $1 AND a.air_stamp IS NOT NULL
		   AND NOT EXISTS (SELECT 1 FROM `+listed+`
		                    WHERE s.season = a.season_number AND s.episode = a.episode_number
		                      AND a.air_date IS NOT NULL AND abs(a.air_date - s.day) <= 1)`,
		titleID, seasons, numbers, stamps, days); err != nil {
		return 0, err
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM acquisition_targets WHERE title_id = $1 AND air_stamp IS NOT NULL`,
		titleID).Scan(&stamped); err != nil {
		return 0, err
	}
	return stamped, tx.Commit(ctx)
}

// ScheduleFailed records why a series' air times could not be read. unknown
// is set when the source does not know the show: its id is forgotten, and it
// is looked up again after a week.
func (s *Store) ScheduleFailed(ctx context.Context, titleID, reason string, unknown bool) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE titles SET schedule_error = $2,
		       schedule_id = CASE WHEN $3 THEN NULL ELSE schedule_id END,
		       schedule_at = CASE WHEN $3 THEN now() ELSE schedule_at END, updated_at = now()
		 WHERE id = $1`, titleID, clipText(reason), unknown)
	return err
}
