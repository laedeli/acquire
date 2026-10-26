package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path"
	"strings"
	"time"
	"unicode"

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
// such a grab's job, and settles one that ended as its event would have: a
// completed one imported, a failed one failed by its release — no failed event
// comes of a job that failed unseen either, and its download row would run on,
// holding a slot of the downloads at once. It runs on the grab-recovery
// schedule, before the stale-grab sweep, and once shortly after acquire
// starts.

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
	Failed  int // failed in their client: failed as their failed event would have, the release blocked
	Running int // not finished in their client
	Ended   int // ended in their client with nothing to import or fail: completed without files, or failed after it completed
	Unknown int // their client does not know the job: left to the stale-grab sweep
	Reused  int // their job id names a newer grab's download: left to the stale-grab sweep
	Foreign int // their client holds another download under their job id: given up, their targets wanted again
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
	add(r.Failed, "failed in their client, their release blocked")
	add(r.Running, "not finished in their client")
	add(r.Ended, "ended in their client with nothing to import")
	add(r.Unknown, "unknown to their client")
	add(r.Reused, "of a job id the client gave again")
	add(r.Foreign, "whose job id holds another download in their client, given up and wanted again")
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
// (onTargetCompleted) —, one that failed to the failed event's: its row
// ended, the grab failed by its release (onTargetFailed). A job the client
// does not know is left to the stale-grab sweep, and so is a grab whose job id
// the client gave to a newer grab since: the job the id names now is the
// newer grab's, and so are its files. A gateway or a client that cannot be
// asked leaves the grab for the next run. One line in the log says what the
// run did.
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
	// it, whether a grab's events will come is not known. A client that did
	// not answer is not asked again in the run.
	var tracked map[string]bool
	var listErr error
	unanswered := map[string]bool{}
	for _, g := range grabs {
		if ctx.Err() != nil || unanswered[g.Adapter] {
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
			unanswered[g.Adapter] = true
			run.Unasked++
			continue
		case actsOn(job) && !s.jobIsGrabs(ctx, g, jobName(job.Title, job.Files)):
			// The client holds another download under the id than the one
			// handed over for this grab.
			s.giveUpToAnother(ctx, g, jobName(job.Title, job.Files))
			run.Foreign++
			continue
		case job.State == "failed":
			// No failed event comes of a job that failed while nothing was
			// watching: its row would run on, holding a slot, and its release
			// would stay unblocked.
			failed, err := s.failUnseen(ctx, g, job)
			switch {
			case err != nil:
				return run, fmt.Errorf("repair %s: %w", g, err)
			case failed:
				run.Failed++
			default:
				run.Ended++
			}
			continue
		case job.State == "completed" && len(job.Files) == 0:
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
	return r.Queued + r.Moved + r.Waiting + r.Failed + r.Running + r.Ended + r.Unknown + r.Reused + r.Foreign + r.Tracked + r.Unasked
}

// actsOn reports whether what a client says of a job is acted on for its
// grab: a completed job's files imported, a failed job's failure failed.
func actsOn(job gateway.JobView) bool {
	return job.State == "failed" || (job.State == "completed" && len(job.Files) > 0)
}

// runs reports whether a job's client works on it still: queued, or
// downloading.
func runs(job gateway.JobView) bool {
	return job.State == "queued" || job.State == "downloading"
}

// runningEvent is the progress event a grab's download would have sent, from
// what its client says of its job now.
func runningEvent(g store.TargetGrab, job gateway.JobView) events.DownloadEvent {
	return events.DownloadEvent{
		Adapter: g.Adapter, ClientID: g.ClientJobID, WantedID: g.ID, Title: job.Title,
		State: job.State, NativeState: job.NativeState, ProgressPct: job.ProgressPct, Downloaded: job.Downloaded,
		SizeBytes: job.SizeBytes, SpeedBps: job.SpeedBps, EtaSec: job.EtaSec,
		Seeders: job.Seeders, Leechers: job.Leechers, Health: job.Health,
	}
}

// failUnseen hands a job its client says failed to the failed event's path,
// as if the gateway had sent the event: the grab, if it is still handed over,
// fails by its release — the release blocked for its targets, wanted again
// after a wait (failTargetGrab) —, and its download row ends with the client's
// word. It reports whether the grab failed: one whose download had completed
// already, or that moved on meanwhile, moves nothing, its row left as it is.
func (s *Service) failUnseen(ctx context.Context, g store.TargetGrab, job gateway.JobView) (bool, error) {
	now, err := s.st.GetTargetGrab(ctx, g.ID)
	if err != nil || now.State != store.GrabGrabbed {
		return false, err
	}
	why := strings.TrimSpace(job.Message)
	if why == "" {
		why = "the download failed in " + g.Adapter
	}
	ev := events.DownloadEvent{Adapter: g.Adapter, ClientID: g.ClientJobID, WantedID: g.ID, Title: job.Title,
		State: "failed", NativeState: job.NativeState, Error: why}
	_ = s.saveDownload(ctx, ev, "failed")
	return s.failTargetGrab(ctx, now, ev)
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

// ── whose job a job id names ───────────────────────────────────────────────
//
// The newest grab acquire recorded on a job id (store.JobOwner) is the job
// the client holds under it only when every hand-over was recorded. An add
// whose answer was lost leaves its grab pending without a job id, and a job
// someone added by hand has no grab at all: either can hold an id an older
// grab had. Before what a client says of a job is taken as a grab's — its
// files imported, its failure failed, its events attributed by the job id
// alone —, the job's name is checked against the names acquire could have
// handed the grab's NZB over under: NZBGet names a job after its NZB.
//
// Only NZBGet gives a job id again. The check is for its jobs alone, and for
// a grab with a release to tell it by: one whose link was handed over by hand
// names nothing.

// nameKey is a name as the check compares it: in lower case, letters and
// digits only — the NZB's name keeps a release's words but not always its
// dots, dashes and plus signs (DD+5.1, DD5.1).
func nameKey(name string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, name)
}

// handedOverAs are the names acquire could have handed a grab's NZB over
// under, cleaned and cut as an NZB's name is (sanitizeFileName): its
// release's, every target grab's since releasePayloadName and what a source
// mostly called the file before; and its label's — the title and what it
// covers, "Show Name S01E03" — which an NZB the source named nothing was
// handed over under before releasePayloadName (payloadName).
func (s *Service) handedOverAs(ctx context.Context, g store.TargetGrab) []string {
	names := []string{sanitizeFileName(g.ReleaseTitle)}
	if title, err := s.st.TitleByID(ctx, g.TitleID); err == nil {
		if label := strings.TrimSpace(title.Title + " " + g.Covers); label != "" {
			names = append(names, payloadBase(label))
		}
	}
	return names
}

// givesIDsAgain reports whether the client a grab was handed to may give a
// job id to another job later: NZBGet, which numbers its jobs anew once it
// restarts with an empty history. It is told by the client's type as acquire
// stores it, else as the gateway's event names it (typ). Other clients' ids
// are made for one job — qBittorrent's, a tag for each add —, and a client
// acquire no longer has cannot be asked about anyway.
func (s *Service) givesIDsAgain(ctx context.Context, adapter, typ string) bool {
	if c, err := s.st.GetDownloadClient(ctx, adapter); err == nil {
		return c.Type == "nzbget"
	}
	return typ == "nzbget"
}

// jobName is what a client calls a job: its title, else the name of the first
// folder or file it reports for it.
func jobName(title string, files []string) string {
	if t := strings.TrimSpace(title); t != "" {
		return t
	}
	if len(files) > 0 {
		return path.Base(path.Clean(files[0]))
	}
	return ""
}

// jobIsGrabs reports whether the job a client holds under a grab's job id —
// named name — is the grab's download: named as acquire handed the grab's NZB
// over (handedOverAs), the name cut as an NZB's name is. A job of a client
// that does not give ids again, and of a grab with no release, is the grab's
// whatever its name.
func (s *Service) jobIsGrabs(ctx context.Context, g store.TargetGrab, name string) bool {
	return s.jobIsGrabsOf(ctx, g, name, "")
}

// jobIsGrabsOf is jobIsGrabs for a job of a client the gateway's event names
// the type of (typ).
func (s *Service) jobIsGrabsOf(ctx context.Context, g store.TargetGrab, name, typ string) bool {
	if strings.TrimSpace(g.ReleaseTitle) == "" || !s.givesIDsAgain(ctx, g.Adapter, typ) {
		return true
	}
	key := nameKey(sanitizeFileName(name))
	if key == "" {
		return false
	}
	for _, handed := range s.handedOverAs(ctx, g) {
		if key == nameKey(handed) {
			return true
		}
	}
	return false
}

// giveUpToAnother gives up a grab whose client holds another download under
// its job id than the one handed over for it: the grab's own download is gone
// from the client. Nothing of the other job is imported or failed for it. The
// grab fails, its download row ends, and its targets are wanted again with the
// reason — nothing blocked, the release was not at fault —, for the sweep to
// grab again on its own (store.FreeTargetGrab). A grab done or given up
// already is left as it is. The log says so too.
func (s *Service) giveUpToAnother(ctx context.Context, g store.TargetGrab, name string) {
	if name == "" {
		name = "a download it gives no name"
	}
	why := clip(fmt.Sprintf("%s holds another download under its job id %s (%s), not %s: this grab's download is gone; wanted again",
		g.Adapter, g.ClientJobID, name, g.ReleaseTitle), 300)
	if _, err := s.st.FreeTargetGrab(ctx, g.ID, why); err != nil {
		return
	}
	log.Printf("acquire: grab %s given up: %s", g.ID, why)
	s.notify()
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
