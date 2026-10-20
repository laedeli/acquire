// The acquire REST surface. Every call carries the signed-in user's bearer;
// a 401 means the session lapsed, which the portal shell handles by signing
// the user in again.

// What the console reads from /api/config: the realm role that makes a user an
// admin, and whether "find & grab" has a source and a client to work with.
export interface Config {
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
  /** How a target's typed search found it (id | coords | text) and what matched. */
  stage?: string
  matchedVia?: string
  /** For a target's search: the episodes the release holds (S04E07, S04E07-E08, S01) and whether it is a season pack. */
  covers?: string
  pack?: boolean
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
  /** Run by the addon itself and configured by its deployment: read-only here. */
  managed: boolean
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
  /** The backlog sweep grabs what it finds missing (off: it only searches). */
  autoGrab: boolean
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
        // The server replaces the whole document: every field goes, or the
        // ones left out read as their defaults.
        body: JSON.stringify({
          preferProtocol: s.preferProtocol,
          storageFloorGb: s.storageFloorGb,
          maxConcurrentGrabs: s.maxConcurrentGrabs,
          autoGrab: s.autoGrab,
        }),
      }),
    // The WANT model's read side. `missing` is the backlog: monitored, aired,
    // not held, of titles whose holdings are known — including rows in search
    // backoff, flagged rather than hidden, because a backlog view that omits
    // everything failing is the least useful version of itself.
    missing: (limit = 500) =>
      call<MissingAnswer>('missing?limit=' + limit).then((r) => ({ ...r, missing: r.missing ?? [] })),
    counts: () => call<Counts>('counts'),
    series: () => call<{ series: SeriesRow[] }>('series').then((r) => r.series ?? []),
    // Grabs bound to targets (admin): queued per episode or for a selection,
    // and a pick among one target's typed search results.
    grabTargets: (targetIds: string[]) =>
      call<{ queued: string[]; skipped: number }>('targets/grab', {
        method: 'POST',
        body: JSON.stringify({ targetIds }),
      }),
    targetReleases: (id: string) =>
      call<{ candidates: Candidate[] | null }>('targets/' + encodeURIComponent(id) + '/search').then(
        (r): SearchResult => ({ candidates: r.candidates ?? [], incomplete: [] }),
      ),
    pickTarget: (id: string, c: Candidate) =>
      call<unknown>('targets/' + encodeURIComponent(id) + '/pick', { method: 'POST', body: JSON.stringify(c) }),
    reconcileHoldings: () => call<unknown>('holdings/reconcile', { method: 'POST' }),
    // Subtitles in the instance's languages: the tab's header and list (any
    // signed-in user), a search, a pick, a removal and a selection put first
    // in line (admin), and their settings (admin).
    subtitleOverview: (language = '') =>
      call<SubtitleOverview>('subtitles/overview' + (language ? '?language=' + encodeURIComponent(language) : '')),
    subtitles: (tab: SubtitleStatus | '', language = '', q = '', limit = 5000) =>
      call<{ items: SubtitleItem[] | null }>(
        'subtitles?' + new URLSearchParams({ tab, language, q, limit: String(limit) }).toString(),
      ).then((r) => r.items ?? []),
    subtitleSearch: (itemId: string, language: string) =>
      call<SubtitleSearch>(`subtitles/${encodeURIComponent(itemId)}/${encodeURIComponent(language)}/search`).then(
        (r) => ({ ...r, candidates: r.candidates ?? [] }),
      ),
    pickSubtitle: (itemId: string, language: string, c: SubtitleCandidate) =>
      call<unknown>(`subtitles/${encodeURIComponent(itemId)}/${encodeURIComponent(language)}/pick`, {
        method: 'POST',
        body: JSON.stringify(c),
      }),
    removeSubtitle: (itemId: string, language: string) =>
      call<void>(`subtitles/${encodeURIComponent(itemId)}/${encodeURIComponent(language)}`, { method: 'DELETE' }),
    subtitlePreview: (itemId: string, language: string) =>
      call<{ text: string }>(`subtitles/${encodeURIComponent(itemId)}/${encodeURIComponent(language)}/preview`).then(
        (r) => r.text,
      ),
    findSubtitles: (keys: SubtitleKey[]) =>
      call<{ queued: SubtitleKey[]; skipped: number }>('subtitles/find', { method: 'POST', body: JSON.stringify({ keys }) }),
    subtitleSettings: () => call<SubtitleSettings>('settings/subtitles'),
    saveSubtitleSettings: (s: SubtitleSettings) =>
      call<SubtitleSettings>('settings/subtitles', {
        method: 'PUT',
        headers: ifMatch(s.revision),
        body: JSON.stringify({ auto: s.auto, allowMachineTranslated: s.allowMachineTranslated }),
      }),
  }
}

/** What acquire knows of the library's holdings. */
export type HoldingsSummary = {
  known: number
  unknown: number
  /** Why the unknown titles' holdings are unknown, as most of them say. */
  reason?: string
  lastAt: string | null
}

export type MissingAnswer = {
  missing: MissingRow[]
  holdings: HoldingsSummary
  /** Whether the backlog sweep grabs on its own (search.autoGrab). */
  autoGrab: boolean
}

// MissingRow is one thing we want and do not have, and what is under way for it.
export type MissingRow = {
  targetId: string
  titleId: string
  title: string
  kind: string
  season: number | null
  episode: number | null
  airDate: string | null
  /** wanted | failed, or grabbed | downloaded | waiting while a grab is under way. */
  state: string
  detail: string
  /** The last search: found | none | error, and what it said. */
  searchOutcome: string
  searchDetail: string
  lastSearchAt: string | null
  searchFailures: number
  /** When the sweep looks again after searches that found nothing. */
  backoffUntil: string | null
  /** A grab asked for in this tab waits for the grab worker. */
  grabQueued: boolean
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
  /** Of the missing: grabbed, downloaded or waiting for the catalog. */
  inFlight: number
  unaired: number
  /** False until acquire knows what the library holds of the series. */
  holdingsKnown: boolean
  holdingsAt: string | null
  holdingsError?: string
}

export type Counts = {
  titles: number
  series: number
  movies: number
  targets: number
  held: number
  missing: number
  inFlight: number
  failed: number
  unaired: number
  inBackoff: number
  /** Monitored titles whose holdings are unknown, and their aired targets left out of missing. */
  holdingsUnknown: number
  unknownTargets: number
}

/** has · on its way · waiting · not found · needs attention: one per title and language. */
export type SubtitleStatus = 'has' | 'way' | 'waiting' | 'notfound' | 'attention'

export type SubtitleKey = { itemId: string; language: string }

/** A subtitle a source found, ranked for one of the instance's languages. */
export type SubtitleCandidate = {
  /** The name of the source it was found at. */
  source: string
  subtitleId: string
  fileId: number
  files: number
  fileName: string
  /** The source's code: de, zh-cn, zh-tw. */
  language: string
  release: string
  downloads: number
  rating: number
  votes: number
  hearingImpaired: boolean
  foreignPartsOnly: boolean
  aiTranslated: boolean
  machineTranslated: boolean
  fromTrusted: boolean
  hashMatch: boolean
  uploaded: string
  want: string
  /** What it is attached as: de, zh-Hans, zh-Hant. */
  tag: string
  traditional: boolean
  /** file: made for this very file · release: for a release of its name · unknown: may be out of sync. */
  match: 'file' | 'release' | 'unknown'
  similarity: number
  score: number
  /** Why an automatic choice never takes it. */
  excluded?: string
  best: boolean
}

export type SubtitleItem = {
  itemId: string
  kind: string
  title: string
  titleId: string
  year: number
  season: number
  episode: number
  airedAt: string | null
  language: string
  languageName: string
  status: SubtitleStatus
  /** The line under the status: why, or what happens next. */
  detail: string
  /** The source's or the catalog's own words, for the details. */
  why?: string
  nextAt: string | null
  hasKind?: string
  /** A subtitle acquire attached, which it can remove. */
  removable: boolean
  preview: boolean
  release?: string
  match?: string
  attachedAt?: string | null
  found?: SubtitleCandidate | null
  asked: boolean
  original?: string
}

export type SubtitleLanguage = { tag: string; name: string; has: number; total: number }

export type SubtitleQuota = { known: boolean; allowed: number; remaining: number; resetAt: string; at: string }

export type SubtitleSettings = { auto: boolean; allowMachineTranslated: boolean; revision: number }

export type SubtitleOverview = {
  /** The subtitle source's name, as the console shows it. */
  source: string
  enabled: boolean
  configured: boolean
  missing?: string[]
  problem?: string
  languages: SubtitleLanguage[]
  /** Where the languages are set: for the whole platform, or in acquire's deployment; and the setting that names them. */
  from: { scope: 'platform' | 'deployment' | ''; setting: string }
  /**
   * The day's downloads: the automatic budget (daily), what the automatic work used of it today and has left, the
   * picks a person made today, what the account said, and what the account has left today — the one number the
   * console shows, and what a pick takes from (null while it is not known for today).
   */
  budget: { daily: number; used: number; left: number; manual: number; account?: SubtitleQuota; accountLeft: number | null }
  settings: SubtitleSettings
  counts: Partial<Record<SubtitleStatus, number>>
}

export type SubtitleSearch = {
  /** The name of the source searched. */
  source: string
  itemId: string
  title: string
  language: string
  languageName: string
  hash: boolean
  release?: string
  original?: string
  candidates: SubtitleCandidate[]
  /** The downloads the account has left today, the one number the console shows; using a candidate takes one of them. Null while not known. */
  left: number | null
  quota?: SubtitleQuota
}

export type Api = ReturnType<typeof makeApi>
