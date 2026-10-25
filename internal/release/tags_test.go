package release

import (
	"fmt"
	"testing"
)

// After a movie's year, or after the episodes a series' release holds, a
// release of the title names tags only — and the episode's title, when it is
// given; anything else is something else of the title. The names are
// invented.
func TestTagsOnly(t *testing.T) {
	for _, tc := range []struct {
		rest, lead string
		want       bool
	}{
		{".1080p.WEB-DL.DDP5.1.H.264-GRP", "", true},
		{".2160p.AMZN.WEB-DL.DV.HDR10.DDP5.1.Atmos.H.265-GRP", "", true},
		{".PROPER.REPACK.720p.HDTV.x264-GRP", "", true},
		{".German.DL.1080p.BluRay.x264-GRP", "", true},
		{" (1080p BluRay x265 10bit)", "", true},
		{"-GRP", "", true},
		{"", "", true},
		{".The.Long.Night.1080p.WEB.h264-GRP", "The Long Night", true},
		{".The.Long.Night.1080p.WEB.h264-GRP", "", false},
		{".Official.Trailer.1080p.WEB-DL.x264-GRP", "", false},
		{".The.Making.Of.1080p.HDTV.x264-GRP", "", false},
		{".Part.Two.1080p.WEB-DL.x264-GRP", "", false},
	} {
		if got := TagsOnly(tc.rest, tc.lead); got != tc.want {
			t.Errorf("TagsOnly(%q, %q) = %v", tc.rest, tc.lead, got)
		}
	}
}

func TestExtra(t *testing.T) {
	for rest, want := range map[string]string{
		".Official.Trailer.1080p.WEB-DL.x264-GRP": "trailer",
		".Teaser.2160p.WEB-DL-GRP":                "teaser",
		".Featurettes.1080p.BluRay.x264-GRP":      "featurettes",
		".Extras.1080p.BluRay.x264-GRP":           "extras",
		".Bonus.Disc.1080p.BluRay-GRP":            "bonus",
		".The.Making.Of.1080p.HDTV.x264-GRP":      "making of",
		".Behind.The.Scenes.720p.WEB-GRP":         "behind the scenes",
		".SAMPLE.1080p.WEB-DL-GRP":                "sample",
		".Cast.Interview.1080p.WEB-GRP":           "interview",
		".Promo.720p.HDTV-GRP":                    "promo",
		".1080p.WEB-DL.DDP5.1.H.264-GRP":          "",
		".Making.Waves.1080p.WEB-GRP":             "",
	} {
		if got := Extra(rest); got != want {
			t.Errorf("Extra(%q) = %q, want %q", rest, got, want)
		}
	}
}

// A name is read at each of its years: a title may hold a year of its own.
func TestSplitYears(t *testing.T) {
	got := fmt.Sprint(SplitYears("Example.Saga.1999.Return.2026.1080p.WEB-DL-GRP"))
	if want := "[{Example Saga 1999 .Return.2026.1080p.WEB-DL-GRP} {Example Saga 1999 Return 2026 .1080p.WEB-DL-GRP}]"; got != want {
		t.Errorf("SplitYears = %s\nwant          %s", got, want)
	}
	if got := SplitYears("2026.Example.1080p"); len(got) != 0 {
		t.Errorf("a name that starts with a year: %v", got)
	}
	if got := SplitYears("Example.Film.1080p.WEB"); len(got) != 0 {
		t.Errorf("a name without a year: %v", got)
	}
	for in, want := range map[string]string{
		"Example.Film.2026.1080p.WEB-DL.x264-GRP":   "Example.Film.2026.1080p.WEB-DL.x264",
		"Example.Film.2026.1080p.WEB-DL-GRP [site]": "Example.Film.2026.1080p.WEB-DL",
		"Example Film (2026) [1080p]":               "Example Film (2026)",
	} {
		if got := StripGroup(in); got != want {
			t.Errorf("StripGroup(%q) = %q, want %q", in, got, want)
		}
	}
	head, cov, rest := SplitSeries("Example.Show.S01E03E04.The.Long.Night.1080p-GRP")
	if head != "Example Show" || cov.String() != "S01E03-E04" || rest != ".The.Long.Night.1080p-GRP" {
		t.Errorf("SplitSeries = %q %s %q", head, cov, rest)
	}
}
