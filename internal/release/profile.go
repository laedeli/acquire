package release

import (
	"fmt"
	"strings"
)

// Profile is the editable definition of "good" for this library.
type Profile struct {
	PreferProtocol  string   `json:"preferProtocol"` // usenet|torrent|any
	Resolutions     []string `json:"resolutions"`    // best first; empty = no preference
	PreferredCodecs []string `json:"preferredCodecs"`
	RejectTerms     []string `json:"rejectTerms"`

	// RejectSources hard-rejects on the PARSED source (see Info.Source), which
	// is the only reliable way to catch the cam family: "hdcam" is a single
	// token, so a reject list of loose terms never sees a "cam" to match.
	RejectSources []string `json:"rejectSources,omitempty"`

	// SourceScores is what a source is worth, e.g. remux over bluray over
	// web-dl. Without it the size penalty below silently ranks a small HDTV rip
	// above a Blu-ray remux.
	SourceScores map[string]int `json:"sourceScores,omitempty"`

	// MaxSizeMbByResolution caps size per resolution, because one global cap
	// cannot both allow a 60 GB 2160p remux and reject a bloated 720p rip.
	// Falls back to MaxSizeMb when the resolution is unknown or unlisted.
	MaxSizeMbByResolution map[string]int64 `json:"maxSizeMbByResolution,omitempty"`

	MinSizeMb  int64 `json:"minSizeMb"`
	MaxSizeMb  int64 `json:"maxSizeMb"`
	MinSeeders int   `json:"minSeeders"`
	PreferHDR  bool  `json:"preferHdr"`

	// MaxLanguages / LanguagePenalty demote multi-language releases. The
	// penalty is PER language over the limit and has to be big enough to
	// matter against SourceScores — a flat -40 is swamped by a +400 remux.
	MaxLanguages    int `json:"maxLanguages,omitempty"`
	LanguagePenalty int `json:"languagePenalty,omitempty"`
}

// DefaultProfile is used when the database has none (fresh install, or the
// profile row was deleted).
func DefaultProfile() Profile {
	return Profile{
		PreferProtocol:  "usenet",
		Resolutions:     []string{"2160p", "1080p", "720p"},
		PreferredCodecs: []string{"x265"},
		// Deliberately NOT "cam" or "ts": matched against the normalised name
		// they hit the real film "Cam.2018.1080p.WEBRip" and titles containing
		// the word "ts". The cam family is caught by RejectSources instead.
		RejectTerms:   []string{"hdcam", "camrip", "hdts", "telesync", "screener", "dvdscr", "workprint"},
		RejectSources: []string{"cam"},
		SourceScores: map[string]int{
			"remux": 400, "bluray": 300, "webdl": 200,
			"webrip": 100, "hdtv": 25, "dvd": 0,
		},
		// Maxima only. Minima were measured against 452 real grabs and would
		// have rejected 126 of them (28%) — the median 2160p grab is 9,268 MB
		// but the smallest legitimate one is 1,208 MB.
		MaxSizeMbByResolution: map[string]int64{
			"2160p": 80000, "1080p": 40000, "720p": 15000, "480p": 8000,
		},
		MinSizeMb:       500,
		MaxSizeMb:       100000, // backstop for an unknown resolution
		MinSeeders:      1,
		PreferHDR:       false,
		MaxLanguages:    3,
		LanguagePenalty: 60,
	}
}

// Candidate is the subset of a search hit that scoring needs.
type Candidate struct {
	Title    string
	Protocol string // usenet|torrent
	SizeMb   int64
	Seeders  int
}

// Verdict is why a release ranked where it did — surfaced in the console so the
// choice is legible rather than magic.
type Verdict struct {
	Score    int      `json:"score"`
	Rejected bool     `json:"rejected"`
	Reasons  []string `json:"reasons"`
}

// Summary is the one-line explanation shown next to a release.
func (v Verdict) Summary() string {
	if len(v.Reasons) == 0 {
		return ""
	}
	return strings.Join(v.Reasons, " · ")
}

// Score rates a candidate against the profile. Rejections are hard (the release
// is unusable or explicitly unwanted); everything else is additive, so the best
// release is simply the highest score.
func Score(c Candidate, p Profile) (Verdict, Info) {
	in := Parse(c.Title)
	v := Verdict{}
	// Match against the NORMALISED name, not the raw title: scene names are
	// dot-separated, so " term " never appears in the raw string and every
	// reject term was silently inert.
	padded := " " + in.Normalized + " "

	// ── hard rejections ────────────────────────────────────────────────────
	for _, term := range p.RejectTerms {
		term = strings.ToLower(strings.TrimSpace(term))
		if term == "" {
			continue
		}
		if strings.Contains(padded, " "+term+" ") {
			v.Rejected = true
			v.Reasons = append(v.Reasons, "rejected: contains "+term)
			return v, in
		}
	}
	// Terms alone cannot catch the cam family — "hdcam" is one token, so no
	// reject term matches it. The parsed source can.
	for _, src := range p.RejectSources {
		if src != "" && in.Source != "" && strings.EqualFold(strings.TrimSpace(src), in.Source) {
			v.Rejected = true
			v.Reasons = append(v.Reasons, "rejected: "+in.Source+" source")
			return v, in
		}
	}
	if p.MinSizeMb > 0 && c.SizeMb > 0 && c.SizeMb < p.MinSizeMb {
		v.Rejected = true
		v.Reasons = append(v.Reasons, fmt.Sprintf("rejected: %s is below the %s minimum", mb(c.SizeMb), mb(p.MinSizeMb)))
		return v, in
	}
	if cap := p.sizeCapFor(in.Resolution); cap > 0 && c.SizeMb > cap {
		v.Rejected = true
		v.Reasons = append(v.Reasons, fmt.Sprintf("rejected: %s is over the %s cap", mb(c.SizeMb), mb(cap)))
		return v, in
	}
	if c.Protocol == "torrent" && p.MinSeeders > 0 && c.Seeders < p.MinSeeders {
		v.Rejected = true
		v.Reasons = append(v.Reasons, fmt.Sprintf("rejected: %d seeders, needs %d", c.Seeders, p.MinSeeders))
		return v, in
	}

	// ── preferences ────────────────────────────────────────────────────────
	if p.PreferProtocol == c.Protocol {
		v.Score += 1000 // protocol preference dominates: NZB-first stays NZB-first
		if c.Protocol == "usenet" {
			v.Reasons = append(v.Reasons, "NZB preferred")
		} else {
			v.Reasons = append(v.Reasons, "torrent preferred")
		}
	}
	if len(p.Resolutions) > 0 {
		if i := indexOfFold(p.Resolutions, in.Resolution); i >= 0 {
			v.Score += 400 - i*100 // first listed resolution wins
			v.Reasons = append(v.Reasons, in.Resolution)
		} else if in.Resolution != "" {
			v.Reasons = append(v.Reasons, in.Resolution+" (not preferred)")
		}
	} else if in.Resolution != "" {
		v.Score += ResolutionRank(in.Resolution) * 50
		v.Reasons = append(v.Reasons, in.Resolution)
	}
	if indexOfFold(p.PreferredCodecs, in.Codec) >= 0 {
		v.Score += 150
		v.Reasons = append(v.Reasons, in.Codec)
	} else if in.Codec != "" {
		v.Reasons = append(v.Reasons, in.Codec)
	}
	if p.PreferHDR && in.HDR {
		v.Score += 100
		v.Reasons = append(v.Reasons, "HDR")
	}
	if in.Repack {
		v.Score += 25
		v.Reasons = append(v.Reasons, "repack")
	}
	if in.Source != "" {
		if pts, ok := p.SourceScores[strings.ToLower(in.Source)]; ok {
			v.Score += pts
		}
		v.Reasons = append(v.Reasons, in.Source)
	}
	// A healthy torrent is worth something, but never enough to outrank the
	// profile's real preferences.
	if c.Protocol == "torrent" && c.Seeders > 0 {
		bonus := c.Seeders
		if bonus > 50 {
			bonus = 50
		}
		v.Score += bonus
		v.Reasons = append(v.Reasons, fmt.Sprintf("%d seeders", c.Seeders))
	}
	// Prefer the smaller of two otherwise-equal releases: a 70 GB multi-language
	// remux and a 12 GB encode should not tie.
	if c.SizeMb > 0 {
		v.Score -= int(c.SizeMb / 2000)
	}
	limit, per := p.MaxLanguages, p.LanguagePenalty
	if limit <= 0 {
		limit = 3
	}
	if per <= 0 {
		per = 60
	}
	if n := len(in.Languages); n > limit {
		v.Score -= (n - limit) * per
		v.Reasons = append(v.Reasons, fmt.Sprintf("%d languages", n))
	}
	return v, in
}

// sizeCapFor returns the size ceiling for a resolution, falling back to the
// global cap when the resolution is unknown or has no entry.
func (p Profile) sizeCapFor(resolution string) int64 {
	if resolution != "" {
		if cap, ok := p.MaxSizeMbByResolution[strings.ToLower(resolution)]; ok {
			return cap
		}
	}
	return p.MaxSizeMb
}

func indexOfFold(list []string, want string) int {
	if want == "" {
		return -1
	}
	for i, s := range list {
		if strings.EqualFold(strings.TrimSpace(s), want) {
			return i
		}
	}
	return -1
}

func mb(v int64) string {
	if v >= 1024 {
		return fmt.Sprintf("%.1f GB", float64(v)/1024)
	}
	return fmt.Sprintf("%d MB", v)
}
