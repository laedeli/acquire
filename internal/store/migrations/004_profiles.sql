-- Quality profiles: what "good" means for this library.
--
-- Until now ranking was hard-coded (NZB first, then biggest / most seeded),
-- which reliably picked bloated multi-language remuxes. A profile makes the
-- preference explicit and lets the console explain why a release won or lost.
CREATE TABLE IF NOT EXISTS quality_profiles (
  id          text PRIMARY KEY,
  name        text NOT NULL,
  is_default  boolean NOT NULL DEFAULT false,
  config      jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now()
);

-- Exactly one default.
CREATE UNIQUE INDEX IF NOT EXISTS quality_profiles_default_idx
  ON quality_profiles (is_default) WHERE is_default;

-- Seed a sensible starting point: prefer NZB, want 1080p, treat HEVC as a bonus,
-- refuse cams, and cap size so a 70 GB remux never wins by default. Guarded so a
-- re-run never overwrites an edited profile.
INSERT INTO quality_profiles (id, name, is_default, config) VALUES (
  'default', 'default', true,
  '{
     "preferProtocol": "usenet",
     "resolutions": ["1080p", "2160p", "720p"],
     "preferredCodecs": ["x265", "hevc"],
     "rejectTerms": ["cam", "camrip", "ts", "telesync", "screener", "workprint"],
     "minSizeMb": 500,
     "maxSizeMb": 25000,
     "minSeeders": 1,
     "preferHdr": false
   }'::jsonb
) ON CONFLICT (id) DO NOTHING;
