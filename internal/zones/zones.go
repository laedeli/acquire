// Package zones tells the time zone a title's days are counted in: the zone
// of the country it comes from, as TMDB names it (origin_country). TMDB dates
// an episode by its day, as broadcast where it comes from; the day begins at
// midnight there, not at midnight UTC — a broadcast in Tokyo on the 23rd may
// be out before the 23rd begins in UTC.
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

// Midnight is 00:00 of day's date in loc: day is a date as TMDB writes one,
// held at 00:00 UTC.
func Midnight(day time.Time, loc *time.Location) time.Time {
	d := day.UTC()
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, loc)
}
