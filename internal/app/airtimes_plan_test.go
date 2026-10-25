package app

import (
	"strings"
	"testing"
	"time"

	"github.com/laedeli/acquire/internal/store"
	"github.com/laedeli/acquire/internal/tvmaze"
)

// What a read of TVmaze makes of each episode: its moment when the two agree
// on the day; TVmaze's day and hour when it dates the episode more than a day
// later — TMDB has not caught up with a move —; TMDB's day when TVmaze's is
// more than a day earlier, the later of the two; held when the two are days
// apart and cannot be told the same episode, unless it is of the old backlog,
// and held when TMDB dates it recently and TVmaze nowhere — with no date,
// not any more, or never. A broadcast's episode without an hour from TVmaze —
// dated to the day alone, or of the old backlog and not listed — gets its
// season's usual hour, else the show's, in its network's zone; without one, a
// recent one is held. A streamed one without an hour is told by its day. The
// ids are invented.
func TestWhatAReadOfTVmazeMakesOfAnEpisode(t *testing.T) {
	now := time.Now()
	ny, _ := time.LoadLocation("America/New_York")
	at := func(d, hh, mm int) *time.Time {
		v := local(d, hh, mm, "America/New_York").UTC()
		return &v
	}
	ep := func(id int64, season, number, d int, hour, name string) tvmaze.Episode {
		day := *day(d)
		e := tvmaze.Episode{ID: id, Season: season, Number: number, Name: name, AirDate: &day, AirTime: hour, Runtime: 60}
		if hour != "" {
			h, _ := time.Parse("15:04", hour)
			st := local(d, h.Hour(), h.Minute(), "America/New_York").UTC()
			e.AirStamp = &st
		}
		return e
	}
	target := func(season, number, d int, title string, scheduleID int64) store.Target {
		s, n := season, number
		return store.Target{ID: "x:" + title, Kind: "episode", SeasonNumber: &s, EpisodeNumber: &n, AirDate: day(d), EpisodeTitle: title,
			ScheduleEpisodeID: scheduleID}
	}
	show := tvmaze.Show{ID: 501, Name: "Example Night Show", Kind: tvmaze.Broadcast, Country: "US", Zone: "America/New_York", Runtime: 60,
		ScheduleTime: "20:00", Episodes: []tvmaze.Episode{
			ep(1, 1, 1, -2, "21:00", "One"),
			ep(2, 1, 2, 12, "21:00", "Two"),       // a week later than TMDB's day 5
			ep(3, 1, 3, -9, "21:00", "Three"),     // a week before TMDB's day -2
			ep(4, 1, 4, 13, "21:00", "Something"), // a week after TMDB's, by another name
			ep(5, 1, 5, 6, "", "Five"),            // to the day alone
			ep(8, 1, 8, -70, "21:00", "Old"),      // of the old backlog, by another name
			ep(9, 1, 9, 8, "21:00", "Nine"),       // TVmaze numbers it E09; it was E07 at the last read
			ep(10, 1, 7, 9, "21:00", "Other"),     // at E07 now, another episode
			ep(20, 2, 1, -100, "", ""),            // season 2: no hour at all
		}}
	targets := []store.Target{
		target(1, 1, -2, "One", 0),
		target(1, 2, 5, "Two", 0),
		target(1, 3, -2, "Three", 0),
		target(1, 4, 6, "Four", 0),
		target(1, 5, 6, "Five", 0),
		target(1, 6, 7, "Six", 0), // not listed, TVmaze listing later ones: held
		target(1, 7, 8, "Nine", 9),
		target(1, 8, -60, "Eight", 0),
		target(2, 1, -1, "", 0), // a season without an hour: the show's
	}
	plans := map[string]store.ScheduledTarget{}
	for _, p := range planSchedule(store.Title{Kind: "series", Countries: []string{"US"}}, show, targets, now) {
		plans[strings.TrimPrefix(p.TargetID, "x:")] = p
	}
	check := func(name string, ok bool, p store.ScheduledTarget) {
		t.Helper()
		if !ok {
			t.Errorf("%s: %+v (stamp %v, day %v)", name, p, p.Stamp, p.Day)
		}
	}
	p := plans["One"]
	check("the two agree: TVmaze's moment", p.Stamp != nil && p.Stamp.Equal(*at(-2, 21, 0)) && !p.Estimated && p.Day == nil && p.Hold == "" && p.EpisodeID == 1, p)
	p = plans["Two"]
	check("TVmaze a week later: its day and hour", p.Stamp != nil && p.Stamp.Equal(*at(12, 21, 0)) && p.Day != nil && p.Day.Equal(*day(12)) && p.Hold == "", p)
	p = plans["Three"]
	check("TVmaze a week earlier: TMDB's day, at the usual hour", p.Stamp != nil && p.Stamp.Equal(*at(-2, 21, 0)) && p.Estimated && p.Day == nil && p.Hold == "", p)
	p = plans["Four"]
	check("a week apart, another name: held", p.Stamp == nil && strings.HasPrefix(p.Hold, "TVmaze dates an episode S01E04 on "+dateOf(day(13))+", TMDB on "+dateOf(day(6))+
		", and they may not be the same episode"), p)
	p = plans["Five"]
	check("to the day alone: the usual hour", p.Stamp != nil && p.Stamp.Equal(*at(6, 21, 0)) && p.Estimated, p)
	p = plans["Six"]
	check("not listed, later ones listed: held", p.Stamp == nil && p.Hold == "TVmaze does not list it, though it lists later episodes of its season: "+
		"no automatic grab takes it until it does", p)
	p = plans["Nine"]
	check("by the episode taken before, renumbered at TVmaze", p.EpisodeID == 9 && p.Stamp != nil && p.Stamp.Equal(*at(8, 21, 0)) && p.Hold == "", p)
	p = plans["Eight"]
	check("of the old backlog, another name: TMDB's day, not held", p.Hold == "" && p.Day == nil && p.Stamp != nil && p.Estimated && p.Stamp.Equal(*at(-60, 21, 0)), p)
	p = plans[""]
	check("a season without an hour: the show's", p.Stamp != nil && p.Estimated && p.Stamp.Equal(*at(-1, 20, 0)), p)

	// The episode taken before is gone, and another sits at its numbers,
	// dated apart: held.
	gone := target(1, 7, 2, "Nine", 99)
	ps := planSchedule(store.Title{Kind: "series"}, show, []store.Target{gone}, now)
	if len(ps) != 1 || ps[0].Hold == "" || ps[0].EpisodeID != 10 {
		t.Errorf("the episode taken before gone: %+v", ps)
	}
	// TVmaze listing nothing: a recent episode is held; an old one is told by
	// its day, without a usual hour to estimate one by.
	bare := show
	bare.ScheduleTime = ""
	bare.Episodes = nil
	ps = planSchedule(store.Title{Kind: "series"}, bare, []store.Target{target(1, 1, -2, "One", 0), target(1, 2, -90, "Two", 0)}, now)
	if len(ps) != 2 || ps[0].Hold != "TVmaze does not list it: no automatic grab takes it until it does" || ps[0].Stamp != nil ||
		ps[1].Hold != "" || ps[1].Stamp != nil {
		t.Errorf("TVmaze listing nothing: %+v", ps)
	}
	// Dated to the day alone, no usual hour: held.
	dayOnly := bare
	dayOnly.Episodes = []tvmaze.Episode{ep(1, 1, 1, -2, "", "One")}
	ps = planSchedule(store.Title{Kind: "series"}, dayOnly, []store.Target{target(1, 1, -2, "One", 0)}, now)
	if len(ps) != 1 || !strings.HasPrefix(ps[0].Hold, "TVmaze dates it to the day alone, and the series' broadcasts have no usual hour") {
		t.Errorf("no usual hour: %+v", ps)
	}
	// TVmaze lists it with no date — postponed —, or lists it no more: held
	// while TMDB's day is recent; of the old backlog, not.
	tba := show
	tba.Episodes = append(append([]tvmaze.Episode(nil), show.Episodes...), tvmaze.Episode{ID: 30, Season: 3, Number: 1, Name: "Postponed"},
		tvmaze.Episode{ID: 31, Season: 3, Number: 3, Name: "Old, postponed"})
	ps = planSchedule(store.Title{Kind: "series"}, tba, []store.Target{target(3, 1, -2, "Postponed", 0), target(3, 2, -2, "Pulled", 77),
		target(3, 3, -60, "Old, postponed", 0)}, now)
	if len(ps) != 3 || ps[0].Hold != "TVmaze lists it with no date — postponed, perhaps —: no automatic grab takes it until TVmaze dates it" ||
		ps[0].Stamp != nil || ps[0].EpisodeID != 30 ||
		ps[1].Hold != "TVmaze listed it and does not any more — pulled, perhaps —: no automatic grab takes it until TVmaze lists it again" ||
		ps[2].Hold != "" {
		t.Errorf("dated nowhere: %+v", ps)
	}
	// Streamed: no hour is estimated; its day tells — but one TVmaze does not
	// list is held as any other.
	stream := show
	stream.Kind = tvmaze.Streaming
	ps = planSchedule(store.Title{Kind: "series"}, stream, []store.Target{target(1, 6, 7, "Six", 0), target(1, 5, 6, "Five", 0)}, now)
	if len(ps) != 2 || ps[0].Stamp != nil || ps[0].Hold == "" || ps[1].Stamp != nil || ps[1].Hold != "" {
		t.Errorf("streamed: %+v", ps)
	}
	// The zone of the show's hours: its network's; else its country's;
	// else where the series comes from.
	if z := scheduleZone(tvmaze.Show{Zone: "Asia/Tokyo"}, store.Title{}); z.String() != "Asia/Tokyo" {
		t.Errorf("zone: %s", z)
	}
	if z := scheduleZone(tvmaze.Show{Country: "GB"}, store.Title{}); z.String() != "Europe/London" {
		t.Errorf("zone by country: %s", z)
	}
	if z := scheduleZone(tvmaze.Show{}, store.Title{Countries: []string{"US"}}); z.String() != ny.String() {
		t.Errorf("zone by origin: %s", z)
	}
}

// The usual hour of a season is the one most of its episodes air at — of two
// as often, the later episode's.
func TestTheUsualHourOfASeason(t *testing.T) {
	ep := func(season, d int, hour string) tvmaze.Episode {
		return tvmaze.Episode{Season: season, AirDate: day(d), AirTime: hour}
	}
	got := usualHours(tvmaze.Show{Episodes: []tvmaze.Episode{
		ep(1, -30, "21:00"), ep(1, -23, "21:00"), ep(1, -16, "22:00"),
		ep(2, -9, "20:00"), ep(2, -2, "21:30"),
		ep(3, -1, ""),
	}})
	if got[1] != "21:00" || got[2] != "21:30" || got[3] != "" || len(got) != 2 {
		t.Errorf("usual hours: %v", got)
	}
}

// TMDB's and TVmaze's names of an episode can be of the same one: equal as
// release names compare, one holding the other, or either naming none.
func TestTheSameEpisodeName(t *testing.T) {
	for _, tc := range []struct {
		tmdb, tvm string
		same      bool
	}{
		{"The Harbour", "The Harbour", true},
		{"The Harbour (Part 1)", "the harbour", true},
		{"", "Anything", true},
		{"Episode 5", "The Harbour", true},
		{"The Harbour", "Episode #1.5", true},
		{"The Harbour", "TBA", true},
		{"The Harbour", "The Lighthouse", false},
	} {
		if got := sameEpisodeName(tc.tmdb, tc.tvm); got != tc.same {
			t.Errorf("%q / %q: %v", tc.tmdb, tc.tvm, got)
		}
	}
}
