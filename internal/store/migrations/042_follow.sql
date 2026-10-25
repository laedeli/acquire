-- Following what was requested, reading TMDB again on the clock, and the
-- sources' newest releases.
--
-- Until now acquire tracked only the titles imported from an earlier
-- automation service, their episodes derived from TMDB once, and a series
-- request was not tracked at all. A request for a series or a movie now makes
-- acquire FOLLOW the title: it is tracked as an imported one is, its episodes
-- (or the movie) are its targets, and the schedule inventory-refresh reads
-- TMDB again for the series still running, so episodes announced after the
-- first read become targets too. The schedule rss-sync reads each source's
-- newest releases and matches them to what is wanted.
-- No foreign keys, as everywhere in the WANT model (010): these files re-run
-- on every boot, and links are soft text columns.

-- titles: followed_at is when a request first made acquire follow the title
-- (NULL: tracked by the import alone); origin is 'request' for a title a
-- request created, '' for one imported or derived. tmdb_status is TMDB's own
-- word on a series or a movie (Returning Series, In Production, Planned,
-- Ended, Canceled, Released …), which decides how often a series is read
-- again; inventory_at is when TMDB was last read for the title (NULL: never),
-- inventory_error why the last read failed. origin_country are the
-- countries TMDB says the title comes from (ISO 3166-1): a release that names
-- a series with a country after it (Show.Name.US) is of the series from there.
ALTER TABLE titles ADD COLUMN IF NOT EXISTS followed_at     timestamptz;
ALTER TABLE titles ADD COLUMN IF NOT EXISTS origin          text NOT NULL DEFAULT '';
ALTER TABLE titles ADD COLUMN IF NOT EXISTS tmdb_status     text NOT NULL DEFAULT '';
ALTER TABLE titles ADD COLUMN IF NOT EXISTS inventory_at    timestamptz;
ALTER TABLE titles ADD COLUMN IF NOT EXISTS inventory_error text NOT NULL DEFAULT '';
ALTER TABLE titles ADD COLUMN IF NOT EXISTS origin_country  text[] NOT NULL DEFAULT '{}';

-- acquisition_targets: added_at is when a follow or a read of TMDB on the
-- clock made the target; NULL for every target made before, by the import or
-- a derivation — the old backlog. A target added after its title's holdings
-- were last learned is of unknown holdings until the next reconcile.
-- episode_title is TMDB's name of the episode. release_pending marks a
-- requested movie TMDB dates no release for yet: not searched, and not
-- missing, until it does. release_kind is what a requested movie's window
-- (air_window_opens_at) is: digital, physical or tv, its first release to
-- watch at home; theatrical when TMDB dates none of those and the window is
-- set from its theatrical release.
ALTER TABLE acquisition_targets ADD COLUMN IF NOT EXISTS added_at        timestamptz;
ALTER TABLE acquisition_targets ADD COLUMN IF NOT EXISTS episode_title   text NOT NULL DEFAULT '';
ALTER TABLE acquisition_targets ADD COLUMN IF NOT EXISTS release_pending boolean NOT NULL DEFAULT false;
ALTER TABLE acquisition_targets ADD COLUMN IF NOT EXISTS release_kind    text NOT NULL DEFAULT '';

-- wanted_items: the title a request follows ('' for one that follows none: a
-- request without a TMDB id, or one made before 042 and not asked again).
ALTER TABLE wanted_items ADD COLUMN IF NOT EXISTS title_id text NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS wanted_title_idx     ON wanted_items (title_id) WHERE title_id <> '';
CREATE INDEX IF NOT EXISTS titles_inventory_idx ON titles (inventory_at NULLS FIRST) WHERE monitored;
CREATE INDEX IF NOT EXISTS targets_added_idx    ON acquisition_targets (title_id, added_at) WHERE added_at IS NOT NULL;

-- rss_cursors: how far rss-sync has read each source's newest releases, per
-- feed (tv, movie, or all: both in one search). newest_at is the newest
-- publication time looked at and newest_guids the releases of that time that
-- were, so each release is looked at once; read_at is when the feed was last
-- read and detail what that read did. The rows of a source go with it.
CREATE TABLE IF NOT EXISTS rss_cursors (
  indexer_id   bigint      NOT NULL,
  feed         text        NOT NULL,
  newest_at    timestamptz,
  newest_guids text[]      NOT NULL DEFAULT '{}',
  read_at      timestamptz,
  detail       text        NOT NULL DEFAULT '',
  PRIMARY KEY (indexer_id, feed)
);
