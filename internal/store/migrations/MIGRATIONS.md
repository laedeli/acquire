# Migration number registry

`migrate()` reads every file in this directory and `Exec`s it **in lexical order,
on every boot**, with no tracking table. Two consequences drive everything here:

1. **Every file must be idempotent.** A migration that is not safe to re-run is a
   migration that breaks the next rolling restart, not a future one.
2. **A number collision is a production outage, not a merge conflict.** Two files
   claiming `005_` both run. If the second re-creates a table with different
   columns, `CREATE TABLE IF NOT EXISTS` silently no-ops and the service then
   fails at runtime forever. If it errors instead, boot fails — and there is no
   version row to skip past and (until logical backup ships) nothing to restore
   from. `log.Fatalf` on a boot path plus no tracking table means the pod
   **CrashLoopBackOffs permanently**.

During planning, eight separate work packages independently allocated `005_`.
This file exists so that cannot happen again. **Claim your number here in the
same commit that adds the file.**

## Allocated

| Range | Phase / package | Status |
|---|---|---|
| `001`–`004` | pre-programme: init, downloads, soft-ref, profiles | shipped |
| `005` | P0 / WP0a — repair the live ranker profile row | shipped |
| `006`–`009` | P2 / WP2 — outbox, sagas, schedules, history | **in progress** |
| `010`–`019` | P3 / WP3 — the WANT model (titles, seasons, acquisition_targets) | reserved |
| `020`–`024` | P4 / WP4 — typed search, id translation cache | reserved |
| `025`–`029` | P5 / WP5 — blocklist, retry, upgrades, retention | reserved |
| `030`–`039` | P6 / WP6 — requests, discovery, notifications | reserved |
| `040`–`059` | P7 / WP7 — TV: episodes, split import, continuous acquisition | reserved |
| `060`–`079` | P8 / WP8 — indexer registry, health, definitions | reserved |
| `080`+ | P9 and beyond | unreserved |

## Rules

- **Take the next free number in your phase's range.** Do not renumber an
  existing file: it has already run somewhere, and renaming it makes it run
  again under a new name.
- **Never edit a shipped migration to change what it does.** Add a new one. The
  old file keeps executing on every boot forever.
- **Repairing an existing row needs its own migration, guarded on the row's
  current state.** `004_profiles.sql` seeds `ON CONFLICT (id) DO NOTHING`, so
  editing a seed only ever reaches a *fresh* database — every running install
  keeps the old value, and every new jsonb field decodes to its zero value.
  `005` is the worked example: it guards on `NOT (config ? 'sourceScores')` so
  it self-disables once applied.
  Guard on **one** condition. ANDing two guards only increases the chance of a
  silent no-op.
- **Anything data-dependent belongs in an admin endpoint or a one-shot Job, not
  here.** A backfill on the boot path turns a data problem into an outage class.
- **Test against a production-shaped fixture, not an empty database.** See
  `migrate_test.go`: a fresh-database test cannot catch a repair bug, because
  `ON CONFLICT DO NOTHING` makes the seed and the repair indistinguishable there.
  CI boots the migrations three times over a real Postgres.
