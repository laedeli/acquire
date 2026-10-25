package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/laedeli/acquire/internal/store"
)

// Following what was requested.
//
// A request for a series or a movie with a TMDB id makes acquire follow the
// title: it is tracked as an imported title is (store.FollowTitle), and its
// targets come from TMDB (inventory.go) — one per episode of a series, the one
// of a movie, wanted and monitored. A series request is not failed and is not
// grabbed as a whole: it stays open while the series runs, showing how far
// acquire is with it, and its episodes are grabbed as targets — on their own
// with search.autoGrabNew, or from the missing tab. A movie request is its
// movie's target: searched from its release on, as TMDB dates it.
//
// A title acquire tracks already is not made twice: the request follows it,
// and it is monitored again. Asking for a title again finds the request there
// is, and makes it follow the title when it did not (a request from before
// follows existed).

var (
	// errFollowsSeries: a request that follows a series is grabbed episode
	// by episode, never as a whole.
	errFollowsSeries = errors.New("this request follows the series: its episodes are grabbed one by one, on their own or from the missing tab")
	// errNotRead: the movie a request follows has no target yet — TMDB was
	// not read for it yet.
	errNotRead = errors.New("this request's movie has not been read from TMDB yet — try again in a minute")
)

// followKind is the kind of title a request of a media type follows: series
// (the console says series, TMDB says tv), or movie; "" for one that follows
// nothing.
func followKind(mediaType string) string {
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "series", "tv":
		return "series"
	case "movie":
		return "movie"
	}
	return ""
}

// mediaTypesOf are the media types a request of a kind of title is recorded
// under: a series' under series (and tv, by a client that sent TMDB's word).
func mediaTypesOf(kind string) []string {
	if kind == "series" {
		return []string{"series", "tv"}
	}
	return []string{kind}
}

// Request records a new WantedItem. A request for a series or a movie with a
// TMDB id follows the title (follow); one without stays a request of its own,
// as before: pending until an admin grabs it.
func (s *Service) Request(ctx context.Context, w store.Wanted, sub string) (store.Wanted, error) {
	// What a request says of itself is what was asked for; what it is now,
	// acquire says.
	w.Status, w.Detail, w.ItemID, w.TitleID = "", "", "", ""
	w.ID = newID()
	w.RequestedBy = sub
	if w.MediaType == "" {
		w.MediaType = "movie"
	}
	kind := followKind(w.MediaType)
	if kind == "series" {
		w.MediaType = "series"
	}
	if kind == "" || w.TMDBID <= 0 || s.st == nil {
		return s.createRequest(ctx, w)
	}
	// One request per title: asking again finds the request there is.
	if prev, err := s.st.FindRequestFor(ctx, w.TMDBID, mediaTypesOf(kind)); err == nil {
		if prev.TitleID != "" {
			return prev, nil
		}
		title, err := s.follow(ctx, kind, w)
		if err != nil {
			return store.Wanted{}, err
		}
		status, detail := followStarted(kind)
		if err := s.st.LinkWanted(ctx, prev.ID, title.ID, status, detail); err != nil {
			return store.Wanted{}, err
		}
		s.notify()
		return s.st.GetWanted(ctx, prev.ID)
	} else if !errors.Is(err, store.ErrNotFound) {
		return store.Wanted{}, err
	}
	title, err := s.follow(ctx, kind, w)
	if err != nil {
		return store.Wanted{}, err
	}
	w.TitleID = title.ID
	w.Status, w.Detail = followStarted(kind)
	return s.createRequest(ctx, w)
}

func (s *Service) createRequest(ctx context.Context, w store.Wanted) (store.Wanted, error) {
	if err := s.st.CreateWanted(ctx, w); err != nil {
		return store.Wanted{}, err
	}
	out, err := s.st.GetWanted(ctx, w.ID)
	if err == nil {
		s.notify()
	}
	return out, err
}

// followStarted is what a request reads as once it follows its title, until
// TMDB was read for it.
func followStarted(kind string) (status, detail string) {
	if kind == "series" {
		return "following", "reading its episodes from TMDB"
	}
	return "pending", "reading its release dates from TMDB"
}

// follow makes acquire follow the title a request names, and asks for it to
// be read from TMDB first.
func (s *Service) follow(ctx context.Context, kind string, w store.Wanted) (store.Title, error) {
	title, _, err := s.st.FollowTitle(ctx, store.Title{TMDBID: w.TMDBID, Kind: kind, Title: strings.TrimSpace(w.Title), Year: w.Year})
	if err != nil {
		return store.Title{}, fmt.Errorf("follow %s: %w", w.Title, err)
	}
	// Its targets are TMDB's (inventory.go); what the library holds of them is
	// the catalog's to say, asked once they are there.
	s.RequestInventory(title.ID)
	return title, nil
}

// RemoveRequest removes a request. The last request that follows a title
// ends its follow (store.UnfollowTitle): a title the request made is not
// hunted any more; one acquire tracked before keeps what it had.
func (s *Service) RemoveRequest(ctx context.Context, id string) error {
	w, err := s.st.GetWanted(ctx, id)
	if err != nil {
		return err
	}
	if err := s.st.DeleteWanted(ctx, id); err != nil {
		return err
	}
	if w.TitleID != "" {
		if err := s.st.UnfollowTitle(ctx, w.TitleID); err != nil {
			return err
		}
	}
	s.notify()
	return nil
}

// ── what a request shows ───────────────────────────────────────────────────

// RequestView is a request as the console shows it. Of one that follows a
// title, the status and the detail are what acquire does with the title now,
// Progress says how far it is, and AutoGrab whether what it waits for is
// grabbed on its own (the search and grab policy).
type RequestView struct {
	store.Wanted
	Progress *store.Progress `json:"progress,omitempty"`
	AutoGrab *bool           `json:"autoGrab,omitempty"`
}

// Requests lists the requests, newest first, each as it reads now.
func (s *Service) Requests(ctx context.Context) ([]RequestView, error) {
	rows, err := s.st.ListWanted(ctx)
	if err != nil {
		return nil, err
	}
	return s.views(ctx, rows)
}

// RequestView is one request as it reads now.
func (s *Service) RequestView(ctx context.Context, w store.Wanted) (RequestView, error) {
	out, err := s.views(ctx, []store.Wanted{w})
	if err != nil || len(out) == 0 {
		return RequestView{Wanted: w}, err
	}
	return out[0], nil
}

func (s *Service) views(ctx context.Context, rows []store.Wanted) ([]RequestView, error) {
	var ids []string
	for _, w := range rows {
		if w.TitleID != "" {
			ids = append(ids, w.TitleID)
		}
	}
	progress, err := s.st.TitleProgress(ctx, ids)
	if err != nil {
		return nil, err
	}
	grabs := s.grabPolicy(ctx).GrabsAnything()
	now := s.now()
	out := make([]RequestView, 0, len(rows))
	for _, w := range rows {
		v := RequestView{Wanted: w}
		if p, ok := progress[w.TitleID]; ok && w.TitleID != "" {
			p := p
			v.Progress = &p
			v.Status, v.Detail = requestState(p, now)
			v.AutoGrab = &grabs
		}
		out = append(out, v)
	}
	return out, nil
}

// RequestStatus is the newest request for a TMDB id, as it reads now, or
// ErrNotFound.
func (s *Service) RequestStatus(ctx context.Context, tmdbID int64) (RequestView, error) {
	w, err := s.st.FindWantedByTMDB(ctx, tmdbID)
	if err != nil {
		return RequestView{}, err
	}
	return s.RequestView(ctx, w)
}

// requestState is the status and the detail of a request that follows a
// title, from how far acquire is with it: a series stays following while it
// runs or misses episodes, and is fulfilled once it ended and the library
// holds every episode; a movie reads as its target is.
func requestState(p store.Progress, now time.Time) (status, detail string) {
	if p.Kind == "movie" {
		return movieState(p, now)
	}
	switch {
	case p.Episodes == 0 && p.InventoryErr != "":
		return "following", clip("its episodes could not be read from TMDB yet: "+p.InventoryErr+"; tried again on a later refresh", 300)
	case p.InventoryAt == nil:
		return "following", "reading its episodes from TMDB"
	case p.Episodes == 0:
		return "following", "TMDB lists no episodes of it yet; it is read again on every refresh while it runs"
	case !p.Running && p.Held == p.Episodes:
		return "fulfilled", fmt.Sprintf("complete: all %s are in the library", count(p.Episodes, "episode", "episodes"))
	}
	parts := []string{fmt.Sprintf("%d in the library", p.Held)}
	if p.OnTheWay > 0 {
		parts = append(parts, fmt.Sprintf("%d on the way", p.OnTheWay))
	}
	if p.Missing > 0 {
		m := fmt.Sprintf("%d missing", p.Missing)
		if p.Failed > 0 {
			m += fmt.Sprintf(" (the last grab of %d failed)", p.Failed)
		}
		parts = append(parts, m)
	}
	if p.Unaired > 0 {
		parts = append(parts, fmt.Sprintf("%d not aired yet", p.Unaired))
	}
	detail = count(p.Episodes, "episode", "episodes") + ": " + strings.Join(parts, ", ")
	if !p.HoldingsKnown {
		detail += "; the catalog has not said yet what the library holds of it"
	}
	if p.Next != nil {
		detail += fmt.Sprintf("; next S%02dE%02d on %s", p.Next.Season, p.Next.Episode, p.Next.AirDate.UTC().Format("2006-01-02"))
	}
	if !p.Running {
		detail += "; TMDB says it has ended"
	}
	return "following", clip(detail, 300)
}

// movieState is a requested movie's status, as its target is.
func movieState(p store.Progress, now time.Time) (status, detail string) {
	t := p.Movie
	switch {
	case t == nil && p.InventoryErr != "":
		return "pending", clip("its release dates could not be read from TMDB yet: "+p.InventoryErr+"; tried again on a later refresh", 300)
	case t == nil:
		return "pending", "reading its release dates from TMDB"
	case t.State == store.TargetHeld && t.HeldPlayable:
		return "fulfilled", "in the library"
	case t.State == store.TargetHeld:
		return "packaging", "in the library; being packaged"
	case store.InFlight(t.State):
		return "downloading", t.Detail
	case t.State == store.TargetFailed:
		return "failed", t.Detail
	case t.ReleasePending:
		return "waiting", "TMDB dates no release to watch at home yet; it is searched once it does"
	case t.WindowAt != nil && t.WindowAt.After(now):
		return "waiting", "out " + releaseWords(t) + "; searched from then on"
	}
	detail = "out " + releaseWords(t)
	switch t.SearchOutcome {
	case store.SearchFound:
		detail += "; a release was found"
	case store.SearchNone:
		detail += "; searched, nothing found yet"
	case store.SearchError:
		detail += "; the last search failed"
	default:
		detail += "; not searched yet"
	}
	if !p.HoldingsKnown {
		detail += "; the catalog has not said yet whether the library holds it"
	}
	return "pending", detail
}

// releaseWords says when and how a requested movie comes out: "digitally on
// 2026-11-25". A movie whose window is its theatrical release says so.
func releaseWords(t *store.Target) string {
	if t.WindowAt == nil {
		return "already"
	}
	on := "on " + t.WindowAt.UTC().Format("2006-01-02")
	switch t.ReleaseKind {
	case "digital":
		return "digitally " + on
	case "physical":
		return "on disc " + on
	case "tv":
		return "on TV " + on
	case "theatrical":
		return fmt.Sprintf("%s, %d days after it came to cinemas (TMDB dates no release to watch at home)", on, int(theatricalWait.Hours()/24))
	}
	return on
}

// count is "1 episode", "13 episodes".
func count(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// ── a request's grab ───────────────────────────────────────────────────────

// requestTarget is the target a request's grab is for: the movie it
// follows. Nothing for a request that follows nothing (ok false); a series
// request has none — its episodes are grabbed one by one.
func (s *Service) requestTarget(ctx context.Context, w store.Wanted) (store.Target, store.Title, bool, error) {
	if w.TitleID == "" {
		return store.Target{}, store.Title{}, false, nil
	}
	title, err := s.st.TitleByID(ctx, w.TitleID)
	if err != nil {
		return store.Target{}, store.Title{}, true, err
	}
	if title.Kind != "movie" {
		return store.Target{}, title, true, errFollowsSeries
	}
	t, err := s.st.TargetAt(ctx, title.ID, nil, nil)
	if errors.Is(err, store.ErrNotFound) {
		return store.Target{}, title, true, errNotRead
	}
	return t, title, true, err
}
