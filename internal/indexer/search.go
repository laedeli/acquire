package indexer

import (
	"context"
	"sort"
	"sync"

	"github.com/laedeli/acquire/internal/release"
)

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
	release.Release
	Match Match
	Stage string // id | coords | text — how it was found, for the console
}

// Engine runs a target against a fleet of search sources.
type Engine struct {
	// Ask sends one query to one source. The caller supplies it because the
	// caller owns what surrounds a request: the concurrency bound, the daily
	// allowance, the timeout and the source's health bookkeeping.
	//
	// The bound is not a performance knob: three unbounded capability sweeps
	// during planning were enough to rate-limit the single most important
	// source into hard failure, twice. Quota is the scarce resource, not time.
	Ask func(ctx context.Context, src Source, q Query) ([]release.Release, error)
}

// Search runs the escalation: precise first, broad only if precise found
// nothing usable.
//
// The stages exist because a zero-result typed query is INCONCLUSIVE — an
// id-search to a source without id support returns 200 with zero items and no
// error. So an empty stage means "ask a different way", never "it does not
// exist".
//
// Every result is identity-verified before it is returned. Without that the
// ranker will happily score a different show: it has no title term at all.
func (e *Engine) Search(ctx context.Context, t Target, fleet []Source) ([]Result, error) {
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
		if ctx.Err() != nil {
			break
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Seeders > out[j].Seeders })
	return out, nil
}

type stage struct {
	name    string
	query   Query
	targets []Source
}

// plan builds the escalation ladder for this target and fleet.
func (e *Engine) plan(t Target, fleet []Source) []stage {
	var out []stage
	movie := t.Kind == "movie"
	media := "tv"
	if movie {
		media = "movie"
	}

	// Stage 1: by id, to the sources that accept one. Most precise, and the
	// only stage that cannot match the wrong show.
	switch {
	case movie && t.IMDBID != "":
		if idFleet := filter(fleet, func(s Source) bool { return s.Caps.AcceptsMovieIMDBID() }); len(idFleet) > 0 {
			out = append(out, stage{"id", Query{Kind: "movie", IMDBID: t.IMDBID, Media: media}, idFleet})
		}
	case !movie && t.TVDBID > 0:
		if idFleet := filter(fleet, func(s Source) bool { return s.Caps.AcceptsTVDBID() }); len(idFleet) > 0 {
			out = append(out, stage{"id", Query{
				Kind: "tvsearch", TVDBID: t.TVDBID, Season: t.Season, Episode: t.Episode, Media: media,
			}, idFleet})
		}
	}

	// Stage 2: title plus coordinates. Works on far more sources and is still
	// precise enough that the wrong episode is rejected.
	if !movie && t.Season != nil {
		if coordFleet := filter(fleet, func(s Source) bool { return s.Caps.AcceptsSeasonEp() }); len(coordFleet) > 0 {
			out = append(out, stage{"coords", Query{
				Kind: "tvsearch", Term: t.Title, Season: t.Season, Episode: t.Episode, Media: media,
			}, coordFleet})
		}
	}

	// Stage 3: free text. Last resort, and the stage where identity
	// verification earns its keep — this is how you get another show.
	if len(fleet) > 0 {
		out = append(out, stage{"text", Query{Kind: "search", Term: t.Title, Media: media}, fleet})
	}
	return out
}

func (e *Engine) runStage(ctx context.Context, t Target, st stage) []Result {
	var mu sync.Mutex
	var wg sync.WaitGroup
	var out []Result

	for _, src := range st.targets {
		wg.Add(1)
		go func(src Source) {
			defer wg.Done()
			items, err := e.Ask(ctx, src, st.query)
			if err != nil {
				// One source failing is normal and must not fail the search;
				// Ask has already recorded it.
				return
			}
			mu.Lock()
			defer mu.Unlock()
			for _, it := range items {
				m := Verify(t.Title, t.Aliases, it.Title, t.Season, t.Episode, st.name == "id")
				if !m.OK {
					continue
				}
				out = append(out, Result{Release: it, Match: m, Stage: st.name})
			}
		}(src)
	}
	wg.Wait()
	return out
}

func filter(in []Source, keep func(Source) bool) []Source {
	var out []Source
	for _, s := range in {
		if keep(s) {
			out = append(out, s)
		}
	}
	return out
}
