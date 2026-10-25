// Package zones tells the time zone a title's days are counted in: the zone
// of the country it comes from, as TMDB names it (origin_country). TMDB dates
// an episode by its day, as broadcast where it comes from; the day begins at
// midnight there, not at midnight UTC — a broadcast in Tokyo on the 23rd may
// be out before the 23rd begins in UTC. It also tells the hour a streaming
// service of a country puts an episode out on the day it is dated, where that
// hour is the rule (Drop).
package zones

import (
	"strings"
	"sync"
	"time"

	// The zone database in the binary: the image may carry none.
	_ "time/tzdata"
)

// byCountry is the zone of each country acquire knows (ISO 3166-1 alpha-2).
// For a country of several zones it is the first, its main one: where most of
// its broadcasts are scheduled — the United States New York, Canada Toronto,
// Mexico Mexico City, Brazil São Paulo, Australia Sydney, Russia Moscow,
// Indonesia Jakarta.
var byCountry = map[string]string{
	"US": "America/New_York", "CA": "America/Toronto", "MX": "America/Mexico_City",
	"BR": "America/Sao_Paulo", "AR": "America/Argentina/Buenos_Aires", "CL": "America/Santiago",
	"CO": "America/Bogota", "PE": "America/Lima",
	"GB": "Europe/London", "IE": "Europe/Dublin", "IS": "Atlantic/Reykjavik", "PT": "Europe/Lisbon",
	"ES": "Europe/Madrid", "FR": "Europe/Paris", "BE": "Europe/Brussels", "NL": "Europe/Amsterdam",
	"LU": "Europe/Luxembourg", "DE": "Europe/Berlin", "AT": "Europe/Vienna", "CH": "Europe/Zurich",
	"IT": "Europe/Rome", "DK": "Europe/Copenhagen", "NO": "Europe/Oslo", "SE": "Europe/Stockholm",
	"FI": "Europe/Helsinki", "PL": "Europe/Warsaw", "CZ": "Europe/Prague", "SK": "Europe/Bratislava",
	"HU": "Europe/Budapest", "RO": "Europe/Bucharest", "BG": "Europe/Sofia", "GR": "Europe/Athens",
	"UA": "Europe/Kyiv", "TR": "Europe/Istanbul", "RU": "Europe/Moscow",
	"IL": "Asia/Jerusalem", "AE": "Asia/Dubai", "SA": "Asia/Riyadh", "EG": "Africa/Cairo",
	"ZA": "Africa/Johannesburg", "NG": "Africa/Lagos", "KE": "Africa/Nairobi",
	"IN": "Asia/Kolkata", "PK": "Asia/Karachi", "TH": "Asia/Bangkok", "VN": "Asia/Ho_Chi_Minh",
	"ID": "Asia/Jakarta", "MY": "Asia/Kuala_Lumpur", "SG": "Asia/Singapore", "PH": "Asia/Manila",
	"CN": "Asia/Shanghai", "HK": "Asia/Hong_Kong", "TW": "Asia/Taipei", "KR": "Asia/Seoul",
	"JP": "Asia/Tokyo", "AU": "Australia/Sydney", "NZ": "Pacific/Auckland",
}

var (
	mu     sync.Mutex
	loaded = map[string]*time.Location{}
)

// Country is the zone of one country, or nil when the table knows none.
func Country(code string) *time.Location {
	name, ok := byCountry[strings.ToUpper(strings.TrimSpace(code))]
	if !ok {
		return nil
	}
	mu.Lock()
	defer mu.Unlock()
	if loc, ok := loaded[name]; ok {
		return loc
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil
	}
	loaded[name] = loc
	return loc
}

// Of is the zone of the first of countries the table knows — TMDB lists a
// co-production's countries in its own order —, UTC when it knows none.
func Of(countries ...string) *time.Location {
	for _, c := range countries {
		if loc := Country(c); loc != nil {
			return loc
		}
	}
	return time.UTC
}

// Midnight is when day's date begins in loc: 00:00 there — day is a date as
// TMDB writes one, held at 00:00 UTC. Where the clocks jump past midnight
// (Santiago and Cairo, the night their summer time begins), the day begins
// when they jump: the first moment it is that day there.
func Midnight(day time.Time, loc *time.Location) time.Time {
	d := day.UTC()
	m := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, loc)
	if sameDate(m.In(loc), d) {
		return m
	}
	// m is the day before there: the first moment of the day lies within
	// the next hours, found to the second.
	lo, hi := m, m.Add(3*time.Hour)
	if !sameDate(hi.In(loc), d) {
		return m
	}
	for hi.Sub(lo) > time.Second {
		mid := lo.Add(hi.Sub(lo) / 2)
		if sameDate(mid.In(loc), d) {
			hi = mid
		} else {
			lo = mid
		}
	}
	return hi.Truncate(time.Second)
}

func sameDate(t, d time.Time) bool {
	y, m, dd := t.Date()
	return y == d.Year() && m == d.Month() && dd == d.Day()
}

// drops are when a streaming service of a country puts an episode out, as a
// rule, against the day it is dated, in the country's zone (byCountry): the
// earliest, and the usual — in the United States 21:00 in New York the evening
// before at the earliest, a service there putting an episode out then, most at
// midnight on the Pacific coast, 03:00 in New York, on the day. A country it
// does not name has no such rule acquire counts on: an episode's day there
// begins at midnight.
var drops = map[string]struct{ earliest, usual dropAt }{"US": {dropAt{-1, 21}, dropAt{0, 3}}}

// dropAt is an hour on a day before or after the one an episode is dated.
type dropAt struct{ days, hour int }

func (d dropAt) on(day time.Time, loc *time.Location) time.Time {
	on := day.UTC().AddDate(0, 0, d.days)
	return time.Date(on.Year(), on.Month(), on.Day(), d.hour, 0, 0, 0, loc)
}

// Drop is the earliest a streaming service of country puts out an episode
// dated day, as a rule (drops), and the zone it is counted in; ok is false
// for a country without one.
func Drop(day time.Time, country string) (at time.Time, loc *time.Location, ok bool) {
	code := strings.ToUpper(strings.TrimSpace(country))
	d, known := drops[code]
	if loc = Country(code); !known || loc == nil {
		return time.Time{}, nil, false
	}
	return d.earliest.on(day, loc), loc, true
}

// UsualDrop is when most streaming services of country put out an episode
// dated day (drops): a release from before, though not before Drop, is no
// fake as a rule, but less sure; ok is false for a country without one.
func UsualDrop(day time.Time, country string) (at time.Time, loc *time.Location, ok bool) {
	code := strings.ToUpper(strings.TrimSpace(country))
	d, known := drops[code]
	if loc = Country(code); !known || loc == nil {
		return time.Time{}, nil, false
	}
	return d.usual.on(day, loc), loc, true
}
