-- Air times, as acquire reads them since 0.6.1 (search.airTimes): an episode
-- TVmaze dates more than a day after TMDB, an episode of a broadcast TVmaze
-- does not date to the hour, and one the two cannot be told the same of.
-- Every statement holds over a database that ran this file before.

-- titles: schedule_version is the version of the rules a series' air times
-- were read by (0: before these): a series read by an earlier one is read
-- again first, and nothing of it is grabbed on its own until it is.
ALTER TABLE titles ADD COLUMN IF NOT EXISTS schedule_version int NOT NULL DEFAULT 0;

-- acquisition_targets: schedule_episode_id is the schedule source's id of the
-- episode a read took for the target — the same episode on the next read,
-- whatever its numbers then (NULL: none). air_stamp_estimated marks an air
-- time estimated from the usual hour of the series' broadcasts on the day the
-- episode is dated. schedule_day is the source's date of the episode when it
-- is more than a day after TMDB's — TMDB has not caught up with a move —: the
-- day it is told by. schedule_hold says why no automatic grab takes the
-- episode until TMDB and the source agree, or it has an air time ('': none).
ALTER TABLE acquisition_targets ADD COLUMN IF NOT EXISTS schedule_episode_id bigint;
ALTER TABLE acquisition_targets ADD COLUMN IF NOT EXISTS air_stamp_estimated boolean NOT NULL DEFAULT false;
ALTER TABLE acquisition_targets ADD COLUMN IF NOT EXISTS schedule_day        date;
ALTER TABLE acquisition_targets ADD COLUMN IF NOT EXISTS schedule_hold       text NOT NULL DEFAULT '';
