package release

import (
	"fmt"
	"regexp"
	"strconv"
)

// Coverage is what a release name, or the name of a file in a download, says
// it holds of a series: one episode, a run of episodes in one season
// (S04E07E08, S04E07-E08, S04E07-08), or whole seasons (S01, S01-S03,
// Season 2), which is a season pack.
//
// One grab can hold many episodes, and that is the whole reason this exists:
// resolving a grab to "the" episode it is for loses every other episode a
// season pack brings, and the catalog cannot be handed a file without knowing
// which episodes it is.
type Coverage struct {
	// Found is false when the name says nothing about seasons or episodes.
	Found bool
	// Season is the first season named; SeasonEnd the last one of a pack of
	// several seasons, and Season otherwise.
	Season    int
	SeasonEnd int
	// Episode is the first episode, 0 for whole seasons; EpisodeEnd the last
	// one, Episode for a single episode.
	Episode    int
	EpisodeEnd int
}

// Pack reports whether the name holds whole seasons rather than episodes.
func (c Coverage) Pack() bool { return c.Found && c.Episode == 0 }

// Multi reports whether the name holds more than one episode of one season.
func (c Coverage) Multi() bool { return c.Found && c.Episode > 0 && c.EpisodeEnd > c.Episode }

// Covers reports whether the episode is among what the name holds.
func (c Coverage) Covers(season, episode int) bool {
	switch {
	case !c.Found:
		return false
	case c.Pack():
		return season >= c.Season && season <= c.SeasonEnd
	default:
		return season == c.Season && episode >= c.Episode && episode <= c.EpisodeEnd
	}
}

// String names the coverage as scene names do: S04E07, S04E07-E08, S01,
// S01-S03. "" when nothing was found.
func (c Coverage) String() string {
	switch {
	case !c.Found:
		return ""
	case c.Pack() && c.SeasonEnd > c.Season:
		return fmt.Sprintf("S%02d-S%02d", c.Season, c.SeasonEnd)
	case c.Pack():
		return fmt.Sprintf("S%02d", c.Season)
	case c.Multi():
		return fmt.Sprintf("S%02dE%02d-E%02d", c.Season, c.Episode, c.EpisodeEnd)
	default:
		return fmt.Sprintf("S%02dE%02d", c.Season, c.Episode)
	}
}

var (
	// S04E07, S04.E07, s4e7: the first episode of a name.
	coverEpisodeRE = regexp.MustCompile(`(?i)\bS(\d{1,3})[ ._]?E(\d{1,4})`)
	// After it, one more episode (E08, -E08, .E08) or the end of a range: -08,
	// or -S04E08. A digit run that runs on into letters (-720p) is not one.
	coverNextRE  = regexp.MustCompile(`(?i)^[ ._-]?E(\d{1,4})`)
	coverRangeRE = regexp.MustCompile(`(?i)^-(?:S\d{1,3}E)?(\d{1,4})\b`)
	// 4x07, and 4x07-08 or 4x07-4x08. \b on both sides keeps 1920x1080 out.
	coverCrossRE      = regexp.MustCompile(`(?i)\b(\d{1,2})x(\d{2,3})\b`)
	coverCrossRangeRE = regexp.MustCompile(`(?i)^-(?:\d{1,2}x)?(\d{2,3})\b`)
	// Whole seasons: S01, S01-S03, S01-03, Season 2, Season 1-3, Seasons 1-3.
	coverSeasonRE     = regexp.MustCompile(`(?i)\bS(\d{1,3})(?:[ ._]?-[ ._]?S?(\d{1,3}))?\b`)
	coverSeasonWordRE = regexp.MustCompile(`(?i)\bSeasons?[ ._]?(\d{1,3})(?:[ ._]?-[ ._]?(\d{1,3}))?\b`)
)

// maxRun bounds how far a range may reach. Anything wider is a misreading of
// something else in the name, not a release of sixty episodes.
const maxRun = 60

// ParseCoverage reads what a release or file name holds of a series.
func ParseCoverage(name string) Coverage {
	if m := coverEpisodeRE.FindStringSubmatchIndex(name); m != nil {
		season, first := atoi(name[m[2]:m[3]]), atoi(name[m[4]:m[5]])
		last := first
		rest := name[m[1]:]
		for {
			if n := coverNextRE.FindStringSubmatch(rest); n != nil {
				if e := atoi(n[1]); e > last && e-first <= maxRun {
					last = e
				}
				rest = rest[len(n[0]):]
				continue
			}
			if n := coverRangeRE.FindStringSubmatch(rest); n != nil {
				if e := atoi(n[1]); e > last && e-first <= maxRun {
					last = e
				}
				rest = rest[len(n[0]):]
				continue
			}
			break
		}
		return Coverage{Found: true, Season: season, SeasonEnd: season, Episode: first, EpisodeEnd: last}
	}
	if m := coverCrossRE.FindStringSubmatchIndex(name); m != nil {
		season, first := atoi(name[m[2]:m[3]]), atoi(name[m[4]:m[5]])
		last := first
		if n := coverCrossRangeRE.FindStringSubmatch(name[m[1]:]); n != nil {
			if e := atoi(n[1]); e > first && e-first <= maxRun {
				last = e
			}
		}
		return Coverage{Found: true, Season: season, SeasonEnd: season, Episode: first, EpisodeEnd: last}
	}
	for _, re := range []*regexp.Regexp{coverSeasonRE, coverSeasonWordRE} {
		if m := re.FindStringSubmatch(name); m != nil {
			first := atoi(m[1])
			last := first
			if m[2] != "" {
				if e := atoi(m[2]); e > first && e-first <= maxRun {
					last = e
				}
			}
			return Coverage{Found: true, Season: first, SeasonEnd: last}
		}
	}
	return Coverage{}
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
