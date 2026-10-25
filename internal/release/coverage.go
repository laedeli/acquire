package release

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Coverage is what a release name, or the name of a file in a download, says
// it holds of a series: one episode, a run of episodes in one season
// (S04E07E08, S04E07-E08, S04E07-08), episodes of one season that are not a
// run (S01E01E03), or whole seasons (S01, S01-S03, S01.S02.S03, Season 2),
// which is a season pack.
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
	// Episodes are the episodes of Season the name holds, in order: for a
	// run every one from Episode to EpisodeEnd, for a list only those named.
	// None for whole seasons.
	Episodes []int
	// Seasons are the whole seasons of a pack, in order.
	Seasons []int
}

// Pack reports whether the name holds whole seasons rather than episodes.
func (c Coverage) Pack() bool { return c.Found && len(c.Episodes) == 0 }

// Multi reports whether the name holds more than one episode of one season.
func (c Coverage) Multi() bool { return len(c.Episodes) > 1 }

// Run reports whether the name holds episodes in a row — one, or several
// without a gap — which is what one file of several episodes can be.
func (c Coverage) Run() bool {
	return len(c.Episodes) > 0 && c.EpisodeEnd-c.Episode+1 == len(c.Episodes)
}

// Covers reports whether the episode is among what the name holds.
func (c Coverage) Covers(season, episode int) bool {
	switch {
	case !c.Found:
		return false
	case c.Pack():
		return contains(c.Seasons, season)
	default:
		return season == c.Season && contains(c.Episodes, episode)
	}
}

// String names the coverage as scene names do: S04E07, S04E07-E08,
// S01E01E03, S01, S01-S03 (S01+S03 for seasons not in a row). "" when nothing
// was found.
func (c Coverage) String() string {
	switch {
	case !c.Found:
		return ""
	case c.Pack() && len(c.Seasons) > 1 && c.SeasonEnd-c.Season+1 == len(c.Seasons):
		return fmt.Sprintf("S%02d-S%02d", c.Season, c.SeasonEnd)
	case c.Pack():
		parts := make([]string, len(c.Seasons))
		for i, s := range c.Seasons {
			parts[i] = fmt.Sprintf("S%02d", s)
		}
		return strings.Join(parts, "+")
	case c.Multi() && c.Run():
		return fmt.Sprintf("S%02dE%02d-E%02d", c.Season, c.Episode, c.EpisodeEnd)
	default:
		var b strings.Builder
		fmt.Fprintf(&b, "S%02d", c.Season)
		for _, e := range c.Episodes {
			fmt.Fprintf(&b, "E%02d", e)
		}
		return b.String()
	}
}

var (
	// S04E07, S04.E07, S04-E07, S04 EP07, s4e7: the first episode of a name.
	coverEpisodeRE = regexp.MustCompile(`(?i)\bS(\d{1,3})[ ._-]?EP?(\d{1,4})`)
	// After it, more of the same season: one more episode of a list (E08,
	// .E08, EP08) or the end of a range (-E08, -08, -S04E08). A digit run that
	// runs on into letters (-720p) is not one.
	coverListRE  = regexp.MustCompile(`(?i)^[ ._]?EP?(\d{1,4})`)
	coverRangeRE = regexp.MustCompile(`(?i)^-(?:S\d{1,3}[ ._-]?EP?|EP?)?(\d{1,4})\b`)
	// Season 1 Episode 5.
	coverWordsRE = regexp.MustCompile(`(?i)\bSeason[ ._]?(\d{1,3})[ ._,-]*Episode[ ._]?(\d{1,4})\b`)
	// 4x07, 4x07x08, 4x07-08 and 4x07-4x08. \b on both sides keeps 1920x1080
	// out.
	coverCrossRE = regexp.MustCompile(`(?i)\b(\d{1,2})((?:x\d{2,3})+)(?:-(?:\d{1,2}x)?(\d{2,3}))?\b`)
	coverCrossEp = regexp.MustCompile(`(?i)x(\d{2,3})`)
	// Whole seasons: S01.S02.S03; S01, S01-S03, S01-03; Season 2, Season 1-3,
	// Seasons 1-3.
	coverSeasonListRE = regexp.MustCompile(`(?i)\bS(\d{1,3})((?:[ ._]S\d{1,3})+)\b`)
	coverSeasonItemRE = regexp.MustCompile(`(?i)S(\d{1,3})`)
	coverSeasonRE     = regexp.MustCompile(`(?i)\bS(\d{1,3})(?:[ ._]?-[ ._]?S?(\d{1,3}))?\b`)
	coverSeasonWordRE = regexp.MustCompile(`(?i)\bSeasons?[ ._]?(\d{1,3})(?:[ ._]?-[ ._]?(\d{1,3}))?\b`)
)

// maxRun bounds how far a range or a list may reach. Anything wider is a
// misreading of something else in the name, not a release of sixty episodes.
const maxRun = 60

// ParseCoverage reads what a release or file name holds of a series.
func ParseCoverage(name string) Coverage {
	c, _ := parseCoverage(name)
	return c
}

// SeriesPart is what a release name says of a series: the words before what
// it holds — "Show Name 2019" of "Show.Name.2019.S01E05.1080p" —, with scene
// separators read as spaces, and what it holds. The name is "" when it holds
// nothing of a series, or names nothing before it.
func SeriesPart(name string) (string, Coverage) {
	c, at := parseCoverage(name)
	if !c.Found {
		return "", c
	}
	head := strings.TrimSpace(wsRE.ReplaceAllString(sepRE.ReplaceAllString(name[:at], " "), " "))
	return strings.TrimRight(head, " -([{"), c
}

// parseCoverage is ParseCoverage, with where in the name what it holds is
// written.
func parseCoverage(name string) (Coverage, int) {
	if m := coverEpisodeRE.FindStringSubmatchIndex(name); m != nil {
		season, first := atoi(name[m[2]:m[3]]), atoi(name[m[4]:m[5]])
		eps := []int{first}
		last := first
		near := func(e int) bool { return e >= first-maxRun && e <= first+maxRun }
		rest := name[m[1]:]
		for {
			if n := coverListRE.FindStringSubmatch(rest); n != nil {
				if e := atoi(n[1]); near(e) {
					eps = append(eps, e)
					last = e
				}
				rest = rest[len(n[0]):]
				continue
			}
			if n := coverRangeRE.FindStringSubmatch(rest); n != nil {
				if e := atoi(n[1]); e > last && near(e) {
					for x := last + 1; x <= e; x++ {
						eps = append(eps, x)
					}
					last = e
				}
				rest = rest[len(n[0]):]
				continue
			}
			break
		}
		return episodes(season, eps), m[0]
	}
	if m := coverWordsRE.FindStringSubmatchIndex(name); m != nil {
		return episodes(atoi(name[m[2]:m[3]]), []int{atoi(name[m[4]:m[5]])}), m[0]
	}
	if m := coverCrossRE.FindStringSubmatchIndex(name); m != nil {
		season := atoi(name[m[2]:m[3]])
		var eps []int
		for _, x := range coverCrossEp.FindAllStringSubmatch(name[m[4]:m[5]], -1) {
			eps = append(eps, atoi(x[1]))
		}
		if m[6] >= 0 {
			if e, last := atoi(name[m[6]:m[7]]), eps[len(eps)-1]; e > last && e-eps[0] <= maxRun {
				for x := last + 1; x <= e; x++ {
					eps = append(eps, x)
				}
			}
		}
		return episodes(season, eps), m[0]
	}
	if m := coverSeasonListRE.FindStringSubmatchIndex(name); m != nil {
		seasons := []int{atoi(name[m[2]:m[3]])}
		for _, x := range coverSeasonItemRE.FindAllStringSubmatch(name[m[4]:m[5]], -1) {
			seasons = append(seasons, atoi(x[1]))
		}
		return seasonsOf(seasons), m[0]
	}
	for _, re := range []*regexp.Regexp{coverSeasonRE, coverSeasonWordRE} {
		if m := re.FindStringSubmatchIndex(name); m != nil {
			first := atoi(name[m[2]:m[3]])
			seasons := []int{first}
			if m[4] >= 0 {
				if e := atoi(name[m[4]:m[5]]); e > first && e-first <= maxRun {
					for x := first + 1; x <= e; x++ {
						seasons = append(seasons, x)
					}
				}
			}
			return seasonsOf(seasons), m[0]
		}
	}
	return Coverage{}, len(name)
}

// episodes is the coverage of the episodes of one season named.
func episodes(season int, eps []int) Coverage {
	eps = sortedUnique(eps)
	return Coverage{Found: true, Season: season, SeasonEnd: season,
		Episode: eps[0], EpisodeEnd: eps[len(eps)-1], Episodes: eps}
}

// seasonsOf is the coverage of the whole seasons named.
func seasonsOf(seasons []int) Coverage {
	seasons = sortedUnique(seasons)
	return Coverage{Found: true, Season: seasons[0], SeasonEnd: seasons[len(seasons)-1], Seasons: seasons}
}

func sortedUnique(v []int) []int {
	sort.Ints(v)
	out := v[:0]
	for i, x := range v {
		if i == 0 || x != v[i-1] {
			out = append(out, x)
		}
	}
	return out
}

func contains(v []int, x int) bool {
	for _, y := range v {
		if y == x {
			return true
		}
	}
	return false
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
