package subtitles

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/laedeli/acquire/internal/opensubtitles"
)

// Target is the title a subtitle is looked for: what it is, the release names
// known of its file (the release acquire grabbed it from), what its file's
// name claimed (the catalog's labels: medium, quality, resolution), and
// whether the search carried its file's hash.
type Target struct {
	Kind    string // movie or episode
	Title   string
	Season  int
	Episode int
	// IMDbID is a movie's IMDb id, ParentIMDbID an episode's series': the
	// number of its tt id, 0 when unknown.
	IMDbID       int64
	ParentIMDbID int64
	Releases     []string
	Labels       map[string]string
	Hash         bool
}

// IMDbNumber is the number of an IMDb id (tt0133093 is 133093); 0 for none.
func IMDbNumber(id string) int64 {
	id = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(id)), "tt")
	var n int64
	for _, r := range id {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int64(r-'0')
	}
	return n
}

// The ways a result fits the title's file, best first.
const (
	// MatchFile: made for this very file (its hash).
	MatchFile = "file"
	// MatchRelease: made for a release of the same name, or of the same
	// source and resolution.
	MatchRelease = "release"
	// MatchUnknown: nothing says it fits; it may be out of sync.
	MatchUnknown = "unknown"
)

// Candidate is a result ranked for one of the instance's languages.
type Candidate struct {
	opensubtitles.Result
	// Want is the instance language it serves; Tag what it is attached as
	// (zh-Hans, zh-Hant, de); Traditional that it is Traditional Chinese for a
	// Simplified want.
	Want        string  `json:"want"`
	Tag         string  `json:"tag"`
	Traditional bool    `json:"traditional"`
	Match       string  `json:"match"`
	Similarity  float64 `json:"similarity"`
	Score       int     `json:"score"`
	// Excluded says why an automatic choice never takes it ("" when it may):
	// machine- or AI-translated, foreign parts only, in several files, made
	// for another title.
	Excluded string `json:"excluded,omitempty"`
	// Best: the one an automatic choice takes for its language.
	Best bool `json:"best"`
}

// Options are what a ranking may take: machine- and AI-translated results
// (never, by default).
type Options struct {
	AllowMachineTranslated bool
}

// Rank ranks results for the instance language want, the best first: a
// result made for the file (its hash) first, then one made for a release of
// its name, then the downloads and the rating say; one without hearing-
// impaired cues before one with, and, for Chinese, Simplified before
// Traditional, which is taken only when no Simplified one is there. A result
// in another language is left out, and so is one made for another episode
// (ofTheEpisode); one an automatic choice never takes stays, saying why
// (Excluded), after the others. The first that may be taken is Best.
func Rank(results []opensubtitles.Result, want string, t Target, o Options) []Candidate {
	known := releaseTokens(t)
	var out []Candidate
	for _, r := range results {
		ok, how := Serves(r.Language, "", want)
		if !ok || !ofTheEpisode(r, t) {
			continue
		}
		c := Candidate{Result: r, Want: want, Tag: TagOf(r.Language), Traditional: how == "hant", Match: MatchUnknown}
		c.Similarity = similarity(known, tokensOf(t, r.Release, r.FileName))
		labels := labelsAgree(t.Labels, r.Release+" "+r.FileName)
		switch {
		case r.HashMatch && t.Hash:
			c.Match, c.Score = MatchFile, 10000
		case c.Similarity >= 0.5 || (len(known) == 0 && labels >= 2):
			c.Match, c.Score = MatchRelease, 3000+int(c.Similarity*1000)
		default:
			c.Score = int(c.Similarity * 1000)
		}
		c.Score += labels * 150
		c.Score += int(200 * math.Log10(1+float64(r.Downloads)))
		if r.Votes > 0 {
			c.Score += int(r.Rating * 20)
		}
		if r.FromTrusted {
			c.Score += 100
		}
		if r.HearingImpaired {
			c.Score -= 150
		}
		if c.Traditional {
			c.Score -= 100000
		}
		c.Excluded = excluded(r, t, o)
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].Excluded == "") != (out[j].Excluded == "") {
			return out[i].Excluded == ""
		}
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Downloads > out[j].Downloads
	})
	if len(out) > 0 && out[0].Excluded == "" {
		out[0].Best = true
	}
	return out
}

// excluded is why an automatic choice never takes r for t. A file's
// fingerprint can be another title's (a subtitle uploaded under the wrong
// film): one the source says was made for another title is left out,
// whatever matched it.
func excluded(r opensubtitles.Result, t Target, o Options) string {
	switch {
	case t.Kind == "movie" && t.IMDbID > 0 && r.IMDbID > 0 && r.IMDbID != t.IMDbID,
		t.Kind == "episode" && t.ParentIMDbID > 0 && r.ParentIMDbID > 0 && r.ParentIMDbID != t.ParentIMDbID,
		r.FeatureType != "" && t.Kind != "" && r.FeatureType != t.Kind:
		return "made for another title"
	case r.MachineTranslated && !o.AllowMachineTranslated:
		return "machine-translated"
	case r.AITranslated && !o.AllowMachineTranslated:
		return "AI-translated"
	case r.ForeignPartsOnly:
		return "foreign parts only"
	case r.Files > 1:
		return fmt.Sprintf("in %d files", r.Files)
	}
	return ""
}

// ofTheEpisode reports whether r may be made for the episode t: nothing says
// it was made for another — neither the source's word on the episode it
// belongs to, nor the episode its release's name, else its file's, names (a
// file of several episodes names the first and the last: S04E07E08). A
// search by the series' identities, a season and an episode answers that
// episode's; one by the series' name may answer any. Every result may be a
// movie's.
func ofTheEpisode(r opensubtitles.Result, t Target) bool {
	if t.Kind != "episode" || t.Season == 0 || t.Episode == 0 {
		return true
	}
	if r.Season > 0 && r.Episode > 0 && (r.Season != t.Season || r.Episode != t.Episode) {
		return false
	}
	for _, name := range []string{r.Release, r.FileName} {
		if season, first, last, ok := episodeNamed(name); ok {
			return season == t.Season && (first == 0 || (first <= t.Episode && t.Episode <= last))
		}
	}
	return true
}

// episodeNamed is the episode a release or file name names — its season,
// and its first and last episode (0 for a whole season: S04) — read from the
// first of its words that names one: S04E07, S04E07E08, 4x07, S04.
func episodeNamed(name string) (season, first, last int, ok bool) {
	for _, w := range tokenRE.FindAllString(strings.ToLower(name), -1) {
		m := namedEpisodeRE.FindStringSubmatch(w)
		if m == nil {
			continue
		}
		num := func(s string) int { n, _ := strconv.Atoi(s); return n }
		switch {
		case m[1] != "": // s04e07, s04e07e08
			season, first, last = num(m[1]), num(m[2]), num(m[2])
			if m[3] != "" && num(m[3]) > first {
				last = num(m[3])
			}
		case m[4] != "": // 4x07
			season, first, last = num(m[4]), num(m[5]), num(m[5])
		default: // s04
			season = num(m[6])
		}
		return season, first, last, true
	}
	return 0, 0, 0, false
}

var (
	tokenRE   = regexp.MustCompile(`[a-z0-9]+`)
	episodeRE = regexp.MustCompile(`^(s\d{1,2}e\d{1,3}(e\d{1,3})?|\d{1,2}x\d{1,3}|s\d{1,2}|e\d{1,3})$`)
	// the words of a name that name an episode, or a season
	namedEpisodeRE = regexp.MustCompile(`^(?:s(\d{1,2})e(\d{1,3})(?:e(\d{1,3}))?|(\d{1,2})x(\d{1,3})|s(\d{1,2}))$`)
	yearRE         = regexp.MustCompile(`^(19|20)\d\d$`)
	// what a release name says of where it came from, and of its size
	webRE   = regexp.MustCompile(`\b(web|webdl|webrip|amzn|nf|dsnp|hmax|atvp|itunes)\b`)
	discRE  = regexp.MustCompile(`\b(bluray|bdrip|brrip|bdremux|remux|dvdrip|dvd|uhd|bd)\b`)
	tvRE    = regexp.MustCompile(`\b(hdtv|pdtv|dsr|tvrip)\b`)
	heightR = regexp.MustCompile(`\b(2160|1080|720|576|480)p\b`)
)

// releaseTokens are the tokens of the release names known of the target's
// file, as tokensOf reads them.
func releaseTokens(t Target) map[string]bool {
	out := map[string]bool{}
	for _, r := range t.Releases {
		for k := range tokensOf(t, r) {
			out[k] = true
		}
	}
	return out
}

// tokensOf are the words of names that tell one release from another: the
// title's own words, its year, its episode's numbers and the file's
// extension left out.
func tokensOf(t Target, names ...string) map[string]bool {
	title := map[string]bool{}
	for _, w := range tokenRE.FindAllString(strings.ToLower(t.Title), -1) {
		title[w] = true
	}
	out := map[string]bool{}
	for _, n := range names {
		n = strings.ToLower(n)
		n = strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(n, ".srt"), ".sub"), ".ass")
		for _, w := range tokenRE.FindAllString(n, -1) {
			if title[w] || episodeRE.MatchString(w) || yearRE.MatchString(w) || len(w) < 2 {
				continue
			}
			out[w] = true
		}
	}
	return out
}

// similarity is how much two sets of tokens have in common, both counted
// (Dice: twice the shared over the two sizes; 0 when either is empty): a
// name sharing one word of a long one is not that release.
func similarity(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	n := 0
	for k := range a {
		if b[k] {
			n++
		}
	}
	return 2 * float64(n) / float64(len(a)+len(b))
}

// labelsAgree counts what a result's names say that agrees with what the
// file's name claimed: the same medium (web, disc, broadcast) and the same
// height (1080p).
func labelsAgree(labels map[string]string, names string) int {
	if len(labels) == 0 {
		return 0
	}
	n := strings.ToLower(strings.NewReplacer("-", "", ".", " ", "_", " ").Replace(names))
	agree := 0
	switch labels["medium"] {
	case "web":
		if webRE.MatchString(n) {
			agree++
		}
	case "disc":
		if discRE.MatchString(n) {
			agree++
		}
	case "broadcast":
		if tvRE.MatchString(n) {
			agree++
		}
	}
	for _, k := range []string{"resolution", "quality"} {
		if m := heightR.FindString(strings.ToLower(labels[k])); m != "" && strings.Contains(n, m) {
			agree++
			break
		}
	}
	return agree
}
