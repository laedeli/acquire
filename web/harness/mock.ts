// A made-up backlog for the console harness: about 2,500 missing episodes and
// movies across ~120 invented series, some on their way, some failed with a
// reason, and titles the catalog holds without an identity. Every name is
// generated from neutral word lists; none is a real title. Deterministic, so
// screenshots compare.
import { makeApi, type Api, type Candidate, type Counts, type MissingAnswer, type MissingRow, type SeriesRow } from '../src/lib/api'

export type Scenario = 'backlog' | 'empty' | 'unknown' | 'error'

const ADJ = ['Quiet', 'Copper', 'Northern', 'Silver', 'Hollow', 'Amber', 'Distant', 'Paper', 'Iron', 'Glass', 'Velvet', 'Winter', 'Salt', 'Cedar', 'Lunar', 'Signal', 'Hidden', 'Golden', 'Restless', 'Coastal']
const NOUN = ['Harbour', 'Valley', 'Orchard', 'Engine', 'Garden', 'Station', 'Meridian', 'Circuit', 'Archive', 'Lantern', 'Ridge', 'Bureau', 'Atlas', 'Current', 'Quarter', 'Ledger', 'Compass', 'Hearth', 'Frontier', 'Assembly']
const FAIL = [
  'the release was removed by its source before it finished',
  'the download finished but the file did not match the episode',
  'the download client refused the release: the downloads volume keeps a 50 GB reserve and has 31 GB free',
  'par repair failed: 412 blocks missing',
]
// relative to the real clock, rounded to the hour, so "5d ago" and "last asked 3h ago" read true.
const NOW = Math.floor(Date.now() / 3600_000) * 3600_000
const DAY = 86_400_000

function rng(seed: number) {
  let a = seed >>> 0
  return () => {
    a = (a + 0x6d2b79f5) >>> 0
    let t = a
    t = Math.imul(t ^ (t >>> 15), t | 1)
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61)
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}

function names(count: number, offset = 0): string[] {
  const out: string[] = []
  for (let i = offset; out.length < count; i++) {
    const a = ADJ[(i * 7) % ADJ.length]
    const n = NOUN[(i * 3 + Math.floor(i / ADJ.length)) % NOUN.length]
    const name = i % 3 === 0 ? `The ${a} ${n}` : `${a} ${n}`
    if (!out.includes(name)) out.push(name)
  }
  return out
}

const uuid = (r: () => number) =>
  'xxxxxxxx-xxxx-4xxx-8xxx-xxxxxxxxxxxx'.replace(/x/g, () => Math.floor(r() * 16).toString(16))

const iso = (t: number) => new Date(t).toISOString()

function row(r: () => number, title: string, titleId: string, kind: string, season: number | null, episode: number | null, aired: number): MissingRow {
  const x = r()
  const base: MissingRow = {
    targetId: uuid(r),
    titleId,
    title,
    kind,
    season,
    episode,
    airDate: iso(aired),
    state: 'wanted',
    detail: '',
    searchOutcome: '',
    searchDetail: '',
    lastSearchAt: null,
    searchFailures: 0,
    backoffUntil: null,
    grabQueued: false,
    searchable: r() > 0.03,
  }
  const what = season == null ? 'the movie' : `S${String(season).padStart(2, '0')}E${String(episode).padStart(2, '0')}`
  if (x < 0.035) return { ...base, state: 'failed', detail: FAIL[Math.floor(r() * FAIL.length)] }
  if (x < 0.05) return { ...base, state: 'grabbed', detail: `grabbed NZB ${what} from source-a via client-1` }
  if (x < 0.06) return { ...base, state: 'downloaded', detail: 'downloaded; handing it over to the library' }
  if (x < 0.07) return { ...base, state: 'waiting', detail: 'waiting for the catalog to list the file' }
  if (x < 0.075) return { ...base, grabQueued: true }
  const s = r()
  const searched = iso(NOW - Math.floor(r() * 3) * DAY - 3600_000)
  if (s < 0.45) return base
  if (s < 0.75)
    return { ...base, searchOutcome: 'none', searchDetail: 'nothing found', lastSearchAt: searched, searchFailures: 2, backoffUntil: iso(NOW + 6 * 3600_000) }
  if (s < 0.95)
    return { ...base, searchOutcome: 'found', searchDetail: `3 release(s); best ${title.replace(/ /g, '.')}.${what}.1080p.WEB via source-b (score 410)`, lastSearchAt: searched }
  return { ...base, searchOutcome: 'error', searchDetail: 'source-a: context deadline exceeded', lastSearchAt: searched, searchFailures: 1, backoffUntil: iso(NOW + 2 * 3600_000) }
}

export function makeBacklog(seed = 21) {
  const r = rng(seed)
  const series = names(120)
  const rows: MissingRow[] = []
  const seriesRows: SeriesRow[] = []
  // 2,380 episodes over the series, unevenly: a few long backlogs, many short.
  const weights = series.map((_, i) => 1 / (1 + (i % 17)) + r() * 0.2)
  const total = weights.reduce((a, b) => a + b, 0)
  let left = 2380
  series.forEach((title, i) => {
    const titleId = uuid(r)
    const n = i === series.length - 1 ? left : Math.max(1, Math.round((weights[i] / total) * 2380))
    left -= n
    const seasons = 1 + Math.floor(r() * 4)
    const first = 1 + Math.floor(r() * 5)
    // about one series in six is airing now; the rest last aired weeks to years ago.
    const lastAired = NOW - (i % 6 === 0 ? 1 + Math.floor(r() * 6) : 30 + Math.floor(r() * 870)) * DAY
    let remaining = n
    for (let s = 0; s < seasons && remaining > 0; s++) {
      const inSeason = s === seasons - 1 ? remaining : Math.max(1, Math.round(remaining / (seasons - s)))
      // seasons a year apart, episodes a week apart, the last season ending on lastAired.
      const seasonEnd = lastAired - (seasons - 1 - s) * 365 * DAY
      for (let e = 1; e <= inSeason; e++) {
        rows.push(row(r, title, titleId, 'series', first + s, e, seasonEnd - (inSeason - e) * 7 * DAY))
      }
      remaining -= inSeason
    }
    seriesRows.push({
      titleId, tmdbId: 1000 + i, tvdbId: 2000 + i, title, year: 2014 + (i % 12), status: 'continuing', type: 'standard',
      monitored: true, episodes: n + 20, held: 20, missing: n, inFlight: 0, unaired: 0, holdingsKnown: true,
      holdingsAt: iso(NOW - 3 * 3600_000),
    })
  })
  for (let m = 0; m < 120; m++) {
    const title = `${m % 2 ? 'The ' : ''}${ADJ[(m * 11) % ADJ.length]} ${NOUN[(m * 13 + 5) % NOUN.length]} (${2017 + (m % 9)})`
    rows.push(row(r, title, uuid(r), 'movie', null, null, NOW - Math.floor(r() * 900) * DAY))
  }
  rows.sort((a, b) => (b.airDate ?? '').localeCompare(a.airDate ?? ''))
  // titles of unknown holdings: 105, the catalog holds them without an identity.
  const unknown = names(105, 300).map((title, i) => {
    const item = uuid(r)
    const reason = `the catalog holds a title of this name without an identity (item ${item}, "${title}"): what it holds of it cannot be known until that item carries one`
    const s: SeriesRow = {
      titleId: uuid(r), tmdbId: 5000 + i, tvdbId: 6000 + i, title, year: 2010 + (i % 15), status: 'ended', type: 'standard',
      monitored: true, episodes: 10 + (i % 30), held: 0, missing: 0, inFlight: 0, unaired: 0, holdingsKnown: false,
      holdingsAt: null, holdingsError: reason,
    }
    return s
  })
  return { rows, seriesRows: [...seriesRows, ...unknown.slice(0, 92)], unknown, unknownMovies: 13 }
}

/** A console Api whose reads answer from the made-up backlog; writes resolve after a short pause. */
export function mockApi(scenario: Scenario): Api {
  const data = makeBacklog()
  let rows = scenario === 'backlog' ? data.rows : []
  const unknownTitles = scenario === 'empty' ? 0 : scenario === 'unknown' ? 105 : 105
  const reason = data.unknown[0].holdingsError
  const pause = <T,>(v: T, ms = 450) => new Promise<T>((res) => setTimeout(() => res(v), ms))
  const base = makeApi('/', undefined, () => undefined)
  return {
    ...base,
    missing: async (limit = 500) => {
      if (scenario === 'error') throw new Error('HTTP 502: upstream connect error or disconnect/reset before headers')
      const answer: MissingAnswer = {
        missing: rows.slice(0, limit),
        holdings: {
          known: scenario === 'unknown' ? 0 : 240,
          unknown: unknownTitles,
          reason: unknownTitles ? reason : undefined,
          lastAt: scenario === 'unknown' ? null : iso(NOW - 3 * 3600_000),
        },
        autoGrab: false,
      }
      return pause(answer, 120)
    },
    counts: async () => {
      const c: Counts = {
        titles: 345, series: 225, movies: 120, targets: 19020, held: 16388,
        missing: rows.length,
        inFlight: rows.filter((r) => ['grabbed', 'downloaded', 'waiting'].includes(r.state)).length,
        failed: rows.filter((r) => r.state === 'failed').length,
        unaired: scenario === 'backlog' ? 132 : 0,
        inBackoff: rows.filter((r) => r.searchFailures > 0).length,
        holdingsUnknown: unknownTitles,
        unknownTargets: unknownTitles ? 1817 : 0,
      }
      return pause(c, 80)
    },
    // what the series view lists: 120 known series and 92 of the unknown titles (the other 13 are movies).
    series: async () =>
      pause(
        scenario === 'empty'
          ? data.seriesRows.slice(0, 120)
          : scenario === 'unknown'
            ? data.seriesRows.slice(120)
            : data.seriesRows,
        200,
      ),
    grabTargets: async (ids: string[]) => {
      const can = new Set(rows.filter((r) => ids.includes(r.targetId) && (r.state === 'wanted' || r.state === 'failed') && !r.grabQueued).map((r) => r.targetId))
      rows = rows.map((r) => (can.has(r.targetId) ? { ...r, grabQueued: true } : r))
      return pause({ queued: [...can], skipped: ids.length - can.size })
    },
    reconcileHoldings: async () => pause(undefined, 300),
    targetReleases: async (id: string) => {
      const t = rows.find((r) => r.targetId === id)
      const name = (t?.title ?? 'Example').replace(/ /g, '.')
      const c = (q: string, size: number, best: boolean, score: number): Candidate => ({
        title: `${name}.S01E01.${q}.WEB`, indexer: 'source-b', protocol: 'usenet', size, seeders: 0, adapter: 'client-1', source: '',
        reason: best ? 'best by score' : 'lower resolution', best, score, rejected: false, resolution: q, codec: 'h264', sourceType: 'web', covers: 'S01E01', pack: false,
      })
      return pause({ candidates: [c('1080p', 2.1e9, true, 410), c('720p', 1.1e9, false, 220)], incomplete: [] }, 600)
    },
    pickTarget: async () => pause(undefined, 400),
  }
}
