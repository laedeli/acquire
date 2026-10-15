# The acquire service

`acquire` (Go, module `github.com/laedeli/acquire`) is the addon's brain and face.
It records requests, drives grabs, reacts to download and pipeline events, and
serves its console: a federated module the portal hosts. acquire has **no page
of its own** — the portal is the shell, and the console opens in place there,
at `/portal/app/acquire`, on the portal's sign-in. The service serves the
console's bundle under `/embed/` and its API under `/api/`, and sends a browser
that asks it for a page to the portal (see [the console](#the-console) and
[Deploying the addon](./deploying.md#where-the-console-is)).

## Request lifecycle

A request is a **WantedItem**. Its `status` walks a small state machine:

```mermaid
stateDiagram-v2
    [*] --> pending: POST /api/wanted
    pending --> downloading: grab / find & grab
    downloading --> packaging: download completed → ingested
    packaging --> fulfilled: pipeline packaged
    pending --> failed
    downloading --> failed
    packaging --> failed
    failed --> downloading: re-grab
    fulfilled --> [*]
```

- **pending** — the request exists (v1 is auto-approved: it simply waits for an
  admin to grab it).
- **downloading** — a source was handed to a download client. The detail line
  records how, e.g. `grabbed NZB from <source> via nzbget` or `grabbed via qbittorrent`.
- **packaging** — the download finished; acquire resolved the video file and
  ingested it (`ingested; pipeline running`) — copied into the library's
  arrivals first when the download lies outside them (`downloaded; copying into
  the library` meanwhile, still *downloading*).
- **fulfilled** — the pipeline packaged the item; it now plays in chino.
- **failed** — any of: grab error, no releases (naming the sources that did not
  answer), download failed, no video file in the completed download, or ingest
  error. The detail line carries the reason. `pending` and `failed` requests are
  re-grabbable. A grab that is merely **refused** — no download client for the
  protocol, not enough free space, too many downloads at once, a source out of
  downloads for the day — leaves the request as it was; one that waits for room
  on the downloads volume says so in its detail line.

acquire reacts to these consumed events:

- `download.client.started` / `.progress` → record live telemetry (state, bytes,
  speed, ETA, seeders/health) and stream it to the console.
- `download.client.completed` → resolve the video file → `POST /api/ingest` →
  `packaging`, or, for a download outside the arrivals, queue its copy (below).
  Idempotent: a repeat (e.g. after a gateway restart) is ignored once the
  request has moved past the download, and queues nothing twice.
- `download.client.failed` → `failed` (with the client's error).
- `catalog.item.packaged` → look up the request by item id → `fulfilled`.

**Resolving the video file** from a completed download: acquire walks the download
folder as it sees it and picks the **largest** file with a video extension
(`.mkv .mp4 .m4v .avi .mov .ts .webm`). A file inside the library's arrivals it
ingests *in place*. With `ACQUIRE_ARRIVALS_ROOT` set, a file outside them — the
download clients the chart runs work on a volume of their own — is **copied in
first**: a worker takes one import at a time, copies the video and the
subtitle files the catalog pairs with it into
`<arrivals>/acquire/<request>/<client>-<job>/` (one sequential pass per file
into a temporary dot name, synced, checked by size and a SHA-256 of both ends,
renamed into place), and hands the copy over through the same
`POST /api/ingest`. A failed copy or hand-over leaves the download where it is
and is tried again, a minute after the first failure and up to an hour apart.
For the clients the deployment runs, acquire then releases the download in
the client, through the client's own API — an NZB right after its verified
copy, a torrent once qBittorrent stopped it at its seeding goal, removed with
its data. acquire never moves or deletes a client's files itself.

## HTTP API

Router: `go-chi`. Auth is an OIDC bearer verifier (issuer-only; roles from the
JWT `realm_access.roles`). If `OIDC_ISSUER` is unset, auth is disabled for local
dev (every caller is treated as admin).

| Method | Path | Who | Purpose |
|---|---|---|---|
| `GET` | `/healthz` | public | liveness |
| `GET` | `/readyz` | public | DB ping (503 when down) |
| `GET` | `/api/config` | public | what the console reads: the admin role and the `autoGrab` flag |
| `GET` | `/api/events` | any signed-in¹ | live stream: change pings + download telemetry |
| `GET` | `/api/wanted` | any signed-in | list requests |
| `POST` | `/api/wanted` | **user** or admin | create a request |
| `GET` | `/api/discover?q=` | any signed-in | TMDB search, flags in-library hits |
| `GET` | `/api/status?tmdbId=` | any signed-in | status of the newest request for a TMDB id |
| `POST` | `/api/wanted/{id}/grab` | **admin** | hand a magnet/URL to a client |
| `POST` | `/api/wanted/{id}/autograb` | **admin** | search the sources + grab the best release |
| `GET` | `/api/wanted/{id}/releases` | **admin** | the ranked releases for a request `{candidates, incomplete}` |
| `POST` | `/api/wanted/{id}/pick` | **admin** | grab one of them (sends back its `release` reference) |
| `GET` | `/api/search?q=&indexers=` | **admin** | manual search across the sources `{candidates, incomplete}` |
| `POST` | `/api/search/grab` | **admin** | grab a manual search result |
| `DELETE` | `/api/wanted/{id}` | **admin** | remove a request |
| `GET` | `/api/downloads` | any signed-in | live + recently finished downloads |
| `GET` | `/api/clients` | any signed-in | per-client health + aggregate speed |
| `POST` | `/api/downloads/{adapter}/{id}/{action}` | **admin** | `pause` \| `resume` \| `cancel` |
| `GET` | `/api/setup` | **admin** | the setup checklist: sources, clients, grab policy |
| `GET` | `/api/health/system` | public | diagnostic checks (database, outbox, storage, sources, clients, clock); always 200 |
| `GET` | `/embed/…` | public | the console's bundle: `assets/remoteEntry.js`, its chunks and `assets/console.css` |
| `GET` | `/` and any other page | public | `302` to acquire's app in the portal (see [the console](#the-console)) |

¹ The stream carries download telemetry (progress, speed, ETA), so it is
authenticated. Clients read it with **fetch-streaming** rather than
`EventSource`, which cannot send an `Authorization` header. It emits `changed`
(refetch the lists) and `download` (one telemetry row, applied in place).

### Configuration API

Search sources, download clients and the search and grab policy are acquire's
own data, edited by admins only:

| Method | Path | Purpose |
|---|---|---|
| `GET` · `POST` | `/api/indexers` | list · add search sources |
| `PUT` · `DELETE` | `/api/indexers/{id}` | replace · remove a source |
| `POST` | `/api/indexers/test` · `/api/indexers/{id}/test` | try an unsaved · a saved source |
| `GET` · `POST` | `/api/download-clients` | list (with live gateway state) · add download clients |
| `PUT` · `DELETE` | `/api/download-clients/{id}` | replace · remove a client (`409` while downloads are in flight, and for a managed client) |
| `GET` | `/api/download-clients/types` | the client types the gateway runs |
| `POST` | `/api/download-clients/test` | reach a client through the gateway without saving it |
| `GET` · `PUT` | `/api/settings/search` | the search and grab policy |

Writes send the revision they were based on in `If-Match` and get `409` when it
is stale; invalid content gets `422 {"fieldErrors":[…]}`. A **managed** client
(`"managed": true`) is one the deployment runs and declares in
`ACQUIRE_MANAGED_CLIENTS`; acquire writes it at start from that declaration, and
the API refuses to replace or remove it (`409`), though it can be tested. Secrets — source API
keys, client passwords and tokens — are write-only: omit to keep, a string to
replace, `{"clear":true}` to remove; reads say only whether one is set. Every
write is recorded in `settings_audit` without its values. See
[Search sources & NZB-first](./indexers-and-nzb.md) and
[Download gateway & clients](./download-gateway.md).

### Roles

| Role (default) | Env | Can |
|---|---|---|
| `zaentrum-user` | `ACQUIRE_USER_ROLE` | request titles |
| `zaentrum-admin` | `ACQUIRE_ADMIN_ROLE` | request **+** grab, auto-grab, remove, search, configure |

## The console

A Vite/React/TypeScript app on the nalet design system, built into the binary
with `go:embed` (source in `web/`, one container — no nginx sidecar). It is a
**federated module** and nothing else. The portal shell loads
`/embed/assets/remoteEntry.js` through its app proxy
(`/api/portal/apps/acquire/`), renders the exposed `Console` inside its own
React tree (React and react-dom are shared; no iframe) and links
`assets/console.css`. The shell signs the user in and hands the console the
API base and the user's access token; a `401` goes back to the shell, which
signs the user in again. acquire has no sign-in, no OIDC client and no page of
its own. **Realm roles come from the ACCESS token** — the ID-token profile
carries none, so reading it makes every user look like a non-admin.

Each tab has a hash route inside the app's route —
`/portal/app/acquire#/requests`, `#/downloads` and so on — which the launchpad
tiles open.

A browser that asks acquire itself for a page — `/`, or any other path the
standalone console of earlier versions answered — gets a `302` to
`ACQUIRE_PLATFORM_URL` followed by `/portal/app/acquire`. A search (`?q=`) is
carried and nothing else of the query, so a stale sign-in callback never
reaches the portal; the browser keeps the place in the console (`#/downloads`).
Without `ACQUIRE_PLATFORM_URL` the redirect goes to the portal's root,
`/portal/`, on the host the browser asked. Unknown `/api/` paths, missing files
of the bundle and other methods are a `404`, never a redirect or a page.

Tabs:

- **requests** — status pill, chosen release, and a live progress bar with speed,
  ETA and the client's own state inline. Admin actions per row: **find & grab**
  (automatic pick), **releases** (interactive search — see below), **magnet**
  (paste a magnet/`.torrent` URL), **remove**.
- **downloads** — every client job with progress and telemetry, pause/resume/
  cancel, under per-client health chips (speed, free disk, active news servers).
- **search** — free-text search across the sources, scoped to some of them if
  wanted, with every release scored and grabbable.
- **discover** — TMDB search + request; in-library titles are marked.
- **search sources** — add, edit, test, enable and remove newznab/torznab
  sources; their caps, today's usage against their limits, and their health.
- **clients** — the download clients: add, edit, test, enable and remove, with
  the gateway's live state and whether it runs the stored revision. The ones
  the addon runs itself carry a **managed** badge and can only be tested.
- **settings** — search and grab (protocol preference, free-space floor,
  downloads at once) and the quality profile.

On **requests** and **search**, admins see what setup still needs, with a link
to the tab that fixes it.

The **release picker** (`releases`) runs the same search auto-grab uses but
shows the ranked candidates — protocol, source, size, seeders and *why* each was
ranked where it was — so an admin can override the automatic choice.
- **Live progress** — each downloading request shows a progress bar with speed,
  ETA and the client's own state; a **downloads** table lists every job with
  per-client health chips and pause/resume/cancel.
- **Live updates** — the console reads `/api/events` with fetch-streaming, applying
  `download` rows in place and refetching on a `changed` ping (30s fallback poll).

## Storage

acquire owns a small Postgres schema (applied on boot, idempotent):

**`wanted_items`** — `id`, `tmdb_id`, `media_type` (`movie`/`series`), `title`,
`year`, `poster_url`, `requested_by` (Keycloak sub), `requested_at`, `status`,
`detail` (last status message), `item_id` (catalog id once created), `updated_at`.

**`grabs`** — `wanted_id` (FK, cascade), `adapter` (the client id),
`client_job_id`, `source` (the link with its credentials removed), plus the
release that won (`release_title`, `indexer`, `indexer_id`, `release_guid`,
`protocol`, `size_bytes`, `seeders`, `reason`) and the full link sealed in
`source_ct`; PK `(wanted_id, adapter, client_job_id)`. Terminal events
normally map back via the `wanted_item_id` the gateway echoes; this table is the
fallback when a restarted gateway no longer knows it.

**`indexers`** and **`indexer_usage`** — the search sources (API key sealed,
cached caps, health) and what each spent per UTC day.

**`imports`** — the finished downloads acquire copies into the arrivals and then
releases in their clients: the files as the client reported them, the state
(`pending`, `imported`, `done`, `failed`), attempts, the last detail, the copy
handed over and when to look again.

**`download_clients`** — the download clients (secret sealed), with a generation
from one sequence that is the revision pushed to the gateway, and whether the
deployment manages the client (`managed`).

**`acquire_settings`** and **`settings_audit`** — the search and grab policy,
and who changed which configuration when (never the values).

**`downloads`** — one row per client job (`adapter` + `client_job_id`): state,
the client's native state, bytes, speed, ETA, seeders/health, timestamps. This is
a *projection* of the clients' state, so `wanted_id` is a soft reference — a
download whose request was deleted simply loses the link. Terminal rows keep the
telemetry the progress stream accumulated, and are never resurrected by a late
progress message.

## Configuration

| Env | Default | Purpose |
|---|---|---|
| `ADDR` | `:8080` | listen address |
| `OIDC_ISSUER` | — | realm issuer (blank ⇒ auth disabled, dev only) |
| `ACQUIRE_ADMIN_ROLE` | `zaentrum-admin` | admin role |
| `ACQUIRE_USER_ROLE` | `zaentrum-user` | request role |
| `ACQUIRE_PLATFORM_URL` | unset | the platform's address, scheme and host (`https://zaentrum.example.org`; a bare host means https): a browser that asks acquire for a page is sent to `/portal/app/acquire` there; unset sends it to `/portal/` on the host it asked |
| `PG_URL` (`DATABASE_URL`) | — | Postgres URL |
| `DOWNLOAD_GATEWAY_URL` | — | the gateway base URL |
| `KATALOG_URL` | — | catalog read API (in-library check) |
| `KATALOG_MANAGER_URL` | — | ingest target |
| `OIDC_TOKEN_URL` | — | token endpoint for the service account |
| `ACQUIRE_SVC_CLIENT_ID` / `ACQUIRE_SVC_CLIENT_SECRET` | — | client-credentials for gateway + ingest calls |
| `TMDB_API_KEY` | — | discovery (optional) |
| `ACQUIRE_CONFIG_KEY` | — | base64 32-byte key sealing stored credentials; without it none can be stored |
| `ACQUIRE_CONFIG_KEY_PREVIOUS` | — | the key before a rotation, still used to open values |
| `ACQUIRE_ENDPOINT_DENY` | — | comma list of hostnames / domain suffixes configuration may never point at |
| `ACQUIRE_ENDPOINT_ALLOW_INTERNAL` | `false` | allow cluster services in other namespaces |
| `POD_NAMESPACE` | service account namespace | acquire's own namespace, for the rule above |
| `ACQUIRE_PREFER` | `usenet` | seeds the protocol preference on first boot |
| `ACQUIRE_STORAGE_FLOOR_GB` | `500` | seeds the free-space floor on first boot |
| `ACQUIRE_MAX_CONCURRENT_GRABS` | `3` | seeds the downloads-at-once cap on first boot |
| `KAFKA_BROKERS` | — | bootstrap (blank ⇒ consumer off, service stays up) |
| `KAFKA_CERT_DIR` | `/etc/kafka-cert` | mTLS cert dir (`user.crt`/`user.key`/`ca.crt`); set to an empty value for a plaintext broker |
| `KAFKA_TOPIC_PREFIX` | `zaentrum-beta.` | tenant prefix |
| `KAFKA_GROUP_ID` | `acquire` | consumer group |
| `ACQUIRE_INBOX_ROOT` | `/var/lib/katalog/packages/_inbox` | staging folder on the media storage |
| `ACQUIRE_DOWNLOADS_ROOT` | `/var/lib/katalog/packages/_downloads` | where downloads are visible to acquire when a client names no folder of its own |
| `ACQUIRE_ARRIVALS_ROOT` | unset | the library's arrivals as acquire and the catalog see it, writable: finished downloads outside it are copied in before the hand-over; unset hands every download over in place |
| `ACQUIRE_DOWNLOADS_RESERVE` | `0` | bytes kept free on the volume the managed clients work on: a release for one of them waits until that volume holds twice its size for an NZB, its size for a torrent, plus this |
| `ACQUIRE_MANAGED_CLIENTS` | unset | the download clients the deployment runs: a JSON list in the shape the console writes a client in (`id`, `type`, `baseUrl`, `auth`, `username`, `secret`, `protocols`, `category`, `remotePath`, `localPath`), stored as managed clients at start; set but empty declares none |

Search sources and download clients are not environment settings: they are
added in the console. `INDEXER_URL` / `INDEXER_API_KEY` are no longer read. The
exception is the download clients the deployment runs itself: the addon chart
declares them in `ACQUIRE_MANAGED_CLIENTS`, from its Secret, and at start, before
its first push to the gateway, acquire creates them, brings them in line with
the declaration, takes over a client of the same id and type once no download
is in flight on it, and removes — or, while downloads are in flight, disables —
the managed clients no longer declared. Unset leaves the stored clients alone.

acquire calls the gateway and katalog-manager with a **service-account** token
(client-credentials), refreshed shortly before expiry, so those calls are
authenticated independently of any user.

## Next

- [Download gateway & clients](./download-gateway.md) — where the bytes come from.
- [Search sources & NZB-first](./indexers-and-nzb.md) — where acquire searches and how `find & grab` chooses.
