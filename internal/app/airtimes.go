package app

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/laedeli/acquire/internal/release"
	"github.com/laedeli/acquire/internal/store"
	"github.com/laedeli/acquire/internal/tvmaze"
	"github.com/laedeli/acquire/internal/zones"
)

// What a read of a series' air times makes of each of its episodes.
//
// TVmaze's episode of a target is the one taken for it before
// (schedule_episode_id), while TVmaze lists it — the same episode, whatever
// its numbers there now —, else the one of the same season and number. The
// two dating it:
//
//   - within a day of each other: TVmaze's moment, when it gives the hour;
//   - TVmaze more than a day later: TMDB has not caught up with a move — the
//     episode is told by TVmaze's day and hour;
//   - TVmaze more than a day earlier: the later of the two, TMDB's day — a
//     mismatch only ever delays;
//   - more than a day apart, and not clearly the same episode — another id at
//     its numbers than the one taken before, or a name of TVmaze's neither
//     TMDB's nor a placeholder —: held, no automatic grab takes it until they
//     agree. An episode that aired more than holdWithin ago is not held: of
//     the old backlog, TMDB's day tells.
//
// Of a broadcast — or a series TVmaze names no network or service of —
// without an hour from TVmaze (not listed, or dated to the day alone), the
// hour is estimated from the usual one of its season's broadcasts, else of the
// show's schedule, in its network's zone, on the day it is told by; without
// one, a recent episode is held until it has an air time. A streamed episode
// without an hour is told by its day.

// holdWithin is how recently an episode aired, or how soon it airs, to be
// held for TVmaze: an older one is the old backlog.
const holdWithin = 30 * 24 * time.Hour

// planSchedule is what a read of TVmaze makes of a series' episode targets,
// now.
func planSchedule(title store.Title, show tvmaze.Show, targets []store.Target, now time.Time) []store.ScheduledTarget {
	byID := map[int64]*tvmaze.Episode{}
	byNumbers := map[[2]int]*tvmaze.Episode{}
	for i := range show.Episodes {
		e := &show.Episodes[i]
		byID[e.ID] = e
		byNumbers[[2]int{e.Season, e.Number}] = e
	}
	zone := scheduleZone(show, title)
	slots := usualHours(show)
	estimate := func(season int, day time.Time) *time.Time {
		hour, ok := slots[season]
		if !ok {
			hour = show.ScheduleTime
		}
		at, err := time.Parse("15:04", hour)
		if err != nil {
			return nil
		}
		d := day.UTC()
		est := time.Date(d.Year(), d.Month(), d.Day(), at.Hour(), at.Minute(), 0, 0, zone).UTC()
		return &est
	}
	broadcast := show.Kind != tvmaze.Streaming
	recent := func(day time.Time) bool { return !day.Before(now.Add(-holdWithin)) }

	out := make([]store.ScheduledTarget, 0, len(targets))
	for _, t := range targets {
		if t.Kind != "episode" || t.SeasonNumber == nil || t.EpisodeNumber == nil {
			continue
		}
		season, number := *t.SeasonNumber, *t.EpisodeNumber
		plan := store.ScheduledTarget{TargetID: t.ID, AirDate: t.AirDate, Runtime: show.Runtime}
		ep, sure := byNumbers[[2]int{season, number}], true
		if t.ScheduleEpisodeID != 0 {
			if e, ok := byID[t.ScheduleEpisodeID]; ok {
				ep = e
			} else if ep != nil {
				// Another episode at its numbers than the one taken before.
				sure = false
			}
		} else if ep != nil {
			sure = sameEpisodeName(t.EpisodeTitle, ep.Name)
		}
		if ep != nil {
			plan.EpisodeID, plan.Runtime = ep.ID, ep.Runtime
		}
		if t.AirDate == nil {
			// TMDB dates it nowhere yet: nothing opens it to an automatic
			// grab; what TVmaze says is kept for when it does.
			if ep != nil && ep.AirStamp != nil {
				plan.Stamp = ep.AirStamp
			}
			out = append(out, plan)
			continue
		}
		tmdbDay := t.AirDate.UTC()
		day, stamp := tmdbDay, (*time.Time)(nil)
		if ep != nil && ep.AirDate != nil {
			apart := daysBetween(tmdbDay, *ep.AirDate)
			switch {
			case apart >= -1 && apart <= 1:
				day, stamp = ep.AirDate.UTC(), ep.AirStamp
			case !sure && (recent(tmdbDay) || recent(*ep.AirDate)):
				plan.Hold = clip(fmt.Sprintf("TVmaze dates an episode %s on %s, TMDB on %s, and they may not be the same episode: "+
					"no automatic grab takes it until the two agree", coords(t), dateString(*ep.AirDate), dateString(tmdbDay)), 300)
			case !sure:
				// Of the old backlog: TMDB's day.
			case apart > 1:
				// Moved later, and TMDB has not caught up.
				day, stamp = ep.AirDate.UTC(), ep.AirStamp
				d := ep.AirDate.UTC()
				plan.Day = &d
			}
		}
		switch {
		case plan.Hold != "":
		case stamp != nil:
			plan.Stamp = stamp
		case !broadcast:
			// Streamed, dated to the day: its day tells.
		default:
			if est := estimate(season, day); est != nil {
				plan.Stamp, plan.Estimated = est, true
			} else if recent(day) {
				what := "TVmaze dates it to the day alone"
				if ep == nil {
					what = "TVmaze does not list it"
				}
				plan.Hold = what + ", and the series' broadcasts have no usual hour to tell it by: no automatic grab takes it until it has an air time"
			}
		}
		out = append(out, plan)
	}
	return out
}

// usualHours is, by season, the hour most of its episodes air at as TVmaze
// gives it ("21:00" where its network is) — of two as often, the later
// episode's.
func usualHours(show tvmaze.Show) map[int]string {
	type tally struct {
		n    int
		last time.Time
	}
	by := map[int]map[string]*tally{}
	for _, e := range show.Episodes {
		if e.AirTime == "" || e.AirDate == nil {
			continue
		}
		if by[e.Season] == nil {
			by[e.Season] = map[string]*tally{}
		}
		c := by[e.Season][e.AirTime]
		if c == nil {
			c = &tally{}
			by[e.Season][e.AirTime] = c
		}
		c.n++
		if e.AirDate.After(c.last) {
			c.last = *e.AirDate
		}
	}
	out := map[int]string{}
	for season, hours := range by {
		var best string
		var top *tally
		for h, c := range hours {
			if top == nil || c.n > top.n || (c.n == top.n && c.last.After(top.last)) {
				best, top = h, c
			}
		}
		out[season] = best
	}
	return out
}

// scheduleZone is the zone a show's hours are in: its network's or
// service's, as TVmaze names it, else its country's, else the zone the series
// comes from.
func scheduleZone(show tvmaze.Show, title store.Title) *time.Location {
	if show.Zone != "" {
		if loc, err := time.LoadLocation(show.Zone); err == nil {
			return loc
		}
	}
	if loc := zones.Country(show.Country); loc != nil {
		return loc
	}
	return zones.Of(title.Countries...)
}

// daysBetween is how many days b is after a, both dates held at 00:00 UTC.
func daysBetween(a, b time.Time) int {
	return int(b.UTC().Sub(a.UTC()).Round(24*time.Hour) / (24 * time.Hour))
}

func dateString(d time.Time) string { return d.UTC().Format("2006-01-02") }

// placeholderName is an episode's name, as release names compare
// (release.MatchKey), that names no episode: "Episode 5", "Episode #1.5",
// "TBA".
var placeholderName = regexp.MustCompile(`^(?:(?:episode|ep|chapter|part)(?: \d+)+|tba|tbd|untitled)$`)

// sameEpisodeName reports whether TMDB's and TVmaze's names of an episode can
// be of the same one: either names none (a placeholder, or nothing), or the
// two are one as release names compare (release.MatchKey), or one holds the
// other, whole words.
func sameEpisodeName(tmdb, tvm string) bool {
	a, b := release.MatchKey(tmdb), release.MatchKey(tvm)
	if a == "" || b == "" || placeholderName.MatchString(a) || placeholderName.MatchString(b) || a == b {
		return true
	}
	if len(a) > len(b) {
		a, b = b, a
	}
	return strings.Contains(" "+b+" ", " "+a+" ")
}
