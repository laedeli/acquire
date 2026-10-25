package release

import "testing"

func TestMatchKey(t *testing.T) {
	for in, want := range map[string]string{
		"Example Saga: Seven Harbours": "example saga seven harbours",
		"Example.Saga.Seven.Harbours":  "example saga seven harbours",
		"Example Show - The Return":    "example show the return",
		"Keeper's Lantern":             "keepers lantern",
		"Keepers.Lantern":              "keepers lantern",
		"Salt & Cedar":                 "salt and cedar",
		"Salt&Cedar":                   "salt and cedar",
		"Café Meridian":                "cafe meridian",
		"Die Straße":                   "die strasse",
		"Show (US)":                    "show us",
		"  Mixed   CASE__words  ":      "mixed case words",
		"":                             "",
		"Über die Brücke: Example":     "uber die brucke example",
	} {
		if got := MatchKey(in); got != want {
			t.Errorf("MatchKey(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]int{"2019": 2019, "1999": 1999, "2160": 0, "1080": 0, "20190": 0, "s01": 0} {
		if got := YearOf(in); got != want {
			t.Errorf("YearOf(%q) = %d, want %d", in, got, want)
		}
	}
}

// What a release name says of a series is what comes before the episodes or
// seasons it holds.
func TestSeriesPart(t *testing.T) {
	for name, want := range map[string]struct {
		series, covers string
	}{
		"Example.Show.S01E03.1080p.WEB.h264-GRP":    {"Example Show", "S01E03"},
		"Example.Show.2024.S01E03.1080p.WEB":        {"Example Show 2024", "S01E03"},
		"Example Show - S01E03 - Title [1080p]":     {"Example Show", "S01E03"},
		"Example.Show.US.S02E01E02.720p.HDTV":       {"Example Show US", "S02E01-E02"},
		"Example.Show.1x07.HDTV":                    {"Example Show", "S01E07"},
		"Example.Show.S03.1080p.BluRay":             {"Example Show", "S03"},
		"Example Show Season 2 Episode 4 1080p":     {"Example Show", "S02E04"},
		"Example.Movie.2026.1080p.WEB-DL":           {"", ""},
		"S01E01.1080p.WEB":                          {"", "S01E01"},
		"Example.Saga.Seven.Harbours.S01E10.2160p.": {"Example Saga Seven Harbours", "S01E10"},
	} {
		series, cov := SeriesPart(name)
		if series != want.series || cov.String() != want.covers {
			t.Errorf("SeriesPart(%q) = %q %q, want %q %q", name, series, cov.String(), want.series, want.covers)
		}
	}
}
