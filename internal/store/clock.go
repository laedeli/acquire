package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
)

// Saga is one durable timeout. Kafka has no delayed delivery, so every "come
// back to this later" in the system — retry backoff, a search that has taken
// too long, an air-date window, "did the ingest we asked for actually land?" —
// is a row with a deadline that survives a restart.
type Saga struct {
	ID         string
	Kind       string
	Subject    string
	DeadlineAt time.Time
	Attempts   int
	Data       json.RawMessage
}

// ClaimDueSchedules advances every enabled schedule whose time has come and
// returns their names. Advancing and reporting happen in the SAME statement, so
// a schedule cannot fire twice: whoever wins the UPDATE owns this run.
//
// next_run_at moves to now()+interval rather than next_run_at+interval on
// purpose. Catch-up semantics would make a job that was disabled for a day fire
// a day's worth of backlog the moment it is re-enabled.
func ClaimDueSchedules(ctx context.Context, tx pgx.Tx) ([]string, error) {
	rows, err := tx.Query(ctx, `
		UPDATE schedules
		   SET next_run_at = now() + make_interval(secs => interval_secs),
		       last_run_at = now()
		 WHERE enabled AND next_run_at <= now()
	 RETURNING name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// ClaimDueSagas takes expired sagas and marks them fired in one statement, for
// the same reason.
func ClaimDueSagas(ctx context.Context, tx pgx.Tx) ([]Saga, error) {
	rows, err := tx.Query(ctx, `
		UPDATE sagas
		   SET state = 'fired', attempts = attempts + 1, updated_at = now()
		 WHERE state = 'pending' AND deadline_at <= now()
	 RETURNING id, kind, subject, deadline_at, attempts, data`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Saga
	for rows.Next() {
		var s Saga
		if err := rows.Scan(&s.ID, &s.Kind, &s.Subject, &s.DeadlineAt, &s.Attempts, &s.Data); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ArmSaga schedules (or re-schedules) a timeout. Re-arming an existing
// (kind, subject) MOVES its deadline instead of adding a second timer — that is
// what the partial unique index enforces, and it is the difference between
// "check again later" and an accumulating pile of duplicate wakeups.
func (s *Store) ArmSaga(ctx context.Context, id, kind, subject string, deadline time.Time, data any) error {
	body, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO sagas (id, kind, subject, deadline_at, data)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (kind, subject) WHERE state = 'pending'
		DO UPDATE SET deadline_at = EXCLUDED.deadline_at,
		              data        = EXCLUDED.data,
		              updated_at  = now()`,
		id, kind, subject, deadline, body)
	return err
}

// CancelSaga disarms a timeout whose reason has gone away.
func (s *Store) CancelSaga(ctx context.Context, kind, subject string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE sagas SET state = 'cancelled', updated_at = now()
		  WHERE kind = $1 AND subject = $2 AND state = 'pending'`, kind, subject)
	return err
}

// EnsureSchedule registers a recurring job without disturbing an operator who
// has disabled it or changed its interval. New jobs appear; existing rows are
// left exactly as they are.
func (s *Store) EnsureSchedule(ctx context.Context, name string, every time.Duration) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO schedules (name, interval_secs) VALUES ($1, $2)
		ON CONFLICT (name) DO NOTHING`, name, int(every.Seconds()))
	return err
}

// OverdueSchedules powers the alert that catches the failure mode nobody sees:
// a clock that has silently stopped while everything else looks healthy.
func (s *Store) OverdueSchedules(ctx context.Context, factor int) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT name FROM schedules
		 WHERE enabled
		   AND next_run_at < now() - make_interval(secs => interval_secs * $1)`, factor)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
