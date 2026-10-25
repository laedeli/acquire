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
-- unfollowed_at is when the last request went (NULL while one follows it);
-- followed_at stays the first follow's, so what a follow added is still what
-- was requested when the title is asked for again. pre_monitored and
-- pre_monitor_new are what the title's monitoring was before the follow
-- began, given back when the last request goes (NULL: a title the request
-- created).
ALTER TABLE titles ADD COLUMN IF NOT EXISTS followed_at     timestamptz;
ALTER TABLE titles ADD COLUMN IF NOT EXISTS unfollowed_at   timestamptz;
ALTER TABLE titles ADD COLUMN IF NOT EXISTS pre_monitored   boolean;
ALTER TABLE titles ADD COLUMN IF NOT EXISTS pre_monitor_new boolean;
ALTER TABLE titles ADD COLUMN IF NOT EXISTS origin          text NOT NULL DEFAULT '';
ALTER TABLE titles ADD COLUMN IF NOT EXISTS tmdb_status     text NOT NULL DEFAULT '';
ALTER TABLE titles ADD COLUMN IF NOT EXISTS inventory_at    timestamptz;
ALTER TABLE titles ADD COLUMN IF NOT EXISTS inventory_error text NOT NULL DEFAULT '';
ALTER TABLE titles ADD COLUMN IF NOT EXISTS origin_country  text[] NOT NULL DEFAULT '{}';

-- titles: the schedule source's word on a series (search.airTimes, TVmaze):
-- schedule_id is the show's id there (NULL: not looked up, or not known
-- there), schedule_at when its air times were last read, schedule_error why
-- the last read failed — a show the source does not know is looked up again
-- after a week.
ALTER TABLE titles ADD COLUMN IF NOT EXISTS schedule_id    bigint;
ALTER TABLE titles ADD COLUMN IF NOT EXISTS schedule_at    timestamptz;
ALTER TABLE titles ADD COLUMN IF NOT EXISTS schedule_error text NOT NULL DEFAULT '';

-- titles: genres are TMDB's genre ids of a series (16: animation) and
-- tmdb_type its kind of show (Scripted, News, Talk Show …): an animation from
-- Japan is numbered by an absolute episode number, a talk or news show named
-- by its date — neither by the season and episode acquire tracks, so no
-- automatic grab takes anything of them.
ALTER TABLE titles ADD COLUMN IF NOT EXISTS genres    int[] NOT NULL DEFAULT '{}';
ALTER TABLE titles ADD COLUMN IF NOT EXISTS tmdb_type text  NOT NULL DEFAULT '';

-- titles: tmdb_seasons is how many seasons TMDB lists of a series — all a
-- request awaiting an admin's approval reads of it: its episodes are made
-- once it is approved.
ALTER TABLE titles ADD COLUMN IF NOT EXISTS tmdb_seasons int NOT NULL DEFAULT 0;

-- seasons and acquisition_targets: follow_monitored marks the ones a follow
-- monitored that would not be monitored without it — a season of a series
-- whose new seasons nobody monitored before, an episode of one. The follow's
-- end unmonitors them, and a follow again monitors them again.
ALTER TABLE seasons             ADD COLUMN IF NOT EXISTS follow_monitored boolean NOT NULL DEFAULT false;
ALTER TABLE acquisition_targets ADD COLUMN IF NOT EXISTS follow_monitored boolean NOT NULL DEFAULT false;

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
-- ever_held is set once the library has held the target: what it held once
-- is not one of the new things any more, so a deletion, or a reconcile that
-- missed it, does not have it grabbed again on its own.
-- tmdb_episode_id is TMDB's id of the episode, which a renumbering keeps:
-- the episode it holds is still held at its new number. tmdb_gone_at is when
-- a read of TMDB first did not list the target: it goes once a read a day or
-- more later agrees, unless it holds anything — the library's file, a grab,
-- what its grabs did —, and it is grabbed by nobody meanwhile.
ALTER TABLE acquisition_targets ADD COLUMN IF NOT EXISTS added_at        timestamptz;
ALTER TABLE acquisition_targets ADD COLUMN IF NOT EXISTS ever_held       boolean NOT NULL DEFAULT false;
ALTER TABLE acquisition_targets ADD COLUMN IF NOT EXISTS tmdb_episode_id bigint;
ALTER TABLE acquisition_targets ADD COLUMN IF NOT EXISTS tmdb_gone_at    timestamptz;
ALTER TABLE acquisition_targets ADD COLUMN IF NOT EXISTS episode_title   text NOT NULL DEFAULT '';
ALTER TABLE acquisition_targets ADD COLUMN IF NOT EXISTS release_pending boolean NOT NULL DEFAULT false;
ALTER TABLE acquisition_targets ADD COLUMN IF NOT EXISTS release_kind    text NOT NULL DEFAULT '';

-- acquisition_targets: air_stamp is the moment an episode airs, as the
-- schedule source says it (NULL: no hour known) — an automatic grab takes
-- nothing published more than 30 minutes before it, and waits
-- search.airDelayAfterAirstampMinutes after it. release_country is where a
-- requested movie's release to watch at home comes out (ISO 3166-1), whose
-- midnight the day it comes out begins at.
ALTER TABLE acquisition_targets ADD COLUMN IF NOT EXISTS air_stamp       timestamptz;
ALTER TABLE acquisition_targets ADD COLUMN IF NOT EXISTS release_country text NOT NULL DEFAULT '';

-- acquisition_targets: held_as is the number the catalog knows a held file
-- by when TMDB numbers its episode anew (S01E02 of a file held at S01E03):
-- the catalog goes on naming the file by it, and a reconcile reads it as the
-- episode the file holds ('' for a file held at its own number).
ALTER TABLE acquisition_targets ADD COLUMN IF NOT EXISTS held_as text NOT NULL DEFAULT '';

-- wanted_items: the title a request follows ('' for one that follows none: a
-- request without a TMDB id, or one made before 042 and not asked again).
-- approval is an admin's word on a request that follows a title: awaiting
-- (made by someone who is not an admin, with requests.autoApprove off: its
-- title is followed, nothing of it grabbed on its own), approved or declined;
-- '' for one that follows none. decided_by and decided_at are who approved or
-- declined it, and when.
ALTER TABLE wanted_items ADD COLUMN IF NOT EXISTS title_id   text NOT NULL DEFAULT '';
ALTER TABLE wanted_items ADD COLUMN IF NOT EXISTS approval   text NOT NULL DEFAULT '';
ALTER TABLE wanted_items ADD COLUMN IF NOT EXISTS decided_by text NOT NULL DEFAULT '';
ALTER TABLE wanted_items ADD COLUMN IF NOT EXISTS decided_at timestamptz;

CREATE INDEX IF NOT EXISTS wanted_title_idx     ON wanted_items (title_id) WHERE title_id <> '';
CREATE INDEX IF NOT EXISTS titles_inventory_idx ON titles (inventory_at NULLS FIRST) WHERE monitored;
CREATE INDEX IF NOT EXISTS targets_added_idx    ON acquisition_targets (title_id, added_at) WHERE added_at IS NOT NULL;

-- rss_cursors: what rss-sync has looked at of each source's newest releases,
-- per feed (tv, movie, or search: both in one search): the releases of the
-- feed looked at, by an id of each (a 64-bit hash of its guid, else of its
-- name: seen_ids), with when acquire first saw each, by its own clock (unix
-- seconds: seen_at) — the last 1,000 it saw, the last first. A release is
-- looked at once, whenever the source lists it among its newest, however it
-- dates it. read_at is when the feed was last read and detail what that read
-- did. The rows of a source go with it.
CREATE TABLE IF NOT EXISTS rss_cursors (
  indexer_id bigint      NOT NULL,
  feed       text        NOT NULL,
  seen_ids   bigint[]    NOT NULL DEFAULT '{}',
  seen_at    bigint[]    NOT NULL DEFAULT '{}',
  read_at    timestamptz,
  detail     text        NOT NULL DEFAULT '',
  PRIMARY KEY (indexer_id, feed)
);
