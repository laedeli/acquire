package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/laedeli/acquire/internal/arrivals"
	"github.com/laedeli/acquire/internal/clientapi"
	"github.com/laedeli/acquire/internal/katalog"
	"github.com/laedeli/acquire/internal/store"
)

// Copy-on-import.
//
// The download clients the chart runs work on a volume of their own, so a
// download they finish is not yet where the catalog takes files from. With
// ACQUIRE_ARRIVALS_ROOT set, a finished download outside the arrivals is
// queued (store.Import) and a worker, one import at a time:
//
//  1. copies the video and the subtitle files the catalog pairs with it into
//     <arrivals>/acquire/<request>/<client>-<job>/ (package arrivals: one
//     sequential pass per file, a temporary name, synced, checked, renamed);
//  2. hands the copy to the catalog exactly as before, POST /api/ingest with
//     its path under the arrivals — the catalog's contract is unchanged, only
//     the file it is given is now acquire's copy;
//  3. releases the download in its client, for the clients the deployment
//     runs: an NZB right after its verified copy (clientapi.NZBGet.Release), a
//     torrent once it stopped seeding at its goal, deleted with its data
//     through qBittorrent's API. acquire itself never moves or deletes a file of
//     a client's.
//
// A copy or hand-over that fails leaves the download where it is and is tried
// again later, a minute after the first failure and up to an hour apart. A
// download already inside the arrivals is handed over in place, as before.

const (
	// importTick is how often the worker looks for due work when nothing woke it.
	importTick = 30 * time.Second
	// importLease holds a claimed import against a second replica while it is
	// copied; a holder that dies leaves it to be claimed again after this.
	importLease = time.Hour
	// seedingCheck is how often a torrent still seeding is looked at again.
	seedingCheck = 10 * time.Minute
	// leftoverAge is how long a copy's temporary file must have gone unwritten
	// before it counts as abandoned: twice the lease, after which no holder
	// still copies it (a copy writes its file all the time).
	leftoverAge = 2 * importLease
	// leftoverSweep is how often the worker looks for abandoned temporary files.
	leftoverSweep = time.Hour
)

// importBackoff is the wait before another attempt after n failed ones.
func importBackoff(n int) time.Duration {
	if n < 1 {
		n = 1
	}
	if n > 7 {
		return time.Hour
	}
	return min(time.Minute<<(n-1), time.Hour)
}

// copiesIntoArrivals reports whether a finished file is copied into the
// arrivals before it is handed over: imports are copies, and the file lies
// outside the arrivals.
func (s *Service) copiesIntoArrivals(path string) bool {
	root := s.cfg.ArrivalsRoot
	if root == "" || path == "" {
		return false
	}
	rel, err := filepath.Rel(root, filepath.Clean(path))
	return err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// queueImport records a finished download for the worker and wakes it.
func (s *Service) queueImport(ctx context.Context, ev eventRef, wantedID string) {
	if _, err := s.st.EnqueueImport(ctx, store.Import{
		Adapter: ev.adapter, ClientJobID: ev.jobID, WantedID: wantedID, Files: ev.files,
	}); err != nil {
		log.Printf("acquire: queue import %s/%s: %v", ev.adapter, ev.jobID, err)
		s.setStatus(ctx, wantedID, "failed", "the finished download could not be queued for import: "+err.Error())
		return
	}
	s.setStatus(ctx, wantedID, "downloading", "downloaded; copying into the library")
	s.wakeImports()
}

// eventRef is the part of a completed event an import needs.
type eventRef struct {
	adapter, jobID string
	files          []string
}

func (s *Service) wakeImports() {
	select {
	case s.importWake <- struct{}{}:
	default:
	}
}

// RunImports works through due imports, one at a time, until ctx ends. A
// target grab's download is always one (handed over in place when it lies in
// the arrivals already, or when there are none); a request's only when it is
// copied.
func (s *Service) RunImports(ctx context.Context) {
	if s.st == nil {
		return
	}
	t := time.NewTicker(importTick)
	defer t.Stop()
	var swept time.Time
	for {
		if now := time.Now(); now.Sub(swept) >= leftoverSweep {
			s.removeLeftovers(now)
			swept = now
		}
		for ctx.Err() == nil && s.importNext(ctx) {
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.importWake:
		}
	}
}

// removeLeftovers takes away the temporary files of copies into the arrivals
// that were killed mid-way (a pod killed for memory, a node lost), under the
// arrivals folder acquire copies into alone; each is said in the log.
func (s *Service) removeLeftovers(now time.Time) {
	if s.cfg.ArrivalsRoot == "" {
		return
	}
	removed, n, err := arrivals.RemoveLeftovers(filepath.Join(s.cfg.ArrivalsRoot, "acquire"), leftoverAge, now)
	for _, p := range removed {
		log.Printf("acquire: removed %s, the temporary file of a copy into the arrivals that was killed mid-way", p)
	}
	if len(removed) > 0 {
		log.Printf("acquire: removed %d abandoned temporary file(s), %d bytes", len(removed), n)
	}
	if err != nil {
		log.Printf("acquire: look for abandoned temporary files in the arrivals: %v", err)
	}
}

// importNext works on the import due first, if any, and reports whether there
// was one.
func (s *Service) importNext(ctx context.Context) bool {
	im, ok, err := s.st.ClaimImport(ctx, importLease)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("acquire: claim an import: %v", err)
		}
		return false
	}
	if !ok {
		return false
	}
	switch {
	case im.State == store.ImportPending && store.IsTargetGrab(im.WantedID):
		s.runTargetImport(ctx, im)
	case im.State == store.ImportPending:
		s.runImport(ctx, im)
	case im.State == store.ImportImported:
		s.releaseImport(ctx, im)
	}
	return true
}

// runImport copies a finished download into the arrivals and hands it over.
func (s *Service) runImport(ctx context.Context, im store.Import) {
	save := func() {
		// Saved even when ctx ends: an interrupted copy must not wait out the lease.
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
		s.setStatus(ctx, im.WantedID, "downloading",
			clip(fmt.Sprintf("downloaded; %s, trying again in %s: %v", what, wait.Round(time.Second), err), 300))
	}

	w, err := s.st.GetWanted(ctx, im.WantedID)
	if errors.Is(err, store.ErrNotFound) {
		finish(store.ImportDone, "the request is gone; nothing copied")
		return
	}
	if err != nil {
		retry("the request could not be read", err)
		return
	}
	if s.cfg.ArrivalsRoot == "" {
		// Queued for a copy while the arrivals were set; there is nowhere to
		// copy it to until they are again.
		retry("the copy waits", errors.New("ACQUIRE_ARRIVALS_ROOT is not set"))
		return
	}
	c, err := s.st.GetDownloadClient(ctx, im.Adapter)
	if err != nil {
		retry("the download client could not be read", err)
		return
	}
	files := mapClientPaths(im.Files, c.RemotePath, c.LocalPath)
	video := katalog.ResolveVideo(files)
	if video == "" {
		finish(store.ImportFailed, "no video file in the completed download")
		s.setStatus(ctx, w.ID, "failed", "no video file in the completed download")
		return
	}

	dir := filepath.Join(s.cfg.ArrivalsRoot, "acquire", safeName(im.WantedID), safeName(im.Adapter+"-"+im.ClientJobID))
	dst := filepath.Join(dir, filepath.Base(video))
	// A copy made by an attempt whose hand-over failed is still good: check it
	// rather than write it again.
	if im.Arrival != dst || arrivals.Verify(video, dst) != nil {
		// Subtitles first, so the video never lies in the arrivals without them.
		for _, side := range append(arrivals.Sidecars(video), video) {
			if err := arrivals.Copy(ctx, side, filepath.Join(dir, filepath.Base(side))); err != nil {
				retry("copying into the library failed", err)
				return
			}
		}
		im.Arrival = dst
	}

	res, err := s.ingestMovie(ctx, movieIngest(dst, w))
	var refusal *katalog.IngestError
	switch {
	case errors.As(err, &refusal) && refusal.Holder() != "":
		// The library holds the movie already: the request is served.
		res.ItemID = refusal.Holder()
		_ = s.st.SetItemID(ctx, w.ID, res.ItemID)
		s.setStatus(ctx, w.ID, "fulfilled", "the library holds it already, as item "+res.ItemID)
		im.Attempts = 0
		if !releases(c) {
			finish(store.ImportDone, "held already; the client keeps the download")
			return
		}
		finish(store.ImportImported, "held already; the client still holds the download")
		if c.Type == "nzbget" {
			s.releaseImport(ctx, im)
		}
		return
	case errors.As(err, &refusal) && refusal.Refused():
		finish(store.ImportFailed, "the catalog refused the copy: "+err.Error())
		s.setStatus(ctx, w.ID, "failed", "ingest failed: "+err.Error())
		return
	case err != nil:
		retry("the hand-over to the catalog failed", err)
		return
	}
	_ = s.st.SetItemID(ctx, w.ID, res.ItemID)
	s.setStatus(ctx, w.ID, "packaging", "copied into the library and ingested; pipeline running")

	im.Attempts = 0
	if !releases(c) {
		finish(store.ImportDone, "copied and handed over; the client keeps the download")
		return
	}
	finish(store.ImportImported, "copied and handed over; the client still holds the download")
	if c.Type == "nzbget" {
		// Verified when it was copied: released right away.
		s.releaseImport(ctx, im)
	}
}

// releases reports whether acquire releases a client's downloads once they are
// imported: the clients the deployment runs, whose types it knows.
func releases(c store.DownloadClient) bool {
	return c.Managed && (c.Type == "qbittorrent" || c.Type == "nzbget")
}

// releaseImport gives an imported download up in its client: an NZB at once,
// a torrent once it stopped seeding at its goal. A client that cannot be asked
// is asked again later.
func (s *Service) releaseImport(ctx context.Context, im store.Import) {
	save := func(state, detail string, next time.Duration) {
		im.State, im.Detail, im.NextAt = state, detail, time.Now().Add(next)
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if err := s.st.SaveImport(sctx, im); err != nil {
			log.Printf("acquire: save import %s/%s: %v", im.Adapter, im.ClientJobID, err)
		}
	}
	later := func(what string, err error) {
		im.Attempts++
		save(store.ImportImported, fmt.Sprintf("%s: %v", what, err), importBackoff(im.Attempts))
	}
	c, err := s.st.GetDownloadClient(ctx, im.Adapter)
	if errors.Is(err, store.ErrNotFound) || (err == nil && !releases(c)) {
		save(store.ImportDone, "copied and handed over; the client is no longer one acquire releases in", 0)
		return
	}
	if err != nil {
		later("the download client could not be read", err)
		return
	}
	secret, err := s.openClientSecret(c)
	if err != nil {
		later("the client's secret cannot be opened", err)
		return
	}
	hc := s.policy.HTTPClient(30 * time.Second)
	switch c.Type {
	case "nzbget":
		id, err := strconv.Atoi(im.ClientJobID)
		if err != nil {
			save(store.ImportDone, "copied and handed over; the job id is no NZBGet record", 0)
			return
		}
		if err := clientapi.NewNZBGet(c.BaseURL, c.Username, secret, hc).Release(ctx, id); err != nil {
			later("releasing it in nzbget failed", err)
			return
		}
		save(store.ImportDone, "copied, handed over and released in nzbget, which removes it with its files", 0)
	case "qbittorrent":
		qb := clientapi.NewQBittorrent(c.BaseURL, c.Username, secret, hc)
		t, found, err := qb.Torrent(ctx, im.ClientJobID)
		switch {
		case err != nil:
			later("asking qbittorrent failed", err)
		case !found:
			save(store.ImportDone, "copied and handed over; qbittorrent no longer holds the torrent", 0)
		case !t.SeedingDone():
			im.Attempts = 0
			save(store.ImportImported, "copied and handed over; seeding until its goal ("+t.State+")", seedingCheck)
		default:
			if err := qb.DeleteWithData(ctx, t.Hash); err != nil {
				later("removing it from qbittorrent failed", err)
				return
			}
			save(store.ImportDone, "copied, handed over, seeded to its goal and removed with its data", 0)
		}
	}
}

// openClientSecret opens a client's stored secret ("" for none).
func (s *Service) openClientSecret(c store.DownloadClient) (string, error) {
	if c.Auth == "none" || len(c.SecretCT) == 0 {
		return "", nil
	}
	table, id, field := store.ClientSecretAAD(c.ID)
	pt, err := s.box.Open(table, id, field, c.SecretCT, c.SecretKID)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

// safeName keeps a path element to letters, digits and . _ -, so an id names
// one folder and nothing more.
func safeName(s string) string {
	out := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			return r
		}
		return '_'
	}, s)
	if strings.Trim(out, ".") == "" {
		return "_"
	}
	return out
}
