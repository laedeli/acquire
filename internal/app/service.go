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
	"net/http"
	"sort"
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
	"github.com/laedeli/acquire/internal/prowlarr"
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
	pr  *prowlarr.Client
	ix  *indexer.Engine
	bus Bus

	// Download client configuration: credentials sealed by box, pushed to the
	// gateway by sync, admin-entered addresses checked by policy, release files
	// fetched through fetch (which applies the same policy at dial time).
	box     *secretbox.Box
	sync    *configsync.Reconciler
	policy  endpoint.Policy
	fetch   *http.Client
	types   typeCache
	sources sourceCache
}

func New(cfg config.Config, st *store.Store, gw *gateway.Client, kc *katalog.Client, tm *tmdb.Client, pr *prowlarr.Client, bus Bus, box *secretbox.Box) *Service {
	// The typed engine talks to the SAME aggregator, but through the
	// per-indexer newznab proxy rather than /api/v1/search — the latter accepts
	// season/ep/tvdbid, returns 200 and discards them.
	ix := &indexer.Engine{
		Client:        indexer.New(cfg.IndexerURL, cfg.IndexerAPIKey),
		MaxConcurrent: 4,
		PerIndexer:    45 * time.Second,
	}
	policy := endpoint.Policy{
		Deny:          cfg.EndpointDeny,
		AllowInternal: cfg.EndpointAllowInternal,
		Namespace:     cfg.PodNamespace,
	}
	svc := &Service{cfg: cfg, st: st, gw: gw, kc: kc, tm: tm, pr: pr, ix: ix, bus: bus,
		box: box, policy: policy, fetch: policy.HTTPClient(90 * time.Second)}
	if st != nil {
		svc.sync = configsync.New(st, gw, box, 30*time.Second)
	}
	return svc
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

// AutoGrab searches for the request's title, ranks the releases by the active
// profile, and grabs the top one through the download client that handles its
// protocol.
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
	// The active quality profile decides what "best" means; the two-stage
	// fan-out only decides where to look.
	releases, err := s.searchIndexers(ctx, query, nil)
	if err != nil {
		s.setStatus(ctx, wantedID, "failed", "indexer search failed: "+err.Error())
		return err
	}
	ranked := s.rankByProfile(ctx, releases)
	for len(ranked) > 0 && ranked[len(ranked)-1].Rejected {
		ranked = ranked[:len(ranked)-1] // never auto-grab something the profile rejected
	}
	if len(ranked) == 0 {
		s.setStatus(ctx, wantedID, "failed", "no releases matched the quality profile")
		return errNoReleases
	}
	best := ranked[0]
	out, err := s.handOff(ctx, candidateHandoff(w, best, best.Reason))
	if err != nil {
		s.failGrab(ctx, wantedID, err)
		return err
	}
	s.setStatus(ctx, wantedID, "downloading",
		"grabbed "+protoLabel(out.Protocol)+" from "+best.Indexer+" via "+out.Client.ID)
	return nil
}

// candidateHandoff turns a ranked release into a hand-off. The candidate's
// client hint is ignored: routing is decided at grab time from the protocol,
// against the clients configured NOW.
func candidateHandoff(w store.Wanted, c Candidate, reason string) handoff {
	var seeders *int32
	if c.Protocol == "torrent" {
		n := int32(c.Seeders)
		seeders = &n
	}
	return handoff{
		WantedID: w.ID, Title: w.Title, Link: c.Source, Protocol: c.Protocol,
		ReleaseTitle: c.Title, Indexer: c.Indexer, Size: c.Size, Seeders: seeders, Reason: reason,
	}
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

// Candidate is one release offered by a search, already scored by the active
// quality profile so the console can show what won and why.
type Candidate struct {
	Title      string `json:"title"`
	Indexer    string `json:"indexer"`
	Protocol   string `json:"protocol"`
	Size       int64  `json:"size"`
	Seeders    int    `json:"seeders"`
	Adapter    string `json:"adapter"`
	Source     string `json:"source"`
	Reason     string `json:"reason"`
	Best       bool   `json:"best"`
	Score      int    `json:"score"`
	Rejected   bool   `json:"rejected"`
	Resolution string `json:"resolution"`
	Codec      string `json:"codec"`
	SourceType string `json:"sourceType"`
	// Provenance: which search stage produced this and what identified it.
	// A console that shows "id" vs "text" tells an operator whether the match
	// is certain or merely plausible.
	Stage      string `json:"stage,omitempty"`
	MatchedVia string `json:"matchedVia,omitempty"`
}

// rankByProfile scores every release against the active quality profile and
// returns them best first, rejected last. This replaces "NZB first, then
// biggest", which kept choosing bloated multi-language remuxes.
func (s *Service) rankByProfile(ctx context.Context, releases []prowlarr.Release) []Candidate {
	profile := s.st.DefaultProfile(ctx)
	routes := s.clientRoutes(ctx)
	out := make([]Candidate, 0, len(releases))
	for _, r := range releases {
		v, info := release.Score(release.Candidate{
			Title:    r.Title,
			Protocol: r.Protocol,
			SizeMb:   r.Size / (1024 * 1024),
			Seeders:  r.Seeders,
		}, profile)
		out = append(out, Candidate{
			Title: r.Title, Indexer: r.Indexer, Protocol: r.Protocol, Size: r.Size,
			Seeders: r.Seeders, Adapter: routes[r.Protocol], Source: r.Source(),
			Reason: v.Summary(), Score: v.Score, Rejected: v.Rejected,
			Resolution: info.Resolution, Codec: info.Codec, SourceType: info.Source,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Rejected != out[j].Rejected {
			return !out[i].Rejected
		}
		return out[i].Score > out[j].Score
	})
	for i := range out {
		out[i].Best = i == 0 && !out[i].Rejected
	}
	return out
}

// searchIndexers runs the two-stage fan-out in the preferred protocol's order:
// by default the (few) usenet indexers first, and the wide torrent search only
// when they come back empty.
// Scoping to explicit indexer ids skips the staging entirely.
func (s *Service) searchIndexers(ctx context.Context, query string, only []int) ([]prowlarr.Release, error) {
	if len(only) > 0 {
		return s.pr.SearchIn(ctx, query, only)
	}
	idx, _ := s.pr.Indexers(ctx)
	first, second := "usenet", "torrent"
	if s.grabPolicy(ctx).PreferProtocol == "torrent" {
		first, second = second, first
	}
	var releases []prowlarr.Release
	if ids := prowlarr.EnabledIDs(idx, first); len(ids) > 0 {
		releases, _ = s.pr.SearchIn(ctx, query, ids)
	}
	if len(releases) > 0 {
		return releases, nil
	}
	return s.pr.SearchIn(ctx, query, prowlarr.EnabledIDs(idx, second))
}

// Search is the console's manual search: a free-text query across the indexers
// (optionally scoped to some of them), ranked by the active profile.
func (s *Service) Search(ctx context.Context, query string, only []int) ([]Candidate, error) {
	if !s.pr.Enabled() {
		return nil, errAutoGrabDisabled
	}
	releases, err := s.searchIndexers(ctx, query, only)
	if err != nil {
		return nil, err
	}
	return s.rankByProfile(ctx, releases), nil
}

// Releases runs the interactive search for one request — same ranking as
// auto-grab, but shown rather than acted on.
func (s *Service) Releases(ctx context.Context, wantedID string) ([]Candidate, error) {
	w, err := s.st.GetWanted(ctx, wantedID)
	if err != nil {
		return nil, err
	}
	query := w.Title
	if w.Year != 0 {
		query = w.Title + " " + itoa(w.Year)
	}
	return s.Search(ctx, query, nil)
}

// GrabCandidate hands a specific release (chosen in a picker or the manual
// search) to the client for its protocol, recording which one it was.
func (s *Service) GrabCandidate(ctx context.Context, wantedID string, c Candidate) error {
	w, err := s.st.GetWanted(ctx, wantedID)
	if err != nil {
		return err
	}
	reason := c.Reason
	if reason == "" {
		reason = "picked manually"
	}
	out, err := s.handOff(ctx, candidateHandoff(w, c, reason))
	if err != nil {
		s.failGrab(ctx, wantedID, err)
		return err
	}
	s.setStatus(ctx, wantedID, "downloading",
		"grabbed "+protoLabel(out.Protocol)+" from "+c.Indexer+" via "+out.Client.ID)
	return nil
}

// GrabAdHoc grabs a release found in the manual search that no request covers
// yet: it records the wanted item first, so the download still lands in the
// catalog and the console can follow it like any other request.
func (s *Service) GrabAdHoc(ctx context.Context, c Candidate, title string, sub string) error {
	if title == "" {
		title = release.Parse(c.Title).Title
	}
	if title == "" {
		title = c.Title
	}
	w, err := s.Request(ctx, store.Wanted{Title: title, MediaType: "movie"}, sub)
	if err != nil {
		return err
	}
	return s.GrabCandidate(ctx, w.ID, c)
}

// IndexerInfo is one configured indexer as shown in the console.
type IndexerInfo struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Enabled  bool   `json:"enabled"`
}

// Indexers lists the search backends currently configured.
func (s *Service) Indexers(ctx context.Context) []IndexerInfo {
	idx, _ := s.pr.Indexers(ctx)
	out := make([]IndexerInfo, 0, len(idx))
	for _, i := range idx {
		out = append(out, IndexerInfo{ID: i.ID, Name: i.Name, Protocol: i.Protocol, Enabled: i.Enable})
	}
	return out
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
