-- Live download state + what was actually grabbed.
--
-- Until now the only trace of a download was a sentence in wanted_items.detail
-- ("grabbed NZB from NZBgeek via nzbget"), and progress was not recorded at all
-- even though the gateway emits it every few seconds. These two changes make
-- the console possible: `downloads` is the live/most-recent telemetry per client
-- job, and the new `grabs` columns remember which release won and why.

CREATE TABLE IF NOT EXISTS downloads (
  adapter        text NOT NULL,                 -- qbittorrent | nzbget | …
  client_job_id  text NOT NULL,                 -- the client's own id/tag
  wanted_id      text REFERENCES wanted_items(id) ON DELETE SET NULL,
  title          text NOT NULL DEFAULT '',
  state          text NOT NULL DEFAULT 'queued',-- queued|downloading|completed|failed
  native_state   text NOT NULL DEFAULT '',      -- the client's own word for it
  progress_pct   double precision NOT NULL DEFAULT 0,
  bytes_done     bigint NOT NULL DEFAULT 0,
  bytes_total    bigint NOT NULL DEFAULT 0,     -- 0 = unknown
  speed_bps      bigint NOT NULL DEFAULT 0,     -- 0 = idle (not unknown)
  eta_sec        int,                           -- NULL = unknown
  seeders        int,
  leechers       int,
  health         int,                           -- usenet article health 0-1000
  error          text NOT NULL DEFAULT '',
  started_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now(),
  finished_at    timestamptz,
  PRIMARY KEY (adapter, client_job_id)
);

CREATE INDEX IF NOT EXISTS downloads_wanted_idx ON downloads(wanted_id);
CREATE INDEX IF NOT EXISTS downloads_state_idx  ON downloads(state);
CREATE INDEX IF NOT EXISTS downloads_updated_idx ON downloads(updated_at DESC);

-- Remember the chosen release, not just its URL, so the console can show
-- "NZB · NZBgeek · 24.3 GB · 1080p x265" and why it beat the alternatives.
ALTER TABLE grabs ADD COLUMN IF NOT EXISTS release_title text NOT NULL DEFAULT '';
ALTER TABLE grabs ADD COLUMN IF NOT EXISTS indexer       text NOT NULL DEFAULT '';
ALTER TABLE grabs ADD COLUMN IF NOT EXISTS protocol      text NOT NULL DEFAULT '';
ALTER TABLE grabs ADD COLUMN IF NOT EXISTS size_bytes    bigint NOT NULL DEFAULT 0;
ALTER TABLE grabs ADD COLUMN IF NOT EXISTS seeders       int;
ALTER TABLE grabs ADD COLUMN IF NOT EXISTS reason        text NOT NULL DEFAULT '';
