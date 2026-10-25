package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Target states. A target walks
//
//	wanted → grabbed → downloaded → held
//
// or ends failed, with the reason in its detail. waiting is a downloaded
// target the catalog cannot take yet (it does not take episodes by identity):
// the hand-over is tried again and the download is kept. The library losing a
// held target's file makes it wanted again.
const (
	TargetWanted     = "wanted"
	TargetGrabbed    = "grabbed"
	TargetDownloaded = "downloaded"
	TargetWaiting    = "waiting"
	TargetHeld       = "held"
	TargetFailed     = "failed"
)

// Grabbable reports whether a target in this state may be grabbed: it is not
// held and nothing for it is in flight.
func Grabbable(state string) bool { return state == TargetWanted || state == TargetFailed }

// InFlight reports whether a grab for a target in this state is under way.
func InFlight(state string) bool {
	return state == TargetGrabbed || state == TargetDownloaded || state == TargetWaiting
}

// Target is one acquisition intent: a movie, or one episode of a series.
type Target struct {
	ID            string     `json:"id"`
	TitleID       string     `json:"titleId"`
	Kind          string     `json:"kind"` // movie | episode
	SeasonNumber  *int       `json:"season"`
	EpisodeNumber *int       `json:"episode"`
	AirDate       *time.Time `json:"airDate"`
	Monitored     bool       `json:"monitored"`
	State         string     `json:"state"`

	// Detail is why the target is in its state: the grab, what the download
	// held for it, the catalog's answer, the reason it failed.
	Detail string `json:"detail"`
	// GrabID is the target grab that covers it now, or covered it last;
	// ReservedBy the pending grab it is reserved for while that grab's release
	// is handed to a client.
	GrabID     string `json:"grabId,omitempty"`
	ReservedBy string `json:"reservedBy,omitempty"`
	// GrabFailures counts the times its release failed it in a row; RetryAt
	// is when it is wanted again (nil once it is left for someone to look at).
	GrabFailures int        `json:"grabFailures"`
	RetryAt      *time.Time `json:"retryAt,omitempty"`
	// HandedOverAt is when acquire last handed a file for it to the catalog
	// and the catalog answered that it holds it.
	HandedOverAt *time.Time `json:"handedOverAt,omitempty"`

	// The last search: found | none | error, what it said, when it ran, and
	// when the target is due to be searched again.
	SearchOutcome  string     `json:"searchOutcome"`
	SearchDetail   string     `json:"searchDetail"`
	LastSearchAt   *time.Time `json:"lastSearchAt"`
	NextSearchAt   *time.Time `json:"nextSearchAt"`
	SearchFailures int        `json:"searchFailures"`

	// The HAVE side, projected from katalog.
	HeldItemID   string `json:"heldItemId,omitempty"`
	HeldVia      string `json:"heldVia,omitempty"`
	HeldPlayable bool   `json:"heldPlayable"`
	HeldRelease  string `json:"heldRelease,omitempty"`
	HeldScore    int    `json:"heldScore"`
	HeldSource   string `json:"heldSource,omitempty"` // grab | derived

	// AddedAt is when a follow or a read of TMDB on the clock made the
	// target; nil for one the import or a derivation made — the old backlog.
	// EpisodeTitle is TMDB's name of the episode.
	AddedAt      *time.Time `json:"addedAt,omitempty"`
	EpisodeTitle string     `json:"episodeTitle,omitempty"`
	// WindowAt is when the target can have a release (air_window_opens_at):
	// an episode's air date, a requested movie's release. ReleasePending is a
	// requested movie TMDB dates no release for yet; ReleaseKind what its
	// window is (digital, physical, tv, theatrical).
	WindowAt       *time.Time `json:"windowAt,omitempty"`
	ReleasePending bool       `json:"releasePending,omitempty"`
	ReleaseKind    string     `json:"releaseKind,omitempty"`
	// EverHeld is set once the library has held it. TMDBGoneAt is when a
	// read of TMDB first did not list it: it is grabbed by nobody, and goes
	// once a read a day or more later agrees, unless it holds anything.
	EverHeld   bool       `json:"everHeld,omitempty"`
	TMDBGoneAt *time.Time `json:"tmdbGoneAt,omitempty"`
	// AirStamp is the moment an episode airs, as the schedule source says
	// it; nil when no hour is known. ReleaseCountry is where a requested
	// movie's release to watch at home comes out.
	AirStamp       *time.Time `json:"airStamp,omitempty"`
	ReleaseCountry string     `json:"releaseCountry,omitempty"`
}

// targetCols are the columns scanTarget reads, under the alias a.
const targetCols = `a.id, a.title_id, a.kind, a.season_number, a.episode_number, a.air_date, a.monitored, a.state,
	a.detail, a.grab_id, a.reserved_by, a.grab_failures, a.retry_at, a.handed_over_at,
	a.search_outcome, a.search_detail, a.last_search_at, a.search_backoff_until, a.search_failures,
	a.held_item_id, a.held_via, a.held_playable, a.held_release, a.held_score, a.held_source,
	a.added_at, a.episode_title, a.air_window_opens_at, a.release_pending, a.release_kind, a.ever_held, a.tmdb_gone_at,
	a.air_stamp, a.release_country`

func scanTarget(r interface{ Scan(...any) error }) (Target, error) {
	var t Target
	err := r.Scan(&t.ID, &t.TitleID, &t.Kind, &t.SeasonNumber, &t.EpisodeNumber, &t.AirDate, &t.Monitored, &t.State,
		&t.Detail, &t.GrabID, &t.ReservedBy, &t.GrabFailures, &t.RetryAt, &t.HandedOverAt,
		&t.SearchOutcome, &t.SearchDetail, &t.LastSearchAt, &t.NextSearchAt, &t.SearchFailures,
		&t.HeldItemID, &t.HeldVia, &t.HeldPlayable, &t.HeldRelease, &t.HeldScore, &t.HeldSource,
		&t.AddedAt, &t.EpisodeTitle, &t.WindowAt, &t.ReleasePending, &t.ReleaseKind, &t.EverHeld, &t.TMDBGoneAt,
		&t.AirStamp, &t.ReleaseCountry)
	return t, err
}

func scanTargets(rows pgx.Rows) ([]Target, error) {
	var out []Target
	for rows.Next() {
		t, err := scanTarget(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// airedSQL is the condition "this target can have a release": its air window
// has opened. An episode without an air date has not aired — searching for
// one spends a source's allowance on something that may not exist — while a
// movie target, which exists once the movie is out, has no window to wait
// for; a requested movie has one, its release, and while TMDB dates none it
// waits (release_pending).
const airedSQL = `COALESCE(a.air_window_opens_at <= now(), a.kind = 'movie' AND NOT a.release_pending)`

// knownSQL is the condition "acquire knows what the library holds of this
// target": its title's holdings are known, learned after the target was
// added. A target a follow or a read of TMDB added since is of unknown
// holdings until the next reconcile — the library may hold it already.
const knownSQL = `t.holdings_at IS NOT NULL AND (a.added_at IS NULL OR a.added_at <= t.holdings_at)`

// missingSQL is what "missing" means everywhere — the Missing view, the series
// overview and the counts: aired, monitored (the target and its title), not
// held, listed by TMDB (not gone from its last read), of holdings acquire
// knows, and not of a title held for an admin's approval. A title whose
// holdings are unknown is not missing anything that acquire can tell.
const missingSQL = `a.state <> 'held' AND a.monitored AND t.monitored AND a.tmdb_gone_at IS NULL AND ` + knownSQL + ` AND ` + airedSQL +
	` AND NOT ` + heldForApprovalSQL

// UpsertTarget creates or updates a target by its coordinates. The unique index
// is (title_id, season_number, episode_number) NULLS NOT DISTINCT, so a movie —
// both coordinates NULL — collides with itself instead of duplicating.
//
// It deliberately does NOT touch the held_* columns: those are owned by the
// reconciler, and an intent import must never overwrite what we actually have.
func (s *Store) UpsertTarget(ctx context.Context, t Target) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO acquisition_targets
		  (id, title_id, kind, season_number, episode_number, air_date, monitored, state)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (title_id, season_number, episode_number) DO UPDATE
		   SET air_date  = EXCLUDED.air_date,
		       monitored = EXCLUDED.monitored,
		       -- The import says what is monitored: no follow's any more.
		       follow_monitored = false,
		       updated_at = now()`,
		t.ID, t.TitleID, t.Kind, t.SeasonNumber, t.EpisodeNumber, t.AirDate, t.Monitored, t.State)
	return err
}

// GetTarget reads one target.
func (s *Store) GetTarget(ctx context.Context, id string) (Target, error) {
	t, err := scanTarget(s.pool.QueryRow(ctx, `SELECT `+targetCols+` FROM acquisition_targets a WHERE a.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Target{}, ErrNotFound
	}
	return t, err
}

// TargetsByID reads the targets named, in no particular order. Ids that name
// no target are left out.
func (s *Store) TargetsByID(ctx context.Context, ids []string) ([]Target, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+targetCols+` FROM acquisition_targets a WHERE a.id = ANY($1)`, nonNil(ids))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTargets(rows)
}

// TitleTargets reads every target of a title, in season and episode order.
func (s *Store) TitleTargets(ctx context.Context, titleID string) ([]Target, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+targetCols+` FROM acquisition_targets a
		 WHERE a.title_id = $1 ORDER BY a.season_number NULLS FIRST, a.episode_number NULLS FIRST`, titleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTargets(rows)
}

// SetTargetState moves a target to a state, with its reason, when it is in
// one of from (any state when from is empty). It reports whether it moved.
func (s *Store) SetTargetState(ctx context.Context, id string, from []string, to, detail string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE acquisition_targets SET state = $3, detail = $4, updated_at = now()
		 WHERE id = $1 AND (cardinality($2::text[]) = 0 OR state = ANY($2))`,
		id, nonNil(from), to, clipText(detail))
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// SetTargetDetail records why a target is as it is without moving it.
func (s *Store) SetTargetDetail(ctx context.Context, id, detail string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE acquisition_targets SET detail = $2, updated_at = now() WHERE id = $1`, id, clipText(detail))
	return err
}

// Hold is the library holding a target's episode or movie.
type Hold struct {
	TargetID string
	ItemID   string
	// Via is the item that holds the file when another's covers this one.
	Via      string
	Playable bool
	// Release names the file's release when a grab produced it; Source is
	// grab then, derived for a file acquire knows nothing more of.
	Release   string
	Source    string
	ProfileID string
	Quality   map[string]any
	Score     int
	Detail    string
	// HandedOver is set when acquire handed the file over and the catalog
	// answered that it holds it: a reconcile does not take it back for a
	// while, since katalog-api may not list it yet.
	HandedOver bool
}

// HoldTarget records that the library holds a target. The quality, score and
// the profile the score was computed under travel together, as in
// ApplyHolding. A target already held by the same item keeps when it was first
// held and whether it is playable.
func (s *Store) HoldTarget(ctx context.Context, h Hold) error {
	q := h.Quality
	if q == nil {
		q = map[string]any{}
	}
	quality, err := json.Marshal(q)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		UPDATE acquisition_targets
		   SET held_playable = CASE WHEN held_item_id = $2 THEN held_playable OR $4 ELSE $4 END,
		       held_at = CASE WHEN held_item_id = $2 AND held_at IS NOT NULL THEN held_at ELSE now() END,
		       held_item_id = $2, held_via = $3, held_release = $5, held_source = $6,
		       held_score_profile = $7, held_quality = $8, held_score = $9, held_scored_at = now(),
		       state = 'held', ever_held = true, detail = $10, grab_requested_at = NULL, grab_claimed_until = NULL,
		       handed_over_at = CASE WHEN $11 THEN now() ELSE handed_over_at END,
		       grab_failures = 0, retry_at = NULL, reserved_by = '',
		       updated_at = now()
		 WHERE id = $1`,
		h.TargetID, h.ItemID, h.Via, h.Playable, h.Release, h.Source, h.ProfileID, quality, h.Score, clipText(h.Detail),
		h.HandedOver)
	return err
}

// MoveGrabTarget moves one target of a grab to another state, with its
// reason, when it is still the grab's and in one of from. It reports whether
// it moved: a target a reconcile held meanwhile, or another grab took, is
// left alone.
func (s *Store) MoveGrabTarget(ctx context.Context, grabID, targetID string, from []string, to, detail string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE acquisition_targets SET state = $4, detail = $5, updated_at = now()
		 WHERE id = $2 AND grab_id = $1 AND state = ANY($3)`, grabID, targetID, nonNil(from), to, clipText(detail))
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// Failure recovery: a target its release failed is wanted again after
// RetryBase, doubling per failure up to RetryCeiling, MaxGrabFailures times;
// then it stays failed for someone to look at.
const (
	RetryBase       = 6 * time.Hour
	RetryCeiling    = 48 * time.Hour
	MaxGrabFailures = 5
)

// LeftForAHuman starts the detail of a target that failed MaxGrabFailures
// times and is not tried again on its own.
const LeftForAHuman = "failed 5 times, not tried again on its own: "

// FailGrabTargets fails the targets of a grab that are still the grab's and
// in one of from, with the reason. When its release failed them (byRelease),
// each failure counts: the target is wanted again after the backoff, until it
// failed MaxGrabFailures times. It reports how many it failed.
func (s *Store) FailGrabTargets(ctx context.Context, grabID string, ids, from []string, reason string, byRelease bool) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE acquisition_targets
		   SET state = 'failed',
		       grab_failures = CASE WHEN $5 THEN grab_failures + 1 ELSE grab_failures END,
		       retry_at = CASE WHEN NOT $5 OR grab_failures + 1 >= $6 THEN NULL
		                       ELSE now() + make_interval(secs => LEAST($7::float8 * POWER(2, grab_failures), $8::float8)) END,
		       detail = CASE WHEN $5 AND grab_failures + 1 >= $6 THEN $9 || $4 ELSE $4 END,
		       reserved_by = '', updated_at = now()
		 WHERE grab_id = $1 AND id = ANY($2) AND state = ANY($3)`,
		grabID, nonNil(ids), nonNil(from), clipText(reason), byRelease, MaxGrabFailures,
		RetryBase.Seconds(), RetryCeiling.Seconds(), LeftForAHuman)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// RecoverFailedTargets makes the failed targets whose retry time has come
// wanted again and due at once; the releases that failed them stay blocked.
func (s *Store) RecoverFailedTargets(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE acquisition_targets
		   SET state = 'wanted', retry_at = NULL, search_backoff_until = NULL,
		       detail = 'failed ' || grab_failures || CASE WHEN grab_failures = 1 THEN ' time' ELSE ' times' END ||
		                '; wanted again, its failed releases blocked — ' || detail,
		       updated_at = now()
		 WHERE state = 'failed' AND retry_at IS NOT NULL AND retry_at <= now() AND grab_failures < $1`, MaxGrabFailures)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ApplyHolding records that katalog holds a file for this target.
//
// This is the HAVE→WANT projection, and it is the only writer of held_*. The
// score is stored together with the profile it was computed under, so a profile
// change can invalidate it; without that pairing the cutoff backlog goes stale
// silently and nothing detects it.
//
// held_source distinguishes a score derived from a real grab from one inferred
// off a pre-existing file. The 16,169 legacy episode files have no grab row, so
// their quality is guessed from assets and path — an upgrade must not fire on a
// guess, and P5 decides that policy knowing which rows are which.
func (s *Store) ApplyHolding(ctx context.Context, targetID, itemID, release, profileID, source string, quality any, score int) error {
	q, err := json.Marshal(quality)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		UPDATE acquisition_targets
		   SET held_item_id = $2, held_release = $3, held_quality = $4,
		       held_score = $5, held_score_profile = $6, held_source = $7,
		       held_scored_at = now(),
		       held_at = COALESCE(held_at, now()),
		       state = 'held', ever_held = true,
		       updated_at = now()
		 WHERE id = $1`,
		targetID, itemID, release, q, score, profileID, source)
	return err
}

// releaseHeldSQL clears what a target held and makes it wanted again. Its
// detail says whether it is hunted again: only when it and its title are
// monitored.
const releaseHeldSQL = `
		   SET held_item_id = '', held_via = '', held_playable = false, held_release = '',
		       held_quality = '{}'::jsonb, held_score = 0, held_score_profile = '', held_source = '',
		       held_scored_at = NULL, held_at = NULL,
		       state = 'wanted', ever_held = true,
		       detail = CASE WHEN a.monitored AND t.monitored THEN 'no longer in the library; wanted again'
		                     ELSE 'no longer in the library; not monitored, so not hunted' END,
		       updated_at = now()`

// ClearHolding reverses it when katalog says the file is gone. A target whose
// file was deleted becomes wanted again — that is the whole point of modelling
// WANT separately from HAVE. An episode covered by the gone item's file goes
// with it.
func (s *Store) ClearHolding(ctx context.Context, itemID string) (int64, error) {
	if itemID == "" {
		return 0, nil
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE acquisition_targets a`+releaseHeldSQL+`
		  FROM titles t
		 WHERE t.id = a.title_id AND a.state = 'held'
		   AND (a.held_item_id = $1 OR a.held_via = $1)`, itemID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// RelinkHeldItem records the item a held target is, keeping everything else
// it holds: an episode held through another's file learning its own item.
func (s *Store) RelinkHeldItem(ctx context.Context, targetID, itemID string, playable bool) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE acquisition_targets SET held_item_id = $2, held_playable = held_playable OR $3, updated_at = now()
		 WHERE id = $1 AND state = 'held'`, targetID, itemID, playable)
	return err
}

// MarkItemPlayable records that the pipeline packaged an item: every target it
// holds, or holds the file of, is playable.
func (s *Store) MarkItemPlayable(ctx context.Context, itemID string) (int64, error) {
	if itemID == "" {
		return 0, nil
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE acquisition_targets SET held_playable = true, updated_at = now()
		 WHERE state = 'held' AND NOT held_playable AND (held_item_id = $1 OR held_via = $1)`, itemID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// InvalidateHeldScores marks every cached score computed under a profile as
// stale. SaveProfile must call this, or the cutoff sweep keeps comparing new
// preferences against scores computed under the old ones.
func (s *Store) InvalidateHeldScores(ctx context.Context, profileID string) (int64, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE acquisition_targets SET held_scored_at = NULL WHERE held_score_profile = $1`,
		profileID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// openParams name the parameters autoOpenSQL reads: the air delay after an
// air date and the delay after the moment an episode airs (in seconds), and
// whether the moments the schedule source gives are used.
type openParams struct{ delay, stampDelay, stamps string }

// autoOpenSQL is airedSQL for an automatic grab. Of an episode whose air time
// the schedule source gives (air_stamp, when stamps are used): the delay after
// it has passed. Of any other target: its window has opened, and the air
// delay has passed since its air date — 00:00 UTC on the day TMDB dates an
// episode, or the day a requested movie comes out. TMDB dates the day, not
// the hour: the window opens a day before an evening broadcast in the
// Americas reaches UTC, and what turns up named for the episode before it was
// broadcast is most often a fake.
func autoOpenSQL(p openParams) string {
	return `(CASE WHEN a.air_window_opens_at IS NULL THEN a.kind = 'movie' AND NOT a.release_pending
	         WHEN ` + p.stamps + `::boolean AND a.air_stamp IS NOT NULL
	              THEN a.air_stamp + make_interval(secs => ` + p.stampDelay + `::float8) <= now()
	         ELSE GREATEST(a.air_window_opens_at,
	                       (a.air_date::timestamp AT TIME ZONE 'UTC') + make_interval(secs => ` + p.delay + `::float8)) <= now() END)`
}

// sweepableSQL is what the backlog sweep may look at, and a source's newest
// release be grabbed for: wanted (not failed, not in flight), monitored with
// its title, listed by TMDB, of holdings acquire knows, open to an automatic
// grab (autoOpenSQL), and not of a title held for an admin's approval
// (heldForApprovalSQL).
func sweepableSQL(p openParams) string {
	return `a.state = 'wanted' AND a.reserved_by = '' AND a.monitored AND t.monitored AND a.tmdb_gone_at IS NULL AND ` + knownSQL +
		` AND ` + autoOpenSQL(p) + ` AND NOT ` + heldForApprovalSQL
}

// recentSQL is the condition "this target aired in the last days days" (the
// parameter named): an episode by its air date, a requested movie by the day
// it came out — never one the library held once.
func recentSQL(days string) string {
	return `COALESCE(NOT a.ever_held AND a.air_date >= (now() AT TIME ZONE 'UTC')::date - ` + days + `::int, false)`
}

// requestedSQL is the condition "an approved request asked for this
// target": a requested movie, or an episode a follow or a read of TMDB added
// after its title was first followed — never one the library held once.
const requestedSQL = `COALESCE(NOT a.ever_held AND ` + followedSQL + ` AND ` + approvedSQL +
	` AND (a.kind = 'movie' OR a.added_at >= t.followed_at), false)`

// newSQL is the condition "this target is one of the new things": an episode
// aired in the last days days (the parameter named), or what a request asked
// for (requestedSQL) — never one the library held once: a deletion, or a
// reconcile that missed it, does not make it new again. search.autoGrabNew
// lets an automatic grab take these, and the sweep looks at them first —
// what aired recently, then what was requested; the old backlog is the rest.
func newSQL(days string) string {
	return `((a.kind = 'episode' AND ` + recentSQL(days) + `) OR ` + requestedSQL + `)`
}

// DueOrder is how the sweep picks the targets it searches: with
// NewWithinDays, the new things first — what aired in the last NewWithinDays
// days, then what a request asked for (newSQL) —; none before AirDelay has
// passed since its air date, or — with Stamps, of an episode whose air time
// the schedule source gives — StampDelay since it aired (autoOpenSQL).
type DueOrder struct {
	NewWithinDays int
	AirDelay      time.Duration
	StampDelay    time.Duration
	Stamps        bool
}

// window are the parameters of the window, in openParams' order.
func (o DueOrder) window() []any {
	return []any{o.AirDelay.Seconds(), o.StampDelay.Seconds(), o.Stamps}
}

// args are a limit, then the window ($2 to $4: dueParams).
func (o DueOrder) args(limit int) []any { return append([]any{limit}, o.window()...) }

// dueParams are the window's parameters as args places them.
var dueParams = openParams{delay: "$2", stampDelay: "$3", stamps: "$4"}

// newFirstSQL orders the new things first: what aired in the last days days
// (the parameter named), then what a request asked for — yesterday's episode
// before a requested series' back catalogue.
func newFirstSQL(days string) string {
	return recentSQL(days) + ` DESC, ` + requestedSQL + ` DESC, `
}

// DueTargets returns the targets the backlog sweep searches next: wanted,
// aired, monitored, of a title whose holdings are known, and due.
//
// Unaired episodes are excluded by the air window rather than filtered in Go:
// searching for an episode that has not aired burns indexer quota against
// releases that cannot exist, and indexer quota is the scarce resource — three
// capability sweeps were enough to rate-limit the most important indexer.
//
// A target never searched comes first, then the one searched longest ago.
// Every search records its outcome and a next due time (RecordSearch), so the
// backlog rotates: before, a target whose search found something was given no
// next time, and the sweep returned the same first targets on every run.
func (s *Store) DueTargets(ctx context.Context, limit int) ([]Target, error) {
	return s.DueTargetsBy(ctx, limit, DueOrder{})
}

// DueTargetsBy is DueTargets in the order the policy asks for (DueOrder).
func (s *Store) DueTargetsBy(ctx context.Context, limit int, o DueOrder) ([]Target, error) {
	order := `a.last_search_at NULLS FIRST, a.air_window_opens_at NULLS FIRST, a.id`
	args := o.args(limit)
	if o.NewWithinDays > 0 {
		order = newFirstSQL("$5") + order
		args = append(args, o.NewWithinDays)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+targetCols+`
		  FROM acquisition_targets a
		  JOIN titles t ON t.id = a.title_id
		 WHERE `+sweepableSQL(dueParams)+`
		   AND (a.search_backoff_until IS NULL OR a.search_backoff_until <= now())
		 ORDER BY `+order+`
		 LIMIT $1`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTargets(rows)
}

// CutoffUnmet returns held targets whose score is below what the profile now
// wants. One index scan on targets_cutoff_idx — the reason held_score is
// materialised rather than asked of katalog per row.
func (s *Store) CutoffUnmet(ctx context.Context, cutoff int, limit int) ([]Target, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+targetCols+`
		  FROM acquisition_targets a
		 WHERE a.state = 'held' AND a.monitored
		   AND a.held_score < $1
		   AND a.held_source = 'grab'   -- never upgrade off a guessed legacy score
		 ORDER BY a.held_score
		 LIMIT $2`, cutoff, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTargets(rows)
}

// BackoffSearch records a failed search and pushes the next attempt out.
// Exponential with a ceiling: a title nobody seeds should not be retried every
// sweep forever, and the ceiling stops the interval growing past usefulness.
func (s *Store) BackoffSearch(ctx context.Context, targetID string, base, max time.Duration) error {
	return s.RecordSearch(ctx, targetID, SearchRecord{Outcome: "", Base: base, Max: max})
}

// Search outcomes.
const (
	SearchFound = "found" // usable releases
	SearchNone  = "none"  // nothing usable
	SearchError = "error" // the search itself failed
)

// SearchRecord is what one search of a target found and when to look again.
type SearchRecord struct {
	Outcome string
	Detail  string
	// Again is the next due time after a search that found something. A
	// search that found nothing usable, or failed, backs off instead:
	// Base doubling per consecutive miss, up to Max.
	Again     time.Duration
	Base, Max time.Duration
}

// RecordSearch records a search's outcome on its target and when the target
// is due again. A find resets the misses; a miss, or an error, backs off.
//
// Durations go to Postgres as seconds: a Go duration's text (1ms, 1µs, 1ns)
// is not always an interval Postgres reads.
func (s *Store) RecordSearch(ctx context.Context, targetID string, r SearchRecord) error {
	if r.Outcome == SearchFound {
		_, err := s.pool.Exec(ctx, `
			UPDATE acquisition_targets
			   SET search_failures = 0, last_search_at = now(),
			       search_backoff_until = now() + make_interval(secs => $2),
			       search_outcome = $3, search_detail = $4, updated_at = now()
			 WHERE id = $1`, targetID, r.Again.Seconds(), r.Outcome, clipText(r.Detail))
		return err
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE acquisition_targets
		   SET search_failures = search_failures + 1,
		       last_search_at = now(),
		       search_backoff_until = now() + make_interval(secs => LEAST(
		         $2::float8 * POWER(2, LEAST(search_failures, 10)), $3::float8)),
		       search_outcome = CASE WHEN $4::text = '' THEN search_outcome ELSE $4::text END,
		       search_detail = CASE WHEN $4::text = '' THEN search_detail ELSE $5::text END,
		       updated_at = now()
		 WHERE id = $1`, targetID, r.Base.Seconds(), r.Max.Seconds(), r.Outcome, clipText(r.Detail))
	return err
}

// ── holdings ───────────────────────────────────────────────────────────────

// HeldEpisode is one episode the library holds, as its holdings answer it.
type HeldEpisode struct {
	Season, Episode int
	ItemID          string
	Via             string // the item holding the file, when another's covers it
	Playable        bool
}

// HoldingsChange is what applying a title's holdings moved.
type HoldingsChange struct {
	Held     int64 `json:"held"`     // targets now held that were not, or held by another item
	Released int64 `json:"released"` // held targets the library no longer holds
}

// ApplySeriesHoldings is ApplySeriesHoldingsAt for holdings asked now.
func (s *Store) ApplySeriesHoldings(ctx context.Context, titleID, catalogID, profileID string, held []HeldEpisode) (HoldingsChange, error) {
	return s.ApplySeriesHoldingsAt(ctx, titleID, catalogID, profileID, held, time.Now())
}

// ApplySeriesHoldingsAt makes a series' targets match what the library holds
// of it, and records that its holdings are known as of asked, when the
// catalog was asked: a target added since is of unknown holdings until the
// next reconcile. A held episode acquire tracks no target for is ignored: the
// library holds things nobody monitors, and they must not become things
// acquire hunts. A target in flight that the library now holds is held; one in
// flight that it does not hold is left to its grab.
func (s *Store) ApplySeriesHoldingsAt(ctx context.Context, titleID, catalogID, profileID string, held []HeldEpisode, asked time.Time) (HoldingsChange, error) {
	seasons := make([]int32, 0, len(held))
	episodes := make([]int32, 0, len(held))
	items := make([]string, 0, len(held))
	vias := make([]string, 0, len(held))
	playable := make([]bool, 0, len(held))
	for _, h := range held {
		seasons, episodes = append(seasons, int32(h.Season)), append(episodes, int32(h.Episode))
		items, vias, playable = append(items, h.ItemID), append(vias, h.Via), append(playable, h.Playable)
	}
	return s.applyHoldings(ctx, titleID, catalogID, asked, func(tx pgx.Tx) (HoldingsChange, error) {
		var ch HoldingsChange
		tag, err := tx.Exec(ctx, `
			WITH h AS (
			  SELECT * FROM unnest($2::int[], $3::int[], $4::text[], $5::text[], $6::bool[])
			           AS h(season, episode, item, via, playable))
			UPDATE acquisition_targets a`+holdSet("$7")+`
			  FROM h
			 WHERE a.title_id = $1 AND a.season_number = h.season AND a.episode_number = h.episode
			   AND (a.state <> 'held' OR a.held_item_id <> h.item OR a.held_via <> h.via
			        OR a.held_playable <> h.playable)`,
			titleID, seasons, episodes, items, vias, playable, profileID)
		if err != nil {
			return ch, err
		}
		ch.Held = tag.RowsAffected()
		tag, err = tx.Exec(ctx, `
			UPDATE acquisition_targets a`+releaseHeldSQL+`
			  FROM titles t
			 WHERE t.id = a.title_id AND a.title_id = $1 AND a.state = 'held'
			   AND a.season_number IS NOT NULL AND `+notJustHandedOver+`
			   AND NOT EXISTS (SELECT 1 FROM unnest($2::int[], $3::int[]) AS h(season, episode)
			                    WHERE h.season = a.season_number AND h.episode = a.episode_number)`,
			titleID, seasons, episodes)
		if err != nil {
			return ch, err
		}
		ch.Released = tag.RowsAffected()
		return ch, nil
	})
}

// notJustHandedOver keeps a reconcile from taking back a target acquire handed
// over in the last half hour: the catalog answered that it holds it, and
// katalog-api may not list it yet.
const notJustHandedOver = `(a.handed_over_at IS NULL OR a.handed_over_at < now() - interval '30 minutes')`

// holdSet holds a target from a holdings row h (item, via, playable) and the
// profile in the parameter named. A target held before by the same file keeps
// what it knew of it: by the same item, or — an episode a file of several
// covers — through the same holder, as a grab's hand-over records it before
// the holdings name the episode's own item. One the library holds by another
// file, or newly, is a derived holding: acquire knows nothing of its release.
func holdSet(profileParam string) string {
	const same = `(a.state = 'held' AND (a.held_item_id = h.item OR (h.via <> '' AND (a.held_item_id = h.via OR a.held_via = h.via))))`
	return `
			   SET held_source = CASE WHEN ` + same + ` AND a.held_source <> '' THEN a.held_source ELSE 'derived' END,
			       held_release = CASE WHEN ` + same + ` THEN a.held_release ELSE '' END,
			       held_quality = CASE WHEN ` + same + ` THEN a.held_quality ELSE '{}'::jsonb END,
			       held_score = CASE WHEN ` + same + ` THEN a.held_score ELSE 0 END,
			       held_score_profile = CASE WHEN ` + same + ` AND a.held_score_profile <> ''
			                                 THEN a.held_score_profile ELSE ` + profileParam + `::text END,
			       held_scored_at = CASE WHEN ` + same + ` THEN a.held_scored_at ELSE now() END,
			       held_at = CASE WHEN ` + same + ` AND a.held_at IS NOT NULL THEN a.held_at ELSE now() END,
			       held_item_id = h.item, held_via = h.via, held_playable = h.playable,
			       detail = CASE WHEN a.state = 'held' THEN a.detail ELSE 'in the library' END,
			       state = 'held', ever_held = true, grab_requested_at = NULL, grab_claimed_until = NULL,
			       grab_failures = 0, retry_at = NULL, reserved_by = '',
			       updated_at = now()`
}

// ApplyMovieHolding is ApplyMovieHoldingAt for a holding asked now.
func (s *Store) ApplyMovieHolding(ctx context.Context, titleID, catalogID, profileID string, held *HeldEpisode) (HoldingsChange, error) {
	return s.ApplyMovieHoldingAt(ctx, titleID, catalogID, profileID, held, time.Now())
}

// ApplyMovieHoldingAt makes a movie's target match what the library holds of
// it: held by the item named, or — held nil — not held, as of asked. A movie
// the library holds gets its target when it has none yet.
func (s *Store) ApplyMovieHoldingAt(ctx context.Context, titleID, catalogID, profileID string, held *HeldEpisode, asked time.Time) (HoldingsChange, error) {
	return s.applyHoldings(ctx, titleID, catalogID, asked, func(tx pgx.Tx) (HoldingsChange, error) {
		var ch HoldingsChange
		if held == nil {
			tag, err := tx.Exec(ctx, `
				UPDATE acquisition_targets a`+releaseHeldSQL+`
				  FROM titles t
				 WHERE t.id = a.title_id AND a.title_id = $1 AND a.state = 'held' AND a.season_number IS NULL
				   AND `+notJustHandedOver, titleID)
			if err != nil {
				return ch, err
			}
			ch.Released = tag.RowsAffected()
			return ch, nil
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO acquisition_targets (id, title_id, kind, monitored, state)
			SELECT $2::text, t.id, 'movie', t.monitored, 'wanted' FROM titles t WHERE t.id = $1
			ON CONFLICT (title_id, season_number, episode_number) DO NOTHING`,
			titleID, TargetID(titleID, nil, nil)); err != nil {
			return ch, err
		}
		tag, err := tx.Exec(ctx, `
			WITH h AS (SELECT $2::text AS item, $3::text AS via, $4::bool AS playable)
			UPDATE acquisition_targets a`+holdSet("$5")+`
			  FROM h
			 WHERE a.title_id = $1 AND a.season_number IS NULL AND a.episode_number IS NULL
			   AND (a.state <> 'held' OR a.held_item_id <> h.item OR a.held_playable <> h.playable)`,
			titleID, held.ItemID, held.Via, held.Playable, profileID)
		if err != nil {
			return ch, err
		}
		ch.Held = tag.RowsAffected()
		return ch, nil
	})
}

// applyHoldings runs one title's holdings change and records, in the same
// transaction, that the title's holdings are known as of asked.
func (s *Store) applyHoldings(ctx context.Context, titleID, catalogID string, asked time.Time, apply func(pgx.Tx) (HoldingsChange, error)) (HoldingsChange, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return HoldingsChange{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	ch, err := apply(tx)
	if err != nil {
		return HoldingsChange{}, fmt.Errorf("apply holdings of %s: %w", titleID, err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE titles SET holdings_at = $3, holdings_error = '', catalog_id = $2, updated_at = now()
		 WHERE id = $1`, titleID, catalogID, asked); err != nil {
		return HoldingsChange{}, err
	}
	return ch, tx.Commit(ctx)
}

// HoldingsUnknown records that the holdings of the titles named — every title
// when none is named — cannot be known, and why. Their targets stop being
// missing and are never grabbed automatically, until a reconcile succeeds.
func (s *Store) HoldingsUnknown(ctx context.Context, titleIDs []string, reason string) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE titles SET holdings_at = NULL, holdings_error = $2, updated_at = now()
		 WHERE cardinality($1::text[]) = 0 OR id = ANY($1)`, nonNil(titleIDs), clipText(reason))
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// HoldingsFailed records why the holdings of the titles named — every title
// when none is named — could not be read this time. What was known before
// stays known: a catalog that is down for a minute does not make the library
// empty, nor full.
func (s *Store) HoldingsFailed(ctx context.Context, titleIDs []string, reason string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE titles SET holdings_error = $2, updated_at = now()
		 WHERE cardinality($1::text[]) = 0 OR id = ANY($1)`, nonNil(titleIDs), clipText(reason))
	return err
}

func clipText(s string) string {
	const max = 500
	if len(s) <= max {
		return s
	}
	return s[:max]
}
