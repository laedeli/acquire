package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/laedeli/acquire/internal/katalog"
	"github.com/laedeli/acquire/internal/store"
	"github.com/laedeli/acquire/internal/tmdb"
)

// ── a TMDB of the tests' own ───────────────────────────────────────────────

// day is midnight UTC d days from today, as TMDB dates an air date.
func day(d int) *time.Time {
	t := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, d)
	return &t
}

type tmdbEpisode struct {
	title string
	air   *time.Time
}

type tmdbSeries struct {
	name, status string
	tvdb         int64
	firstAir     *time.Time
	// aliases are its titles in the US; countries where it comes from;
	// genres TMDB's genre ids of it, and kind its kind of show.
	aliases   []string
	countries []string
	genres    []int
	kind      string
	seasons   map[int][]tmdbEpisode
}

// fakeTMDB answers /tv/{id}, /tv/{id}/season/{n} and /movie/{id} from what
// a test sets; down answers 503.
type fakeTMDB struct {
	mu     sync.Mutex
	series map[int64]*tmdbSeries
	movies map[int64]map[string]any
	down   bool
	calls  []string
}

func newFakeTMDB() *fakeTMDB {
	return &fakeTMDB{series: map[int64]*tmdbSeries{}, movies: map[int64]map[string]any{}}
}

func (f *fakeTMDB) set(fn func(f *fakeTMDB)) {
	f.mu.Lock()
	fn(f)
	f.mu.Unlock()
}

func (f *fakeTMDB) called() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func dateOf(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format("2006-01-02")
}

func (f *fakeTMDB) client(t *testing.T) *tmdb.Client {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls = append(f.calls, r.URL.Path)
		if f.down {
			http.Error(w, `{"status_message":"busy"}`, http.StatusServiceUnavailable)
			return
		}
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		id, _ := strconv.ParseInt(parts[min(1, len(parts)-1)], 10, 64)
		switch {
		case len(parts) == 2 && parts[0] == "tv" && f.series[id] != nil:
			s := f.series[id]
			var seasons []map[string]any
			for n, eps := range s.seasons {
				var air *time.Time
				if len(eps) > 0 {
					air = eps[0].air
				}
				seasons = append(seasons, map[string]any{"season_number": n, "episode_count": len(eps), "air_date": dateOf(air)})
			}
			var alt []map[string]any
			for _, a := range s.aliases {
				alt = append(alt, map[string]any{"iso_3166_1": "US", "title": a})
			}
			var genres []map[string]any
			for _, g := range s.genres {
				genres = append(genres, map[string]any{"id": g, "name": "genre"})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": id, "name": s.name, "status": s.status, "first_air_date": dateOf(s.firstAir), "origin_country": s.countries,
				"genres": genres, "type": s.kind,
				"external_ids":       map[string]any{"tvdb_id": s.tvdb},
				"alternative_titles": map[string]any{"results": alt},
				"seasons":            seasons,
			})
		case len(parts) == 4 && parts[0] == "tv" && parts[2] == "season" && f.series[id] != nil:
			n, _ := strconv.Atoi(parts[3])
			var eps []map[string]any
			for i, e := range f.series[id].seasons[n] {
				eps = append(eps, map[string]any{"season_number": n, "episode_number": i + 1, "name": e.title, "air_date": dateOf(e.air)})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"episodes": eps})
		case len(parts) == 2 && parts[0] == "movie" && f.movies[id] != nil:
			_ = json.NewEncoder(w).Encode(f.movies[id])
		default:
			http.Error(w, `{"status_code":34}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	c := tmdb.New("eyJhbGciOiJIUzI1NiJ9.fake.token", "en-US")
	c.HTTP, c.BaseURL = srv.Client(), srv.URL
	return c
}

// sevenHarbours is a series shaped as one that premiered sixteen days ago
// and airs in weekly batches — three, three, three, then four next week —,
// returning. The name is invented.
func sevenHarbours() *tmdbSeries {
	s := &tmdbSeries{name: "Example Saga: Seven Harbours", status: "Returning Series", tvdb: 9904242, firstAir: day(-16), countries: []string{"US"},
		aliases: []string{"Seven Harbours", "Example Saga: Seven Harbours"}, seasons: map[int][]tmdbEpisode{0: {{title: "Special", air: day(-30)}}}}
	for i, d := range []int{-16, -16, -16, -9, -9, -9, -2, -2, -2, 5, 5, 5, 5} {
		s.seasons[1] = append(s.seasons[1], tmdbEpisode{title: fmt.Sprintf("Harbour %d", i+1), air: day(d)})
	}
	return s
}

// followRig is a service with a client, a catalog that takes ingest by
// identity, katalog-api's holdings (the library holds nothing) and a TMDB of
// its own; no search source until a test adds one.
type followRig struct {
	*targetRig
	tmdb *fakeTMDB
}

func newFollowRig(t *testing.T) *followRig {
	t.Helper()
	r := &targetRig{gw: &fakeGateway{seq: true}, cat: &identityCatalog{}, api: newHoldingsAPI()}
	r.svc = e2eService(t, r.gw, testBox(t))
	r.svc.kc = katalog.New(r.cat.server(t).URL, r.api.server(t).URL, nil)
	r.downloads = t.TempDir()
	if _, err := r.svc.CreateClient(context.Background(), ClientInput{Type: "nzbget", BaseURL: "http://worker:6789", Auth: "none",
		RemotePath: "/downloads", LocalPath: r.downloads}, "admin"); err != nil {
		t.Fatal(err)
	}
	f := newFakeTMDB()
	r.svc.tm = f.client(t)
	return &followRig{targetRig: r, tmdb: f}
}

// sqlExec runs a statement in the app tests' schema.
func sqlExec(t *testing.T, q string, args ...any) {
	t.Helper()
	dsn := os.Getenv("TEST_DSN")
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	pool, err := pgxpool.New(context.Background(), dsn+sep+"search_path=acquire_app_test")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(context.Background(), q, args...); err != nil {
		t.Fatal(err)
	}
}

// settle reads TMDB for what was asked, and asks the catalog what the
// library holds, as the workers would.
func (r *followRig) settle(t *testing.T, scheduled bool) InventoryRun {
	t.Helper()
	ctx := context.Background()
	run, err := r.svc.RefreshInventory(ctx, scheduled)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	r.svc.holdingsMu.Lock()
	var ids []string
	for id := range r.svc.holdingsIDs {
		ids = append(ids, id)
	}
	r.svc.holdingsIDs = map[string]bool{}
	r.svc.holdingsMu.Unlock()
	if len(ids) > 0 {
		if _, err := r.svc.ReconcileHoldings(ctx, ids...); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
	}
	return run
}

func (r *followRig) view(t *testing.T, id string) RequestView {
	t.Helper()
	all, err := r.svc.Requests(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range all {
		if v.ID == id {
			return v
		}
	}
	t.Fatalf("no request %s", id)
	return RequestView{}
}

// ── following a series ─────────────────────────────────────────────────────

// A request for a series follows it: the title with its TVDB id and aliases
// from TMDB, one monitored target per episode, the request open and saying
// how far acquire is — never failed. Asking again is the same request.
func TestARequestFollowsTheSeries(t *testing.T) {
	r := newFollowRig(t)
	ctx := context.Background()
	r.tmdb.set(func(f *fakeTMDB) { f.series[990001] = sevenHarbours() })

	w, created, err := r.svc.Request(ctx, store.Wanted{TMDBID: 990001, MediaType: "tv", Title: "Example Saga: Seven Harbours", Year: 2026,
		Status: "fulfilled", TitleID: "series:1"}, asAdmin("user-1"))
	if err != nil || !created {
		t.Fatal(err)
	}
	if w.TitleID != "series:990001" || w.MediaType != "series" || w.Status != "following" || w.RequestedBy != "user-1" {
		t.Fatalf("the request: %+v", w)
	}
	if v := r.view(t, w.ID); v.Status != "following" || v.Detail != "reading its episodes from TMDB" {
		t.Errorf("before TMDB was read: %s %q", v.Status, v.Detail)
	}

	run := r.settle(t, false)
	if run.Followed != 1 || run.Added != 13 || run.AddedAired != 9 || run.Calls != 2 {
		t.Errorf("the follow's read: %+v", run)
	}
	title, err := r.svc.st.TitleByID(ctx, "series:990001")
	if err != nil || title.TVDBID != 9904242 || !title.Followed() || title.Origin != store.OriginRequest || title.TMDBStatus != "Returning Series" ||
		title.Year != 2026 || !title.HoldingsKnown() || strings.Join(title.Countries, ",") != "US" {
		t.Fatalf("the title: %+v %v", title, err)
	}
	aliases, _ := r.svc.st.AliasesFor(ctx, title.ID)
	if len(aliases) != 1 || aliases[0] != "Seven Harbours" {
		t.Errorf("aliases = %v", aliases)
	}
	targets, _ := r.svc.st.TitleTargets(ctx, title.ID)
	if len(targets) != 13 {
		t.Fatalf("targets = %d, want 13 (no special)", len(targets))
	}
	for _, x := range targets {
		if !x.Monitored || x.State != store.TargetWanted || x.AddedAt == nil || x.EpisodeTitle == "" {
			t.Errorf("a target: %+v", x)
		}
	}
	v := r.view(t, w.ID)
	want := fmt.Sprintf("13 episodes: 0 in the library, 9 missing, 4 not aired yet; next S01E10 on %s", dateOf(day(5)))
	if v.Status != "following" || v.Detail != want || v.Progress == nil || v.Progress.Episodes != 13 || v.AutoGrab == nil || *v.AutoGrab {
		t.Errorf("after the read: %s %q %+v, want %q", v.Status, v.Detail, v.Progress, want)
	}
	if miss, _ := r.svc.st.Missing(ctx, 50); len(miss) != 9 {
		t.Errorf("missing = %d, want the 9 aired", len(miss))
	}

	again, _, err := r.svc.Request(ctx, store.Wanted{TMDBID: 990001, MediaType: "series", Title: "Seven Harbours"}, asAdmin("user-2"))
	if err != nil || again.ID != w.ID {
		t.Errorf("asked again: %+v %v", again, err)
	}
	if all, _ := r.svc.st.ListWanted(ctx); len(all) != 1 {
		t.Errorf("%d requests for one series", len(all))
	}
	// A series request is grabbed episode by episode, never as a whole.
	if err := r.svc.AutoGrab(ctx, w.ID); !errors.Is(err, errFollowsSeries) {
		t.Errorf("find & grab of a series request: %v", err)
	}
}

// A request from before follows existed — a series request that failed, as
// they did — follows its series once it is asked for again.
func TestARequestFromBeforeFollowsOnceAskedAgain(t *testing.T) {
	r := newFollowRig(t)
	ctx := context.Background()
	r.tmdb.set(func(f *fakeTMDB) { f.series[990001] = sevenHarbours() })
	if err := r.svc.st.CreateWanted(ctx, store.Wanted{ID: "w_old", TMDBID: 990001, MediaType: "series", Title: "Example Saga"}); err != nil {
		t.Fatal(err)
	}
	_ = r.svc.st.SetStatus(ctx, "w_old", "failed", "a series request cannot be handed over")
	w, _, err := r.svc.Request(ctx, store.Wanted{TMDBID: 990001, MediaType: "series", Title: "Example Saga"}, asAdmin("user"))
	if err != nil || w.ID != "w_old" || w.TitleID != "series:990001" || w.Status != "following" {
		t.Fatalf("asked again: %+v %v", w, err)
	}
}

// A title acquire tracks already is not made twice: the request follows it,
// it is monitored again, and its episodes from before stay the old backlog.
func TestARequestForATrackedSeriesFollowsIt(t *testing.T) {
	r := newFollowRig(t)
	ctx := context.Background()
	title, eps := track(t, r.svc, 990711, "Show Name", 2, true)
	sqlExec(t, `UPDATE titles SET monitored = false WHERE id = $1`, title.ID)
	r.tmdb.set(func(f *fakeTMDB) {
		f.series[990711] = &tmdbSeries{name: "Show Name", status: "Returning Series", tvdb: 9903627,
			seasons: map[int][]tmdbEpisode{1: {{title: "One", air: day(-2)}, {title: "Two", air: day(-2)}, {title: "Three", air: day(-1)}}}}
	})
	w, _, err := r.svc.Request(ctx, store.Wanted{TMDBID: 990711, MediaType: "series", Title: "Show Name"}, asAdmin("user"))
	if err != nil || w.TitleID != title.ID {
		t.Fatalf("request: %+v %v", w, err)
	}
	r.settle(t, false)
	got, _ := r.svc.st.TitleByID(ctx, title.ID)
	if !got.Monitored || !got.Followed() || got.Origin != "" || got.TVDBID != 9903627 {
		t.Errorf("the tracked title: %+v", got)
	}
	targets, _ := r.svc.st.TitleTargets(ctx, title.ID)
	if len(targets) != 3 {
		t.Fatalf("targets = %d", len(targets))
	}
	if targets[0].ID != eps[0].ID || targets[0].AddedAt != nil || targets[2].AddedAt == nil {
		t.Errorf("the episodes from before stay as they were, the new one is added: %+v / %+v", targets[0], targets[2])
	}
	// Its last request removed, the title the import tracks is as an admin
	// left it: not monitored.
	if err := r.svc.RemoveRequest(ctx, w.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.svc.st.TitleByID(ctx, title.ID); got.Monitored || !got.MonitorNew || got.Followed() {
		t.Errorf("after its last request: %+v", got)
	}
}

// A series requested, its request removed and requested again is followed
// from its first request on: what that follow added is what was requested,
// and is grabbed on its own as the new things are.
func TestARequestAgainKeepsTheFirstFollow(t *testing.T) {
	r := newFollowRig(t)
	ctx := context.Background()
	r.tmdb.set(func(f *fakeTMDB) { f.series[990001] = sevenHarbours() })
	w, _, _ := r.svc.Request(ctx, store.Wanted{TMDBID: 990001, MediaType: "series", Title: "Example Saga"}, asAdmin("user"))
	r.settle(t, false)
	if err := r.svc.RemoveRequest(ctx, w.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.svc.st.TitleByID(ctx, "series:990001"); got.Monitored || got.Followed() {
		t.Fatalf("after its request was removed: %+v", got)
	}
	if _, _, err := r.svc.Request(ctx, store.Wanted{TMDBID: 990001, MediaType: "series", Title: "Example Saga"}, asAdmin("user")); err != nil {
		t.Fatal(err)
	}
	r.settle(t, false)
	title, _ := r.svc.st.TitleByID(ctx, "series:990001")
	targets, _ := r.svc.st.TitleTargets(ctx, title.ID)
	pol := SearchSettings{AutoGrabNew: true, NewWithinDays: 1}
	n := 0
	for _, x := range targets {
		if x.AirDate != nil && x.AirDate.Before(*day(-5)) && pol.Grabs(title, x, time.Now()) {
			n++
		}
	}
	if !title.Followed() || !title.Monitored || n != 6 {
		t.Errorf("requested again: %+v; %d of the six that aired over five days ago grabbed on their own", title, n)
	}
}

// A title a request made is not hunted once its last request is removed.
func TestRemovingTheLastRequestEndsTheFollow(t *testing.T) {
	r := newFollowRig(t)
	ctx := context.Background()
	r.tmdb.set(func(f *fakeTMDB) { f.series[990001] = sevenHarbours() })
	w, _, _ := r.svc.Request(ctx, store.Wanted{TMDBID: 990001, MediaType: "series", Title: "Example Saga"}, asAdmin("user"))
	r.settle(t, false)
	if err := r.svc.RemoveRequest(ctx, w.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.svc.st.TitleByID(ctx, "series:990001"); got.Monitored || got.Followed() {
		t.Errorf("the title after its last request: %+v", got)
	}
	if miss, _ := r.svc.st.Missing(ctx, 50); len(miss) != 0 {
		t.Errorf("a title nobody follows is missing %d", len(miss))
	}
	if err := r.svc.RemoveRequest(ctx, w.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("removed twice: %v", err)
	}
}

// A request from someone who is not an admin awaits an admin's approval, and
// changes nothing meanwhile: its series, which acquire did not track, is
// made, followed and monitored by nobody; TMDB is read for what it says of
// it, and its episodes are made once it is approved — nothing of it searched
// or grabbed before. A declined request stays, saying so. An admin's own
// request is approved at once, as everyone's is with requests.autoApprove —
// approved by the setting, not by whoever asked.
func TestARequestAwaitsAnAdminsApproval(t *testing.T) {
	r := newFollowRig(t)
	ctx := context.Background()
	r.tmdb.set(func(f *fakeTMDB) {
		f.series[990001] = sevenHarbours()
		f.movies[990100] = map[string]any{"id": 990100, "title": "Example Clay Film", "status": "Released", "release_date": dateOf(day(-60)),
			"release_dates": map[string]any{"results": []map[string]any{{"iso_3166_1": "US",
				"release_dates": []map[string]any{{"type": 4, "release_date": day(-2).Format(time.RFC3339)}}}}}}
	})
	user := Requester{Sub: "user"}
	w, created, err := r.svc.Request(ctx, store.Wanted{TMDBID: 990001, MediaType: "series", Title: "Example Saga: Seven Harbours"}, user)
	if err != nil || !created || w.Approval != store.ApprovalAwaiting || w.TitleID != "series:990001" {
		t.Fatalf("a user's request: %+v %v", w, err)
	}
	r.settle(t, false)
	title, _ := r.svc.st.TitleByID(ctx, "series:990001")
	if targets, _ := r.svc.st.TitleTargets(ctx, title.ID); len(targets) != 0 || title.Monitored || title.Followed() || title.InventoryAt == nil {
		t.Errorf("awaiting approval: %d episodes made, %+v", len(targets), title)
	}
	v := r.view(t, w.ID)
	if v.Status != "awaiting" || v.AutoGrab == nil || *v.AutoGrab ||
		v.Detail != "waiting for an admin's approval: until then nothing of it is searched or grabbed · TMDB lists 1 season of it; its episodes are tracked once it is approved" {
		t.Errorf("awaiting approval: %s %q %v", v.Status, v.Detail, v.AutoGrab)
	}
	feedSrc := newFeedSource(t, true, tv("Example.Saga.Seven.Harbours.S01E07.1080p.WEB.h264-GRP", ago(time.Hour)))
	addFeed(t, r.svc, "feed", feedSrc)
	if _, err := r.svc.SaveSearchSettings(ctx, SearchSettings{PreferProtocol: "usenet", MaxConcurrentGrabs: 10, AutoGrab: true}, 0, "test"); err != nil {
		t.Fatal(err)
	}
	if run, _ := r.svc.SyncRSS(ctx); run.Feeds != 0 || len(r.gw.added()) != 0 {
		t.Errorf("the feeds, awaiting approval: %+v", run)
	}
	if due, _ := r.svc.st.DueTargetsBy(ctx, 50, r.svc.grabPolicy(ctx).dueOrder()); len(due) != 0 {
		t.Errorf("due awaiting approval: %v", ids(due))
	}

	// Asked again by the user: the same request, still awaiting.
	if again, created, _ := r.svc.Request(ctx, store.Wanted{TMDBID: 990001, MediaType: "series", Title: "x"}, user); again.ID != w.ID || created ||
		again.Approval != store.ApprovalAwaiting {
		t.Errorf("asked again by the user: %+v", again)
	}
	// Approved: followed, its episodes made, and grabbed on its own as the
	// policy allows.
	if ok, err := r.svc.ApproveRequest(ctx, w.ID, "admin"); err != nil || ok.Approval != store.ApprovalApproved || ok.DecidedBy != "admin" || ok.DecidedAt == nil {
		t.Fatalf("approved: %+v %v", ok, err)
	}
	if v := r.view(t, w.ID); v.Status != "following" || v.Detail != "reading its episodes from TMDB" {
		t.Errorf("approved, before its episodes are read: %s %q", v.Status, v.Detail)
	}
	r.settle(t, false)
	if targets, _ := r.svc.st.TitleTargets(ctx, title.ID); len(targets) != 13 {
		t.Errorf("its episodes once approved: %d", len(targets))
	}
	if v := r.view(t, w.ID); v.Status != "following" || !*v.AutoGrab {
		t.Errorf("approved: %s %q", v.Status, v.Detail)
	}
	if run, _ := r.svc.SyncRSS(ctx); run.Grabbed != 1 {
		t.Errorf("the feeds once approved: %+v", run)
	}

	// Declined: it stays, saying so, and its movie is not followed.
	m, _, _ := r.svc.Request(ctx, store.Wanted{TMDBID: 990100, MediaType: "movie", Title: "Example Clay Film", Year: 2026}, user)
	r.settle(t, false)
	if v := r.view(t, m.ID); v.Status != "awaiting" ||
		v.Detail != "waiting for an admin's approval: until then nothing of it is searched or grabbed · out digitally since "+dateOf(day(-2)) {
		t.Errorf("a movie awaiting approval: %s %q", v.Status, v.Detail)
	}
	if tg, _ := r.svc.st.TargetAt(ctx, "movie:990100", nil, nil); tg.Monitored {
		t.Errorf("the movie's target awaiting approval: %+v", tg)
	}
	if _, err := r.svc.DeclineRequest(ctx, m.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	if v := r.view(t, m.ID); v.Status != "declined" || v.Detail != "an admin declined it: acquire does not follow it for this request" {
		t.Errorf("declined: %s %q", v.Status, v.Detail)
	}
	if title, _ := r.svc.st.TitleByID(ctx, "movie:990100"); title.Followed() || title.Monitored {
		t.Errorf("the declined request's movie: %+v", title)
	}
	// A user asking again gets the declined request.
	if again, created, _ := r.svc.Request(ctx, store.Wanted{TMDBID: 990100, MediaType: "movie", Title: "x"}, Requester{Sub: "user-2"}); created ||
		again.ID != m.ID || again.Approval != store.ApprovalDeclined {
		t.Errorf("a user asking again: %+v", again)
	}
	// An admin asking for it approves it, and it is followed — its target
	// monitored.
	if again, created, err := r.svc.Request(ctx, store.Wanted{TMDBID: 990100, MediaType: "movie", Title: "Example Clay Film"}, asAdmin("admin")); err != nil ||
		created || again.ID != m.ID || again.Approval != store.ApprovalApproved {
		t.Errorf("an admin asking for it: %+v %v", again, err)
	}
	if title, _ := r.svc.st.TitleByID(ctx, "movie:990100"); !title.Followed() || !title.Monitored || !title.Approved {
		t.Errorf("approved by an admin's asking: %+v", title)
	}
	if tg, _ := r.svc.st.TargetAt(ctx, "movie:990100", nil, nil); !tg.Monitored {
		t.Errorf("the approved movie's target: %+v", tg)
	}
	if _, err := r.svc.ApproveRequest(ctx, "w_none", "admin"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("approving no request: %v", err)
	}

	// requests.autoApprove: a user's request is approved at once — by the
	// setting.
	if _, err := r.svc.PutRequestSettings(ctx, RequestSettings{AutoApprove: true}, 0, "admin"); err != nil {
		t.Fatal(err)
	}
	if got := r.svc.RequestSettings(ctx); !got.AutoApprove || got.Revision == 0 {
		t.Errorf("the request settings: %+v", got)
	}
	r.tmdb.set(func(f *fakeTMDB) {
		f.series[990002] = &tmdbSeries{name: "Another Saga", status: "Ended", seasons: map[int][]tmdbEpisode{}}
	})
	if auto, _, _ := r.svc.Request(ctx, store.Wanted{TMDBID: 990002, MediaType: "series", Title: "Another Saga"}, user); auto.Approval != store.ApprovalApproved ||
		auto.DecidedBy != "autoApprove" || auto.RequestedBy != "user" {
		t.Errorf("with autoApprove: %+v", auto)
	}
	// A request that follows nothing has nothing to approve.
	plain, _, _ := r.svc.Request(ctx, store.Wanted{Title: "Some Film", MediaType: "movie"}, user)
	if _, err := r.svc.ApproveRequest(ctx, plain.ID, "admin"); !IsNoFollow(err) || plain.Approval != "" {
		t.Errorf("approving a request that follows nothing: %v %+v", err, plain)
	}
}

// A request awaiting approval for a title acquire tracks changes nothing of
// it: an admin who stopped monitoring it, or its new seasons, finds it so —
// a season TMDB announces meanwhile is not monitored, nor grabbed — and the
// request read nothing of its episodes. Declined, nothing was to undo.
func TestARequestAwaitingApprovalChangesNothingOfATrackedTitle(t *testing.T) {
	r := newFollowRig(t)
	ctx := context.Background()
	monitored, _ := track(t, r.svc, 990711, "Show Name", 2, true)
	sqlExec(t, `UPDATE titles SET monitor_new = false WHERE id = $1`, monitored.ID)
	sqlExec(t, `INSERT INTO seasons (title_id, season_number, episode_count, monitored) VALUES ($1, 1, 2, true)
	            ON CONFLICT (title_id, season_number) DO UPDATE SET monitored = true`, monitored.ID)
	unmonitored, _ := track(t, r.svc, 990712, "Other Show", 1, true)
	sqlExec(t, `UPDATE titles SET monitored = false, monitor_new = false WHERE id = $1`, unmonitored.ID)
	r.tmdb.set(func(f *fakeTMDB) {
		f.series[990711] = &tmdbSeries{name: "Show Name", status: "Returning Series", tvdb: 1,
			seasons: map[int][]tmdbEpisode{1: {{title: "One", air: day(-300)}, {title: "Two", air: day(-299)}}}}
		f.series[990712] = &tmdbSeries{name: "Other Show", status: "Returning Series", tvdb: 2,
			seasons: map[int][]tmdbEpisode{1: {{title: "One", air: day(-300)}}, 2: {{title: "New", air: day(-3)}}}}
	})
	r.svc.RequestInventory(monitored.ID)
	r.settle(t, false)
	user := Requester{Sub: "user"}
	a, _, _ := r.svc.Request(ctx, store.Wanted{TMDBID: 990711, MediaType: "series", Title: "Show Name"}, user)
	b, _, _ := r.svc.Request(ctx, store.Wanted{TMDBID: 990712, MediaType: "series", Title: "Other Show"}, user)
	r.settle(t, false)
	if ti, _ := r.svc.st.TitleByID(ctx, monitored.ID); ti.Followed() || !ti.Monitored || ti.MonitorNew {
		t.Errorf("the monitored series, awaiting: %+v", ti)
	}
	if ti, _ := r.svc.st.TitleByID(ctx, unmonitored.ID); ti.Followed() || ti.Monitored || ti.MonitorNew {
		t.Errorf("the unmonitored series, awaiting: %+v", ti)
	}
	if targets, _ := r.svc.st.TitleTargets(ctx, unmonitored.ID); len(targets) != 1 {
		t.Errorf("the unmonitored series' episodes, awaiting: %d, want its one, nothing read", len(targets))
	}
	if v := r.view(t, a.ID); !strings.HasPrefix(v.Detail, "waiting for an admin's approval; until then acquire goes on with it as before the request · 2 episodes") {
		t.Errorf("a tracked title awaiting: %q", v.Detail)
	}

	// TMDB announces a second season of the monitored one, aired three
	// days ago: not monitored, nor grabbed.
	r.tmdb.set(func(f *fakeTMDB) { f.series[990711].seasons[2] = []tmdbEpisode{{title: "New", air: day(-3)}} })
	sqlExec(t, `UPDATE titles SET inventory_at = now() - interval '6 hours' WHERE id = $1`, monitored.ID)
	r.settle(t, true)
	s2 := target(t, r.svc, "series:990711:s02e001")
	ti, _ := r.svc.st.TitleByID(ctx, monitored.ID)
	pol := SearchSettings{AutoGrabNew: true, AirDelayHours: 30}
	if s2.Monitored || pol.Grabs(ti, s2, time.Now()) {
		t.Errorf("a new season the admin does not monitor, awaiting: %+v", s2)
	}
	for _, w := range []store.Wanted{a, b} {
		if _, err := r.svc.DeclineRequest(ctx, w.ID, "admin"); err != nil {
			t.Fatal(err)
		}
	}
	if ti, _ := r.svc.st.TitleByID(ctx, monitored.ID); !ti.Monitored || ti.MonitorNew {
		t.Errorf("declined: %+v", ti)
	}
	if ti, _ := r.svc.st.TitleByID(ctx, unmonitored.ID); ti.Monitored || ti.MonitorNew {
		t.Errorf("declined: %+v", ti)
	}
}

// An admin grabbing a movie request awaiting approval — a find & grab, a
// release picked for it — approves it. A series request is grabbed episode
// by episode, never as a whole: find & grab refuses it and approves nothing.
// Nothing of a title held for approval is grabbed from the missing tab.
func TestGrabbingARequestApprovesIt(t *testing.T) {
	r := newFollowRig(t)
	ctx := context.Background()
	r.tmdb.set(func(f *fakeTMDB) {
		f.series[990001] = sevenHarbours()
		f.movies[990100] = map[string]any{"id": 990100, "title": "Example Clay Film", "status": "Released", "release_date": dateOf(day(-60)),
			"release_dates": map[string]any{"results": []map[string]any{{"iso_3166_1": "US",
				"release_dates": []map[string]any{{"type": 4, "release_date": day(-5).Format(time.RFC3339)}}}}}}
	})
	user := Requester{Sub: "user"}
	series, _, _ := r.svc.Request(ctx, store.Wanted{TMDBID: 990001, MediaType: "series", Title: "Example Saga"}, user)
	movie, _, _ := r.svc.Request(ctx, store.Wanted{TMDBID: 990100, MediaType: "movie", Title: "Example Clay Film", Year: 2026}, user)
	r.settle(t, false)
	release := "Example.Clay.Film.2026.1080p.WEB-DL.x264-GRP"
	src := newFakeSource(t, "usenet", "K", release)
	addSource(t, r.svc, "example", src, nil)

	// The missing tab takes nothing of either.
	if q, _ := r.svc.RequestTargetGrabs(ctx, []string{"movie:990100:movie"}); len(q) != 0 {
		t.Errorf("grab all, awaiting: %v", q)
	}
	if _, err := r.svc.GrabTarget(ctx, "movie:990100:movie", "missing"); !errors.Is(err, errAwaitsApproval) {
		t.Errorf("grab now, awaiting: %v", err)
	}

	// Find & grab of the series: refused, and nothing approved.
	if err := r.svc.ApproveForGrab(ctx, series.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.AutoGrab(ctx, series.ID); !errors.Is(err, errFollowsSeries) {
		t.Errorf("find & grab of a series request: %v", err)
	}
	if got, _ := r.svc.st.GetWanted(ctx, series.ID); got.Approval != store.ApprovalAwaiting {
		t.Errorf("the series request: %+v", got)
	}
	// A release picked for the movie: approved, and grabbed.
	if err := r.svc.ApproveForGrab(ctx, movie.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	res, err := r.svc.Releases(ctx, movie.ID)
	if err != nil || len(res.Candidates) != 1 {
		t.Fatalf("the releases: %+v %v", res, err)
	}
	if err := r.svc.GrabCandidate(ctx, movie.ID, res.Candidates[0]); err != nil {
		t.Errorf("the pick: %v", err)
	}
	if got, _ := r.svc.st.GetWanted(ctx, movie.ID); got.Approval != store.ApprovalApproved || got.DecidedBy != "admin" {
		t.Errorf("the movie request: %+v", got)
	}
	if len(r.gw.added()) != 1 {
		t.Errorf("handed over %d", len(r.gw.added()))
	}
}

// ── reading TMDB again ─────────────────────────────────────────────────────

// The refresh reads a series that runs again on every run: what TMDB
// announced since becomes targets — a new season too —, dates move, an
// episode TMDB dropped goes unless something holds it. The catalog is asked
// what the library holds of what was added.
func TestTheRefreshAddsWhatTMDBAnnounces(t *testing.T) {
	r := newFollowRig(t)
	ctx := context.Background()
	r.tmdb.set(func(f *fakeTMDB) { f.series[990001] = sevenHarbours() })
	w, _, _ := r.svc.Request(ctx, store.Wanted{TMDBID: 990001, MediaType: "series", Title: "Example Saga"}, asAdmin("user"))
	r.settle(t, false)
	if run := r.settle(t, true); run.Titles != 0 {
		t.Errorf("read again right away: %+v", run)
	}

	// A week on: TMDB moves E10 a day later, drops E13 and announces a
	// second season of two.
	r.tmdb.set(func(f *fakeTMDB) {
		s := f.series[990001]
		s.seasons[1][9].air = day(6)
		s.seasons[1] = s.seasons[1][:12]
		s.seasons[2] = []tmdbEpisode{{title: "Return", air: day(40)}, {title: "", air: nil}}
	})
	sqlExec(t, `UPDATE titles SET inventory_at = now() - interval '6 hours' WHERE id = 'series:990001'`)
	before := len(r.tmdb.called())
	run := r.settle(t, true)
	if run.Titles != 1 || run.Added != 2 || run.Gone != 1 || run.Removed != 0 || run.Updated != 1 {
		t.Errorf("the refresh: %+v", run)
	}
	// The answer, the last season and the season that changed: three calls.
	if calls := r.tmdb.called()[before:]; len(calls) != 3 {
		t.Errorf("calls = %v", calls)
	}
	// E13 goes once a read a day or more later agrees.
	sqlExec(t, `UPDATE titles SET inventory_at = now() - interval '6 hours' WHERE id = 'series:990001'`)
	sqlExec(t, `UPDATE acquisition_targets SET tmdb_gone_at = now() - interval '25 hours' WHERE tmdb_gone_at IS NOT NULL`)
	if run := r.settle(t, true); run.Removed != 1 {
		t.Errorf("a day later: %+v", run)
	}
	targets, _ := r.svc.st.TitleTargets(ctx, "series:990001")
	if len(targets) != 14 {
		t.Fatalf("targets = %d, want 12 of season 1 and 2 of season 2", len(targets))
	}
	ten, _ := r.svc.st.GetTarget(ctx, "series:990001:s01e010")
	if !ten.AirDate.Equal(*day(6)) {
		t.Errorf("E10's air date = %v", ten.AirDate)
	}
	two, _ := r.svc.st.GetTarget(ctx, "series:990001:s02e001")
	if !two.Monitored || two.AddedAt == nil {
		t.Errorf("the new season of a followed series: %+v", two)
	}
	if v := r.view(t, w.ID); !strings.Contains(v.Detail, "14 episodes") {
		t.Errorf("the request after the refresh: %q", v.Detail)
	}
}

// A run is paced: it starts no title once its budget of calls is spent, and
// what is still due is read on the next runs — the follows first, then what
// runs, then the rest.
func TestTheRefreshIsPaced(t *testing.T) {
	r := newFollowRig(t)
	ctx := context.Background()
	for id := int64(1); id <= 5; id++ {
		if err := r.svc.st.UpsertTitle(ctx, store.Title{TMDBID: id, Kind: "series", Title: fmt.Sprintf("Show %d", id), Monitored: true, Status: "ended"}); err != nil {
			t.Fatal(err)
		}
		r.tmdb.set(func(f *fakeTMDB) {
			f.series[id] = &tmdbSeries{name: fmt.Sprintf("Show %d", id), status: "Ended",
				seasons: map[int][]tmdbEpisode{1: {{title: "One", air: day(-900)}}, 2: {{title: "Two", air: day(-800)}}}}
		})
	}
	r.svc.inventoryBudget = 5
	first := r.settle(t, true)
	if first.Titles != 2 || first.Calls != 6 || !first.Left {
		t.Errorf("the first run: %+v", first)
	}
	second := r.settle(t, true)
	third := r.settle(t, true)
	if second.Titles != 2 || third.Titles != 1 || third.Left {
		t.Errorf("the next runs: %+v / %+v", second, third)
	}
	if run := r.settle(t, true); run.Titles != 0 {
		t.Errorf("an ended series read again within the week: %+v", run)
	}
}

// A run keeps a third of its budget for the series that do not run — ended,
// or never read —: a library of running series does not keep them waiting
// for ever.
func TestTheRefreshKeepsBudgetForTheSeriesThatEnded(t *testing.T) {
	r := newFollowRig(t)
	ctx := context.Background()
	for id := int64(1); id <= 6; id++ {
		status, tmdbStatus := "continuing", "Returning Series"
		if id > 4 {
			status, tmdbStatus = "ended", "Ended"
		}
		if err := r.svc.st.UpsertTitle(ctx, store.Title{TMDBID: 990000 + id, Kind: "series", Title: fmt.Sprintf("Show %d", id), Monitored: true, Status: status}); err != nil {
			t.Fatal(err)
		}
		r.tmdb.set(func(f *fakeTMDB) {
			f.series[990000+id] = &tmdbSeries{name: fmt.Sprintf("Show %d", id), status: tmdbStatus,
				seasons: map[int][]tmdbEpisode{1: {{title: "One", air: day(-900)}}}}
		})
	}
	// Four running ones, read before, are more than a run's budget; the ended
	// ones were never read.
	for id := 1; id <= 4; id++ {
		sqlExec(t, `UPDATE titles SET inventory_at = now() - interval '6 hours', tmdb_status = 'Returning Series' WHERE id = $1`,
			fmt.Sprintf("series:99000%d", id))
	}
	r.svc.inventoryBudget = 6
	run := r.settle(t, true)
	ended := 0
	for _, id := range []string{"series:990005", "series:990006"} {
		if ti, _ := r.svc.st.TitleByID(ctx, id); ti.InventoryAt != nil {
			ended++
		}
	}
	if run.Titles != 3 || ended != 1 || !run.Left {
		t.Errorf("a run of six calls: %+v, %d of the ended series read; want two running and one ended", run, ended)
	}
}

// TMDB not answering ends the run and fails nothing: the follow says why,
// and is read on the next run. Without a TMDB key nothing can be read, and
// the request says so.
func TestTMDBNotAnswering(t *testing.T) {
	r := newFollowRig(t)
	ctx := context.Background()
	r.tmdb.set(func(f *fakeTMDB) {
		f.series[990001] = sevenHarbours()
		f.down = true
	})
	w, _, _ := r.svc.Request(ctx, store.Wanted{TMDBID: 990001, MediaType: "series", Title: "Example Saga"}, asAdmin("user"))
	run := r.settle(t, false)
	if run.Error == "" || run.Titles != 0 {
		t.Errorf("a run against TMDB down: %+v", run)
	}
	if v := r.view(t, w.ID); v.Status != "following" || !strings.Contains(v.Detail, "could not be read from TMDB yet: TMDB did not answer") {
		t.Errorf("the request: %s %q", v.Status, v.Detail)
	}
	// Tried again a quarter of an hour on, not on the next refresh hours
	// away.
	r.tmdb.set(func(f *fakeTMDB) { f.down = false })
	if !r.svc.retryFollows(ctx) {
		t.Fatal("the follow TMDB could not be read for is not tried again")
	}
	if run := r.settle(t, false); run.Titles != 1 || run.Added != 13 {
		t.Errorf("tried again: %+v", run)
	}
	if r.svc.retryFollows(ctx) {
		t.Error("a follow read is tried again")
	}

	// A title TMDB does not know is the title's fault: recorded, and the
	// run goes on.
	if _, _, err := r.svc.Request(ctx, store.Wanted{TMDBID: 404, MediaType: "series", Title: "Nobody Knows"}, asAdmin("user")); err != nil {
		t.Fatal(err)
	}
	if run := r.settle(t, false); run.Failed != 1 || run.Error != "" {
		t.Errorf("an unknown title: %+v", run)
	}
	unknown, _ := r.svc.st.TitleByID(ctx, "series:404")
	if !strings.Contains(unknown.InventoryError, "TMDB does not know it") || unknown.InventoryAt == nil {
		t.Errorf("an unknown title: %+v", unknown)
	}

	off := e2eService(t, &fakeGateway{}, testBox(t))
	off.tm = tmdb.New("", "")
	w2, _, err := off.Request(ctx, store.Wanted{TMDBID: 1, MediaType: "series", Title: "Anything"}, asAdmin("user"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := off.RefreshInventory(ctx, false); err == nil {
		t.Error("a refresh without a TMDB key did not say so")
	}
	all, _ := off.Requests(ctx)
	if len(all) != 1 || all[0].ID != w2.ID || !strings.Contains(all[0].Detail, "TMDB_API_KEY") {
		t.Errorf("the request without a TMDB key: %+v", all)
	}
}

// ── a requested movie ──────────────────────────────────────────────────────

// A requested movie is its target, wanted from the start and searched from
// its release as TMDB dates it: waiting while it is in cinemas only (then
// 90 days after), searched once out digitally. Find & grab grabs the
// target, as the sweep would.
func TestARequestedMovie(t *testing.T) {
	r := newFollowRig(t)
	ctx := context.Background()
	theatrical := day(-44)
	movie := map[string]any{
		"id": 990100, "title": "Example Clay Film", "status": "Released", "release_date": dateOf(theatrical), "imdb_id": "tt99009100",
		"release_dates": map[string]any{"results": []map[string]any{{"iso_3166_1": "US",
			"release_dates": []map[string]any{{"type": 3, "release_date": theatrical.Format(time.RFC3339)}}}}},
	}
	r.tmdb.set(func(f *fakeTMDB) { f.movies[990100] = movie })
	w, _, err := r.svc.Request(ctx, store.Wanted{TMDBID: 990100, MediaType: "movie", Title: "Example Clay Film", Year: 2026}, asAdmin("friend"))
	if err != nil || w.TitleID != "movie:990100" || w.Status != "pending" {
		t.Fatalf("the request: %+v %v", w, err)
	}
	r.settle(t, false)
	v := r.view(t, w.ID)
	window := theatrical.Add(theatricalWait)
	if v.Status != "waiting" || !strings.Contains(v.Detail, "in cinemas since "+dateOf(theatrical)+", and TMDB dates no release to watch at home; searched from "+
		window.Format("2006-01-02 15:04 UTC")+" on, 90 days after it came to cinemas") {
		t.Errorf("in cinemas only: %s %q", v.Status, v.Detail)
	}
	if due, _ := r.svc.st.DueTargets(ctx, 10); len(due) != 0 {
		t.Errorf("searched before its release: %+v", due)
	}

	// TMDB dates its digital release, yesterday.
	r.tmdb.set(func(f *fakeTMDB) {
		f.movies[990100]["release_dates"] = map[string]any{"results": []map[string]any{{"iso_3166_1": "US",
			"release_dates": []map[string]any{
				{"type": 3, "release_date": theatrical.Format(time.RFC3339)},
				{"type": 4, "release_date": day(-1).Format(time.RFC3339)}}}}}
	})
	sqlExec(t, `UPDATE titles SET inventory_at = now() - interval '1 day' WHERE id = 'movie:990100'`)
	r.settle(t, true)
	if v := r.view(t, w.ID); v.Status != "pending" || v.Detail != "out digitally since "+dateOf(day(-1))+"; not searched yet" {
		t.Errorf("once out: %s %q", v.Status, v.Detail)
	}
	if due, _ := r.svc.st.DueTargets(ctx, 10); len(due) != 1 {
		t.Errorf("due once out: %d", len(due))
	}
	if run := r.settle(t, true); run.Titles != 0 {
		t.Errorf("a released movie is not read again: %+v", run)
	}

	src := newFakeSource(t, "usenet", "K", "Example.Clay.Film.2026.1080p.WEB-DL.x265-G")
	addSource(t, r.svc, "example", src, nil)
	if err := r.svc.AutoGrab(ctx, w.ID); err != nil {
		t.Fatal(err)
	}
	tg, _ := r.svc.st.TargetAt(ctx, "movie:990100", nil, nil)
	if tg.State != store.TargetGrabbed || !store.IsTargetGrab(tg.GrabID) {
		t.Fatalf("find & grab grabbed the target: %+v", tg)
	}
	if g, _ := r.svc.st.GetTargetGrab(ctx, tg.GrabID); g.Via != "request" {
		t.Errorf("the grab came via %q", g.Via)
	}
	if v := r.view(t, w.ID); v.Status != "downloading" {
		t.Errorf("while it downloads: %s %q", v.Status, v.Detail)
	}
	if _, err := r.svc.st.LatestGrab(ctx, w.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a request grab was recorded beside the target grab: %v", err)
	}
	if err := r.svc.AutoGrab(ctx, w.ID); err == nil {
		t.Error("grabbed twice")
	}
}

// A movie that premiered at a festival long before it reached cinemas is
// searched 90 days after it reached them, not after the premiere; and while
// its window is that guess, TMDB is read for it daily, until TMDB dates a
// release to watch at home.
func TestAMovieInCinemasIsReadUntilItIsOutAtHome(t *testing.T) {
	r := newFollowRig(t)
	ctx := context.Background()
	festival, cinema := day(-200), day(-100)
	dates := []map[string]any{{"type": 1, "release_date": festival.Format(time.RFC3339)}, {"type": 3, "release_date": cinema.Format(time.RFC3339)}}
	r.tmdb.set(func(f *fakeTMDB) {
		f.movies[990100] = map[string]any{"id": 990100, "title": "Example Clay Film", "status": "Released", "release_date": dateOf(festival),
			"release_dates": map[string]any{"results": []map[string]any{{"iso_3166_1": "US", "release_dates": dates}}}}
	})
	if _, _, err := r.svc.Request(ctx, store.Wanted{TMDBID: 990100, MediaType: "movie", Title: "Example Clay Film", Year: 2026}, asAdmin("friend")); err != nil {
		t.Fatal(err)
	}
	r.settle(t, false)
	tg, _ := r.svc.st.TargetAt(ctx, "movie:990100", nil, nil)
	if tg.ReleaseKind != "theatrical" || tg.WindowAt == nil || !tg.WindowAt.Equal(cinema.Add(theatricalWait)) || !tg.AirDate.Equal(*cinema) {
		t.Fatalf("the window: %+v", tg)
	}
	sqlExec(t, `UPDATE titles SET inventory_at = now() - interval '1 day' WHERE id = 'movie:990100'`)
	if run := r.settle(t, true); run.Titles != 1 {
		t.Errorf("a movie whose window is a guess, read a day ago: %+v", run)
	}
	r.tmdb.set(func(f *fakeTMDB) {
		f.movies[990100]["release_dates"] = map[string]any{"results": []map[string]any{{"iso_3166_1": "US",
			"release_dates": append(dates, map[string]any{"type": 4, "release_date": day(-3).Format(time.RFC3339)})}}}
	})
	sqlExec(t, `UPDATE titles SET inventory_at = now() - interval '1 day' WHERE id = 'movie:990100'`)
	r.settle(t, true)
	if tg, _ := r.svc.st.TargetAt(ctx, "movie:990100", nil, nil); tg.ReleaseKind != "digital" || !tg.WindowAt.Equal(*day(-3)) {
		t.Errorf("once out at home: %+v", tg)
	}
	sqlExec(t, `UPDATE titles SET inventory_at = now() - interval '1 day' WHERE id = 'movie:990100'`)
	if run := r.settle(t, true); run.Titles != 0 {
		t.Errorf("a movie out at home read again: %+v", run)
	}
}

// ── what grabs on its own ──────────────────────────────────────────────────

func TestWhatIsNew(t *testing.T) {
	now := time.Now()
	followed := now.Add(-48 * time.Hour)
	before, after := followed.Add(-time.Hour), followed.Add(time.Hour)
	tracked := store.Title{Kind: "series"}
	requested := store.Title{Kind: "series", FollowedAt: &followed, Approved: true}
	awaiting := store.Title{Kind: "series", FollowedAt: &followed}
	ep := func(aired *time.Time, added *time.Time) store.Target {
		return store.Target{Kind: "episode", AirDate: aired, AddedAt: added}
	}
	for _, tc := range []struct {
		name  string
		title store.Title
		t     store.Target
		want  bool
	}{
		{"aired today", tracked, ep(day(0), nil), true},
		{"aired 14 days ago", tracked, ep(day(-14), nil), true},
		{"aired 15 days ago", tracked, ep(day(-15), nil), false},
		{"no air date", tracked, ep(nil, nil), false},
		{"an old episode a follow added", requested, ep(day(-400), &after), true},
		{"an old episode from before the follow", requested, ep(day(-400), &before), false},
		{"an old episode of the import", requested, ep(day(-400), nil), false},
		{"a refresh added it, nobody follows", tracked, ep(day(-400), &after), false},
		{"a requested movie", store.Title{Kind: "movie", FollowedAt: &followed, Approved: true}, store.Target{Kind: "movie"}, true},
		{"a requested movie awaiting approval", store.Title{Kind: "movie", FollowedAt: &followed}, store.Target{Kind: "movie"}, false},
		{"an old episode a follow added, awaiting approval", awaiting, ep(day(-400), &after), false},
		{"the import's movie", store.Title{Kind: "movie"}, store.Target{Kind: "movie", AddedAt: &after}, false},
		{"a requested movie the library held once", store.Title{Kind: "movie", FollowedAt: &followed, Approved: true}, store.Target{Kind: "movie", EverHeld: true}, false},
		{"aired yesterday, held once", tracked, store.Target{Kind: "episode", AirDate: day(-1), EverHeld: true}, false},
		{"a follow added it, held once", requested, store.Target{Kind: "episode", AirDate: day(-400), AddedAt: &after, EverHeld: true}, false},
	} {
		if got := isNew(tc.title, tc.t, 14, now); got != tc.want {
			t.Errorf("%s: isNew = %v", tc.name, got)
		}
	}
	old := ep(day(-400), nil)
	fresh := ep(day(-1), nil)
	old.Monitored, fresh.Monitored, tracked.Monitored = true, true, true
	for _, tc := range []struct {
		name         string
		pol          SearchSettings
		old, fresh   bool
		grabsAnyThng bool
	}{
		{"both off", SearchSettings{}, false, false, false},
		{"everything", SearchSettings{AutoGrab: true}, true, true, true},
		{"new only", SearchSettings{AutoGrabNew: true, NewWithinDays: 7}, false, true, true},
		{"new only, no days named", SearchSettings{AutoGrabNew: true}, false, true, true},
	} {
		if tc.pol.Grabs(tracked, old, now) != tc.old || tc.pol.Grabs(tracked, fresh, now) != tc.fresh || tc.pol.GrabsAnything() != tc.grabsAnyThng {
			t.Errorf("%s: old %v fresh %v anything %v", tc.name, tc.pol.Grabs(tracked, old, now), tc.pol.Grabs(tracked, fresh, now), tc.pol.GrabsAnything())
		}
		// A title held for an admin's approval, a series the import says is
		// anime or daily, what is not monitored: nothing on its own.
		if tc.pol.Grabs(store.Title{Kind: "series", Monitored: true, Origin: store.OriginRequest}, fresh, now) {
			t.Errorf("%s: a title held for approval is grabbed on its own", tc.name)
		}
		for _, kind := range []string{"anime", "daily"} {
			if tc.pol.Grabs(store.Title{Kind: "series", Monitored: true, SeriesType: kind}, fresh, now) {
				t.Errorf("%s: a series of type %s is grabbed on its own", tc.name, kind)
			}
		}
		unmonitored := fresh
		unmonitored.Monitored = false
		if tc.pol.Grabs(tracked, unmonitored, now) || tc.pol.Grabs(store.Title{Kind: "series"}, fresh, now) {
			t.Errorf("%s: what is not monitored is grabbed on its own", tc.name)
		}
	}
}

func TestMovieWindow(t *testing.T) {
	cinema, home, festival, limited := *day(-10), *day(20), *day(-120), *day(-20)
	for _, tc := range []struct {
		name string
		m    tmdb.Movie
		at   *time.Time
		kind string
	}{
		{"a digital release", tmdb.Movie{Releases: []tmdb.Release{{Type: tmdb.ReleaseTheatrical, Date: cinema}, {Type: tmdb.ReleaseDigital, Date: home}}}, &home, "digital"},
		{"in cinemas only", tmdb.Movie{Releases: []tmdb.Release{{Type: tmdb.ReleaseTheatrical, Date: cinema}}}, ptr(cinema.Add(theatricalWait)), "theatrical"},
		{"nothing dated", tmdb.Movie{}, nil, ""},
		// A festival's premiere months before it reaches cinemas opens
		// nothing: the theatrical releases do, the first of them.
		{"a premiere, then cinemas", tmdb.Movie{Releases: []tmdb.Release{{Type: tmdb.ReleasePremiere, Date: festival},
			{Type: tmdb.ReleaseTheatrical, Date: cinema}, {Type: tmdb.ReleaseTheatricalLimited, Date: limited}}},
			ptr(limited.Add(theatricalWait)), "theatrical"},
		{"a premiere and a primary date", tmdb.Movie{ReleaseDate: &cinema, Releases: []tmdb.Release{{Type: tmdb.ReleasePremiere, Date: festival}}},
			ptr(cinema.Add(theatricalWait)), "theatrical"},
		{"a premiere only", tmdb.Movie{Releases: []tmdb.Release{{Type: tmdb.ReleasePremiere, Date: festival}}},
			ptr(festival.Add(theatricalWait)), "theatrical"},
	} {
		at, _, kind := movieWindow(tc.m)
		if kind != tc.kind || (at == nil) != (tc.at == nil) || (at != nil && !at.Equal(*tc.at)) {
			t.Errorf("%s: %v %q", tc.name, at, kind)
		}
	}
}

func ptr(t time.Time) *time.Time { return &t }

// What a request reads as, from how far acquire is with its title.
func TestRequestState(t *testing.T) {
	now := time.Now()
	read := now.Add(-time.Hour)
	next := &store.NextEpisode{Season: 1, Episode: 10, AirDate: *day(5)}
	// past is two days ago: out yesterday, the 30-hour delay would still run
	// until 06:00 UTC today.
	soon, past := day(20), day(-2)
	for _, tc := range []struct {
		name           string
		p              store.Progress
		status, detail string
	}{
		{"not read yet", store.Progress{Kind: "series"}, "following", "reading its episodes from TMDB"},
		{"not readable", store.Progress{Kind: "series", InventoryErr: "TMDB did not answer: 503"}, "following",
			"its episodes could not be read from TMDB yet: TMDB did not answer: 503; tried again every 15 minutes until they are"},
		{"unknown to TMDB", store.Progress{Kind: "series", InventoryAt: &read, InventoryErr: "TMDB does not know it (/tv/1)",
			InventoryRetryAt: day(7)}, "following",
			"its episodes could not be read from TMDB: TMDB does not know it (/tv/1); read again on " + dateOf(day(7))},
		{"no TMDB key", store.Progress{Kind: "movie", InventoryErr: noTMDB}, "pending", "its release dates cannot be read: " + noTMDB},
		{"no episodes, ended", store.Progress{Kind: "series", InventoryAt: &read}, "following",
			"TMDB lists no episodes of it, and says it does not run: it is read again weekly"},
		{"running", store.Progress{Kind: "series", InventoryAt: &read, Running: true, HoldingsKnown: true, Episodes: 13, Held: 3,
			OnTheWay: 2, Missing: 4, Failed: 1, Unaired: 4, Next: next}, "following",
			"13 episodes: 3 in the library, 2 on the way, 4 missing (the last grab of 1 failed), 4 not aired yet; next S01E10 on " + dateOf(day(5))},
		{"holdings unknown", store.Progress{Kind: "series", InventoryAt: &read, Running: true, Episodes: 1, Unaired: 1}, "following",
			"1 episode: 0 in the library, 1 not aired yet; the catalog has not said yet what the library holds of it"},
		{"complete", store.Progress{Kind: "series", InventoryAt: &read, Episodes: 8, Held: 8, HoldingsKnown: true}, "fulfilled",
			"complete: all 8 episodes are in the library"},
		{"held, still running", store.Progress{Kind: "series", InventoryAt: &read, Running: true, Episodes: 8, Held: 8, HoldingsKnown: true},
			"following", "8 episodes: 8 in the library"},
		{"no episodes yet", store.Progress{Kind: "series", InventoryAt: &read, Running: true}, "following",
			"TMDB lists no episodes of it yet; it is read again on every refresh while it runs"},
		{"a movie not read yet", store.Progress{Kind: "movie"}, "pending", "reading its release dates from TMDB"},
		{"a movie without a date", store.Progress{Kind: "movie", Movie: &store.Target{State: store.TargetWanted, ReleasePending: true}},
			"waiting", "TMDB dates no release of it yet: it is read again daily, and searched once it comes out to watch at home — " +
				"or 90 days after it comes to cinemas, if TMDB dates nothing else"},
		{"a movie to come", store.Progress{Kind: "movie", Movie: &store.Target{State: store.TargetWanted, WindowAt: soon, AirDate: soon, ReleaseKind: "physical"}},
			"waiting", "comes out on disc on " + dateOf(soon) + "; searched from " + soon.Add(30*time.Hour).Format("2006-01-02 15:04 UTC") + " on"},
		{"a movie out", store.Progress{Kind: "movie", HoldingsKnown: true, Movie: &store.Target{State: store.TargetWanted, WindowAt: past, AirDate: past,
			ReleaseKind: "tv", SearchOutcome: store.SearchNone}}, "pending", "out on TV since " + dateOf(past) + "; searched, nothing found yet"},
		{"a movie out, within the delay", store.Progress{Kind: "movie", HoldingsKnown: true, Movie: &store.Target{State: store.TargetWanted,
			WindowAt: day(0), AirDate: day(0), ReleaseKind: "digital"}}, "waiting",
			"out digitally since " + dateOf(day(0)) + "; searched from " + day(0).Add(30*time.Hour).Format("2006-01-02 15:04 UTC") + " on"},
		{"a movie in cinemas", store.Progress{Kind: "movie", HoldingsKnown: true, Movie: &store.Target{State: store.TargetWanted,
			WindowAt: ptr(day(-10).Add(theatricalWait)), AirDate: day(-10), ReleaseKind: "theatrical"}}, "waiting",
			"in cinemas since " + dateOf(day(-10)) + ", and TMDB dates no release to watch at home; searched from " +
				day(-10).Add(theatricalWait).Format("2006-01-02 15:04 UTC") + " on, 90 days after it came to cinemas: until TMDB dates one, " +
				"only an admin grabs it — a release before one is most often a recording made in a cinema; TMDB is read again daily"},
		{"a movie on its way", store.Progress{Kind: "movie", Movie: &store.Target{State: store.TargetDownloaded, Detail: "downloaded; copying"}},
			"downloading", "downloaded; copying"},
		{"a movie held", store.Progress{Kind: "movie", Movie: &store.Target{State: store.TargetHeld}}, "packaging", "in the library; being packaged"},
		{"a movie that plays", store.Progress{Kind: "movie", Movie: &store.Target{State: store.TargetHeld, HeldPlayable: true}}, "fulfilled", "in the library"},
	} {
		status, detail := requestState(tc.p, now, SearchSettings{AirDelayHours: 30, AirTimes: AirTimesOff})
		if status != tc.status || detail != tc.detail {
			t.Errorf("%s:\n  got  %s %q\n  want %s %q", tc.name, status, detail, tc.status, tc.detail)
		}
	}
	// With TVmaze's air times, a series whose air times were never read says
	// that nothing of it is grabbed on its own until they are.
	running := store.Progress{Kind: "series", InventoryAt: &read, Running: true, HoldingsKnown: true, Episodes: 2, Missing: 1, Unaired: 1}
	withTimes := SearchSettings{AirDelayHours: 30, AirTimes: AirTimesTVmaze}
	if _, detail := requestState(running, now, withTimes); detail !=
		"2 episodes: 0 in the library, 1 missing, 1 not aired yet; nothing of it is grabbed on its own until TVmaze's air times of it are read" {
		t.Errorf("air times never read: %q", detail)
	}
	running.ScheduleErr = "TVmaze did not answer: HTTP 503"
	if _, detail := requestState(running, now, withTimes); !strings.HasSuffix(detail, "until TVmaze's air times of it are read (TVmaze did not answer: HTTP 503)") {
		t.Errorf("air times never read, TVmaze down: %q", detail)
	}
	running.ScheduleAt, running.AirTimesRead = &read, true
	if _, detail := requestState(running, now, withTimes); detail != "2 episodes: 0 in the library, 1 missing, 1 not aired yet" {
		t.Errorf("air times read: %q", detail)
	}
	running.AirTimeHolds = 1
	if _, detail := requestState(running, now, withTimes); !strings.HasSuffix(detail,
		"; 1 episode held, not grabbed on its own until TVmaze and TMDB agree on it or it has an air time") {
		t.Errorf("an episode held: %q", detail)
	}
}

// With only the new things grabbed on their own, the sweep takes them first
// and grabs them; the old backlog is searched and left for the missing tab.
func TestTheSweepGrabsOnlyTheNewThings(t *testing.T) {
	r := newTargetRig(t, "Show.Name.S01E01.1080p.WEB-DL.x265-G", "Show.Name.S01E02.1080p.WEB-DL.x265-G")
	ctx := context.Background()
	_, eps := track(t, r.svc, 990711, "Show Name", 2, true)
	_ = r.svc.st.SetAirWindow(ctx, eps[0].ID, day(-300), 0)
	if _, err := r.svc.SaveSearchSettings(ctx, SearchSettings{PreferProtocol: "usenet", MaxConcurrentGrabs: 3, AutoGrabNew: true}, 0, "test"); err != nil {
		t.Fatal(err)
	}
	doc, _ := r.svc.GetSearchSettings(ctx)
	if !doc.AutoGrabNew || doc.NewWithinDays != DefaultNewWithinDays || doc.AutoGrab {
		t.Fatalf("settings: %+v", doc)
	}
	// One target a run: the new one first, though the old one aired first.
	if err := r.svc.SweepBacklog(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if st := target(t, r.svc, eps[1].ID); st.State != store.TargetGrabbed {
		t.Fatalf("the new episode: %+v", st)
	}
	if err := r.svc.SweepBacklog(ctx, 1); err != nil {
		t.Fatal(err)
	}
	old := target(t, r.svc, eps[0].ID)
	if old.State != store.TargetWanted || old.SearchOutcome != store.SearchFound || len(r.gw.added()) != 1 {
		t.Errorf("the old backlog: %+v (adds %d)", old, len(r.gw.added()))
	}
	policy := ""
	for _, sec := range r.svc.Setup(ctx).Sections {
		if sec.Key == "grab-policy" {
			policy = sec.Summary
		}
	}
	if !strings.Contains(policy, "new episodes (aired in the last 14 days) and what was requested are grabbed on their own") {
		t.Errorf("setup's grab policy: %q", policy)
	}
	var ve *ValidationError
	if _, err := r.svc.SaveSearchSettings(ctx, SearchSettings{PreferProtocol: "usenet", MaxConcurrentGrabs: 3, NewWithinDays: 400}, 0, "test"); !errors.As(err, &ve) {
		t.Errorf("400 days: %v", err)
	}
}

// The cap on downloads at once holds for grabbers at the same time — the
// sweep, the feeds, the grab queue —: a grab being handed over takes its
// slot, so a second one meanwhile is refused instead of both going.
func TestTheCapHoldsForGrabsAtOnce(t *testing.T) {
	r := newTargetRig(t, "Show.Name.S01E01.1080p.WEB-DL.x265-G", "Show.Name.S01E02.1080p.WEB-DL.x265-G")
	ctx := context.Background()
	_, eps := track(t, r.svc, 990711, "Show Name", 2, true)
	if _, err := r.svc.SaveSearchSettings(ctx, SearchSettings{PreferProtocol: "usenet", MaxConcurrentGrabs: 1}, 0, "test"); err != nil {
		t.Fatal(err)
	}
	hold := make(chan struct{})
	r.gw.mu.Lock()
	r.gw.addHold = hold
	r.gw.mu.Unlock()
	first := make(chan error, 1)
	go func() {
		_, err := r.svc.GrabTarget(ctx, eps[0].ID, "missing")
		first <- err
	}()
	for deadline := time.Now().Add(5 * time.Second); len(r.gw.added()) == 0; {
		if time.Now().After(deadline) {
			t.Fatal("the first grab never reached the gateway")
		}
		time.Sleep(5 * time.Millisecond)
	}
	_, err := r.svc.GrabTarget(ctx, eps[1].ID, "missing")
	var capErr atCapacity
	if !errors.As(err, &capErr) || !strings.Contains(err.Error(), "on their way (cap 1)") {
		t.Errorf("a second grab while the first is handed over: %v", err)
	}
	close(hold)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if adds := r.gw.added(); len(adds) != 1 {
		t.Errorf("%d grabs handed over under a cap of one", len(adds))
	}
}

// handedOver are the releases the gateway was handed, by name.
func handedOver(t *testing.T, r *targetRig) []string {
	t.Helper()
	var out []string
	for _, add := range r.gw.added() {
		id, _ := add["wanted_item_id"].(string)
		g, err := r.svc.st.GetTargetGrab(context.Background(), id)
		if err != nil {
			t.Fatalf("an add for %q: %v", id, err)
		}
		out = append(out, g.ReleaseTitle)
	}
	return out
}

// asAdmin is a request an admin makes: approved at once.
func asAdmin(sub string) Requester { return Requester{Sub: sub, Admin: true} }

func TestFollowKind(t *testing.T) {
	got := []string{followKind("series"), followKind("tv"), followKind("TV"), followKind("movie"), followKind("episode"), followKind("")}
	if want := []string{"series", "series", "series", "movie", "", ""}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("kinds = %q", got)
	}
	m := mediaTypesOf("series")
	sort.Strings(m)
	if strings.Join(m, ",") != "series,tv" {
		t.Errorf("media types of a series: %v", m)
	}
}

// A requested series TMDB files as animation from Japan is numbered by its
// releases as one run, not by season and episode: nothing of it is grabbed on
// its own — not from the feeds, not by the sweep —, and the request says so.
// Its episodes are searched, and grabbed from the missing tab.
func TestARequestedAnimeIsGrabbedFromTheMissingTabOnly(t *testing.T) {
	r := newFollowRig(t)
	ctx := context.Background()
	r.tmdb.set(func(f *fakeTMDB) {
		f.series[990751] = &tmdbSeries{name: "Example Harbour Anime", status: "Returning Series", tvdb: 9907751, firstAir: day(-30),
			countries: []string{"JP"}, genres: []int{16}, kind: "Scripted",
			seasons: map[int][]tmdbEpisode{1: {{title: "One", air: day(-10)}, {title: "Two", air: day(-3)}}}}
	})
	w, _, err := r.svc.Request(ctx, store.Wanted{TMDBID: 990751, MediaType: "series", Title: "Example Harbour Anime"}, asAdmin("admin"))
	if err != nil {
		t.Fatal(err)
	}
	r.settle(t, false)
	release := "Example.Harbour.Anime.S01E02.1080p.WEB.h264-GRP"
	addFeed(t, r.svc, "feed", newFeedSource(t, true, tv(release, ago(time.Hour))))
	src := newFakeSource(t, "usenet", "K", release)
	addSource(t, r.svc, "example", src, nil)
	if _, err := r.svc.SaveSearchSettings(ctx, SearchSettings{PreferProtocol: "usenet", MaxConcurrentGrabs: 10, AutoGrab: true}, 0, "test"); err != nil {
		t.Fatal(err)
	}
	if run, _ := r.svc.SyncRSS(ctx); run.Feeds != 0 || run.Grabbed != 0 {
		t.Errorf("the feeds: %+v", run)
	}
	if err := r.svc.SweepBacklog(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if len(r.gw.added()) != 0 {
		t.Errorf("the sweep grabbed %d", len(r.gw.added()))
	}
	if st := target(t, r.svc, "series:990751:s01e002"); st.LastSearchAt == nil {
		t.Errorf("not searched: %+v", st)
	}
	v := r.view(t, w.ID)
	if v.AutoGrab == nil || *v.AutoGrab || !strings.Contains(v.Detail, "an anime, a daily show): grabbed from the missing tab only") {
		t.Errorf("the request: %q %v", v.Detail, v.AutoGrab)
	}
	if _, err := r.svc.GrabTarget(ctx, "series:990751:s01e002", "missing"); err != nil || len(r.gw.added()) != 1 {
		t.Errorf("grab now: %v (adds %d)", err, len(r.gw.added()))
	}
}

// A title TMDB does not know says when it is read again — a week on —, and
// is, whatever its kind: a followed series, a followed movie, a request
// awaiting approval, which no refresh reads otherwise. Not before.
func TestATitleTMDBDoesNotKnowIsReadAgainInAWeek(t *testing.T) {
	r := newFollowRig(t)
	ctx := context.Background()
	series, _, _ := r.svc.Request(ctx, store.Wanted{TMDBID: 990901, MediaType: "series", Title: "Nobody Knows"}, asAdmin("admin"))
	movie, _, _ := r.svc.Request(ctx, store.Wanted{TMDBID: 990902, MediaType: "movie", Title: "Nobody Knows Either"}, asAdmin("admin"))
	awaiting, _, _ := r.svc.Request(ctx, store.Wanted{TMDBID: 990903, MediaType: "series", Title: "Nobody Asked"}, Requester{Sub: "user"})
	r.settle(t, false)
	again := time.Now().Add(7 * 24 * time.Hour).UTC().Format("2006-01-02")
	for _, w := range []store.Wanted{series, movie} {
		if v := r.view(t, w.ID); !strings.Contains(v.Detail, "could not be read from TMDB: TMDB does not know it") ||
			!strings.HasSuffix(v.Detail, "read again on "+again) {
			t.Errorf("%s: %q", w.Title, v.Detail)
		}
	}
	calls := len(r.tmdb.called())
	if r.settle(t, true); len(r.tmdb.called()) != calls || r.svc.retryFollows(ctx) {
		t.Errorf("read again before its week: %v", r.tmdb.called()[calls:])
	}
	sqlExec(t, `UPDATE titles SET inventory_retry_at = now() - interval '1 minute' WHERE tmdb_id IN (990901, 990902, 990903)`)
	if !r.svc.retryFollows(ctx) {
		t.Error("the request awaiting approval is not tried again a week on")
	}
	r.settle(t, true)
	read := map[string]bool{}
	for _, c := range r.tmdb.called()[calls:] {
		read[c] = true
	}
	if !read["/tv/990901"] || !read["/movie/990902"] || !read["/tv/990903"] {
		t.Errorf("a week on, read: %v", r.tmdb.called()[calls:])
	}
	if v := r.view(t, awaiting.ID); v.Detail != "waiting for an admin's approval: until then nothing of it is searched or grabbed · "+
		"what TMDB says of it could not be read from TMDB: TMDB does not know it (/tv/990903); read again on "+again {
		t.Errorf("awaiting: %q", v.Detail)
	}
}

// What a request reads as in the five words of a request before it could
// follow a title, for a client that knows those alone.
func TestLegacyStatus(t *testing.T) {
	onTheWay := &store.Progress{Kind: "series", OnTheWay: 2}
	for _, tc := range []struct {
		v    RequestView
		want string
	}{
		{RequestView{Wanted: store.Wanted{Status: "waiting"}}, "pending"},
		{RequestView{Wanted: store.Wanted{Status: "awaiting"}}, "pending"},
		{RequestView{Wanted: store.Wanted{Status: "following"}, Progress: &store.Progress{Kind: "series"}}, "pending"},
		{RequestView{Wanted: store.Wanted{Status: "following"}, Progress: onTheWay}, "downloading"},
		{RequestView{Wanted: store.Wanted{Status: "declined"}}, "failed"},
		{RequestView{Wanted: store.Wanted{Status: "packaging"}}, "packaging"},
		{RequestView{Wanted: store.Wanted{Status: "fulfilled"}}, "fulfilled"},
	} {
		if got := LegacyStatus(tc.v); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.v.Status, got, tc.want)
		}
	}
}

// A requested movie the library held, then lost — its item removed — is
// wanted again, held once: not one of the new things, so not grabbed on its
// own with only those grabbed so.
func TestARequestedMovieHeldOnceIsNotNewAgain(t *testing.T) {
	r := newFollowRig(t)
	ctx := context.Background()
	r.tmdb.set(func(f *fakeTMDB) {
		f.movies[990100] = map[string]any{"id": 990100, "title": "Example Clay Film", "status": "Released", "release_date": dateOf(day(-60)),
			"release_dates": map[string]any{"results": []map[string]any{{"iso_3166_1": "US",
				"release_dates": []map[string]any{{"type": 4, "release_date": day(-20).Format(time.RFC3339)}}}}}}
	})
	if _, _, err := r.svc.Request(ctx, store.Wanted{TMDBID: 990100, MediaType: "movie", Title: "Example Clay Film", Year: 2026}, asAdmin("admin")); err != nil {
		t.Fatal(err)
	}
	r.settle(t, false)
	if _, err := r.svc.st.ApplyMovieHolding(ctx, "movie:990100", "", "default", &store.HeldEpisode{ItemID: "item-9", Playable: true}); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.OnItemRemoved(ctx, "item-9"); err != nil {
		t.Fatal(err)
	}
	tg, _ := r.svc.st.TargetAt(ctx, "movie:990100", nil, nil)
	title, _ := r.svc.st.TitleByID(ctx, "movie:990100")
	pol := SearchSettings{AutoGrabNew: true, AirDelayHours: 30}
	if tg.State != store.TargetWanted || !tg.EverHeld || pol.Grabs(title, tg, time.Now()) {
		t.Errorf("a movie held once: %+v, grabbed on its own %v", tg, pol.Grabs(title, tg, time.Now()))
	}
	if !(SearchSettings{AutoGrab: true}).Grabs(title, tg, time.Now()) {
		t.Error("with everything grabbed on its own, it is not")
	}
}

// Asked for at once — by users and admins, as series and as tv —, a title
// gets one request, and every one asking finds it; an admin among them
// approves it.
func TestATitleAskedForAtOnceGetsOneRequest(t *testing.T) {
	r := newFollowRig(t)
	ctx := context.Background()
	r.tmdb.set(func(f *fakeTMDB) { f.series[990801] = sevenHarbours() })
	const n = 8
	ids, errs := make([]string, n), make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			media := map[bool]string{true: "series", false: "tv"}[i%3 == 0]
			w, _, err := r.svc.Request(ctx, store.Wanted{TMDBID: 990801, MediaType: media, Title: "Example Saga"},
				Requester{Sub: fmt.Sprintf("someone-%d", i), Admin: i%2 == 0})
			ids[i], errs[i] = w.ID, err
		}(i)
	}
	close(start)
	wg.Wait()
	for i := range ids {
		if errs[i] != nil || ids[i] != ids[0] {
			t.Errorf("asker %d: %s %v, want %s", i, ids[i], errs[i], ids[0])
		}
	}
	all, _ := r.svc.st.ListWanted(ctx)
	if len(all) != 1 || all[0].Approval != store.ApprovalApproved || all[0].TitleID != "series:990801" {
		t.Errorf("the requests: %+v", all)
	}
}

// An admin approving a request while another declines it: one decision is
// taken, the other refused, saying what the request is now; the title is
// followed as the one taken says.
func TestOneDecisionOnARequestIsTaken(t *testing.T) {
	r := newFollowRig(t)
	ctx := context.Background()
	for round := int64(0); round < 4; round++ {
		id := 990810 + round
		r.tmdb.set(func(f *fakeTMDB) { f.series[id] = sevenHarbours() })
		w, _, err := r.svc.Request(ctx, store.Wanted{TMDBID: id, MediaType: "series", Title: "Example Saga"}, Requester{Sub: "user"})
		if err != nil || w.Approval != store.ApprovalAwaiting {
			t.Fatalf("a user's request: %+v %v", w, err)
		}
		var approved, declined error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); _, approved = r.svc.ApproveRequest(ctx, w.ID, "admin-1") }()
		go func() { defer wg.Done(); _, declined = r.svc.DeclineRequest(ctx, w.ID, "admin-2") }()
		wg.Wait()
		if (approved == nil) == (declined == nil) || (approved != nil && !IsDecided(approved)) || (declined != nil && !IsDecided(declined)) {
			t.Fatalf("round %d: approve %v, decline %v — want one taken, the other refused", round, approved, declined)
		}
		now, _ := r.svc.st.GetWanted(ctx, w.ID)
		title, _ := r.svc.st.TitleByID(ctx, w.TitleID)
		if approved == nil && (now.Approval != store.ApprovalApproved || now.DecidedBy != "admin-1" || !title.Followed() ||
			declined.Error() != "the request is approved now, by admin-1") {
			t.Errorf("round %d, approved: %+v %+v %v", round, now, title, declined)
		}
		if declined == nil && (now.Approval != store.ApprovalDeclined || now.DecidedBy != "admin-2" || title.Followed() ||
			approved.Error() != "the request is declined now, by admin-2") {
			t.Errorf("round %d, declined: %+v %+v %v", round, now, title, approved)
		}
	}
}

// Of a series a request made, every episode is one it asked for: with only
// the new things grabbed on its own, its back catalogue is grabbed on its own
// too, and its request says so. Of a series acquire tracked before, only what
// is new is: the episodes it had before wait for the missing tab, unless
// everything missing is grabbed on its own.
func TestWhatOfARequestedSeriesIsGrabbedOnItsOwn(t *testing.T) {
	r := newFollowRig(t)
	ctx := context.Background()
	r.tmdb.set(func(f *fakeTMDB) {
		f.series[990821] = &tmdbSeries{name: "Example Requested Show", status: "Returning Series", firstAir: day(-40),
			seasons: map[int][]tmdbEpisode{1: {{title: "One", air: day(-40)}, {title: "Two", air: day(-2)}}}}
		f.series[990822] = &tmdbSeries{name: "Example Tracked Show", status: "Returning Series", firstAir: day(-40),
			seasons: map[int][]tmdbEpisode{1: {{title: "One", air: day(-40)}, {title: "Two", air: day(-2)}}}}
	})
	tracked, _ := track(t, r.svc, 990822, "Example Tracked Show", 1, true)
	sqlExec(t, `UPDATE acquisition_targets SET air_date = current_date - 40, air_window_opens_at = now() - interval '40 days' WHERE title_id = $1`, tracked.ID)
	made, _, err := r.svc.Request(ctx, store.Wanted{TMDBID: 990821, MediaType: "series", Title: "Example Requested Show"}, asAdmin("admin"))
	if err != nil {
		t.Fatal(err)
	}
	asked, _, err := r.svc.Request(ctx, store.Wanted{TMDBID: 990822, MediaType: "series", Title: "Example Tracked Show"}, asAdmin("admin"))
	if err != nil {
		t.Fatal(err)
	}
	r.settle(t, false)
	if _, err := r.svc.SaveSearchSettings(ctx, SearchSettings{PreferProtocol: "usenet", MaxConcurrentGrabs: 3, AutoGrabNew: true}, 0, "test"); err != nil {
		t.Fatal(err)
	}
	pol := r.svc.grabPolicy(ctx)
	grabs := func(titleID, target string) bool {
		ti, _ := r.svc.st.TitleByID(ctx, titleID)
		return pol.Grabs(ti, *mustTarget(t, r.svc, target), time.Now())
	}
	if v := r.view(t, made.ID); v.AutoGrabAired == nil || !*v.AutoGrabAired || !grabs("series:990821", "series:990821:s01e001") {
		t.Errorf("the series the request made: %v, its first episode grabbed %v", v.AutoGrabAired, grabs("series:990821", "series:990821:s01e001"))
	}
	if v := r.view(t, asked.ID); v.AutoGrabAired == nil || *v.AutoGrabAired || grabs(tracked.ID, "series:990822:s01e001") ||
		!grabs(tracked.ID, "series:990822:s01e002") {
		t.Errorf("the series tracked before: %v", v.AutoGrabAired)
	}
	if _, err := r.svc.SaveSearchSettings(ctx, SearchSettings{PreferProtocol: "usenet", MaxConcurrentGrabs: 3, AutoGrab: true}, 0, "test"); err != nil {
		t.Fatal(err)
	}
	if v := r.view(t, asked.ID); v.AutoGrabAired == nil || !*v.AutoGrabAired {
		t.Errorf("everything missing grabbed on its own: %v", v.AutoGrabAired)
	}
}

func mustTarget(t *testing.T, svc *Service, id string) *store.Target {
	t.Helper()
	tg := target(t, svc, id)
	return &tg
}
