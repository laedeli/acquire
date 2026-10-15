package indexer

import (
	"strings"

	"github.com/laedeli/acquire/internal/release"
)

// Match is why a release was or was not accepted as the thing we asked for.
type Match struct {
	OK     bool
	Reason string
	// Via records what matched: id | title | alias | coordinates. Production
	// matched 119 of 500 grabs on an ALIAS rather than the title, so a verifier
	// that only knows titles rejects real releases.
	Via string
}

// Verify checks that a release is actually the target we searched for.
//
// The current ranker has NO title-similarity term at all: it scores a release
// entirely on its technical attributes, so a free-text search for "Breaking
// Bad" that returns "The.Bad.Guys.Breaking.In.S02E05" scores it happily. That
// release was the TOP free-text hit in a live probe.
//
// idMatched means the indexer resolved the query by tvdbid/imdbid, in which
// case the series identity is already settled by the wire protocol and only the
// coordinates need checking.
func Verify(title string, aliases []string, name string, season, episode *int, idMatched bool) Match {
	in := release.Parse(name)

	// Coordinates first: an id-resolved search still returns a whole season, so
	// the wrong episode is the likeliest error. A release that holds the
	// episode among others — a file of several episodes in a row, or a season
	// pack — holds it too; which targets a grab of it covers is decided when it
	// is grabbed.
	if season != nil && episode != nil {
		cov := release.ParseCoverage(name)
		if !cov.Found {
			return Match{Reason: "no season/episode in the release name"}
		}
		if !cov.Covers(*season, *episode) {
			return Match{Reason: "wrong episode: release is " + cov.String()}
		}
	}
	if idMatched {
		return Match{OK: true, Via: "id", Reason: "id-resolved"}
	}

	norm := in.Normalized
	if norm == "" {
		norm = strings.ToLower(name)
	}
	if containsTitle(norm, title) {
		return Match{OK: true, Via: "title", Reason: "title matched"}
	}
	for _, a := range aliases {
		if containsTitle(norm, a) {
			return Match{OK: true, Via: "alias", Reason: "alias matched: " + a}
		}
	}
	return Match{Reason: "release does not name " + title + " or any known alias"}
}

// containsTitle does a normalised prefix-anchored containment check.
//
// Anchoring matters: plain containment accepts "The Bad Guys Breaking In" for
// "Breaking Bad" only if the words happen to appear together, but it also
// accepts a title that merely mentions another show. Requiring the title's
// words to appear consecutively is the cheap version of the right answer.
func containsTitle(normalizedRelease, title string) bool {
	t := normalise(title)
	if t == "" {
		return false
	}
	return strings.Contains(" "+normalizedRelease+" ", " "+t+" ") ||
		strings.HasPrefix(normalizedRelease, t+" ")
}

func normalise(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prevSpace := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevSpace = false
		default:
			if !prevSpace {
				b.WriteRune(' ')
				prevSpace = true
			}
		}
	}
	return strings.TrimSpace(b.String())
}
