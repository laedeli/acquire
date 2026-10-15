package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path/filepath"
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
//     names, and for the episodes a file covers after its first, theirs;
//   - a 409 naming the item that holds the episode already is "already
//     held": the targets are held by that item, not failed, and a reconcile
//     of the title checks it;
//   - a catalog that does not take ingest by identity yet (a 422, or a 400
//     on those fields) leaves the targets waiting: the copy stays, the
//     download is not released, and the hand-over is tried again, half an
//     hour after the first refusal and up to six hours apart;
//   - any other refusal fails the targets with the catalog's reason.
//
// Once every target has its answer the download is released in its client,
// as a request's is.

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

// live reports whether a grab still answers for a target: it is the grab's,
// and not answered yet.
func live(t store.Target, grabID string) bool {
	return t.GrabID == grabID &&
		(t.State == store.TargetDownloaded || t.State == store.TargetWaiting || t.State == store.TargetGrabbed)
}

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
		finish(store.ImportFailed, why)
		_ = s.st.SetTargetGrabState(ctx, g.ID, store.GrabFailed, why)
		_, _ = s.st.MoveGrabTargets(ctx, g.ID, []string{store.TargetDownloaded, store.TargetWaiting, store.TargetGrabbed},
			store.TargetFailed, why)
		s.notify()
		return
	}
	var plan episodePlan
	if title.Kind == "movie" {
		plan = planMovie(videos, targets)
	} else {
		plan = planEpisodes(videos, targets, g.ReleaseTitle)
	}

	dir := filepath.Join(s.cfg.ArrivalsRoot, "acquire", safeName(g.ID), safeName(im.Adapter+"-"+im.ClientJobID))
	used := map[string]bool{}
	var handed, already, waiting, refusedN, missing int
	var waitingWhy string
	reconcile := false
	refuse := func(f fileFor, answer []store.Target, path, why string) {
		refusedN++
		for _, t := range answer {
			_, _ = s.st.SetTargetState(ctx, t.ID, nil, store.TargetFailed, why)
			_ = s.st.RecordGrabFile(ctx, store.GrabFile{Adapter: im.Adapter, ClientJobID: im.ClientJobID, GrabID: g.ID,
				TargetID: t.ID, State: "failed", Path: path, EpisodeEnd: episodeEnd(f.Coverage), Detail: why})
		}
	}
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
		if n := f.Coverage.EpisodeEnd - f.Coverage.Episode + 1; f.Coverage.Multi() && n > katalog.MaxCovered {
			// The catalog takes one file for at most MaxCovered episodes in a
			// row; this one cannot be handed over as what it is.
			refuse(f, answer, path, clip(fmt.Sprintf("%s covers %d episodes in a row (%s); the catalog takes at most %d in one file",
				filepath.Base(path), n, f.Coverage.String(), katalog.MaxCovered), 300))
			continue
		}
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
			path = dst
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
			handed++
			// The items of the episodes a file covers after its first are its
			// answer's covered, but not in an order that names them: one the
			// library held already is left out of it. What each episode is held
			// by is the holdings' to say.
			reconcile = reconcile || f.Coverage.Multi()
		case errors.As(err, &ie) && ie.Holder() != "":
			s.holdFile(ctx, g, im, title, f, answer, path, ie.Holder(),
				"the library holds it already, as item "+ie.Holder())
			already++
		case errors.As(err, &ie) && ie.Unsupported():
			waiting++
			waitingWhy = clip("waiting for the catalog: it does not take episodes by identity yet ("+ie.Message()+")", 300)
			for _, t := range answer {
				_, _ = s.st.SetTargetState(ctx, t.ID, nil, store.TargetWaiting, waitingWhy)
				_ = s.st.RecordGrabFile(ctx, store.GrabFile{Adapter: im.Adapter, ClientJobID: im.ClientJobID, GrabID: g.ID,
					TargetID: t.ID, State: "waiting", Path: path, EpisodeEnd: episodeEnd(f.Coverage), Detail: waitingWhy})
			}
		case errors.As(err, &ie) && ie.Refused():
			refuse(f, answer, path, clip("the catalog refused "+filepath.Base(path)+": "+ie.Message(), 300))
		default:
			retry("the hand-over to the catalog failed", err)
			return
		}
	}
	for _, t := range plan.Missing {
		if !live(t, g.ID) {
			continue
		}
		missing++
		why := "the download holds no file for " + coords(t)
		_, _ = s.st.SetTargetState(ctx, t.ID, nil, store.TargetFailed, why)
		_ = s.st.RecordGrabFile(ctx, store.GrabFile{Adapter: im.Adapter, ClientJobID: im.ClientJobID, GrabID: g.ID,
			TargetID: t.ID, State: "missing", Detail: why})
	}
	if already > 0 || reconcile {
		// "Held already" is the catalog's word for one file — the item it names
		// may hold another episode, or contradict the name — and a file of
		// several holds its episodes through items it does not name in order:
		// what the library holds of the title is the reconcile's to say.
		s.RequestReconcile(title.ID)
	}
	summary := importSummary(handed, already, waiting, refusedN, missing, plan.Left)
	s.notify()
	if waiting > 0 {
		im.Attempts++
		wait := catalogWait(im.Attempts)
		im.Detail, im.NextAt = clip(waitingWhy+"; tried again in "+wait.String(), 300), time.Now().Add(wait)
		save()
		_ = s.st.SetTargetGrabState(ctx, g.ID, store.GrabDownloaded, clip(summary, 300))
		return
	}
	_ = s.st.SetTargetGrabState(ctx, g.ID, store.GrabImported, clip(summary, 300))
	im.Attempts = 0
	if !releases(c) {
		finish(store.ImportDone, clip("handed over; the client keeps the download · "+summary, 300))
		return
	}
	finish(store.ImportImported, clip("handed over; the client still holds the download · "+summary, 300))
	if c.Type == "nzbget" {
		s.releaseImport(ctx, im)
	}
}

// holdFile records the catalog's answer for one file on the targets it holds:
// its first episode is the item named; each one after it is held through that
// item's file (held_via), by it until a reconcile names the episode's own item
// — the holder's packaged event makes them all playable, and its removal
// takes them all.
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
		item := itemID
		if err := s.st.HoldTarget(ctx, store.Hold{
			TargetID: t.ID, ItemID: item, Via: via, Release: name, Source: "grab", ProfileID: profileID,
			Quality: quality, Score: v.Score, Detail: clip(detail, 300),
		}); err != nil {
			log.Printf("acquire: hold %s: %v", t.ID, err)
		}
		_ = s.st.RecordGrabFile(ctx, store.GrabFile{Adapter: im.Adapter, ClientJobID: im.ClientJobID, GrabID: g.ID,
			TargetID: t.ID, State: "imported", Path: path, EpisodeEnd: episodeEnd(f.Coverage), ItemID: item, Detail: clip(detail, 300)})
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
func importSummary(handed, already, waiting, refused, missing int, left []string) string {
	var parts []string
	add := func(n int, what string) {
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, what))
		}
	}
	add(handed, "handed over")
	add(already, "held already")
	add(waiting, "waiting for the catalog")
	add(refused, "refused")
	add(missing, "without a file")
	if len(left) > 0 {
		parts = append(parts, "left out: "+strings.Join(left, "; "))
	}
	if len(parts) == 0 {
		return "nothing to hand over"
	}
	return strings.Join(parts, " · ")
}
