package zones

import (
	"testing"
	"time"
)

// A day begins at midnight where the title comes from: in Tokyo nine hours
// before UTC, in New York four or five hours after.
func TestMidnightWhereATitleComesFrom(t *testing.T) {
	day := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		countries []string
		want      string
	}{
		{[]string{"JP"}, "2026-10-22T15:00:00Z"},
		{[]string{"US"}, "2026-10-23T04:00:00Z"},
		{[]string{"gb"}, "2026-10-22T23:00:00Z"},
		{[]string{"XX", "KR"}, "2026-10-22T15:00:00Z"},
		{[]string{"XX"}, "2026-10-23T00:00:00Z"},
		{nil, "2026-10-23T00:00:00Z"},
	} {
		if got := Midnight(day, Of(tc.countries...)).UTC().Format(time.RFC3339); got != tc.want {
			t.Errorf("%v: %s, want %s", tc.countries, got, tc.want)
		}
	}
	// Every zone of the table loads from the database in the binary.
	for code, name := range byCountry {
		if Country(code) == nil {
			t.Errorf("%s: %s does not load", code, name)
		}
	}
}

// A day begins at midnight in its zone across the turns of the clocks: in
// New York before and after the autumn's turn and on the spring's, in London
// on the autumn's, in Sydney on the spring's; in Santiago, whose clocks jump
// from midnight to one o'clock, at that one o'clock. A country the table does
// not know counts in UTC.
func TestMidnightAcrossTheClocksTurning(t *testing.T) {
	for _, tc := range []struct {
		day, country, want string
	}{
		{"2026-11-01", "US", "2026-11-01T04:00:00Z"},
		{"2026-11-02", "US", "2026-11-02T05:00:00Z"},
		{"2027-03-14", "US", "2027-03-14T05:00:00Z"},
		{"2026-10-25", "GB", "2026-10-24T23:00:00Z"},
		{"2026-10-04", "AU", "2026-10-03T14:00:00Z"},
		{"2026-09-06", "CL", "2026-09-06T04:00:00Z"},
		{"2026-09-07", "CL", "2026-09-07T03:00:00Z"},
		{"2026-10-25", "ZZ", "2026-10-25T00:00:00Z"},
	} {
		d, _ := time.Parse("2006-01-02", tc.day)
		loc := Of(tc.country)
		m := Midnight(d, loc)
		if got := m.UTC().Format(time.RFC3339); got != tc.want {
			t.Errorf("%s %s: %s, want %s", tc.country, tc.day, got, tc.want)
		}
		if !sameDate(m.In(loc), d) || sameDate(m.Add(-time.Second).In(loc), d) {
			t.Errorf("%s %s: %s is not the day's first moment there", tc.country, tc.day, m.In(loc))
		}
	}
}

// A streaming service of the United States puts an episode out at 03:00 in
// New York, midnight on the Pacific coast, on the day it is dated; of a
// country without such a rule, nothing is said.
func TestWhenAStreamingServicePutsAnEpisodeOut(t *testing.T) {
	d := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	at, loc, ok := Drop(d, "us")
	if !ok || loc.String() != "America/New_York" || at.UTC().Format(time.RFC3339) != "2026-10-23T07:00:00Z" {
		t.Errorf("in the US: %s %v %v", at.UTC(), loc, ok)
	}
	if at, _, ok := Drop(time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC), "US"); !ok || at.UTC().Format(time.RFC3339) != "2026-11-06T08:00:00Z" {
		t.Errorf("in the US in winter: %s", at.UTC())
	}
	for _, c := range []string{"DE", "", "ZZ"} {
		if _, _, ok := Drop(d, c); ok {
			t.Errorf("%q has a drop hour", c)
		}
	}
}
