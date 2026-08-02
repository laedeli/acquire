-- The WANT model.
--
-- katalog owns HAVE: every row it holds corresponds to a file that exists.
-- acquire owns WANT: every row here is an intent, satisfied or not. The seam is
-- (tmdb_id, season_number, episode_number) going in and katalog's UUID coming
-- back out; katalog's UUID is never an input.
--
-- Why not put fileless "expected" episodes in katalog instead: hasPrimaryAsset
-- gates them out of the pipeline, ItemIsPackaged(series) becomes permanently
-- false for any show with one unaired episode, packageItem's fan-out counts
-- phantoms, and chino would render unplayable rows on four clients.
--
-- NO FOREIGN KEYS anywhere below. ADD CONSTRAINT has no IF NOT EXISTS, and
-- these files re-run on every boot — 003_downloads_soft_ref.sql set the
-- precedent when it DROPPED a FK for exactly this reason. Links are soft text
-- columns. CREATE INDEX CONCURRENTLY is also impossible here: pgx sends each
-- file as one implicit transaction.

-- titles: one row per movie or series we care about. TMDB is the identity
-- space; tvdb_id/imdb_id are carried because NO indexer accepts a tmdbId —
-- 0 of 70 advertise it — so every typed search has to translate.
CREATE TABLE IF NOT EXISTS titles (
  id              text PRIMARY KEY,
  tmdb_id         bigint      NOT NULL,
  kind            text        NOT NULL,              -- movie | series
  title           text        NOT NULL DEFAULT '',
  sort_title      text        NOT NULL DEFAULT '',
  year            int,
  tvdb_id         bigint,                            -- for tvsearch
  imdb_id         text        NOT NULL DEFAULT '',   -- for movie search
  status          text        NOT NULL DEFAULT '',   -- continuing | ended | released | …
  series_type     text        NOT NULL DEFAULT 'standard', -- standard | daily | anime
  monitored       boolean     NOT NULL DEFAULT true,
  monitor_new     boolean     NOT NULL DEFAULT true, -- auto-monitor new seasons
  profile_id      text        NOT NULL DEFAULT '',   -- '' = the default profile
  air_grace_hours int         NOT NULL DEFAULT 0,    -- don't search the instant it airs
  numbering_suspect boolean   NOT NULL DEFAULT false,-- TMDB/scene numbering disagree
  added_at        timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS titles_tmdb_idx ON titles (tmdb_id, kind);
CREATE INDEX IF NOT EXISTS titles_monitored_idx ON titles (kind) WHERE monitored;

-- title_aliases: releases are named after whatever the scene calls a show, not
-- what TMDB calls it. Production matched 119 of 500 grabs on an ALIAS rather
-- than the title, so identity verification without these rejects real releases.
CREATE TABLE IF NOT EXISTS title_aliases (
  title_id text NOT NULL,
  alias    text NOT NULL,
  source   text NOT NULL DEFAULT 'tmdb',   -- tmdb | xem | manual
  PRIMARY KEY (title_id, alias)
);

CREATE TABLE IF NOT EXISTS seasons (
  title_id      text NOT NULL,
  season_number int  NOT NULL,
  monitored     boolean NOT NULL DEFAULT true,
  episode_count int  NOT NULL DEFAULT 0,
  air_date      date,
  PRIMARY KEY (title_id, season_number)
);

-- acquisition_targets: ONE polymorphic table for movies and episodes.
--
-- This is the choice that lets one binary replace both incumbent services.
-- Separate movies/episodes tables would mean two sweepers, two state machines
-- and two console views — and no way to express "the file I hold is good
-- enough to stop looking", because that question is identical for both.
--
-- held_* is a PROJECTION of katalog, exactly as `downloads` projects the
-- clients. Without a materialised held_score, "is this good enough?" is an N+1
-- across a service boundary and the upgrade story is unrepresentable. It also
-- has to be materialised rather than computed on read for the 16,169 legacy
-- episode files, which have no grab row to derive a score from.
CREATE TABLE IF NOT EXISTS acquisition_targets (
  id                 text PRIMARY KEY,
  title_id           text NOT NULL,
  kind               text NOT NULL,                 -- movie | episode
  season_number      int,
  episode_number     int,                           -- both NULL for a movie
  absolute_number    int,                           -- anime
  scene_season       int,
  scene_episode      int,
  scene_absolute     int,
  air_date           date,
  air_window_opens_at timestamptz,                  -- air_date + titles.air_grace_hours
  episode_type       text NOT NULL DEFAULT '',      -- standard | special | finale …
  monitored          boolean NOT NULL DEFAULT true,

  -- the HAVE side, projected
  held_item_id       text NOT NULL DEFAULT '',      -- katalog UUID, soft ref
  held_quality       jsonb NOT NULL DEFAULT '{}'::jsonb,
  held_release       text NOT NULL DEFAULT '',
  held_score         int  NOT NULL DEFAULT 0,
  -- held_score is a CACHE of Score(held_quality, profile). The profile it was
  -- computed under is stored so SaveProfile can invalidate it; without that the
  -- cutoff backlog drifts silently and nobody finds out.
  held_score_profile text NOT NULL DEFAULT '',
  held_source        text NOT NULL DEFAULT '',      -- grab | derived (legacy files)
  held_scored_at     timestamptz,
  held_at            timestamptz,

  state              text NOT NULL DEFAULT 'wanted', -- wanted | searching | grabbed | held | unaired
  last_search_at     timestamptz,
  search_backoff_until timestamptz,
  search_failures    int NOT NULL DEFAULT 0,
  updated_at         timestamptz NOT NULL DEFAULT now()
);

-- One target per coordinate. NULLS NOT DISTINCT (PG >= 15; acid-cluster is 17)
-- makes the movie case — both season and episode NULL — collide correctly
-- instead of allowing unlimited duplicates.
CREATE UNIQUE INDEX IF NOT EXISTS targets_coord_idx
  ON acquisition_targets (title_id, season_number, episode_number) NULLS NOT DISTINCT;

-- The backlog sweep: what is monitored, wanted, and past its air window.
CREATE INDEX IF NOT EXISTS targets_missing_idx
  ON acquisition_targets (air_window_opens_at)
  WHERE state = 'wanted' AND monitored;

-- The upgrade sweep: one index scan instead of asking katalog per row.
CREATE INDEX IF NOT EXISTS targets_cutoff_idx
  ON acquisition_targets (title_id, held_score)
  WHERE state = 'held' AND monitored;

-- Retry scheduling.
CREATE INDEX IF NOT EXISTS targets_backoff_idx
  ON acquisition_targets (search_backoff_until)
  WHERE state = 'wanted' AND search_backoff_until IS NOT NULL;

-- grab_targets: ONE grab can satisfy N targets. This is what a season pack is,
-- and it is why the current code loses 9 of 10 episodes — it resolves a grab to
-- the single largest video file and marks the request fulfilled.
CREATE TABLE IF NOT EXISTS grab_targets (
  adapter       text NOT NULL,
  client_job_id text NOT NULL,
  target_id     text NOT NULL,
  state         text NOT NULL DEFAULT 'expected', -- expected|matched|imported|missing|failed
  file_path     text NOT NULL DEFAULT '',
  episode_end   int,                              -- S01E01E02 -> 2; NULL when single
  item_id       text NOT NULL DEFAULT '',
  updated_at    timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (adapter, client_job_id, target_id)
);
CREATE INDEX IF NOT EXISTS grab_targets_target_idx ON grab_targets (target_id);
