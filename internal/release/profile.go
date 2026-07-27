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
	MinSizeMb       int64    `json:"minSizeMb"`
	MaxSizeMb       int64    `json:"maxSizeMb"`
	MinSeeders      int      `json:"minSeeders"`
	PreferHDR       bool     `json:"preferHdr"`
}

// DefaultProfile is used when the database has none (fresh install, or the
// profile row was deleted).
func DefaultProfile() Profile {
	return Profile{
		PreferProtocol:  "usenet",
		Resolutions:     []string{"1080p", "2160p", "720p"},
		PreferredCodecs: []string{"x265"},
		RejectTerms:     []string{"cam", "camrip", "telesync", "screener"},
		MinSizeMb:       500,
		MaxSizeMb:       25000,
		MinSeeders:      1,
		PreferHDR:       false,
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
	lower := strings.ToLower(c.Title)

	// ── hard rejections ────────────────────────────────────────────────────
	for _, term := range p.RejectTerms {
		term = strings.ToLower(strings.TrimSpace(term))
		if term == "" {
			continue
		}
		if strings.Contains(" "+lower+" ", " "+term+" ") {
			v.Rejected = true
			v.Reasons = append(v.Reasons, "rejected: contains "+term)
			return v, in
		}
	}
	if p.MinSizeMb > 0 && c.SizeMb > 0 && c.SizeMb < p.MinSizeMb {
		v.Rejected = true
		v.Reasons = append(v.Reasons, fmt.Sprintf("rejected: %s is below the %s minimum", mb(c.SizeMb), mb(p.MinSizeMb)))
		return v, in
	}
	if p.MaxSizeMb > 0 && c.SizeMb > p.MaxSizeMb {
		v.Rejected = true
		v.Reasons = append(v.Reasons, fmt.Sprintf("rejected: %s is over the %s cap", mb(c.SizeMb), mb(p.MaxSizeMb)))
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
	if n := len(in.Languages); n > 3 {
		v.Score -= 40
		v.Reasons = append(v.Reasons, fmt.Sprintf("%d languages", n))
	}
	return v, in
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
