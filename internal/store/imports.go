package store

import (
	"context"
	"errors"
	"fmt"
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

// ErrImportBusy: a client job's import is another download's, and not
// finished (ImportBusyError).
var ErrImportBusy = errors.New("the job's import is another download's, not finished yet")

// ImportBusyError is an import not recorded because its client job still
// holds the import of another grab, pending or imported: that grab's id and
// its import's state.
type ImportBusyError struct{ WantedID, State string }

func (e *ImportBusyError) Error() string {
	return "the job's import is " + e.WantedID + "'s, still " + e.State
}

func (e *ImportBusyError) Is(target error) bool { return target == ErrImportBusy }

// EnqueueImport records a finished download to import, due now, and reports
// whether this call recorded it. A download already recorded is left as it
// is: a completed event can arrive twice.
//
// An import is keyed by its client job, and a client may give a job id again
// once it forgot the download that had it (see JobOwner). Another grab's
// import that is finished — done or failed — gives way: the row becomes this
// one's, pending with its files, as new. One that is not finished is neither
// replaced nor joined: ImportBusyError says whose it is, and the caller tries
// again once it is done.
func (s *Store) EnqueueImport(ctx context.Context, im Import) (created bool, err error) {
	for range 3 {
		tag, err := s.pool.Exec(ctx, `
			INSERT INTO imports (adapter, client_job_id, wanted_id, files)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (adapter, client_job_id) DO UPDATE
			   SET wanted_id = EXCLUDED.wanted_id, files = EXCLUDED.files, state = 'pending', attempts = 0,
			       detail = '', arrival = '', next_at = now(), created_at = now(), updated_at = now()
			 WHERE imports.wanted_id <> EXCLUDED.wanted_id AND imports.state IN ('done', 'failed')`,
			im.Adapter, im.ClientJobID, im.WantedID, nonNil(im.Files))
		if err != nil {
			return false, err
		}
		if tag.RowsAffected() == 1 {
			return true, nil
		}
		// Nothing written: the job's import is this download's already, or
		// another's that is not finished.
		var wantedID, state string
		err = s.pool.QueryRow(ctx, `SELECT wanted_id, state FROM imports WHERE adapter = $1 AND client_job_id = $2`,
			im.Adapter, im.ClientJobID).Scan(&wantedID, &state)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			continue // gone meanwhile: recorded on the next pass
		case err != nil:
			return false, err
		case wantedID == im.WantedID:
			return false, nil
		case state == ImportPending || state == ImportImported:
			return false, &ImportBusyError{WantedID: wantedID, State: state}
		}
		// The other import finished meanwhile: it gives way on the next pass.
	}
	return false, fmt.Errorf("the import of %s/%s kept changing while it was recorded", im.Adapter, im.ClientJobID)
}

// ClaimImport takes the import that is due first — pending, or imported and
// waiting on its client — and holds it for lease: until then no other claim
// returns it, so two replicas never work on one download. The holder saves it
// (SaveImport) when it is done with it; a holder that dies leaves it to be
// claimed again once the lease runs out. ok is false when nothing is due. The
// row is picked and taken in one statement, so what it returns is the import
// its job held then, whoever's it is; an unfinished import is never taken
// over (EnqueueImport) while it is held.
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
// detail, arrival and when it is due next. It writes the row only while it is
// still the import's of im.WantedID: one another grab's import took over since
// (EnqueueImport) is ErrNotFound.
func (s *Store) SaveImport(ctx context.Context, im Import) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE imports SET state = $3, attempts = $4, detail = $5, arrival = $6, next_at = $7, updated_at = now()
		 WHERE adapter = $1 AND client_job_id = $2 AND wanted_id = $8`,
		im.Adapter, im.ClientJobID, im.State, im.Attempts, im.Detail, im.Arrival, im.NextAt, im.WantedID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GetImport returns the import a client job holds, or ErrNotFound. On a job
// id the client gave again it may be another grab's than the one the caller
// has in mind: compare its WantedID.
func (s *Store) GetImport(ctx context.Context, adapter, clientJobID string) (Import, error) {
	im, err := scanImport(s.pool.QueryRow(ctx,
		`SELECT `+importCols+` FROM imports WHERE adapter = $1 AND client_job_id = $2`, adapter, clientJobID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Import{}, ErrNotFound
	}
	return im, err
}
