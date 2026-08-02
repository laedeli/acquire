package app

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/laedeli/acquire/internal/events"
	"github.com/laedeli/acquire/internal/store"
)

// ClockEmitter turns a due schedule or an expired saga into an outbox row.
//
// It does NOT act on them. That separation is the point of the clock: the timer
// says "this is due", the outbox carries that fact durably, and a worker that
// consumes the topic decides what it means. Executing here would put acquisition
// logic inside a leader-elected goroutine, where it would be invisible to the
// event stream and impossible to replay.
//
// Enqueuing inside the clock's own transaction is what makes "it fired" and
// "we told someone" a single atomic fact — the clock advances next_run_at and
// writes the event together, so a crash between the two is not representable.
type ClockEmitter struct{ prefix string }

func NewClockEmitter(topicPrefix string) *ClockEmitter { return &ClockEmitter{prefix: topicPrefix} }

// ScheduleDue announces a recurring job whose time has come, e.g.
// zaentrum-beta.acquire.schedule.due with {"name":"backlog-sweep"}.
func (e *ClockEmitter) ScheduleDue(ctx context.Context, tx pgx.Tx, name string) error {
	return store.Enqueue(ctx, tx,
		events.Topic(e.prefix, events.DomainSchedule, "due"), name,
		map[string]any{"name": name})
}

// SagaDue announces an expired durable timeout. The subject is the message key
// so everything about one request lands on the same partition and keeps its
// order.
func (e *ClockEmitter) SagaDue(ctx context.Context, tx pgx.Tx, s store.Saga) error {
	return store.Enqueue(ctx, tx,
		events.Topic(e.prefix, events.DomainSchedule, "saga.due"), s.Subject,
		map[string]any{
			"id":       s.ID,
			"kind":     s.Kind,
			"subject":  s.Subject,
			"attempts": s.Attempts,
			"data":     s.Data,
		})
}
