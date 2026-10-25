package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/laedeli/acquire/internal/events"
	"github.com/laedeli/acquire/internal/katalog"
	"github.com/laedeli/acquire/internal/release"
	"github.com/laedeli/acquire/internal/store"
)

// What the library holds.
//
// katalog owns HAVE, acquire owns WANT. acquire learns what the library holds
// of each tracked title from katalog-api's holdings by identity (ADR-0012),
// and follows it between reconciles through the catalog's events: an item
// discovered or packaged is held (packaged: and playable), an item removed is
// wanted again. Nothing here creates a target for a holding acquire does not
// track — the library holds things nobody is monitoring, and they must not
// become things acquire hunts — save a movie's one target.
//
// Until a title's holdings are known (never reconciled, or the catalog does
// not answer holdings by identity), its targets are "holdings unknown": not
// missing, and never grabbed automatically, because nothing says they are not
// on disk.

const (
	// holdingsRun bounds one reconcile of the whole library.
	holdingsRun = 30 * time.Minute
	// holdingsUnsupported is what a title whose holdings cannot be known says.
	holdingsUnsupported = "the catalog does not answer holdings by identity yet (katalog-api GET /api/v1/holdings)"
)

// HoldingsRun is what one reconcile did.
type HoldingsRun struct {
	// Supported is false when the catalog does not answer holdings by
	// identity: every title's holdings are unknown then.
	Supported bool `json:"supported"`
	Titles    int  `json:"titles"`
	Known     int  `json:"known"`
	// Unknown are the titles the catalog may hold as an item without an
	// identity (heldWithoutIdentity): of unknown holdings until it has one.
	Unknown  int    `json:"unknown"`
	Failed   int    `json:"failed"`
	Held     int64  `json:"held"`
	Released int64  `json:"released"`
	Error    string `json:"error,omitempty"`
}

// ReconcileHoldings asks katalog-api what the library holds of the titles
// named — every tracked series and movie when none is — and makes their
// targets match: held with the item that holds each, or wanted. A title the
// read side cannot be asked about keeps what was known of it; a catalog
// without holdings by identity makes every title's holdings unknown.
func (s *Service) ReconcileHoldings(ctx context.Context, titleIDs ...string) (HoldingsRun, error) {
	var run HoldingsRun
	if s.st == nil {
		return run, errors.New("no store")
	}
	if s.kc == nil || s.kc.KatalogURL == "" {
		return run, errors.New("KATALOG_URL is not set: what the library holds cannot be asked")
	}
	titles, err := s.holdingsTitles(ctx, titleIDs)
	if err != nil {
		return run, err
	}
	run.Titles = len(titles)
	if len(titles) == 0 {
		return run, nil
	}
	// The titles a catalog-wide answer is recorded on: the ones named, or —
	// none named — every title (nil).
	var ids []string
	if len(titleIDs) > 0 {
		for _, t := range titles {
			ids = append(ids, t.ID)
		}
	}
	switch err := s.kc.ProbeHoldings(ctx); {
	case errors.Is(err, katalog.ErrHoldingsUnsupported):
		if _, uerr := s.st.HoldingsUnknown(ctx, ids, holdingsUnsupported); uerr != nil {
			return run, uerr
		}
		run.Error = holdingsUnsupported
		log.Printf("acquire holdings: %s; %d title(s) stay of unknown holdings, none is missing or grabbed automatically",
			holdingsUnsupported, len(titles))
		s.notify()
		return run, nil
	case err != nil:
		run.Error = err.Error()
		_ = s.st.HoldingsFailed(ctx, ids, "could not ask the catalog: "+err.Error())
		return run, fmt.Errorf("holdings: %w", err)
	}
	run.Supported = true
	ns := &namesakes{read: map[string]error{}, byName: map[string][]katalog.ListedTitle{}}
	for _, t := range titles {
		if ctx.Err() != nil {
			run.Error = ctx.Err().Error()
			break
		}
		ch, err := s.reconcileTitle(ctx, t, ns)
		wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		var unnamed heldWithoutIdentity
		switch {
		case errors.Is(err, katalog.ErrHoldingsUnsupported):
			run.Failed++
			_, _ = s.st.HoldingsUnknown(wctx, []string{t.ID}, holdingsUnsupported)
		case errors.As(err, &unnamed):
			run.Unknown++
			_, _ = s.st.HoldingsUnknown(wctx, []string{t.ID}, unnamed.Error())
		case err != nil:
			run.Failed++
			_ = s.st.HoldingsFailed(wctx, []string{t.ID}, err.Error())
		default:
			run.Known++
			run.Held += ch.Held
			run.Released += ch.Released
		}
		cancel()
	}
	log.Printf("acquire holdings: %d title(s) — %d known, %d held without an identity, %d failed; %d target(s) held, %d no longer held",
		run.Titles, run.Known, run.Unknown, run.Failed, run.Held, run.Released)
	s.notify()
	return run, nil
}

// holdingsTitles are the titles a reconcile is for.
func (s *Service) holdingsTitles(ctx context.Context, ids []string) ([]store.Title, error) {
	if len(ids) > 0 {
		var out []store.Title
		for _, id := range ids {
			t, err := s.st.TitleByID(ctx, id)
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			out = append(out, t)
		}
		return out, nil
	}
	series, err := s.st.TitlesOfKind(ctx, "series", false)
	if err != nil {
		return nil, err
	}
	movies, err := s.st.TitlesOfKind(ctx, "movie", false)
	if err != nil {
		return nil, err
	}
	return append(series, movies...), nil
}

// reconcileTitle asks what the library holds of one title, by each identity
// acquire holds in turn — TMDB, then TVDB, then IMDb — and applies it. A title
// none of whose identities the library holds, but that it may hold as an item
// without an identity (ns), is heldWithoutIdentity: nothing is applied.
func (s *Service) reconcileTitle(ctx context.Context, t store.Title, ns *namesakes) (store.HoldingsChange, error) {
	_, profileID := s.ScoringProfile(ctx, t.ProfileID)
	// The holdings are known as of when the catalog was asked: a target a
	// read of TMDB adds meanwhile waits for the next reconcile.
	asked := time.Now()
	if t.Kind == "movie" {
		m, err := lookupByIdentity(t, func(kind, id string) (katalog.MovieHolding, error) {
			return s.kc.MovieHoldings(ctx, kind, id)
		}, false)
		switch {
		case errors.Is(err, katalog.ErrNotHeld):
			if err := ns.check(ctx, s.kc, t); err != nil {
				return store.HoldingsChange{}, err
			}
			return s.st.ApplyMovieHoldingAt(ctx, t.ID, "", profileID, nil, asked)
		case err == nil && !m.Held():
			return s.st.ApplyMovieHoldingAt(ctx, t.ID, "", profileID, nil, asked)
		case err != nil:
			return store.HoldingsChange{}, err
		}
		return s.st.ApplyMovieHoldingAt(ctx, t.ID, m.ItemID, profileID, &store.HeldEpisode{ItemID: m.ItemID, Playable: m.Playable}, asked)
	}
	sh, err := lookupByIdentity(t, func(kind, id string) (katalog.SeriesHoldings, error) {
		return s.kc.SeriesHoldings(ctx, kind, id)
	}, true)
	if errors.Is(err, katalog.ErrNotHeld) {
		if err := ns.check(ctx, s.kc, t); err != nil {
			return store.HoldingsChange{}, err
		}
		return s.st.ApplySeriesHoldingsAt(ctx, t.ID, "", profileID, nil, asked)
	}
	if err != nil {
		return store.HoldingsChange{}, err
	}
	return s.st.ApplySeriesHoldingsAt(ctx, t.ID, sh.SeriesID, profileID, heldEpisodes(sh), asked)
}

// heldEpisodes are the episodes a series' holdings hold, each once. A series
// the catalog holds twice is answered as one, the first made, with the
// episodes of every copy in its seasons — an episode both copies hold is
// listed twice: it is held by the copy that plays, else one with a file of its
// own, else the first listed.
func heldEpisodes(sh katalog.SeriesHoldings) []store.HeldEpisode {
	rank := func(e katalog.EpisodeHolding) int {
		switch {
		case e.Playable:
			return 3
		case e.HasFile && e.CoveredBy == "":
			return 2
		case e.HasFile:
			return 1
		}
		return 0
	}
	var held []store.HeldEpisode
	var ranks []int
	at := map[[2]int]int{}
	for _, season := range sh.Seasons {
		for _, e := range season.Episodes {
			if !e.Held() {
				continue
			}
			h := store.HeldEpisode{Season: season.Season, Episode: e.Episode, ItemID: e.ItemID, Via: e.CoveredBy, Playable: e.Playable}
			key := [2]int{season.Season, e.Episode}
			if i, ok := at[key]; ok {
				if rank(e) > ranks[i] {
					held[i], ranks[i] = h, rank(e)
				}
				continue
			}
			at[key] = len(held)
			held, ranks = append(held, h), append(ranks, rank(e))
		}
	}
	return held
}

// heldWithoutIdentity is a title the library may hold as an item that carries
// no identity: a lookup by identity cannot find it, but its name — and its
// year, when both have one — is the title's. What the library holds of it
// cannot be known until the item carries an identity; until then nothing of
// it is missing, nor grabbed automatically.
type heldWithoutIdentity struct{ item katalog.ListedTitle }

func (e heldWithoutIdentity) Error() string {
	return fmt.Sprintf("the catalog holds a title of this name without an identity (item %s, %q): "+
		"what it holds of it cannot be known until that item carries one", e.item.ItemID, e.item.Title)
}

// namesakes are the titles the catalog holds without an identity, by kind and
// name, read once per reconcile — from the listing, page by page — when a
// lookup by identity first finds nothing.
type namesakes struct {
	read   map[string]error // kind → the listing's error, once read
	byName map[string][]katalog.ListedTitle
}

// check reports a title none of whose identities the library holds as
// heldWithoutIdentity when the library holds a title of its name without an
// identity, and an error when that cannot be told.
func (n *namesakes) check(ctx context.Context, kc *katalog.Client, t store.Title) error {
	kind := t.Kind
	if _, done := n.read[kind]; !done {
		all, err := kc.ListHoldings(ctx, kind)
		n.read[kind] = err
		for _, it := range all {
			if len(it.ExternalIDs) > 0 {
				continue
			}
			key, year := titleKey(it.Title)
			if it.Year == 0 {
				it.Year = year
			}
			n.byName[kind+"\x00"+key] = append(n.byName[kind+"\x00"+key], it)
		}
	}
	if err := n.read[kind]; err != nil {
		return fmt.Errorf("the catalog's titles without an identity could not be read: %w", err)
	}
	key, year := titleKey(t.Title)
	if t.Year != 0 {
		year = t.Year
	}
	for _, it := range n.byName[kind+"\x00"+key] {
		if it.Year == 0 || year == 0 || it.Year == year {
			return heldWithoutIdentity{it}
		}
	}
	return nil
}

// trailingYear is a year a title's name ends with, in parentheses.
var trailingYear = regexp.MustCompile(`\s*\((\d{4})\)\s*$`)

// titleKey is a title's name as namesakes compare it — letters and digits in
// lower case, one space between words — and the year its name ends with in
// parentheses, if any.
func titleKey(name string) (string, int) {
	year := 0
	if m := trailingYear.FindStringSubmatchIndex(name); m != nil {
		year, _ = strconv.Atoi(name[m[2]:m[3]])
		name = name[:m[0]]
	}
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteRune(r)
			space = false
			continue
		}
		space = true
	}
	return b.String(), year
}

// lookupByIdentity asks by each identity of a title in turn, in the order an
// ingest by identity tries them — TMDB, then TVDB (a series'), then IMDb —
// until one is held: a title enrichment gave only a TVDB id is found by that.
// One id a request: two that named different titles would be refused. An id
// not in its source's form is not asked by.
func lookupByIdentity[T any](t store.Title, ask func(kind, id string) (T, error), tvdb bool) (T, error) {
	var zero T
	var ids [][2]string
	if t.TMDBID > 0 {
		ids = append(ids, [2]string{"tmdb", strconv.FormatInt(t.TMDBID, 10)})
	}
	if tvdb && t.TVDBID > 0 {
		ids = append(ids, [2]string{"tvdb", strconv.FormatInt(t.TVDBID, 10)})
	}
	if imdb := katalog.IMDbID(t.IMDBID); imdb != "" {
		ids = append(ids, [2]string{"imdb", imdb})
	}
	err := katalog.ErrNotHeld
	for _, id := range ids {
		var out T
		out, err = ask(id[0], id[1])
		if !errors.Is(err, katalog.ErrNotHeld) {
			return out, err
		}
	}
	return zero, err
}

// RequestReconcile asks the reconcile worker to learn what the library holds
// of the titles named — every title when none is. Requests that arrive while
// one runs are gathered into the next.
func (s *Service) RequestReconcile(titleIDs ...string) {
	s.holdingsMu.Lock()
	if len(titleIDs) == 0 {
		s.holdingsAll = true
	}
	for _, id := range titleIDs {
		s.holdingsIDs[id] = true
	}
	s.holdingsMu.Unlock()
	select {
	case s.holdingsWake <- struct{}{}:
	default:
	}
}

// RunHoldings reconciles what it is asked to until ctx ends: on its schedule
// (holdings-reconcile), after an import or derivation of titles, after a
// hand-over the catalog answered "held already", and from the console.
func (s *Service) RunHoldings(ctx context.Context) {
	if s.st == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.holdingsWake:
		}
		s.holdingsMu.Lock()
		all := s.holdingsAll
		ids := make([]string, 0, len(s.holdingsIDs))
		for id := range s.holdingsIDs {
			ids = append(ids, id)
		}
		s.holdingsAll, s.holdingsIDs = false, map[string]bool{}
		s.holdingsMu.Unlock()
		if all {
			ids = nil
		} else if len(ids) == 0 {
			continue
		}
		rctx, cancel := context.WithTimeout(ctx, holdingsRun)
		if _, err := s.ReconcileHoldings(rctx, ids...); err != nil && ctx.Err() == nil {
			log.Printf("acquire holdings: %v", err)
		}
		cancel()
	}
}

// ── one holding, from an event ─────────────────────────────────────────────

// Holding is one file katalog holds, described by identity and coordinates
// rather than by katalog's own id for the title. The item id travels back to
// us; it is never something we send. That direction is the whole seam:
// katalog owns HAVE, acquire owns WANT.
type Holding struct {
	ItemID string
	// The title's identities: a movie's own, an episode's series'.
	TMDBID        int64
	TVDBID        int64
	IMDBID        string
	Kind          string // movie | episode
	SeasonNumber  *int
	EpisodeNumber *int
	Playable      bool
	Release       string // the release name, when a grab produced this file
	Quality       map[string]any
}

// ReconcileHolding projects one katalog file onto its acquisition target.
//
// This is what makes the existing library appear as `held` with ZERO data
// migration: TMDB supplies the inventory, katalog supplies the holdings, and
// this joins them on identity and coordinates. Nothing moves.
//
// A holding for a title or coordinate we do not track is IGNORED rather than
// auto-created. Creating targets from whatever katalog happens to contain would
// silently invent intent — the library holds things nobody is monitoring, and
// they must not become things we hunt upgrades for. A movie's one target is
// the exception: it is the title.
func (s *Service) ReconcileHolding(ctx context.Context, h Holding) error {
	_, err := s.reconcileHolding(ctx, h)
	return err
}

// reconcileHolding is ReconcileHolding, saying the tracked title the holding
// is of (zero when acquire tracks none).
func (s *Service) reconcileHolding(ctx context.Context, h Holding) (store.Title, error) {
	if h.ItemID == "" || (h.TMDBID == 0 && h.TVDBID == 0 && h.IMDBID == "") {
		return store.Title{}, fmt.Errorf("holding needs an item id and an identity")
	}
	kind := "movie"
	if h.Kind == "episode" {
		kind = "series"
	}
	title, err := s.st.TitleByIdentity(ctx, kind, h.TMDBID, h.TVDBID, h.IMDBID)
	if errors.Is(err, store.ErrNotFound) {
		return store.Title{}, nil
	}
	if err != nil {
		return store.Title{}, err
	}
	if kind == "series" && (h.SeasonNumber == nil || h.EpisodeNumber == nil) {
		return title, nil
	}
	t, err := s.st.TargetAt(ctx, title.ID, h.SeasonNumber, h.EpisodeNumber)
	if err == nil && t.State == store.TargetHeld && t.HeldItemID == h.ItemID {
		// Held by this very item already (a grab's own hand-over, or a
		// discovered before this packaged): only what is new.
		if h.Playable && !t.HeldPlayable {
			_, err = s.st.MarkItemPlayable(ctx, h.ItemID)
		}
		return title, err
	}
	if err == nil && t.State == store.TargetHeld && t.HeldVia != "" && t.HeldVia != h.ItemID {
		// An episode held through another's file — one of several a grab's
		// file covers — and this its own item: only the item is new; it is
		// held through the same file, by the same release.
		return title, s.st.RelinkHeldItem(ctx, t.ID, h.ItemID, h.Playable)
	}
	profile, profileID := s.ScoringProfile(ctx, title.ProfileID)
	if kind == "movie" {
		// One file is all a movie is: this holding is its holdings.
		_, err := s.st.ApplyMovieHolding(ctx, title.ID, h.ItemID, profileID,
			&store.HeldEpisode{ItemID: h.ItemID, Playable: h.Playable})
		return title, err
	}
	if errors.Is(err, store.ErrNotFound) {
		return title, nil
	}
	if err != nil {
		return title, err
	}
	// Score what we hold under the active profile, so "is this good enough?"
	// is answerable without asking katalog again per row.
	source, score := "derived", 0
	quality := h.Quality
	if quality == nil {
		quality = map[string]any{}
	}
	if h.Release != "" {
		// A real release name means a grab produced this file and the score is
		// trustworthy.
		source = "grab"
		v, in := release.Score(release.Candidate{Title: h.Release, Protocol: "usenet"}, profile)
		score = v.Score
		quality["resolution"], quality["source"], quality["codec"] = in.Resolution, in.Source, in.Codec
	}
	return title, s.st.HoldTarget(ctx, store.Hold{
		TargetID: t.ID, ItemID: h.ItemID, Playable: h.Playable, Release: h.Release, Source: source,
		ProfileID: profileID, Quality: quality, Score: score, Detail: "in the library",
	})
}

// holdingOf reads the holding an item event describes, when it carries the
// identities to tell (ADR-0012): an episode by its series' identity
// (seriesExternalIds — its own externalIds name the episode, a TMDB one under
// tmdb-episode) and its numbers, a movie by its own.
func holdingOf(ev events.ItemEvent, playable bool) (Holding, bool) {
	h := Holding{ItemID: ev.ItemID, Playable: playable}
	switch {
	case ev.SeasonNumber != nil && ev.EpisodeNumber != nil && !ev.SeriesExternalIDs.Empty():
		h.Kind, h.SeasonNumber, h.EpisodeNumber = "episode", ev.SeasonNumber, ev.EpisodeNumber
		h.TMDBID, h.TVDBID, h.IMDBID = ev.SeriesExternalIDs.Int("tmdb"), ev.SeriesExternalIDs.Int("tvdb"), ev.SeriesExternalIDs.IMDb()
	case ev.Type == "movie" && !ev.ExternalIDs.Empty():
		h.Kind = "movie"
		h.TMDBID, h.IMDBID = ev.ExternalIDs.Int("tmdb"), ev.ExternalIDs.IMDb()
	default:
		return h, false
	}
	return h, h.ItemID != ""
}

// holdingFromEvent applies what an item event says the library holds: a
// packaged item makes what it holds playable — every episode held through its
// file too —, and an event with identities holds its target.
//
// A file of several episodes is packaged once, as its first episode: the
// episodes it covers get no event of their own. What acquire recorded of them
// (held through the holder's item) is made playable here; an episode's
// packaged event also asks the holdings of its series to confirm the rest,
// which covers a file acquire did not grab.
func (s *Service) holdingFromEvent(ctx context.Context, ev events.ItemEvent, playable bool) {
	if s.st == nil || ev.ItemID == "" {
		return
	}
	changed := false
	if playable {
		if n, err := s.st.MarkItemPlayable(ctx, ev.ItemID); err == nil && n > 0 {
			changed = true
		}
	}
	if h, ok := holdingOf(ev, playable); ok {
		title, err := s.reconcileHolding(ctx, h)
		switch {
		case err != nil:
			log.Printf("acquire holdings: item %s: %v", ev.ItemID, err)
		case title.ID != "":
			changed = true
			if playable && h.Kind == "episode" {
				s.RequestReconcile(title.ID)
			}
		}
	}
	if changed {
		s.notify()
	}
}

// OnDiscovered: the catalog took in an item — the library holds it.
func (s *Service) OnDiscovered(ctx context.Context, ev events.ItemEvent) error {
	s.holdingFromEvent(ctx, ev, false)
	return nil
}

// OnRemoved: the catalog lost an item — what it held is wanted again.
func (s *Service) OnRemoved(ctx context.Context, ev events.ItemEvent) error {
	return s.OnItemRemoved(ctx, ev.ItemID)
}

// OnItemRemoved reverses a holding when katalog loses the file. The target
// becomes wanted again — that is why WANT is modelled separately from HAVE, and
// a one-way projection would quietly leave a hole nobody hunts. Of a title
// that is not monitored, it is not hunted. Its subtitles are looked for no
// more.
func (s *Service) OnItemRemoved(ctx context.Context, itemID string) error {
	s.subtitleRemoved(ctx, itemID) // nothing to look subtitles for any more
	n, err := s.st.ClearHolding(ctx, itemID)
	if err != nil {
		return err
	}
	if n > 0 {
		log.Printf("acquire: katalog item %s removed; %d target(s) no longer held", itemID, n)
		s.notify()
	}
	return nil
}

// InvalidateProfileScores is called after a profile is saved.
//
// held_score is a CACHE of Score(held_quality, profile). Without this, editing a
// profile leaves every cutoff comparison running against scores computed under
// the old preferences, and nothing detects it — the backlog is simply wrong,
// quietly, until somebody notices the wrong things being upgraded.
func (s *Service) InvalidateProfileScores(ctx context.Context, profileID string) {
	n, err := s.st.InvalidateHeldScores(ctx, profileID)
	if err != nil {
		log.Printf("acquire: could not invalidate held scores for profile %s: %v", profileID, err)
		return
	}
	if n > 0 {
		log.Printf("acquire: profile %s changed; %d cached score(s) marked stale", profileID, n)
	}
}
