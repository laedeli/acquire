package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// TargetGrabPrefix starts every target grab's id. The id travels to the
// gateway as the wanted item id and comes back on the download's events, and
// the prefix is what tells it from a request's (w_…).
const TargetGrabPrefix = "tg_"

// IsTargetGrab reports whether an id the gateway echoes is a target grab's.
func IsTargetGrab(id string) bool { return strings.HasPrefix(id, TargetGrabPrefix) }

// Target grab states (migration 040).
const (
	GrabPending    = "pending"    // written before the hand-over; holds its targets
	GrabGrabbed    = "grabbed"    // with a download client
	GrabDownloaded = "downloaded" // the download finished; its files are being handed over
	GrabImported   = "imported"   // every target it covers has its answer
	GrabFailed     = "failed"
)

// TargetGrab is one release handed to a download client for the targets it
// covers: one episode, a file of several, a season pack, or a movie.
type TargetGrab struct {
	ID          string   `json:"id"`
	TitleID     string   `json:"titleId"`
	TargetIDs   []string `json:"targetIds"`
	Adapter     string   `json:"adapter"`
	ClientJobID string   `json:"clientJobId"`
	State       string   `json:"state"`
	Detail      string   `json:"detail"`
	Via         string   `json:"via"` // sweep | missing | pick | search

	ReleaseTitle string `json:"releaseTitle"`
	Covers       string `json:"covers"`
	Indexer      string `json:"indexer"`
	IndexerID    *int64 `json:"indexerId,omitempty"`
	ReleaseGUID  string `json:"releaseGuid,omitempty"`
	Protocol     string `json:"protocol"`
	SizeBytes    int64  `json:"sizeBytes"`
	Seeders      *int32 `json:"seeders"`
	Reason       string `json:"reason"`
	// Source is always the redacted link; the full one only ever exists sealed.
	Source    string `json:"source"`
	SourceCT  []byte `json:"-"`
	SourceKID string `json:"-"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// TargetGrabAAD is the secretbox binding for a target grab's sealed link.
func TargetGrabAAD(id string) (table, rowID, field string) { return "target_grabs", id, "source" }

const targetGrabCols = `id, title_id, target_ids, adapter, client_job_id, state, detail, via,
	release_title, covers, indexer, indexer_id, release_guid, protocol, size_bytes, seeders, reason,
	source, source_ct, source_kid, created_at, updated_at`

func scanTargetGrab(r interface{ Scan(...any) error }) (TargetGrab, error) {
	var g TargetGrab
	err := r.Scan(&g.ID, &g.TitleID, &g.TargetIDs, &g.Adapter, &g.ClientJobID, &g.State, &g.Detail, &g.Via,
		&g.ReleaseTitle, &g.Covers, &g.Indexer, &g.IndexerID, &g.ReleaseGUID, &g.Protocol, &g.SizeBytes,
		&g.Seeders, &g.Reason, &g.Source, &g.SourceCT, &g.SourceKID, &g.CreatedAt, &g.UpdatedAt)
	if g.TargetIDs == nil {
		g.TargetIDs = []string{}
	}
	return g, err
}

// NotGrabbableError says why the target a grab is for cannot be grabbed now.
type NotGrabbableError struct {
	TargetID string
	State    string
	Detail   string
	// Busy is set when another grab under way holds it.
	Busy bool
}

func (e *NotGrabbableError) Error() string {
	switch {
	case e.State == "":
		return e.Detail
	case e.Busy:
		return "another grab is under way for it"
	case e.State == TargetHeld:
		return "the library holds it already"
	case InFlight(e.State):
		if e.Detail != "" {
			return "it is " + e.State + " already: " + e.Detail
		}
		return "it is " + e.State + " already"
	}
	return "it cannot be grabbed while " + e.State
}

// ReserveTargetGrab writes a pending grab for the targets a release covers and
// holds them against a second grab, in one transaction: until it is bound or
// released, no other reservation takes them. Covered targets that are held,
// in flight or reserved by another grab are left out of it; anchor, the
// target the grab is for, must not be (a NotGrabbableError says why). The
// grab is returned with the targets it took.
//
// The claim is one conditional UPDATE (reserved_by = ” … RETURNING): a
// second reservation racing for the same target waits on its row lock and
// then finds it taken. Reading the target and its grab first and deciding in
// the client let both take it.
func (s *Store) ReserveTargetGrab(ctx context.Context, g TargetGrab, anchor string, covered []string) (TargetGrab, error) {
	return s.ReserveTargetGrabWithin(ctx, g, anchor, covered, 0)
}

// AtCapacityError is a reservation refused because as many grabs as may be
// under way at once are: downloads in flight, grabs handed over the gateway
// has not reported yet, and reservations being handed over.
type AtCapacityError struct{ InFlight, Max int }

func (e *AtCapacityError) Error() string {
	return fmt.Sprintf("%d download(s) in flight or on their way (cap %d)", e.InFlight, e.Max)
}

// reserveLock is the advisory lock reservations with a cap take, so the
// count they read and the reservation they write are one step for every
// grabber at once — the sweep, the sources' newest releases, the grab queue.
const reserveLock = 4207001

// downloadsInFlightSQL counts the download rows that say their download runs,
// but not one whose target grab has ended — failed, given up or imported —:
// its row runs on only because no event ended it, and would hold a slot of
// the downloads at once for good.
const downloadsInFlightSQL = `SELECT count(*) FROM downloads d
	 WHERE d.state NOT IN ('completed','failed','cancelled')
	   AND NOT EXISTS (SELECT 1 FROM target_grabs ended WHERE ended.id = d.wanted_id AND ended.state IN ('failed','imported'))`

// ReserveTargetGrabWithin is ReserveTargetGrab under a cap: with max > 0, it
// is refused (AtCapacityError) while max grabs are under way — downloads in
// flight, grabs handed over the gateway has not reported yet, and other
// reservations being handed over —, counted and taken under one lock, so two
// grabbers at once cannot both take the last slot.
//
// A grab handed over counts until a download row of its own shows: one that
// names it (wanted_id), not any row of its client job. A client that gave the
// job id again has the ended row of an earlier grab under it, which says
// nothing of this one (see JobOwner). A row counts while it runs
// (downloadsInFlightSQL).
func (s *Store) ReserveTargetGrabWithin(ctx context.Context, g TargetGrab, anchor string, covered []string, max int) (TargetGrab, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return TargetGrab{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if max > 0 {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, reserveLock); err != nil {
			return TargetGrab{}, err
		}
		var n int
		if err := tx.QueryRow(ctx, `
			SELECT (`+downloadsInFlightSQL+`)
			     + (SELECT count(*) FROM target_grabs g
			         WHERE g.created_at > now() - interval '1 hour'
			           AND (g.state = 'pending'
			                OR (g.state = 'grabbed' AND NOT EXISTS (SELECT 1 FROM downloads d
			                                                         WHERE d.adapter = g.adapter AND d.client_job_id = g.client_job_id
			                                                           AND d.wanted_id = g.id))))`).
			Scan(&n); err != nil {
			return TargetGrab{}, err
		}
		if n >= max {
			return TargetGrab{}, &AtCapacityError{InFlight: n, Max: max}
		}
	}

	rows, err := tx.Query(ctx, `
		UPDATE acquisition_targets SET reserved_by = $2, updated_at = now()
		 WHERE id = ANY($1) AND state IN ('wanted','failed') AND reserved_by = ''
		 RETURNING id`, nonNil(covered), g.ID)
	if err != nil {
		return TargetGrab{}, err
	}
	var free []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return TargetGrab{}, err
		}
		free = append(free, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return TargetGrab{}, err
	}
	sort.Strings(free)
	if anchor != "" && !slices.Contains(free, anchor) {
		var state, detail, reservedBy string
		err := tx.QueryRow(ctx, `SELECT state, detail, reserved_by FROM acquisition_targets WHERE id = $1`, anchor).
			Scan(&state, &detail, &reservedBy)
		if errors.Is(err, pgx.ErrNoRows) {
			return TargetGrab{}, ErrNotFound
		}
		if err != nil {
			return TargetGrab{}, err
		}
		return TargetGrab{}, &NotGrabbableError{TargetID: anchor, State: state, Detail: detail,
			Busy: reservedBy != "" && Grabbable(state)}
	}
	if len(free) == 0 {
		return TargetGrab{}, &NotGrabbableError{TargetID: anchor,
			Detail: "nothing it covers can be grabbed: held already, or another grab is under way"}
	}
	g.TargetIDs, g.State = free, GrabPending
	if _, err := tx.Exec(ctx, `
		INSERT INTO target_grabs (id, title_id, target_ids, state, via, release_title, covers, indexer, indexer_id,
		                          release_guid, protocol, size_bytes, seeders, reason, source)
		VALUES ($1,$2,$3,'pending',$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		g.ID, g.TitleID, g.TargetIDs, g.Via, g.ReleaseTitle, g.Covers, g.Indexer, g.IndexerID,
		g.ReleaseGUID, g.Protocol, g.SizeBytes, g.Seeders, g.Reason, g.Source); err != nil {
		return TargetGrab{}, err
	}
	return g, tx.Commit(ctx)
}

// ReleaseTargetGrab undoes a reservation whose hand-over did not happen: the
// targets keep the state they had, and the pending row goes. Only call it when
// the gateway certainly has no job for the grab; one whose outcome is unknown
// stays reserved for the job's events, or the stale-grab sweep, to settle.
func (s *Store) ReleaseTargetGrab(ctx context.Context, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx,
		`UPDATE acquisition_targets SET reserved_by = '', updated_at = now() WHERE reserved_by = $1`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM target_grabs WHERE id = $1 AND state = 'pending'`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// BindTargetGrab records the client job a pending grab was handed to, with
// where its link came from, and moves the targets reserved for it to grabbed.
// A grab that is no longer pending — bound already, or failed meanwhile — is
// returned as it is: binding moves nothing twice.
func (s *Store) BindTargetGrab(ctx context.Context, g TargetGrab, detail string) (TargetGrab, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return TargetGrab{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	bound, err := scanTargetGrab(tx.QueryRow(ctx, `
		UPDATE target_grabs
		   SET adapter = $2, client_job_id = $3, protocol = CASE WHEN $4::text <> '' THEN $4::text ELSE protocol END,
		       source = CASE WHEN $5::text <> '' THEN $5::text ELSE source END,
		       source_ct = COALESCE($6, source_ct), source_kid = CASE WHEN $7::text <> '' THEN $7::text ELSE source_kid END,
		       state = 'grabbed', detail = $8, updated_at = now()
		 WHERE id = $1 AND state = 'pending'
		 RETURNING `+targetGrabCols, g.ID, g.Adapter, g.ClientJobID, g.Protocol, g.Source, g.SourceCT, g.SourceKID, clipText(detail)))
	if errors.Is(err, pgx.ErrNoRows) {
		return s.GetTargetGrab(ctx, g.ID)
	}
	if err != nil {
		return TargetGrab{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE acquisition_targets SET state = 'grabbed', grab_id = $1, reserved_by = '', detail = $3, updated_at = now(),
		       grab_requested_at = NULL, grab_claimed_until = NULL
		 WHERE reserved_by = $1 AND id = ANY($2) AND state IN ('wanted','failed')`,
		bound.ID, bound.TargetIDs, clipText(detail)); err != nil {
		return TargetGrab{}, err
	}
	// A target's row is keyed by its client job: one an earlier grab of the
	// same target left on a job id the client gave again is this grab's now.
	if _, err := tx.Exec(ctx, `
		INSERT INTO grab_targets (adapter, client_job_id, target_id, grab_id, state)
		SELECT $1, $2, t, $3, 'expected' FROM unnest($4::text[]) AS t
		ON CONFLICT (adapter, client_job_id, target_id) DO UPDATE
		   SET grab_id = EXCLUDED.grab_id, state = 'expected', file_path = '', episode_end = NULL, item_id = '',
		       detail = '', updated_at = now()
		 WHERE grab_targets.grab_id <> EXCLUDED.grab_id`, bound.Adapter, bound.ClientJobID, bound.ID, bound.TargetIDs); err != nil {
		return TargetGrab{}, err
	}
	return bound, tx.Commit(ctx)
}

// UnbindGrabTarget takes a target off a grab: the release turned out not to
// hold it. The grab answers for the rest.
func (s *Store) UnbindGrabTarget(ctx context.Context, grabID, targetID string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE target_grabs SET target_ids = array_remove(target_ids, $2), updated_at = now() WHERE id = $1`,
		grabID, targetID)
	return err
}

// StaleTargetGrabs are the grabs that may be stuck: pending, grabbed or
// downloaded, with no word of them for age — the grab's own row and its
// download's last event alike — and no import working on them. A download
// row is the grab's only when it names it: another grab's on the same job id
// is no word of this one.
func (s *Store) StaleTargetGrabs(ctx context.Context, age time.Duration) ([]TargetGrab, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+prefixCols("g", targetGrabCols)+`
		  FROM target_grabs g
		  LEFT JOIN downloads d ON g.client_job_id <> '' AND d.adapter = g.adapter AND d.client_job_id = g.client_job_id
		                       AND d.wanted_id = g.id
		 WHERE g.state IN ('pending','grabbed','downloaded')
		   AND GREATEST(g.updated_at, COALESCE(d.updated_at, g.updated_at)) < now() - make_interval(secs => $1)
		   AND NOT EXISTS (SELECT 1 FROM imports i WHERE i.wanted_id = g.id AND i.state IN ('pending','imported'))
		 ORDER BY g.created_at`, age.Seconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TargetGrab
	for rows.Next() {
		g, err := scanTargetGrab(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// UnimportedTargetGrabs are the grabs whose download may have finished
// without their import being queued: downloaded with no import of their own —
// its completed event's import swallowed, or waiting, on a job id the client
// gave again —, and handed over (grabbed) with no word of them for quiet, the
// grab's own row and its download's alike — a completed event lost, or sent
// while acquire was away. At most limit of them, in no set order, so that
// grabs the caller cannot settle yet — a job still running, a client that
// cannot be asked — do not keep the others from their turn. What their
// clients say of their jobs is the caller's to ask.
func (s *Store) UnimportedTargetGrabs(ctx context.Context, quiet time.Duration, limit int) ([]TargetGrab, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+prefixCols("g", targetGrabCols)+`
		  FROM target_grabs g
		  LEFT JOIN downloads d ON d.adapter = g.adapter AND d.client_job_id = g.client_job_id AND d.wanted_id = g.id
		 WHERE g.client_job_id <> ''
		   AND NOT EXISTS (SELECT 1 FROM imports i WHERE i.wanted_id = g.id)
		   AND (g.state = 'downloaded'
		        OR (g.state = 'grabbed'
		            AND GREATEST(g.updated_at, COALESCE(d.updated_at, g.updated_at)) < now() - make_interval(secs => $1)))
		 ORDER BY random()
		 LIMIT $2`, quiet.Seconds(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TargetGrab
	for rows.Next() {
		g, err := scanTargetGrab(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// FreeTargetGrab fails a grab that will not finish — cancelled, or stale —
// and frees its targets: those it reserved, and those in flight with it, go
// back to wanted with the reason. It reports how many it freed. The grab's own
// download row, if it still says the download runs, ends with it, saying why:
// no event of it comes any more, and a row left running would hold a slot of
// the downloads at once for good.
func (s *Store) FreeTargetGrab(ctx context.Context, id, reason string) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx,
		`UPDATE target_grabs SET state = 'failed', detail = $2, updated_at = now() WHERE id = $1`, id, clipText(reason)); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, endGrabDownloadSQL, id, clipText(reason)); err != nil {
		return 0, err
	}
	tag, err := tx.Exec(ctx, `
		UPDATE acquisition_targets
		   SET state = CASE WHEN grab_id = $1 AND state IN ('grabbed','downloaded','waiting') THEN 'wanted' ELSE state END,
		       detail = $2, reserved_by = '', updated_at = now()
		 WHERE reserved_by = $1 OR (grab_id = $1 AND state IN ('grabbed','downloaded','waiting'))`, id, clipText(reason))
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), tx.Commit(ctx)
}

// endGrabDownloadSQL ends a grab's own download row that still says the
// download runs ($1 the grab, $2 why): no event of it comes any more.
const endGrabDownloadSQL = `
	UPDATE downloads SET state = 'failed', error = $2, speed_bps = 0, eta_sec = NULL,
	       finished_at = COALESCE(finished_at, now()), updated_at = now()
	 WHERE wanted_id = $1 AND state NOT IN ('completed','failed','cancelled')`

// EndGrabDownload ends a target grab's own download row that still says the
// download runs, with the reason: what a grab acquire stops waiting for asks
// of its row, which no event ends any more.
func (s *Store) EndGrabDownload(ctx context.Context, grabID, reason string) error {
	_, err := s.pool.Exec(ctx, endGrabDownloadSQL, grabID, clipText(reason))
	return err
}

func prefixCols(alias, cols string) string {
	parts := strings.Split(cols, ",")
	for i, p := range parts {
		parts[i] = alias + "." + strings.TrimSpace(p)
	}
	return strings.Join(parts, ", ")
}

// GetTargetGrab reads one target grab, or ErrNotFound.
func (s *Store) GetTargetGrab(ctx context.Context, id string) (TargetGrab, error) {
	g, err := scanTargetGrab(s.pool.QueryRow(ctx, `SELECT `+targetGrabCols+` FROM target_grabs WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return TargetGrab{}, ErrNotFound
	}
	return g, err
}

// ── client job ids given again ─────────────────────────────────────────────
//
// A client's job id is not unique for ever. NZBGet numbers its jobs from 1
// again when it restarts with an empty history — and acquire empties it, by
// releasing every download it imported —, so the id an earlier grab got is
// handed to a later one. A job id names the job its client holds under it
// now, which is the newest grab's handed to it: every older grab on the same
// id is about a download that is gone. A job id means nothing without its
// client either: two clients number their jobs on their own.

// JobGrab is a grab handed to a client job: a target grab's id or a
// request's, and when the grab was made.
type JobGrab struct {
	WantedID  string
	CreatedAt time.Time
}

// JobOwner is the grab a client job belongs to now: the newest handed to
// (adapter, jobID), a target grab or a request's grab. ErrNotFound when none
// was.
func (s *Store) JobOwner(ctx context.Context, adapter, jobID string) (JobGrab, error) {
	if adapter == "" || jobID == "" {
		return JobGrab{}, ErrNotFound
	}
	var g JobGrab
	err := s.pool.QueryRow(ctx, `
		SELECT id, created_at FROM (
		  SELECT id, created_at FROM target_grabs WHERE adapter = $1 AND client_job_id = $2
		  UNION ALL
		  SELECT wanted_id, created_at FROM grabs WHERE adapter = $1 AND client_job_id = $2
		) j ORDER BY created_at DESC LIMIT 1`, adapter, jobID).Scan(&g.WantedID, &g.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return JobGrab{}, ErrNotFound
	}
	return g, err
}

// JobReused reports whether a grab newer than the target grab grabID — a
// target grab or a request's grab — was handed to the same client job: the
// client gave the id again once grabID's download was gone, so what the job
// is now is not grabID's. A grab acquire does not know is not reused.
func (s *Store) JobReused(ctx context.Context, adapter, jobID, grabID string) (bool, error) {
	if adapter == "" || jobID == "" {
		return false, nil
	}
	var reused bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
		  SELECT 1 FROM target_grabs me
		   WHERE me.id = $3
		     AND (EXISTS (SELECT 1 FROM target_grabs t
		                   WHERE t.adapter = $1 AND t.client_job_id = $2 AND t.id <> me.id AND t.created_at > me.created_at)
		          OR EXISTS (SELECT 1 FROM grabs r
		                      WHERE r.adapter = $1 AND r.client_job_id = $2 AND r.created_at > me.created_at)))`,
		adapter, jobID, grabID).Scan(&reused)
	return reused, err
}

// FindTargetGrabByJob finds the target grab a client job belongs to now —
// what a restarted gateway's events need, since it no longer knows the grab's
// id: the newest grab handed to the job (JobOwner), when that is a target
// grab. An older grab on a job id given again is not found, its download
// being gone, and neither is one of another client with the same job id.
func (s *Store) FindTargetGrabByJob(ctx context.Context, adapter, jobID string) (TargetGrab, error) {
	owner, err := s.JobOwner(ctx, adapter, jobID)
	if err != nil {
		return TargetGrab{}, err
	}
	if !IsTargetGrab(owner.WantedID) {
		return TargetGrab{}, ErrNotFound
	}
	return s.GetTargetGrab(ctx, owner.WantedID)
}

// SetTargetGrabState records where a grab is and why.
func (s *Store) SetTargetGrabState(ctx context.Context, id, state, detail string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE target_grabs SET state = $2, detail = $3, updated_at = now() WHERE id = $1`, id, state, clipText(detail))
	return err
}

// MoveGrabTargets moves the targets a grab holds that are in one of from to
// another state, with its reason. A target the grab no longer holds — held by
// a reconcile meanwhile, or grabbed again — is left alone.
func (s *Store) MoveGrabTargets(ctx context.Context, grabID string, from []string, to, detail string) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE acquisition_targets SET state = $3, detail = $4, updated_at = now()
		 WHERE grab_id = $1 AND state = ANY($2)`, grabID, nonNil(from), to, clipText(detail))
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// GrabFile is what became of one target of a finished grab: the file handed
// over for it, the episode run that file covers, and the catalog's answer.
type GrabFile struct {
	Adapter, ClientJobID, GrabID, TargetID string
	State                                  string // imported | held (by an item of its own) | waiting | missing | failed
	Path                                   string
	EpisodeEnd                             *int
	ItemID                                 string
	Detail                                 string
}

// RecordGrabFile records it on the grab's grab_targets row (010). The row is
// keyed by the client job: one an earlier grab left on a job id the client
// gave again becomes the grab's whose import writes it.
func (s *Store) RecordGrabFile(ctx context.Context, f GrabFile) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO grab_targets (adapter, client_job_id, target_id, grab_id, state, file_path, episode_end, item_id, detail)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (adapter, client_job_id, target_id) DO UPDATE
		   SET grab_id = EXCLUDED.grab_id, state = EXCLUDED.state, file_path = EXCLUDED.file_path,
		       episode_end = EXCLUDED.episode_end, item_id = EXCLUDED.item_id, detail = EXCLUDED.detail,
		       updated_at = now()`,
		f.Adapter, f.ClientJobID, f.TargetID, f.GrabID, f.State, f.Path, f.EpisodeEnd, f.ItemID, clipText(f.Detail))
	return err
}

// GrabFiles reads what became of each target of a grab.
func (s *Store) GrabFiles(ctx context.Context, grabID string) ([]GrabFile, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT adapter, client_job_id, grab_id, target_id, state, file_path, episode_end, item_id, detail
		  FROM grab_targets WHERE grab_id = $1 ORDER BY target_id`, grabID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GrabFile
	for rows.Next() {
		var f GrabFile
		if err := rows.Scan(&f.Adapter, &f.ClientJobID, &f.GrabID, &f.TargetID, &f.State, &f.Path,
			&f.EpisodeEnd, &f.ItemID, &f.Detail); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// ── grabs an admin asked for ───────────────────────────────────────────────

// RequestTargetGrabs queues a search-and-grab for each target named that can
// be grabbed, for the worker that does them one at a time. It returns the ones
// it queued; a target that is held or in flight is left as it is.
func (s *Store) RequestTargetGrabs(ctx context.Context, ids []string) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		UPDATE acquisition_targets a
		   SET grab_requested_at = now(), grab_claimed_until = NULL,
		       detail = 'queued for a search and grab', updated_at = now()
		  FROM titles t
		 WHERE t.id = a.title_id AND a.id = ANY($1) AND a.state IN ('wanted','failed') AND NOT `+heldForApprovalSQL+`
		 RETURNING a.id`, nonNil(ids))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ClaimTargetGrab takes the requested grab due first and holds it for lease,
// so two replicas never work on one target. ok is false when none is due.
func (s *Store) ClaimTargetGrab(ctx context.Context, lease time.Duration) (string, bool, error) {
	var id string
	err := s.pool.QueryRow(ctx, `
		UPDATE acquisition_targets SET grab_claimed_until = now() + make_interval(secs => $1)
		 WHERE id = (
		   SELECT id FROM acquisition_targets
		    WHERE grab_requested_at IS NOT NULL
		      AND (grab_claimed_until IS NULL OR grab_claimed_until <= now())
		    ORDER BY grab_requested_at, id
		    LIMIT 1
		    FOR UPDATE SKIP LOCKED)
		 RETURNING id`, lease.Seconds()).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return id, true, nil
}

// FinishTargetGrabRequest takes a requested grab off the queue.
func (s *Store) FinishTargetGrabRequest(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE acquisition_targets SET grab_requested_at = NULL, grab_claimed_until = NULL WHERE id = $1`, id)
	return err
}

// DeferTargetGrabRequest leaves a requested grab queued and looks at it again
// after wait, saying why it waits.
func (s *Store) DeferTargetGrabRequest(ctx context.Context, id string, wait time.Duration, detail string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE acquisition_targets SET grab_claimed_until = now() + make_interval(secs => $2),
		       detail = $3, updated_at = now()
		 WHERE id = $1 AND grab_requested_at IS NOT NULL`, id, wait.Seconds(), clipText(detail))
	return err
}

// GrabsInFlight counts the target grabs handed to a client whose download the
// downloads table does not show yet. The table follows the gateway's events,
// which arrive after the hand-over, so counting it alone lets a run of grabs
// overshoot the limit on downloads at once. A pending reservation is not
// counted: it is the grab being admitted. Only a row that names the grab
// shows its download: the ended row an earlier grab left on a job id the
// client gave again does not.
func (s *Store) GrabsInFlight(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM target_grabs g
		 WHERE g.state = 'grabbed' AND g.created_at > now() - interval '1 hour'
		   AND NOT EXISTS (SELECT 1 FROM downloads d
		                    WHERE d.adapter = g.adapter AND d.client_job_id = g.client_job_id AND d.wanted_id = g.id)`).Scan(&n)
	return n, err
}

func (g TargetGrab) String() string {
	return fmt.Sprintf("%s (%s)", g.ID, g.ReleaseTitle)
}
