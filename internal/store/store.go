// Package store is acquire's Postgres data layer for wanted_items + grabs.
package store

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

var ErrNotFound = errors.New("not found")

type Store struct{ pool *pgxpool.Pool }

func New(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	s := &Store{pool: pool}
	if err := migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	return s, nil
}

// migrate applies every embedded migration in name order, on every boot. Each
// file must be idempotent (CREATE/ALTER ... IF NOT EXISTS) — that keeps a
// re-run harmless and means a fresh database and an existing one converge on
// the same schema without a version table to drift out of sync.
func migrate(ctx context.Context, pool *pgxpool.Pool) error {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		body, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		if _, err := pool.Exec(ctx, string(body)); err != nil {
			return fmt.Errorf("apply %s: %w", name, err)
		}
	}
	return nil
}

func (s *Store) Close() {
	if s.pool != nil {
		s.pool.Close()
	}
}

func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// Wanted is one requested title.
type Wanted struct {
	ID          string    `json:"id"`
	TMDBID      int64     `json:"tmdbId"`
	MediaType   string    `json:"mediaType"`
	Title       string    `json:"title"`
	Year        int       `json:"year"`
	PosterURL   string    `json:"posterUrl"`
	RequestedBy string    `json:"requestedBy"`
	RequestedAt time.Time `json:"requestedAt"`
	Status      string    `json:"status"`
	Detail      string    `json:"detail"`
	ItemID      string    `json:"itemId"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

const wantedCols = `id, tmdb_id, media_type, title, year, poster_url, requested_by, requested_at, status, detail, item_id, updated_at`

func scanWanted(r interface{ Scan(...any) error }) (Wanted, error) {
	var w Wanted
	var tmdb, year *int64
	err := r.Scan(&w.ID, &tmdb, &w.MediaType, &w.Title, &year, &w.PosterURL,
		&w.RequestedBy, &w.RequestedAt, &w.Status, &w.Detail, &w.ItemID, &w.UpdatedAt)
	if tmdb != nil {
		w.TMDBID = *tmdb
	}
	if year != nil {
		w.Year = int(*year)
	}
	return w, err
}

func (s *Store) CreateWanted(ctx context.Context, w Wanted) error {
	var tmdb, year *int64
	if w.TMDBID != 0 {
		tmdb = &w.TMDBID
	}
	if w.Year != 0 {
		y := int64(w.Year)
		year = &y
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO wanted_items (id, tmdb_id, media_type, title, year, poster_url, requested_by, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,'pending')`,
		w.ID, tmdb, w.MediaType, w.Title, year, w.PosterURL, w.RequestedBy)
	return err
}

func (s *Store) ListWanted(ctx context.Context) ([]Wanted, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+wantedCols+` FROM wanted_items ORDER BY requested_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Wanted
	for rows.Next() {
		w, err := scanWanted(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *Store) GetWanted(ctx context.Context, id string) (Wanted, error) {
	w, err := scanWanted(s.pool.QueryRow(ctx, `SELECT `+wantedCols+` FROM wanted_items WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Wanted{}, ErrNotFound
	}
	return w, err
}

// FindWantedByTMDB returns the newest non-terminal request for a tmdb id (used
// by the slot status feed + fulfillment matching).
func (s *Store) FindWantedByTMDB(ctx context.Context, tmdbID int64) (Wanted, error) {
	w, err := scanWanted(s.pool.QueryRow(ctx,
		`SELECT `+wantedCols+` FROM wanted_items WHERE tmdb_id=$1 ORDER BY requested_at DESC LIMIT 1`, tmdbID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Wanted{}, ErrNotFound
	}
	return w, err
}

// FindWantedByItemID resolves the request a catalog item belongs to (set during
// ingest) — used to mark fulfillment on catalog.item.packaged.
func (s *Store) FindWantedByItemID(ctx context.Context, itemID string) (Wanted, error) {
	w, err := scanWanted(s.pool.QueryRow(ctx,
		`SELECT `+wantedCols+` FROM wanted_items WHERE item_id=$1 ORDER BY updated_at DESC LIMIT 1`, itemID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Wanted{}, ErrNotFound
	}
	return w, err
}

func (s *Store) DeleteWanted(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM wanted_items WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetStatus updates status + detail (+ optional item id) and stamps updated_at.
func (s *Store) SetStatus(ctx context.Context, id, status, detail string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE wanted_items SET status=$2, detail=$3, updated_at=now() WHERE id=$1`, id, status, detail)
	return err
}

func (s *Store) SetItemID(ctx context.Context, id, itemID string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE wanted_items SET item_id=$2, updated_at=now() WHERE id=$1`, id, itemID)
	return err
}

// FindWantedByClientJob resolves a wanted id from a gateway job id (download
// events carry client_job_id, not the wanted id — grabs bridges them).
func (s *Store) FindWantedByClientJob(ctx context.Context, clientJobID string) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx,
		`SELECT wanted_id FROM grabs WHERE client_job_id=$1 ORDER BY created_at DESC LIMIT 1`, clientJobID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return id, err
}

func (s *Store) RecordGrab(ctx context.Context, wantedID, adapter, clientJobID, source string) error {
	return s.RecordGrabRelease(ctx, Grab{
		WantedID: wantedID, Adapter: adapter, ClientJobID: clientJobID, Source: source,
	})
}

// Grab is one hand-off of a release to a download client, including which
// release won so the console can show it long after the search is gone.
type Grab struct {
	WantedID     string    `json:"wantedId"`
	Adapter      string    `json:"adapter"`
	ClientJobID  string    `json:"clientJobId"`
	Source       string    `json:"source"`
	ReleaseTitle string    `json:"releaseTitle"`
	Indexer      string    `json:"indexer"`
	Protocol     string    `json:"protocol"`
	SizeBytes    int64     `json:"sizeBytes"`
	Seeders      *int32    `json:"seeders"`
	Reason       string    `json:"reason"`
	CreatedAt    time.Time `json:"createdAt"`
}

// RecordGrabRelease stores the grab together with the chosen release.
func (s *Store) RecordGrabRelease(ctx context.Context, g Grab) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO grabs (wanted_id, adapter, client_job_id, source,
		                    release_title, indexer, protocol, size_bytes, seeders, reason)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		 ON CONFLICT DO NOTHING`,
		g.WantedID, g.Adapter, g.ClientJobID, g.Source,
		g.ReleaseTitle, g.Indexer, g.Protocol, g.SizeBytes, g.Seeders, g.Reason)
	return err
}

// LatestGrab returns the most recent grab for a request (the release the user
// is currently waiting on), or ErrNotFound.
func (s *Store) LatestGrab(ctx context.Context, wantedID string) (Grab, error) {
	var g Grab
	err := s.pool.QueryRow(ctx,
		`SELECT wanted_id, adapter, client_job_id, source, release_title, indexer,
		        protocol, size_bytes, seeders, reason, created_at
		   FROM grabs WHERE wanted_id=$1 ORDER BY created_at DESC LIMIT 1`, wantedID).
		Scan(&g.WantedID, &g.Adapter, &g.ClientJobID, &g.Source, &g.ReleaseTitle, &g.Indexer,
			&g.Protocol, &g.SizeBytes, &g.Seeders, &g.Reason, &g.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Grab{}, ErrNotFound
	}
	return g, err
}

// Download is the live (or final) state of one client job.
type Download struct {
	Adapter     string     `json:"adapter"`
	ClientJobID string     `json:"clientJobId"`
	WantedID    string     `json:"wantedId"`
	Title       string     `json:"title"`
	State       string     `json:"state"`
	NativeState string     `json:"nativeState"`
	ProgressPct float64    `json:"progressPct"`
	BytesDone   int64      `json:"bytesDone"`
	BytesTotal  int64      `json:"bytesTotal"`
	SpeedBps    int64      `json:"speedBps"`
	EtaSec      *int32     `json:"etaSec"`
	Seeders     *int32     `json:"seeders"`
	Leechers    *int32     `json:"leechers"`
	Health      *int32     `json:"health"`
	Error       string     `json:"error"`
	StartedAt   time.Time  `json:"startedAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
	FinishedAt  *time.Time `json:"finishedAt"`
}

// UpsertDownload records the newest telemetry for a client job and returns the
// merged row.
//
// Merge rules exist because the events are NOT uniform: progress carries full
// telemetry, while completed/failed carry almost none. Overwriting blindly
// would zero a finished download's numbers — and since the gateway stops
// tracking a job once it is terminal, nothing would ever repair it. So:
//   - terminal events keep the telemetry the progress stream accumulated
//     (completed additionally pins 100% / full bytes);
//   - "unknown" (NULL) peer counts never overwrite a known value;
//   - a terminal row is absorbing: a late progress message cannot resurrect it.
func (s *Store) UpsertDownload(ctx context.Context, d Download) (Download, error) {
	terminal := d.State == "completed" || d.State == "failed"
	row := s.pool.QueryRow(ctx,
		`INSERT INTO downloads (adapter, client_job_id, wanted_id, title, state, native_state,
		                        progress_pct, bytes_done, bytes_total, speed_bps, eta_sec,
		                        seeders, leechers, health, error, updated_at, finished_at)
		 VALUES ($1,$2,NULLIF($3,''),$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,now(),
		         CASE WHEN $16 THEN now() ELSE NULL END)
		 ON CONFLICT (adapter, client_job_id) DO UPDATE SET
		   wanted_id   = COALESCE(NULLIF(EXCLUDED.wanted_id,''), downloads.wanted_id),
		   title       = CASE WHEN EXCLUDED.title <> '' THEN EXCLUDED.title ELSE downloads.title END,
		   state       = EXCLUDED.state,
		   native_state= CASE WHEN EXCLUDED.native_state <> '' THEN EXCLUDED.native_state
		                      ELSE downloads.native_state END,
		   progress_pct= CASE WHEN NOT $16 THEN EXCLUDED.progress_pct
		                      WHEN EXCLUDED.state = 'completed' THEN 100
		                      ELSE downloads.progress_pct END,
		   bytes_done  = CASE WHEN NOT $16 THEN EXCLUDED.bytes_done
		                      WHEN EXCLUDED.state = 'completed'
		                        THEN GREATEST(downloads.bytes_done, downloads.bytes_total, EXCLUDED.bytes_total)
		                      ELSE downloads.bytes_done END,
		   bytes_total = CASE WHEN EXCLUDED.bytes_total > 0 THEN EXCLUDED.bytes_total
		                      ELSE downloads.bytes_total END,
		   speed_bps   = CASE WHEN $16 THEN 0 ELSE EXCLUDED.speed_bps END,
		   eta_sec     = CASE WHEN $16 THEN NULL ELSE EXCLUDED.eta_sec END,
		   seeders     = COALESCE(EXCLUDED.seeders, downloads.seeders),
		   leechers    = COALESCE(EXCLUDED.leechers, downloads.leechers),
		   health      = COALESCE(EXCLUDED.health, downloads.health),
		   error       = CASE WHEN EXCLUDED.error <> '' THEN EXCLUDED.error ELSE downloads.error END,
		   updated_at  = now(),
		   finished_at = COALESCE(downloads.finished_at, EXCLUDED.finished_at)
		 WHERE downloads.state NOT IN ('completed','failed') OR $16
		 RETURNING adapter, client_job_id, COALESCE(wanted_id,''), title, state, native_state,
		           progress_pct, bytes_done, bytes_total, speed_bps, eta_sec,
		           seeders, leechers, health, error, started_at, updated_at, finished_at`,
		d.Adapter, d.ClientJobID, d.WantedID, d.Title, d.State, d.NativeState,
		d.ProgressPct, d.BytesDone, d.BytesTotal, d.SpeedBps, d.EtaSec,
		d.Seeders, d.Leechers, d.Health, d.Error, terminal)

	var out Download
	err := row.Scan(&out.Adapter, &out.ClientJobID, &out.WantedID, &out.Title, &out.State,
		&out.NativeState, &out.ProgressPct, &out.BytesDone, &out.BytesTotal, &out.SpeedBps,
		&out.EtaSec, &out.Seeders, &out.Leechers, &out.Health, &out.Error,
		&out.StartedAt, &out.UpdatedAt, &out.FinishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		// The WHERE guard suppressed the update (a stale progress message for an
		// already-finished job). Report the row as it stands.
		return s.GetDownload(ctx, d.Adapter, d.ClientJobID)
	}
	return out, err
}

// GetDownload returns one client job's row.
func (s *Store) GetDownload(ctx context.Context, adapter, clientJobID string) (Download, error) {
	var d Download
	err := s.pool.QueryRow(ctx,
		`SELECT adapter, client_job_id, COALESCE(wanted_id,''), title, state, native_state,
		        progress_pct, bytes_done, bytes_total, speed_bps, eta_sec,
		        seeders, leechers, health, error, started_at, updated_at, finished_at
		   FROM downloads WHERE adapter=$1 AND client_job_id=$2`, adapter, clientJobID).
		Scan(&d.Adapter, &d.ClientJobID, &d.WantedID, &d.Title, &d.State, &d.NativeState,
			&d.ProgressPct, &d.BytesDone, &d.BytesTotal, &d.SpeedBps, &d.EtaSec,
			&d.Seeders, &d.Leechers, &d.Health, &d.Error, &d.StartedAt, &d.UpdatedAt, &d.FinishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Download{}, ErrNotFound
	}
	return d, err
}

// ListDownloads returns active downloads first, then recently finished ones.
func (s *Store) ListDownloads(ctx context.Context, limit int) ([]Download, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx,
		`SELECT adapter, client_job_id, COALESCE(wanted_id,''), title, state, native_state,
		        progress_pct, bytes_done, bytes_total, speed_bps, eta_sec,
		        seeders, leechers, health, error, started_at, updated_at, finished_at
		   FROM downloads
		  ORDER BY (state IN ('queued','downloading')) DESC, updated_at DESC
		  LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Download{}
	for rows.Next() {
		var d Download
		if err := rows.Scan(&d.Adapter, &d.ClientJobID, &d.WantedID, &d.Title, &d.State,
			&d.NativeState, &d.ProgressPct, &d.BytesDone, &d.BytesTotal, &d.SpeedBps,
			&d.EtaSec, &d.Seeders, &d.Leechers, &d.Health, &d.Error,
			&d.StartedAt, &d.UpdatedAt, &d.FinishedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ActiveDownloadFor returns the in-flight download for a request, if any.
func (s *Store) ActiveDownloadFor(ctx context.Context, wantedID string) (Download, error) {
	var d Download
	err := s.pool.QueryRow(ctx,
		`SELECT adapter, client_job_id, COALESCE(wanted_id,''), title, state, native_state,
		        progress_pct, bytes_done, bytes_total, speed_bps, eta_sec,
		        seeders, leechers, health, error, started_at, updated_at, finished_at
		   FROM downloads WHERE wanted_id=$1
		  ORDER BY (state IN ('queued','downloading')) DESC, updated_at DESC LIMIT 1`, wantedID).
		Scan(&d.Adapter, &d.ClientJobID, &d.WantedID, &d.Title, &d.State, &d.NativeState,
			&d.ProgressPct, &d.BytesDone, &d.BytesTotal, &d.SpeedBps, &d.EtaSec,
			&d.Seeders, &d.Leechers, &d.Health, &d.Error, &d.StartedAt, &d.UpdatedAt, &d.FinishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Download{}, ErrNotFound
	}
	return d, err
}

// DeleteDownload drops a job row (used when a download is cancelled).
func (s *Store) DeleteDownload(ctx context.Context, adapter, clientJobID string) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM downloads WHERE adapter=$1 AND client_job_id=$2`, adapter, clientJobID)
	return err
}
