package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// OutboxRow is one undelivered event.
type OutboxRow struct {
	ID       int64
	Topic    string
	Key      string
	Payload  []byte
	Attempts int
}

// Enqueue writes an event inside the CALLER'S transaction, so the fact and the
// announcement of the fact commit together or not at all. Emitting to a broker
// directly from a handler cannot offer that: the publish can succeed and the
// transaction then roll back, or the row can commit and the publish fail, and
// in both cases the database and the stream disagree with nobody noticing.
func Enqueue(ctx context.Context, tx pgx.Tx, topic, key string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("outbox marshal %s: %w", topic, err)
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO outbox (topic, key, payload) VALUES ($1, $2, $3)`,
		topic, key, body)
	if err != nil {
		return fmt.Errorf("outbox insert %s: %w", topic, err)
	}
	return nil
}

// ClaimOutbox takes up to limit undelivered events for this replica.
//
// FOR UPDATE SKIP LOCKED is what makes the relay safe to run on every pod
// rather than only the leader: two relays claim disjoint sets instead of
// blocking on each other or double-publishing. Delivery is at-least-once, so
// consumers must be idempotent.
func (s *Store) ClaimOutbox(ctx context.Context, limit int) ([]OutboxRow, pgx.Tx, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	rows, err := tx.Query(ctx, `
		SELECT id, topic, key, payload, attempts
		  FROM outbox
		 WHERE published_at IS NULL
		 ORDER BY id
		 LIMIT $1
		   FOR UPDATE SKIP LOCKED`, limit)
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, nil, err
	}
	defer rows.Close()

	var out []OutboxRow
	for rows.Next() {
		var r OutboxRow
		if err := rows.Scan(&r.ID, &r.Topic, &r.Key, &r.Payload, &r.Attempts); err != nil {
			_ = tx.Rollback(ctx)
			return nil, nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		_ = tx.Rollback(ctx)
		return nil, nil, err
	}
	return out, tx, nil
}

// MarkPublished is called inside the claiming transaction.
func MarkPublished(ctx context.Context, tx pgx.Tx, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx,
		`UPDATE outbox SET published_at = now() WHERE id = ANY($1)`, ids)
	return err
}

// MarkFailed records why an event could not be delivered and leaves it pending.
// It deliberately does NOT drop the row: a broker outage should delay events,
// not lose them.
func MarkFailed(ctx context.Context, tx pgx.Tx, ids []int64, cause string) error {
	if len(ids) == 0 {
		return nil
	}
	if len(cause) > 500 {
		cause = cause[:500]
	}
	_, err := tx.Exec(ctx,
		`UPDATE outbox SET attempts = attempts + 1, last_error = $2 WHERE id = ANY($1)`,
		ids, cause)
	return err
}

// SweepOutbox deletes delivered events older than keep. Published rows are an
// audit trail, not a queue; without this the table only grows.
func (s *Store) SweepOutbox(ctx context.Context, keep time.Duration) (int64, error) {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM outbox WHERE published_at IS NOT NULL AND published_at < now() - $1::interval`,
		keep.String())
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// OutboxDepth is the pending backlog, for /metrics. A backlog that climbs means
// the relay is failing while writes keep succeeding — the failure mode that is
// otherwise invisible.
func (s *Store) OutboxDepth(ctx context.Context) (int64, error) {
	var n int64
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM outbox WHERE published_at IS NULL`).Scan(&n)
	return n, err
}

// WithLeaderLock runs fn only if this replica wins a Postgres advisory lock for
// name, and releases it when the transaction ends.
//
// It is transaction-scoped ON PURPOSE. A session-scoped pg_try_advisory_lock
// cannot be used correctly over a pgxpool: the lock belongs to a connection the
// pool may hand to somebody else or recycle, so a `TryLock() (bool, error)` API
// over a pool is not implementable — it would report leadership it does not
// hold. Scoping the lock to a transaction makes its lifetime explicit.
//
// This matters in steady state, not only in exotic failures: RollingUpdate plus
// imagePullPolicy Always plus CI's rollout restart means two acquire pods
// overlap on every single deploy.
func (s *Store) WithLeaderLock(ctx context.Context, name string, fn func(context.Context, pgx.Tx) error) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var got bool
	// hashtext gives a stable per-name key without a registry of magic numbers.
	if err := tx.QueryRow(ctx,
		`SELECT pg_try_advisory_xact_lock(hashtext($1))`, name).Scan(&got); err != nil {
		return false, err
	}
	if !got {
		return false, nil
	}
	if err := fn(ctx, tx); err != nil {
		return true, err
	}
	return true, tx.Commit(ctx)
}
