package subtitles

import (
	"slices"
	"testing"

	"github.com/laedeli/acquire/internal/opensubtitles"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
)

// A track's or a source's language serves a language when it is that
// language. For Chinese the script decides: a want of no script is served by
// either script, Traditional marked; a want of a script by that script, or
// one unsaid — never by the other script, which OtherScript names.
func TestServes(t *testing.T) {
	for _, c := range []struct {
		code, label, want string
		ok                bool
		how               string
	}{
		{"ger", "", "de", true, ""},
		{"deu", "", "de", true, ""},
		{"de-CH", "", "de", true, ""},
		{"eng", "SDH", "en", true, ""},
		{"fre", "", "de", false, ""},
		{"und", "", "de", false, ""},
		// Chinese of no script: either script, Traditional marked
		{"chi", "", "zh", true, ""},
		{"zh-cn", "", "zh", true, ""},
		{"zh-tw", "", "zh", true, "hant"},
		{"chi", "繁體中文", "zh", true, "hant"},
		{"chi", "简体", "zh", true, ""},
		// Simplified: its script, or one unsaid; not Traditional
		{"zh-Hans", "", "zh-Hans", true, ""},
		{"zh-cn", "", "zh-Hans", true, ""},
		{"chi", "", "zh-Hans", true, ""},
		{"ze", "", "zh-Hans", true, ""},
		{"zh-tw", "", "zh-Hans", false, ""},
		{"chi", "Chinese (Traditional)", "zh-Hans", false, ""},
		{"zh-tw", "", "zh-CN", false, ""},
		// Traditional: its script, or one unsaid; not Simplified
		{"zh-tw", "", "zh-Hant", true, ""},
		{"zh-HK", "", "zh-Hant", true, ""},
		{"chi", "", "zh-Hant", true, ""},
		{"zh-cn", "", "zh-Hant", false, ""},
		{"zh-cn", "", "zh-TW", false, ""},
	} {
		ok, how := Serves(c.code, c.label, c.want)
		if ok != c.ok || how != c.how {
			t.Errorf("%s %q for %s: %v %q, want %v %q", c.code, c.label, c.want, ok, how, c.ok, c.how)
		}
	}
	for _, c := range []struct{ code, label, want, other string }{
		{"zh-tw", "", "zh-Hans", "hant"},
		{"chi", "繁體中文", "zh-Hans", "hant"},
		{"zh-cn", "", "zh-Hant", "hans"},
		{"zh-cn", "", "zh-Hans", ""},
		{"chi", "", "zh-Hans", ""},
		{"zh-tw", "", "zh", ""},
		{"de", "", "zh-Hans", ""},
	} {
		if got := OtherScript(c.code, c.label, c.want); got != c.other {
			t.Errorf("OtherScript(%s %q, %s): %q, want %q", c.code, c.label, c.want, got, c.other)
		}
	}
}

// What a source is asked for a language, and what its codes are attached as.
func TestSourceCodesAndTags(t *testing.T) {
	for tag, want := range map[string]string{"en": "en", "de": "de", "zh": "zh-cn,zh-tw", "zh-Hans": "zh-cn",
		"zh-CN": "zh-cn", "zh-SG": "zh-cn", "zh-Hant": "zh-tw", "zh-TW": "zh-tw", "zh-HK": "zh-tw", "pt": "pt-pt,pt-br",
		"pt-BR": "pt-br", "und": ""} {
		got := ""
		for i, c := range SourceCodes(tag) {
			if i > 0 {
				got += ","
			}
			got += c
		}
		if got != want {
			t.Errorf("%s: %s, want %s", tag, got, want)
		}
	}
	for code, want := range map[string]string{"zh-cn": "zh-Hans", "zh-TW": "zh-Hant", "pt-br": "pt-BR", "de": "de", "ger": "de", "ze": "zh"} {
		if got := TagOf(code); got != want {
			t.Errorf("%s: %s, want %s", code, got, want)
		}
	}
	if Name("de") != "German" || Name("zh") != "Chinese" || Name("zh-Hans") != "Chinese (Simplified)" || Name("zh-Hant") != "Chinese (Traditional)" || Name("xx") != "xx" {
		t.Error("names")
	}
}

func result(lang, release string, downloads int, f func(*opensubtitles.Result)) opensubtitles.Result {
	r := opensubtitles.Result{FileID: int64(len(release) + downloads), Language: lang, Release: release, Downloads: downloads, Files: 1}
	if f != nil {
		f(&r)
	}
	return r
}

// A result made for the file comes first, then one made for a release of
// its name, then the downloads and the rating decide; machine- and
// AI-translated ones and one in several files are never taken on their own,
// but listed, saying why; another language, and another episode, are left
// out.
func TestRank(t *testing.T) {
	target := Target{Kind: "episode", Title: "The Quiet Harbour", Season: 4, Episode: 7, Hash: true,
		Releases: []string{"The.Quiet.Harbour.S04E07.1080p.WEB-DL.DDP5.1.H.264-GRP"}, Labels: map[string]string{"medium": "web", "quality": "1080p"}}
	results := []opensubtitles.Result{
		result("de", "The.Quiet.Harbour.S04E07.720p.HDTV.x264-OTHER", 90000, nil),
		result("de", "The.Quiet.Harbour.S04E07.1080p.WEB-DL.DDP5.1.H.264-GRP", 40, nil),
		result("de", "Quiet Harbour 4x07", 10, func(r *opensubtitles.Result) { r.HashMatch = true }),
		result("de", "The.Quiet.Harbour.S04E07.1080p.WEB-DL-GRP", 100000, func(r *opensubtitles.Result) { r.MachineTranslated = true }),
		result("de", "The.Quiet.Harbour.S04E08.1080p.WEB-DL-GRP", 100000, func(r *opensubtitles.Result) { r.Season, r.Episode = 4, 8 }),
		result("de", "The.Quiet.Harbour.S04E07.CD1", 100000, func(r *opensubtitles.Result) { r.Files = 2 }),
		result("en", "The.Quiet.Harbour.S04E07.1080p.WEB-DL.DDP5.1.H.264-GRP", 100000, nil),
	}
	got := Rank(results, "de", target, Options{})
	if len(got) != 5 {
		t.Fatalf("%d candidates: %+v", len(got), got)
	}
	if !got[0].HashMatch || got[0].Match != MatchFile || !got[0].Best {
		t.Errorf("first: %+v", got[0])
	}
	if got[1].Release != "The.Quiet.Harbour.S04E07.1080p.WEB-DL.DDP5.1.H.264-GRP" || got[1].Match != MatchRelease || got[1].Best {
		t.Errorf("second: %+v", got[1])
	}
	if got[2].Match != MatchUnknown || got[2].Downloads != 90000 {
		t.Errorf("third: %+v", got[2])
	}
	for i, why := range []string{"machine-translated", "in 2 files"} {
		found := false
		for _, c := range got[3:] {
			found = found || c.Excluded == why
		}
		if !found {
			t.Errorf("no candidate excluded as %q (%d)", why, i)
		}
	}
	// allowed, a machine translation competes like any other
	if all := Rank(results, "de", target, Options{AllowMachineTranslated: true}); all[3].Excluded == "machine-translated" {
		t.Errorf("allowed machine translations: %+v", all[3])
	}
	// with nothing known of the file, nothing matches it: the downloads decide
	bare := Rank(results[:2], "de", Target{Kind: "episode", Title: "The Quiet Harbour", Season: 4, Episode: 7}, Options{})
	if bare[0].Downloads != 90000 || bare[0].Match != MatchUnknown {
		t.Errorf("nothing known: %+v", bare[0])
	}
}

// An episode's search keeps only what may be made for that episode: one the
// source says is another's is left out, and so is one whose release's name,
// else its file's, names another episode or another season; one that names
// a run of episodes the episode is in, a whole season, or none stays. A
// movie's search keeps every result.
func TestRankLeavesOutAnotherEpisode(t *testing.T) {
	target := Target{Kind: "episode", Title: "The Quiet Orchard", Season: 1, Episode: 8}
	other := func(r *opensubtitles.Result) { r.Season, r.Episode = 1, 1 }
	this := func(r *opensubtitles.Result) { r.Season, r.Episode = 1, 8 }
	results := []opensubtitles.Result{
		result("de", "The.Quiet.Orchard.S01E08.1080p.WEB-DL-GRP", 10, this),
		result("de", "The.Quiet.Orchard.S01E01.1080p.WEB-DL-GRP", 90000, other),
		result("de", "The.Quiet.Orchard.S01E01.720p.HDTV", 80000, nil),
		result("de", "", 70000, func(r *opensubtitles.Result) { r.FileName = "The.Quiet.Orchard.S01E02.srt" }),
		result("de", "The.Quiet.Orchard.S02E08.WEB", 60000, nil),
		result("de", "The.Quiet.Orchard.S02.Complete.BluRay", 50000, nil),
		result("de", "The.Quiet.Orchard.S01E07E08.720p.HDTV", 40, nil),
		result("de", "Quiet Orchard 1x08", 30, nil),
		result("de", "The.Quiet.Orchard.S01.Complete.BluRay", 20, nil),
		result("de", "Quiet Orchard WEB", 15, nil),
	}
	var kept []string
	for _, c := range Rank(results, "de", target, Options{}) {
		kept = append(kept, c.Release)
	}
	want := []string{"The.Quiet.Orchard.S01E07E08.720p.HDTV", "Quiet Orchard 1x08", "The.Quiet.Orchard.S01.Complete.BluRay",
		"Quiet Orchard WEB", "The.Quiet.Orchard.S01E08.1080p.WEB-DL-GRP"}
	if len(kept) != len(want) {
		t.Fatalf("kept %q", kept)
	}
	for _, w := range want {
		if !slices.Contains(kept, w) {
			t.Errorf("%q left out: %q", w, kept)
		}
	}
	movie := Rank(results[1:3], "de", Target{Kind: "movie", Title: "The Quiet Orchard"}, Options{})
	if len(movie) != 2 {
		t.Errorf("a movie's results: %+v", movie)
	}
	for name, want := range map[string][3]int{"Show.S04E07E09.WEB": {4, 7, 9}, "show.4x07.srt": {4, 7, 7}, "Show.S04.Complete": {4, 0, 0}} {
		if s, f, l, ok := episodeNamed(name); !ok || [3]int{s, f, l} != want {
			t.Errorf("%s: %d %d %d %v", name, s, f, l, ok)
		}
	}
	if _, _, _, ok := episodeNamed("Film.2019.1080p.DDP5.1.x264"); ok {
		t.Error("a movie's name names an episode")
	}
}

// A subtitle the source says was made for another title is never taken,
// though its fingerprint matched; nor one of another kind.
func TestRankLeavesOutAnotherTitle(t *testing.T) {
	results := []opensubtitles.Result{
		result("en", "Another.Film.2018.1080p", 70000, func(r *opensubtitles.Result) { r.HashMatch, r.IMDbID = true, 7777777 }),
		result("en", "The.Film.2013.1080p", 100, func(r *opensubtitles.Result) { r.IMDbID = 1392214 }),
		result("en", "The.Film.S01E01", 100, func(r *opensubtitles.Result) { r.FeatureType = "episode" }),
	}
	got := Rank(results, "en", Target{Kind: "movie", Title: "The Film", IMDbID: IMDbNumber("tt1392214"), Hash: true}, Options{})
	if got[0].IMDbID != 1392214 || !got[0].Best {
		t.Errorf("first: %+v", got[0])
	}
	if got[1].Excluded != "made for another title" || got[2].Excluded != "made for another title" {
		t.Errorf("the others: %q %q", got[1].Excluded, got[2].Excluded)
	}
	ep := Rank([]opensubtitles.Result{result("de", "Other.Show.S01E01", 10, func(r *opensubtitles.Result) {
		r.ParentIMDbID, r.Season, r.Episode = 42, 1, 1
	})}, "de", Target{Kind: "episode", Title: "A Show", Season: 1, Episode: 1, ParentIMDbID: 903747}, Options{})
	if ep[0].Excluded != "made for another title" || ep[0].Best {
		t.Errorf("another series' episode: %+v", ep[0])
	}
	if IMDbNumber("tt0133093") != 133093 || IMDbNumber("x") != 0 || IMDbNumber("") != 0 {
		t.Error("IMDbNumber")
	}
}

// For Chinese of no script, Simplified is taken before Traditional, whatever
// the downloads say; Traditional only when there is no Simplified one,
// marked. Chinese of a script is offered that script alone, unmarked.
func TestRankChinese(t *testing.T) {
	results := []opensubtitles.Result{
		result("zh-tw", "Film.2019.1080p.BluRay", 50000, nil),
		result("zh-cn", "Film.2019.720p.WEB", 20, nil),
	}
	got := Rank(results, "zh", Target{Kind: "movie", Title: "Film"}, Options{})
	if got[0].Language != "zh-cn" || got[0].Tag != "zh-Hans" || got[0].Traditional || !got[0].Best {
		t.Errorf("first: %+v", got[0])
	}
	got = Rank(results[:1], "zh", Target{Kind: "movie", Title: "Film"}, Options{})
	if got[0].Tag != "zh-Hant" || !got[0].Traditional || !got[0].Best {
		t.Errorf("Traditional alone: %+v", got[0])
	}
	if got := Rank(results, "zh-Hans", Target{Kind: "movie", Title: "Film"}, Options{}); len(got) != 1 || got[0].Language != "zh-cn" {
		t.Errorf("for Simplified: %+v", got)
	}
	if got := Rank(results[:1], "zh-Hans", Target{Kind: "movie", Title: "Film"}, Options{}); len(got) != 0 {
		t.Errorf("Traditional alone, for Simplified: %+v", got)
	}
	if got := Rank(results, "zh-Hant", Target{Kind: "movie", Title: "Film"}, Options{}); len(got) != 1 || got[0].Language != "zh-tw" ||
		got[0].Traditional || !got[0].Best {
		t.Errorf("for Traditional: %+v", got)
	}
}

// A file's text is UTF-8 for the catalog: as it is when it is, else read in
// the encoding its language was most often written in; UTF-16 by its byte
// order mark; NUL bytes are no text.
func TestUTF8(t *testing.T) {
	german := "1\n00:00:01,000 --> 00:00:02,000\nGrüße aus Köln\n"
	latin, _ := charmap.Windows1252.NewEncoder().String(german)
	if out, err := UTF8([]byte(latin), "de"); err != nil || string(out) != german {
		t.Errorf("Windows-1252: %q %v", out, err)
	}
	if out, err := UTF8([]byte(german), "de"); err != nil || string(out) != german {
		t.Errorf("UTF-8: %q %v", out, err)
	}
	simplified := "1\n00:00:01,000 --> 00:00:02,000\n你好，世界\n"
	gb, _ := simplifiedchinese.GB18030.NewEncoder().String(simplified)
	if out, err := UTF8([]byte(gb), "zh-cn"); err != nil || string(out) != simplified {
		t.Errorf("GB18030: %q %v", out, err)
	}
	traditional := "1\n00:00:01,000 --> 00:00:02,000\n你好，世界\n"
	big5, _ := traditionalchinese.Big5.NewEncoder().String(traditional)
	if out, err := UTF8([]byte(big5), "zh-tw"); err != nil || string(out) != traditional {
		t.Errorf("Big5: %q %v", out, err)
	}
	utf16 := []byte{0xff, 0xfe, 'H', 0, 'i', 0}
	if out, err := UTF8(utf16, "en"); err != nil || string(out) != "Hi" {
		t.Errorf("UTF-16: %q %v", out, err)
	}
	if _, err := UTF8([]byte("a\x00b\xff"), "en"); err == nil {
		t.Error("NUL bytes taken for text")
	}
}
