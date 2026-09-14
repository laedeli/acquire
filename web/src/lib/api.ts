// The acquire REST surface. Every call carries the signed-in user's bearer;
// a 401 means the session lapsed, which the auth layer handles by re-login.

export interface Config {
  oidcIssuer: string
  oidcClientId: string
  adminRole: string
  autoGrab: boolean
}

export interface Wanted {
  id: string
  tmdbId: number
  mediaType: string
  title: string
  year: number
  posterUrl: string
  requestedBy: string
  requestedAt: string
  status: string
  detail: string
  itemId: string
  updatedAt: string
}

export interface Download {
  adapter: string
  clientJobId: string
  wantedId: string
  title: string
  state: string
  nativeState: string
  progressPct: number
  bytesDone: number
  bytesTotal: number
  speedBps: number
  etaSec: number | null
  seeders: number | null
  leechers: number | null
  health: number | null
  error: string
  startedAt: string
  updatedAt: string
  finishedAt: string | null
}

export interface ClientStatus {
  id?: string
  type?: string
  name: string
  reachable: boolean
  error?: string
  down_bps: number
  up_bps: number
  paused: boolean
  free_disk_bytes?: number
  detail?: Record<string, string>
}

export interface Candidate {
  title: string
  indexer: string
  protocol: string
  size: number
  seeders: number
  adapter: string
  /** The release link with its credentials removed — for display only. */
  source: string
  reason: string
  best: boolean
  score: number
  rejected: boolean
  resolution: string
  codec: string
  sourceType: string
  indexerId?: number
  guid?: string
  /** Opaque reference a grab sends back; the server resolves the real link. */
  release?: string
}

/** A search's answer: what arrived, and which sources did not answer. */
export interface SearchResult {
  candidates: Candidate[]
  incomplete: string[]
}

export interface DiscoverHit {
  tmdbId: number
  mediaType: string
  title: string
  year: number
  posterUrl: string
  overview: string
  inLibrary: boolean
}


export interface QualityProfile {
  id: string
  name: string
  isDefault: boolean
  updatedAt?: string
  config: {
    preferProtocol: string
    resolutions: string[]
    preferredCodecs: string[]
    rejectTerms: string[]
    minSizeMb: number
    maxSizeMb: number
    minSeeders: number
    preferHdr: boolean
  }
}

// ── configuration ───────────────────────────────────────────────────────────

/** A secret is write-only: the API only says whether one is stored. */
export interface SecretState {
  set: boolean
  updatedAt: string | null
}

export interface DownloadClient {
  id: string
  type: string
  baseUrl: string
  auth: 'basic' | 'token' | 'none'
  username: string
  secret: SecretState
  protocols: string[]
  category: string
  remotePath: string
  localPath: string
  priority: number
  enabled: boolean
  revision: number
  createdAt: string
  updatedAt: string
  status?: ClientStatus
  applyError?: string
}

export interface SyncState {
  configured: boolean
  checked: boolean
  reachable: boolean
  configApi: boolean
  revision: number
  gatewayRevision: number
  applied: string[] | null
  errors: { id: string; message: string }[] | null
  lastError?: string
}

export interface ClientList {
  revision: number
  keyConfigured: boolean
  sync: SyncState
  clients: DownloadClient[]
}

export interface ClientType {
  type: string
  protocols: string[]
  auth: string[]
  acceptsPayload: boolean
  supportsSavePath: boolean
  canPause: boolean
}

/**
 * What the console writes. secret: omit to keep the stored one, a string to
 * replace it, {clear:true} to remove it.
 */
export interface ClientInput {
  id?: string
  type?: string
  baseUrl: string
  auth: string
  username: string
  secret?: string | { clear: true }
  protocols: string[]
  category: string
  remotePath: string
  localPath: string
  priority: number
  enabled: boolean
}

export interface ClientWriteResult extends DownloadClient {
  sync: { applied: boolean; error?: string }
}

export interface ClientTestResult {
  reachable: boolean
  version: string
  error: string
  status?: ClientStatus
}

// ── search sources ──────────────────────────────────────────────────────────

export interface Categories {
  movie: number[]
  tv: number[]
}

export interface CapsCategory {
  id: number
  name: string
  subcats?: CapsCategory[]
}

export interface SearchMode {
  available: boolean
  params: string[]
}

export interface Caps {
  server: { title?: string; version?: string }
  limits: { max?: number; default?: number }
  search: SearchMode
  tvSearch: SearchMode
  movieSearch: SearchMode
  categories: CapsCategory[]
}

export interface Source {
  id: number
  name: string
  protocol: 'usenet' | 'torrent'
  baseUrl: string
  apiPath: string
  apiKey: SecretState
  categories: Categories
  priority: number
  enabled: boolean
  queryLimitDay: number | null
  grabLimitDay: number | null
  /** Spent today (UTC). */
  usage: { queries: number; grabs: number }
  caps: Caps | null
  capsAt: string | null
  health: {
    state: 'ok' | 'backoff' | 'limit' | 'failing' | 'disabled' | 'key'
    detail?: string
    failures: number
    backoffUntil: string | null
    lastError: string
  }
  revision: number
  createdAt: string
  updatedAt: string
}

export interface SourceList {
  keyConfigured: boolean
  sources: Source[]
}

/**
 * What the console writes. apiKey: omit to keep the stored one, a string to
 * replace it, {clear:true} to remove it. A limit of null means no limit.
 */
export interface SourceInput {
  id?: number
  name: string
  protocol: string
  baseUrl: string
  apiPath: string
  apiKey?: string | { clear: true }
  categories: Categories
  priority: number
  enabled: boolean
  queryLimitDay: number | null
  grabLimitDay: number | null
}

export interface SourceWriteResult extends Source {
  capsError?: string
}

export interface SourceTestResult {
  ok: boolean
  error?: string
  items: number
  caps?: Caps
  capsError?: string
}

export interface SearchSettings {
  preferProtocol: 'usenet' | 'torrent'
  storageFloorGb: number
  maxConcurrentGrabs: number
  revision: number
  updatedAt?: string
}

export interface SetupSection {
  key: string
  state: 'ready' | 'needs-setup' | 'degraded'
  summary: string
}

export interface SetupStatus {
  state: 'ready' | 'needs-setup' | 'degraded'
  sections: SetupSection[]
}

export interface FieldError {
  entity: string
  id: string
  field: string
  message: string
}

// Where the API lives depends on how the console is running: standalone it sits
// next to the SPA, embedded it is reached through the portal's proxy. The host
// tells us, so nothing here assumes a mount point.
export function defaultApiBase(): string {
  return window.location.pathname.replace(/\/[^/]*$/, '/')
}

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
    /** Per-field problems from a 422, keyed by field name. */
    public fields: Record<string, string> = {},
  ) {
    super(message)
  }
}

export function makeApi(base: string, token: string | undefined, onUnauthorized: () => void) {
  async function call<T>(path: string, init: RequestInit = {}): Promise<T> {
    const res = await fetch(base + 'api/' + path, {
      ...init,
      headers: {
        ...(init.headers || {}),
        ...(token ? { Authorization: 'Bearer ' + token } : {}),
        ...(init.body ? { 'Content-Type': 'application/json' } : {}),
      },
    })
    if (res.status === 401) {
      onUnauthorized()
      throw new ApiError(401, 'session expired')
    }
    if (res.status === 422) {
      // Validation: map the field list onto the form rather than showing JSON.
      const body = (await res.json().catch(() => ({}))) as { fieldErrors?: FieldError[] }
      const fields: Record<string, string> = {}
      for (const f of body.fieldErrors ?? []) fields[f.field] = fields[f.field] || f.message
      const first = body.fieldErrors?.[0]
      throw new ApiError(422, first ? `${first.field}: ${first.message}` : 'invalid', fields)
    }
    if (!res.ok) throw new ApiError(res.status, (await res.text()) || res.statusText)
    if (res.status === 204) return undefined as T
    return (await res.json()) as T
  }

  const ifMatch = (revision: number) => ({ 'If-Match': String(revision) })

  return {
    wanted: () => call<Wanted[]>('wanted'),
    request: (hit: DiscoverHit) =>
      call<Wanted>('wanted', {
        method: 'POST',
        body: JSON.stringify({
          tmdbId: hit.tmdbId,
          mediaType: hit.mediaType,
          title: hit.title,
          year: hit.year,
          posterUrl: hit.posterUrl,
        }),
      }),
    remove: (id: string) => call<void>('wanted/' + encodeURIComponent(id), { method: 'DELETE' }),
    autograb: (id: string) =>
      call<unknown>('wanted/' + encodeURIComponent(id) + '/autograb', { method: 'POST' }),
    // No client named: the server routes by what the source turns out to be.
    grabMagnet: (id: string, source: string) =>
      call<unknown>('wanted/' + encodeURIComponent(id) + '/grab', {
        method: 'POST',
        body: JSON.stringify({ source }),
      }),
    releases: (id: string) => call<SearchResult>('wanted/' + encodeURIComponent(id) + '/releases'),
    pick: (id: string, c: Candidate) =>
      call<unknown>('wanted/' + encodeURIComponent(id) + '/pick', {
        method: 'POST',
        body: JSON.stringify(c),
      }),
    discover: (q: string) => call<DiscoverHit[]>('discover?q=' + encodeURIComponent(q)),
    downloads: () => call<Download[]>('downloads'),
    clients: () => call<ClientStatus[]>('clients'),
    search: (q: string, indexerIds: number[] = []) =>
      call<SearchResult>(
        'search?q=' +
          encodeURIComponent(q) +
          (indexerIds.length ? '&indexers=' + indexerIds.join(',') : ''),
      ),
    grabFound: (c: Candidate, opts: { wantedId?: string; title?: string }) =>
      call<unknown>('search/grab', {
        method: 'POST',
        body: JSON.stringify({ ...c, wantedId: opts.wantedId || '', title2: opts.title || '' }),
      }),
    profiles: () => call<QualityProfile[]>('profiles'),
    saveProfile: (p: QualityProfile) =>
      call<QualityProfile>('profiles/' + encodeURIComponent(p.id), {
        method: 'PUT',
        body: JSON.stringify(p),
      }),
    deleteProfile: (id: string) =>
      call<void>('profiles/' + encodeURIComponent(id), { method: 'DELETE' }),
    control: (adapter: string, jobId: string, action: 'pause' | 'resume' | 'cancel') =>
      call<unknown>(
        `downloads/${encodeURIComponent(adapter)}/${encodeURIComponent(jobId)}/${action}`,
        { method: 'POST' },
      ),
    config: () => call<Config>('config'),
    // Configuration (admin). Writes send the revision they were based on.
    setup: () => call<SetupStatus>('setup'),
    downloadClients: () => call<ClientList>('download-clients'),
    clientTypes: () => call<ClientType[]>('download-clients/types'),
    createClient: (c: ClientInput) =>
      call<ClientWriteResult>('download-clients', { method: 'POST', body: JSON.stringify(c) }),
    updateClient: (id: string, c: ClientInput, revision: number) =>
      call<ClientWriteResult>('download-clients/' + encodeURIComponent(id), {
        method: 'PUT',
        headers: ifMatch(revision),
        body: JSON.stringify(c),
      }),
    deleteClient: (id: string, revision: number) =>
      call<{ sync: { applied: boolean; error?: string } }>('download-clients/' + encodeURIComponent(id), {
        method: 'DELETE',
        headers: ifMatch(revision),
      }),
    testClient: (c: ClientInput) =>
      call<ClientTestResult>('download-clients/test', { method: 'POST', body: JSON.stringify(c) }),
    sources: () => call<SourceList>('indexers'),
    createSource: (src: SourceInput) =>
      call<SourceWriteResult>('indexers', { method: 'POST', body: JSON.stringify(src) }),
    updateSource: (id: number, src: SourceInput, revision: number) =>
      call<SourceWriteResult>('indexers/' + id, {
        method: 'PUT',
        headers: ifMatch(revision),
        body: JSON.stringify(src),
      }),
    deleteSource: (id: number, revision: number) =>
      call<void>('indexers/' + id, { method: 'DELETE', headers: ifMatch(revision) }),
    /** Test an unsaved source (or an edit; with id and no key the stored key is used). */
    testSource: (src: SourceInput) =>
      call<SourceTestResult>('indexers/test', { method: 'POST', body: JSON.stringify(src) }),
    /** Test a saved source as stored; the outcome becomes its health. */
    testStoredSource: (id: number) => call<SourceTestResult>('indexers/' + id + '/test', { method: 'POST' }),
    searchSettings: () => call<SearchSettings>('settings/search'),
    saveSearchSettings: (s: SearchSettings) =>
      call<SearchSettings>('settings/search', {
        method: 'PUT',
        headers: ifMatch(s.revision),
        body: JSON.stringify({
          preferProtocol: s.preferProtocol,
          storageFloorGb: s.storageFloorGb,
          maxConcurrentGrabs: s.maxConcurrentGrabs,
        }),
      }),
    // The WANT model's read side. `missing` is the backlog: monitored, aired,
    // still wanted — including rows in search backoff, flagged rather than
    // hidden, because a backlog view that omits everything failing is the least
    // useful version of itself.
    missing: (limit = 500) =>
      call<{ missing: MissingRow[] }>('missing?limit=' + limit).then((r) => r.missing ?? []),
    counts: () => call<Counts>('counts'),
    series: () => call<{ series: SeriesRow[] }>('series').then((r) => r.series ?? []),
  }
}

// MissingRow is one thing we want and do not have.
export type MissingRow = {
  targetId: string
  title: string
  kind: string
  season: number | null
  episode: number | null
  airDate: string | null
  searchFailures: number
  backoffUntil: string | null
  // False when the title carries no id an indexer will accept. Zero of 70
  // indexers accept a tmdbId, so such a row can only be searched as free text
  // and needs to say so rather than look like an ordinary miss.
  searchable: boolean
}

// SeriesRow is one tracked series with its acquisition progress.
export type SeriesRow = {
  titleId: string
  tmdbId: number
  tvdbId: number
  title: string
  year: number
  status: string
  type: string
  monitored: boolean
  episodes: number
  held: number
  missing: number
  unaired: number
}

export type Counts = {
  titles: number
  series: number
  movies: number
  targets: number
  held: number
  missing: number
  unaired: number
  inBackoff: number
}

export type Api = ReturnType<typeof makeApi>
