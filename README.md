# acquire (laedeli addon) — requests + downloads for the zaentrum platform

`acquire` is the **acquisition addon**: it brings request + download capability
to a neutral [zaentrum](https://github.com/zaentrum/zaentrum) instance. The
core platform ships only extension *seams*; installing this addon surfaces a
"request" button in chino's search and drives download → ingest → package →
playback. Uninstalled, the core shows no acquisition UI and no trace.

It is deliberately event-driven and reuses neutral core capabilities — it adds
**no** download-specific code to the core. The platform-side contracts it
builds on are documented canonically in the platform docs:
**[Extending zaentrum](https://github.com/zaentrum/zaentrum/wiki/extending)**.

## What it is

- **The acquire service** — requests (WantedItems), discovery search, native
  newznab/torznab search of your sources, the grab decision (NZB-first), a
  transactional outbox + scheduler, and ingestion of finished downloads into
  the catalog via the platform's neutral ingest seam. Search sources and
  download clients are its own configuration, edited in its React console,
  hosted inside the portal shell.
- **Following what is requested** — a requested series is followed: its
  episodes come from TMDB, read again on the clock while it runs, so new
  ones become wanted as they are announced; a requested movie is searched
  from its release on. Each source's newest releases are read every fifteen
  minutes and matched to what is wanted. Whether new episodes and requests are
  grabbed on their own, and whether the whole backlog is, are two switches in
  the console, both off until you turn them on; an automatic grab waits a
  while after an episode airs — by TVmaze's public air times when it knows
  them, asked by the series' TVDB or IMDb id alone — and takes nothing
  published before, and a request from someone who is not an admin changes
  nothing until an admin approves it, unless requests are approved at once.
- **[download-gateway](https://github.com/laedeli/download-gateway)** — the
  download plane: one API + Kafka events (`download.client.*`) in front of the
  actual download clients.
- **Subtitles from OpenSubtitles.com** — in the configured languages (by
  default the platform's), for the movies and episodes the library holds that
  lack one: new arrivals first,
  then the rest of the library, up to a daily download limit. acquire adds the
  best match of each language to the title through the catalog, where every
  player lists and plays it. A worklist in the console shows what needs you,
  and lets an admin pick a version.

The loop: a request becomes a **WantedItem** → a grab (manual, or `find &
grab`, which searches your sources NZB-first) hands the release to the gateway →
the gateway drives a download client and emits progress events → on
`completed`, acquire ingests the finished file in place (`POST /api/ingest`,
which emits `catalog.item.discovered`) → the core pipeline enriches, analyzes,
transcodes, packages → on `catalog.item.packaged` the request flips to
**fulfilled** and plays in chino. Commands go over HTTP at the edges;
everything else is Kafka.

## Documentation

Full design docs live in [`docs/`](./docs) and are mirrored to this repo's
**[Wiki](https://github.com/laedeli/acquire/wiki)**:

- [Architecture](./docs/architecture.md) — how acquire uses the platform's
  extension seams; the event-driven loop.
- [The acquire service](./docs/acquire.md) — request lifecycle, API, console,
  schema.
- [Download gateway & clients](./docs/download-gateway.md) — the download plane.
- [Search sources & NZB-first](./docs/indexers-and-nzb.md) — how auto-grab
  chooses a release.
- [Deploying the addon](./docs/deploying.md) — installing it on a platform,
  the addon-side view of the platform's
  [installing addons](https://github.com/zaentrum/zaentrum/wiki/extending-installing)
  guide.

## Status

Live end-to-end on the author's instance: request → grab → download → ingest →
package → playback, with the console (requests, downloads, search, search
sources, download clients, series, missing backlog, quality profiles) hosted
in the portal. Subtitles (since chart 0.5.0) are new, and need the catalog's
attach seam; following requested series and movies, TMDB read again on the
clock and the sources' newest releases (since chart 0.6.0) are newer still.
Expect sharp
edges: this is a young codebase serving one deployment so far, and
installation still involves the manual steps the platform's
[addon identity](https://github.com/zaentrum/zaentrum/wiki/extending-identity)
page describes.
