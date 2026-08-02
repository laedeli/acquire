package app

import (
	"context"
	"fmt"
	"time"

	"github.com/laedeli/acquire/internal/importer"
	"github.com/laedeli/acquire/internal/store"
	"github.com/laedeli/acquire/internal/tmdb"
)

// ImportResult is what an import did, in terms an operator can check against
// the system it came from.
type ImportResult struct {
	DryRun       bool     `json:"dryRun"`
	Titles       int      `json:"titles"`
	Monitored    int      `json:"monitored"`
	Aliases      int      `json:"aliases"`
	Skipped      []string `json:"skipped"`      // no tmdb id: cannot be a row at all
	Unresolvable []string `json:"unresolvable"` // no tvdb/imdb id: cannot be typed-searched
	Errors       []string `json:"errors"`
}

// ImportIntent pulls tracked titles from an incumbent service and writes them
// as intent.
//
// Deliberately an explicit operation rather than a boot backfill: a one-way
// data change on a startup path turns a data problem into an outage class, and
// there is no version row to skip past when it goes wrong.
//
// Safe to re-run. Ids are derived from identity, so a second import updates the
// same rows.
func (s *Service) ImportIntent(ctx context.Context, src importer.Source, dryRun bool) (ImportResult, error) {
	res := ImportResult{DryRun: dryRun}
	titles, err := src.Import(ctx)
	if err != nil {
		return res, err
	}
	rep := importer.Summarise(titles)
	res.Unresolvable = rep.Unresolvable
	res.Skipped = rep.MissingTMDBID

	for _, t := range titles {
		if t.TMDBID == 0 {
			continue // already reported in Skipped; a row needs an identity
		}
		res.Titles++
		if t.Monitored {
			res.Monitored++
		}
		res.Aliases += len(t.Aliases)
		if dryRun {
			continue
		}
		id := store.TitleID(t.Kind, t.TMDBID)
		if err := s.st.UpsertTitle(ctx, store.Title{
			ID: id, TMDBID: t.TMDBID, TVDBID: t.TVDBID, IMDBID: t.IMDBID,
			Kind: t.Kind, Title: t.Title, SortTitle: t.SortTitle, Year: t.Year,
			Status: t.Status, SeriesType: t.SeriesType,
			Monitored: t.Monitored, MonitorNew: t.MonitorNew,
		}); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", t.Title, err))
			continue
		}
		if len(t.Aliases) > 0 {
			if err := s.st.ReplaceAliases(ctx, id, t.Aliases, "incumbent"); err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("%s aliases: %v", t.Title, err))
			}
		}
	}
	return res, nil
}

// InventoryResult reports an episode derivation.
type InventoryResult struct {
	DryRun   bool     `json:"dryRun"`
	Series   int      `json:"series"`
	Episodes int      `json:"episodes"`
	Aired    int      `json:"aired"`
	Failed   []string `json:"failed"`
}

// DeriveInventory turns tracked series into acquisition targets using TMDB.
//
// Episodes are derived, never migrated: a derivation can be re-run and checked
// against the source, a copy cannot. A series whose derivation fails is reported
// and SKIPPED rather than partially written — a half-written inventory is
// indistinguishable from genuinely missing episodes, which is exactly the
// mistake that produced a wrong "48.6% unresolvable" figure during planning.
func (s *Service) DeriveInventory(ctx context.Context, tm *tmdb.Client, limit int, dryRun bool) (InventoryResult, error) {
	res := InventoryResult{DryRun: dryRun}
	titles, err := s.st.TitlesOfKind(ctx, "series", true)
	if err != nil {
		return res, err
	}
	now := time.Now()
	for i, t := range titles {
		if limit > 0 && i >= limit {
			break
		}
		inv, err := tm.SeriesInventory(ctx, t.TMDBID)
		if err != nil {
			res.Failed = append(res.Failed, fmt.Sprintf("%s: %v", t.Title, err))
			continue
		}
		res.Series++
		res.Episodes += len(inv.Episodes)
		for _, e := range inv.Episodes {
			if e.Aired(now) {
				res.Aired++
			}
		}
		if dryRun {
			continue
		}
		for season, count := range inv.SeasonInfo {
			if err := s.st.UpsertSeason(ctx, t.ID, season, count); err != nil {
				res.Failed = append(res.Failed, fmt.Sprintf("%s season %d: %v", t.Title, season, err))
			}
		}
		for _, e := range inv.Episodes {
			season, ep := e.SeasonNumber, e.EpisodeNumber
			id := store.TargetID(t.ID, &season, &ep)
			if err := s.st.UpsertTarget(ctx, store.Target{
				ID: id, TitleID: t.ID, Kind: "episode",
				SeasonNumber: &season, EpisodeNumber: &ep,
				Monitored: true, State: "wanted",
			}); err != nil {
				res.Failed = append(res.Failed, fmt.Sprintf("%s S%02dE%02d: %v", t.Title, season, ep, err))
				continue
			}
			if err := s.st.SetAirWindow(ctx, id, e.AirDate, 0); err != nil {
				res.Failed = append(res.Failed, fmt.Sprintf("%s S%02dE%02d air: %v", t.Title, season, ep, err))
			}
		}
	}
	return res, nil
}
