package app

import (
	"context"
	"errors"
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
	case "grab-recovery":
		return s.RecoverGrabs(ctx)
	case "subtitles-sweep":
		// The subtitle worker searches and downloads; the event waits for nothing.
		s.wakeSubtitles()
		return nil
	case "holdings-reconcile":
		// The reconcile asks the catalog about every tracked title; its worker
		// does that, so the download events behind this one wait for nothing.
		s.RequestReconcile()
		return nil
	case "outbox-audit":
		n, err := s.st.OutboxDepth(ctx)
		if err == nil && n > 0 {
			log.Printf("acquire: outbox depth %d", n)
		}
		return err
	}
	return nil
}

// SweepBacklog searches a bounded slice of the backlog: wanted, aired,
// monitored targets of titles whose holdings are known, never searched first,
// then the ones searched longest ago.
//
// Every search records its outcome on its target — found, none or error —
// with when it is due again, so the next sweep takes the next slice instead of
// the same one (store.DueTargets).
//
// It grabs only when search.autoGrab allows it. Searching is cheap and
// reversible; grabbing spends disk and bandwidth and is not. With autoGrab
// off — the default — the sweep only searches and records what it would
// take. With it on, it grabs the best release an automatic grab may take —
// the episode alone, or one run of at most ten episodes in a row that are all
// missing (autoCandidates) — bound to every target that release covers, and
// only while a download slot is free; a target whose title's holdings are
// unknown is never grabbed.
func (s *Service) SweepBacklog(ctx context.Context, limit int) error {
	due, err := s.st.DueTargets(ctx, limit)
	if err != nil {
		return err
	}
	return s.sweep(ctx, "sweep", due)
}

// SweepRetries re-examines targets whose searches found nothing or failed and
// whose backoff has expired.
func (s *Service) SweepRetries(ctx context.Context, limit int) error {
	due, err := s.st.RetryableTargets(ctx, limit)
	if err != nil || len(due) == 0 {
		return err
	}
	return s.sweep(ctx, "retry", due)
}

// sweep searches each target and, with autoGrab, grabs.
func (s *Service) sweep(ctx context.Context, what string, due []store.Target) error {
	if len(due) == 0 {
		return nil
	}
	grabbing := s.grabPolicy(ctx).AutoGrab
	var found, empty, failed, grabbed int
	var stopped, halted string
	for _, t := range due {
		if ctx.Err() != nil {
			break
		}
		cands, err := s.SearchTarget(ctx, t.ID)
		if ctx.Err() != nil {
			break // a search cut short says nothing of its target
		}
		usable := s.recordSearch(ctx, t, cands, err)
		switch {
		case err != nil:
			failed++
			continue
		case len(usable) == 0:
			empty++
			continue
		}
		found++
		if !grabbing {
			continue
		}
		title, ok := s.autoGrabAllowed(ctx, t)
		if !ok {
			continue
		}
		if full, why := s.slotsFull(ctx); full {
			// Nothing more starts this run. What was found is found again on
			// the next sweep rather than in twelve hours.
			grabbing, stopped = false, why
			s.foundButWaiting(ctx, t, why)
			continue
		}
		_, err = s.grabBest(ctx, title, t, usable, "sweep")
		var capErr atCapacity
		var r refused
		switch {
		case err == nil:
			grabbed++
		case errors.As(err, &capErr), errors.As(err, &r):
			// No slot, no room, no client: none of the rest would fare
			// better this run.
			grabbing, stopped = false, err.Error()
			s.foundButWaiting(ctx, t, err.Error())
		default:
			s.targetGrabFailed(ctx, t, err)
		}
		if err != nil && gatewayDown(err) {
			// The gateway is not taking adds: the rest of the slice would be
			// searched — spending the sources' allowance — for grabs that
			// cannot be handed over. It waits for the next run.
			halted = err.Error()
			break
		}
	}
	line := "acquire %s: %d target(s) — %d with candidates, %d empty, %d errored, %d grabbed"
	args := []any{what, len(due), found, empty, failed, grabbed}
	if stopped != "" {
		line += "; stopped grabbing: %s"
		args = append(args, stopped)
	}
	if halted != "" {
		line += "; stopped: the gateway does not take grabs now: %s"
		args = append(args, halted)
	}
	log.Printf(line, args...)
	if grabbed > 0 {
		s.notify()
	}
	return nil
}

// autoGrabAllowed reports whether the sweep may grab a target it found
// releases for, and its title: only a wanted target, monitored with its
// title, of a title whose holdings are known. The setting itself the caller
// checked.
func (s *Service) autoGrabAllowed(ctx context.Context, t store.Target) (store.Title, bool) {
	title, err := s.st.TitleByID(ctx, t.TitleID)
	switch {
	case err != nil:
		return store.Title{}, false
	case !title.HoldingsKnown():
		// Nothing says it is not on disk: never grabbed automatically.
		return title, false
	case !title.Monitored || !t.Monitored || t.State != store.TargetWanted:
		return title, false
	}
	return title, true
}

// foundButWaiting records that a target's releases were found but not grabbed
// for now, and makes it due again on the next sweep.
func (s *Service) foundButWaiting(ctx context.Context, t store.Target, why string) {
	_ = s.st.RecordSearch(ctx, t.ID, store.SearchRecord{Outcome: store.SearchFound, Again: backoffBase,
		Detail: clip("found; not grabbed yet: "+why, 300)})
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
