package app

import (
	"context"
	"fmt"
	"sort"

	"github.com/laedeli/acquire/internal/indexer"
	"github.com/laedeli/acquire/internal/release"
	"github.com/laedeli/acquire/internal/store"
)

// SearchTarget finds releases for one acquisition target using TYPED queries.
//
// This is the path that replaces free-text search for anything we actually
// track. The difference is not precision-for-its-own-sake: a free-text search
// for "Breaking Bad" returned "The.Bad.Guys.Breaking.In.S02E05" as its top hit
// in a live probe, and the ranker has no title term with which to reject it.
//
// The console's manual Search stays free-text on purpose — a human typing a
// query wants exactly what they typed.
func (s *Service) SearchTarget(ctx context.Context, targetID string) ([]Candidate, error) {
	t, title, err := s.st.TargetWithTitle(ctx, targetID)
	if err != nil {
		return nil, err
	}
	if s.ix == nil || s.ix.Client == nil || !s.ix.Client.Enabled() {
		return nil, fmt.Errorf("no indexer configured")
	}
	fleet, err := s.ix.Client.Fleet(ctx)
	if err != nil {
		return nil, fmt.Errorf("indexer fleet: %w", err)
	}

	aliases, _ := s.st.AliasesFor(ctx, title.ID)
	kind := "series"
	if t.Kind == "movie" {
		kind = "movie"
	}
	results, err := s.ix.Search(ctx, indexer.Target{
		Title: title.Title, Aliases: aliases,
		TVDBID: title.TVDBID, IMDBID: title.IMDBID,
		Season: t.SeasonNumber, Episode: t.EpisodeNumber,
		Kind: kind, Year: title.Year,
	}, fleet)
	if err != nil {
		return nil, err
	}

	profile, _ := s.ScoringProfile(ctx, title.ProfileID)
	out := make([]Candidate, 0, len(results))
	for _, r := range results {
		v, in := release.Score(release.Candidate{
			Title: r.Title, Protocol: r.Protocol,
			SizeMb: r.Size / (1024 * 1024), Seeders: r.Seeders,
		}, profile)
		out = append(out, Candidate{
			Title: r.Title, Indexer: r.Indexer, Protocol: r.Protocol,
			Size: r.Size, Seeders: r.Seeders,
			Score: v.Score, Rejected: v.Rejected, Reason: v.Summary(),
			Resolution: in.Resolution, Codec: in.Codec, SourceType: in.Source,
			Source: r.Link, Adapter: adapterFor(r.Protocol),
			// How it was found and what identified it, so the console can show
			// an id match as certain rather than merely plausible.
			Stage: r.Stage, MatchedVia: r.Match.Via,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Rejected != out[j].Rejected {
			return !out[i].Rejected
		}
		return out[i].Score > out[j].Score
	})
	for i := range out {
		out[i].Best = i == 0 && !out[i].Rejected
	}
	return out, nil
}

// TargetSearchable reports whether a typed search is even possible, so the
// caller can say why rather than returning an empty list. Zero of 70 indexers
// accept a tmdbId, so a series without a tvdbId can only be searched as text.
func TargetSearchable(t store.Title) bool {
	if t.Kind == "series" {
		return t.TVDBID > 0
	}
	return t.IMDBID != "" || t.TMDBID > 0
}

func adapterFor(protocol string) string {
	if protocol == "usenet" {
		return "nzbget"
	}
	return "qbittorrent"
}
