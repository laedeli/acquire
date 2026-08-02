package store

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func migrated(t *testing.T) *Store {
	t.Helper()
	pool := testPool(t)
	if err := migrate(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return &Store{pool: pool}
}

// The whole point of the outbox: the fact and the announcement commit together.
// If the transaction rolls back, no event escapes.
func TestOutboxIsTransactional(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := Enqueue(ctx, tx, "t.acquire.request.grabbed", "k1", map[string]any{"a": 1}); err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback(ctx)

	if n, _ := s.OutboxDepth(ctx); n != 0 {
		t.Fatalf("a rolled-back transaction leaked %d event(s)", n)
	}

	tx2, _ := s.pool.Begin(ctx)
	if err := Enqueue(ctx, tx2, "t.acquire.request.grabbed", "k1", map[string]any{"a": 1}); err != nil {
		t.Fatal(err)
	}
	if err := tx2.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.OutboxDepth(ctx); n != 1 {
		t.Fatalf("committed event not durable: depth %d", n)
	}
}

// SKIP LOCKED is what lets the relay run on every replica instead of only the
// leader. Two concurrent claims must partition the backlog, never overlap.
func TestOutboxClaimsDoNotOverlap(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		tx, _ := s.pool.Begin(ctx)
		_ = Enqueue(ctx, tx, "t.acquire.request.grabbed", "k", map[string]any{"i": i})
		_ = tx.Commit(ctx)
	}

	a, txa, err := s.ClaimOutbox(ctx, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = txa.Rollback(ctx) }()
	b, txb, err := s.ClaimOutbox(ctx, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = txb.Rollback(ctx) }()

	if len(a) != 4 || len(b) != 4 {
		t.Fatalf("claims returned %d and %d, want 4 and 4", len(a), len(b))
	}
	seen := map[int64]bool{}
	for _, r := range append(a, b...) {
		if seen[r.ID] {
			t.Fatalf("event %d was claimed twice — SKIP LOCKED is not doing its job", r.ID)
		}
		seen[r.ID] = true
	}
}

// A broker outage must delay events, never lose them.
func TestOutboxFailureKeepsEventsPending(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	tx, _ := s.pool.Begin(ctx)
	_ = Enqueue(ctx, tx, "t.acquire.request.grabbed", "k", map[string]any{})
	_ = tx.Commit(ctx)

	rows, ctx2tx, _ := s.ClaimOutbox(ctx, 10)
	ids := []int64{rows[0].ID}
	if err := MarkFailed(ctx, ctx2tx, ids, "broker unreachable"); err != nil {
		t.Fatal(err)
	}
	_ = ctx2tx.Commit(ctx)

	if n, _ := s.OutboxDepth(ctx); n != 1 {
		t.Fatalf("a failed publish dropped the event: depth %d", n)
	}
	again, tx3, _ := s.ClaimOutbox(ctx, 10)
	defer func() { _ = tx3.Rollback(ctx) }()
	if len(again) != 1 || again[0].Attempts != 1 {
		t.Fatalf("re-claim = %+v, want the same row with attempts=1", again)
	}
}

// Two replicas overlap on every deploy (RollingUpdate + Always + rollout
// restart). Exactly one may hold the clock.
func TestLeaderLockAdmitsOnlyOne(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()

	var mu sync.Mutex
	var concurrent, maxConcurrent, wins int
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			led, err := s.WithLeaderLock(ctx, "test.clock", func(ctx context.Context, tx pgx.Tx) error {
				mu.Lock()
				concurrent++
				if concurrent > maxConcurrent {
					maxConcurrent = concurrent
				}
				mu.Unlock()
				time.Sleep(40 * time.Millisecond)
				mu.Lock()
				concurrent--
				mu.Unlock()
				return nil
			})
			if err != nil {
				t.Errorf("lock: %v", err)
			}
			if led {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if maxConcurrent > 1 {
		t.Errorf("%d holders at once — the advisory lock is not exclusive", maxConcurrent)
	}
	if wins == 0 {
		t.Error("nobody ever won the lock, so no clock would ever tick")
	}
}

// A schedule must fire once per interval, not once per replica per tick.
func TestScheduleFiresOnceAndAdvances(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	if err := s.EnsureSchedule(ctx, "backlog-sweep", time.Hour); err != nil {
		t.Fatal(err)
	}
	// EnsureSchedule must not disturb an operator's edits on a re-run.
	if _, err := s.pool.Exec(ctx,
		`UPDATE schedules SET enabled=false WHERE name='backlog-sweep'`); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureSchedule(ctx, "backlog-sweep", time.Hour); err != nil {
		t.Fatal(err)
	}
	var enabled bool
	_ = s.pool.QueryRow(ctx, `SELECT enabled FROM schedules WHERE name='backlog-sweep'`).Scan(&enabled)
	if enabled {
		t.Error("EnsureSchedule re-enabled a schedule the operator disabled")
	}
	_, _ = s.pool.Exec(ctx, `UPDATE schedules SET enabled=true, next_run_at=now()-interval '1 minute' WHERE name='backlog-sweep'`)

	tx, _ := s.pool.Begin(ctx)
	due, err := ClaimDueSchedules(ctx, tx)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0] != "backlog-sweep" {
		t.Fatalf("due = %v, want [backlog-sweep]", due)
	}
	// Same transaction, immediately again: it must not still be due.
	again, _ := ClaimDueSchedules(ctx, tx)
	if len(again) != 0 {
		t.Errorf("schedule fired twice in one tick: %v", again)
	}
	_ = tx.Commit(ctx)
}

// Re-arming must MOVE the deadline, not stack a second timer for the same thing.
func TestArmSagaIsIdempotentPerSubject(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	deadline := time.Now().Add(time.Hour)
	if err := s.ArmSaga(ctx, "s1", "retry", "wanted:42", deadline, map[string]any{"n": 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.ArmSaga(ctx, "s2", "retry", "wanted:42", deadline.Add(time.Hour), map[string]any{"n": 2}); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM sagas WHERE state='pending' AND subject='wanted:42'`).Scan(&n)
	if n != 1 {
		t.Fatalf("%d live sagas for one subject, want 1", n)
	}

	_, _ = s.pool.Exec(ctx, `UPDATE sagas SET deadline_at = now() - interval '1 second' WHERE subject='wanted:42'`)
	tx, _ := s.pool.Begin(ctx)
	fired, err := ClaimDueSagas(ctx, tx)
	if err != nil {
		t.Fatal(err)
	}
	if len(fired) != 1 || fired[0].Attempts != 1 {
		t.Fatalf("fired = %+v", fired)
	}
	again, _ := ClaimDueSagas(ctx, tx)
	if len(again) != 0 {
		t.Errorf("saga fired twice in one tick: %+v", again)
	}
	_ = tx.Commit(ctx)
}

// History must outlive its subject. grabs CASCADEs with the request, which is
// why beta holds zero grab rows despite five real grab events — deleting the
// request erased the evidence it was ever acted on.
func TestHistorySurvivesSubjectDeletion(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO history (kind, subject, title, indexer, protocol, size_mb, score, reason)
		VALUES ('grabbed', 'wanted:gone', 'Some.Release.1080p', 'anIndexer', 'usenet', 7000, 1497, 'NZB preferred')`); err != nil {
		t.Fatal(err)
	}
	// There is no wanted_items row at all; history does not care, by design.
	var n int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM history WHERE subject='wanted:gone'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("history rows = %d, want 1 with no parent row present", n)
	}
	// And it must have no FK that could ever cascade it away.
	var fks int
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM information_schema.table_constraints
		 WHERE table_name = 'history' AND constraint_type = 'FOREIGN KEY'`).Scan(&fks); err != nil {
		t.Fatal(err)
	}
	if fks != 0 {
		t.Errorf("history has %d foreign key(s) — it will be cascaded away like grabs was", fks)
	}
}

// The alert that catches a clock which has silently stopped.
func TestOverdueSchedulesDetectsAStoppedClock(t *testing.T) {
	s := migrated(t)
	ctx := context.Background()
	_ = s.EnsureSchedule(ctx, "healthy", time.Hour)
	_ = s.EnsureSchedule(ctx, "stalled", time.Hour)
	_, _ = s.pool.Exec(ctx, `UPDATE schedules SET next_run_at = now() + interval '30 minutes' WHERE name='healthy'`)
	_, _ = s.pool.Exec(ctx, `UPDATE schedules SET next_run_at = now() - interval '10 hours' WHERE name='stalled'`)

	over, err := s.OverdueSchedules(ctx, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(over) != 1 || over[0] != "stalled" {
		t.Errorf("overdue = %v, want [stalled]", over)
	}
}
