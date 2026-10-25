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

// Requester is who asks for a title: their subject, and whether they are an
// admin.
type Requester struct {
	Sub   string
	Admin bool
}

// Request records a new WantedItem. A request for a series or a movie with a
// TMDB id follows the title (follow); one without stays a request of its own,
// as before: pending until an admin grabs it. created reports whether the
// request is new: asking for a title again answers the request there is.
//
// A request that follows a title is approved at once when an admin makes it,
// or requests.autoApprove is on; otherwise it awaits an admin's approval, and
// changes nothing meanwhile: a title acquire tracks stays as it is; one it
// does not is created, followed and monitored by nobody — TMDB is read for
// what it says of it, its episodes made once it is approved — and nothing of
// it is searched or grabbed (store.Title.HeldForApproval). An admin asking
// again for a title that awaits approval approves its request.
func (s *Service) Request(ctx context.Context, w store.Wanted, by Requester) (out store.Wanted, created bool, err error) {
	// What a request says of itself is what was asked for; what it is now,
	// acquire says.
	w.Status, w.Detail, w.ItemID, w.TitleID = "", "", "", ""
	w.Approval, w.DecidedBy, w.DecidedAt = "", "", nil
	w.ID = newID()
	w.RequestedBy = by.Sub
	if w.MediaType == "" {
		w.MediaType = "movie"
	}
	kind := followKind(w.MediaType)
	if kind == "series" {
		w.MediaType = "series"
	}
	if kind == "" || w.TMDBID <= 0 || s.st == nil {
		out, err := s.createRequest(ctx, w)
		return out, err == nil, err
	}
	approval, decidedBy := store.ApprovalAwaiting, ""
	switch {
	case by.Admin:
		approval, decidedBy = store.ApprovalApproved, by.Sub
		if decidedBy == "" {
			decidedBy = "acquire"
		}
	case s.RequestSettings(ctx).AutoApprove:
		// Approved by the setting, not by whoever asked.
		approval, decidedBy = store.ApprovalApproved, decidedByAutoApprove
	}
	// One request per title: asking again finds the request there is — an
	// admin asking approves it.
	again := func(prev store.Wanted) (store.Wanted, bool, error) {
		if !by.Admin || prev.Approval == store.ApprovalApproved {
			return prev, false, nil
		}
		out, err := s.ApproveRequest(ctx, prev.ID, decidedBy)
		return out, false, err
	}
	if prev, err := s.st.FindRequestFor(ctx, w.TMDBID, mediaTypesOf(kind)); err == nil {
		if prev.TitleID != "" {
			return again(prev)
		}
		title, err := s.requested(ctx, kind, w, approval)
		if err != nil {
			return store.Wanted{}, false, err
		}
		status, detail := followStarted(kind)
		if err := s.st.LinkWanted(ctx, prev.ID, title.ID, status, detail, approval, decidedBy); err != nil {
			return store.Wanted{}, false, err
		}
		s.notify()
		out, err := s.st.GetWanted(ctx, prev.ID)
		return out, false, err
	} else if !errors.Is(err, store.ErrNotFound) {
		return store.Wanted{}, false, err
	}
	title, err := s.requested(ctx, kind, w, approval)
	if err != nil {
		return store.Wanted{}, false, err
	}
	w.TitleID = title.ID
	w.Status, w.Detail = followStarted(kind)
	w.Approval, w.DecidedBy = approval, decidedBy
	if approval == store.ApprovalApproved {
		now := time.Now()
		w.DecidedAt = &now
	}
	out, err = s.createRequest(ctx, w)
	if errors.Is(err, store.ErrExists) {
		// Asked at once from elsewhere: the request made there.
		prev, err := s.st.FindRequestFor(ctx, w.TMDBID, mediaTypesOf(kind))
		if err != nil {
			return store.Wanted{}, false, err
		}
		return again(prev)
	}
	return out, err == nil, err
}

// decidedByAutoApprove is who approved a request requests.autoApprove
// approved.
const decidedByAutoApprove = "autoApprove"

// requested is the title a request asks for: followed when the request is
// approved (follow), else only asked for (store.RequestTitle) — read from
// TMDB for what it says of it, nothing of anyone's monitoring changed.
func (s *Service) requested(ctx context.Context, kind string, w store.Wanted, approval string) (store.Title, error) {
	if approval == store.ApprovalApproved {
		return s.follow(ctx, kind, w)
	}
	title, _, err := s.st.RequestTitle(ctx, store.Title{TMDBID: w.TMDBID, Kind: kind, Title: strings.TrimSpace(w.Title), Year: w.Year})
	if err != nil {
		return store.Title{}, fmt.Errorf("request %s: %w", w.Title, err)
	}
	s.RequestInventory(title.ID)
	return title, nil
}

// errNoFollow: approving or declining a request that follows no title.
var errNoFollow = errors.New("this request follows no title: there is nothing to approve or decline")

// ApproveRequest approves a request that follows a title — awaiting
// approval, or declined before —: the title is followed, its episodes made
// from TMDB, and what it follows is grabbed on its own as the search and grab
// policy allows. A title whose follow ended when the request was declined is
// followed again.
func (s *Service) ApproveRequest(ctx context.Context, id, by string) (store.Wanted, error) {
	w, err := s.st.GetWanted(ctx, id)
	if err != nil {
		return store.Wanted{}, err
	}
	if w.TitleID == "" {
		return store.Wanted{}, errNoFollow
	}
	if err := s.st.DecideWanted(ctx, id, store.ApprovalApproved, by); err != nil {
		return store.Wanted{}, err
	}
	if title, err := s.st.TitleByID(ctx, w.TitleID); err == nil && !title.Followed() {
		if _, err := s.follow(ctx, title.Kind, store.Wanted{TMDBID: title.TMDBID, Title: title.Title, Year: title.Year}); err != nil {
			return store.Wanted{}, err
		}
	}
	s.notify()
	return s.st.GetWanted(ctx, id)
}

// DeclineRequest declines a request that follows a title: it stays, saying
// so, and its title's follow, if it was approved before, ends
// (store.UnfollowTitle).
func (s *Service) DeclineRequest(ctx context.Context, id, by string) (store.Wanted, error) {
	w, err := s.st.GetWanted(ctx, id)
	if err != nil {
		return store.Wanted{}, err
	}
	if w.TitleID == "" {
		return store.Wanted{}, errNoFollow
	}
	if err := s.st.DecideWanted(ctx, id, store.ApprovalDeclined, by); err != nil {
		return store.Wanted{}, err
	}
	if err := s.st.UnfollowTitle(ctx, w.TitleID); err != nil {
		return store.Wanted{}, err
	}
	s.notify()
	return s.st.GetWanted(ctx, id)
}

// IsNoFollow reports whether an approval was refused because the request
// follows no title.
func IsNoFollow(err error) bool { return errors.Is(err, errNoFollow) }

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
	pol := s.grabPolicy(ctx)
	grabs := pol.GrabsAnything()
	now := s.now()
	out := make([]RequestView, 0, len(rows))
	for _, w := range rows {
		v := RequestView{Wanted: w}
		if p, ok := progress[w.TitleID]; ok && w.TitleID != "" {
			p := p
			v.Progress = &p
			status, detail := requestState(p, now, pol)
			v.Status, v.Detail = approvalState(w.Approval, p, status, detail)
			auto := grabs && w.Approval == store.ApprovalApproved && !p.HandsOff && (p.Movie == nil || !guessedWindow(*p.Movie))
			v.AutoGrab = &auto
		}
		out = append(out, v)
	}
	return out, nil
}

// approvalState is a request's status and detail as an admin's word on it
// leaves them. One awaiting approval says so first: of a title a request
// created, that nothing of it is searched or grabbed until then — acquire
// knows only what TMDB says of it —; of a title acquire tracked before, that
// it goes on with it as before the request. A declined one says so alone.
func approvalState(approval string, p store.Progress, status, detail string) (string, string) {
	switch {
	case approval == store.ApprovalDeclined:
		return "declined", "an admin declined it: acquire does not follow it for this request"
	case approval != store.ApprovalAwaiting || status == "fulfilled":
		return status, detail
	case !p.FromRequest:
		return "awaiting", clip("waiting for an admin's approval; until then acquire goes on with it as before the request · "+detail, 300)
	}
	known := ""
	switch {
	case p.InventoryErr != "" && (p.InventoryAt == nil || p.InventoryRetryAt != nil):
		known = " · " + notRead("what TMDB says of it", p)
	case p.InventoryAt == nil:
		known = " · reading what TMDB says of it"
	case p.Kind == "series":
		known = " · TMDB lists " + count(p.Seasons, "season", "seasons") + " of it; its episodes are tracked once it is approved"
	case p.Movie != nil && p.Movie.AirDate != nil:
		known = " · " + releaseWords(p.Movie, time.Now())
	}
	return "awaiting", clip("waiting for an admin's approval: until then nothing of it is searched or grabbed"+known, 300)
}

// ApproveForGrab approves a request for a movie awaiting approval that an
// admin grabs — a find & grab, a release picked for it, a link handed over
// —: the grab is their word on it. A series request is grabbed episode by
// episode, never as a whole: none of those approves it.
func (s *Service) ApproveForGrab(ctx context.Context, id, by string) error {
	w, err := s.st.GetWanted(ctx, id)
	if err != nil || w.TitleID == "" || w.Approval != store.ApprovalAwaiting {
		return nil
	}
	if title, err := s.st.TitleByID(ctx, w.TitleID); err != nil || title.Kind != "movie" {
		return nil
	}
	_, err = s.ApproveRequest(ctx, id, by)
	return err
}

// LegacyStatus is a request's status in the five words a request read in
// before it could follow a title — pending, downloading, packaging,
// fulfilled, failed —, for a client that knows those alone: a request
// waiting for its release, for an admin's approval or following a series
// with nothing on the way is pending; one following a series with episodes
// on the way, downloading; a declined one, failed.
func LegacyStatus(v RequestView) string {
	switch v.Status {
	case "waiting", "awaiting":
		return "pending"
	case "following":
		if v.Progress != nil && v.Progress.OnTheWay > 0 {
			return "downloading"
		}
		return "pending"
	case "declined":
		return "failed"
	}
	return v.Status
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
// holds every episode; a movie reads as its target is (movieState), searched
// from when the policy opens it to an automatic grab (openAt).
func requestState(p store.Progress, now time.Time, pol SearchSettings) (status, detail string) {
	if p.Kind == "movie" {
		return movieState(p, now, pol)
	}
	switch {
	case p.InventoryErr != "" && (p.Episodes == 0 || p.InventoryRetryAt != nil):
		return "following", clip(notRead("its episodes", p), 300)
	case p.InventoryAt == nil, p.Episodes == 0 && p.Seasons > 0 && !p.EpisodesRead:
		return "following", "reading its episodes from TMDB"
	case p.Episodes == 0 && p.Running:
		return "following", "TMDB lists no episodes of it yet; it is read again on every refresh while it runs"
	case p.Episodes == 0:
		return "following", "TMDB lists no episodes of it, and says it does not run: it is read again weekly"
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
	switch {
	case p.HandsOff:
		detail += "; its releases name no season and episode (an anime, a daily show): grabbed from the missing tab only"
	case pol.exactAirTimes() && p.ScheduleAt == nil && p.Missing+p.Unaired > 0:
		detail += "; nothing of it is grabbed on its own until TVmaze's air times of it are read"
		if p.ScheduleErr != "" {
			detail += " (" + p.ScheduleErr + ")"
		}
	}
	return "following", clip(detail, 300)
}

// movieState is a requested movie's status, as its target is, saying what
// happens next: while TMDB dates no release of it, it is read again daily;
// it is searched from the day it comes out to watch at home, the air delay
// after the day TMDB dates — or, when TMDB dates it in cinemas only, 90 days
// after it came to cinemas, read again daily meanwhile in case TMDB dates a
// release to watch at home sooner.
func movieState(p store.Progress, now time.Time, pol SearchSettings) (status, detail string) {
	t := p.Movie
	switch {
	case t == nil && p.InventoryErr != "":
		return "pending", clip(notRead("its release dates", p), 300)
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
		return "waiting", "TMDB dates no release of it yet: it is read again daily, and searched once it comes out to watch at home — " +
			fmt.Sprintf("or %d days after it comes to cinemas, if TMDB dates nothing else", int(theatricalWait.Hours()/24))
	}
	from := searchedFrom(t, pol)
	if from != nil && from.After(now) {
		return "waiting", clip(releaseWords(t, now)+"; searched from "+from.UTC().Format("2006-01-02 15:04 UTC")+" on"+guessWords(t), 300)
	}
	detail = releaseWords(t, now)
	if from != nil && t.ReleaseKind == "theatrical" {
		detail += "; searched since " + from.UTC().Format("2006-01-02") + guessWords(t)
	}
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
	return "pending", clip(detail, 300)
}

// searchedFrom is when a requested movie is searched from: its window, and
// the air delay after the day it comes out (openAt, as store's autoOpenSQL);
// nil for a movie the import tracks.
func searchedFrom(t *store.Target, pol SearchSettings) *time.Time {
	if t.WindowAt == nil {
		return nil
	}
	at := openAt(store.Title{Kind: "movie"}, *t, pol)
	return &at
}

// releaseWords says when and how a requested movie comes out, or came out:
// "comes out digitally on 2026-11-25", "out on disc since 2026-10-02". One
// TMDB dates in cinemas only says so.
func releaseWords(t *store.Target, now time.Time) string {
	if t.AirDate == nil {
		return "out"
	}
	day := t.AirDate.UTC().Format("2006-01-02")
	past := !t.AirDate.After(now)
	how := map[string]string{"digital": "digitally", "physical": "on disc", "tv": "on TV"}[t.ReleaseKind]
	switch {
	case t.ReleaseKind == "theatrical" && past:
		return "in cinemas since " + day + ", and TMDB dates no release to watch at home"
	case t.ReleaseKind == "theatrical":
		return "comes to cinemas on " + day + ", and TMDB dates no release to watch at home"
	case past && how != "":
		return "out " + how + " since " + day
	case past:
		return "out since " + day
	case how != "":
		return "comes out " + how + " on " + day
	}
	return "comes out on " + day
}

// guessWords says, of a movie whose window is set from its theatrical
// release, how that window came about, that nothing grabs it on its own
// meanwhile, and that it may change.
func guessWords(t *store.Target) string {
	if t.ReleaseKind != "theatrical" {
		return ""
	}
	return fmt.Sprintf(", %d days after it came to cinemas: until TMDB dates one, only an admin grabs it — a release before one is "+
		"most often a recording made in a cinema; TMDB is read again daily", int(theatricalWait.Hours()/24))
}

// notRead says why what TMDB says of a followed title could not be read, and
// when it is read again: every 15 minutes while TMDB does not answer, once a
// TMDB key is configured without one, a week on for a title TMDB does not
// know.
func notRead(what string, p store.Progress) string {
	switch {
	case p.InventoryErr == noTMDB:
		return what + " cannot be read: " + noTMDB
	case p.InventoryAt == nil:
		return fmt.Sprintf("%s could not be read from TMDB yet: %s; tried again every %d minutes until they are",
			what, p.InventoryErr, int(followRetry.Minutes()))
	case p.InventoryRetryAt != nil:
		return what + " could not be read from TMDB: " + p.InventoryErr + "; read again on " + p.InventoryRetryAt.UTC().Format("2006-01-02")
	}
	return what + " could not be read from TMDB: " + p.InventoryErr + "; read again on a later refresh"
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
