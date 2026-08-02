-- Repair the LIVE default profile row.
--
-- 004 seeds with ON CONFLICT (id) DO NOTHING, so editing that seed only ever
-- fixes a fresh database. Every already-running install still holds the config
-- that made the ranker prefer junk:
--
--   * rejectTerms were matched against the raw title, so none of them ever
--     fired; the terms "cam" and "ts" are also unsafe once matching is fixed
--     (they hit the 2018 film "Cam" and any title containing the word "ts"),
--     so the cam family moves to rejectSources, which matches the PARSED source
--   * sources scored nothing, so the size penalty ranked HDTV above Blu-ray remux
--   * one global 25 GB cap rejected 71% of real Remux-1080p releases and every
--     Remux-2160p; caps are now per resolution, maxima only
--   * resolutions listed 1080p ahead of 2160p
--   * nothing expressed a language preference at a weight that could matter
--
-- Self-disabling: it only touches rows that have not been migrated yet. It is
-- deliberately NOT also guarded on created_at = updated_at — ANDing two guards
-- only increases the chance of a silent no-op, and an operator who edited the
-- profile still wants a ranker that works.
--
-- Migrations here re-run on every boot with no tracking table, so this must be
-- idempotent: the WHERE clause is what makes it so.
UPDATE quality_profiles SET
  config = config
    - 'rejectTerms'
    || jsonb_build_object(
         'resolutions',           '["2160p","1080p","720p"]'::jsonb,
         'rejectTerms',           '["hdcam","camrip","hdts","telesync","screener","dvdscr","workprint"]'::jsonb,
         'rejectSources',         '["cam"]'::jsonb,
         'sourceScores',          '{"remux":400,"bluray":300,"webdl":200,"webrip":100,"hdtv":25,"dvd":0}'::jsonb,
         'maxSizeMbByResolution', '{"2160p":80000,"1080p":40000,"720p":15000,"480p":8000}'::jsonb,
         'maxSizeMb',             to_jsonb(100000),
         'maxLanguages',          to_jsonb(3),
         'languagePenalty',       to_jsonb(60)
       ),
  updated_at = now()
WHERE NOT (config ? 'sourceScores');
