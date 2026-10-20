package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/laedeli/acquire/internal/config"
	"github.com/laedeli/acquire/internal/events"
	"github.com/laedeli/acquire/internal/identity"
	"github.com/laedeli/acquire/internal/katalog"
	"github.com/laedeli/acquire/internal/opensubtitles"
	"github.com/laedeli/acquire/internal/store"
	"github.com/laedeli/acquire/internal/subtitles"
)

// subtitleCatalog is the catalog's attach seam as acquire uses it: the
// instance's languages, each title's subtitles and its original's
// fingerprint, attaches and removals; and the read side's item names.
type subtitleCatalog struct {
	mu        sync.Mutex
	langs     []string
	titles    map[string]*catalogTitle
	attached  []katalog.AttachRequest
	detached  []string
	refuse    bool              // answer an attach 422
	refuseIn  string            // answer 422 to an attach whose text holds this
	unlisted  bool              // take an attach, and list it not
	down      bool              // answer every call 503
	contents  map[string]string // the text of each subtitle attached, by its id
	fingerRea int               // fingerprint reads
}

type catalogTitle struct {
	attachable bool
	subs       []katalog.CatalogSubtitle
	original   *katalog.Original
	note       string
	name       katalog.ItemName
}

func newSubtitleCatalog() *subtitleCatalog {
	return &subtitleCatalog{langs: []string{"en", "de", "zh"}, titles: map[string]*catalogTitle{}, contents: map[string]string{}}
}

func (c *subtitleCatalog) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		defer c.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if c.down {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"error":"the catalog is starting"}`)
			return
		}
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		switch {
		case r.URL.Path == "/api/library/subtitles/languages":
			_ = json.NewEncoder(w).Encode(map[string]any{"languages": c.langs, "from": "packager.language_whitelist"})
		case len(parts) == 4 && parts[0] == "api" && parts[1] == "v1" && parts[2] == "items":
			ti := c.titles[parts[3]]
			if ti == nil {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(ti.name)
		case len(parts) >= 5 && parts[2] == "items" && parts[4] == "subtitles":
			id := parts[3]
			ti := c.titles[id]
			if ti == nil {
				w.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(w, `{"error":"no such title"}`)
				return
			}
			switch r.Method {
			case http.MethodGet:
				answer := map[string]any{"itemId": id, "type": "movie", "versionId": "v-" + id, "attachable": ti.attachable,
					"subtitles": ti.subs}
				if !ti.attachable {
					answer["versionId"], answer["reason"] = nil, "the title has no package in the library"
				}
				if r.URL.Query().Get("fingerprint") == "true" {
					c.fingerRea++
					answer["original"] = ti.original
					if ti.note != "" {
						answer["originalNote"] = ti.note
					}
				}
				_ = json.NewEncoder(w).Encode(answer)
			case http.MethodPost:
				var req katalog.AttachRequest
				_ = json.NewDecoder(r.Body).Decode(&req)
				if c.refuse || (c.refuseIn != "" && strings.Contains(req.Content, c.refuseIn)) {
					w.WriteHeader(http.StatusUnprocessableEntity)
					_, _ = io.WriteString(w, `{"error":"the subtitle holds no cue"}`)
					return
				}
				for _, sub := range ti.subs { // the same text again: the one there is
					if sub.Attached && sub.Language == req.Language && c.contents[sub.ID] == req.Content {
						_ = json.NewEncoder(w).Encode(map[string]any{"itemId": id, "created": false, "subtitle": sub})
						return
					}
				}
				c.attached = append(c.attached, req)
				origin := req.Origin
				sub := katalog.CatalogSubtitle{ID: fmt.Sprintf("att-%d", len(c.attached)), Language: req.Language,
					Format: "webvtt", SDH: req.SDH, Origin: &origin, Attached: true}
				if !c.unlisted {
					ti.subs = append(ti.subs, sub)
				}
				c.contents[sub.ID] = req.Content
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(map[string]any{"itemId": id, "created": true, "subtitle": sub})
			case http.MethodDelete:
				c.detached = append(c.detached, parts[5])
				kept := ti.subs[:0]
				for _, s := range ti.subs {
					if s.ID != parts[5] {
						kept = append(kept, s)
					}
				}
				ti.subs = kept
				w.WriteHeader(http.StatusNoContent)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// subtitleSource is OpenSubtitles as acquire uses it: searches by the
// title's identities and hash, downloads within a quota, and the files.
type subtitleSource struct {
	mu        sync.Mutex
	srv       *httptest.Server
	results   map[string][]map[string]any // by the imdb_id or parent_tmdb_id searched
	searches  []string
	downloads []int64
	quota     int
}

func newSubtitleSource(t *testing.T) *subtitleSource {
	s := &subtitleSource{results: map[string][]map[string]any{}, quota: 20}
	s.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		switch {
		case r.URL.Path == "/api/v1/login":
			_, _ = io.WriteString(w, `{"user":{"allowed_downloads":20,"level":"Sub leecher"},"token":"t1"}`)
		case r.URL.Path == "/api/v1/infos/user":
			_, _ = io.WriteString(w, fmt.Sprintf(`{"data":{"allowed_downloads":20,"remaining_downloads":%d}}`, s.quota))
		case r.URL.Path == "/api/v1/subtitles":
			s.searches = append(s.searches, r.URL.RawQuery)
			key := r.URL.Query().Get("imdb_id") + r.URL.Query().Get("parent_tmdb_id")
			_ = json.NewEncoder(w).Encode(map[string]any{"total_pages": 1, "page": 1, "data": s.results[key]})
		case r.URL.Path == "/api/v1/download":
			var body struct {
				FileID int64 `json:"file_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if s.quota == 0 {
				w.WriteHeader(http.StatusNotAcceptable)
				_, _ = io.WriteString(w, `{"remaining":0,"message":"quota","reset_time_utc":"2030-01-01T00:00:00.000Z"}`)
				return
			}
			s.quota--
			s.downloads = append(s.downloads, body.FileID)
			_ = json.NewEncoder(w).Encode(map[string]any{"link": s.srv.URL + fmt.Sprintf("/file/%d", body.FileID),
				"remaining": s.quota, "reset_time_utc": "2030-01-01T00:00:00.000Z"})
		case strings.HasPrefix(r.URL.Path, "/file/"):
			_, _ = io.WriteString(w, "1\r\n00:00:01,000 --> 00:00:02,000\r\nfile "+strings.TrimPrefix(r.URL.Path, "/file/")+"\r\n")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func osResult(fileID int64, lang, release string, downloads int, hash bool, extra map[string]any) map[string]any {
	a := map[string]any{"language": lang, "release": release, "download_count": downloads, "moviehash_match": hash,
		"files": []map[string]any{{"file_id": fileID, "file_name": release + ".srt"}}}
	for k, v := range extra {
		a[k] = v
	}
	return map[string]any{"attributes": a}
}

type subtitleRig struct {
	svc *Service
	cat *subtitleCatalog
	src *subtitleSource
}

func newSubtitleRig(t *testing.T, budget int) *subtitleRig {
	t.Helper()
	st := testStore(t)
	r := &subtitleRig{cat: newSubtitleCatalog(), src: newSubtitleSource(t)}
	srv := r.cat.server(t)
	cfg := config.Config{SubtitlesEnabled: true, SubtitleDailyBudget: budget, OpenSubtitlesAPIKey: "k",
		OpenSubtitlesUsername: "u", OpenSubtitlesPassword: "p", Version: "0.5.0", SvcClientID: "acquire-svc"}
	r.svc = New(cfg, st, nil, katalog.New(srv.URL, srv.URL, nil), nil, nil, nil)
	r.svc.subs.client = opensubtitles.New(opensubtitles.Config{BaseURL: r.src.srv.URL + "/api/v1", APIKey: "k",
		Username: "u", Password: "p", UserAgent: "acquire v0.5.0", MinInterval: time.Millisecond, HTTPClient: r.src.srv.Client()})
	return r
}

// a movie acquire tracks, held and playable as item, with the release it
// was grabbed from.
func (r *subtitleRig) movie(t *testing.T, item string, tmdb int64, imdb, title string) {
	t.Helper()
	ctx := context.Background()
	if err := r.svc.st.UpsertTitle(ctx, store.Title{TMDBID: tmdb, Kind: "movie", Title: title, Year: 2019, IMDBID: imdb,
		Monitored: true}); err != nil {
		t.Fatal(err)
	}
	id := store.TitleID("movie", tmdb)
	if err := r.svc.st.UpsertTarget(ctx, store.Target{ID: id, TitleID: id, Kind: "movie", Monitored: true, State: store.TargetWanted}); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.st.HoldTarget(ctx, store.Hold{TargetID: id, ItemID: item, Playable: true, Source: "grab",
		Release: strings.ReplaceAll(title, " ", ".") + ".2019.1080p.WEB-DL-GRP"}); err != nil {
		t.Fatal(err)
	}
}

func (r *subtitleRig) want(t *testing.T, item, lang string) store.SubtitleWant {
	t.Helper()
	w, err := r.svc.st.SubtitleWantOf(context.Background(), store.SubtitleKey{ItemID: item, Language: lang})
	if err != nil {
		t.Fatalf("%s %s: %v", item, lang, err)
	}
	return w
}

// A packaged movie is a new arrival: the catalog says what it has (English,
// its own) and what its original is matched by; one search for German and
// Chinese finds a German one made for the file and only Traditional Chinese;
// both are downloaded, as SubRip, and attached as the provider's — the
// Chinese as zh-Hant — and only once the catalog lists them is each had.
func TestANewArrivalGetsItsSubtitles(t *testing.T) {
	r := newSubtitleRig(t, 18)
	ctx := context.Background()
	r.movie(t, "item-m1", 101, "tt0000101", "A Film")
	r.cat.titles["item-m1"] = &catalogTitle{attachable: true,
		subs:     []katalog.CatalogSubtitle{{ID: "own-en", Language: "eng", Format: "webvtt"}},
		original: &katalog.Original{SizeBytes: 3000000, Sum64: "0123456789abcdef", Labels: map[string]any{"medium": "web", "edition": nil}}}
	r.src.results["101"] = []map[string]any{
		osResult(11, "de", "A.Film.2019.720p.HDTV", 9000, false, nil),
		osResult(12, "de", "A.Film.2019.1080p.WEB-DL-GRP", 50, true, nil),
		osResult(13, "zh-tw", "A.Film.2019.BluRay", 100, false, map[string]any{"hearing_impaired": true}),
		osResult(14, "de", "A.Film.2019.MT", 99999, false, map[string]any{"machine_translated": true}),
	}
	if err := r.svc.OnPackaged(ctx, events.ItemEvent{ItemID: "item-m1", Type: "movie",
		ExternalIDs: identity.IDs{"tmdb": "101", "imdb": "tt0000101"}}); err != nil {
		t.Fatal(err)
	}
	ti, err := r.svc.st.SubtitleTitleByID(ctx, "item-m1")
	if err != nil || ti.Priority != store.SubtitleNewArrival || ti.Title != "A Film" || ti.Release == "" {
		t.Fatalf("the arrival: %+v %v", ti, err)
	}
	r.svc.SubtitlePass(ctx)

	if len(r.src.searches) != 1 || !strings.Contains(r.src.searches[0], "languages=de%2Czh-cn%2Czh-tw") ||
		!strings.Contains(r.src.searches[0], "moviehash=0123456789abcdef") || !strings.Contains(r.src.searches[0], "imdb_id=101") {
		t.Errorf("the searches: %v", r.src.searches)
	}
	if len(r.src.downloads) != 2 || r.src.downloads[0] != 12 || r.src.downloads[1] != 13 {
		t.Errorf("downloads: %v", r.src.downloads)
	}
	if len(r.cat.attached) != 2 {
		t.Fatalf("attached: %+v", r.cat.attached)
	}
	de, zh := r.cat.attached[0], r.cat.attached[1]
	if de.Language != "de" || de.Origin != "provider" || !strings.Contains(de.Content, "file 12") || de.SDH {
		t.Errorf("the German one: %+v", de)
	}
	if zh.Language != "zh-Hant" || !zh.SDH {
		t.Errorf("the Chinese one: %+v", zh)
	}
	if w := r.want(t, "item-m1", "en"); w.State != store.SubtitleHas || w.HasKind != "own" {
		t.Errorf("English: %+v", w)
	}
	if w := r.want(t, "item-m1", "de"); w.State != store.SubtitleHas || w.HasKind != "attached" || w.SubtitleID != "att-1" ||
		w.Match != subtitles.MatchFile || w.FileID != 12 || !strings.Contains(w.Preview, "00:00:01  file 12") || w.AttachedHow != "auto" {
		t.Errorf("German: %+v", w)
	}
	if w := r.want(t, "item-m1", "zh"); w.State != store.SubtitleHas || w.HasKind != "traditional" {
		t.Errorf("Chinese: %+v", w)
	}
	// the console says how each came to be
	had, _ := r.svc.SubtitleList(ctx, SubStatusHas, "", "", 10)
	lines := map[string]string{}
	for _, it := range had {
		lines[it.Language] = it.Detail
	}
	if lines["en"] != "came with the file" || lines["de"] != "added by acquire · matches this file" ||
		lines["zh"] != "added by acquire · in Traditional characters" {
		t.Errorf("the lines of what it has: %v", lines)
	}
	if n, _ := r.svc.st.SubtitleDownloadsSince(ctx, dayStart(time.Now()), "auto"); n != 2 {
		t.Errorf("downloads counted: %d", n)
	}
	// Another pass does nothing: everything is had.
	r.svc.SubtitlePass(ctx)
	if len(r.src.searches) != 1 || len(r.src.downloads) != 2 || r.cat.fingerRea != 1 {
		t.Errorf("a second pass: %d searches, %d downloads, %d fingerprints", len(r.src.searches), len(r.src.downloads), r.cat.fingerRea)
	}
}

// The day's budget is kept: what it does not reach waits, found, in the
// order downloads are spent, and says so; nothing found is looked for again
// after a week; only machine-translated results are never taken on their
// own, and say so; a catalog that refuses the file is a want that needs
// attention.
func TestTheBudgetAndWhatIsNotFound(t *testing.T) {
	r := newSubtitleRig(t, 1)
	ctx := context.Background()
	for i, item := range []string{"item-a", "item-b", "item-c", "item-d"} {
		r.movie(t, item, int64(201+i), fmt.Sprintf("tt%07d", 201+i), "Film "+item)
		r.cat.titles[item] = &catalogTitle{attachable: true, subs: []katalog.CatalogSubtitle{{ID: "own", Language: "en"},
			{ID: "own-zh", Language: "chi"}}}
	}
	r.src.results["201"] = []map[string]any{osResult(21, "de", "Film.A.WEB", 10, false, nil)}
	r.src.results["202"] = []map[string]any{osResult(22, "de", "Film.B.WEB", 10, false, nil)}
	r.src.results["204"] = []map[string]any{osResult(24, "de", "Film.D", 10, false, map[string]any{"ai_translated": true})}
	if n, err := r.svc.st.FillSubtitleTitles(ctx, 10, []string{"en", "de", "zh"}); err != nil || n != 4 {
		t.Fatalf("fill: %d %v", n, err)
	}
	r.svc.SubtitlePass(ctx)
	if len(r.src.downloads) != 1 {
		t.Fatalf("downloads with a budget of one: %v", r.src.downloads)
	}
	var had, waiting string
	for _, item := range []string{"item-a", "item-b"} {
		switch r.want(t, item, "de").State {
		case store.SubtitleHas:
			had = item
		case store.SubtitleFound:
			waiting = item
		}
	}
	if had == "" || waiting == "" {
		t.Fatalf("one had, one waiting: %q %q", had, waiting)
	}
	c := r.want(t, "item-c", "de")
	if c.State != store.SubtitleNotFound || c.NextAt == nil || time.Until(*c.NextAt) < 6*24*time.Hour || c.Detail != "nothing in this language" {
		t.Errorf("nothing found: %+v", c)
	}
	d := r.want(t, "item-d", "de")
	if d.State != store.SubtitleNotFound || !strings.Contains(d.Detail, "only 1 AI-translated") {
		t.Errorf("only an AI translation: %+v", d)
	}

	// The console says each in words.
	items, err := r.svc.SubtitleList(ctx, "", "de", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	status := map[string]SubtitleItem{}
	for _, it := range items {
		status[it.ItemID] = it
	}
	if it := status[waiting]; it.Status != SubStatusWaiting || it.Detail != "found · waits for the daily download limit (about 1 day)" ||
		it.Found == nil || it.Found.Match != subtitles.MatchUnknown {
		t.Errorf("waiting for the budget: %+v", it)
	}
	if it := status[had]; it.Status != SubStatusHas || it.Detail != "added by acquire · may be out of sync" || !it.Removable {
		t.Errorf("had: %+v", it)
	}
	if it := status["item-c"]; it.Status != SubStatusNotFound || !strings.HasPrefix(it.Detail, "nothing found · again in ") ||
		it.Why != "nothing in this language" {
		t.Errorf("not found: %+v", it)
	}
	if it := status["item-d"]; it.Detail == "" || !strings.HasPrefix(it.Detail, "only machine translations · again in ") {
		t.Errorf("only an AI translation: %+v", it)
	}
	o := r.svc.SubtitleOverview(ctx, "")
	if o.Counts[SubStatusHas] != 9 || o.Counts[SubStatusWaiting] != 1 || o.Counts[SubStatusNotFound] != 2 ||
		o.Budget.Daily != 1 || o.Budget.Used != 1 || o.Budget.Left != 0 || len(o.Languages) != 3 || o.Languages[1].Name != "German" {
		t.Errorf("the overview: %+v", o)
	}

	// Asked for in the console: first in line, searched and downloaded on the
	// next day's budget; a catalog refusing the file needs attention.
	asked, err := r.svc.FindSubtitles(ctx, []store.SubtitleKey{{ItemID: "item-c", Language: "de"}})
	if err != nil || len(asked) != 1 {
		t.Fatalf("find: %v %v", asked, err)
	}
	if it, _ := r.svc.SubtitleList(ctx, SubStatusWay, "de", "", 10); len(it) != 1 || it[0].ItemID != "item-c" {
		t.Errorf("on its way: %+v", it)
	}
	r.cat.refuse = true
	r.src.results["203"] = []map[string]any{osResult(23, "de", "Film.C", 10, false, nil)}
	// a pick, though the automatic budget is spent: the account's own download
	pick := store.SubtitleKey{ItemID: "item-c", Language: "de"}
	c23 := subtitles.Candidate{Result: opensubtitles.Result{FileID: 23, Language: "de", Release: "Film.C"}}
	if err := r.svc.PickSubtitle(ctx, pick, c23); err == nil || errors.Is(err, ErrNoDownloadsLeft) {
		t.Errorf("a pick the catalog refused: %v", err)
	}
	// the only version there is, refused: none is left, looked for again after days
	w := r.want(t, "item-c", "de")
	if w.State != store.SubtitleFailed || !strings.Contains(w.Detail, "the subtitle holds no cue") || w.Trouble != "the catalog refused it" ||
		w.Retry != store.SubtitleRetryNone || w.NextAt == nil || time.Until(*w.NextAt) < 6*24*time.Hour {
		t.Errorf("refused by the catalog: %+v", w)
	}
	if it, _ := r.svc.SubtitleList(ctx, SubStatusAttention, "de", "", 10); len(it) != 1 ||
		!strings.HasPrefix(it[0].Detail, "the catalog refused it · no other version yet · again in ") ||
		!strings.Contains(it[0].Why, "HTTP 422") {
		t.Errorf("its line: %+v", it)
	}
	if left, used := r.svc.BudgetLeft(ctx); left != 0 || used != 1 {
		t.Errorf("a pick is no automatic download: %d left, %d used", left, used)
	}
}

// A person picks a version: downloaded at once, from the account's own
// downloads, whatever the budget (none here), the picker saying how many the
// account has left; refused only when the account has none left. One a
// person removes is removed from the catalog, its file never taken again for
// the title, and another looked for after a day.
func TestAPickAndARemoval(t *testing.T) {
	r := newSubtitleRig(t, 0)
	ctx := context.Background()
	r.movie(t, "item-p", 301, "tt0000301", "A Picked Film")
	r.cat.titles["item-p"] = &catalogTitle{attachable: true}
	r.src.results["301"] = []map[string]any{osResult(31, "en", "A.Picked.Film.WEB", 5, false, nil),
		osResult(32, "en", "A.Picked.Film.BluRay", 500, false, nil)}
	if err := r.svc.st.PutSubtitleTitle(ctx, store.SubtitleTitle{ItemID: "item-p", Kind: "movie", Title: "A Picked Film",
		IMDbID: "tt0000301"}, []string{"en"}); err != nil {
		t.Fatal(err)
	}
	k := store.SubtitleKey{ItemID: "item-p", Language: "en"}
	found, err := r.svc.SearchSubtitleTitle(ctx, k)
	if err != nil || len(found.Candidates) != 2 || found.Candidates[0].FileID != 32 || !found.Candidates[0].Best ||
		found.Left == nil || *found.Left != 20 {
		t.Fatalf("the search (the account asked for its count): %+v %v", found, err)
	}
	if err := r.svc.PickSubtitle(ctx, k, found.Candidates[1]); err != nil {
		t.Fatal(err)
	}
	if again, _ := r.svc.SearchSubtitleTitle(ctx, k); again.Left == nil || *again.Left != 19 {
		t.Errorf("the downloads left after a pick: %v", again.Left)
	}
	if o := r.svc.SubtitleOverview(ctx, ""); o.Budget.AccountLeft == nil || *o.Budget.AccountLeft != 19 || o.Budget.Manual != 1 ||
		o.Budget.Used != 0 {
		t.Errorf("the overview's one number: %+v", o.Budget)
	}
	if w := r.want(t, "item-p", "en"); w.State != store.SubtitleHas || w.FileID != 31 || w.AttachedHow != "manual" {
		t.Errorf("picked: %+v", w)
	}
	if n, _ := r.svc.st.SubtitleDownloadsSince(ctx, dayStart(time.Now()), "manual"); n != 1 {
		t.Errorf("manual downloads: %d", n)
	}
	if err := r.svc.RemoveSubtitle(ctx, k); err != nil {
		t.Fatal(err)
	}
	if len(r.cat.detached) != 1 || r.cat.detached[0] != "att-1" {
		t.Errorf("detached: %v", r.cat.detached)
	}
	w := r.want(t, "item-p", "en")
	if w.State != store.SubtitleWanted || len(w.Blocked) != 1 || w.Blocked[0] != 31 || w.NextAt == nil || time.Until(*w.NextAt) < 23*time.Hour {
		t.Errorf("removed: %+v", w)
	}
	again, _ := r.svc.SearchSubtitleTitle(ctx, k)
	for _, c := range again.Candidates {
		if c.FileID == 31 {
			t.Error("a removed file is offered again")
		}
	}
	if err := r.svc.RemoveSubtitle(ctx, k); err == nil {
		t.Error("removed what acquire did not attach")
	}
	// the account has none left: a pick is refused, the want as it was
	r.src.quota = 0
	if err := r.svc.PickSubtitle(ctx, k, again.Candidates[0]); !errors.Is(err, ErrNoDownloadsLeft) {
		t.Errorf("a pick with the account empty: %v", err)
	}
	if err := r.svc.PickSubtitle(ctx, k, again.Candidates[0]); !errors.Is(err, ErrNoDownloadsLeft) {
		t.Errorf("a second pick with the account empty: %v", err)
	}
	if w := r.want(t, "item-p", "en"); w.State != store.SubtitleWanted || w.Failures != 0 {
		t.Errorf("after the refused picks: %+v", w)
	}
}

// A file the catalog refuses for what it holds is not downloaded again for
// the title: looked for again, the next best is taken. A catalog that does
// not answer gets no download spent.
func TestAFileTheCatalogRefusesIsNotTakenAgain(t *testing.T) {
	r := newSubtitleRig(t, 18)
	ctx := context.Background()
	own := []katalog.CatalogSubtitle{{ID: "own-en", Language: "en"}, {ID: "own-zh", Language: "zh"}}
	r.movie(t, "item-r", 401, "tt0000401", "A Refused Film")
	r.cat.titles["item-r"] = &catalogTitle{attachable: true, subs: own}
	r.src.results["401"] = []map[string]any{osResult(41, "de", "A.Refused.Film.2019.1080p.WEB-DL-GRP", 500, true, nil),
		osResult(42, "de", "A.Refused.Film.2019.720p.HDTV", 50, false, nil)}
	r.cat.refuseIn = "file 41"
	r.svc.SubtitlePass(ctx)
	if w := r.want(t, "item-r", "de"); w.State != store.SubtitleFailed || len(w.Blocked) != 1 || w.Blocked[0] != 41 ||
		!strings.Contains(w.Detail, "the subtitle holds no cue") || w.Retry != store.SubtitleRetryAnother || w.Versions != 1 {
		t.Fatalf("refused: %+v", w)
	}
	if it, _ := r.svc.SubtitleList(ctx, SubStatusAttention, "de", "", 10); len(it) != 1 ||
		!strings.HasPrefix(it[0].Detail, "the catalog refused it · tries another version in ") {
		t.Errorf("its line: %+v", it)
	}
	if _, err := r.svc.FindSubtitles(ctx, []store.SubtitleKey{{ItemID: "item-r", Language: "de"}}); err != nil {
		t.Fatal(err)
	}
	r.svc.SubtitlePass(ctx)
	if w := r.want(t, "item-r", "de"); w.State != store.SubtitleHas || w.FileID != 42 {
		t.Errorf("the next best: %+v", w)
	}
	if len(r.src.downloads) != 2 || r.src.downloads[0] != 41 || r.src.downloads[1] != 42 {
		t.Errorf("downloads: %v", r.src.downloads)
	}

	// a match in line while the catalog does not answer
	k := store.SubtitleKey{ItemID: "item-s", Language: "de"}
	r.cat.titles["item-s"] = &catalogTitle{attachable: true, subs: own}
	_ = r.svc.st.PutSubtitleTitle(ctx, store.SubtitleTitle{ItemID: "item-s", Kind: "movie", IMDbID: "tt0000402"}, []string{"en", "de", "zh"})
	_ = r.svc.st.SetSubtitleCatalog(ctx, "item-s", store.SubtitleCatalog{VersionID: "v-item-s", Attachable: true,
		Has: map[string]string{"en": "own", "zh": "own"}, Fingerprint: &store.SubtitleFingerprint{Note: "none"}})
	_ = r.svc.st.RecordSubtitleSearch(ctx, k, store.SubtitleSearchOutcome{State: store.SubtitleFound,
		Found: subtitles.Candidate{Result: opensubtitles.Result{FileID: 43, Language: "de"}, Tag: "de", Best: true}, Versions: 1,
		Detail: "1 found"})
	r.cat.down = true
	r.svc.SubtitlePass(ctx)
	if len(r.src.downloads) != 2 {
		t.Errorf("downloads while the catalog does not answer: %v", r.src.downloads)
	}
	if w := r.want(t, "item-s", "de"); w.State != store.SubtitleFound || w.Failures != 0 {
		t.Errorf("the match in line: %+v", w)
	}
	r.cat.down = false
	r.svc.SubtitlePass(ctx)
	if w := r.want(t, "item-s", "de"); w.State != store.SubtitleHas || w.FileID != 43 {
		t.Errorf("once the catalog answers: %+v", w)
	}
}

// A subtitle the catalog takes and does not list is no fault of the file:
// the same version is tried again, the line says so.
func TestAFailureNotOfTheFile(t *testing.T) {
	r := newSubtitleRig(t, 18)
	ctx := context.Background()
	r.movie(t, "item-u", 451, "tt0000451", "An Unlisted Film")
	r.cat.titles["item-u"] = &catalogTitle{attachable: true, subs: []katalog.CatalogSubtitle{{ID: "own-en", Language: "en"},
		{ID: "own-zh", Language: "zh"}}}
	r.src.results["451"] = []map[string]any{osResult(45, "de", "An.Unlisted.Film.2019.1080p.WEB-DL-GRP", 10, true, nil)}
	r.cat.unlisted = true
	r.svc.SubtitlePass(ctx)
	w := r.want(t, "item-u", "de")
	if w.State != store.SubtitleFailed || w.Retry != store.SubtitleRetrySame || len(w.Blocked) != 0 || w.Trouble != "the catalog doesn't list it" {
		t.Fatalf("not listed: %+v", w)
	}
	if it, _ := r.svc.SubtitleList(ctx, SubStatusAttention, "de", "", 10); len(it) != 1 ||
		!strings.HasPrefix(it[0].Detail, "the catalog doesn't list it · again in ") {
		t.Errorf("its line: %+v", it)
	}
}

// A title packaged again is looked at again though it had every language:
// its new file lost the German acquire attached, which is looked for anew,
// by the new file's fingerprint, in the same pass; a match that waited for
// the old file is not taken, the title searched again first.
func TestATitlePackagedAgainIsLookedAtAgain(t *testing.T) {
	r := newSubtitleRig(t, 18)
	ctx := context.Background()
	r.movie(t, "item-n", 501, "tt0000501", "A New Cut")
	cat := &catalogTitle{attachable: true, subs: []katalog.CatalogSubtitle{{ID: "own-en", Language: "en"}, {ID: "own-zh", Language: "zh"}},
		original: &katalog.Original{SizeBytes: 1000, Sum64: "1111111111111111"}}
	r.cat.titles["item-n"] = cat
	r.src.results["501"] = []map[string]any{osResult(51, "de", "A.New.Cut.2019.1080p.WEB-DL-GRP", 10, true, nil)}
	arrive := func() {
		t.Helper()
		if err := r.svc.OnPackaged(ctx, events.ItemEvent{ItemID: "item-n", Type: "movie",
			ExternalIDs: identity.IDs{"tmdb": "501", "imdb": "tt0000501"}}); err != nil {
			t.Fatal(err)
		}
		r.svc.SubtitlePass(ctx)
	}
	arrive()
	if w := r.want(t, "item-n", "de"); w.State != store.SubtitleHas || w.SubtitleID != "att-1" {
		t.Fatalf("German: %+v", w)
	}
	// another file: the catalog carried the German one not over
	cat.subs, cat.original = cat.subs[:2], &katalog.Original{SizeBytes: 2000, Sum64: "2222222222222222"}
	r.src.results["501"] = []map[string]any{osResult(52, "de", "A.New.Cut.2019.2160p.BluRay", 10, true, nil)}
	arrive()
	if w := r.want(t, "item-n", "de"); w.State != store.SubtitleHas || w.FileID != 52 || w.SubtitleID != "att-2" {
		t.Errorf("German for the new file: %+v", w)
	}
	if last := r.src.searches[len(r.src.searches)-1]; !strings.Contains(last, "moviehash=2222222222222222") || r.cat.fingerRea != 2 {
		t.Errorf("the new file's fingerprint: %d reads, the search %s", r.cat.fingerRea, last)
	}

	// a match that waits for the budget when the title is packaged again
	cat.subs = cat.subs[:2]
	r.svc.cfg.SubtitleDailyBudget = 0
	r.src.results["501"] = []map[string]any{osResult(53, "de", "A.New.Cut.2019.1080p.WEB-DL-GRP", 10, true, nil)}
	arrive()
	var waiting subtitles.Candidate
	if w := r.want(t, "item-n", "de"); w.State != store.SubtitleFound || json.Unmarshal(w.Found, &waiting) != nil || waiting.FileID != 53 {
		t.Fatalf("waiting: %+v", w)
	}
	searched := len(r.src.searches)
	r.svc.cfg.SubtitleDailyBudget = 18
	r.src.results["501"] = []map[string]any{osResult(54, "de", "A.New.Cut.2019.1080p.WEB-DL-GRP", 10, true, nil)}
	arrive()
	if w := r.want(t, "item-n", "de"); w.State != store.SubtitleHas || w.FileID != 54 || len(r.src.searches) != searched+1 {
		t.Errorf("after the next packaging: %+v, %d searches", w, len(r.src.searches)-searched)
	}
	for _, f := range r.src.downloads {
		if f == 53 {
			t.Error("the match for the file before was downloaded")
		}
	}
}

// A version a person picks for a language acquire filled takes the place of
// the one before: that one is removed from the catalog, its file never taken
// again; the same file picked again changes nothing.
func TestAPickTakesThePlaceOfTheOneBefore(t *testing.T) {
	r := newSubtitleRig(t, 18)
	ctx := context.Background()
	r.cat.titles["item-q"] = &catalogTitle{attachable: true}
	r.src.results["601"] = []map[string]any{osResult(61, "en", "A.Second.Pick.WEB", 500, false, nil),
		osResult(62, "en", "A.Second.Pick.BluRay", 5, false, nil)}
	if err := r.svc.st.PutSubtitleTitle(ctx, store.SubtitleTitle{ItemID: "item-q", Kind: "movie", Title: "A Second Pick",
		IMDbID: "tt0000601"}, []string{"en"}); err != nil {
		t.Fatal(err)
	}
	k := store.SubtitleKey{ItemID: "item-q", Language: "en"}
	found, err := r.svc.SearchSubtitleTitle(ctx, k)
	if err != nil || len(found.Candidates) != 2 {
		t.Fatalf("the search: %+v %v", found, err)
	}
	pick := func(fileID int64) {
		t.Helper()
		for _, c := range found.Candidates {
			if c.FileID == fileID {
				if err := r.svc.PickSubtitle(ctx, k, c); err != nil {
					t.Fatal(err)
				}
				return
			}
		}
		t.Fatalf("no candidate %d", fileID)
	}
	pick(61)
	pick(62)
	if len(r.cat.detached) != 1 || r.cat.detached[0] != "att-1" {
		t.Errorf("detached: %v", r.cat.detached)
	}
	if w := r.want(t, "item-q", "en"); w.SubtitleID != "att-2" || w.FileID != 62 || len(w.Blocked) != 1 || w.Blocked[0] != 61 {
		t.Errorf("the pick: %+v", w)
	}
	pick(62)
	if subs := r.cat.titles["item-q"].subs; len(r.cat.detached) != 1 || len(subs) != 1 || subs[0].ID != "att-2" {
		t.Errorf("picked again: %v detached, %+v", r.cat.detached, subs)
	}
}

// A language no longer the instance's: the subtitle acquire attached in it
// stays, removable, and is not looked for again when the title is; removed,
// its want goes.
func TestALanguageNoLongerTheInstances(t *testing.T) {
	r := newSubtitleRig(t, 18)
	ctx := context.Background()
	r.movie(t, "item-l", 701, "tt0000701", "A Language Film")
	r.cat.titles["item-l"] = &catalogTitle{attachable: true, subs: []katalog.CatalogSubtitle{{ID: "own-en", Language: "en"},
		{ID: "own-de", Language: "de"}}}
	r.src.results["701"] = []map[string]any{osResult(71, "zh-cn", "A.Language.Film.WEB", 10, true, nil)}
	r.svc.SubtitlePass(ctx)
	k := store.SubtitleKey{ItemID: "item-l", Language: "zh"}
	if w := r.want(t, "item-l", "zh"); w.State != store.SubtitleHas || w.SubtitleID != "att-1" {
		t.Fatalf("Chinese: %+v", w)
	}
	r.cat.langs = []string{"en", "de"}
	r.svc.subs.langs = nil // the platform's languages read again
	if err := r.svc.OnPackaged(ctx, events.ItemEvent{ItemID: "item-l", Type: "movie",
		ExternalIDs: identity.IDs{"tmdb": "701", "imdb": "tt0000701"}}); err != nil {
		t.Fatal(err)
	}
	r.svc.SubtitlePass(ctx)
	if w := r.want(t, "item-l", "zh"); w.State != store.SubtitleHas || w.SubtitleID != "att-1" {
		t.Errorf("Chinese, no longer the instance's: %+v", w)
	}
	if len(r.src.downloads) != 1 || len(r.src.searches) != 1 {
		t.Errorf("looked for again: %v downloads, %d searches", r.src.downloads, len(r.src.searches))
	}
	if err := r.svc.RemoveSubtitle(ctx, k); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.st.SubtitleWantOf(ctx, k); err != store.ErrNotFound {
		t.Errorf("the want after the removal: %v", err)
	}
}

// With downloading on its own off, titles are searched and nothing is
// downloaded but what a person asks for, from the day's budget; the rest
// says it waits for you to pick it.
func TestDownloadingOnItsOwnOff(t *testing.T) {
	r := newSubtitleRig(t, 18)
	ctx := context.Background()
	if _, err := r.svc.PutSubtitleSettings(ctx, SubtitleSettings{Auto: false}, 0, "admin"); err != nil {
		t.Fatal(err)
	}
	for i, item := range []string{"item-x", "item-y"} {
		r.movie(t, item, int64(801+i), fmt.Sprintf("tt%07d", 801+i), "Film "+item)
		r.cat.titles[item] = &catalogTitle{attachable: true, subs: []katalog.CatalogSubtitle{{ID: "own", Language: "en"},
			{ID: "own-zh", Language: "zh"}}}
		r.src.results[fmt.Sprint(801+i)] = []map[string]any{osResult(int64(81+i), "de", "Film.WEB", 10, false, nil)}
	}
	r.svc.SubtitlePass(ctx)
	if len(r.src.downloads) != 0 || r.want(t, "item-x", "de").State != store.SubtitleFound {
		t.Fatalf("searched only: %v, %+v", r.src.downloads, r.want(t, "item-x", "de"))
	}
	if _, err := r.svc.FindSubtitles(ctx, []store.SubtitleKey{{ItemID: "item-x", Language: "de"}}); err != nil {
		t.Fatal(err)
	}
	items, _ := r.svc.SubtitleList(ctx, "", "de", "", 10)
	for _, it := range items {
		switch {
		case it.ItemID == "item-x" && (it.Status != SubStatusWay || !strings.Contains(it.Detail, "in today's downloads")):
			t.Errorf("asked for: %+v", it)
		case it.ItemID == "item-y" && (it.Status != SubStatusWaiting || it.Detail != "found · automatic downloads are off: pick it to download"):
			t.Errorf("not asked for: %+v", it)
		}
	}
	r.svc.SubtitlePass(ctx)
	if len(r.src.downloads) != 1 || r.src.downloads[0] != 81 || r.want(t, "item-x", "de").State != store.SubtitleHas ||
		r.want(t, "item-y", "de").State != store.SubtitleFound {
		t.Errorf("what was asked for: %v", r.src.downloads)
	}
	if left, used := r.svc.BudgetLeft(ctx); left != 17 || used != 1 {
		t.Errorf("the budget: %d left, %d used", left, used)
	}
}

// The picker for an episode offers that episode's subtitles alone, though
// the source answered others too: by its word on their episode, or by their
// names. The search asks for the series, the season and the episode.
func TestThePickerOffersTheEpisodesOwn(t *testing.T) {
	r := newSubtitleRig(t, 18)
	ctx := context.Background()
	r.cat.titles["item-e8"] = &catalogTitle{attachable: true}
	if err := r.svc.st.PutSubtitleTitle(ctx, store.SubtitleTitle{ItemID: "item-e8", Kind: "episode", Title: "The Quiet Orchard",
		Season: 1, Episode: 8, ParentTMDBID: 901}, []string{"de"}); err != nil {
		t.Fatal(err)
	}
	episode := func(s, e int) map[string]any {
		return map[string]any{"feature_details": map[string]any{"season_number": s, "episode_number": e}}
	}
	r.src.results["901"] = []map[string]any{
		osResult(91, "de", "The.Quiet.Orchard.S01E01.1080p.WEB-DL-GRP", 9000, false, episode(1, 1)),
		osResult(92, "de", "The.Quiet.Orchard.S01E01.720p.HDTV", 8000, false, nil),
		osResult(93, "de", "The.Quiet.Orchard.S01E08.1080p.WEB-DL-GRP", 50, false, episode(1, 8)),
	}
	found, err := r.svc.SearchSubtitleTitle(ctx, store.SubtitleKey{ItemID: "item-e8", Language: "de"})
	if err != nil {
		t.Fatal(err)
	}
	if len(found.Candidates) != 1 || found.Candidates[0].FileID != 93 || !found.Candidates[0].Best || found.Left == nil || *found.Left != 20 {
		t.Errorf("the picker: %+v, %v left", found.Candidates, found.Left)
	}
	if q := r.src.searches[len(r.src.searches)-1]; !strings.Contains(q, "parent_tmdb_id=901") ||
		!strings.Contains(q, "season_number=1") || !strings.Contains(q, "episode_number=8") {
		t.Errorf("the search: %s", q)
	}
}

// Chinese in a named script is had in that script alone: a title with only a
// subtitle in Traditional characters lacks Simplified Chinese — searched for
// in Simplified alone, counted open, its row saying what it has — and has
// Chinese of no script, in Traditional characters.
func TestChineseOfAScript(t *testing.T) {
	traditionalOnly := func() *catalogTitle {
		return &catalogTitle{attachable: true, subs: []katalog.CatalogSubtitle{{ID: "own-en", Language: "en"},
			{ID: "own-de", Language: "de"}, {ID: "own-zh", Language: "zh-Hant"}}}
	}
	ctx := context.Background()

	r := newSubtitleRig(t, 18)
	r.cat.langs = []string{"en", "de", "zh-Hans"}
	r.movie(t, "item-t", 951, "tt0000951", "A Traditional Film")
	r.cat.titles["item-t"] = traditionalOnly()
	r.svc.SubtitlePass(ctx)
	if len(r.src.searches) != 1 || !strings.Contains(r.src.searches[0], "languages=zh-cn") || strings.Contains(r.src.searches[0], "zh-tw") {
		t.Errorf("the search: %v", r.src.searches)
	}
	w := r.want(t, "item-t", "zh-Hans")
	if w.State != store.SubtitleNotFound || w.OtherScript != "hant" {
		t.Errorf("Simplified, with a Traditional one: %+v", w)
	}
	if it, _ := r.svc.SubtitleList(ctx, SubStatusNotFound, "zh-Hans", "", 10); len(it) != 1 || it[0].LanguageName != "Chinese (Simplified)" ||
		!strings.HasPrefix(it[0].Detail, "nothing found · has one in Traditional characters · again in ") {
		t.Errorf("its line: %+v", it)
	}
	if o := r.svc.SubtitleOverview(ctx, ""); len(o.Languages) != 3 || o.Languages[2].Name != "Chinese (Simplified)" ||
		o.Languages[2].Has != 0 || o.Languages[2].Total != 1 || o.Counts[SubStatusHas] != 2 {
		t.Errorf("the coverage: %+v, %v", o.Languages, o.Counts)
	}

	plain := newSubtitleRig(t, 18) // the languages en, de, zh
	plain.movie(t, "item-t", 951, "tt0000951", "A Traditional Film")
	plain.cat.titles["item-t"] = traditionalOnly()
	plain.svc.SubtitlePass(ctx)
	if w := plain.want(t, "item-t", "zh"); w.State != store.SubtitleHas || w.HasKind != "traditional" || w.OtherScript != "" {
		t.Errorf("Chinese of no script: %+v", w)
	}
	if o := plain.svc.SubtitleOverview(ctx, ""); o.Languages[2].Has != 1 || o.Languages[2].Total != 1 {
		t.Errorf("the coverage of Chinese: %+v", o.Languages)
	}
	if it, _ := plain.svc.SubtitleList(ctx, SubStatusHas, "zh", "", 10); len(it) != 1 || it[0].Detail != "came with the file · in Traditional characters" {
		t.Errorf("its line: %+v", it)
	}
	if len(plain.src.searches) != 0 {
		t.Errorf("searched though it has every language: %v", plain.src.searches)
	}
}

// With no account and key, or turned off, nothing runs, and setup says what
// is missing without a credential.
func TestSubtitlesWithoutAnAccount(t *testing.T) {
	svc := New(config.Config{SubtitlesEnabled: true, OpenSubtitlesAPIKey: "k"}, nil, nil, nil, nil, nil, nil)
	sec := svc.subtitlesSection(context.Background())
	if sec.State != SetupNeedsSetup || !strings.Contains(sec.Summary, "username, password") {
		t.Errorf("setup: %+v", sec)
	}
	if _, err := svc.FindSubtitles(context.Background(), nil); err != ErrSubtitlesOff {
		t.Errorf("find: %v", err)
	}
	off := New(config.Config{}, nil, nil, nil, nil, nil, nil)
	if sec := off.subtitlesSection(context.Background()); sec.State != SetupReady || !strings.Contains(sec.Summary, "off") {
		t.Errorf("off: %+v", sec)
	}
	if userAgent("0.5.0") != "acquire v0.5.0" || userAgent("v1.2.3") != "acquire v1.2.3" || userAgent("dev") != "acquire dev" {
		t.Error("the user agent")
	}
}

// A title the catalog no longer holds is looked at no more: answered 404 for
// it, or removed by its event.
func TestATitleTheCatalogNoLongerHolds(t *testing.T) {
	r := newSubtitleRig(t, 18)
	ctx := context.Background()
	for _, id := range []string{"gone", "removed"} {
		if err := r.svc.st.PutSubtitleTitle(ctx, store.SubtitleTitle{ItemID: id, Kind: "movie", IMDbID: "tt0000009"},
			[]string{"de"}); err != nil {
			t.Fatal(err)
		}
	}
	r.cat.titles["removed"] = &catalogTitle{attachable: true}
	r.src.results["9"] = nil
	r.svc.SubtitlePass(ctx)
	if _, err := r.svc.st.SubtitleTitleByID(ctx, "gone"); err != store.ErrNotFound {
		t.Errorf("a title the catalog does not hold: %v", err)
	}
	if err := r.svc.OnRemoved(ctx, events.ItemEvent{ItemID: "removed"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.st.SubtitleTitleByID(ctx, "removed"); err != store.ErrNotFound {
		t.Errorf("a title removed: %v", err)
	}
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		q    opensubtitles.Quota
		want bool
	}{
		{opensubtitles.Quota{ResetAt: now.Add(time.Hour)}, true},
		{opensubtitles.Quota{ResetAt: now.Add(-2 * time.Minute)}, false},
		{opensubtitles.Quota{At: now.Add(-time.Hour)}, true},
		{opensubtitles.Quota{At: now.Add(-13 * time.Hour)}, false},
	} {
		if got := quotaOfToday(c.q, now); got != c.want {
			t.Errorf("%+v: %v, want %v", c.q, got, c.want)
		}
	}
}

// The tabs' counts: a match within today's downloads, or a language asked
// for, is on its way; the others are waiting.
func TestTabCounts(t *testing.T) {
	c := map[string]int{store.SubtitleFound: 30, store.SubtitleFound + ":asked": 2, store.SubtitleWanted: 100,
		store.SubtitleWanted + ":asked": 3, store.SubtitleNotFound: 7, store.SubtitleFailed: 1, store.SubtitleHas: 50}
	got := tabCounts(c, 18, true)
	if got[SubStatusWay] != 21 || got[SubStatusWaiting] != 109 || got[SubStatusNotFound] != 7 || got[SubStatusAttention] != 1 ||
		got[SubStatusHas] != 50 {
		t.Errorf("auto: %v", got)
	}
	if got := tabCounts(c, 18, false); got[SubStatusWay] != 5 || got[SubStatusWaiting] != 125 {
		t.Errorf("not auto: %v", got)
	}
	if inWords(90*time.Minute) != "in 2h" || inWords(3*24*time.Hour) != "in 3 days" || inWords(time.Second) != "soon" {
		t.Error("in words")
	}
}
