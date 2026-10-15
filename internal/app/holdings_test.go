package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/laedeli/acquire/internal/events"
	"github.com/laedeli/acquire/internal/identity"
	"github.com/laedeli/acquire/internal/katalog"
	"github.com/laedeli/acquire/internal/store"
)

// holdingsAPI is katalog-api's holdings by identity.
type holdingsAPI struct {
	mu sync.Mutex
	// mode: "" answers; "unsupported" is a catalog without the endpoint (404
	// for everything); "down" answers 503.
	mode   string
	series map[string][]katalog.SeasonHoldings // by "tmdb:4711", "tvdb:…"
	movies map[string]katalog.MovieHolding     // by "imdb:tt…"
	asked  []string
}

func newHoldingsAPI() *holdingsAPI {
	return &holdingsAPI{series: map[string][]katalog.SeasonHoldings{}, movies: map[string]katalog.MovieHolding{}}
}

func (h *holdingsAPI) set(f func(h *holdingsAPI)) {
	h.mu.Lock()
	f(h)
	h.mu.Unlock()
}

func (h *holdingsAPI) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		defer h.mu.Unlock()
		q := r.URL.Query()
		h.asked = append(h.asked, r.URL.RawQuery)
		switch {
		case h.mode == "down":
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		case h.mode == "unsupported" || r.URL.Path != "/api/v1/holdings":
			http.NotFound(w, r)
			return
		case q.Get("page") != "":
			_, _ = w.Write([]byte(`{"items":[],"page":1,"pageSize":1,"total":0}`))
			return
		}
		key := ""
		for _, k := range []string{"tmdb", "tvdb", "imdb"} {
			if v := q.Get(k); v != "" {
				key = k + ":" + v
			}
		}
		switch q.Get("type") {
		case "series":
			if seasons, ok := h.series[key]; ok {
				_ = json.NewEncoder(w).Encode(katalog.SeriesHoldings{SeriesID: "series-" + key, Seasons: seasons})
				return
			}
		case "movie":
			if m, ok := h.movies[key]; ok {
				_ = json.NewEncoder(w).Encode(m)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not held"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func season1(eps ...katalog.EpisodeHolding) []katalog.SeasonHoldings {
	return []katalog.SeasonHoldings{{Season: 1, Episodes: eps}}
}

// The reconcile asks katalog-api what the library holds of every tracked
// series and movie, by each identity acquire holds in turn, and makes the
// targets match both ways. Until it has, nothing is missing.
func TestHoldingsReconcileFromTheAPI(t *testing.T) {
	r := newTargetRig(t)
	ctx := context.Background()
	show, eps := track(t, r.svc, 4711, "Show Name", 4, false)
	other, _ := track(t, r.svc, 99, "Other Show", 2, false)
	if err := r.svc.st.UpsertTitle(ctx, store.Title{TMDBID: 603, IMDBID: "tt0133093", Kind: "movie", Title: "The Matrix", Monitored: true}); err != nil {
		t.Fatal(err)
	}
	movieID := store.TitleID("movie", 603)
	r.api.set(func(h *holdingsAPI) {
		h.series["tmdb:4711"] = season1(
			katalog.EpisodeHolding{Episode: 1, ItemID: "e1", HasFile: true, Playable: true},
			katalog.EpisodeHolding{Episode: 2, ItemID: "e2", CoveredBy: "e1", Playable: true},
			katalog.EpisodeHolding{Episode: 3, ItemID: "e3"}, // an item without a file is not held
		)
		h.movies["imdb:tt0133093"] = katalog.MovieHolding{ItemID: "m1", HasFile: true}
	})
	if miss, _ := r.svc.st.Missing(ctx, 50); len(miss) != 0 {
		t.Fatalf("before the first reconcile nothing is missing: %+v", miss)
	}
	if c, _ := r.svc.st.Counts(ctx); c.HoldingsUnknown != 3 || c.UnknownTargets != 6 {
		t.Errorf("counts before: %+v", c)
	}

	run, err := r.svc.ReconcileHoldings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !run.Supported || run.Titles != 3 || run.Known != 3 || run.Failed != 0 || run.Held != 3 {
		t.Fatalf("run = %+v", run)
	}
	one, two := target(t, r.svc, eps[0].ID), target(t, r.svc, eps[1].ID)
	if one.State != store.TargetHeld || one.HeldItemID != "e1" || !one.HeldPlayable || two.HeldVia != "e1" {
		t.Errorf("held: %+v / %+v", one, two)
	}
	miss, _ := r.svc.st.Missing(ctx, 50)
	var names []string
	for _, m := range miss {
		names = append(names, m.Title)
	}
	if len(miss) != 4 || strings.Count(strings.Join(names, ","), "Show Name") != 2 {
		t.Errorf("missing = %v, want E03 and E04 of Show Name and both of Other Show", names)
	}
	if m, err := r.svc.st.TargetAt(ctx, movieID, nil, nil); err != nil || m.State != store.TargetHeld || m.HeldItemID != "m1" {
		t.Errorf("the movie, found by its IMDb id after its TMDB id: %+v %v", m, err)
	}
	if ti, _ := r.svc.st.TitleByID(ctx, other.ID); !ti.HoldingsKnown() || ti.CatalogID != "" {
		t.Errorf("a series the library does not hold is known to hold nothing: %+v", ti)
	}
	if ti, _ := r.svc.st.TitleByID(ctx, show.ID); ti.CatalogID != "series-tmdb:4711" {
		t.Errorf("catalog id: %+v", ti)
	}

	// The library losing a file makes its episodes wanted again.
	r.api.set(func(h *holdingsAPI) {
		h.series["tmdb:4711"] = season1(katalog.EpisodeHolding{Episode: 4, ItemID: "e4", HasFile: true})
	})
	run, _ = r.svc.ReconcileHoldings(ctx, show.ID)
	if run.Titles != 1 || run.Held != 1 || run.Released != 2 {
		t.Errorf("second run = %+v", run)
	}
	if one := target(t, r.svc, eps[0].ID); one.State != store.TargetWanted || one.Detail != "no longer in the library; wanted again" {
		t.Errorf("after the file went: %+v", one)
	}

	// A read side that is down keeps what is known, and says why.
	r.api.set(func(h *holdingsAPI) { h.mode = "down" })
	if _, err := r.svc.ReconcileHoldings(ctx); err == nil {
		t.Error("a read side that is down is an error")
	}
	if ti, _ := r.svc.st.TitleByID(ctx, show.ID); !ti.HoldingsKnown() || !strings.Contains(ti.HoldingsError, "503") {
		t.Errorf("after an outage: %+v", ti)
	}
	if miss, _ := r.svc.st.Missing(ctx, 50); len(miss) != 5 {
		t.Errorf("an outage changed what is missing: %d", len(miss))
	}

	// A catalog without holdings by identity makes every title's unknown:
	// nothing is missing, nothing is due.
	r.api.set(func(h *holdingsAPI) { h.mode = "unsupported" })
	run, err = r.svc.ReconcileHoldings(ctx)
	if err != nil || run.Supported || !strings.Contains(run.Error, "does not answer holdings by identity") {
		t.Fatalf("against a catalog without the contract: %+v %v", run, err)
	}
	if miss, _ := r.svc.st.Missing(ctx, 50); len(miss) != 0 {
		t.Errorf("missing with unknown holdings: %+v", miss)
	}
	if due, _ := r.svc.st.DueTargets(ctx, 50); len(due) != 0 {
		t.Errorf("due with unknown holdings: %+v", due)
	}
	if ti, _ := r.svc.st.TitleByID(ctx, show.ID); ti.HoldingsKnown() || !strings.Contains(ti.HoldingsError, "does not answer holdings") {
		t.Errorf("title: %+v", ti)
	}
}

// Between reconciles the catalog's events keep the holdings: an episode or
// movie discovered or packaged with its identities is held (packaged: and
// playable), a removed item's targets are wanted again — hunted only while
// monitored — and an event without identities still makes what acquire
// recorded for that item playable.
func TestHoldingsFromEvents(t *testing.T) {
	r := newTargetRig(t)
	ctx := context.Background()
	show, eps := track(t, r.svc, 4711, "Show Name", 3, true)
	one, two := 1, 2
	series := identity.IDs{"tmdb": "4711"}

	_ = r.svc.OnPackaged(ctx, events.ItemEvent{ItemID: "e1", SeriesExternalIDs: series, SeasonNumber: &season, EpisodeNumber: &one})
	if st := target(t, r.svc, eps[0].ID); st.State != store.TargetHeld || st.HeldItemID != "e1" || !st.HeldPlayable {
		t.Errorf("packaged with identity: %+v", st)
	}
	_ = r.svc.OnDiscovered(ctx, events.ItemEvent{ItemID: "e2", SeriesExternalIDs: series, SeasonNumber: &season, EpisodeNumber: &two})
	if st := target(t, r.svc, eps[1].ID); st.State != store.TargetHeld || st.HeldPlayable {
		t.Errorf("discovered: %+v", st)
	}
	_ = r.svc.OnPackaged(ctx, events.ItemEvent{ItemID: "e2"})
	if st := target(t, r.svc, eps[1].ID); !st.HeldPlayable {
		t.Errorf("packaged by item id alone: %+v", st)
	}
	// A discovered event replayed after the packaged one takes nothing back.
	_ = r.svc.OnDiscovered(ctx, events.ItemEvent{ItemID: "e2", SeriesExternalIDs: series, SeasonNumber: &season, EpisodeNumber: &two})
	if st := target(t, r.svc, eps[1].ID); !st.HeldPlayable {
		t.Errorf("a replayed discovered: %+v", st)
	}

	_ = r.svc.OnRemoved(ctx, events.ItemEvent{ItemID: "e1"})
	if st := target(t, r.svc, eps[0].ID); st.State != store.TargetWanted || st.Detail != "no longer in the library; wanted again" {
		t.Errorf("removed: %+v", st)
	}
	if miss, _ := r.svc.st.Missing(ctx, 10); len(miss) != 2 {
		t.Errorf("missing after a removal: %+v", miss)
	}
	// A series no longer monitored: what it loses is not hunted.
	if err := r.svc.st.UpsertTitle(ctx, store.Title{TMDBID: 4711, Kind: "series", Title: "Show Name", Monitored: false}); err != nil {
		t.Fatal(err)
	}
	_ = r.svc.OnRemoved(ctx, events.ItemEvent{ItemID: "e2"})
	if st := target(t, r.svc, eps[1].ID); st.State != store.TargetWanted || st.Detail != "no longer in the library; not monitored, so not hunted" {
		t.Errorf("removed from an unmonitored series: %+v", st)
	}
	if miss, _ := r.svc.st.Missing(ctx, 10); len(miss) != 0 {
		t.Errorf("an unmonitored series is missing %d", len(miss))
	}
	if due, _ := r.svc.st.DueTargets(ctx, 10); len(due) != 0 {
		t.Errorf("an unmonitored series is swept: %+v", due)
	}

	// An episode a grab's file of several covers is held through the holder;
	// its own item's discovered event names its item and keeps the rest, and
	// the holder's packaged event — the file's only one — makes it playable
	// and asks the holdings to confirm the series.
	three := 3
	if err := r.svc.st.HoldTarget(ctx, store.Hold{TargetID: eps[2].ID, ItemID: "h1", Via: "h1", Release: "Show.Name.S01E02E03.1080p-G",
		Source: "grab", Detail: "in the library through h1"}); err != nil {
		t.Fatal(err)
	}
	_ = r.svc.OnDiscovered(ctx, events.ItemEvent{ItemID: "c3", SeriesExternalIDs: series, SeasonNumber: &season, EpisodeNumber: &three})
	if st := target(t, r.svc, eps[2].ID); st.HeldItemID != "c3" || st.HeldVia != "h1" || st.HeldSource != "grab" || st.HeldPlayable {
		t.Errorf("a covered episode's own item: %+v", st)
	}
	r.svc.holdingsMu.Lock()
	r.svc.holdingsIDs = map[string]bool{}
	r.svc.holdingsMu.Unlock()
	two2 := 2
	_ = r.svc.OnPackaged(ctx, events.ItemEvent{ItemID: "h1", SeriesExternalIDs: series, SeasonNumber: &season, EpisodeNumber: &two2})
	if st := target(t, r.svc, eps[2].ID); !st.HeldPlayable {
		t.Errorf("the holder's packaged did not make what it covers playable: %+v", st)
	}
	r.svc.holdingsMu.Lock()
	confirm := r.svc.holdingsIDs[show.ID]
	r.svc.holdingsMu.Unlock()
	if !confirm {
		t.Error("an episode's packaged event did not ask the holdings to confirm its series")
	}

	// A movie is its one target, made when the library holds it.
	_ = r.svc.st.UpsertTitle(ctx, store.Title{TMDBID: 603, IMDBID: "tt0133093", Kind: "movie", Title: "The Matrix", Monitored: true})
	_ = r.svc.OnDiscovered(ctx, events.ItemEvent{ItemID: "m1", Type: "movie", ExternalIDs: identity.IDs{"imdb": "tt0133093"}})
	if m, err := r.svc.st.TargetAt(ctx, store.TitleID("movie", 603), nil, nil); err != nil || m.State != store.TargetHeld {
		t.Errorf("a movie discovered: %+v %v", m, err)
	}
	// What acquire does not track is ignored, not made.
	_ = r.svc.OnPackaged(ctx, events.ItemEvent{ItemID: "z", SeriesExternalIDs: identity.IDs{"tmdb": "1"}, SeasonNumber: &season, EpisodeNumber: &one})
	if c, _ := r.svc.st.Counts(ctx); c.Titles != 2 || c.Targets != 4 {
		t.Errorf("an untracked holding made something: %+v", c)
	}
	_ = show
}

var season = 1

// The schedule's reconcile is asked for, not run inside the event handler:
// the download events behind it wait for nothing.
func TestTheReconcileScheduleWakesItsWorker(t *testing.T) {
	r := newTargetRig(t)
	if err := r.svc.OnScheduleDue(context.Background(), "holdings-reconcile"); err != nil {
		t.Fatal(err)
	}
	r.svc.holdingsMu.Lock()
	all := r.svc.holdingsAll
	r.svc.holdingsMu.Unlock()
	select {
	case <-r.svc.holdingsWake:
	default:
		t.Fatal("the worker was not woken")
	}
	if !all {
		t.Error("the schedule asks for every title")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.svc.RunHoldings(ctx); close(done) }()
	_, eps := track(t, r.svc, 4711, "Show Name", 1, false)
	r.api.set(func(h *holdingsAPI) {
		h.series["tmdb:4711"] = season1(katalog.EpisodeHolding{Episode: 1, ItemID: "e1", HasFile: true})
	})
	r.svc.RequestReconcile()
	deadline := time.Now().Add(5 * time.Second)
	for target(t, r.svc, eps[0].ID).State != store.TargetHeld {
		if time.Now().After(deadline) {
			t.Fatal("the worker did not reconcile")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
}
