// Package relay publishes outbox rows to Kafka.
//
// It runs on EVERY replica, not just the leader: claims use
// FOR UPDATE SKIP LOCKED, so two relays partition the backlog rather than
// racing. That keeps publish throughput independent of leader election, and
// means a leaderless moment during a deploy delays nothing.
package relay

import (
	"context"
	"log"
	"time"

	kafka "github.com/segmentio/kafka-go"

	"github.com/laedeli/acquire/internal/events"
	"github.com/laedeli/acquire/internal/store"
)

const (
	batchSize = 500
	// Published rows are an audit trail, not a queue.
	keepPublished = 48 * time.Hour
)

type Relay struct {
	st   *store.Store
	prod *events.Producer
	tick time.Duration
}

func New(st *store.Store, prod *events.Producer, tick time.Duration) *Relay {
	if tick <= 0 {
		tick = 2 * time.Second
	}
	return &Relay{st: st, prod: prod, tick: tick}
}

func (r *Relay) Run(ctx context.Context) {
	if r.prod == nil {
		log.Printf("acquire relay: no Kafka producer — events stay in the outbox")
		return
	}
	t := time.NewTicker(r.tick)
	defer t.Stop()
	sweep := time.NewTicker(time.Hour)
	defer sweep.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-sweep.C:
			if n, err := r.st.SweepOutbox(ctx, keepPublished); err != nil {
				log.Printf("acquire relay: sweep failed: %v", err)
			} else if n > 0 {
				log.Printf("acquire relay: swept %d delivered event(s)", n)
			}
		case <-t.C:
			// Drain: a burst should not wait a full tick per batch.
			for {
				n, err := r.drain(ctx)
				if err != nil {
					log.Printf("acquire relay: %v", err)
					break
				}
				if n < batchSize {
					break
				}
			}
		}
	}
}

func (r *Relay) drain(ctx context.Context) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	rows, tx, err := r.st.ClaimOutbox(ctx, batchSize)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if len(rows) == 0 {
		return 0, nil
	}

	msgs := make([]kafka.Message, 0, len(rows))
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		msgs = append(msgs, kafka.Message{
			Topic: row.Topic,
			Key:   []byte(row.Key),
			Value: row.Payload,
		})
		ids = append(ids, row.ID)
	}

	if err := r.prod.Publish(ctx, msgs); err != nil {
		// Leave them pending. A broker outage must delay events, never lose
		// them — so the failure is recorded on the rows and committed, and the
		// same rows are re-claimed next tick.
		if mErr := store.MarkFailed(ctx, tx, ids, err.Error()); mErr != nil {
			return 0, mErr
		}
		if cErr := tx.Commit(ctx); cErr != nil {
			return 0, cErr
		}
		return 0, err
	}

	if err := store.MarkPublished(ctx, tx, ids); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		// Published but not marked: the batch will be re-sent next tick.
		// At-least-once is the contract; consumers are idempotent.
		return 0, err
	}
	return len(rows), nil
}
