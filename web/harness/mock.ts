// A made-up backlog for the console harness: about 2,500 missing episodes and
// movies across ~120 invented series, some on their way, some failed with a
// reason, and titles the catalog holds without an identity. Every name is
// generated from neutral word lists; none is a real title. Deterministic, so
// screenshots compare.
import {
  makeApi,
  type Api,
  type Candidate,
  type Counts,
  type MissingAnswer,
  type MissingRow,
  type QualityProfile,
  type RequestSettings,
  type SearchSettings,
  type SeriesRow,
  type SubtitleCandidate,
  type SubtitleItem,
  type SubtitleKey,
  type SubtitleLanguage,
  type SubtitleOverview,
  type SubtitleSettings,
  type SubtitleStatus,
} from '../src/lib/api'

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
        autoGrabNew: scenario === 'backlog',
        newWithinDays: 14,
        airDelayHours: 30,
        airTimes: 'tvmaze',
        airDelayAfterAirstampMinutes: 60,
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

// ---- subtitles ---------------------------------------------------------------
// A made-up library for the subtitles tab: invented series and movies in
// English, German and Chinese, most English had, German partly, Chinese
// little; a few that need attention, many not found or waiting for the day's
// downloads. Deterministic, like the backlog.

// unset: the library's rows, and the source's API key missing since
export type SubtitleScenario = 'library' | 'calm' | 'off' | 'unset' | 'error'

// the source's name, as the server gives it (opensubtitles.Name)
const SOURCE_NAME = 'OpenSubtitles.com'

const SUB_LANGS: SubtitleLanguage[] = [
  { tag: 'en', name: 'English', has: 0, total: 0 },
  { tag: 'de', name: 'German', has: 0, total: 0 },
  { tag: 'zh', name: 'Chinese', has: 0, total: 0 },
]

// a wait in the server's words: "in 6h", "in 3 days"
const inWords = (ms: number) => (ms < 48 * 3600_000 ? `in ${Math.round(ms / 3600_000)}h` : `in ${Math.round(ms / DAY)} days`)

// what the server records as a failure: the file's own (never taken again;
// the next search takes the next best), or a catalog that took it and does
// not list it. A source or a catalog that does not answer fails nothing.
// Each says what went wrong in plain words (the row's line) and in the
// source's or the catalog's own (its details), and what the next try does:
// another version (the failing file is blocked), none left yet, or the same.
const ATTENTION = [
  { why: 'the catalog did not take it: the subtitle holds no cue (HTTP 422)', trouble: 'the catalog refused it', retry: 'another' },
  { why: 'the download was refused: OpenSubtitles: file not found (HTTP 404)', trouble: 'the source refused the file', retry: 'none' },
  { why: 'the subtitle file is no text acquire can read', trouble: 'can’t read the file', retry: 'another' },
  { why: 'the catalog took it, and does not list it', trouble: 'the catalog doesn’t list it', retry: 'same' },
] as const

// a failed want's line, as the server writes it
const failedLine = (a: (typeof ATTENTION)[number], next: number) =>
  a.retry === 'another'
    ? `${a.trouble} · tries another version ${inWords(next - NOW)}`
    : a.retry === 'none'
      ? `${a.trouble} · no other version yet · again ${inWords(next - NOW)}`
      : `${a.trouble} · again ${inWords(next - NOW)}`

/** Chinese as the languages name it: of no script (zh), or Simplified (zh-Hans). */
export type SubtitleChinese = 'zh' | 'zh-Hans'

/** The languages in use: English, German and Chinese of no script, or Simplified. */
const subtitleLanguages = (chinese: SubtitleChinese): SubtitleLanguage[] =>
  SUB_LANGS.map((l) => (l.tag === 'zh' && chinese === 'zh-Hans' ? { ...l, tag: 'zh-Hans', name: 'Chinese (Simplified)' } : l))

function subtitleRows(scenario: SubtitleScenario, chinese: SubtitleChinese): SubtitleItem[] {
  const r = rng(77)
  const out: SubtitleItem[] = []
  const series = names(48, 40)
  const add = (base: Omit<SubtitleItem, 'language' | 'languageName' | 'status' | 'detail' | 'nextAt' | 'removable' | 'preview' | 'asked'>) => {
    const at = base.kind === 'movie' ? String(base.year) : `S${String(base.season).padStart(2, '0')}E${String(base.episode).padStart(2, '0')}`
    for (const l of subtitleLanguages(chinese)) {
      const x = r()
      // English is mostly the title's own; German half; Chinese little
      const hasP = l.tag === 'en' ? 0.92 : l.tag === 'de' ? 0.41 : 0.12
      let row: SubtitleItem
      if (x < hasP) {
        const attached = r() < 0.35 && l.tag !== 'en'
        const kind = l.tag.startsWith('zh') && r() < 0.3 ? 'traditional' : attached ? 'attached' : 'own'
        if (kind === 'traditional' && l.tag === 'zh-Hans') {
          // one in Traditional characters doesn't count for Simplified: the language stays open, its row says what it has
          const next = NOW + (3 + (base.episode % 20)) * DAY
          out.push(
            attached
              ? { ...base, language: l.tag, languageName: l.name, status: 'notfound', nextAt: iso(next), asked: false, removable: false,
                  preview: false, detail: `nothing found · has one in Traditional characters · again ${inWords(next - NOW)}`,
                  why: 'nothing in this language' }
              : { ...base, language: l.tag, languageName: l.name, status: 'waiting', nextAt: null, asked: false, removable: false,
                  preview: false, detail: 'not searched yet · has one in Traditional characters' },
          )
          continue
        }
        row = {
          ...base, language: l.tag, languageName: l.name, status: 'has', nextAt: null, asked: false,
          hasKind: kind, removable: attached, preview: attached,
          detail: kind === 'traditional'
            ? `${attached ? 'added by acquire' : 'came with the file'} · in Traditional characters`
            : attached ? 'added by acquire · matches this file' : 'came with the file',
          release: attached ? `${base.title.replace(/ /g, '.')}.${at}.1080p.WEB-DL-GRP` : undefined,
          match: attached ? 'file' : undefined, attachedAt: attached ? iso(NOW - Math.floor(r() * 20) * DAY) : null,
        }
      } else {
        const y = r()
        if (y < (scenario === 'calm' ? 0 : 0.006)) {
          const a = ATTENTION[Math.floor(r() * ATTENTION.length)]
          const next = NOW + (a.retry === 'none' ? 6 * DAY : 5 * 3600_000)
          row = { ...base, language: l.tag, languageName: l.name, status: 'attention', nextAt: iso(next), asked: false,
            removable: false, preview: false, detail: failedLine(a, next), why: a.why,
            found: candidate(base.title, at, l.tag, 'release', true, r) }
        } else if (y < 0.45) {
          const machine = r() < 0.2
          row = { ...base, language: l.tag, languageName: l.name, status: 'notfound', nextAt: iso(NOW + (1 + Math.floor(r() * 27)) * DAY),
            asked: false, removable: false, preview: false, detail: '', why: machine ? 'only 2 machine-translated — never taken on their own' : 'nothing in this language' }
          row.detail = `${machine ? 'only machine translations' : 'nothing found'} · again ${inWords(new Date(row.nextAt!).getTime() - NOW)}`
        } else if (y < 0.47) {
          row = { ...base, language: l.tag, languageName: l.name, status: 'way', nextAt: null, asked: r() < 0.3, removable: false, preview: false,
            detail: "found · in today's downloads", found: candidate(base.title, at, l.tag, 'file', true, r), why: '3 found; the best made for this file' }
        } else {
          const searched = r() < 0.6
          const fit = (['file', 'release', 'unknown'] as const)[Math.floor(r() * 3)]
          row = { ...base, language: l.tag, languageName: l.name, status: 'waiting', nextAt: null, asked: false, removable: false, preview: false,
            detail: 'not searched yet',
            found: searched ? candidate(base.title, at, l.tag, fit, true, r) : null,
            why: searched ? `2 found; the best ${{ file: 'made for this file', release: 'made for this release', unknown: 'may be out of sync' }[fit]}` : undefined }
        }
      }
      out.push(row)
    }
  }
  series.forEach((title, i) => {
    const titleId = `series-${i}`
    const seasons = 1 + (i % 3)
    for (let s = 1; s <= seasons; s++) {
      const eps = 6 + Math.floor(r() * 7)
      for (let e = 1; e <= eps; e++) {
        add({ itemId: uuid(r), kind: 'episode', title, titleId, year: 2012 + (i % 13), season: s, episode: e,
          airedAt: iso(NOW - ((seasons - s) * 365 + (eps - e) * 7 + (i % 40) * 30) * DAY) })
      }
    }
  })
  for (let m = 0; m < 60; m++) {
    const title = `${m % 2 ? 'The ' : ''}${ADJ[(m * 7 + 3) % ADJ.length]} ${NOUN[(m * 11 + 2) % NOUN.length]}`
    add({ itemId: uuid(r), kind: 'movie', title, titleId: '', year: 2008 + (m % 17), season: 0, episode: 0, airedAt: iso(NOW - m * 41 * DAY) })
  }
  // the matches waiting for a later day, in the order the work spends its
  // downloads (the newest first), 18 a day
  out
    .filter((x) => x.status === 'waiting' && x.found)
    .sort((a, b) => (b.airedAt ?? '').localeCompare(a.airedAt ?? ''))
    .forEach((x, i) => {
      const days = 1 + Math.floor(i / 18)
      x.detail = `found · waits for the daily download limit (about ${days} ${days === 1 ? 'day' : 'days'})`
    })
  return out
}

/** A version the source has: of the title's own episode (at: S01E08), or of the movie's year. */
function candidate(title: string, at: string, lang: string, match: 'file' | 'release' | 'unknown', best: boolean, r: () => number): SubtitleCandidate {
  const name = title.replace(/ /g, '.')
  const code = lang.startsWith('zh') ? 'zh-cn' : lang
  return {
    source: SOURCE_NAME,
    subtitleId: String(Math.floor(r() * 1e7)), fileId: Math.floor(r() * 1e7), files: 1, fileName: `${name}.${at}.${code}.srt`, language: code,
    release: `${name}.${at}.1080p.WEB-DL.DDP5.1.H.264-GRP`, downloads: Math.floor(r() * 40000), rating: 6 + r() * 4, votes: Math.floor(r() * 30),
    hearingImpaired: false, foreignPartsOnly: false, aiTranslated: false, machineTranslated: false, fromTrusted: r() < 0.4,
    hashMatch: match === 'file', uploaded: iso(NOW - Math.floor(r() * 900) * DAY), want: lang, tag: lang.startsWith('zh') ? 'zh-Hans' : lang,
    traditional: false, match, similarity: match === 'unknown' ? 0.2 : 0.9, score: 1000, best,
  }
}

/** The subtitles tab's Api on the made-up library; writes resolve after a short pause. */
export function mockSubtitlesApi(scenario: SubtitleScenario, chinese: SubtitleChinese = 'zh'): Api {
  const base = mockApi('backlog')
  let rows = scenario === 'off' ? [] : subtitleRows(scenario, chinese)
  const pause = <T,>(v: T, ms = 300) => new Promise<T>((res) => setTimeout(() => res(v), ms))
  // the day's downloads: the automatic work used 11 of its 18 today; the
  // account (used elsewhere too) has 9 of its 20 left — the one number the
  // console shows, which a pick takes from and the automatic work stops at
  let picked = 0
  const budget = () => {
    const remaining = 9 - picked
    return { daily: 18, used: 11, left: Math.max(0, Math.min(18 - 11, remaining)), manual: picked, accountLeft: remaining,
      account: { known: true, allowed: 20, remaining, resetAt: iso(NOW + 9 * 3600_000), at: iso(NOW) } }
  }
  const overview = (language: string): SubtitleOverview => {
    const langs = subtitleLanguages(chinese).map((l) => {
      const of = rows.filter((x) => x.language === l.tag)
      return { ...l, has: of.filter((x) => x.status === 'has').length, total: of.length }
    })
    const counts: Partial<Record<SubtitleStatus, number>> = {}
    for (const x of rows) if (!language || x.language === language) counts[x.status] = (counts[x.status] ?? 0) + 1
    return {
      source: SOURCE_NAME, enabled: true, configured: scenario !== 'off' && scenario !== 'unset',
      missing: scenario === 'off' ? ['username', 'password', 'apiKey'] : scenario === 'unset' ? ['apiKey'] : [],
      problem: scenario === 'error' ? 'OpenSubtitles refused the sign-in (HTTP 401): check the username and password' : undefined,
      languages: langs, from: { scope: 'platform', setting: 'packager.language_whitelist' },
      budget: budget(),
      settings: { auto: true, allowMachineTranslated: false, revision: 1 }, counts,
    }
  }
  return {
    ...base,
    subtitleOverview: async (language = '') => pause(overview(language), 80),
    subtitles: async (tab, language = '') =>
      pause(rows.filter((x) => (!tab || x.status === tab) && (!language || x.language === language)), 150),
    // what the source has for the row's own episode (or the movie's year):
    // the server leaves out what is made for another episode
    subtitleSearch: async (itemId: string, language: string) => {
      const row = rows.find((x) => x.itemId === itemId && x.language === language)
      const title = row?.title ?? 'Example'
      const r = rng(5)
      const name = title.replace(/ /g, '.')
      const at = !row ? '2019' : row.kind === 'movie' ? String(row.year) : `S${String(row.season).padStart(2, '0')}E${String(row.episode).padStart(2, '0')}`
      const c = (release: string, match: 'file' | 'release' | 'unknown', downloads: number, extra: Partial<SubtitleCandidate> = {}): SubtitleCandidate => ({
        ...candidate(title, at, language, match, false, r), release, fileName: `${release}.srt`, downloads, ...extra,
      })
      const list: SubtitleCandidate[] = [
        c(`${name}.${at}.1080p.WEB-DL.DDP5.1.H.264-GRP`, 'file', 2210, { best: true, votes: 12, rating: 8.7 }),
        c(`${name}.${at}.1080p.WEB-DL.H.264-OTHER`, 'release', 15400, { votes: 40, rating: 7.9 }),
        c(`${name}.${at}.720p.HDTV.x264-TV`, 'unknown', 38000, { hearingImpaired: true, votes: 3, rating: 6.1 }),
        c(`${name}.${at}.1080p.WEB-DL-GRP`, 'file', 120, { machineTranslated: true, excluded: 'machine-translated' }),
        c(`${name}.${at}.CD1`, 'unknown', 900, { files: 2, excluded: 'in 2 files' }),
      ]
      if (language === 'zh') list.push(c(`${name}.${at}.BluRay`, 'unknown', 640, { language: 'zh-tw', tag: 'zh-Hant', traditional: true }))
      return pause({ source: SOURCE_NAME, itemId, title: row ? `${row.title} ${row.kind === 'movie' ? '' : at}`.trim() : title,
        language, languageName: row?.languageName ?? language, hash: true, release: `${name}.${at}.1080p.WEB-DL.DDP5.1.H.264-GRP`,
        candidates: list, left: budget().accountLeft, quota: budget().account }, 500)
    },
    pickSubtitle: async (itemId: string, language: string) => {
      picked++
      rows = rows.map((x) => (x.itemId === itemId && x.language === language ? { ...x, status: 'has', detail: 'added by acquire · matches this file', removable: true, preview: true, hasKind: 'attached' } : x))
      return pause(undefined, 400)
    },
    removeSubtitle: async (itemId: string, language: string) => {
      rows = rows.map((x) => (x.itemId === itemId && x.language === language ? { ...x, status: 'waiting', detail: 'removed · again in 24h', removable: false, preview: false } : x))
      return pause(undefined, 300)
    },
    subtitlePreview: async () =>
      pause('00:00:04  Where were you last night?\n00:00:06  At the harbour. / The lights were out again.\n00:00:11  Again? That makes three times this week.\n00:00:15  Someone should tell the council.', 200),
    findSubtitles: async (keys: SubtitleKey[]) => {
      const set = new Set(keys.map((k) => `${k.itemId}|${k.language}`))
      let n = 0
      rows = rows.map((x) => {
        if (!set.has(`${x.itemId}|${x.language}`) || x.status === 'has') return x
        n++
        return { ...x, status: 'way', asked: true, detail: 'asked for · searched next' }
      })
      return pause({ queued: keys.slice(0, n), skipped: keys.length - n })
    },
    subtitleSettings: async () => pause({ auto: true, allowMachineTranslated: false, revision: 1 }),
    // the rest of the settings view, so it renders whole
    searchSettings: async () =>
      pause<SearchSettings>({ preferProtocol: 'usenet', storageFloorGb: 50, maxConcurrentGrabs: 3, autoGrab: false,
        autoGrabNew: true, newWithinDays: 14, airDelayHours: 30, airTimes: 'tvmaze', airDelayAfterAirstampMinutes: 60, revision: 1 }),
    requestSettings: async () => pause<RequestSettings>({ autoApprove: false, revision: 1 }),
    saveRequestSettings: async (s: RequestSettings) => pause({ ...s, revision: s.revision + 1 }),
    profiles: async () =>
      pause<QualityProfile[]>([{ id: 'standard', name: 'standard', isDefault: true, config: { preferProtocol: 'usenet',
        resolutions: ['1080p', '720p'], preferredCodecs: ['h264', 'hevc'], rejectTerms: [], minSizeMb: 0, maxSizeMb: 0, minSeeders: 1,
        preferHdr: false } }]),
    saveSubtitleSettings: async (s: SubtitleSettings) => pause({ ...s, revision: s.revision + 1 }),
  }
}
