// Package app is acquire's orchestration: it turns requests + admin grabs into
// gateway commands, and reacts to download/pipeline EVENTS to advance request
// status (downloading → packaging → fulfilled). Every status change is published
// to SSE. It owns no HTTP — httpapi drives it; events.Consumer feeds it.
package app

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/laedeli/acquire/internal/config"
	"github.com/laedeli/acquire/internal/events"
	"github.com/laedeli/acquire/internal/gateway"
	"github.com/laedeli/acquire/internal/katalog"
	"github.com/laedeli/acquire/internal/store"
	"github.com/laedeli/acquire/internal/tmdb"
)

type Service struct {
	cfg    config.Config
	st     *store.Store
	gw     *gateway.Client
	kc     *katalog.Client
	tm     *tmdb.Client
	notify func() // sse ping
}

func New(cfg config.Config, st *store.Store, gw *gateway.Client, kc *katalog.Client, tm *tmdb.Client, notify func()) *Service {
	return &Service{cfg: cfg, st: st, gw: gw, kc: kc, tm: tm, notify: notify}
}

// setStatus updates + pings subscribers.
func (s *Service) setStatus(ctx context.Context, id, status, detail string) {
	if err := s.st.SetStatus(ctx, id, status, detail); err != nil {
		log.Printf("acquire: set status %s=%s: %v", id, status, err)
		return
	}
	s.notify()
}

// ── request-side (commands, called by httpapi) ──────────────────────────────

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
	if ev.WantedID == "" {
		return nil // not one of ours
	}
	w, err := s.st.GetWanted(ctx, ev.WantedID)
	if err != nil {
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
	if ev.WantedID == "" {
		return nil
	}
	detail := ev.Error
	if detail == "" {
		detail = "download failed"
	}
	s.setStatus(ctx, ev.WantedID, "failed", detail)
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

// Handlers returns the consumer callback set bound to this service.
func (s *Service) Handlers() events.Handlers {
	return events.Handlers{
		OnCompleted: s.OnCompleted,
		OnFailed:    s.OnFailed,
		OnPackaged:  s.OnPackaged,
	}
}
