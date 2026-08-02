# Architecture

acquire is designed around one principle: **the platform core stays neutral, and
acquisition is an addon that plugs into two seams.** Nothing in the core mentions
requests or downloads; the addon is the only thing that does. This keeps the core
honest (it's a catalog + player for a library you own) and makes the whole
acquisition capability something you can add or remove cleanly.

## The two seams

The core exposes exactly two extension points. The addon uses both; the core
depends on neither.

### Seam 1 — the UI extension registry (a native button, zero coupling)

The portal keeps a tiny `ui_extensions` registry table. Each row says “in **this
slot**, show a button with **this label** that goes to **this URL**.” Product
clients read their slots and render whatever they find as a **native** button —
so the core ships the *socket*, and the addon ships the *plug*.

- The registry lives in **portal-api** (`ui_extensions` table, migration `006`).
  CRUD is gated to an admin **or** an addon service account (the `zaentrum-addon`
  realm role, `PORTAL_ADDON_ROLE`); the per-slot read (`GET /api/portal/slots/{slot}`)
  is open to any signed-in user.
- **chino-web** renders the slot. Its search page mounts
  `<ExtensionSlot slot="search.empty">` — shown only when a search returns no
  titles and no people. In the core, that slot is empty, so **nothing renders**.
- **chino-api** forwards the browser's bearer to portal-api best-effort
  (`GET /api/v1/extensions?slot=`); an unreachable or unset portal simply yields
  an empty slot.

When acquire is installed, its deploy registers one row:

```json
{ "key": "acquire.search-request", "addon": "acquire", "slot": "search.empty",
  "kind": "link", "label": "Request this", "icon": "download",
  "url": "https://<host>/acquire/?q={q}", "ord": 10, "enabled": true }
```

Now “no results for _X_” grows a **Request this** button. Delete the row (or
uninstall the addon) and the button is gone — the core never knew its name.

### Seam 2 — the neutral ingest contract (a file becomes a catalog item)

katalog-manager exposes `POST /api/ingest`: hand it an absolute path to a file
that already lives on the platform's storage, plus a title/type, and it creates a
catalog item + primary playback asset and **emits `catalog.item.discovered`** —
exactly what the scanner does for files it finds on its own. It “knows nothing of
where the file came from.”

- The path must sit under the media root **or** the packages root (guarded).
- It's **idempotent on the path**: re-ingesting a known file returns the existing
  item and fires nothing (no duplicate, no re-pipeline).
- `discovered` is the pipeline's front door — enrich → analyze → transcode →
  package all follow from it.

This is the seam that lets an addon put content into the catalog **without**
reaching into the catalog's internals. acquire owns one invariant here: it ingests
the finished download's video file *in place* (no staging copy), and lets the
pipeline take it from `discovered` onward.

## Event-driven by design

acquire is **consume-only** on the event bus. It never emits a platform event;
it reacts to them and issues commands at the edges (request, grab, ingest) over
plain HTTP. The bus carries the truth; HTTP carries the intent.

```mermaid
sequenceDiagram
    autonumber
    actor U as User
    participant W as chino-web
    participant A as acquire
    participant PR as indexer aggregator
    participant G as download-gateway
    participant DC as download client
    participant KM as katalog-manager
    participant PL as pipeline

    U->>W: search "Some Title" (no results)
    W-->>U: native "Request this" (search.empty slot)
    U->>A: POST /api/wanted  (request)
    Note over A: status = pending
    U->>A: find & grab (admin)
    A->>PR: search indexers (NZB-first)
    A->>G: POST /api/v1/downloads {adapter, source, wanted_item_id}
    G->>DC: add (torrent / NZB)
    Note over A: status = downloading
    DC-->>G: (poll) completed
    G-->>A: kafka download.client.completed
    A->>KM: POST /api/ingest {path, title, ...}
    KM-->>A: {itemId, created}
    KM-->>PL: kafka catalog.item.discovered
    Note over A: status = packaging
    PL-->>A: kafka catalog.item.packaged
    Note over A: status = fulfilled → plays in chino
```

**Topics** (all carry a tenant prefix, e.g. `zaentrum-beta.`):

| Topic | Producer | acquire |
|---|---|---|
| `download.client.started` / `.progress` | download-gateway | ignored |
| `download.client.completed` | download-gateway | **consumed** → ingest |
| `download.client.failed` | download-gateway | **consumed** → mark failed |
| `catalog.item.discovered` | katalog-manager (on ingest) | — (drives the pipeline) |
| `catalog.item.packaged` | katalog-manager (pipeline) | **consumed** → mark fulfilled |

acquire's consumer starts at the **latest** offset (only new events; history is
not replayed) and is poison-safe (undecodable messages are skipped).

## Component map

```mermaid
flowchart TB
    subgraph addon["acquire addon — laedeli"]
        direction TB
        ACQ["acquire<br/><i>requests · brain · SPA · indexer search</i>"]
        GW["download-gateway<br/><i>neutral client facade + events</i>"]
        QB["qBittorrent<br/><i>torrents</i>"]
        NZ["NZBGet<br/><i>usenet</i>"]
        PRW["the indexer aggregator<br/><i>indexer aggregator (search only)</i>"]
    end
    subgraph coreapp["zaentrum core"]
        POR["portal-api<br/><i>ui_extensions registry</i>"]
        WEB["chino-web / chino-api<br/><i>extension slot</i>"]
        KMG["katalog-manager<br/><i>/api/ingest + pipeline</i>"]
    end
    BUS[["shared Kafka (mTLS, tenant-prefixed)"]]

    ACQ -->|"POST /api/portal/extensions (register)"| POR
    WEB -->|"read slot"| POR
    ACQ -->|"grab"| GW
    ACQ -->|"search"| PRW
    GW --> QB & NZ
    GW -->|"download.client.*"| BUS
    BUS -->|"completed / failed"| ACQ
    ACQ -->|"POST /api/ingest"| KMG
    KMG -->|"discovered / packaged"| BUS
    BUS -->|"packaged"| ACQ
```

## The neutral-core property, restated

Every seam degrades to nothing:

- **Zero registry rows** → `ExtensionSlot` renders `null`; an unreachable portal →
  chino-api returns `[]`. No button, no trace.
- **Re-ingest of a known path** → no new item, no event. Idempotent.
- **No download clients / no indexer configured** → the gateway simply registers
  no adapters, and auto-grab reports “not configured.” The service stays up.

That's what makes acquisition an *addon* and not a fork: install it for the whole
capability, remove it for a clean, neutral platform.

## Next

- [The acquire service](./acquire.md) — the request lifecycle, API, SPA and schema.
- [Download gateway & clients](./download-gateway.md) — the download plane.
- [Indexer search & NZB-first](./indexers-and-nzb.md) — how auto-grab picks a release.
- [Deploying the addon](./deploying.md) — installing it on a platform.
