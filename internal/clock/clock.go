// Package clock is acquire's only source of recurring and delayed work.
//
// Until this package existed, acquire had none: main() started a one-shot
// reconcile, a Kafka consumer and an HTTP server, and the single ticker in the
// tree was a 20 s SSE heartbeat. Everything the incumbent automation does
// unattended — hunt the backlog, retry a failure, notice an episode has aired —
// needs a due time to fire from.
//
// The design is deliberately small: ONE leader-elected goroutine that does two
// statements (advance due schedules, claim expired sagas) and emits. It decides
// nothing. Emitting rather than executing is what keeps the clock from becoming
// a second copy of the domain — and it is enforced: this package must not
// import internal/app, and a test asserts that.
package clock

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/laedeli/acquire/internal/store"
)

// Emitter receives due work. Implementations enqueue to the outbox inside the
// clock's own transaction, so "this fired" and "we told someone" commit
// together.
type Emitter interface {
	ScheduleDue(ctx context.Context, tx pgx.Tx, name string) error
	SagaDue(ctx context.Context, tx pgx.Tx, s store.Saga) error
}

type Clock struct {
	st   *store.Store
	em   Emitter
	tick time.Duration
}

func New(st *store.Store, em Emitter, tick time.Duration) *Clock {
	if tick <= 0 {
		tick = 10 * time.Second
	}
	return &Clock{st: st, em: em, tick: tick}
}

// Run ticks until ctx is cancelled. Every replica runs this; the advisory lock
// decides which one actually does the work on any given tick.
//
// Leadership is per-tick rather than long-lived on purpose. There is no lease to
// expire, no split brain to reason about, and a pod that dies mid-tick simply
// loses the transaction — the next tick on any replica redoes it.
func (c *Clock) Run(ctx context.Context) {
	t := time.NewTicker(c.tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.tickOnce(ctx)
		}
	}
}

func (c *Clock) tickOnce(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	led, err := c.st.WithLeaderLock(ctx, "acquire.clock", func(ctx context.Context, tx pgx.Tx) error {
		due, err := store.ClaimDueSchedules(ctx, tx)
		if err != nil {
			return err
		}
		for _, name := range due {
			if err := c.em.ScheduleDue(ctx, tx, name); err != nil {
				return err
			}
		}
		sagas, err := store.ClaimDueSagas(ctx, tx)
		if err != nil {
			return err
		}
		for _, s := range sagas {
			if err := c.em.SagaDue(ctx, tx, s); err != nil {
				return err
			}
		}
		if len(due) > 0 || len(sagas) > 0 {
			log.Printf("acquire clock: %d schedule(s), %d saga(s) due", len(due), len(sagas))
		}
		return nil
	})
	// Not being the leader is the normal case on every replica but one, and on
	// every deploy where two pods overlap. It is not an error and must not be
	// logged as one, or the log becomes useless exactly when it matters.
	if err != nil && led {
		log.Printf("acquire clock: tick failed: %v", err)
	}
}
