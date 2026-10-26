package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/laedeli/acquire/internal/events"
	"github.com/laedeli/acquire/internal/gateway"
	"github.com/laedeli/acquire/internal/store"
)

// Grabs whose download finished but whose import never came.
//
// A download's completed event is what queues its import. When it is lost —
// published while acquire was away, or never published —, or its import is
// swallowed by another download's on the same job id (a client gives a job id
// again once it forgot the download that had it, see store.JobOwner), the
// grab stays grabbed or downloaded for ever: the gateway forgets a job once it
// ends, and adopts only the jobs still running after it restarts, so no event
// comes again. The repair asks the client, through the gateway, what became of
// such a grab's job, and imports a completed one as its completed event would
// have. It runs on the grab-recovery schedule, before the stale-grab sweep,
// and once shortly after acquire starts.

const (
	// repairBatch bounds the grabs one repair asks the gateway about.
	repairBatch = 25
	// repairQuiet is how long a grab handed over may give no word of its
	// download before the repair asks what became of its job, when the
	// gateway no longer tracks it.
	repairQuiet = 15 * time.Minute
	// repairAtStart is how soon after acquire starts the repair runs once,
	// outside the schedule: the grabs an upgrade finds stuck are imported
	// then, after the gateway was told its clients.
	repairAtStart = 2 * time.Minute
)

// RepairRun is what one repair did with the grabs it looked at.
type RepairRun struct {
	Found   int // grabs that may have finished unseen
	Queued  int // their import queued
	Moved   int // nothing queued: queued already, or the grab moved on meanwhile — done or given up
	Waiting int // their job id still holds another download's import, not done
	Running int // not finished in their client
	Ended   int // ended in their client with nothing to import: failed, or no files
	Unknown int // their client does not know the job: left to the stale-grab sweep
	Reused  int // their job id names a newer grab's download: left to the stale-grab sweep
	Tracked int // the gateway still tracks the job: its events will come
	Unasked int // the gateway or the client could not be asked: tried on the next run
}

func (r RepairRun) String() string {
	var parts []string
	add := func(n int, what string) {
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, what))
		}
	}
	add(r.Queued, "queued for import")
	add(r.Moved, "queued already or moved on meanwhile")
	add(r.Waiting, "waiting for another download's import on the same job id")
	add(r.Running, "not finished in their client")
	add(r.Ended, "ended in their client with nothing to import")
	add(r.Unknown, "unknown to their client")
	add(r.Reused, "of a job id the client gave again")
	add(r.Tracked, "still tracked by the gateway")
	add(r.Unasked, "not asked: the gateway or the client did not answer")
	return strings.Join(parts, ", ")
}

// RepairGrabs looks at the grabs whose download may have finished without
// their import being queued (store.UnimportedTargetGrabs), at most
// repairBatch: downloaded with no import of their own, and handed over with
// no word for repairQuiet whose job the gateway no longer tracks. It asks each
// one's client, through the gateway, what its job is now, and a job that
// completed with files goes to the completed event's path — its paths mapped,
// its import queued, replacing a finished import of another grab on the job id
// (onTargetCompleted). A job the client does not know is left to the
// stale-grab sweep, and so is a grab whose job id the client gave to a newer
// grab since: the job the id names now is the newer grab's, and so are its
// files. A gateway or a client that cannot be asked leaves the grab for the
// next run. One line in the log says what the run did.
func (s *Service) RepairGrabs(ctx context.Context) (RepairRun, error) {
	var run RepairRun
	if s.st == nil || !s.gw.Enabled() {
		return run, nil
	}
	grabs, err := s.st.UnimportedTargetGrabs(ctx, repairQuiet, repairBatch)
	if err != nil || len(grabs) == 0 {
		return run, err
	}
	run.Found = len(grabs)
	defer func() {
		log.Printf("acquire grab-recovery: %d grab(s) whose download may have finished unseen: %s", run.Found, run)
	}()

	// What the gateway tracks, asked once, for the grabs handed over: without
	// it, whether a grab's events will come is not known.
	var tracked map[string]bool
	var listErr error
	for _, g := range grabs {
		if ctx.Err() != nil {
			run.Unasked++
			continue
		}
		reused, err := s.st.JobReused(ctx, g.Adapter, g.ClientJobID, g.ID)
		if err != nil {
			return run, err
		}
		if reused {
			run.Reused++
			continue
		}
		if g.State == store.GrabGrabbed {
			if tracked == nil && listErr == nil {
				tracked, listErr = s.trackedJobs(ctx)
			}
			switch {
			case listErr != nil:
				run.Unasked++
				continue
			case tracked[g.ID] || tracked[g.Adapter+"/"+g.ClientJobID]:
				run.Tracked++
				continue
			}
		}
		job, err := s.gw.Describe(ctx, g.Adapter, g.ClientJobID)
		switch {
		case errors.Is(err, gateway.ErrNoDescribe):
			// An older gateway: no grab can be asked about.
			run.Unasked += len(grabs) - run.handled()
			log.Printf("acquire grab-recovery: the download gateway is older than acquire and cannot describe a job; " +
				"grabs whose download finished unseen are not imported until it is upgraded")
			return run, nil
		case errors.Is(err, gateway.ErrJobNotFound):
			run.Unknown++
			continue
		case err != nil:
			run.Unasked++
			continue
		case job.State == "failed" || (job.State == "completed" && len(job.Files) == 0):
			run.Ended++
			continue
		case job.State != "completed":
			run.Running++
			continue
		}
		done, err := s.importUnseen(ctx, g, job)
		switch {
		case errors.Is(err, errImportWaits):
			run.Waiting++
		case err != nil:
			return run, fmt.Errorf("repair %s: %w", g, err)
		case done == completedQueued:
			run.Queued++
		default:
			run.Moved++
		}
	}
	if run.Queued > 0 {
		s.notify()
	}
	return run, nil
}

// handled counts the grabs a run has come to an answer for.
func (r RepairRun) handled() int {
	return r.Queued + r.Moved + r.Waiting + r.Running + r.Ended + r.Unknown + r.Reused + r.Tracked + r.Unasked
}

// importUnseen hands a job its client says completed to the completed event's
// path, as if the gateway had sent the event: its download row recorded, its
// import queued (completeTargetGrab) — for the grab as it is now, read again,
// since it may have moved on while its client was asked.
func (s *Service) importUnseen(ctx context.Context, g store.TargetGrab, job gateway.JobView) (completion, error) {
	ev := completedEvent(g, job)
	_ = s.saveDownload(ctx, ev, "completed")
	now, err := s.st.GetTargetGrab(ctx, g.ID)
	if err != nil {
		return completedNothing, err
	}
	return s.completeTargetGrab(ctx, now, ev)
}

// trackedJobs is what the gateway tracks: each job by its grab's id, when the
// gateway knows it, and by its client and job id.
func (s *Service) trackedJobs(ctx context.Context) (map[string]bool, error) {
	jobs, err := s.gw.List(ctx)
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, j := range jobs {
		if j.WantedItemID != "" {
			known[j.WantedItemID] = true
		}
		known[j.Adapter+"/"+j.ClientJobID] = true
	}
	return known, nil
}

// completedEvent is the completed event a grab's download would have sent,
// from what its client says of its job now: the grab's id, client and job,
// and the job's files as the client reports them.
func completedEvent(g store.TargetGrab, job gateway.JobView) events.DownloadEvent {
	ev := events.DownloadEvent{
		Adapter: g.Adapter, ClientID: g.ClientJobID, WantedID: g.ID, Title: job.Title, Files: job.Files,
		State: "completed", NativeState: job.NativeState, SizeBytes: job.SizeBytes, Health: job.Health,
	}
	if job.SizeBytes == nil && job.Downloaded > 0 {
		size := job.Downloaded
		ev.SizeBytes = &size
	}
	return ev
}

// RepairAtStart runs the repair once, repairAtStart after it is called —
// not up to an hour later, when the grab-recovery schedule comes round: what
// an upgrade or a restart finds stuck is imported at once.
func (s *Service) RepairAtStart(ctx context.Context) {
	if s.st == nil {
		return
	}
	t := time.NewTimer(s.repairStart)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return
	case <-t.C:
	}
	if _, err := s.RepairGrabs(ctx); err != nil && ctx.Err() == nil {
		log.Printf("acquire grab-recovery: the repair after the start: %v", err)
	}
}
