package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

	"github.com/laedeli/acquire/internal/store"
	"github.com/laedeli/acquire/internal/tmdb"
)

// Reading TMDB for what acquire tracks.
//
// The episodes acquire hunts are TMDB's. The import's derivation read them
// once; this reads them again — on the inventory-refresh schedule, and at
// once for a title a request follows — so what TMDB announces later becomes
// a target too: a new season, an episode added to one, a date set or moved,
// an episode's title. An episode TMDB no longer lists goes once a read a day
// or more later agrees, unless it holds anything (store.ApplySeriesInventory).
//
// A series that runs (TMDB says it returns, is in production or planned, or
// an episode aired in the last 60 days or airs in the next 14) is read on
// every run; one that ended at most weekly; a requested movie that waits for
// its release daily, for the date TMDB names. Of a series read before, only
// the seasons that can have changed are read (store.SeasonsToRead).
//
// The library is thousands of calls to read whole, so a run is paced: it
// starts no title once it has spent inventoryBudget calls, and the rest are
// due on the next runs — the follows first, then what runs, a third of the
// budget kept for the series that ended or were never read. A follow TMDB
// could not be read for is read again every followRetry until it is.

const (
	// inventoryBudget bounds the TMDB calls one run starts titles with (the
	// service's inventoryBudget, which tests lower).
	inventoryBudget = 300
	// runningEvery is how long a series that runs goes without a read:
	// just under the schedule's six hours, so every run reads it.
	runningEvery = 5*time.Hour + 30*time.Minute
	// endedEvery: a series that ended is read at most weekly.
	endedEvery = 7 * 24 * time.Hour
	// waitingEvery: a requested movie waiting for its release, daily.
	waitingEvery = 20 * time.Hour
	// theatricalWait: a requested movie TMDB dates no release to watch at
	// home for is searched this long after it came to cinemas.
	theatricalWait = 90 * 24 * time.Hour
	// inventoryRunTime bounds one run.
	inventoryRunTime = 30 * time.Minute
	// followRetry is how soon a follow whose read of TMDB failed — TMDB out
	// of reach or failing — is read again, until it is read.
	followRetry = 15 * time.Minute
	// inventoryReserve is the part of a run's budget, in thirds, kept for
	// the series that do not run — ended, or never read —, so a library of
	// running series does not keep them waiting for ever.
	inventoryReserve = 1
)

// noTMDB is why nothing can be read without a TMDB key.
const noTMDB = "TMDB is not configured (TMDB_API_KEY): a followed title's episodes and release dates cannot be read"

// InventoryRun is what one run read and changed.
type InventoryRun struct {
	Titles   int `json:"titles"`
	Followed int `json:"followed"`
	Calls    int `json:"calls"`
	Failed   int `json:"failed"`
	store.InventoryChange
	// Left is set when the run stopped at its budget: what is still due is
	// read on the next runs.
	Left  bool   `json:"left"`
	Error string `json:"error,omitempty"`
}

// RequestInventory asks the worker to read TMDB: for the titles named first
// and whole — a request follows them —, or, with none named, a scheduled run
// of what is due. Requests that come while one runs are taken next.
func (s *Service) RequestInventory(titleIDs ...string) {
	s.inventoryMu.Lock()
	if len(titleIDs) == 0 {
		s.inventoryRun = true
	}
	for _, id := range titleIDs {
		s.inventoryIDs[id] = true
	}
	s.inventoryMu.Unlock()
	select {
	case s.inventoryWake <- struct{}{}:
	default:
	}
}

// takeFollows takes the titles asked to be read first.
func (s *Service) takeFollows() []string {
	s.inventoryMu.Lock()
	defer s.inventoryMu.Unlock()
	ids := make([]string, 0, len(s.inventoryIDs))
	for id := range s.inventoryIDs {
		ids = append(ids, id)
	}
	s.inventoryIDs = map[string]bool{}
	return ids
}

func (s *Service) takeScheduled() bool {
	s.inventoryMu.Lock()
	defer s.inventoryMu.Unlock()
	run := s.inventoryRun
	s.inventoryRun = false
	return run
}

// RunInventory reads TMDB as it is asked to until ctx ends, and every
// followRetry the follows TMDB could not be read for yet.
func (s *Service) RunInventory(ctx context.Context) {
	if s.st == nil {
		return
	}
	retry := time.NewTicker(followRetry)
	defer retry.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.inventoryWake:
		case <-retry.C:
			if !s.retryFollows(ctx) {
				continue
			}
		}
		rctx, cancel := context.WithTimeout(ctx, inventoryRunTime)
		if _, err := s.RefreshInventory(rctx, s.takeScheduled()); err != nil && ctx.Err() == nil {
			log.Printf("acquire inventory-refresh: %v", err)
		}
		cancel()
	}
}

// retryFollows asks for the follows TMDB could not be read for yet to be
// read, and reports whether there are any. Without a TMDB key there is
// nothing to try again.
func (s *Service) retryFollows(ctx context.Context) bool {
	if !s.tm.Enabled() {
		return false
	}
	ids, err := s.st.FollowsNotRead(ctx)
	if err != nil || len(ids) == 0 {
		return false
	}
	s.inventoryMu.Lock()
	for _, id := range ids {
		s.inventoryIDs[id] = true
	}
	s.inventoryMu.Unlock()
	return true
}

// tmdbDown is TMDB not answering as it should — out of reach, failing,
// limiting, refusing the key: no other title would fare better this run.
type tmdbDown struct{ error }

func (e tmdbDown) Unwrap() error { return e.error }

// RefreshInventory reads TMDB for the titles requests follow that were asked
// for (RequestInventory), each whole, and — scheduled — for what is due,
// within the run's budget. A title TMDB does not know is recorded as failed
// and read again after its interval; TMDB not answering ends the run, the
// titles still due.
func (s *Service) RefreshInventory(ctx context.Context, scheduled bool) (InventoryRun, error) {
	var run InventoryRun
	if !s.tm.Enabled() {
		follows := s.takeFollows()
		for _, id := range follows {
			_ = s.st.InventoryFailed(ctx, id, noTMDB, false)
		}
		if len(follows) > 0 {
			s.notify()
		}
		if scheduled || len(follows) > 0 {
			run.Error = noTMDB
			return run, errors.New(noTMDB)
		}
		return run, nil
	}
	budget := s.inventoryBudget
	spend := func() {
		budget--
		run.Calls++
	}
	var reconcile []string
	defer func() {
		if len(reconcile) > 0 {
			// New targets are of unknown holdings until the catalog says
			// what the library holds of them.
			s.RequestReconcile(reconcile...)
		}
		if run.Titles > 0 || run.Failed > 0 || run.Error != "" {
			s.notify()
			line := fmt.Sprintf("acquire inventory-refresh: %d title(s) read (%d followed), %d TMDB call(s), %d failed; "+
				"%d target(s) added (%d aired), %d updated, %d renumbered; %d no longer listed (removed once a read a day later agrees), "+
				"%d removed, %d kept as they hold something",
				run.Titles, run.Followed, run.Calls, run.Failed, run.Added, run.AddedAired, run.Updated, run.Renumbered, run.Gone,
				run.Removed, run.Kept)
			if run.Left {
				line += "; the run's budget is spent, what is still due is read on the next runs"
			}
			if run.Error != "" {
				line += "; stopped: " + run.Error
			}
			log.Print(line)
		}
	}()

	// read reads one title and records what came of it. It reports false
	// when TMDB does not answer and the run is to stop.
	read := func(t store.Title, full bool) bool {
		ch, unknown, err := s.readTitle(ctx, t, full, spend)
		var down tmdbDown
		switch {
		case ctx.Err() != nil:
			return false
		case errors.As(err, &down):
			run.Error = err.Error()
			_ = s.st.InventoryFailed(ctx, t.ID, "TMDB did not answer: "+err.Error(), false)
			return false
		case err != nil:
			run.Failed++
			_ = s.st.InventoryFailed(ctx, t.ID, err.Error(), true)
			log.Printf("acquire inventory-refresh: %s: %v", t.Title, err)
			return true
		}
		run.Titles++
		run.Added += ch.Added
		run.AddedAired += ch.AddedAired
		run.Updated += ch.Updated
		run.Gone += ch.Gone
		run.Removed += ch.Removed
		run.Kept += ch.Kept
		run.Renumbered += ch.Renumbered
		if unknown {
			reconcile = append(reconcile, t.ID)
		}
		return true
	}
	// follows reads the titles requests asked for, first and whole. Those
	// not read because TMDB does not answer say so, and are due first on the
	// next run.
	follows := func() bool {
		ids := s.takeFollows()
		for i, id := range ids {
			t, err := s.st.TitleByID(ctx, id)
			if err != nil {
				continue
			}
			run.Followed++
			if !read(t, true) {
				for _, rest := range ids[i+1:] {
					if run.Error != "" {
						_ = s.st.InventoryFailed(ctx, rest, "TMDB did not answer: "+run.Error, false)
					}
				}
				return false
			}
		}
		return true
	}
	if !follows() || !scheduled {
		return run, nil
	}

	movies, err := s.st.MovieInventoryDue(ctx, waitingEvery, budget)
	if err != nil {
		return run, err
	}
	for _, t := range movies {
		if budget <= 0 {
			run.Left = true
			return run, nil
		}
		if !follows() || !read(t, t.InventoryAt == nil) {
			return run, nil
		}
	}
	if budget <= 0 {
		run.Left = true
		return run, nil
	}
	// The series that run, but for a third of the budget, which the others
	// — ended, or never read — get; what either leaves goes to the series
	// that run.
	running, err := s.st.SeriesInventoryDue(ctx, runningEvery, endedEvery, budget+1, store.SeriesRunning)
	if err != nil {
		return run, err
	}
	others, err := s.st.SeriesInventoryDue(ctx, runningEvery, endedEvery, budget+1, store.SeriesOther)
	if err != nil {
		return run, err
	}
	reserve := s.inventoryBudget * inventoryReserve / 3
	next := 0
	readSeries := func(d store.DueTitle) bool { return follows() && read(d.Title, d.Title.InventoryAt == nil) }
	for ; next < len(running) && budget > reserve; next++ {
		if !readSeries(running[next]) {
			return run, nil
		}
	}
	for _, d := range others {
		if budget <= 0 {
			run.Left = true
			return run, nil
		}
		if !readSeries(d) {
			return run, nil
		}
	}
	for ; next < len(running); next++ {
		if budget <= 0 {
			run.Left = true
			return run, nil
		}
		if !readSeries(running[next]) {
			return run, nil
		}
	}
	return run, nil
}

// readTitle reads one title from TMDB and applies it: a series' answer and
// the seasons that can have changed (all of them, full), a movie's answer.
// spend counts each call. Nothing is applied of a series one of whose
// seasons could not be read: a short inventory looks like a complete one.
// unknown reports a target the read left of unknown holdings — a new one, or
// a title never asked of the catalog —: the catalog is asked next.
func (s *Service) readTitle(ctx context.Context, t store.Title, full bool, spend func()) (ch store.InventoryChange, unknown bool, err error) {
	if t.Kind == "movie" {
		spend()
		m, err := s.tm.Movie(ctx, t.TMDBID)
		if err != nil {
			return ch, false, tmdbError(err)
		}
		window, released, kind := movieWindow(m)
		target, err := s.st.ApplyMovieInventory(ctx, t.ID, store.MovieRead{
			IMDBID: m.IMDBID, Name: m.Title, Year: m.Year(), Status: m.Status, Aliases: m.Aliases, Countries: m.Countries,
			Window: window, Released: released, Kind: kind,
		})
		if err != nil {
			return ch, false, err
		}
		unknown = t.InventoryAt == nil ||
			(target.AddedAt != nil && (!t.HoldingsKnown() || target.AddedAt.After(*t.HoldingsAt)))
		return ch, unknown, nil
	}
	spend()
	head, err := s.tm.Series(ctx, t.TMDBID)
	if err != nil {
		return ch, false, tmdbError(err)
	}
	listed := map[int]int{}
	for _, season := range head.Seasons {
		listed[season.Number] = season.EpisodeCount
	}
	toRead, err := s.st.SeasonsToRead(ctx, t.ID, listed, full || t.InventoryAt == nil)
	if err != nil {
		return ch, false, err
	}
	r := store.SeriesRead{
		TVDBID: head.TVDBID, IMDBID: head.IMDBID, Name: head.Name, Year: head.Year(),
		Status: head.Status, Running: tmdb.Running(head.Status), Aliases: head.Aliases, Countries: head.Countries,
		Seasons: listed, Read: toRead, MonitorAll: t.Followed() && t.InventoryAt == nil,
	}
	for _, n := range toRead {
		spend()
		eps, err := s.tm.Season(ctx, t.TMDBID, n)
		if err != nil {
			return ch, false, tmdbError(fmt.Errorf("season %d: %w", n, err))
		}
		for _, e := range eps {
			r.Episodes = append(r.Episodes, store.EpisodeRead{TMDBID: e.TMDBID, Season: e.SeasonNumber, Episode: e.EpisodeNumber,
				Title: e.Title, AirDate: e.AirDate})
		}
	}
	ch, err = s.st.ApplySeriesInventory(ctx, t.ID, r)
	return ch, err == nil && (ch.Added > 0 || t.InventoryAt == nil), err
}

// tmdbError says what a failed call to TMDB means for the run. TMDB out of
// reach, failing (5xx), limiting (429) or refusing the key (401) is TMDB's
// fault, and ends the run. A title TMDB does not know (404), or an answer
// that cannot be read, is the title's: it is read again after its interval.
func tmdbError(err error) error {
	var he *tmdb.HTTPError
	var ue *url.Error
	switch {
	case errors.As(err, &he) && he.NotFound():
		return fmt.Errorf("TMDB does not know it (%s)", strings.TrimSpace(he.Path))
	case errors.As(err, &he) && (he.Status == 401 || he.Status == 429 || he.Status >= 500):
		return tmdbDown{err}
	case errors.As(err, &ue), errors.Is(err, context.DeadlineExceeded):
		return tmdbDown{err}
	}
	return err
}

// movieWindow is when a requested movie is searched from: its first release
// to watch at home, as TMDB dates it — digital, physical or TV —; when TMDB
// dates none, theatricalWait after it came to cinemas; nil while TMDB dates
// nothing at all. released is the day it came out as kind says: at home, or
// in cinemas.
func movieWindow(m tmdb.Movie) (window, released *time.Time, kind string) {
	if at, kind, ok := m.HomeRelease(); ok {
		return &at, &at, kind
	}
	if at, ok := m.TheatricalRelease(); ok {
		w := at.Add(theatricalWait)
		return &w, &at, "theatrical"
	}
	return nil, nil, ""
}
