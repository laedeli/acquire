// Package app is acquire's orchestration: it turns requests + admin grabs into
// gateway commands, and reacts to download/pipeline EVENTS to advance request
// status (downloading → packaging → fulfilled). Every status change is published
// to SSE. It owns no HTTP — httpapi drives it; events.Consumer feeds it.
package app

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/laedeli/acquire/internal/config"
	"github.com/laedeli/acquire/internal/configsync"
	"github.com/laedeli/acquire/internal/endpoint"
	"github.com/laedeli/acquire/internal/events"
	"github.com/laedeli/acquire/internal/gateway"
	"github.com/laedeli/acquire/internal/indexer"
	"github.com/laedeli/acquire/internal/katalog"
	"github.com/laedeli/acquire/internal/release"
	"github.com/laedeli/acquire/internal/secretbox"
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
	bus Bus

	// Download client configuration: credentials sealed by box, pushed to the
	// gateway by sync, admin-entered addresses checked by policy, release files
	// fetched through fetch (which applies the same policy at dial time).
	box    *secretbox.Box
	sync   *configsync.Reconciler
	policy endpoint.Policy
	fetch  *http.Client
	types  typeCache

	// Search sources: asked through ixc (the same policy at dial time), at most
	// len(slots) requests at once across every search in the process. Results
	// handed to the console carry a reference sealed by refs, a key that exists
	// only for the life of this process, instead of the source's link.
	ixc    *indexer.Client
	slots  chan struct{}
	refs   *secretbox.Box
	timing searchTiming
}

func New(cfg config.Config, st *store.Store, gw *gateway.Client, kc *katalog.Client, tm *tmdb.Client, bus Bus, box *secretbox.Box) *Service {
	policy := endpoint.Policy{
		Deny:          cfg.EndpointDeny,
		AllowInternal: cfg.EndpointAllowInternal,
		Namespace:     cfg.PodNamespace,
		ClusterDomain: cfg.ClusterDomain,
	}
	svc := &Service{cfg: cfg, st: st, gw: gw, kc: kc, tm: tm, bus: bus,
		box: box, policy: policy, fetch: policy.HTTPClient(90 * time.Second),
		ixc:   &indexer.Client{HTTP: policy.HTTPClient(2 * typedPerSource)},
		slots: make(chan struct{}, maxSourceRequests), refs: processKey(), timing: defaultTiming}
	if st != nil {
		svc.sync = configsync.New(st, gw, box, 30*time.Second)
	}
	return svc
}

// processKey is a random key for sealing release references. Nothing sealed
// with it outlives the process, which is the point: a reference is only good
// until the next restart.
func processKey() *secretbox.Box {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return &secretbox.Box{}
	}
	b, err := secretbox.New(base64.StdEncoding.EncodeToString(raw), "")
	if err != nil {
		return &secretbox.Box{}
	}
	return b
}

// RunConfigSync keeps the gateway running the stored download clients until
// ctx ends.
func (s *Service) RunConfigSync(ctx context.Context) {
	if s.sync != nil {
		s.sync.Run(ctx)
	}
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

// AutoGrabEnabled reports whether a grab can happen at all: some protocol has
// both an enabled search source and an enabled client to hand it to.
func (s *Service) AutoGrabEnabled(ctx context.Context) bool { return s.Coverage(ctx).CanGrab() }

// setStatus updates + pings subscribers.
func (s *Service) setStatus(ctx context.Context, id, status, detail string) {
	if err := s.st.SetStatus(ctx, id, status, detail); err != nil {
		log.Printf("acquire: set status %s=%s: %v", id, status, err)
		return
	}
	s.notify()
}

// ── request-side (commands, called by httpapi) ──────────────────────────────

var errNoReleases = errors.New("no releases found")

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

// Grab hands a concrete source (magnet, release-file URL or hoster link) for a
// request to a download client via the gateway, tagging the wanted id so the
// completed event maps back. client names a client id; "" routes by what the
// link turns out to be.
func (s *Service) Grab(ctx context.Context, wantedID, source, client string) error {
	w, err := s.st.GetWanted(ctx, wantedID)
	if err != nil {
		return err
	}
	out, err := s.handOff(ctx, handoff{
		WantedID: wantedID, Title: w.Title, Link: source, ClientID: client, Reason: "manual source",
	})
	if err != nil {
		s.failGrab(ctx, wantedID, err)
		return err
	}
	s.setStatus(ctx, wantedID, "downloading", "grabbed via "+out.Client.ID)
	return nil
}

func protoLabel(protocol string) string {
	switch protocol {
	case "usenet":
		return "NZB"
	case "torrent":
		return "torrent"
	}
	return "link"
}

// Profiles / SaveProfile / DeleteProfile expose the quality profiles the
// console's settings tab edits.
func (s *Service) Profiles(ctx context.Context) ([]store.QualityProfile, error) {
	return s.st.ListProfiles(ctx)
}

func (s *Service) SaveProfile(ctx context.Context, p store.QualityProfile) error {
	if err := s.st.SaveProfile(ctx, p); err != nil {
		return err
	}
	// held_score is a cache of Score(held_quality, this profile). Saving without
	// invalidating leaves every cutoff comparison running against the old
	// preferences, wrong and silent.
	s.InvalidateProfileScores(ctx, p.ID)
	return nil
}

func (s *Service) DeleteProfile(ctx context.Context, id string) error {
	return s.st.DeleteProfile(ctx, id)
}

// ScoringProfile returns the profile ranking would actually use: the one named,
// or the default when id is empty or unknown. The second result is the id that
// was really used, so a caller can report it rather than assume.
func (s *Service) ScoringProfile(ctx context.Context, id string) (release.Profile, string) {
	if id != "" {
		all, err := s.st.ListProfiles(ctx)
		if err == nil {
			for _, p := range all {
				if p.ID == id {
					return p.Config, p.ID
				}
			}
		}
	}
	return s.st.DefaultProfile(ctx), "default"
}

// TMDB exposes the metadata client so an admin-triggered derivation can use the
// same configured credentials as discovery rather than taking a key over HTTP.
func (s *Service) TMDB() *tmdb.Client { return s.tm }

// Discover proxies a TMDB multi-search, flagging in-library hits.
func (s *Service) Discover(ctx context.Context, q string) []DiscoverHit {
	results, _ := s.tm.Search(ctx, q)
	out := make([]DiscoverHit, 0, len(results))
	// The catalog is a dependency that can be down. Log the FIRST failure per
	// call rather than once per result (a 20-hit search would otherwise emit 20
	// identical lines), and mark the affected rows unknown instead of claiming
	// the user does not own them.
	var libErrLogged bool
	for _, r := range results {
		avail, err := s.kc.InLibrary(ctx, r.Title)
		if err != nil && !libErrLogged {
			libErrLogged = true
			log.Printf("acquire: library check unavailable, discovery results are unannotated: %v", err)
		}
		out = append(out, DiscoverHit{
			TMDBID: r.TMDBID, MediaType: r.MediaType, Title: r.Title, Year: r.Year,
			PosterURL: r.PosterURL, Overview: r.Overview,
			InLibrary:    avail == katalog.InLibraryYes,
			LibraryState: avail.String(),
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
	// LibraryState distinguishes "not in library" from "could not tell", so the
	// console can say so instead of showing a confidently wrong answer.
	LibraryState string `json:"libraryState"`
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
	// The client reports paths as IT sees them; acquire reads the same folder
	// under its own mount.
	files := ev.Files
	if c, err := s.st.GetDownloadClient(ctx, ev.Adapter); err == nil {
		files = mapClientPaths(files, c.RemotePath, c.LocalPath)
	}
	video := katalog.ResolveVideo(files)
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
	req := katalog.IngestRequest{Path: video, Type: typ, Title: w.Title, Year: yearPtr}
	if typ == "episode" {
		// katalog rejects an episode without coordinates, and it is right to:
		// a NULL parent produces a playable orphan with no error anywhere.
		// Until the request model carries them (P6), a single-file series
		// download cannot say WHICH episode it is — so fail loudly here rather
		// than send an ingest we know will be refused, or worse, one that would
		// have created an orphan on an older katalog.
		s.setStatus(ctx, w.ID, "failed",
			"series downloads need episode coordinates before ingest (parent, season, episode)")
		return nil
	}
	res, err := s.kc.Ingest(ctx, req)
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
		OnStarted:     s.OnStarted,
		OnProgress:    s.OnProgress,
		OnCompleted:   s.OnCompleted,
		OnFailed:      s.OnFailed,
		OnPackaged:    s.OnPackaged,
		OnScheduleDue: s.OnScheduleDue,
	}
}
