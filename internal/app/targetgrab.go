package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/laedeli/acquire/internal/endpoint"
	"github.com/laedeli/acquire/internal/events"
	"github.com/laedeli/acquire/internal/release"
	"github.com/laedeli/acquire/internal/store"
)

// Grabs bound to targets.
//
// A target grab is one release handed to a download client for the targets
// it covers: one episode, a file of several episodes, a season pack, or a
// movie. It comes from the sweep (with search.autoGrab), from the console's
// Missing tab (a search and grab per episode or in bulk, done by a worker one
// at a time), from a pick among a target's typed search results, or from a
// manual search result that names episodes of a tracked series.
//
// Each covered target walks
//
//	wanted → grabbed → downloaded → held
//
// or ends failed, with the reason in its detail; waiting is a downloaded
// target the catalog does not take yet (it has no ingest by identity). Only
// its release fails a target — its download failed, the download held no
// file for it, the catalog refused the file — and such a target is wanted
// again after a wait, six hours doubling to two days, the releases that
// failed it blocked, until it failed five times (grab-recovery). A grab that
// cannot happen, a cancelled download and a grab nothing is heard of give the
// targets back as wanted. The download's files go into the catalog by
// identity (targetimport.go); the library's own word on what it holds comes
// from holdings.go.

const (
	// targetGrabTick is how often the grab worker looks for queued grabs when
	// nothing woke it; targetGrabLease holds a claimed one against a second
	// replica while it is searched and grabbed.
	targetGrabTick  = 30 * time.Second
	targetGrabLease = 10 * time.Minute
	// slotWait is how long a queued grab waits when as many downloads as the
	// policy allows are in flight. It is not searched meanwhile: a search
	// whose result cannot be grabbed spends a source's allowance for nothing.
	slotWait = 2 * time.Minute
	// foundAgain is when a target whose search found something is due again
	// while it stays wanted (the sweep searches without grabbing, or the grab
	// was refused).
	foundAgain = 12 * time.Hour
)

func newTargetGrabID() string {
	return store.TargetGrabPrefix + strings.ReplaceAll(time.Now().UTC().Format("20060102150405.000000000"), ".", "")
}

// coords names a target as scene names do: S04E07, or "movie".
func coords(t store.Target) string {
	if t.SeasonNumber == nil || t.EpisodeNumber == nil {
		return "movie"
	}
	return fmt.Sprintf("S%02dE%02d", *t.SeasonNumber, *t.EpisodeNumber)
}

// aired reports whether an episode target has aired: a season pack holds
// only those.
func aired(t store.Target, now time.Time) bool {
	return t.AirDate != nil && !t.AirDate.After(now)
}

// covering returns the targets of a title a release holds, and what its name
// says it holds: the movie for a movie; for a series every target the name's
// episodes or seasons cover — of a season pack, the aired ones. A release
// whose name says nothing covers only the target it was found for. anchor,
// when set, must be among them.
func (s *Service) covering(ctx context.Context, title store.Title, anchor store.Target, name string) ([]store.Target, release.Coverage, error) {
	if title.Kind == "movie" {
		if anchor.ID == "" {
			t, err := s.st.TargetAt(ctx, title.ID, nil, nil)
			if err != nil {
				return nil, release.Coverage{}, fmt.Errorf("%s has no target to grab for", title.Title)
			}
			anchor = t
		}
		return []store.Target{anchor}, release.Coverage{}, nil
	}
	cov := release.ParseCoverage(name)
	if !cov.Found {
		if anchor.ID == "" {
			return nil, cov, fmt.Errorf("%s names no episode or season of %s", name, title.Title)
		}
		return []store.Target{anchor}, cov, nil
	}
	all, err := s.st.TitleTargets(ctx, title.ID)
	if err != nil {
		return nil, cov, err
	}
	now := time.Now()
	var out []store.Target
	for _, t := range all {
		if t.SeasonNumber == nil || t.EpisodeNumber == nil || !cov.Covers(*t.SeasonNumber, *t.EpisodeNumber) {
			continue
		}
		if cov.Pack() && !aired(t, now) {
			continue
		}
		out = append(out, t)
	}
	if anchor.ID != "" {
		found := false
		for _, t := range out {
			found = found || t.ID == anchor.ID
		}
		if !found {
			return nil, cov, fmt.Errorf("%s does not hold %s", name, coords(anchor))
		}
	}
	if len(out) == 0 {
		return nil, cov, fmt.Errorf("%s holds no episode acquire tracks of %s", name, title.Title)
	}
	return out, cov, nil
}

// answerLostDetail is what a target reserved by an add whose answer was lost
// says until the download's events, or the stale-grab sweep, settle it.
const answerLostDetail = "handed to the gateway, but its answer was lost: waiting for the download's events"

// grabRelease hands one release to a download client for the targets of a
// title it covers, bound to them: reserved first (so no second grab takes
// them meanwhile), handed over, then bound to the client's job. A hand-over
// that does not happen gives the targets back as they were (handOverFailed).
// Once the add is sent it and its bookkeeping run to their end even if the
// caller goes away. anchor is the target the grab is for, or empty for a
// release found by name.
func (s *Service) grabRelease(ctx context.Context, title store.Title, anchor store.Target, c Candidate, via string) (store.TargetGrab, error) {
	targets, cov, err := s.covering(ctx, title, anchor, c.Title)
	if err != nil {
		return store.TargetGrab{}, notCovered{err}
	}
	ids := make([]string, 0, len(targets))
	for _, t := range targets {
		ids = append(ids, t.ID)
	}
	covers := cov.String()
	if covers == "" && title.Kind != "movie" && len(targets) == 1 {
		covers = coords(targets[0])
	}
	g := store.TargetGrab{
		ID: newTargetGrabID(), TitleID: title.ID, Via: via,
		ReleaseTitle: c.Title, Covers: covers, Indexer: c.Indexer, ReleaseGUID: c.GUID,
		Protocol: c.Protocol, SizeBytes: c.Size, Reason: c.Reason, Source: endpoint.Redact(c.link),
	}
	if c.IndexerID > 0 {
		id := c.IndexerID
		g.IndexerID = &id
	}
	var seeders *int32
	if c.Protocol == "torrent" {
		n := int32(c.Seeders)
		seeders, g.Seeders = &n, &n
	}
	// Reserved under the cap on downloads at once, counted and taken in one
	// step: the sweep, the feeds and the grab queue cannot both take the
	// last slot.
	g, err = s.st.ReserveTargetGrabWithin(ctx, g, anchor.ID, ids, s.grabPolicy(ctx).MaxConcurrentGrabs)
	var full *store.AtCapacityError
	if errors.As(err, &full) {
		return store.TargetGrab{}, atCapacity{msg: "refusing grab: " + full.Error()}
	}
	if err != nil {
		return store.TargetGrab{}, err
	}

	label := strings.TrimSpace(title.Title + " " + covers)
	out, err := s.handOff(ctx, handoff{
		WantedID: g.ID, Title: label, Link: c.link, Protocol: c.Protocol,
		ReleaseTitle: c.Title, Indexer: c.Indexer, IndexerID: c.IndexerID, ReleaseGUID: c.GUID,
		Size: c.Size, Seeders: seeders, Reason: c.Reason,
	})
	// What follows records what became of the hand-over: the gateway may have
	// the release, whether or not the caller still waits.
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err != nil {
		s.handOverFailed(wctx, g, c, err)
		return store.TargetGrab{}, err
	}

	g.Adapter, g.ClientJobID, g.Protocol = out.Client.ID, out.JobID, out.Protocol
	if s.box.Enabled() && c.link != "" {
		table, id, field := store.TargetGrabAAD(g.ID)
		if ct, kid, err := s.box.Seal(table, id, field, []byte(c.link)); err == nil {
			g.SourceCT, g.SourceKID = ct, kid
		}
	}
	what := covers
	if what == "" {
		what = title.Title
	}
	detail := "grabbed " + protoLabel(out.Protocol) + " " + what + " from " + c.Indexer + " via " + out.Client.ID
	if c.Indexer == "" {
		detail = "grabbed " + what + " via " + out.Client.ID
	}
	bound, err := s.st.BindTargetGrab(wctx, g, clip(detail, 300))
	if err != nil {
		// The download is on its way and its events carry the grab's id: the
		// completed event binds it then (onTargetCompleted).
		log.Printf("acquire: bind target grab %s to %s/%s: %v", g.ID, out.Client.ID, out.JobID, err)
		bound = g
	}
	_ = s.st.RecordHistory(wctx, store.HistoryEntry{
		Kind: "grabbed", Subject: g.ID, Title: c.Title, Indexer: c.Indexer, Protocol: out.Protocol,
		SizeMb: c.Size >> 20, Score: c.Score, Reason: clip(fmt.Sprintf("%s for %d target(s) of %s", via, len(bound.TargetIDs), title.Title), 300),
	})
	s.notify()
	return bound, nil
}

// handOverFailed settles a reservation whose release did not reach a client.
// An add whose answer was lost keeps it: the gateway may have the release,
// and its events bind the grab (onTargetCompleted) or the stale-grab sweep
// gives it up. Anything else gives the targets back as they were — a release
// that cannot be had blocked for them first, so the next grab takes another.
func (s *Service) handOverFailed(ctx context.Context, g store.TargetGrab, c Candidate, err error) {
	var lost answerLost
	if errors.As(err, &lost) {
		why := clip(answerLostDetail+" ("+lost.Error()+")", 300)
		_ = s.st.SetTargetGrabState(ctx, g.ID, store.GrabPending, why)
		for _, id := range g.TargetIDs {
			_ = s.st.SetTargetDetail(ctx, id, why)
		}
		log.Printf("acquire: grab %s: %v; its reservation stays for the download's events", g.ID, err)
		return
	}
	var bad badRelease
	if errors.As(err, &bad) {
		for _, id := range g.TargetIDs {
			_ = s.BlockFailedRelease(ctx, id, c.Title, c.Indexer, clip(bad.Error(), 200))
		}
	}
	if rerr := s.st.ReleaseTargetGrab(ctx, g.ID); rerr != nil {
		log.Printf("acquire: release the reservation %s: %v", g.ID, rerr)
	}
}

// GrabTarget searches one target with its typed search and grabs the best
// release an automatic grab may take (autoCandidates), bound to every target
// that release covers. The search's outcome is recorded on the target either
// way. A target that is held or in flight is refused (store.NotGrabbableError).
func (s *Service) GrabTarget(ctx context.Context, targetID, via string) (store.TargetGrab, error) {
	t, title, err := s.st.TargetWithTitle(ctx, targetID)
	if err != nil {
		return store.TargetGrab{}, err
	}
	if !store.Grabbable(t.State) {
		return store.TargetGrab{}, &store.NotGrabbableError{TargetID: t.ID, State: t.State, Detail: t.Detail}
	}
	if title.HeldForApproval() {
		return store.TargetGrab{}, errAwaitsApproval
	}
	cands, err := s.SearchTarget(ctx, targetID)
	if cerr := ctx.Err(); cerr != nil {
		// Cut short — a shutdown, a caller gone: no word on the target.
		return store.TargetGrab{}, unavailable{error: cerr}
	}
	usable := s.recordSearch(ctx, t, cands, err, nil)
	if err != nil {
		return store.TargetGrab{}, searchFailed{err}
	}
	if len(usable) == 0 {
		if n := preAiredCount(cands); n > 0 {
			return store.TargetGrab{}, onlyPreAired{found: len(cands), preAired: n, what: title.Title + " " + coords(t)}
		}
		return store.TargetGrab{}, fmt.Errorf("%w for %s %s", errNoReleases, title.Title, coords(t))
	}
	return s.grabBest(ctx, title, t, usable, via)
}

// errAwaitsApproval: a grab of a title a request awaiting an admin's
// approval asked for. The admin approves the request first.
var errAwaitsApproval = errors.New("its request awaits an admin's approval: approve it in the requests tab first")

// searchFailed is a target's search that could not be done: no source to
// ask, every source failing. It says nothing about the target.
type searchFailed struct{ error }

func (e searchFailed) Unwrap() error { return e.error }

// notCovered is a release that holds nothing a grab could be bound to — not
// the target it was picked for, or no episode acquire tracks. Nothing was
// grabbed; it says nothing about the target either.
type notCovered struct{ error }

func (e notCovered) Unwrap() error { return e.error }

// grabBest grabs the first of the ranked releases that can be grabbed: a
// source out of release downloads for today, and a release that cannot be had
// (blocked for its targets now), pass to the next.
func (s *Service) grabBest(ctx context.Context, title store.Title, t store.Target, ranked []Candidate, via string) (store.TargetGrab, error) {
	var lastErr error
	for _, c := range ranked {
		g, err := s.grabRelease(ctx, title, t, c, via)
		var bad badRelease
		if errors.Is(err, errGrabLimit) || errors.As(err, &bad) {
			lastErr = err
			continue
		}
		return g, err
	}
	return store.TargetGrab{}, lastErr
}

// GrabTargetCandidate grabs a release picked among a target's typed search
// results, bound to every wanted target it covers — a season pack too, which
// an automatic grab never takes.
func (s *Service) GrabTargetCandidate(ctx context.Context, targetID string, c Candidate) (store.TargetGrab, error) {
	if err := s.resolveCandidate(&c); err != nil {
		return store.TargetGrab{}, err
	}
	t, title, err := s.st.TargetWithTitle(ctx, targetID)
	if err != nil {
		return store.TargetGrab{}, err
	}
	if c.Reason == "" {
		c.Reason = "picked manually"
	}
	g, err := s.grabRelease(ctx, title, t, c, "pick")
	if err != nil {
		wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		s.targetGrabFailed(wctx, t, err)
	}
	return g, err
}

// targetGrabFailed records why a grab of a target did not happen. Nothing
// that keeps a grab from happening fails its target: what fails a target is
// its release's download or hand-over, never the grab's way there. A search
// that could not be done, nothing to grab, a refusal (no client, no room, no
// slot, the gateway's configuration) and the store failing say so on it; a
// source or gateway that cannot be had for now makes it due again on a later
// sweep; a release that cannot be had is blocked for it already
// (handOverFailed); an add whose answer was lost says so already.
func (s *Service) targetGrabFailed(ctx context.Context, t store.Target, err error) {
	var ng *store.NotGrabbableError
	var sf searchFailed
	var un unavailable
	var bad badRelease
	var lost answerLost
	switch {
	case err == nil, errors.As(err, &ng), errors.Is(err, errStaleRelease), errors.As(err, &lost):
		return
	case errors.As(err, &sf):
		_ = s.st.SetTargetDetail(ctx, t.ID, clip("search failed: "+sf.Error(), 300))
	case errors.Is(err, errNoReleases):
		_ = s.st.SetTargetDetail(ctx, t.ID, clip("searched: "+err.Error(), 300))
	case errors.As(err, &un):
		s.foundButWaiting(ctx, t, un.Error())
		_ = s.st.SetTargetDetail(ctx, t.ID, clip("not grabbed for now: "+un.Error()+"; searched again later", 300))
	case errors.As(err, &bad):
		_ = s.st.SetTargetDetail(ctx, t.ID, clip("not grabbed: "+bad.Error()+"; that release is blocked for it", 300))
	default:
		_ = s.st.SetTargetDetail(ctx, t.ID, clip("not grabbed: "+err.Error(), 300))
	}
	s.notify()
}

// gatewayDown reports whether a grab's error is the gateway not taking adds
// — out of reach, failing, its answer lost, refusing them: no other grab
// would reach a client now.
func gatewayDown(err error) bool {
	var un unavailable
	var lost answerLost
	var gr gatewayRefused
	return (errors.As(err, &un) && un.gateway) || errors.As(err, &lost) || errors.As(err, &gr)
}

// recordSearch records a target search's outcome — found, none or error —
// with when the target is due again, and returns the releases an automatic
// grab may take (autoCandidates; pol for a grab that comes on its own).
// Recording every outcome is what makes the sweep rotate. A release published
// before the target aired is skipped, saying so — never blocked: the date it
// is told by may be a guess, and a person may pick it.
func (s *Service) recordSearch(ctx context.Context, t store.Target, cands []Candidate, err error, pol *SearchSettings) []Candidate {
	if err != nil {
		// A search that errors is a failure to LOOK, not evidence of absence,
		// so it backs off rather than marking anything.
		_ = s.st.RecordSearch(ctx, t.ID, store.SearchRecord{Outcome: store.SearchError,
			Detail: clip(err.Error(), 300), Base: backoffBase, Max: backoffCeiling})
		return nil
	}
	usable := s.autoCandidates(ctx, t, cands, pol)
	if len(usable) == 0 {
		// Nothing usable is also not proof of absence — a typed query to an
		// indexer without id support returns zero with no error — so this
		// backs off too and will be asked again later.
		detail := "nothing found"
		switch n := preAiredCount(cands); {
		case n > 0 && n == len(cands):
			detail = fmt.Sprintf("%d release(s), every one published before it aired — most likely fakes, skipped", n)
		case n > 0:
			detail = fmt.Sprintf("%d release(s): %d published before it aired — most likely fakes, skipped —, none other an automatic grab takes",
				len(cands), n)
		case len(cands) > 0:
			detail = fmt.Sprintf("%d release(s), none an automatic grab takes", len(cands))
		}
		_ = s.st.RecordSearch(ctx, t.ID, store.SearchRecord{Outcome: store.SearchNone,
			Detail: detail, Base: backoffBase, Max: backoffCeiling})
		return nil
	}
	best := usable[0]
	_ = s.st.RecordSearch(ctx, t.ID, store.SearchRecord{Outcome: store.SearchFound, Again: foundAgain,
		Detail: clip(fmt.Sprintf("%d release(s); best %s via %s (score %d)", len(usable), best.Title, best.Indexer, best.Score), 300)})
	_ = s.st.RecordHistory(ctx, store.HistoryEntry{
		Kind: "searched", Subject: t.ID, Title: best.Title,
		Indexer: best.Indexer, Protocol: best.Protocol,
		SizeMb: best.Size / (1024 * 1024), Score: best.Score,
		Reason: fmt.Sprintf("%d candidate(s), best via %s", len(usable), best.MatchedVia),
	})
	return usable
}

// ── the grab queue (the console's Missing tab) ─────────────────────────────

// RequestTargetGrabs queues a search and grab for each target named that can
// be grabbed, done by the worker one at a time, and returns the ones queued.
func (s *Service) RequestTargetGrabs(ctx context.Context, ids []string) ([]string, error) {
	queued, err := s.st.RequestTargetGrabs(ctx, ids)
	if err != nil {
		return nil, err
	}
	if len(queued) > 0 {
		s.wakeTargetGrabs()
		s.notify()
	}
	return queued, nil
}

func (s *Service) wakeTargetGrabs() {
	select {
	case s.grabWake <- struct{}{}:
	default:
	}
}

// RunTargetGrabs works through queued grabs until ctx ends.
func (s *Service) RunTargetGrabs(ctx context.Context) {
	if s.st == nil {
		return
	}
	t := time.NewTicker(targetGrabTick)
	defer t.Stop()
	for {
		for ctx.Err() == nil && s.targetGrabNext(ctx) {
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.grabWake:
		}
	}
}

// targetGrabNext works on the queued grab due first, if any, and reports
// whether there was one. While no download slot is free it waits, unsearched;
// a grab refused for another reason (no client, no room) is taken off the
// queue saying why, since asking again would only spend allowance.
func (s *Service) targetGrabNext(ctx context.Context) bool {
	id, ok, err := s.st.ClaimTargetGrab(ctx, targetGrabLease)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("acquire: claim a queued grab: %v", err)
		}
		return false
	}
	if !ok {
		return false
	}
	if full, why := s.slotsFull(ctx); full {
		wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_ = s.st.DeferTargetGrabRequest(wctx, id, slotWait, clip("queued: "+why+"; tried again when a slot is free", 300))
		return true
	}
	_, err = s.GrabTarget(ctx, id, "missing")
	// Recorded whether or not ctx ended meanwhile, and after the grab, which
	// may take longer than any wait set before it.
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	var capErr atCapacity
	switch {
	case errors.As(err, &capErr):
		_ = s.st.DeferTargetGrabRequest(wctx, id, slotWait, clip("queued: "+capErr.Error(), 300))
		return true
	case err != nil && ctx.Err() != nil:
		// Cut short by a shutdown: it stays queued, for whoever runs next.
		_ = s.st.DeferTargetGrabRequest(wctx, id, 0, "queued for a search and grab")
		return true
	}
	_ = s.st.FinishTargetGrabRequest(wctx, id)
	if err != nil {
		s.targetGrabFailed(wctx, store.Target{ID: id}, err)
	}
	s.notify()
	return true
}

// ── the download's events ──────────────────────────────────────────────────

// targetGrabOf finds the target grab a download event is about: by the id the
// gateway echoes, else — a restarted gateway forgot it — by the client job,
// whose grab is the newest handed to it (store.FindTargetGrabByJob).
func (s *Service) targetGrabOf(ctx context.Context, ev events.DownloadEvent) (store.TargetGrab, bool) {
	if s.st == nil {
		return store.TargetGrab{}, false
	}
	if store.IsTargetGrab(ev.WantedID) {
		g, err := s.st.GetTargetGrab(ctx, ev.WantedID)
		return g, err == nil
	}
	if ev.WantedID == "" {
		g, err := s.st.FindTargetGrabByJob(ctx, ev.Adapter, ev.ClientID)
		return g, err == nil
	}
	return store.TargetGrab{}, false
}

// inFlightGrab binds a grab an event names whose bind never landed — the
// add's answer was lost — to the event's job, and reports whether the event
// moves anything: a grab with a client (bound now, or before) that is not done
// (imported) nor given up (failed). A repeated or late event for a grab that
// is done or given up moves nothing, and neither does one of a grab whose job
// id the client gave to a newer grab since: its download is gone, and the job
// is the newer grab's (store.JobReused).
func (s *Service) inFlightGrab(ctx context.Context, g store.TargetGrab, ev events.DownloadEvent) (store.TargetGrab, bool, error) {
	if reused, err := s.st.JobReused(ctx, ev.Adapter, ev.ClientID, g.ID); err != nil || reused {
		return g, false, err
	}
	if g.State == store.GrabPending {
		g.Adapter, g.ClientJobID = ev.Adapter, ev.ClientID
		bound, err := s.st.BindTargetGrab(ctx, g, "grabbed")
		if err != nil {
			return g, false, err
		}
		g = bound
	}
	return g, g.State == store.GrabGrabbed || g.State == store.GrabDownloaded, nil
}

// errImportWaits: a target grab's download finished while its client job still
// held the import of another download that had the job id before, not
// finished (store.ErrImportBusy). Nothing was queued; the grab is downloaded,
// saying so, and the grab recovery queues its import once the other one is
// done.
var errImportWaits = errors.New("its import waits for another download's on the same job id")

// onTargetCompleted: the download of a target grab finished. Its targets move
// from grabbed to downloaded and the download is queued for the import worker,
// which copies its files into the arrivals when they lie outside them and hands
// each over by identity (targetimport.go). A repeat of the event queues
// nothing twice, and a target that failed meanwhile stays failed, for its
// recovery.
//
// Only an import queued, or this grab's queued before, moves the targets as
// handed on: a job id the client gave again may still hold another download's
// import, which is not replaced while it runs (errImportWaits).
func (s *Service) onTargetCompleted(ctx context.Context, g store.TargetGrab, ev events.DownloadEvent) error {
	g, moves, err := s.inFlightGrab(ctx, g, ev)
	if err != nil || !moves {
		return err
	}
	created, err := s.st.EnqueueImport(ctx, store.Import{
		Adapter: ev.Adapter, ClientJobID: ev.ClientID, WantedID: g.ID, Files: ev.Files,
	})
	var busy *store.ImportBusyError
	switch {
	case errors.As(err, &busy):
		why := clip(fmt.Sprintf("downloaded; not copied into the library yet: %s gave its job id %s to an earlier download, "+
			"whose import is not done (%s); queued once it is", ev.Adapter, ev.ClientID, busy.State), 300)
		_ = s.st.SetTargetGrabState(ctx, g.ID, store.GrabDownloaded, why)
		_, _ = s.st.MoveGrabTargets(ctx, g.ID, []string{store.TargetGrabbed, store.TargetDownloaded}, store.TargetDownloaded, why)
		log.Printf("acquire: grab %s: its import waits: %s/%s still holds the import of %s (%s)",
			g.ID, ev.Adapter, ev.ClientID, busy.WantedID, busy.State)
		s.notify()
		return fmt.Errorf("%w: %v", errImportWaits, busy)
	case err != nil:
		// The store failing says nothing of the targets: they stay grabbed,
		// for the event's repeat or the stale-grab sweep.
		log.Printf("acquire: queue the import of %s: %v", g.ID, err)
		return err
	}
	if !created && g.State == store.GrabDownloaded {
		return nil
	}
	_ = s.st.SetTargetGrabState(ctx, g.ID, store.GrabDownloaded, "downloaded")
	detail := "downloaded; handing it over to the library"
	if files := s.mapEventFiles(ctx, ev); len(files) > 0 && s.copiesIntoArrivals(files[0]) {
		detail = "downloaded; copying into the library"
	}
	// Targets downloaded already waited for another download's import to end.
	_, _ = s.st.MoveGrabTargets(ctx, g.ID, []string{store.TargetGrabbed, store.TargetDownloaded}, store.TargetDownloaded, detail)
	s.wakeImports()
	s.notify()
	return nil
}

// onTargetFailed: the download of a target grab failed. Its targets that are
// still its own and grabbed fail by their release, with the client's reason:
// the release is blocked for each of them so that the next grab picks
// another, and they are wanted again after a wait (failure recovery). A grab
// whose download finished already, or that was given up, moves nothing.
func (s *Service) onTargetFailed(ctx context.Context, g store.TargetGrab, ev events.DownloadEvent) error {
	g, moves, err := s.inFlightGrab(ctx, g, ev)
	if err != nil || !moves || g.State != store.GrabGrabbed {
		return err
	}
	detail := ev.Error
	if detail == "" {
		detail = "download failed"
	}
	_ = s.st.SetTargetGrabState(ctx, g.ID, store.GrabFailed, clip(detail, 300))
	_, _ = s.st.FailGrabTargets(ctx, g.ID, g.TargetIDs, []string{store.TargetGrabbed}, clip("download failed: "+detail, 300), true)
	for _, id := range g.TargetIDs {
		_ = s.BlockFailedRelease(ctx, id, g.ReleaseTitle, g.Indexer, clip(detail, 200))
	}
	s.notify()
	return nil
}

// cancelTargetGrab gives up the target grab of a download someone cancelled
// at once, instead of on a gateway event that may never come: the grab fails
// (cancelled), its targets are wanted again with the release blocked for them
// — whoever cancelled the download did not want it — and a hand-over still
// waiting for the catalog stops, its copies deleted.
func (s *Service) cancelTargetGrab(ctx context.Context, adapter, jobID string) {
	g, err := s.st.FindTargetGrabByJob(ctx, adapter, jobID)
	if err != nil || (g.State != store.GrabPending && g.State != store.GrabGrabbed && g.State != store.GrabDownloaded) {
		return
	}
	const why = "cancelled: its download was removed from the client; wanted again, that release blocked for it"
	if _, err := s.st.FreeTargetGrab(ctx, g.ID, why); err != nil {
		log.Printf("acquire: give up the cancelled grab %s: %v", g.ID, err)
		return
	}
	for _, id := range g.TargetIDs {
		_ = s.BlockFailedRelease(ctx, id, g.ReleaseTitle, g.Indexer, "its download was cancelled")
	}
	// The job's import may be another grab's, on a job id the client gave again.
	if im, err := s.st.GetImport(ctx, adapter, jobID); err == nil && im.WantedID == g.ID && im.State == store.ImportPending {
		im.State, im.Detail, im.NextAt = store.ImportDone, "the grab was cancelled; nothing more handed over", time.Now()
		_ = s.st.SaveImport(ctx, im)
	}
	s.dropCopies(g.ID, s.untakenCopies(ctx, g.ID))
}

// staleGrabAge is how long a grab in flight may go without a word of it — no
// event of its download, nothing done with it — before the stale-grab sweep
// asks the gateway whether it still knows it.
const staleGrabAge = 48 * time.Hour

// SweepStaleGrabs gives up the grabs in flight that nothing was heard of for
// two days and the gateway does not know, by the grab's id or by its client
// job: a hand-over that died before its job was bound, a job its client lost,
// events that never came. Their targets are wanted again. A gateway that
// cannot be asked gives nothing up: no grab is given up on a guess.
func (s *Service) SweepStaleGrabs(ctx context.Context) (int, error) {
	stale, err := s.st.StaleTargetGrabs(ctx, staleGrabAge)
	if err != nil || len(stale) == 0 || !s.gw.Enabled() {
		return 0, err
	}
	jobs, err := s.gw.List(ctx)
	if err != nil {
		return 0, fmt.Errorf("stale grabs: the gateway cannot be asked which downloads it has: %w", err)
	}
	known := map[string]bool{}
	for _, j := range jobs {
		if j.WantedItemID != "" {
			known[j.WantedItemID] = true
		}
		known[j.Adapter+"/"+j.ClientJobID] = true
	}
	freed := 0
	for _, g := range stale {
		if known[g.ID] || (g.ClientJobID != "" && known[g.Adapter+"/"+g.ClientJobID]) {
			continue
		}
		const why = "nothing was heard of its download for two days and the gateway does not know it: given up, wanted again"
		if _, err := s.st.FreeTargetGrab(ctx, g.ID, why); err != nil {
			return freed, err
		}
		log.Printf("acquire: grab %s given up: %s", g, why)
		freed++
	}
	if freed > 0 {
		s.notify()
	}
	return freed, nil
}

// RecoverGrabs is the grab-recovery schedule: targets their release failed
// are wanted again once their wait ran out (store.RecoverFailedTargets), and
// grabs stuck in flight are given up (SweepStaleGrabs).
func (s *Service) RecoverGrabs(ctx context.Context) error {
	n, err := s.st.RecoverFailedTargets(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		log.Printf("acquire grab-recovery: %d failed target(s) wanted again, their failed releases blocked", n)
		s.notify()
	}
	_, err = s.SweepStaleGrabs(ctx)
	return err
}

// mapEventFiles is the event's files as acquire sees them.
func (s *Service) mapEventFiles(ctx context.Context, ev events.DownloadEvent) []string {
	if c, err := s.st.GetDownloadClient(ctx, ev.Adapter); err == nil {
		return mapClientPaths(ev.Files, c.RemotePath, c.LocalPath)
	}
	return ev.Files
}

// tmdbString is a title's TMDB id as ingest by identity takes it.
func tmdbString(id int64) string {
	if id <= 0 {
		return ""
	}
	return strconv.FormatInt(id, 10)
}
