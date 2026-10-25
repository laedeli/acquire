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
