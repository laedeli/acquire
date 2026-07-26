// Package app is acquire's orchestration: it turns requests + admin grabs into
// gateway commands, and reacts to download/pipeline EVENTS to advance request
// status (downloading → packaging → fulfilled). Every status change is published
// to SSE. It owns no HTTP — httpapi drives it; events.Consumer feeds it.
package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/laedeli/acquire/internal/config"
	"github.com/laedeli/acquire/internal/events"
	"github.com/laedeli/acquire/internal/gateway"
	"github.com/laedeli/acquire/internal/katalog"
	"github.com/laedeli/acquire/internal/prowlarr"
	"github.com/laedeli/acquire/internal/store"
	"github.com/laedeli/acquire/internal/tmdb"
)

// Bus is the live channel to connected consoles: a payload-free ping to refetch
// lists, and typed events for high-frequency telemetry (download progress).
type Bus interface {
	Notify()
	Publish(name string, data any)
}

type Service struct {
	cfg config.Config
	st  *store.Store
	gw  *gateway.Client
	kc  *katalog.Client
	tm  *tmdb.Client
	pr  *prowlarr.Client
	bus Bus
}

func New(cfg config.Config, st *store.Store, gw *gateway.Client, kc *katalog.Client, tm *tmdb.Client, pr *prowlarr.Client, bus Bus) *Service {
	return &Service{cfg: cfg, st: st, gw: gw, kc: kc, tm: tm, pr: pr, bus: bus}
}

func (s *Service) notify() {
	if s.bus != nil {
		s.bus.Notify()
	}
}

func (s *Service) publish(name string, data any) {
	if s.bus != nil {
		s.bus.Publish(name, data)
	}
}

// AutoGrabEnabled reports whether Prowlarr search is configured.
func (s *Service) AutoGrabEnabled() bool { return s.pr.Enabled() }

// setStatus updates + pings subscribers.
func (s *Service) setStatus(ctx context.Context, id, status, detail string) {
	if err := s.st.SetStatus(ctx, id, status, detail); err != nil {
		log.Printf("acquire: set status %s=%s: %v", id, status, err)
		return
	}
	s.notify()
}

// ── request-side (commands, called by httpapi) ──────────────────────────────

var (
	errAutoGrabDisabled = errors.New("indexer search not configured")
	errNoReleases       = errors.New("no releases found")
)

func itoa(n int) string { return strconv.Itoa(n) }

func newID() string {
	// time-ordered enough for a request id; uniqueness from the nanosecond.
	return "w_" + strings.ReplaceAll(time.Now().UTC().Format("20060102150405.000000000"), ".", "")
}

// Request records a new WantedItem (auto-approved v1: status stays pending until
// an admin grabs).
func (s *Service) Request(ctx context.Context, w store.Wanted, sub string) (store.Wanted, error) {
	w.ID = newID()
	w.RequestedBy = sub
	if w.MediaType == "" {
		w.MediaType = "movie"
	}
	if err := s.st.CreateWanted(ctx, w); err != nil {
		return store.Wanted{}, err
	}
	out, err := s.st.GetWanted(ctx, w.ID)
	if err == nil {
		s.notify()
	}
	return out, err
}

// Grab hands a concrete source (magnet/URL) for a request to a download client
// via the gateway, tagging the wanted id so the completed event maps back.
func (s *Service) Grab(ctx context.Context, wantedID, source, adapter string) error {
	w, err := s.st.GetWanted(ctx, wantedID)
	if err != nil {
		return err
	}
	if adapter == "" {
		adapter = "qbittorrent"
	}
	res, err := s.gw.Add(ctx, gateway.AddRequest{
		Adapter:      adapter,
		Source:       source,
		Title:        w.Title,
		SavePath:     s.cfg.SavePath,
		WantedItemID: wantedID,
	})
	if err != nil {
		s.setStatus(ctx, wantedID, "failed", "grab failed: "+err.Error())
		return err
	}
	_ = s.st.RecordGrab(ctx, wantedID, adapter, res.ClientJobID, source)
	s.setStatus(ctx, wantedID, "downloading", "grabbed via "+adapter)
	return nil
}

// AutoGrab searches Prowlarr for the request's title, ranks the releases
// NZB-first, and grabs the top one through the matching gateway adapter
// (usenet→nzbget, torrent→qbittorrent). This is the *arr-style automation on top
// of the manual Grab.
func (s *Service) AutoGrab(ctx context.Context, wantedID string) error {
	w, err := s.st.GetWanted(ctx, wantedID)
	if err != nil {
		return err
	}
	if !s.pr.Enabled() {
		return errAutoGrabDisabled
	}
	query := w.Title
	if w.Year != 0 {
		query = w.Title + " " + itoa(w.Year)
	}
	// NZB-first + FAST: search the usenet indexers first (a couple of indexers,
	// ~1s). Only fall back to the slow torrent-wide fan-out when there is no NZB.
	idx, _ := s.pr.Indexers(ctx)
	var releases []prowlarr.Release
	if s.cfg.PreferUsenet {
		if usenetIDs := prowlarr.EnabledIDs(idx, "usenet"); len(usenetIDs) > 0 {
			releases, _ = s.pr.SearchIn(ctx, query, usenetIDs)
		}
	}
	ranked := prowlarr.Rank(releases, s.cfg.PreferUsenet)
	if len(ranked) == 0 {
		// Torrent fallback (or the whole set when not usenet-preferring).
		torrentIDs := prowlarr.EnabledIDs(idx, "torrent")
		rel2, err := s.pr.SearchIn(ctx, query, torrentIDs)
		if err != nil {
			s.setStatus(ctx, wantedID, "failed", "indexer search failed: "+err.Error())
			return err
		}
		ranked = prowlarr.Rank(rel2, s.cfg.PreferUsenet)
	}
	if len(ranked) == 0 {
		s.setStatus(ctx, wantedID, "failed", "no releases found on the indexers")
		return errNoReleases
	}
	best := ranked[0]
	adapter := best.Adapter()
	res, err := s.gw.Add(ctx, gateway.AddRequest{
		Adapter:      adapter,
		Source:       best.Source(),
		Title:        w.Title,
		SavePath:     s.cfg.SavePath,
		WantedItemID: wantedID,
	})
	if err != nil {
		s.setStatus(ctx, wantedID, "failed", "grab failed: "+err.Error())
		return err
	}
	proto := "NZB"
	if !best.IsUsenet() {
		proto = "torrent"
	}
	// Keep the release itself, not just its URL: the console shows what won and
	// the search results are gone by the time anyone looks.
	var seeders *int32
	if !best.IsUsenet() {
		n := int32(best.Seeders)
		seeders = &n
	}
	_ = s.st.RecordGrabRelease(ctx, store.Grab{
		WantedID: wantedID, Adapter: adapter, ClientJobID: res.ClientJobID,
		Source: best.Source(), ReleaseTitle: best.Title, Indexer: best.Indexer,
		Protocol: best.Protocol, SizeBytes: best.Size, Seeders: seeders,
		Reason: rankReason(best, s.cfg.PreferUsenet),
	})
	s.setStatus(ctx, wantedID, "downloading",
		"grabbed "+proto+" from "+best.Indexer+" via "+adapter)
	return nil
}

// rankReason explains in one line why this release was picked, so the console
// can show it next to the choice.
func rankReason(r prowlarr.Release, preferUsenet bool) string {
	if r.IsUsenet() {
		if preferUsenet {
			return "NZB preferred; largest usenet release on " + r.Indexer
		}
		return "usenet release on " + r.Indexer
	}
	return "most seeded torrent on " + r.Indexer + " (" + itoa(r.Seeders) + " seeders)"
}

// Discover proxies a TMDB multi-search, flagging in-library hits.
func (s *Service) Discover(ctx context.Context, q string) []DiscoverHit {
	results, _ := s.tm.Search(ctx, q)
	out := make([]DiscoverHit, 0, len(results))
	for _, r := range results {
		out = append(out, DiscoverHit{
			TMDBID: r.TMDBID, MediaType: r.MediaType, Title: r.Title, Year: r.Year,
			PosterURL: r.PosterURL, Overview: r.Overview,
			InLibrary: s.kc.InLibrary(ctx, r.Title),
		})
	}
	return out
}

// DiscoverHit is one discovery result annotated with in-library state.
type DiscoverHit struct {
	TMDBID    int64  `json:"tmdbId"`
	MediaType string `json:"mediaType"`
	Title     string `json:"title"`
	Year      int    `json:"year"`
	PosterURL string `json:"posterUrl"`
	Overview  string `json:"overview"`
	InLibrary bool   `json:"inLibrary"`
}

// ── event-side (reactions, called by events.Consumer) ───────────────────────

// OnCompleted: the download finished. Resolve the video file, ingest it into the
// catalog (item + primary asset + discovered → pipeline), and mark packaging.
func (s *Service) OnCompleted(ctx context.Context, ev events.DownloadEvent) error {
	// Record the terminal download state first — this is true whether or not the
	// job belongs to a request we know about.
	_ = s.saveDownload(ctx, ev, "completed")
	wantedID := ev.WantedID
	if wantedID == "" {
		if id, err := s.st.FindWantedByClientJob(ctx, ev.ClientID); err == nil {
			wantedID = id
		}
	}
	if wantedID == "" {
		return nil // not one of ours
	}
	w, err := s.st.GetWanted(ctx, wantedID)
	if err != nil {
		return nil
	}
	// Completed can legitimately arrive twice — e.g. the gateway restarts and
	// re-adopts a job, or a client re-reports history. Ingest is idempotent on
	// the path, but re-running it would knock a fulfilled request back to
	// "packaging", so stop here once the request has moved past the download.
	if w.Status == "packaging" || w.Status == "fulfilled" {
		return nil
	}
	video := katalog.ResolveVideo(ev.Files)
	if video == "" {
		s.setStatus(ctx, w.ID, "failed", "no video file in the completed download")
		return nil
	}
	typ := "movie"
	if w.MediaType == "series" || w.MediaType == "episode" {
		typ = "episode"
	}
	var yearPtr *int32
	if w.Year != 0 {
		y := int32(w.Year)
		yearPtr = &y
	}
	res, err := s.kc.Ingest(ctx, katalog.IngestRequest{
		Path: video, Type: typ, Title: w.Title, Year: yearPtr,
	})
	if err != nil {
		s.setStatus(ctx, w.ID, "failed", "ingest failed: "+err.Error())
		return nil
	}
	_ = s.st.SetItemID(ctx, w.ID, res.ItemID)
	s.setStatus(ctx, w.ID, "packaging", "ingested; pipeline running")
	return nil
}

// OnFailed: the download failed.
func (s *Service) OnFailed(ctx context.Context, ev events.DownloadEvent) error {
	_ = s.saveDownload(ctx, ev, "failed")
	wantedID := ev.WantedID
	if wantedID == "" {
		if id, err := s.st.FindWantedByClientJob(ctx, ev.ClientID); err == nil {
			wantedID = id
		}
	}
	if wantedID == "" {
		return nil
	}
	detail := ev.Error
	if detail == "" {
		detail = "download failed"
	}
	s.setStatus(ctx, wantedID, "failed", detail)
	return nil
}

// OnPackaged: the pipeline finished packaging the item → the request is served.
func (s *Service) OnPackaged(ctx context.Context, ev events.ItemEvent) error {
	if ev.ItemID == "" {
		return nil
	}
	w, err := s.st.FindWantedByItemID(ctx, ev.ItemID)
	if err != nil {
		return nil // not one of ours
	}
	s.setStatus(ctx, w.ID, "fulfilled", "packaged and playable")
	return nil
}

// OnStarted / OnProgress record download telemetry. The gateway emits progress
// every few seconds; we persist the latest snapshot and push it straight to the
// console so a progress bar moves without refetching anything.
func (s *Service) OnStarted(ctx context.Context, ev events.DownloadEvent) error {
	return s.saveDownload(ctx, ev, "queued")
}

func (s *Service) OnProgress(ctx context.Context, ev events.DownloadEvent) error {
	state := ev.State
	if state == "" {
		state = "downloading"
	}
	return s.saveDownload(ctx, ev, state)
}

// saveDownload upserts one client job's telemetry and streams it to subscribers.
func (s *Service) saveDownload(ctx context.Context, ev events.DownloadEvent, state string) error {
	if ev.Adapter == "" || ev.ClientID == "" {
		return nil
	}
	wantedID := ev.WantedID
	if wantedID == "" {
		// A job the gateway adopted after a restart has no wanted id; recover it
		// from our own grab record.
		if id, err := s.st.FindWantedByClientJob(ctx, ev.ClientID); err == nil {
			wantedID = id
		}
	}
	d := store.Download{
		Adapter: ev.Adapter, ClientJobID: ev.ClientID, WantedID: wantedID,
		Title: ev.Title, State: state, NativeState: ev.NativeState,
		ProgressPct: ev.ProgressPct, BytesDone: ev.Downloaded,
		SpeedBps: ev.SpeedBps, EtaSec: ev.EtaSec,
		Seeders: ev.Seeders, Leechers: ev.Leechers, Health: ev.Health,
		Error: ev.Error,
	}
	if ev.SizeBytes != nil {
		d.BytesTotal = *ev.SizeBytes
	}
	// Publish the MERGED row, not the sparse event: a terminal event carries
	// almost no telemetry, and the store is what reconciles the two.
	merged, err := s.st.UpsertDownload(ctx, d)
	if err != nil {
		log.Printf("acquire: upsert download %s/%s: %v", ev.Adapter, ev.ClientID, err)
		return nil
	}
	s.publish("download", merged)
	return nil
}

// Downloads lists current + recently finished downloads.
func (s *Service) Downloads(ctx context.Context, limit int) ([]store.Download, error) {
	return s.st.ListDownloads(ctx, limit)
}

// ClientsStatus proxies the gateway's per-client health/speed.
func (s *Service) ClientsStatus(ctx context.Context) ([]gateway.ClientStatus, error) {
	return s.gw.ClientsStatus(ctx)
}

// ControlDownload cancels, pauses or resumes a client job.
func (s *Service) ControlDownload(ctx context.Context, adapter, jobID, action string) error {
	switch action {
	case "pause":
		return s.gw.Pause(ctx, adapter, jobID)
	case "resume":
		return s.gw.Resume(ctx, adapter, jobID)
	case "cancel":
		if err := s.gw.Cancel(ctx, adapter, jobID); err != nil {
			return err
		}
		if err := s.st.DeleteDownload(ctx, adapter, jobID); err != nil {
			return err
		}
		s.notify()
		return nil
	}
	return fmt.Errorf("unknown action %q", action)
}

// Reconcile re-syncs from the gateway at boot. The Kafka consumer starts at the
// latest offset, so downloads that started (or finished) while acquire was down
// would otherwise be invisible until their next progress tick.
func (s *Service) Reconcile(ctx context.Context) {
	if !s.gw.Enabled() {
		return
	}
	jobs, err := s.gw.List(ctx)
	if err != nil {
		log.Printf("acquire: reconcile downloads: %v", err)
		return
	}
	for _, j := range jobs {
		_ = s.saveDownload(ctx, events.DownloadEvent{
			ClientID: j.ClientJobID, Adapter: j.Adapter, WantedID: j.WantedItemID,
			Title: j.Title, State: j.State, NativeState: j.NativeState,
			ProgressPct: j.ProgressPct, Downloaded: j.Downloaded, SizeBytes: j.SizeBytes,
			SpeedBps: j.SpeedBps, EtaSec: j.EtaSec,
			Seeders: j.Seeders, Leechers: j.Leechers, Health: j.Health,
		}, j.State)
	}
	if len(jobs) > 0 {
		log.Printf("acquire: reconciled %d in-flight download(s) from the gateway", len(jobs))
	}
}

// Handlers returns the consumer callback set bound to this service.
func (s *Service) Handlers() events.Handlers {
	return events.Handlers{
		OnStarted:   s.OnStarted,
		OnProgress:  s.OnProgress,
		OnCompleted: s.OnCompleted,
		OnFailed:    s.OnFailed,
		OnPackaged:  s.OnPackaged,
	}
}
