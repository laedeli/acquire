package indexer

import (
	"context"
	"log"
	"sort"
	"sync"
	"time"
)

// Capability is what one indexer will actually answer, as advertised by the
// aggregator's caps. Advertised caps are NOT trustworthy on their own — one
// indexer advertises tvdbId and returns zero for every id query — so the engine
// treats them as a hint for ORDERING, never as proof.
type Capability struct {
	ID              int
	Name            string
	Protocol        string
	AcceptsTVDBID   bool
	AcceptsIMDBID   bool
	AcceptsSeasonEp bool
}

// Target is what we are trying to find.
type Target struct {
	Title   string
	Aliases []string
	TVDBID  int64
	IMDBID  string
	Season  *int
	Episode *int
	Kind    string // series | movie
	Year    int
}

// Result is one verified release.
type Result struct {
	Item
	Match Match
	Stage string // id | coords | text — how it was found, for the console
}

// Engine runs a target against a fleet of indexers.
type Engine struct {
	Client *Client
	// MaxConcurrent bounds the fan-out. This is not a performance knob: three
	// unbounded capability sweeps during planning were enough to rate-limit the
	// single most important indexer into hard failure, twice. Quota is the
	// scarce resource, not time.
	MaxConcurrent int
	PerIndexer    time.Duration
}

// Search runs the escalation: precise first, broad only if precise found
// nothing usable.
//
// The stages exist because a zero-result typed query is INCONCLUSIVE — an
// id-search to an indexer without id support returns 200 with zero items and no
// error. So an empty stage means "ask a different way", never "it does not
// exist".
//
// Every result is identity-verified before it is returned. Without that the
// ranker will happily score a different show: it has no title term at all.
func (e *Engine) Search(ctx context.Context, t Target, fleet []Capability) ([]Result, error) {
	stages := e.plan(t, fleet)
	var out []Result
	for _, st := range stages {
		res := e.runStage(ctx, t, st)
		out = append(out, res...)
		if len(res) > 0 {
			// A stage that produced verified results is enough. Escalating
			// further only spends quota on a broader, less precise query.
			break
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Seeders > out[j].Seeders })
	return out, nil
}

type stage struct {
	name    string
	query   Query
	targets []Capability
}

// plan builds the escalation ladder for this target and fleet.
func (e *Engine) plan(t Target, fleet []Capability) []stage {
	var out []stage
	cat := CatTV
	kind := "tvsearch"
	if t.Kind == "movie" {
		cat, kind = CatMovie, "movie"
	}

	// Stage 1: by id, to the indexers that accept one. Most precise, and the
	// only stage that cannot match the wrong show.
	if idFleet := filter(fleet, func(c Capability) bool {
		return (t.TVDBID > 0 && c.AcceptsTVDBID) || (t.IMDBID != "" && c.AcceptsIMDBID)
	}); len(idFleet) > 0 {
		out = append(out, stage{"id", Query{
			Kind: kind, TVDBID: t.TVDBID, IMDBID: t.IMDBID,
			Season: t.Season, Episode: t.Episode, Cat: cat,
		}, idFleet})
	}

	// Stage 2: title plus coordinates. Works on far more indexers and is still
	// precise enough that the wrong episode is rejected.
	if coordFleet := filter(fleet, func(c Capability) bool { return c.AcceptsSeasonEp }); len(coordFleet) > 0 && t.Season != nil {
		out = append(out, stage{"coords", Query{
			Kind: kind, Term: t.Title, Season: t.Season, Episode: t.Episode, Cat: cat,
		}, coordFleet})
	}

	// Stage 3: free text. Last resort, and the stage where identity
	// verification earns its keep — this is how you get another show.
	out = append(out, stage{"text", Query{Kind: "search", Term: t.Title, Cat: cat}, fleet})
	return out
}

func (e *Engine) runStage(ctx context.Context, t Target, st stage) []Result {
	max := e.MaxConcurrent
	if max <= 0 {
		max = 4
	}
	sem := make(chan struct{}, max)
	var mu sync.Mutex
	var wg sync.WaitGroup
	var out []Result

	for _, cap := range st.targets {
		wg.Add(1)
		go func(c Capability) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			cctx := ctx
			if e.PerIndexer > 0 {
				var cancel context.CancelFunc
				cctx, cancel = context.WithTimeout(ctx, e.PerIndexer)
				defer cancel()
			}
			items, err := e.Client.Search(cctx, c.ID, st.query)
			if err != nil {
				// One indexer failing is normal and must not fail the search.
				log.Printf("acquire search: %s (%s): %v", c.Name, st.name, err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			for _, it := range items {
				it.Indexer = c.Name
				m := Verify(t.Title, t.Aliases, it.Title, t.Season, t.Episode, st.name == "id")
				if !m.OK {
					continue
				}
				out = append(out, Result{Item: it, Match: m, Stage: st.name})
			}
		}(cap)
	}
	wg.Wait()
	return out
}

func filter(in []Capability, keep func(Capability) bool) []Capability {
	var out []Capability
	for _, c := range in {
		if keep(c) {
			out = append(out, c)
		}
	}
	return out
}
