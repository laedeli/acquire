package app

import (
	"context"
	"fmt"
	"log"

	"github.com/laedeli/acquire/internal/release"
	"github.com/laedeli/acquire/internal/store"
)

// Holding is one file katalog holds, described by its coordinates rather than
// by katalog's identity. The UUID travels back to us; it is never something we
// send. That direction is the whole seam: katalog owns HAVE, acquire owns WANT.
type Holding struct {
	ItemID        string
	TMDBID        int64
	Kind          string // movie | episode
	SeasonNumber  *int
	EpisodeNumber *int
	Release       string // the release name, when a grab produced this file
	Quality       map[string]any
}

// ReconcileHolding projects one katalog file onto its acquisition target.
//
// This is what makes the existing 25.7 TB library appear as `held` with ZERO
// data migration: TMDB supplies the inventory, katalog supplies the holdings,
// and this joins them on coordinates. Nothing moves.
//
// A holding for a coordinate we do not track is IGNORED rather than
// auto-created. Creating targets from whatever katalog happens to contain would
// silently invent intent — the library holds things nobody is monitoring, and
// they must not become things we hunt upgrades for.
func (s *Service) ReconcileHolding(ctx context.Context, h Holding) error {
	if h.ItemID == "" || h.TMDBID == 0 {
		return fmt.Errorf("holding needs an item id and a tmdb id")
	}
	titleKind := "movie"
	if h.Kind == "episode" {
		titleKind = "series"
	}
	titleID := store.TitleID(titleKind, h.TMDBID)
	targetID := store.TargetID(titleID, h.SeasonNumber, h.EpisodeNumber)

	// Score what we hold under the active profile, so "is this good enough?"
	// is answerable without asking katalog again per row.
	profile, profileID := s.ScoringProfile(ctx, "")
	source := "derived"
	score := 0
	if h.Release != "" {
		// A real release name means a grab produced this file and the score is
		// trustworthy.
		source = "grab"
		v, _ := release.Score(release.Candidate{Title: h.Release, Protocol: "usenet"}, profile)
		score = v.Score
	}
	quality := h.Quality
	if quality == nil {
		quality = map[string]any{}
	}
	if h.Release != "" {
		in := release.Parse(h.Release)
		quality["resolution"], quality["source"], quality["codec"] = in.Resolution, in.Source, in.Codec
	}
	return s.st.ApplyHolding(ctx, targetID, h.ItemID, h.Release, profileID, source, quality, score)
}

// OnItemRemoved reverses a holding when katalog loses the file. The target
// becomes wanted again — that is why WANT is modelled separately from HAVE, and
// a one-way projection would quietly leave a hole nobody hunts.
func (s *Service) OnItemRemoved(ctx context.Context, itemID string) error {
	n, err := s.st.ClearHolding(ctx, itemID)
	if err != nil {
		return err
	}
	if n > 0 {
		log.Printf("acquire: katalog item %s removed; %d target(s) wanted again", itemID, n)
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
