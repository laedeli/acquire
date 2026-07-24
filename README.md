# acquire (laedeli addon) — requests + downloads for the zaentrum platform

`acquire` is the **acquisition addon**: it brings request + download capability to
a neutral zaentrum instance. The core platform ships only extension *seams*;
installing this addon (via the Lädeli) surfaces a "request" button in chino's
search and drives download → package → playback. Uninstalled, the core shows no
acquisition UI.

This is **v0.1** for zaentrum-beta. It is deliberately event-driven and reuses
neutral core capabilities — it adds **no** download-specific code to the core.

## Architecture (event-driven; commands REST at the edges)

```
user (chino search miss)
  → "request" (extension slot, kind=link → /acquire/?q=…)
  → acquire SPA: TMDB discovery pick → POST /api/wanted   [command]
  → admin grab: POST /api/wanted/{id}/grab                [command]
      → download-gateway POST /api/v1/downloads (wanted_item_id)   [command]
  → gateway emits zaentrum-beta.download.client.{started,progress,completed,failed}  [events]
  → acquire consumes .completed:
      • stage the finished video into packages/_inbox/<itemId>/
      • create the catalog item (with the request's TMDB metadata) + source asset
      • EMIT zaentrum-beta.catalog.item.discovered          [event → the pipeline]
  → core pipeline: enrich → analyze → transcode → PACKAGE (packaged storage)  [events]
  → acquire consumes .packaged → wanted = fulfilled
  → live status to the SPA + chino slot via acquire's Kafka→SSE bridge         [events]
```

The only non-events are the three genuine user/boundary **commands** (request,
grab, gateway download-add) — matching the platform idiom (commands REST,
reactions Kafka). Packaged storage is the durable serving artifact; the raw
download in `_downloads`/`_inbox` is transient staging, cleaned after packaging.

## What's authored (this tree, compiling backend pieces)

- `internal/config` — env wiring (issuer, DB, gateway, katalog, TMDB, Kafka mTLS, inbox paths).
- `internal/store` — `acquire_beta` schema (wanted_items + grabs) + CRUD + status transitions + client-job↔wanted bridge.
- `internal/events` — Kafka mTLS: **producer** for `catalog.item.discovered`; **consumer** for `download.client.completed/failed` + `catalog.item.packaged`. Tenant-prefixed topics.
- `internal/gateway` — download-gateway command client + a client-credentials `TokenSource` (service account on the shared realm).
- `internal/tmdb` — multi-search discovery (movies + series → tmdb id + metadata).

## Remaining integration points (wire + verify against LIVE beta katalog-manager)

1. **Catalog write (the ingest step).** On `download.client.completed`, acquire
   must create the item and attach the staged source file, then emit
   `catalog.item.discovered`. katalog-manager exposes a GraphQL `CreateItem`
   mutation (`internal/graph/resolver.go:538`, input: type/title/year/
   description/…). Open question to verify live: how to attach the **source
   PlaybackAsset path** (inbox path) so the analyzer reads it — GraphQL asset
   mutation vs a REST seam. Model on `katalog-manager/internal/odownloader/
   poller.go` (which streams to inbox + writes the asset) + the manual-packaging
   recipe. Verify the pipeline picks up the discovered event and packages.
2. **In-library check** — `GET katalog-api /api/v1/items?...` by tmdb id (confirm
   a tmdb filter exists; else title+year match) to flag "already in library".
3. **httpapi** — REST (`/api/wanted`, `/grab`, `/discover`, `/status`) with OIDC
   bearer auth (user role requests, admin grabs) + the Kafka→SSE bridge.
4. **web** — embedded React SPA at `/acquire/` (@nalet/design-system): discovery
   search + request, my-requests, admin grab dialog, live status via SSE.
5. **main** — wire store + events + clients + http; lazy/fail-closed OIDC.

## Deploy (beta) — see zaentrum/deploy zaentrum-beta overlay + SSO clients
`laedeli-acquire` (public PKCE, redirect zaentrum.beta.nalet.cloud/acquire/*) +
`laedeli-acquire-svc` (confidential SA, role `zaentrum-addon`). DB `acquire_beta`
on the shared cluster. qbittorrent-nox saving into `packages/_downloads/`.
