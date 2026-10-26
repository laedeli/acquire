package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/laedeli/acquire/internal/arrivals"
	"github.com/laedeli/acquire/internal/katalog"
	"github.com/laedeli/acquire/internal/release"
	"github.com/laedeli/acquire/internal/store"
)

// A target grab's finished download into the catalog.
//
// The import worker (imports.go) takes it like a request's: one at a time,
// copying into <arrivals>/acquire/<grab>/<client>-<job>/ first when the files
// lie outside the arrivals (one sequential pass per file, synced, checked,
// renamed), and in place otherwise. Then, file by file (episodes.go maps them):
//
//   - an episode goes over by identity — its series' TMDB id (else TVDB or
//     IMDb, whichever acquire holds), its season and episode, and episodeEnd
//     for a file of several — and a movie with its own identity;
//   - the catalog's answer holds the targets the file covers: the item it
//     names, and for the episodes a file covers after its first, theirs; a
//     reconcile in the next half hour does not take them back, as katalog-api
//     may not list them yet;
//   - a 409 says the file was not taken in. The episode it names — the
//     file's first, by which the catalog refuses a file of several — or the
//     movie is held by the item that holds it, with nothing recorded of a
//     file acquire did not bring; every other episode of the file is wanted
//     again, the release blocked for it;
//   - a catalog that does not take ingest by identity yet (a 422, or a 400
//     on those fields) leaves the targets waiting: the copy stays, the
//     download is kept, and the hand-over is tried again, half an hour after
//     the first refusal and up to six hours apart;
//   - any other refusal, a file of more than ten episodes in a row, and a
//     target the download holds no file for fail their targets by their
//     release: blocked for them, wanted again after a wait (failure
//     recovery, store.FailGrabTargets). A season pack without a file for an
//     episode only lets go of it — wanted again, the pack blocked for it,
//     nothing failed.
//
// A copy the catalog did not take is deleted from the arrivals. The download
// is released in its managed client only when every target the grab still
// holds is held — its file handed over and taken, or held already — and
// nothing of it went over in place, where the client's file is the library's.
// Otherwise the grab fails, saying why, and the client keeps the download.

// catalogWait is the wait before handing a file over again to a catalog that
// does not take ingest by identity yet, after n refusals.
func catalogWait(n int) time.Duration {
	if n < 1 {
		n = 1
	}
	if n > 5 {
		return 6 * time.Hour
	}
	return min(30*time.Minute<<(n-1), 6*time.Hour)
}

// inFlight are the states of a target its grab still answers for.
var inFlight = []string{store.TargetGrabbed, store.TargetDownloaded, store.TargetWaiting}

// live reports whether a grab still answers for a target: it is the grab's,
// and not answered yet.
func live(t store.Target, grabID string) bool {
	return t.GrabID == grabID && store.InFlight(t.State)
}

// importPass is what one attempt at a grab's hand-over came to.
type importPass struct {
	handed, already, waiting, refused, missing, letGo int
	waitingWhy                                        string
	// kept says why the client keeps the download: a file refused, a target
	// without a file. Empty when the library has all of it.
	kept []string
	// reconcile is set when what the library holds of the title is the
	// reconcile's to say: a file of several, a conflict.
	reconcile bool
}

func (p *importPass) keep(why string) { p.kept = append(p.kept, why) }

// runTargetImport copies a target grab's finished download into the arrivals
// when it has to, and hands each of its files over for the targets it covers.
func (s *Service) runTargetImport(ctx context.Context, im store.Import) {
	save := func() {
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if err := s.st.SaveImport(sctx, im); err != nil {
			log.Printf("acquire: save import %s/%s: %v", im.Adapter, im.ClientJobID, err)
		}
	}
	finish := func(state, detail string) {
		im.State, im.Detail, im.NextAt = state, detail, time.Now()
		save()
	}
	retry := func(what string, err error) {
		im.Attempts++
		wait := importBackoff(im.Attempts)
		im.Detail, im.NextAt = fmt.Sprintf("%s: %v", what, err), time.Now().Add(wait)
		save()
		_, _ = s.st.MoveGrabTargets(ctx, im.WantedID, []string{store.TargetDownloaded, store.TargetGrabbed}, store.TargetDownloaded,
			clip(fmt.Sprintf("downloaded; %s, trying again in %s: %v", what, wait.Round(time.Second), err), 300))
		s.notify()
	}

	g, err := s.st.GetTargetGrab(ctx, im.WantedID)
	if errors.Is(err, store.ErrNotFound) {
		finish(store.ImportDone, "the grab is gone; nothing handed over")
		return
	}
	if err != nil {
		retry("the grab could not be read", err)
		return
	}
	if g.State == store.GrabFailed {
		// Given up meanwhile — cancelled, or stale: nothing more goes over.
		finish(store.ImportDone, clip("the grab was given up ("+g.Detail+"); nothing more handed over", 300))
		s.dropCopies(g.ID, s.untakenCopies(ctx, g.ID))
		return
	}
	title, err := s.st.TitleByID(ctx, g.TitleID)
	if errors.Is(err, store.ErrNotFound) {
		finish(store.ImportFailed, "the title is no longer tracked; nothing handed over")
		_ = s.st.SetTargetGrabState(ctx, g.ID, store.GrabFailed, "the title is no longer tracked")
		return
	}
	if err != nil {
		retry("the title could not be read", err)
		return
	}
	targets, err := s.st.TargetsByID(ctx, g.TargetIDs)
	if err != nil {
		retry("the targets could not be read", err)
		return
	}
	c, err := s.st.GetDownloadClient(ctx, im.Adapter)
	files := im.Files
	switch {
	case err == nil:
		files = mapClientPaths(files, c.RemotePath, c.LocalPath)
	case !errors.Is(err, store.ErrNotFound):
		retry("the download client could not be read", err)
		return
	}
	videos := listVideos(files)
	if len(videos) == 0 {
		const why = "no video file in the completed download"
		var answer []store.Target
		for _, t := range targets {
			if live(t, g.ID) {
				answer = append(answer, t)
			}
		}
		s.failByRelease(ctx, g, im, answer, fileFor{}, "", why)
		_ = s.st.SetTargetGrabState(ctx, g.ID, store.GrabFailed, why)
		finish(store.ImportFailed, why+"; kept in the client")
		s.notify()
		return
	}
	// A download any of whose files lies in the arrivals already goes over in
	// place, or partly so: the client's file is the library's then, and the
	// client is never told to delete it.
	inPlace := false
	for _, v := range videos {
		inPlace = inPlace || !s.copiesIntoArrivals(v.Path)
	}
	var plan episodePlan
	pack := false
	if title.Kind == "movie" {
		plan = planMovie(videos, targets)
	} else {
		plan = planEpisodes(videos, targets, g.ReleaseTitle)
		pack = release.ParseCoverage(g.ReleaseTitle).Pack()
	}

	// The grab's id first: two downloads a client gave one job id never share
	// a folder.
	dir := filepath.Join(s.cfg.ArrivalsRoot, "acquire", safeName(g.ID), safeName(im.Adapter+"-"+im.ClientJobID))
	used := map[string]bool{}
	var pass importPass
	var drop []string // copies the catalog did not take
	for _, f := range plan.Files {
		var answer []store.Target
		for _, t := range f.Targets {
			if live(t, g.ID) {
				answer = append(answer, t)
			}
		}
		if len(answer) == 0 {
			continue // answered on an earlier attempt, or no longer this grab's
		}
		path := f.File.Path
		if n := len(f.Coverage.Episodes); n > katalog.MaxCovered {
			// The catalog takes one file for at most MaxCovered episodes in a
			// row; this one cannot be handed over as what it is.
			why := clip(fmt.Sprintf("%s covers %d episodes in a row (%s); the catalog takes at most %d in one file",
				filepath.Base(path), n, f.Coverage.String(), katalog.MaxCovered), 300)
			s.failByRelease(ctx, g, im, answer, f, path, why)
			pass.refused++
			pass.keep(why)
			continue
		}
		copied := false
		if s.copiesIntoArrivals(path) {
			// Of a pack whose seasons share file names, each one in a folder
			// of its own.
			name := filepath.Base(path)
			dst := filepath.Join(dir, name)
			if used[name] {
				dst = filepath.Join(dir, safeName(f.Coverage.String()), name)
			}
			used[name] = true
			if arrivals.Verify(path, dst) != nil {
				// Subtitles first, so the video never lies in the arrivals
				// without them.
				for _, side := range append(arrivals.Sidecars(path), path) {
					if err := arrivals.Copy(ctx, side, filepath.Join(filepath.Dir(dst), filepath.Base(side))); err != nil {
						retry("copying into the library failed", err)
						return
					}
				}
			}
			path, copied = dst, true
		}

		var res katalog.IngestResult
		var err error
		if title.Kind == "movie" {
			res, err = s.ingestMovie(ctx, movieTargetIngest(title, path))
		} else {
			res, err = s.kc.Ingest(ctx, episodeIngest(title, f.Coverage, path))
		}
		var ie *katalog.IngestError
		switch {
		case err == nil:
			s.holdFile(ctx, g, im, title, f, answer, path, res.ItemID,
				"in the library as item "+res.ItemID+"; pipeline running")
			pass.handed++
			// The items of the episodes a file covers after its first are its
			// answer's covered, but not in an order that names them: one the
			// library held already is left out of it. What each episode is held
			// by is the holdings' to say.
			pass.reconcile = pass.reconcile || f.Coverage.Multi()
		case errors.As(err, &ie) && ie.Holder() != "":
			s.conflicted(ctx, g, im, f, answer, path, ie, &pass)
			if copied {
				drop = append(drop, path)
			}
		case errors.As(err, &ie) && ie.Unsupported():
			pass.waiting++
			pass.waitingWhy = clip("waiting for the catalog: it does not take episodes by identity yet ("+ie.Message()+")", 300)
			for _, t := range answer {
				_, _ = s.st.MoveGrabTarget(ctx, g.ID, t.ID, inFlight, store.TargetWaiting, pass.waitingWhy)
				_ = s.st.RecordGrabFile(ctx, store.GrabFile{Adapter: im.Adapter, ClientJobID: im.ClientJobID, GrabID: g.ID,
					TargetID: t.ID, State: "waiting", Path: path, EpisodeEnd: episodeEnd(f.Coverage), Detail: pass.waitingWhy})
			}
		case errors.As(err, &ie) && ie.Refused():
			why := clip("the catalog refused "+filepath.Base(path)+": "+ie.Message(), 300)
			s.failByRelease(ctx, g, im, answer, f, path, why)
			pass.refused++
			pass.keep(why)
			if copied {
				drop = append(drop, path)
			}
		default:
			retry("the hand-over to the catalog failed", err)
			return
		}
	}
	for _, t := range plan.Missing {
		if !live(t, g.ID) {
			continue
		}
		if pack {
			// A season pack that holds no file for an episode is just not that
			// episode's: it is wanted again, the pack blocked for it.
			s.letGo(ctx, g, im, t, "the pack "+g.ReleaseTitle+" held no file for it; wanted again, the pack blocked for it")
			pass.letGo++
			continue
		}
		why := "the download holds no file for " + coords(t)
		s.failByRelease(ctx, g, im, []store.Target{t}, fileFor{}, "", why)
		pass.missing++
		pass.keep(why)
	}
	if pass.letGo > 0 && len(plan.Unnamed) > 0 {
		// The files that name no episode may be the ones the pack seemed to
		// lack: the client keeps the download for someone to look at.
		pass.keep(clip(fmt.Sprintf("%d episode(s) of the pack have no file, and %d of its files name no episode (%s)",
			pass.letGo, len(plan.Unnamed), strings.Join(plan.Unnamed, ", ")), 300))
	}
	s.dropCopies(g.ID, drop)
	if pass.already > 0 || pass.reconcile {
		// "Held already" is the catalog's word for one file — the item it names
		// may hold another episode, or contradict the name — and a file of
		// several holds its episodes through items it does not name in order:
		// what the library holds of the title is the reconcile's to say.
		s.RequestReconcile(title.ID)
	}
	summary := importSummary(pass, plan.Left)
	s.notify()
	if pass.waiting > 0 {
		im.Attempts++
		wait := catalogWait(im.Attempts)
		im.Detail, im.NextAt = clip(pass.waitingWhy+"; tried again in "+wait.String(), 300), time.Now().Add(wait)
		save()
		_ = s.st.SetTargetGrabState(ctx, g.ID, store.GrabDownloaded, clip(summary, 300))
		return
	}
	if why := s.notAllHeld(ctx, g.ID, pass); why != "" {
		// The library does not have all of it: the client keeps the download,
		// for someone to look at, and the grab fails saying why.
		_ = s.st.SetTargetGrabState(ctx, g.ID, store.GrabFailed, clip(why+" · "+summary, 300))
		finish(store.ImportFailed, clip("kept in the client: "+why+" · "+summary, 300))
		s.dropCopies(g.ID, s.untakenCopies(ctx, g.ID))
		return
	}
	_ = s.st.SetTargetGrabState(ctx, g.ID, store.GrabImported, clip(summary, 300))
	im.Attempts = 0
	switch {
	case inPlace:
		finish(store.ImportDone, clip("handed over in place: the client's file is the library's, and the client keeps it · "+summary, 300))
	case !releases(c):
		finish(store.ImportDone, clip("handed over; the client keeps the download · "+summary, 300))
	default:
		finish(store.ImportImported, clip("handed over; the client still holds the download · "+summary, 300))
		if c.Type == "nzbget" {
			s.releaseImport(ctx, im)
		}
	}
}

// notAllHeld says why the library does not have all a grab holds — a file
// refused, a target without a file, any target the grab still holds that is
// not held — or "" when it has.
func (s *Service) notAllHeld(ctx context.Context, grabID string, pass importPass) string {
	if len(pass.kept) > 0 {
		why := pass.kept[0]
		if n := len(pass.kept) - 1; n > 0 {
			why += fmt.Sprintf(" (and %d more)", n)
		}
		return why
	}
	g, err := s.st.GetTargetGrab(ctx, grabID)
	if err != nil {
		return "what it holds could not be read: " + err.Error()
	}
	targets, err := s.st.TargetsByID(ctx, g.TargetIDs)
	if err != nil {
		return "what it holds could not be read: " + err.Error()
	}
	for _, t := range targets {
		if t.State != store.TargetHeld {
			return coords(t) + " is " + t.State + ": " + t.Detail
		}
	}
	if len(targets) < len(g.TargetIDs) {
		return "a target it holds is gone"
	}
	return ""
}

// failByRelease fails targets of a grab its release failed — a file refused,
// none for them — when they are still the grab's and in flight: blocked for
// them, they are wanted again after a wait (store.FailGrabTargets). Each one
// says why, and that the client keeps the download.
func (s *Service) failByRelease(ctx context.Context, g store.TargetGrab, im store.Import, targets []store.Target, f fileFor, path, why string) {
	if len(targets) == 0 {
		return
	}
	ids := make([]string, 0, len(targets))
	for _, t := range targets {
		ids = append(ids, t.ID)
	}
	detail := clip(why+"; the download stays in its client, the release blocked for it", 300)
	if _, err := s.st.FailGrabTargets(ctx, g.ID, ids, inFlight, detail, true); err != nil {
		log.Printf("acquire: fail the targets of %s: %v", g.ID, err)
	}
	state := "failed"
	if path == "" {
		state = "missing"
	}
	for _, t := range targets {
		_ = s.BlockFailedRelease(ctx, t.ID, g.ReleaseTitle, g.Indexer, clip(why, 200))
		_ = s.st.RecordGrabFile(ctx, store.GrabFile{Adapter: im.Adapter, ClientJobID: im.ClientJobID, GrabID: g.ID,
			TargetID: t.ID, State: state, Path: path, EpisodeEnd: episodeEnd(f.Coverage), Detail: clip(why, 300)})
	}
}

// letGo takes a target off a grab whose release turned out not to bring it:
// wanted again, the release blocked for it, nothing failed.
func (s *Service) letGo(ctx context.Context, g store.TargetGrab, im store.Import, t store.Target, why string) {
	why = clip(why, 300)
	if ok, err := s.st.MoveGrabTarget(ctx, g.ID, t.ID, inFlight, store.TargetWanted, why); err != nil || !ok {
		return // held meanwhile, or no longer this grab's
	}
	_ = s.st.UnbindGrabTarget(ctx, g.ID, t.ID)
	_ = s.BlockFailedRelease(ctx, t.ID, g.ReleaseTitle, g.Indexer, clip(why, 200))
	_ = s.st.RecordGrabFile(ctx, store.GrabFile{Adapter: im.Adapter, ClientJobID: im.ClientJobID, GrabID: g.ID,
		TargetID: t.ID, State: "missing", Detail: why})
}

// codeRE finds an episode's coordinates in the catalog's words (S01E07).
var codeRE = regexp.MustCompile(`(?i)\bS(\d{1,3})E(\d{1,4})\b`)

// conflicted records a 409: the catalog did not take the file in, because
// the library holds what it is already, as the item the answer names. That
// item holds the episode the answer names — the file's first, by which the
// catalog refuses a file of several, unless its words name another — or the
// movie; acquire did not bring its file, so nothing of a quality or a release
// is recorded with it. Every other episode the file covers is not in the
// library by this file: wanted again, the release blocked for it, and the
// client keeps the download.
func (s *Service) conflicted(ctx context.Context, g store.TargetGrab, im store.Import, f fileFor, answer []store.Target,
	path string, ie *katalog.IngestError, pass *importPass) {
	holder := ie.Holder()
	named := ""
	switch {
	case len(answer) == 1 && !f.Coverage.Multi():
		named = answer[0].ID
	default:
		season, episode := f.Coverage.Season, f.Coverage.Episode
		if m := codeRE.FindStringSubmatch(ie.Message()); m != nil {
			season, _ = strconv.Atoi(m[1])
			episode, _ = strconv.Atoi(m[2])
		}
		for _, t := range answer {
			if t.SeasonNumber != nil && t.EpisodeNumber != nil && *t.SeasonNumber == season && *t.EpisodeNumber == episode {
				named = t.ID
			}
		}
	}
	pass.reconcile = true
	base := filepath.Base(path)
	for _, t := range answer {
		if t.ID == named {
			detail := "the library holds it already, as item " + holder
			if err := s.st.HoldTarget(ctx, store.Hold{TargetID: t.ID, ItemID: holder, Source: "derived",
				Detail: detail, HandedOver: true}); err != nil {
				log.Printf("acquire: hold %s: %v", t.ID, err)
			}
			_ = s.st.RecordGrabFile(ctx, store.GrabFile{Adapter: im.Adapter, ClientJobID: im.ClientJobID, GrabID: g.ID,
				TargetID: t.ID, State: "held", Path: path, EpisodeEnd: episodeEnd(f.Coverage), ItemID: holder, Detail: detail})
			pass.already++
			continue
		}
		why := clip(fmt.Sprintf("%s was not taken in (%s); %s is not in the library by it: wanted again, the release blocked for it",
			base, ie.Message(), coords(t)), 300)
		s.letGo(ctx, g, im, t, why)
		pass.refused++
		pass.keep(why)
	}
}

// untakenCopies are the copies of a grab's files in the arrivals that the
// catalog did not take — refused, waiting — as its hand-over recorded them.
func (s *Service) untakenCopies(ctx context.Context, grabID string) []string {
	files, err := s.st.GrabFiles(ctx, grabID)
	if err != nil {
		return nil
	}
	taken := map[string]bool{}
	for _, f := range files {
		if f.State == "imported" {
			taken[f.Path] = true
		}
	}
	var out []string
	for _, f := range files {
		if f.Path != "" && !taken[f.Path] {
			out = append(out, f.Path)
		}
	}
	return out
}

// dropCopies deletes copies the catalog did not take from the arrivals, with
// the subtitles copied next to each, and the grab's folders they leave empty.
// Only copies under the grab's own folder are touched: a file handed over in
// place is the client's.
func (s *Service) dropCopies(grabID string, paths []string) {
	if s.cfg.ArrivalsRoot == "" || len(paths) == 0 {
		return
	}
	root := filepath.Join(s.cfg.ArrivalsRoot, "acquire", safeName(grabID))
	for _, p := range paths {
		rel, err := filepath.Rel(root, filepath.Clean(p))
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		for _, side := range arrivals.Sidecars(p) {
			_ = os.Remove(side)
		}
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("acquire: remove the copy %s: %v", p, err)
		}
		for dir := filepath.Dir(p); strings.HasPrefix(dir, root); dir = filepath.Dir(dir) {
			if os.Remove(dir) != nil {
				break // not empty
			}
		}
	}
}

// holdFile records the catalog's answer for one file it took on the targets
// it holds: its first episode is the item named; each one after it is held
// through that item's file (held_via), by it until a reconcile names the
// episode's own item — the holder's packaged event makes them all playable,
// and its removal takes them all. A reconcile in the next half hour does not
// take them back (Hold.HandedOver).
func (s *Service) holdFile(ctx context.Context, g store.TargetGrab, im store.Import, title store.Title, f fileFor,
	answer []store.Target, path, itemID, detail string) {
	profile, profileID := s.ScoringProfile(ctx, title.ProfileID)
	name := strings.TrimSuffix(filepath.Base(f.File.Path), filepath.Ext(f.File.Path))
	if release.Parse(name).Resolution == "" {
		name = g.ReleaseTitle // an obfuscated file name says nothing of its quality
	}
	size := f.File.Size
	if size < 0 {
		size = 0
	}
	v, in := release.Score(release.Candidate{Title: name, Protocol: g.Protocol, SizeMb: size >> 20}, profile)
	quality := map[string]any{"resolution": in.Resolution, "source": in.Source, "codec": in.Codec}
	for _, t := range answer {
		via := ""
		if f.Coverage.Multi() && t.EpisodeNumber != nil && *t.EpisodeNumber > f.Coverage.Episode {
			via = itemID
		}
		if err := s.st.HoldTarget(ctx, store.Hold{
			TargetID: t.ID, ItemID: itemID, Via: via, Release: name, Source: "grab", ProfileID: profileID,
			Quality: quality, Score: v.Score, Detail: clip(detail, 300), HandedOver: true,
		}); err != nil {
			log.Printf("acquire: hold %s: %v", t.ID, err)
		}
		_ = s.st.RecordGrabFile(ctx, store.GrabFile{Adapter: im.Adapter, ClientJobID: im.ClientJobID, GrabID: g.ID,
			TargetID: t.ID, State: "imported", Path: path, EpisodeEnd: episodeEnd(f.Coverage), ItemID: itemID, Detail: clip(detail, 300)})
	}
}

// episodeIngest is a file of episodes as ingest by identity takes it.
func episodeIngest(title store.Title, cov release.Coverage, path string) katalog.IngestRequest {
	season, first := int32(cov.Season), int32(cov.Episode)
	req := katalog.IngestRequest{
		Path: path, Type: "episode", Title: title.Title,
		Series: seriesIdentity(title), SeasonNumber: &season, EpisodeNumber: &first,
	}
	if cov.EpisodeEnd > cov.Episode {
		end := int32(cov.EpisodeEnd)
		req.EpisodeEnd = &end
	}
	if title.Year != 0 {
		y := int32(title.Year)
		req.Year = &y
	}
	return req
}

// seriesIdentity names a series by every identity acquire holds of it, its
// TMDB id — which every tracked title has, and which the catalog tries first —
// then its TVDB and IMDb ids: a series the catalog knows only by its TVDB id is
// found by that, instead of made a second time from the TMDB id. The
// holdings are asked by the same ids in the same order (holdings.go).
func seriesIdentity(t store.Title) *katalog.Identity {
	return katalog.NewIdentity(t.TMDBID, t.TVDBID, t.IMDBID)
}

// movieTargetIngest is a tracked movie's file, with its identities.
func movieTargetIngest(title store.Title, path string) katalog.IngestRequest {
	req := katalog.IngestRequest{Path: path, Type: "movie", Title: title.Title}
	if title.Year != 0 {
		y := int32(title.Year)
		req.Year = &y
	}
	req.Identity = katalog.NewIdentity(title.TMDBID, 0, title.IMDBID)
	return req
}

func episodeEnd(c release.Coverage) *int {
	if c.EpisodeEnd > c.Episode {
		e := c.EpisodeEnd
		return &e
	}
	return nil
}

// importSummary says what a grab's hand-over did, for its detail line.
func importSummary(p importPass, left []string) string {
	var parts []string
	add := func(n int, what string) {
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, what))
		}
	}
	add(p.handed, "handed over")
	add(p.already, "held already")
	add(p.waiting, "waiting for the catalog")
	add(p.refused, "refused")
	add(p.missing, "without a file")
	add(p.letGo, "not in the pack, let go")
	if len(left) > 0 {
		parts = append(parts, "left out: "+strings.Join(left, "; "))
	}
	if len(parts) == 0 {
		return "nothing to hand over"
	}
	return strings.Join(parts, " · ")
}
