-- Imports: finished downloads acquire copies into the library's arrivals.
--
-- The download clients the chart runs work on a volume of their own, not on
-- the media storage. When one of their downloads finishes, acquire copies the
-- files the catalog takes into the arrivals and hands the copy over (POST
-- /api/ingest), one import at a time; then it releases the download in its
-- client: an NZB right after the verified copy, a torrent once it has seeded to
-- its goal. A row is that work and survives a restart:
--   pending  — the copy or the hand-over is still to do; next_at is when it is
--              tried (again);
--   imported — the catalog has the copy; the client still holds the download
--              until it is released (next_at: when to look again);
--   done     — nothing more to do: released, or a client acquire releases
--              nothing in;
--   failed   — the catalog refused the hand-over.
-- files are the paths the client reported, as it sees them.
CREATE TABLE IF NOT EXISTS imports (
  adapter       text        NOT NULL,
  client_job_id text        NOT NULL,
  wanted_id     text        NOT NULL,
  files         text[]      NOT NULL DEFAULT '{}',
  state         text        NOT NULL DEFAULT 'pending'
                            CHECK (state IN ('pending', 'imported', 'done', 'failed')),
  attempts      int         NOT NULL DEFAULT 0,
  detail        text        NOT NULL DEFAULT '',
  arrival       text        NOT NULL DEFAULT '',   -- the copy handed to the catalog
  next_at       timestamptz NOT NULL DEFAULT now(),
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (adapter, client_job_id)
);

CREATE INDEX IF NOT EXISTS imports_due_idx ON imports (next_at) WHERE state IN ('pending', 'imported');
