package store

import (
	"context"
	"slices"
	"testing"
	"time"
)

func exec(t *testing.T, s *Store, sql string, args ...any) {
	t.Helper()
	if _, err := s.pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

// seedLibrary: a movie acquire grabbed, a series of three held episodes —
// the third covered by the second's file — one not packaged yet, and one the
// library does not hold.
func seedLibrary(t *testing.T, s *Store) {
	t.Helper()
	exec(t, s, `INSERT INTO titles (id, tmdb_id, kind, title, year, imdb_id) VALUES
		('m:1', 101, 'movie', 'A Film', 2019, 'tt0101'), ('s:1', 201, 'series', 'A Show', 2015, 'tt0201')`)
	exec(t, s, `INSERT INTO acquisition_targets (id, title_id, kind, season_number, episode_number, air_date, state,
		held_item_id, held_via, held_playable, held_release, held_source) VALUES
		('m:1', 'm:1', 'movie', NULL, NULL, '2019-05-01', 'held', 'item-m1', '', true, 'A.Film.2019.1080p.WEB-DL-GRP', 'grab'),
		('s:1:1', 's:1', 'episode', 1, 1, '2015-01-01', 'held', 'item-e1', '', true, '', 'derived'),
		('s:1:2', 's:1', 'episode', 1, 2, '2015-01-08', 'held', 'item-e2', '', true, 'A.Show.S01E02E03.720p.HDTV', 'derived'),
		('s:1:3', 's:1', 'episode', 1, 3, '2015-01-15', 'held', 'item-e3', 'item-e2', true, '', 'derived'),
		('s:1:4', 's:1', 'episode', 1, 4, '2015-01-22', 'held', 'item-e4', '', false, '', 'derived'),
		('s:1:5', 's:1', 'episode', 1, 5, '2015-01-29', 'wanted', '', '', false, '', '')`)
}

// The titles the library holds and plays are recorded from the targets, the
// newest first, a movie with its own identities and an episode with its
// series', an episode covered by another's file left to its holder, each
// with a want per language; one acquire grabbed is asked for.
func TestFillSubtitleTitles(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	seedLibrary(t, s)
	n, err := s.FillSubtitleTitles(ctx, 2, []string{"en", "de"})
	if err != nil || n != 2 {
		t.Fatalf("first fill: %d %v", n, err)
	}
	n, err = s.FillSubtitleTitles(ctx, 10, []string{"en", "de"})
	if err != nil || n != 1 {
		t.Fatalf("second fill: %d %v", n, err)
	}
	if n, _ := s.FillSubtitleTitles(ctx, 10, []string{"en", "de"}); n != 0 {
		t.Errorf("a third fill recorded %d", n)
	}
	m, err := s.SubtitleTitleByID(ctx, "item-m1")
	if err != nil || m.Kind != "movie" || m.IMDbID != "tt0101" || m.TMDBID != 101 || m.ParentTMDBID != 0 ||
		m.Priority != SubtitleAskedFor || m.Release != "A.Film.2019.1080p.WEB-DL-GRP" || m.Year != 2019 {
		t.Errorf("the movie: %+v %v", m, err)
	}
	e, err := s.SubtitleTitleByID(ctx, "item-e2")
	if err != nil || e.Kind != "episode" || e.IMDbID != "" || e.ParentIMDbID != "tt0201" || e.ParentTMDBID != 201 ||
		e.Season != 1 || e.Episode != 2 || e.Priority != SubtitleLibrary || e.Title != "A Show" {
		t.Errorf("the episode: %+v %v", e, err)
	}
	for _, id := range []string{"item-e3", "item-e4"} {
		if _, err := s.SubtitleTitleByID(ctx, id); err != ErrNotFound {
			t.Errorf("%s recorded: %v", id, err)
		}
	}
	wants, _ := s.SubtitleWantsOf(ctx, "item-m1")
	if len(wants) != 2 || wants[0].Language != "de" || wants[0].State != SubtitleWanted {
		t.Errorf("the movie's wants: %+v", wants)
	}
}

// What the catalog says the title has is had; what it no longer has is
// wanted again at once; a language no longer the instance's goes, but for
// one acquire attached a subtitle in.
func TestSubtitleCatalogAndLanguages(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	if err := s.PutSubtitleTitle(ctx, SubtitleTitle{ItemID: "i1", Kind: "movie", Title: "A Film", Priority: SubtitleNewArrival},
		[]string{"en", "de", "zh"}); err != nil {
		t.Fatal(err)
	}
	fp := &SubtitleFingerprint{SizeBytes: 1000, Sum64: "0123456789abcdef", Labels: map[string]string{"medium": "web"}}
	if err := s.SetSubtitleCatalog(ctx, "i1", SubtitleCatalog{VersionID: "v1", Attachable: true, Fingerprint: fp,
		Has: map[string]string{"en": "own"}}); err != nil {
		t.Fatal(err)
	}
	ti, _ := s.SubtitleTitleByID(ctx, "i1")
	if !ti.Attachable || ti.VersionID != "v1" || ti.Sum64 != "0123456789abcdef" || ti.Labels["medium"] != "web" || ti.ReadAt == nil {
		t.Errorf("the title: %+v", ti)
	}
	en, _ := s.SubtitleWantOf(ctx, SubtitleKey{"i1", "en"})
	if en.State != SubtitleHas || en.HasKind != "own" {
		t.Errorf("English: %+v", en)
	}
	if err := s.RecordSubtitleAttached(ctx, SubtitleKey{"i1", "de"}, SubtitleAttached{SubtitleID: "sub-1", FileID: 7,
		Release: "A.Film.WEB", Match: "file", How: "auto"}); err != nil {
		t.Fatal(err)
	}
	// the library lost English, and the German acquire attached
	if err := s.SetSubtitleCatalog(ctx, "i1", SubtitleCatalog{VersionID: "v2", Attachable: true}); err != nil {
		t.Fatal(err)
	}
	for _, l := range []string{"en", "de"} {
		w, _ := s.SubtitleWantOf(ctx, SubtitleKey{"i1", l})
		if w.State != SubtitleWanted || w.SubtitleID != "" || w.NextAt == nil || w.Detail != "the library no longer has one" {
			t.Errorf("%s after the library lost it: %+v", l, w)
		}
	}
	// a subtitle attached in Chinese stays when Chinese is no longer wanted
	if err := s.RecordSubtitleAttached(ctx, SubtitleKey{"i1", "zh"}, SubtitleAttached{SubtitleID: "sub-2", FileID: 8, How: "manual"}); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureSubtitleWants(ctx, []string{"en", "fr"}); err != nil {
		t.Fatal(err)
	}
	wants, _ := s.SubtitleWantsOf(ctx, "i1")
	var langs []string
	for _, w := range wants {
		langs = append(langs, w.Language)
	}
	if len(langs) != 3 || langs[0] != "en" || langs[1] != "fr" || langs[2] != "zh" {
		t.Errorf("the wants after the languages changed: %v", langs)
	}
}

// The day's work is taken in its order: what a person asked for, new
// arrivals, what was asked for before, then the library, the newest first; a
// title the catalog said takes no subtitle waits; a found want is
// downloaded in the same order; downloads are counted.
func TestSubtitleWorkOrder(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	day := func(d int) *time.Time { x := time.Date(2026, 1, d, 0, 0, 0, 0, time.UTC); return &x }
	for _, ti := range []SubtitleTitle{
		{ItemID: "old", Kind: "movie", Priority: SubtitleLibrary, AiredAt: day(1)},
		{ItemID: "newer", Kind: "movie", Priority: SubtitleLibrary, AiredAt: day(20)},
		{ItemID: "grabbed", Kind: "movie", Priority: SubtitleAskedFor, AiredAt: day(2)},
		{ItemID: "arrived", Kind: "movie", Priority: SubtitleNewArrival, PackagedAt: day(3)},
		{ItemID: "unpackaged", Kind: "movie", Priority: SubtitleNewArrival, PackagedAt: day(4)},
	} {
		if err := s.PutSubtitleTitle(ctx, ti, []string{"de"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetSubtitleCatalog(ctx, "unpackaged", SubtitleCatalog{NotAttachable: "not packaged yet"}); err != nil {
		t.Fatal(err)
	}
	order := func() []string {
		due, err := s.DueSubtitleSearches(ctx, 10, []string{"de"})
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, d := range due {
			ids = append(ids, d.ItemID)
		}
		return ids
	}
	if got := order(); len(got) != 4 || got[0] != "arrived" || got[1] != "grabbed" || got[2] != "newer" || got[3] != "old" {
		t.Errorf("the search order: %v", got)
	}
	asked, err := s.AskSubtitles(ctx, []SubtitleKey{{"old", "de"}, {"missing", "de"}})
	if err != nil || len(asked) != 1 {
		t.Fatalf("asked: %v %v", asked, err)
	}
	if got := order(); got[0] != "old" {
		t.Errorf("asked for first: %v", got)
	}
	// nothing found: not due until its wait ran out
	later := time.Now().Add(7 * 24 * time.Hour)
	if err := s.SetSubtitleCatalog(ctx, "old", SubtitleCatalog{VersionID: "v-old", Attachable: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordSubtitleSearch(ctx, SubtitleKey{"old", "de"}, SubtitleSearchOutcome{State: SubtitleNotFound,
		Trouble: "nothing found", Detail: "nothing for this file", Next: &later}); err != nil {
		t.Fatal(err)
	}
	if got := order(); len(got) != 3 || got[0] != "arrived" {
		t.Errorf("after a miss: %v", got)
	}
	w, _ := s.SubtitleWantOf(ctx, SubtitleKey{"old", "de"})
	if w.AskedAt != nil || w.Searches != 1 || w.SearchedAt == nil {
		t.Errorf("the want not found: %+v", w)
	}
	// found: downloaded in the order of the queue, of titles that take a subtitle
	for _, id := range []string{"newer", "arrived", "grabbed"} {
		if err := s.SetSubtitleCatalog(ctx, id, SubtitleCatalog{VersionID: "v-" + id, Attachable: true}); err != nil {
			t.Fatal(err)
		}
		if err := s.RecordSubtitleSearch(ctx, SubtitleKey{id, "de"}, SubtitleSearchOutcome{State: SubtitleFound,
			Found: map[string]any{"fileId": 1}, Versions: 1, Detail: "found"}); err != nil {
			t.Fatal(err)
		}
	}
	found, err := s.FoundSubtitles(ctx, 10, false)
	if err != nil || len(found) != 3 || found[0].ItemID != "arrived" || found[1].ItemID != "grabbed" || found[2].ItemID != "newer" {
		t.Fatalf("found: %+v %v", found, err)
	}
	if len(found[0].Want.Found) == 0 {
		t.Error("the match is not kept")
	}
	rank, _ := s.FoundSubtitleRank(ctx)
	if rank[SubtitleKey{"arrived", "de"}] != 0 || rank[SubtitleKey{"newer", "de"}] != 2 {
		t.Errorf("rank: %v", rank)
	}
	// five failures in a row: not tried again on its own
	next := time.Now().Add(time.Hour)
	for i := 0; i < 5; i++ {
		if err := s.RecordSubtitleFailed(ctx, SubtitleKey{"newer", "de"}, SubtitleFailure{Trouble: "the catalog doesn't list it",
			Detail: "the catalog took it, and does not list it", Retry: SubtitleRetrySame, Next: &next}); err != nil {
			t.Fatal(err)
		}
	}
	if w, _ := s.SubtitleWantOf(ctx, SubtitleKey{"newer", "de"}); w.State != SubtitleFailed || w.Failures != 5 || w.NextAt != nil {
		t.Errorf("five failures: %+v", w)
	}
	since := time.Now().Add(-time.Minute)
	for _, ok := range []bool{true, true, false} {
		if err := s.RecordSubtitleDownload(ctx, SubtitleDownload{ItemID: "arrived", Language: "de", FileID: 1, How: "auto", OK: ok}); err != nil {
			t.Fatal(err)
		}
	}
	_ = s.RecordSubtitleDownload(ctx, SubtitleDownload{ItemID: "arrived", Language: "de", FileID: 2, How: "manual", OK: true})
	if n, _ := s.SubtitleDownloadsSince(ctx, since, "auto"); n != 2 {
		t.Errorf("automatic downloads: %d", n)
	}
	if n, _ := s.SubtitleDownloadsSince(ctx, since, ""); n != 3 {
		t.Errorf("every download: %d", n)
	}
}

// A subtitle a person removed is wanted again later, and its file never
// taken again; the console's list filters by state, language and title or
// episode, and counts each language's coverage.
func TestSubtitleRemovalAndTheList(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	_ = s.PutSubtitleTitle(ctx, SubtitleTitle{ItemID: "e1", Kind: "episode", Title: "A Show", Season: 4, Episode: 7}, []string{"de", "en"})
	_ = s.PutSubtitleTitle(ctx, SubtitleTitle{ItemID: "m1", Kind: "movie", Title: "A Film"}, []string{"de", "en"})
	k := SubtitleKey{"e1", "de"}
	_ = s.RecordSubtitleAttached(ctx, k, SubtitleAttached{SubtitleID: "sub-1", FileID: 42, How: "auto"})
	_ = s.RecordSubtitleAttached(ctx, SubtitleKey{"m1", "en"}, SubtitleAttached{SubtitleID: "sub-2", FileID: 43, How: "auto"})
	next := time.Now().Add(24 * time.Hour)
	if err := s.ForgetAttachedSubtitle(ctx, k, next, "removed in the console"); err != nil {
		t.Fatal(err)
	}
	_ = s.RecordSubtitleAttached(ctx, k, SubtitleAttached{SubtitleID: "sub-3", FileID: 44, How: "manual"})
	_ = s.ForgetAttachedSubtitle(ctx, k, next, "removed in the console")
	w, _ := s.SubtitleWantOf(ctx, k)
	if w.State != SubtitleWanted || w.SubtitleID != "" || len(w.Blocked) != 2 || w.Blocked[0] != 42 || w.Blocked[1] != 44 {
		t.Errorf("after removal: %+v", w)
	}
	rows, err := s.SubtitleRows(ctx, SubtitleFilter{Language: "de"})
	if err != nil || len(rows) != 2 {
		t.Fatalf("German: %+v %v", rows, err)
	}
	for q, want := range map[string]int{"show": 2, "S04E07": 2, "s04 e07": 2, "film": 2, "nothing": 0} {
		if rows, _ := s.SubtitleRows(ctx, SubtitleFilter{Query: q}); len(rows) != want {
			t.Errorf("%q: %d rows, want %d", q, len(rows), want)
		}
	}
	if rows, _ := s.SubtitleRows(ctx, SubtitleFilter{States: []string{SubtitleHas}}); len(rows) != 1 || rows[0].ItemID != "m1" {
		t.Errorf("had: %+v", rows)
	}
	counts, _ := s.SubtitleStateCounts(ctx, "")
	if counts[SubtitleHas] != 1 || counts[SubtitleWanted] != 3 {
		t.Errorf("counts: %v", counts)
	}
	cov, _ := s.SubtitleCoverageByLanguage(ctx)
	if len(cov) != 2 || cov[1].Language != "en" || cov[1].Has != 1 || cov[1].Total != 2 {
		t.Errorf("coverage: %+v", cov)
	}
	// packaged again: the catalog is asked again, every language not had due now
	_ = s.RecordSubtitleSearch(ctx, SubtitleKey{"m1", "de"}, SubtitleSearchOutcome{State: SubtitleNotFound, Trouble: "nothing found",
		Detail: "nothing", Next: &next})
	if err := s.RenewSubtitleTitle(ctx, "m1"); err != nil {
		t.Fatal(err)
	}
	if w, _ := s.SubtitleWantOf(ctx, SubtitleKey{"m1", "de"}); w.State != SubtitleWanted || w.NextAt == nil || w.NextAt.After(time.Now()) {
		t.Errorf("renewed: %+v", w)
	}
	// the languages unknown for now: a title recorded again keeps its wants
	_ = s.PutSubtitleTitle(ctx, SubtitleTitle{ItemID: "m1", Kind: "movie", Priority: SubtitleNewArrival}, nil)
	if wants, _ := s.SubtitleWantsOf(ctx, "m1"); len(wants) != 2 {
		t.Errorf("wants after a record without languages: %+v", wants)
	}
	if gone, err := s.ForgetSubtitleTitle(ctx, "m1"); !gone || err != nil {
		t.Errorf("forget: %v %v", gone, err)
	}
	if wants, _ := s.SubtitleWantsOf(ctx, "m1"); len(wants) != 0 {
		t.Errorf("wants of a title forgotten: %+v", wants)
	}
	if clip("aüb", 3) != "…" || clip("abc", 3) != "abc" {
		t.Errorf("clip: %q", clip("aüb", 3))
	}
}

// A title packaged again is looked at again whatever its languages' states:
// a match for its old file dropped, its failures and what was said of its
// original forgotten. Failures in a row stay when a search finds a match
// again, and a file is blocked once. A want of a language no longer the
// instance's is left as it is by what the catalog says, and is never due;
// what a person asked for is spent alone when asked so; a title that takes no
// subtitle now waits.
func TestSubtitleRenewalAndWhatStays(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	langs := []string{"de", "en"}
	_ = s.PutSubtitleTitle(ctx, SubtitleTitle{ItemID: "m1", Kind: "movie", Title: "A Film"}, langs)
	if err := s.SetSubtitleCatalog(ctx, "m1", SubtitleCatalog{VersionID: "v1", Attachable: true,
		Fingerprint: &SubtitleFingerprint{Note: "the original cannot be read"}, Has: map[string]string{"de": "own", "en": "own"},
		Languages: langs}); err != nil {
		t.Fatal(err)
	}
	due := func() map[string][]string {
		t.Helper()
		list, err := s.DueSubtitleSearches(ctx, 10, langs)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string][]string{}
		for _, d := range list {
			out[d.ItemID] = d.Languages
		}
		return out
	}
	if got := due(); len(got) != 0 {
		t.Errorf("a title with every language is due: %v", got)
	}
	if err := s.RenewSubtitleTitle(ctx, "m1"); err != nil {
		t.Fatal(err)
	}
	if got, ok := due()["m1"]; !ok || len(got) != 0 {
		t.Errorf("packaged again, it is looked at again: %v %v", got, ok)
	}
	if ti, _ := s.SubtitleTitleByID(ctx, "m1"); ti.ReadAt != nil || ti.OriginalNote != "" || ti.Sum64 != "" {
		t.Errorf("renewed: %+v", ti)
	}

	// its new file lost German; a match found after two failures keeps them
	_ = s.SetSubtitleCatalog(ctx, "m1", SubtitleCatalog{VersionID: "v2", Attachable: true, Has: map[string]string{"en": "own"},
		Languages: langs})
	k := SubtitleKey{"m1", "de"}
	next := time.Now().Add(time.Hour)
	_ = s.RecordSubtitleFailed(ctx, k, SubtitleFailure{Trouble: "the catalog refused it", Retry: SubtitleRetrySame, Next: &next})
	_ = s.RecordSubtitleFailed(ctx, k, SubtitleFailure{Trouble: "the catalog refused it", Retry: SubtitleRetrySame, Next: &next})
	_ = s.RecordSubtitleSearch(ctx, k, SubtitleSearchOutcome{State: SubtitleFound, Found: map[string]any{"fileId": 5}, Versions: 2,
		Detail: "2 found"})
	if w, _ := s.SubtitleWantOf(ctx, k); w.State != SubtitleFound || w.Failures != 2 || w.Versions != 2 || w.Trouble != "" {
		t.Errorf("found again after two failures: %+v", w)
	}
	// the file failed: the next try is another version, one usable less;
	// with none left, there is none
	_ = s.RecordSubtitleFailed(ctx, k, SubtitleFailure{Trouble: "can't read the file", Retry: SubtitleRetryAnother, Next: &next})
	if w, _ := s.SubtitleWantOf(ctx, k); w.State != SubtitleFailed || w.Trouble != "can't read the file" ||
		w.Retry != SubtitleRetryAnother || w.Versions != 1 || w.Failures != 3 {
		t.Errorf("a file that failed: %+v", w)
	}
	_ = s.RecordSubtitleFailed(ctx, k, SubtitleFailure{Trouble: "can't read the file", Retry: SubtitleRetryNone, Next: &next})
	if w, _ := s.SubtitleWantOf(ctx, k); w.Retry != SubtitleRetryNone || w.Versions != 0 {
		t.Errorf("no other version: %+v", w)
	}
	_ = s.RecordSubtitleSearch(ctx, k, SubtitleSearchOutcome{State: SubtitleFound, Found: map[string]any{"fileId": 5}, Versions: 2,
		Detail: "2 found"})
	for i := 0; i < 2; i++ {
		if err := s.BlockSubtitleFile(ctx, k, 5); err != nil {
			t.Fatal(err)
		}
	}
	if w, _ := s.SubtitleWantOf(ctx, k); len(w.Blocked) != 1 || w.Blocked[0] != 5 {
		t.Errorf("blocked: %v", w.Blocked)
	}
	_ = s.RenewSubtitleTitle(ctx, "m1")
	if w, _ := s.SubtitleWantOf(ctx, k); w.State != SubtitleWanted || len(w.Found) != 0 || w.Failures != 0 ||
		w.Detail != "packaged again" || w.Trouble != "packaged again" || w.Retry != "" || len(w.Blocked) != 1 {
		t.Errorf("the match for the old file: %+v", w)
	}

	// Chinese is no longer the instance's: the one acquire attached in it
	// stays as it is, whatever the catalog says, and is never due
	zh := SubtitleKey{"m1", "zh"}
	_ = s.EnsureSubtitleWants(ctx, []string{"de", "en", "zh"})
	_ = s.RecordSubtitleAttached(ctx, zh, SubtitleAttached{SubtitleID: "sub-zh", FileID: 9, How: "auto"})
	_ = s.EnsureSubtitleWants(ctx, langs)
	if err := s.SetSubtitleCatalog(ctx, "m1", SubtitleCatalog{VersionID: "v2", Attachable: true,
		Has: map[string]string{"en": "own"}, Languages: langs}); err != nil {
		t.Fatal(err)
	}
	if w, _ := s.SubtitleWantOf(ctx, zh); w.State != SubtitleHas || w.SubtitleID != "sub-zh" {
		t.Errorf("a language no longer the instance's: %+v", w)
	}
	exec(t, s, `UPDATE subtitle_wants SET state = 'wanted', next_at = NULL WHERE language = 'zh'`)
	if got := due()["m1"]; slices.Contains(got, "zh") || !slices.Contains(got, "de") {
		t.Errorf("due: %v", got)
	}
	if err := s.DropSubtitleWant(ctx, zh); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SubtitleWantOf(ctx, zh); err != ErrNotFound {
		t.Errorf("dropped: %v", err)
	}

	// what a person asked for, alone; a title that takes none now waits
	_ = s.PutSubtitleTitle(ctx, SubtitleTitle{ItemID: "m2", Kind: "movie"}, langs)
	for _, id := range []string{"m1", "m2"} {
		_ = s.SetSubtitleCatalog(ctx, id, SubtitleCatalog{VersionID: "v-" + id, Attachable: true, Languages: langs})
		_ = s.RecordSubtitleSearch(ctx, SubtitleKey{id, "de"}, SubtitleSearchOutcome{State: SubtitleFound,
			Found: map[string]any{"fileId": 1}, Versions: 1, Detail: "found"})
	}
	if asked, err := s.AskSubtitles(ctx, []SubtitleKey{{"m2", "de"}}); err != nil || len(asked) != 1 {
		t.Fatalf("asked: %v %v", asked, err)
	}
	if f, _ := s.FoundSubtitles(ctx, 10, true); len(f) != 1 || f[0].ItemID != "m2" {
		t.Errorf("what was asked for: %+v", f)
	}
	if f, _ := s.FoundSubtitles(ctx, 10, false); len(f) != 2 {
		t.Errorf("every match: %+v", f)
	}
	if err := s.SubtitleTitleNotAttachable(ctx, "m2", "the title has no package in the library"); err != nil {
		t.Fatal(err)
	}
	if f, _ := s.FoundSubtitles(ctx, 10, false); len(f) != 1 || f[0].ItemID != "m1" {
		t.Errorf("of a title that takes none: %+v", f)
	}
	if _, ok := due()["m2"]; ok {
		t.Error("a title that takes none is due")
	}
}
