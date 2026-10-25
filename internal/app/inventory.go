package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

	"github.com/laedeli/acquire/internal/release"
	"github.com/laedeli/acquire/internal/store"
	"github.com/laedeli/acquire/internal/tmdb"
	"github.com/laedeli/acquire/internal/tvmaze"
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
//
// With search.airTimes, a series read is followed by a read of its air times
// at the schedule source (readSchedule): how it airs, the moment each episode
// airs and how long it runs. A scheduled run then reads the air times still
// due of every series whose air times matter (readSchedules), at the source's
// pace and outside TMDB's budget — up to scheduleCalls calls —, so the first
// runs after air times are switched on read every series that runs, the ones
// with an episode near its air date first; a series whose air times were
// never read is tried again every followRetry while the source does not
// answer. Until they are read, nothing of it is grabbed on its own.

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
	// scheduleEvery: the air times of a series that does not run are read
	// again monthly; scheduleUnknownAgain: a show the schedule source does
	// not know is looked up again after a week.
	scheduleEvery        = 30 * 24 * time.Hour
	scheduleUnknownAgain = 7 * 24 * time.Hour
	// scheduleCalls bounds the calls to the schedule source one run makes:
	// at TVmaze's pace, under 20 minutes of the run's 30.
	scheduleCalls = 2000
)

// scheduleEveryOf is how often a series' air times are read again, as
// scheduleDue says.
var scheduleEveryOf = store.ScheduleEvery{Running: runningEvery, Unknown: scheduleUnknownAgain, Other: scheduleEvery}

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
	// Scheduled are the series whose air times were read, in ScheduleCalls
	// calls to the schedule source; ScheduleError is why it stopped being
	// asked this run, ScheduleLeft set when the run's calls to it were spent
	// with air times still due.
	Scheduled     int    `json:"scheduled"`
	ScheduleCalls int    `json:"scheduleCalls"`
	ScheduleError string `json:"scheduleError,omitempty"`
	ScheduleLeft  bool   `json:"scheduleLeft,omitempty"`
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
// followRetry the follows TMDB could not be read for yet, and the air times
// TVmaze did not give.
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
			s.retrySchedules(ctx)
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
	ids, err := s.st.RequestedNotRead(ctx)
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

// retrySchedules reads the air times of the series never read because the
// schedule source did not answer when it was asked, now that it may.
func (s *Service) retrySchedules(ctx context.Context) {
	if s.st == nil || !s.grabPolicy(ctx).exactAirTimes() {
		return
	}
	var run InventoryRun
	s.readSchedules(ctx, &run, true)
	if run.Scheduled > 0 || run.ScheduleError != "" {
		line := fmt.Sprintf("acquire inventory-refresh: the air times of %d series read again", run.Scheduled)
		if run.ScheduleError != "" {
			line += "; the schedule source still does not answer: " + run.ScheduleError
		}
		log.Print(line)
	}
	if run.Scheduled > 0 {
		s.notify()
	}
}

// readSchedules reads the air times due (store.SchedulesDue) — of the series
// never read because the source did not answer, with failed —, the ones never
// read first, until the run has made scheduleCalls calls to the source or it
// stops answering.
func (s *Service) readSchedules(ctx context.Context, run *InventoryRun, failed bool) {
	due, err := s.st.SchedulesDue(ctx, scheduleEveryOf, scheduleCalls, failed)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("acquire inventory-refresh: the air times due: %v", err)
		}
		return
	}
	for _, t := range due {
		switch {
		case ctx.Err() != nil, run.ScheduleError != "":
			return
		case run.ScheduleCalls+2 > scheduleCalls:
			run.ScheduleLeft = true
			return
		}
		if !s.readScheduleOf(ctx, t, run) {
			return
		}
	}
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
	schedule := s.grabPolicy(ctx).exactAirTimes() && s.sched.Enabled()
	var reconcile []string
	defer func() {
		if len(reconcile) > 0 {
			// New targets are of unknown holdings until the catalog says
			// what the library holds of them.
			s.RequestReconcile(reconcile...)
		}
		if run.Titles > 0 || run.Failed > 0 || run.Error != "" || run.Scheduled > 0 || run.ScheduleError != "" {
			s.notify()
			line := fmt.Sprintf("acquire inventory-refresh: %d title(s) read (%d followed), %d TMDB call(s), %d failed; "+
				"%d target(s) added (%d aired), %d updated, %d renumbered; %d no longer listed (removed once a read a day later agrees), "+
				"%d removed, %d kept as they hold something; the air times of %d series read (%d call(s) to the schedule source)",
				run.Titles, run.Followed, run.Calls, run.Failed, run.Added, run.AddedAired, run.Updated, run.Renumbered, run.Gone,
				run.Removed, run.Kept, run.Scheduled, run.ScheduleCalls)
			if run.ScheduleError != "" {
				line += "; the schedule source stopped answering: " + run.ScheduleError
			}
			if run.Left {
				line += "; the run's budget is spent, what is still due is read on the next runs"
			}
			if run.ScheduleLeft {
				line += "; the run's calls to the schedule source are spent, the air times still due are read on the next runs"
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
		if schedule && t.Kind == "series" && !headOnly(t) {
			// An episode TMDB added or moved is read at TVmaze at once:
			// moved, it is held until it is.
			schedule = s.readSchedule(ctx, t.ID, &run, ch.Added > 0 || ch.Updated > 0)
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
	err := func() error {
		if !follows() || !scheduled {
			return nil
		}
		movies, err := s.st.MovieInventoryDue(ctx, waitingEvery, budget)
		if err != nil {
			return err
		}
		for _, t := range movies {
			if budget <= 0 {
				run.Left = true
				return nil
			}
			if !follows() || !read(t, t.InventoryAt == nil) {
				return nil
			}
		}
		if budget <= 0 {
			run.Left = true
			return nil
		}
		// The series that run, but for a third of the budget, which the others
		// — ended, or never read — get; what either leaves goes to the series
		// that run.
		running, err := s.st.SeriesInventoryDue(ctx, runningEvery, endedEvery, budget+1, store.SeriesRunning)
		if err != nil {
			return err
		}
		others, err := s.st.SeriesInventoryDue(ctx, runningEvery, endedEvery, budget+1, store.SeriesOther)
		if err != nil {
			return err
		}
		reserve := s.inventoryBudget * inventoryReserve / 3
		next := 0
		readSeries := func(d store.DueTitle) bool { return follows() && read(d.Title, d.Title.InventoryAt == nil) }
		for ; next < len(running) && budget > reserve; next++ {
			if !readSeries(running[next]) {
				return nil
			}
		}
		for _, d := range others {
			if budget <= 0 {
				run.Left = true
				return nil
			}
			if !readSeries(d) {
				return nil
			}
		}
		for ; next < len(running); next++ {
			if budget <= 0 {
				run.Left = true
				return nil
			}
			if !readSeries(running[next]) {
				return nil
			}
		}
		return nil
	}()
	// The air times still due, of every series whose air times matter,
	// outside TMDB's budget.
	if scheduled && schedule && ctx.Err() == nil {
		s.readSchedules(ctx, &run, false)
		if st, err := s.AirTimesStatus(ctx); err == nil && st.Warning != "" {
			log.Printf("acquire inventory-refresh: %s", st.Warning)
		}
	}
	return run, err
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
		country := ""
		if kind != "theatrical" {
			country = m.HomeCountry()
		}
		target, err := s.st.ApplyMovieInventory(ctx, t.ID, store.MovieRead{
			IMDBID: m.IMDBID, Name: m.Title, Year: m.Year(), Status: m.Status, Aliases: m.Aliases, Countries: m.Countries,
			Window: window, Released: released, Kind: kind, Country: country,
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
	if headOnly(t) {
		// A request awaiting approval reads what TMDB says of the series,
		// and makes nothing of its seasons or episodes.
		return ch, false, s.st.ApplySeriesHead(ctx, t.ID, store.SeriesRead{
			TVDBID: head.TVDBID, IMDBID: head.IMDBID, Name: head.Name, Year: head.Year(),
			Status: head.Status, Running: tmdb.Running(head.Status), Aliases: head.Aliases, Countries: head.Countries,
			Genres: head.Genres, Type: head.Type, Seasons: listed,
		})
	}
	toRead, err := s.st.SeasonsToRead(ctx, t.ID, listed, full || t.InventoryAt == nil)
	if err != nil {
		return ch, false, err
	}
	r := store.SeriesRead{
		TVDBID: head.TVDBID, IMDBID: head.IMDBID, Name: head.Name, Year: head.Year(),
		Status: head.Status, Running: tmdb.Running(head.Status), Aliases: head.Aliases, Countries: head.Countries,
		Genres: head.Genres, Type: head.Type,
		Seasons: listed, Read: toRead, MonitorAll: t.Followed() && t.InventoryAt == nil,
		AirTimes: s.grabPolicy(ctx).exactAirTimes(),
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

// readSchedule reads a series' air times at the schedule source when they
// are due (scheduleDue) — or at once, with now: a read of TMDB added an
// episode or moved one —, as readScheduleOf does.
func (s *Service) readSchedule(ctx context.Context, titleID string, run *InventoryRun, now bool) bool {
	t, err := s.st.TitleByID(ctx, titleID)
	if err != nil || (!now && !scheduleDue(t, time.Now())) {
		return true
	}
	return s.readScheduleOf(ctx, t, run)
}

// readScheduleOf reads a series' air times at the schedule source: its show
// id, looked up once by the series' TVDB id or IMDb id — nothing else is
// sent —, then the show's record with its episodes in one call — how it
// airs, broadcast or streamed, its usual hour, and the moment each episode
// airs and how long it runs —, made into what each episode target is told by
// (planSchedule). A show whose name is neither the series' nor one of its
// aliases is another show behind the same id: none of its air times is
// taken, and it is looked up again in a week. It reports false when the
// source does not answer as it should — out of reach, failing, limiting,
// refusing —: no other series would fare better this run. An answer that
// cannot be read leaves the series unread, to be read again, and the run goes
// on. Nothing fails of the series either way.
func (s *Service) readScheduleOf(ctx context.Context, t store.Title, run *InventoryRun) bool {
	if t.TVDBID == 0 && t.IMDBID == "" {
		_ = s.st.ScheduleFailed(ctx, t.ID, "no TVDB or IMDb id to look it up at TVmaze by; looked up again in a week", store.ScheduleUnknown)
		return true
	}
	fail := func(err error, what string) bool {
		switch {
		case errors.Is(err, tvmaze.ErrNotFound):
			_ = s.st.ScheduleFailed(ctx, t.ID, "TVmaze does not know "+what+"; looked up again in a week", store.ScheduleUnknown)
			return true
		case errors.Is(err, tvmaze.ErrUnreadable):
			log.Printf("acquire inventory-refresh: the air times of %s: %v", t.Title, err)
			_ = s.st.ScheduleFailed(ctx, t.ID, err.Error()+"; read again on the next refresh", store.ScheduleDown)
			return true
		}
		// TVmaze not answering as it should (tvmaze.Unavailable): out of
		// reach, failing, limiting, refusing.
		run.ScheduleError = err.Error()
		_ = s.st.ScheduleFailed(ctx, t.ID, "TVmaze did not answer: "+err.Error(), store.ScheduleDown)
		return false
	}
	id := t.ScheduleID
	if id == 0 {
		run.ScheduleCalls++
		var err error
		if id, err = s.sched.LookupShow(ctx, t.TVDBID, t.IMDBID); err != nil {
			return fail(err, "it by its TVDB or IMDb id")
		}
	}
	run.ScheduleCalls++
	show, err := s.sched.Show(ctx, id)
	if err != nil {
		return fail(err, fmt.Sprintf("its show %d any more", id))
	}
	if aliases, err := s.st.AliasesFor(ctx, t.ID); err == nil && !sameShow(show.Name, t.Title, aliases) {
		log.Printf("acquire inventory-refresh: %s: TVmaze's show %d by its TVDB or IMDb id is %q, another show — its air times are not taken",
			t.Title, id, show.Name)
		_ = s.st.ScheduleFailed(ctx, t.ID, fmt.Sprintf("TVmaze's show %d by its TVDB or IMDb id is %q, not this series: its air times are not taken; "+
			"looked up again in a week", id, show.Name), store.ScheduleOtherShow)
		return true
	}
	targets, err := s.st.TitleTargets(ctx, t.ID)
	if err != nil {
		log.Printf("acquire inventory-refresh: the air times of %s: %v", t.Title, err)
		return true
	}
	rec := store.ScheduledShow{ID: id, Country: show.Country, Targets: planSchedule(t, show, targets, time.Now())}
	switch show.Kind {
	case tvmaze.Broadcast:
		rec.Kind = store.ScheduleBroadcast
	case tvmaze.Streaming:
		rec.Kind = store.ScheduleStreaming
	}
	if _, err := s.st.ApplySchedule(ctx, t.ID, rec); err != nil {
		log.Printf("acquire inventory-refresh: the air times of %s: %v", t.Title, err)
		return true
	}
	run.Scheduled++
	return true
}

// sameShow reports whether the schedule source's name of a show is the
// series' — its title or one of its aliases, compared as release names are
// (release.MatchKey), without a qualifier in brackets at the end ("(US)",
// "(2005)") or a leading "the" —, or holds it, or is held by it, whole words
// of at least two.
func sameShow(name, title string, aliases []string) bool {
	key := func(s string) string {
		s = strings.TrimSpace(s)
		if i := strings.LastIndex(s, "("); i > 0 && strings.HasSuffix(s, ")") {
			s = s[:i]
		}
		return strings.TrimPrefix(release.MatchKey(s), "the ")
	}
	n := key(name)
	if n == "" {
		return true
	}
	for _, a := range append([]string{title}, aliases...) {
		k := key(a)
		if k == "" {
			continue
		}
		if k == n {
			return true
		}
		short, long := k, n
		if len(short) > len(long) {
			short, long = long, short
		}
		if strings.Contains(short, " ") && strings.Contains(" "+long+" ", " "+short+" ") {
			return true
		}
	}
	return false
}

// scheduleDue reports whether a series' air times are read now: never read,
// or read by an earlier version of the rules (store.ScheduleVersion); of a
// series that runs, on every read of it; of any other, monthly; a show the
// schedule source did not know, a week after it was looked up.
func scheduleDue(t store.Title, now time.Time) bool {
	switch {
	case t.ScheduleAt == nil, t.ScheduleVersion < store.ScheduleVersion:
		return true
	case t.ScheduleID == 0:
		return now.Sub(*t.ScheduleAt) >= scheduleUnknownAgain
	case tmdb.Running(t.TMDBStatus):
		return now.Sub(*t.ScheduleAt) >= runningEvery
	}
	return now.Sub(*t.ScheduleAt) >= scheduleEvery
}

// headOnly reports whether a read of a series makes nothing of its seasons
// and episodes: nobody monitors it and no approved request follows it — a
// request awaiting an admin's approval asks for it.
func headOnly(t store.Title) bool { return t.Kind == "series" && !t.Monitored && !t.Followed() }

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
