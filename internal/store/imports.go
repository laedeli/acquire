package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Import states (migration 064).
const (
	ImportPending  = "pending"  // the copy or the hand-over is still to do
	ImportImported = "imported" // the catalog has the copy; the client still holds the download
	ImportDone     = "done"     // nothing more to do
	ImportFailed   = "failed"   // the catalog refused the hand-over
)

// Import is one finished download acquire copies into the library's arrivals
// and then releases in its client. Files are the paths the client reported,
// as it sees them; Arrival is the copy handed to the catalog.
type Import struct {
	Adapter     string
	ClientJobID string
	WantedID    string
	Files       []string
	State       string
	Attempts    int
	Detail      string
	Arrival     string
	NextAt      time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

const importCols = `adapter, client_job_id, wanted_id, files, state, attempts, detail, arrival, next_at, created_at, updated_at`

func scanImport(r interface{ Scan(...any) error }) (Import, error) {
	var im Import
	err := r.Scan(&im.Adapter, &im.ClientJobID, &im.WantedID, &im.Files, &im.State, &im.Attempts,
		&im.Detail, &im.Arrival, &im.NextAt, &im.CreatedAt, &im.UpdatedAt)
	if im.Files == nil {
		im.Files = []string{}
	}
	return im, err
}

// EnqueueImport records a finished download to import, due now. A download
// that is already recorded is left as it is (a completed event can arrive
// twice); created reports whether this call recorded it.
func (s *Store) EnqueueImport(ctx context.Context, im Import) (created bool, err error) {
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO imports (adapter, client_job_id, wanted_id, files)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (adapter, client_job_id) DO NOTHING`,
		im.Adapter, im.ClientJobID, im.WantedID, nonNil(im.Files))
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// ClaimImport takes the import that is due first — pending, or imported and
// waiting on its client — and holds it for lease: until then no other claim
// returns it, so two replicas never work on one download. The holder saves it
// (SaveImport) when it is done with it; a holder that dies leaves it to be
// claimed again once the lease runs out. ok is false when nothing is due.
func (s *Store) ClaimImport(ctx context.Context, lease time.Duration) (Import, bool, error) {
	im, err := scanImport(s.pool.QueryRow(ctx, `
		UPDATE imports SET next_at = now() + make_interval(secs => $1), updated_at = now()
		 WHERE (adapter, client_job_id) = (
		   SELECT adapter, client_job_id FROM imports
		    WHERE state IN ('pending', 'imported') AND next_at <= now()
		    ORDER BY next_at, created_at
		    LIMIT 1
		    FOR UPDATE SKIP LOCKED)
		 RETURNING `+importCols, lease.Seconds()))
	if errors.Is(err, pgx.ErrNoRows) {
		return Import{}, false, nil
	}
	if err != nil {
		return Import{}, false, err
	}
	return im, true, nil
}

// SaveImport writes what working on an import changed: its state, attempts,
// detail, arrival and when it is due next.
func (s *Store) SaveImport(ctx context.Context, im Import) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE imports SET state = $3, attempts = $4, detail = $5, arrival = $6, next_at = $7, updated_at = now()
		 WHERE adapter = $1 AND client_job_id = $2`,
		im.Adapter, im.ClientJobID, im.State, im.Attempts, im.Detail, im.Arrival, im.NextAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GetImport returns one import or ErrNotFound.
func (s *Store) GetImport(ctx context.Context, adapter, clientJobID string) (Import, error) {
	im, err := scanImport(s.pool.QueryRow(ctx,
		`SELECT `+importCols+` FROM imports WHERE adapter = $1 AND client_job_id = $2`, adapter, clientJobID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Import{}, ErrNotFound
	}
	return im, err
}
