package app

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/laedeli/acquire/internal/store"
)

// Sweep limits. These are QUOTA bounds, not throughput knobs.
//
// Three unbounded capability sweeps during planning rate-limited the single
// most important indexer into hard failure, twice, within minutes. A backlog of
// 21,537 episodes searched without a ceiling would ban us permanently — so a
// sweep takes a small slice each time it runs and lets the schedule interval do
// the pacing.
const (
	sweepBatch     = 10
	retryBatch     = 10
	backoffBase    = 30 * time.Minute
	backoffCeiling = 24 * time.Hour
	// A failed release is blocked for long enough that a retry picks something
	// else, but not forever: an indexer outage should not permanently poison a
	// release that is actually fine.
	blockTTL = 14 * 24 * time.Hour
)

// OnScheduleDue is the reactive half of the clock. The clock emits; this acts.
//
// Keeping the decision here rather than in the timer is what makes the work
// replayable: the event is durable in the outbox, so a sweep that dies mid-way
// is redone rather than silently skipped.
func (s *Service) OnScheduleDue(ctx context.Context, name string) error {
	switch name {
	case "backlog-sweep":
		return s.SweepBacklog(ctx, sweepBatch)
	case "retry-failed":
		return s.SweepRetries(ctx, retryBatch)
	case "outbox-audit":
		n, err := s.st.OutboxDepth(ctx)
		if err == nil && n > 0 {
			log.Printf("acquire: outbox depth %d", n)
		}
		return err
	}
	return nil
}

// SweepBacklog searches for a bounded slice of what we want and do not have.
//
// It does NOT grab. Searching is cheap and reversible; grabbing spends disk and
// bandwidth and is not. Until the request model and the target model are
// bridged (P6), the sweep reports what it would take and records the search
// outcome, so the backlog stops being invisible without anything irreversible
// happening on a timer.
func (s *Service) SweepBacklog(ctx context.Context, limit int) error {
	due, err := s.st.DueTargets(ctx, limit)
	if err != nil {
		return err
	}
	if len(due) == 0 {
		return nil
	}
	var found, empty, failed int
	for _, t := range due {
		cands, err := s.SearchTarget(ctx, t.ID)
		if err != nil {
			failed++
			// A search that errors is a failure to LOOK, not evidence of
			// absence, so it backs off rather than marking anything.
			_ = s.st.BackoffSearch(ctx, t.ID, backoffBase, backoffCeiling)
			continue
		}
		usable := 0
		for _, c := range cands {
			if !c.Rejected {
				usable++
			}
		}
		if usable == 0 {
			empty++
			// Nothing usable is also not proof of absence — a typed query to an
			// indexer without id support returns zero with no error — so this
			// backs off too and will be asked again later.
			_ = s.st.BackoffSearch(ctx, t.ID, backoffBase, backoffCeiling)
			continue
		}
		found++
		_ = s.st.RecordHistory(ctx, store.HistoryEntry{
			Kind: "searched", Subject: t.ID, Title: cands[0].Title,
			Indexer: cands[0].Indexer, Protocol: cands[0].Protocol,
			SizeMb: cands[0].Size / (1024 * 1024), Score: cands[0].Score,
			Reason: fmt.Sprintf("%d candidate(s), best via %s", usable, cands[0].MatchedVia),
		})
	}
	log.Printf("acquire sweep: %d target(s) — %d with candidates, %d empty, %d errored",
		len(due), found, empty, failed)
	return nil
}

// SweepRetries re-examines targets whose backoff has expired. Blocking the
// release that failed is what makes the retry pick something else — ranking is
// deterministic, so without it the identical release is chosen again.
func (s *Service) SweepRetries(ctx context.Context, limit int) error {
	due, err := s.st.RetryableTargets(ctx, limit)
	if err != nil || len(due) == 0 {
		return err
	}
	log.Printf("acquire retry: %d target(s) eligible", len(due))
	return s.SweepBacklog(ctx, limit)
}

// BlockFailedRelease is called when a download fails. It is the difference
// between a retry and an identical retry.
func (s *Service) BlockFailedRelease(ctx context.Context, targetID, releaseTitle, indexer, reason string) error {
	if releaseTitle == "" {
		return nil
	}
	if err := s.st.Block(ctx, releaseTitle, targetID, indexer, reason, blockTTL); err != nil {
		return err
	}
	_ = s.st.RecordHistory(ctx, store.HistoryEntry{
		Kind: "blocked", Subject: targetID, Title: releaseTitle,
		Indexer: indexer, Reason: reason,
	})
	return nil
}
