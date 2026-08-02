-- The clock's two tables.
--
-- acquire has no recurring work at all today: main() starts a one-shot
-- reconcile, a Kafka consumer and an HTTP server, and the only ticker in the
-- tree is a 20 s SSE heartbeat. Everything the incumbent does unattended needs
-- a due-time to fire from.

-- schedules: recurring work. One row per job, advanced by the leader.
CREATE TABLE IF NOT EXISTS schedules (
  name          text PRIMARY KEY,
  interval_secs int         NOT NULL CHECK (interval_secs > 0),
  enabled       boolean     NOT NULL DEFAULT true,
  next_run_at   timestamptz NOT NULL DEFAULT now(),
  last_run_at   timestamptz,
  last_error    text        NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS schedules_due_idx
  ON schedules (next_run_at) WHERE enabled;

-- sagas: the durable-timeout primitive. Kafka has no delayed delivery, so
-- "check again in 20 minutes", "this search has taken too long", "this episode
-- airs on Thursday" and "did the ingest we asked for actually land?" all have
-- to be rows with a deadline that survives a restart.
CREATE TABLE IF NOT EXISTS sagas (
  id          text PRIMARY KEY,
  kind        text        NOT NULL,
  subject     text        NOT NULL,
  deadline_at timestamptz NOT NULL,
  state       text        NOT NULL DEFAULT 'pending',
  attempts    int         NOT NULL DEFAULT 0,
  data        jsonb       NOT NULL DEFAULT '{}'::jsonb,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS sagas_due_idx
  ON sagas (deadline_at) WHERE state = 'pending';

-- One live saga per (kind, subject): re-arming must move the deadline, never
-- accumulate a second timer for the same thing.
CREATE UNIQUE INDEX IF NOT EXISTS sagas_live_idx
  ON sagas (kind, subject) WHERE state = 'pending';
