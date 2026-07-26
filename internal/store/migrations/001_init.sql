-- acquire's own state (DB acquire_beta). Applied on boot; idempotent.
-- WantedItems + grabs live HERE, not in the neutral catalog — the catalog only
-- ever learns about a title once acquire creates the item on download-complete.

CREATE TABLE IF NOT EXISTS wanted_items (
  id           text PRIMARY KEY,               -- ulid/uuid string
  tmdb_id      bigint,
  media_type   text NOT NULL DEFAULT 'movie',  -- movie|series
  title        text NOT NULL,
  year         int,
  poster_url   text NOT NULL DEFAULT '',
  requested_by text NOT NULL DEFAULT '',        -- keycloak sub
  requested_at timestamptz NOT NULL DEFAULT now(),
  status       text NOT NULL DEFAULT 'pending', -- pending|grabbed|downloading|packaging|fulfilled|failed
  detail       text NOT NULL DEFAULT '',        -- last status message
  item_id      text NOT NULL DEFAULT '',        -- catalog item id once created
  updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS wanted_status_idx ON wanted_items(status);
CREATE INDEX IF NOT EXISTS wanted_tmdb_idx   ON wanted_items(tmdb_id);

CREATE TABLE IF NOT EXISTS grabs (
  wanted_id     text NOT NULL REFERENCES wanted_items(id) ON DELETE CASCADE,
  adapter       text NOT NULL,
  client_job_id text NOT NULL DEFAULT '',
  source        text NOT NULL DEFAULT '',
  created_at    timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (wanted_id, adapter, client_job_id)
);
