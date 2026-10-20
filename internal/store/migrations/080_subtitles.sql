-- 080_subtitles.sql — subtitles in the instance's languages for the titles the
-- library holds (idempotent).
--
-- subtitle_titles: one row per catalog item whose file acquire looks
-- subtitles for (an episode's holder when one file covers several): what it
-- is and the identities a subtitle source is asked by, the release name known
-- of its file, how urgent it is (0 a new arrival, 1 asked for, 2 the rest of
-- the library), and what the catalog last said of it — the version a subtitle
-- is attached to, or why there is none, and its original's fingerprint.
CREATE TABLE IF NOT EXISTS subtitle_titles (
  item_id        text PRIMARY KEY,
  kind           text        NOT NULL,              -- movie | episode
  title          text        NOT NULL DEFAULT '',   -- the movie's, or the episode's series'
  year           int,
  season         int,
  episode        int,
  imdb_id        text        NOT NULL DEFAULT '',   -- a movie's own
  tmdb_id        bigint,
  parent_imdb_id text        NOT NULL DEFAULT '',   -- an episode's series'
  parent_tmdb_id bigint,
  target_id      text        NOT NULL DEFAULT '',   -- acquire's target, when it tracks the title
  title_id       text        NOT NULL DEFAULT '',
  release        text        NOT NULL DEFAULT '',   -- the release name known of its file
  priority       smallint    NOT NULL DEFAULT 2,
  aired_at       date,
  packaged_at    timestamptz,                       -- its packaged event
  version_id     text        NOT NULL DEFAULT '',
  attachable     boolean     NOT NULL DEFAULT false,
  not_attachable text        NOT NULL DEFAULT '',
  size_bytes     bigint,
  sum64          text        NOT NULL DEFAULT '',
  labels         jsonb       NOT NULL DEFAULT '{}'::jsonb,
  original_note  text        NOT NULL DEFAULT '',
  read_at        timestamptz,                       -- when the catalog last said what it has
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now()
);

-- subtitle_wants: one row per title and instance language. state is has (the
-- title has a full subtitle in it: its own, or one attached), wanted (not
-- searched yet, or due again), found (a match waits for a download),
-- notfound (nothing usable; looked for again at next_at) or failed (needs
-- attention; the reason in detail). found is the best match of the last
-- search; blocked the files never taken again: removed by a person, put in
-- another's place, or refused by the catalog for what they hold. failures
-- counts the failed downloads and attaches since the last that worked.
-- trouble is what keeps the language from the title, in plain words (detail
-- the source's or the catalog's own); retry what a failed want does next
-- (another: the next version, same: this one again, none: there is no other
-- yet); versions how many usable versions the last search found;
-- other_script the script (hant, hans) of a subtitle the title has in the
-- other characters than the language names, which doesn't count for it.
CREATE TABLE IF NOT EXISTS subtitle_wants (
  item_id      text        NOT NULL REFERENCES subtitle_titles (item_id) ON DELETE CASCADE,
  language     text        NOT NULL,
  state        text        NOT NULL DEFAULT 'wanted',
  detail       text        NOT NULL DEFAULT '',
  trouble      text        NOT NULL DEFAULT '',
  retry        text        NOT NULL DEFAULT '',     -- another | same | none
  versions     int         NOT NULL DEFAULT 0,
  other_script text        NOT NULL DEFAULT '',
  has_kind     text        NOT NULL DEFAULT '',     -- own | attached | ai | traditional
  found        jsonb,
  searched_at  timestamptz,
  next_at      timestamptz,
  searches     int         NOT NULL DEFAULT 0,
  failures     int         NOT NULL DEFAULT 0,
  asked_at     timestamptz,                         -- asked for in the console: first in line
  subtitle_id  text        NOT NULL DEFAULT '',     -- the catalog's id of the one acquire attached
  file_id      bigint,
  release      text        NOT NULL DEFAULT '',
  match        text        NOT NULL DEFAULT '',
  preview      text        NOT NULL DEFAULT '',
  attached_at  timestamptz,
  attached_how text        NOT NULL DEFAULT '',     -- auto | manual
  blocked      jsonb       NOT NULL DEFAULT '[]'::jsonb,
  updated_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (item_id, language)
);
CREATE INDEX IF NOT EXISTS subtitle_wants_state ON subtitle_wants (state, next_at);

-- subtitle_downloads: every download asked of a subtitle source, as its
-- account's quota counts them: the day's budget is read from here.
CREATE TABLE IF NOT EXISTS subtitle_downloads (
  id        bigserial PRIMARY KEY,
  at        timestamptz NOT NULL DEFAULT now(),
  item_id   text        NOT NULL,
  language  text        NOT NULL,
  file_id   bigint      NOT NULL,
  how       text        NOT NULL,                   -- auto | manual
  ok        boolean     NOT NULL,
  remaining int,
  detail    text        NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS subtitle_downloads_at ON subtitle_downloads (at);
-- The subtitle settings the console edits are acquire_settings' "subtitles".
