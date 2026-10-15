-- Grabs bound to targets, what the library holds, and every search's outcome.
--
-- Until now no target could be grabbed: a grab belonged to a request
-- (wanted_items), and a series request's download was failed on purpose
-- because nothing said which episodes it held. A target grab is one release
-- handed to a download client for the targets it covers — one episode, a file
-- of several, or a season pack — and each of those targets walks
--   wanted -> grabbed -> downloaded -> held, or failed (or waiting, while the
--   catalog does not take episodes by identity yet).
-- No foreign keys, as everywhere in the WANT model (010): these files re-run on
-- every boot, and links are soft text columns.

-- titles: whether acquire knows what the library holds of a title. NULL is
-- "holdings unknown": never reconciled, or the catalog does not answer holdings
-- by identity. Such a title's targets are not missing and are never grabbed
-- automatically, because nothing says they are not on disk.
ALTER TABLE titles ADD COLUMN IF NOT EXISTS holdings_at    timestamptz;
ALTER TABLE titles ADD COLUMN IF NOT EXISTS holdings_error text NOT NULL DEFAULT '';
-- The catalog's own id for the series or movie, as its holdings answer it. A
-- soft copy for the console; acquire never sends it anywhere.
ALTER TABLE titles ADD COLUMN IF NOT EXISTS catalog_id     text NOT NULL DEFAULT '';

-- acquisition_targets: the grab, its reasons, and what each search found.
ALTER TABLE acquisition_targets ADD COLUMN IF NOT EXISTS detail         text NOT NULL DEFAULT '';
ALTER TABLE acquisition_targets ADD COLUMN IF NOT EXISTS grab_id        text NOT NULL DEFAULT '';
-- held_via is the item that holds the file when this episode is covered by
-- another's (S04E08 in a file of S04E07-E08); '' when it holds its own.
ALTER TABLE acquisition_targets ADD COLUMN IF NOT EXISTS held_via       text NOT NULL DEFAULT '';
ALTER TABLE acquisition_targets ADD COLUMN IF NOT EXISTS held_playable  boolean NOT NULL DEFAULT false;
-- The last search's outcome: found | none | error. With search_backoff_until
-- as the next due time, every searched target moves to the back of the sweep,
-- so the backlog rotates instead of returning the same first few for ever.
ALTER TABLE acquisition_targets ADD COLUMN IF NOT EXISTS search_outcome text NOT NULL DEFAULT '';
ALTER TABLE acquisition_targets ADD COLUMN IF NOT EXISTS search_detail  text NOT NULL DEFAULT '';
-- A grab an admin asked for from the console (per episode or in bulk), done
-- by a worker one at a time: requested, and claimed until.
ALTER TABLE acquisition_targets ADD COLUMN IF NOT EXISTS grab_requested_at  timestamptz;
ALTER TABLE acquisition_targets ADD COLUMN IF NOT EXISTS grab_claimed_until timestamptz;

CREATE INDEX IF NOT EXISTS targets_held_item_idx ON acquisition_targets (held_item_id) WHERE held_item_id <> '';
CREATE INDEX IF NOT EXISTS targets_held_via_idx  ON acquisition_targets (held_via) WHERE held_via <> '';
CREATE INDEX IF NOT EXISTS targets_grab_idx      ON acquisition_targets (grab_id) WHERE grab_id <> '';
CREATE INDEX IF NOT EXISTS targets_grab_requested_idx ON acquisition_targets (grab_requested_at)
  WHERE grab_requested_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS titles_tvdb_idx ON titles (tvdb_id) WHERE tvdb_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS titles_imdb_idx ON titles (imdb_id) WHERE imdb_id <> '';

-- target_grabs: one release handed to a download client for one or more
-- targets. The id (tg_…) goes to the gateway as the wanted item id, so the
-- download's events come back to it; (adapter, client_job_id) finds it when a
-- restarted gateway no longer knows that id. A row is written before the
-- hand-over (pending, holding its targets against a second grab) and bound to
-- its job after it.
CREATE TABLE IF NOT EXISTS target_grabs (
  id            text        PRIMARY KEY,
  title_id      text        NOT NULL,
  target_ids    text[]      NOT NULL DEFAULT '{}',
  adapter       text        NOT NULL DEFAULT '',
  client_job_id text        NOT NULL DEFAULT '',
  state         text        NOT NULL DEFAULT 'pending'
                            CHECK (state IN ('pending', 'grabbed', 'downloaded', 'imported', 'failed')),
  detail        text        NOT NULL DEFAULT '',
  via           text        NOT NULL DEFAULT '',   -- sweep | missing | pick | search
  release_title text        NOT NULL DEFAULT '',
  covers        text        NOT NULL DEFAULT '',   -- S04E07, S04E07-E08, S01 …
  indexer       text        NOT NULL DEFAULT '',
  indexer_id    bigint,
  release_guid  text        NOT NULL DEFAULT '',
  protocol      text        NOT NULL DEFAULT '',
  size_bytes    bigint      NOT NULL DEFAULT 0,
  seeders       int,
  reason        text        NOT NULL DEFAULT '',
  -- the link with its credentials removed; the full one only ever sealed
  source        text        NOT NULL DEFAULT '',
  source_ct     bytea,
  source_kid    text        NOT NULL DEFAULT '',
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS target_grabs_job_idx   ON target_grabs (client_job_id, adapter);
CREATE INDEX IF NOT EXISTS target_grabs_title_idx ON target_grabs (title_id, created_at DESC);

-- grab_targets (010) records each target's file of a finished grab: the file
-- handed over for it, the episode run that file covers, and the item the
-- catalog answered. grab_id ties it to its target_grabs row.
ALTER TABLE grab_targets ADD COLUMN IF NOT EXISTS grab_id text NOT NULL DEFAULT '';
ALTER TABLE grab_targets ADD COLUMN IF NOT EXISTS detail  text NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS grab_targets_grab_idx ON grab_targets (grab_id);
