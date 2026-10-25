package release

import (
	"regexp"
	"strings"
)

// What a release name says after a movie's year, or after the episodes a
// series' release holds: its tags.
//
// A release found in a source's newest releases is matched to a title by its
// name alone. A release of the title names it, then its year or the episodes
// it holds, then only what releases tag themselves with — resolution, source,
// streaming service, codec, HDR, audio, edition, language — and a release
// group. Words of anything else after the year are something else of the
// title: a trailer, a making-of, an extra, or another title that starts with
// its name.

// YearSplit is a release name read at one of its years: the words before it
// with scene separators read as spaces — "Example Clay Film" of
// "Example.Clay.Film.2026.1080p.WEB-DL-GRP" —, the year, and what follows
// it as it is written (".1080p.WEB-DL-GRP").
type YearSplit struct {
	Head string
	Year int
	Rest string
}

// SplitYears reads a release name at each year it writes after its first
// word, the first year first: a title may hold a year of its own.
func SplitYears(name string) []YearSplit {
	var out []YearSplit
	for i := 0; i < len(name); {
		m := nameYearRE.FindStringSubmatchIndex(name[i:])
		if m == nil {
			break
		}
		at, end := i+m[2], i+m[3]
		i = end
		head := strings.TrimRight(strings.TrimSpace(wsRE.ReplaceAllString(sepRE.ReplaceAllString(name[:at], " "), " ")), " -([{")
		if head != "" {
			out = append(out, YearSplit{Head: head, Year: YearOf(name[at:end]), Rest: name[end:]})
		}
	}
	return out
}

// nameYearRE is a year a name may write: 1900 to 2099, a word of its own.
var nameYearRE = regexp.MustCompile(`(?:^|[^A-Za-z0-9])((?:19|20)\d{2})(?:$|[^A-Za-z0-9])`)

// TagsOnly reports whether what a release name says after a year, or after
// the episodes it holds (rest, as written), is tags only: words releases tag
// themselves with, words with a digit in them (1080p, x264, DDP5.1), an
// edition named as one (Special.Edition, Directors.Cut — "Special" alone is
// something else of the title), and a release group at its end. lead, when
// set, may come first: an episode's title, which a release of it may name.
func TagsOnly(rest, lead string) bool {
	words := afterLead(StripGroup(rest), lead)
	tag := func(w string) bool { return tagWords[w] || strings.ContainsAny(w, "0123456789") }
	prev := false
	for i := 0; i < len(words); i++ {
		w := words[i]
		switch {
		case editionWords[w]:
			// A tag only in an edition's name, which ends in its head.
			end := -1
			for j := i + 1; j < len(words) && j <= i+3; j++ {
				if editionHeads[words[j]] {
					end = j
					break
				}
				if !editionWords[words[j]] && !tag(words[j]) {
					break
				}
			}
			if end < 0 {
				return false
			}
			i = end
		case editionHeads[w]:
			// Extended.Cut, Uncut.Version: after a tag.
			if !prev {
				return false
			}
		case !tag(w):
			return false
		}
		prev = true
	}
	return true
}

// afterLead are the words, as MatchKey writes them, of what a release name
// says after its year or episodes (rest), without lead when it comes first:
// an episode's own title.
func afterLead(rest, lead string) []string {
	words := strings.Fields(MatchKey(rest))
	if l := strings.Fields(MatchKey(lead)); len(l) > 0 && len(words) >= len(l) && strings.Join(words[:len(l)], " ") == strings.Join(l, " ") {
		words = words[len(l):]
	}
	return words
}

// Extra names what a release name says after a year, or after the episodes
// it holds (rest, as written), that makes it an extra of the title rather
// than the title — a trailer, a teaser, a featurette, extras, a bonus, a
// making-of, behind the scenes, a sample, an interview, a promo —; "" when it
// says none.
func Extra(rest string) string { return ExtraAfter(rest, "") }

// ExtraAfter is Extra of what follows lead, when the name says it first: an
// episode's own title, which may hold one of the words — "The Interview".
func ExtraAfter(rest, lead string) string {
	key := " " + strings.Join(afterLead(rest, lead), " ") + " "
	for _, w := range extraWords {
		if strings.Contains(key, " "+w+" ") {
			return w
		}
	}
	return ""
}

// editionWords name an edition with a head after them (editionHeads):
// Special.Edition, Directors.Cut, 25th.Anniversary.Edition, Open.Matte.
// Alone, they are no tag: Example.Film.2026.Special is something else of the
// film.
var editionWords = set("special", "anniversary", "collectors", "collector", "ultimate", "final", "directors", "director",
	"open", "alternate", "alternative", "international", "deluxe", "definitive")

// editionHeads end an edition's name.
var editionHeads = set("edition", "cut", "version", "collection", "matte")

// extraWords are the words and phrases of an extra, singular and plural.
var extraWords = []string{
	"trailer", "trailers", "teaser", "teasers", "featurette", "featurettes", "extras", "bonus",
	"making of", "behind the scenes", "sample", "interview", "interviews", "promo", "promos",
	"deleted scenes", "bloopers", "gag reel",
}

// StripGroup takes off the end of a release name what names who released it:
// a group after its last hyphen (-GRP), and anything in square brackets after
// it.
func StripGroup(name string) string {
	name = strings.TrimSpace(name)
	for {
		trimmed := strings.TrimSpace(bracketEndRE.ReplaceAllString(name, ""))
		if trimmed == name {
			break
		}
		name = trimmed
	}
	return groupEndRE.ReplaceAllString(name, "")
}

var (
	bracketEndRE = regexp.MustCompile(`\[[^\[\]]*\]$`)
	groupEndRE   = regexp.MustCompile(`-[A-Za-z0-9]+$`)
)

// tagWords are the words, as MatchKey writes them, that releases tag
// themselves with besides the ones with a digit in them.
var tagWords = set(
	// resolution and source
	"sd", "hd", "fhd", "uhd", "web", "dl", "webdl", "webrip", "webhd", "webcap", "hdrip", "bluray", "blu", "ray",
	"bdrip", "brrip", "bdremux", "remux", "bd", "hdtv", "pdtv", "sdtv", "dsr", "dsrip", "tvrip", "satrip", "dvb",
	"dvd", "dvdrip", "dvdr", "dvdscr", "hddvd", "vhsrip", "ppv", "vod", "vodrip", "cam", "hdcam", "camrip", "ts",
	"hdts", "telesync", "tc", "telecine", "scr", "screener", "workprint", "wp", "r5",
	// streaming services and broadcasters, as releases abbreviate them
	"amzn", "nf", "dsnp", "atvp", "hmax", "max", "hulu", "pcok", "pmtp", "stan", "it", "ip", "cr", "funi", "red",
	"crav", "itvx", "all4", "my5", "now", "sky", "roku", "tubi", "vudu", "hbo", "sho", "starz", "epix", "mgm",
	"cbc", "bbc", "itv", "nbc", "abc", "cbs", "fox", "amc", "fx", "pbs", "hidive", "joyn", "rtl", "zdf", "ard",
	"srf", "arte", "mubi", "ma",
	// codec and picture
	"h", "x", "hevc", "avc", "xvid", "divx", "mpeg", "vc", "hi10p", "hdr", "hdr10plus", "dv", "dovi", "hlg",
	"sdr", "hfr", "imax", "3d", "hsbs", "hou", "sbs", "ou", "ai", "upscale", "upscaled", "regraded",
	// audio
	"aac", "ac3", "eac3", "dd", "ddp", "dts", "hdma", "hra", "es", "truehd", "atmos", "flac", "mp3", "opus",
	"lpcm", "pcm", "ch", "mono", "stereo", "audio", "dual", "multi",
	// edition and flags
	"proper", "repack", "rerip", "real", "internal", "limited", "extended", "uncut", "unrated", "uncensored",
	"dc", "theatrical", "remastered", "restored", "criterion",
	"hybrid", "complete", "festival", "retail", "readnfo", "read", "nfo", "dubbed", "dub", "subbed", "sub",
	"subs", "multisubs", "hardsub", "hardsubbed", "custom", "fixed", "sync", "dirfix", "nfofix", "samplefix",
	"proof", "redux", "v2", "v3", "ws", "fs", "pal", "ntsc", "ld", "md", "line", "mic", "dubbing",
	// languages
	"english", "eng", "en", "german", "ger", "deu", "de", "french", "fre", "fra", "fr", "truefrench", "vff",
	"vfq", "vf", "vf2", "vostfr", "subfrench", "spanish", "spa", "esp", "castellano", "latino", "lat",
	"italian", "ita", "japanese", "jpn", "jap", "ja", "jp", "korean", "kor", "ko", "kr", "chinese", "chi",
	"chs", "cht", "zho", "zh", "mandarin", "cantonese", "russian", "rus", "ru", "dutch", "nl", "flemish",
	"swedish", "swe", "sv", "se", "norwegian", "nor", "no", "danish", "dan", "da", "dk", "finnish", "fin",
	"fi", "nordic", "polish", "pol", "pl", "portuguese", "por", "pt", "br", "brazilian", "hindi", "hin",
	"tamil", "telugu", "turkish", "tur", "tr", "czech", "cze", "cz", "hungarian", "hun", "hu", "greek",
	"gre", "el", "hebrew", "heb", "arabic", "ara", "ar", "thai", "tha", "vietnamese", "vie", "ukrainian",
	"ukr", "romanian", "rom", "ro", "icelandic", "ice", "is",
)

func set(words ...string) map[string]bool {
	out := make(map[string]bool, len(words))
	for _, w := range words {
		out[w] = true
	}
	return out
}
