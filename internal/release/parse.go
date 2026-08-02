// Package release reads what a scene release name is actually offering:
// resolution, source, codec, HDR and the release group. Scoring needs this to
// compare candidates, and the console shows it so a choice is legible.
//
// The vocabulary deliberately mirrors the platform scanner
// (katalog-manager/internal/scanner/classify.go) so acquire and the catalog
// describe the same file the same way.
package release

import (
	"regexp"
	"strconv"
	"strings"
)

// Info is what a release name claims to contain.
type Info struct {
	Title      string   `json:"title"`
	Year       int      `json:"year"`
	Resolution string   `json:"resolution"` // 2160p|1080p|720p|480p|""
	Source     string   `json:"source"`     // bluray|remux|webdl|webrip|hdtv|dvd|cam|""
	Codec      string   `json:"codec"`      // x265|x264|av1|xvid|""
	HDR        bool     `json:"hdr"`
	Repack     bool     `json:"repack"`
	Group      string   `json:"group"`
	Languages  []string `json:"languages"`
	Season     int      `json:"season"`
	Episode    int      `json:"episode"`

	// Normalized is the release name with scene separators collapsed to spaces
	// and lowercased — the string every term match must run against. Matching
	// the RAW title instead is why "Movie.2024.HDCAM.x264-GRP" slipped past a
	// reject list containing "cam".
	Normalized string `json:"-"`
}

var (
	resolutionRE = regexp.MustCompile(`(?i)\b(2160p|1080p|720p|480p|4k|uhd)\b`)
	yearRE       = regexp.MustCompile(`\b((?:19|20)\d{2})\b`)
	episodeRE    = regexp.MustCompile(`(?i)\bS(\d{1,2})E(\d{1,3})\b`)
	groupRE      = regexp.MustCompile(`-([A-Za-z0-9_.]{2,20})$`)
	sepRE        = regexp.MustCompile(`[._]+`)
	wsRE         = regexp.MustCompile(`\s+`)

	// Ordered: composite tokens before the single tokens they contain.
	// strict=true matches only as a whole word. The cam family needs it: a loose
	// substring match makes "Film.2024.x264-CAMELOT" parse as Source=cam, which
	// would hard-reject a legitimate release group.
	sources = []struct {
		token, name string
		strict      bool
	}{
		{token: "bdremux", name: "remux"}, {token: "bd-remux", name: "remux"}, {token: "remux", name: "remux"},
		{token: "bluray", name: "bluray"}, {token: "blu-ray", name: "bluray"},
		{token: "brrip", name: "bluray"}, {token: "bdrip", name: "bluray"},
		{token: "web-dl", name: "webdl"}, {token: "webdl", name: "webdl"},
		{token: "webrip", name: "webrip"}, {token: "web", name: "webdl"},
		{token: "hdtv", name: "hdtv"}, {token: "pdtv", name: "hdtv"},
		{token: "dvdrip", name: "dvd"}, {token: "dvd", name: "dvd"},
		{token: "telesync", name: "cam", strict: true}, {token: "camrip", name: "cam", strict: true},
		{token: "hdcam", name: "cam", strict: true}, {token: "hdts", name: "cam", strict: true},
		{token: "cam", name: "cam", strict: true},
	}
	codecs = []struct{ token, name string }{
		{"x265", "x265"}, {"h265", "x265"}, {"h.265", "x265"}, {"hevc", "x265"},
		{"x264", "x264"}, {"h264", "x264"}, {"h.264", "x264"}, {"avc", "x264"},
		{"av1", "av1"}, {"xvid", "xvid"}, {"divx", "xvid"},
	}
	hdrTokens = []string{"hdr10+", "hdr10", "hdr", "dolby vision", "dovi", " dv ", ".dv.", "hlg"}
	langNames = map[string]string{
		"eng": "en", "english": "en", "ger": "de", "german": "de", "deu": "de",
		"fre": "fr", "french": "fr", "ita": "it", "italian": "it", "spa": "es",
		"spanish": "es", "rus": "ru", "russian": "ru", "jpn": "ja", "japanese": "ja",
		"por": "pt", "cze": "cs", "hun": "hu", "pol": "pl", "tha": "th", "tur": "tr",
		"kor": "ko", "chi": "zh", "dan": "da", "dut": "nl", "swe": "sv", "nor": "no",
	}
)

// Parse extracts what it can from a release name. Everything is best-effort:
// an unrecognised field stays empty rather than guessing.
func Parse(name string) Info {
	var in Info
	if strings.TrimSpace(name) == "" {
		return in
	}
	// Scene names use dots/underscores as spaces.
	spaced := strings.ToLower(wsRE.ReplaceAllString(sepRE.ReplaceAllString(name, " "), " "))
	padded := " " + spaced + " "

	if m := resolutionRE.FindStringSubmatch(spaced); m != nil {
		switch strings.ToLower(m[1]) {
		case "4k", "uhd":
			in.Resolution = "2160p"
		default:
			in.Resolution = strings.ToLower(m[1])
		}
	}
	for _, s := range sources {
		if strings.Contains(padded, " "+s.token+" ") {
			in.Source = s.name
			break
		}
		if !s.strict && strings.Contains(spaced, s.token) {
			in.Source = s.name
			break
		}
	}
	for _, c := range codecs {
		if strings.Contains(spaced, c.token) {
			in.Codec = c.name
			break
		}
	}
	for _, h := range hdrTokens {
		if strings.Contains(padded, h) {
			in.HDR = true
			break
		}
	}
	in.Repack = strings.Contains(spaced, "repack") || strings.Contains(spaced, "proper")

	if m := episodeRE.FindStringSubmatch(name); m != nil {
		in.Season, _ = strconv.Atoi(m[1])
		in.Episode, _ = strconv.Atoi(m[2])
	}
	if m := yearRE.FindStringSubmatch(spaced); m != nil {
		in.Year, _ = strconv.Atoi(m[1])
	}
	if m := groupRE.FindStringSubmatch(strings.TrimSpace(name)); m != nil {
		in.Group = m[1]
	}

	seen := map[string]bool{}
	for _, tok := range strings.Fields(spaced) {
		if code, ok := langNames[tok]; ok && !seen[code] {
			seen[code] = true
			in.Languages = append(in.Languages, code)
		}
	}

	// The title is whatever precedes the first piece of technical vocabulary.
	in.Title = titleOf(spaced, in)
	in.Normalized = spaced
	return in
}

func titleOf(spaced string, in Info) string {
	cut := len(spaced)
	marks := []string{in.Resolution, in.Source, in.Codec}
	if in.Year != 0 {
		marks = append(marks, strconv.Itoa(in.Year))
	}
	for _, m := range marks {
		if m == "" {
			continue
		}
		if i := strings.Index(spaced, m); i > 0 && i < cut {
			cut = i
		}
	}
	return strings.TrimSpace(strings.Trim(spaced[:cut], " -("))
}

// ResolutionRank orders resolutions so a profile can express a preference
// without the caller knowing the vocabulary. Unknown sorts last.
func ResolutionRank(r string) int {
	switch r {
	case "2160p":
		return 4
	case "1080p":
		return 3
	case "720p":
		return 2
	case "480p":
		return 1
	}
	return 0
}
