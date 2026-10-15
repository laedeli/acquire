package release

import (
	"reflect"
	"testing"
)

// ep is the coverage of the episodes named, in one season.
func ep(season int, episodes ...int) Coverage {
	return Coverage{Found: true, Season: season, SeasonEnd: season,
		Episode: episodes[0], EpisodeEnd: episodes[len(episodes)-1], Episodes: episodes}
}

// run is the coverage of the episodes first..last in a row.
func run(season, first, last int) Coverage {
	var eps []int
	for e := first; e <= last; e++ {
		eps = append(eps, e)
	}
	return ep(season, eps...)
}

// pack is the coverage of whole seasons.
func pack(seasons ...int) Coverage {
	return Coverage{Found: true, Season: seasons[0], SeasonEnd: seasons[len(seasons)-1], Seasons: seasons}
}

func TestParseCoverage(t *testing.T) {
	cases := map[string]Coverage{
		// One episode, however it is written.
		"Show.Name.S04E07.1080p.WEB-DL.x265-GRP":   ep(4, 7),
		"Show Name - S04E07 - The Title.mkv":       ep(4, 7),
		"show.name.s4e7.720p.hdtv":                 ep(4, 7),
		"Show.Name.S04.E07.1080p":                  ep(4, 7),
		"Show.Name.S01-E05.1080p.WEB":              ep(1, 5),
		"Show Name S01 EP05 1080p":                 ep(1, 5),
		"Show.Name.S01.EP05.720p":                  ep(1, 5),
		"Show.Name.S01EP05.720p":                   ep(1, 5),
		"Show Name Season 1 Episode 5 1080p":       ep(1, 5),
		"Show.Name.Season.01.Episode.05.WEB":       ep(1, 5),
		"Show.Name.4x07.HDTV.x264-GRP":             ep(4, 7),
		"Show.Name.1x01.HDTV":                      ep(1, 1),
		"Show.Name.S04E07.Episode.Title.1080p-GRP": ep(4, 7),
		"Show.Name.S02E10.ENG.1080p":               ep(2, 10),
		"Show.Name.S00E01.Special.1080p":           ep(0, 1),
		"Show.Name.2019.S01E05.1080p":              ep(1, 5),
		// A file that covers several episodes in a row.
		"Show.Name.S04E07E08.1080p.WEB-DL-GRP":  run(4, 7, 8),
		"Show.Name.S04E07-E08.1080p":            run(4, 7, 8),
		"Show.Name.S04E07-08.1080p":             run(4, 7, 8),
		"Show.Name.S04E07E08E09.720p":           run(4, 7, 9),
		"Show.Name.S04E07-S04E08.1080p":         run(4, 7, 8),
		"Show.Name.4x07-08.HDTV":                run(4, 7, 8),
		"Show.Name.1x01x02.HDTV":                run(1, 1, 2),
		"Show.Name.S01E01-03.720p":              run(1, 1, 3),
		"Show.Name.S01E01-04.Complete.Arc.720p": run(1, 1, 4),
		"Show.Name.S01E01-E12.1080p.WEB-DL":     run(1, 1, 12),
		// A list that is not one run: the episodes it names, and no others.
		"Show.Name.S01E01E03.1080p": ep(1, 1, 3),
		// A resolution or a year after the episode is no range.
		"Show.Name.S04E07-720p.WEB":  ep(4, 7),
		"Show.Name.S04E07-2160p.WEB": ep(4, 7),
		"Show.Name.S04E07-2021.WEB":  ep(4, 7),
		// Whole seasons.
		"Show.Name.S01.1080p.BluRay.x265-GRP":     pack(1),
		"Show.Name.S01-S03.1080p.WEB-DL":          pack(1, 2, 3),
		"Show.Name.S01-03.720p":                   pack(1, 2, 3),
		"Show.Name.S01.S02.S03.1080p.BluRay":      pack(1, 2, 3),
		"Show Name Season 2 Complete 1080p":       pack(2),
		"Show.Name.Seasons.1-4.720p":              pack(1, 2, 3, 4),
		"Show.Name.S01-1080p.WEB":                 pack(1),
		"Show.Name.S01.COMPLETE.2160p.WEB-DL-GRP": pack(1),
		// Nothing of a series.
		"Show.Name.Complete.Series.1080p.BluRay":   {},
		"Movie.Title.2020.1080p.BluRay.x265-GRP":   {},
		"Movie.Title.2020.1920x1080.WEB-DL":        {},
		"Movie.Title.2020.DTS-HD.MA.5.1.1080p-GRP": {},
		"Blade.Runner.2049.2017.1080p.BluRay":      {},
		"Daily.Show.2024.05.12.Guest.720p.WEB":     {},
	}
	for name, want := range cases {
		if got := ParseCoverage(name); !reflect.DeepEqual(got, want) {
			t.Errorf("ParseCoverage(%q) = %+v, want %+v", name, got, want)
		}
	}
}

func TestCoverageCovers(t *testing.T) {
	multi := ParseCoverage("Show.S04E07E08.1080p")
	if !multi.Covers(4, 7) || !multi.Covers(4, 8) || multi.Covers(4, 9) || multi.Covers(3, 7) {
		t.Errorf("multi-episode coverage wrong: %+v", multi)
	}
	if !multi.Multi() || !multi.Run() || multi.Pack() || multi.String() != "S04E07-E08" {
		t.Errorf("multi-episode is %+v %q", multi, multi.String())
	}
	list := ParseCoverage("Show.S01E01E03.1080p")
	if !list.Covers(1, 1) || list.Covers(1, 2) || !list.Covers(1, 3) || list.Run() || !list.Multi() || list.String() != "S01E01E03" {
		t.Errorf("a list is not a run: %+v %q", list, list.String())
	}
	packs := ParseCoverage("Show.S01-S03.1080p")
	if !packs.Covers(2, 99) || packs.Covers(4, 1) || !packs.Pack() || packs.String() != "S01-S03" {
		t.Errorf("season packs: %+v %q", packs, packs.String())
	}
	if one := ParseCoverage("Show.S02.720p"); one.String() != "S02" || !one.Covers(2, 1) {
		t.Errorf("one season: %+v", one)
	}
	if none := ParseCoverage("Movie.2020.1080p"); none.Covers(1, 1) || none.String() != "" {
		t.Errorf("no coverage: %+v", none)
	}
	if single := ParseCoverage("Show.S01E05.1080p"); single.Multi() || !single.Run() || single.String() != "S01E05" {
		t.Errorf("single: %+v %q", single, single.String())
	}
	if c := ParseCoverage("Show.S01-E05.1080p"); c.Pack() || c.Covers(1, 4) {
		t.Errorf("S01-E05 is one episode, not season 1: %+v", c)
	}
}
