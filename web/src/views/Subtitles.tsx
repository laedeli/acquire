// Subtitles: every title with subtitles in the instance's languages — a
// worklist that opens on what needs a person.
//
// Automatic downloads do the routine work: new arrivals first, then what was
// asked for, then the rest of the library, within the day's download limit.
// So the tab leads with the exceptions: what needs attention, what no source
// has, what waits for a later day. One status per title and language, in
// words — has subtitles, on its way, waiting, not found, needs attention —
// and the line under it says why or what happens next. The source, how a
// subtitle fits the file and the file's name are a row's details: shown on a
// row that needs attention, in the row's menu on the others.
//
// The anatomy is the missing tab's: tabs with counts, a language facet and a
// search, rows grouped by series and season (movies in a group of their
// own), one action menu per row (pick a version, preview, details, remove)
// and one emphasized action for a selection ("find German for 12 selected").
// Setup — the languages, the account, whether it downloads on its own — is in
// settings; with nothing set up and nothing done, the tab says so once, in
// its empty state.
import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'
import {
  ActionMenu,
  Badge,
  Button,
  DataTable,
  DateCell,
  FilterBar,
  IdentifierCell,
  IllustratedMessage,
  MessageStrip,
  Modal,
  SelectionToolbar,
  StatusCell,
  Text,
  Worklist,
  defineStates,
  formatCount,
  toast,
  type DataTableColumn,
  type DataTableGroup,
  type DataTableSort,
  type FilterTab,
  type StateSpec,
} from '@nalet/design-system'
import { Search } from 'lucide-react'
import type { Api, SubtitleItem, SubtitleKey, SubtitleOverview, SubtitleStatus } from '../lib/api'
import { KeepCase } from '../components/Bits'
import { SubtitlePicker } from '../components/SubtitlePicker'
import { ago, fitWords, until } from '../lib/format'

const LIMIT = 5000
const PAGE = 100

// The status vocabulary, written once.
const STATES = defineStates({
  has: { state: 'success', text: 'has subtitles' },
  way: { state: 'information', text: 'on its way' },
  waiting: { state: 'neutral', text: 'waiting' },
  notfound: { state: 'neutral', text: 'not found' },
  attention: { state: 'error', text: 'needs attention' },
})

const TAB_ORDER: SubtitleStatus[] = ['attention', 'notfound', 'waiting', 'way', 'has']

const TAB_LABEL: Record<SubtitleStatus, string> = {
  attention: 'needs attention',
  notfound: 'not found',
  waiting: 'waiting',
  way: 'on its way',
  has: 'with subtitles',
}

// What a group of a tab's rows is, for its header: "12 not found".
const GROUP_NOUN: Record<SubtitleStatus, string> = {
  attention: 'need attention',
  notfound: 'not found',
  waiting: 'waiting',
  way: 'on their way',
  has: 'with subtitles',
}

const statusOf = (r: SubtitleItem): StateSpec => STATES[r.status] ?? STATES.waiting

const key = (r: SubtitleItem) => `${r.itemId}|${r.language}`

function coords(r: SubtitleItem) {
  if (r.kind === 'movie') return r.title || 'a movie'
  return `S${String(r.season).padStart(2, '0')}E${String(r.episode).padStart(2, '0')}`
}

/** How a row is named on its own: "The Quiet Harbour · S04E07 · German". */
const rowName = (r: SubtitleItem) =>
  (r.kind === 'movie' ? [r.title, r.languageName] : [r.title, coords(r), r.languageName]).join(' · ')

const groupOf = (r: SubtitleItem) => (r.kind === 'movie' ? 'movies' : `${r.titleId}:${r.season}`)

/**
 * What "find" does something for: a row not searched yet, not found, or one
 * that needs attention — not one that has subtitles, is asked for already, or
 * has a match that only waits for a later day's downloads.
 */
const findable = (r: SubtitleItem) =>
  !r.asked && (r.status === 'attention' || r.status === 'notfound' || (r.status === 'waiting' && !r.found))

/** The quiet word beside a language: how the subtitle there is, when it says more than the language. */
function languageNote(r: SubtitleItem): string {
  if (r.status !== 'has') return ''
  if (r.hasKind === 'ai') return 'AI'
  if (r.hasKind === 'traditional') return 'Traditional'
  return ''
}

/** What a row's details say: the source and the release, how it fits, the source's or the catalog's words, the original, when it was added. */
function detailLines(r: SubtitleItem): string[] {
  const lines: string[] = []
  const f = r.found
  if (f) {
    lines.push(`OpenSubtitles.com · ${f.release || f.fileName}`)
    lines.push(
      `${fitWords(f.match)}${f.hashMatch ? ' (its fingerprint)' : ''} · ${formatCount(f.downloads)} downloads${
        f.votes > 0 ? ` · rated ${f.rating.toFixed(1)}` : ''
      }${f.hearingImpaired ? ' · hearing impaired' : ''}${f.traditional ? ' · Traditional' : ''}`,
    )
    if (f.fileName && f.fileName !== f.release) lines.push(`file: ${f.fileName}`)
  } else if (r.release) {
    lines.push(`OpenSubtitles.com · ${r.release}`)
    lines.push(fitWords(r.match))
  }
  if (r.why) lines.push(r.why)
  if (r.original) lines.push(`the original: ${r.original}`)
  if (r.attachedAt) lines.push(`added ${ago(r.attachedAt)}`)
  return lines
}

/** "German 92% · Chinese 12%": how many titles have each language. */
function coverageLine(o: SubtitleOverview | null): string {
  if (!o || !o.languages.some((l) => l.total > 0)) return ''
  return o.languages.map((l) => `${l.name} ${l.total ? Math.floor((100 * l.has) / l.total) : 0}%`).join(' · ')
}

/**
 * "9 downloads left today": what the account has left today, the one number the console shows and what a pick takes
 * from; nothing while it is not known for today, rather than a guess.
 */
function budgetLine(o: SubtitleOverview | null): string {
  if (!o || !o.configured || !o.enabled) return ''
  const n = o.budget.accountLeft
  if (n == null) return ''
  if (n <= 0) return 'no downloads left today'
  return `${formatCount(n)} ${n === 1 ? 'download' : 'downloads'} left today`
}

/** What the header's details say: the automatic downloads, the account, the setting the languages come from. */
function headerDetails(o: SubtitleOverview | null): string[] {
  if (!o) return []
  const out: string[] = []
  if (o.configured && o.enabled) {
    const b = o.budget
    out.push(`automatic downloads: ${formatCount(b.used)} of ${formatCount(b.daily)} used today`)
    if (b.account?.known && b.accountLeft != null) {
      const renewed = b.account.resetAt ? until(b.account.resetAt) : ''
      out.push(
        `the OpenSubtitles.com account: ${formatCount(b.accountLeft)} of ${formatCount(b.account.allowed)} downloads left today${
          renewed && renewed !== 'now' ? ` · renewed ${renewed}` : ''
        }`,
      )
    }
  }
  if (o.from?.setting) {
    out.push(
      o.from.scope === 'deployment' ? `languages: set for acquire (${o.from.setting})` : `languages: set for the whole platform (${o.from.setting})`,
    )
  }
  return out
}

/** The one language rows share, "" when they have several. */
function oneLanguage(rows: SubtitleItem[]): string {
  const langs = new Set(rows.map((r) => r.languageName))
  return langs.size === 1 ? [...langs][0] : ''
}

/**
 * "find German for 12 selected", the language in its case; "find subtitles for
 * 12 selected". One span around the whole text: design system 0.3.0 lays out a
 * button's children as flex items, which would drop the spaces around the
 * name. TODO(design system 0.4.0): its labels keep the spaces; the span goes.
 */
function findLabel(rows: SubtitleItem[], n: number): ReactNode {
  const lang = oneLanguage(rows)
  if (!lang) return `find subtitles for ${formatCount(n)} selected`
  return (
    <span>
      find <KeepCase>{lang}</KeepCase> for {formatCount(n)} selected
    </span>
  )
}

/** What a toast says was asked for: "German for 12 selected". */
const findWhat = (rows: SubtitleItem[], n: number) => `${oneLanguage(rows) || 'subtitles'} for ${formatCount(n)} selected`

/** Where the tab opens: a tab, a language, a search, and the picker on the first row (for review). */
export type SubtitlesStart = { tab?: SubtitleStatus; language?: string; q?: string; pick?: boolean }

export function Subtitles({
  api,
  admin,
  tick,
  onNavigate,
  start,
}: {
  api: Api
  admin: boolean
  tick: number
  onNavigate?: (tab: string) => void
  start?: SubtitlesStart
}) {
  const [overview, setOverview] = useState<SubtitleOverview | null>(null)
  const [rows, setRows] = useState<SubtitleItem[]>([])
  const [tab, setTab] = useState<SubtitleStatus>(start?.tab ?? 'attention')
  const [language, setLanguage] = useState(start?.language ?? '')
  const [q, setQ] = useState(start?.q ?? '')
  const [pickFirst, setPickFirst] = useState(!!start?.pick)
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [sort, setSort] = useState<DataTableSort | null>(null)
  const [busy, setBusy] = useState('')
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState('')
  const [actionError, setActionError] = useState<{ what: string; detail: string } | null>(null)
  const [picking, setPicking] = useState<SubtitleItem | null>(null)
  const [previewing, setPreviewing] = useState<{ row: SubtitleItem; text: string | null } | null>(null)
  const [detailing, setDetailing] = useState<SubtitleItem | null>(null)

  const load = useCallback(async () => {
    try {
      const [o, list] = await Promise.all([api.subtitleOverview(language), api.subtitles(tab, language, '', LIMIT)])
      setOverview(o)
      setRows(list)
      setLoadError('')
      setSelected((prev) => {
        const can = new Set(list.filter(findable).map(key))
        return new Set([...prev].filter((k) => can.has(k)))
      })
    } catch (e) {
      setLoadError(String((e as Error).message || e))
    } finally {
      setLoading(false)
    }
  }, [api, tab, language])

  useEffect(() => {
    setLoading(true)
    void load()
    const t = setInterval(() => void load(), 30000)
    return () => clearInterval(t)
  }, [load, tick])

  useEffect(() => {
    if (pickFirst && rows.length) {
      setPicking(rows[0])
      setPickFirst(false)
    }
  }, [pickFirst, rows])

  async function act(k: string, what: string, fn: () => Promise<void>) {
    setBusy(k)
    setActionError(null)
    try {
      await fn()
      await load()
    } catch (e) {
      setActionError({ what, detail: String((e as Error).message || e) })
    } finally {
      setBusy('')
    }
  }

  const find = (keys: SubtitleKey[], k: string, what: string) =>
    act(k, 'Couldn’t queue them.', async () => {
      const r = await api.findSubtitles(keys)
      setSelected(new Set())
      toast(
        `${what}: ${formatCount(r.queued.length)} queued${r.skipped ? ` · ${formatCount(r.skipped)} have subtitles already` : ''}`,
        { state: r.queued.length ? 'success' : 'neutral' },
      )
    })

  const remove = (r: SubtitleItem) =>
    act(key(r), `Couldn’t remove the ${r.languageName} subtitle.`, async () => {
      await api.removeSubtitle(r.itemId, r.language)
      toast(`Removed ${rowName(r)}. acquire searches for another in a day.`, { state: 'information' })
    })

  const preview = (r: SubtitleItem) => {
    setPreviewing({ row: r, text: null })
    api
      .subtitlePreview(r.itemId, r.language)
      .then((text) => setPreviewing({ row: r, text }))
      .catch((e) => setPreviewing({ row: r, text: `Couldn’t load it: ${String((e as Error).message || e)}` }))
  }

  // ---- what the search keeps --------------------------------------------
  const filtered = useMemo(() => {
    const needle = q.trim().toLowerCase().replace(/\s+/g, '')
    if (!needle) return rows
    return rows.filter((r) => r.title.toLowerCase().replace(/\s+/g, '').includes(needle) || coords(r).toLowerCase() === needle)
  }, [rows, q])
  const inView = useMemo(() => new Set(filtered.map(key)), [filtered])
  const selectedInView = useMemo(() => new Set([...selected].filter((k) => inView.has(k))), [selected, inView])
  const findableInView = useMemo(() => filtered.filter(findable), [filtered])
  const selectedRows = useMemo(() => filtered.filter((r) => selectedInView.has(key(r))), [filtered, selectedInView])
  const filtersActive = !!(q.trim() || language)
  const clearFilters = () => {
    setQ('')
    setLanguage('')
  }

  const columns = useMemo<DataTableColumn<SubtitleItem>[]>(
    () => [
      {
        id: 'item',
        header: 'episode',
        cell: (r) => <IdentifierCell title={coords(r)} text={r.kind === 'movie' && r.year > 0 ? String(r.year) : undefined} />,
        sortValue: (r) => `${r.title} ${coords(r)}`,
      },
      {
        id: 'language',
        header: 'language',
        cell: (r) => {
          const note = languageNote(r)
          return (
            <span>
              {r.languageName}
              {note ? <span className="acq__quiet">{` · ${note}`}</span> : null}
            </span>
          )
        },
        sortValue: (r) => r.languageName,
      },
      {
        id: 'state',
        header: 'state',
        cell: (r) => {
          // a row that needs attention shows its details; the others keep them in the row's menu
          const lines = r.status === 'attention' ? detailLines(r) : []
          return (
            <div>
              <StatusCell {...statusOf(r)} detail={r.detail} />
              {lines.length > 0 && (
                <details className="acq__details">
                  <summary>details</summary>
                  {lines.map((l, i) => (
                    <span key={i} className="acq__sub">
                      {l}
                    </span>
                  ))}
                </details>
              )}
            </div>
          )
        },
        sortValue: (r) => TAB_ORDER.indexOf(r.status),
      },
      {
        id: 'aired',
        header: 'aired',
        cell: (r) => <DateCell value={r.airedAt} />,
        sortValue: (r) => r.airedAt,
        firstSortDirection: 'descending',
        importance: 'low',
      },
      {
        id: 'actions',
        header: <span className="acq__sr">actions</span>,
        align: 'right' as const,
        width: 44,
        cell: (r: SubtitleItem) => (
          <ActionMenu
            label={`actions for ${rowName(r)}`}
            loading={busy === key(r)}
            items={[
              ...(admin ? [{ id: 'pick', label: 'pick a version', onSelect: () => setPicking(r) }] : []),
              { id: 'preview', label: 'preview', disabled: !r.preview, onSelect: () => preview(r) },
              ...(r.status === 'attention'
                ? []
                : [{ id: 'details', label: 'details', disabled: detailLines(r).length === 0, onSelect: () => setDetailing(r) }]),
              ...(admin
                ? [{ id: 'remove', label: 'remove', danger: true, disabled: !r.removable, onSelect: () => void remove(r) }]
                : []),
            ]}
          />
        ),
      },
    ],
    [admin, busy],
  )

  const groupHeader = (g: DataTableGroup<SubtitleItem>) => {
    const first = g.rows[0]
    const n = formatCount(g.rows.length)
    return g.key === 'movies'
      ? `movies · ${n} ${GROUP_NOUN[tab]}`
      : `${first.title} · S${String(first.season).padStart(2, '0')} · ${n} ${GROUP_NOUN[tab]}`
  }
  const groupAction = (g: DataTableGroup<SubtitleItem>) => {
    if (!admin || tab === 'has' || tab === 'way') return null
    const can = g.rows.filter(findable)
    if (!can.length) return null
    return (
      <Button
        size="sm"
        variant="ghost"
        loading={busy === `group:${g.key}`}
        leading={<Search size={14} aria-hidden />}
        onClick={() => void find(can.map((r) => ({ itemId: r.itemId, language: r.language })), `group:${g.key}`, groupHeader(g))}
      >
        {`find all ${formatCount(can.length)}`}
      </Button>
    )
  }

  // ---- the parts of the worklist -----------------------------------------
  const counts = overview?.counts ?? {}
  const known = !!overview
  // Nothing set up and nothing done yet: the empty state alone says so — no
  // banner over it, no tabs of zeros.
  const unset = !!overview && (!overview.enabled || !overview.configured)
  const setupOnly = unset && TAB_ORDER.every((id) => !(counts[id] ?? 0))
  const tabs: FilterTab[] = TAB_ORDER.map((id) => ({
    id,
    label: TAB_LABEL[id],
    count: known ? (counts[id] ?? 0) : undefined,
    state: id === 'attention' && (counts.attention ?? 0) > 0 ? 'error' : id === 'way' && (counts.way ?? 0) > 0 ? 'information' : undefined,
  }))
  const settingsButton = onNavigate ? (
    <Button size="sm" variant="ghost" onClick={() => onNavigate('settings')}>
      settings
    </Button>
  ) : undefined

  const strips: ReactNode[] = []
  if (overview && !setupOnly) {
    if (!overview.enabled) {
      strips.push(
        <MessageStrip key="off" state="information" actions={settingsButton}>
          Subtitles are switched off in acquire’s installation, so nothing new is searched for.
        </MessageStrip>,
      )
    } else if (!overview.configured) {
      strips.push(
        <MessageStrip
          key="setup"
          state="warning"
          details={`missing in acquire’s installation: subtitles.opensubtitles.${(overview.missing ?? []).join(', ')}`}
          actions={settingsButton}
        >
          OpenSubtitles.com isn’t set up, so nothing is downloaded. acquire needs an account and an API key, added in its installation.
        </MessageStrip>,
      )
    } else if (overview.problem) {
      strips.push(
        <MessageStrip key="problem" state="error" details={overview.problem}>
          Subtitles can’t be looked for right now.
        </MessageStrip>,
      )
    } else if (!overview.settings.auto) {
      strips.push(
        <MessageStrip key="manual" state="information" actions={settingsButton}>
          Automatic downloads are off. Only what you find or pick is downloaded.
        </MessageStrip>,
      )
    }
  }
  if (loadError && rows.length) {
    strips.push(
      <MessageStrip
        key="load"
        state="warning"
        details={loadError}
        actions={
          <Button size="sm" variant="ghost" onClick={() => void load()}>
            try again
          </Button>
        }
      >
        Couldn’t refresh the list. It shows what was loaded before.
      </MessageStrip>,
    )
  }
  if (actionError) {
    strips.push(
      <MessageStrip key="action" state="error" details={actionError.detail} onDismiss={() => setActionError(null)}>
        {actionError.what}
      </MessageStrip>,
    )
  }

  const EMPTY: Record<SubtitleStatus, { title: string; description: string }> = {
    attention: {
      title: 'nothing needs attention',
      description:
        overview && !overview.settings.auto
          ? 'Automatic downloads are off, so subtitles are downloaded when you find or pick them. Titles that no source has are under “not found”; found ones wait for you under “waiting”.'
          : 'Subtitles are downloaded on their own. Titles that no source has are under “not found”; found ones that wait for a later day are under “waiting”.',
    },
    notfound: { title: 'every search found one', description: 'Every title searched so far has a match in each language.' },
    waiting: { title: 'nothing waiting', description: 'Everything found so far is downloaded or on its way.' },
    way: { title: 'nothing on its way', description: 'Nothing is downloading right now.' },
    has: { title: 'no subtitles yet', description: 'Titles show up here, one row per language, once they have a subtitle.' },
  }

  let body: ReactNode
  if (loading && !rows.length && !loadError) {
    body = (
      <DataTable<SubtitleItem>
        aria-label="titles and their subtitles"
        columns={columns}
        rows={[]}
        rowKey={key}
        loading
        empty={<p className="acq__note">loading…</p>}
      />
    )
  } else if (!rows.length && loadError) {
    body = (
      <IllustratedMessage
        illustration="error"
        title="couldn't load the subtitles"
        description="The list couldn’t be read. It fills in once it can."
        headingLevel={3}
        actions={
          <>
            <Button onClick={() => void load()}>try again</Button>
            <details className="acq__details">
              <summary>details</summary>
              <span className="acq__sub">{loadError}</span>
            </details>
          </>
        }
      />
    )
  } else if (setupOnly && overview) {
    body = (
      <IllustratedMessage
        illustration="unknown"
        title={overview.enabled ? "subtitles aren't set up" : 'subtitles are off'}
        description={
          overview.enabled
            ? 'To download subtitles, acquire needs an OpenSubtitles.com account and an API key. Both are added in acquire’s installation; settings shows which are missing.'
            : 'Subtitles are switched off in acquire’s installation, so nothing is searched for.'
        }
        headingLevel={3}
        actions={onNavigate ? <Button onClick={() => onNavigate('settings')}>open settings</Button> : undefined}
      />
    )
  } else if (!filtered.length) {
    body = filtersActive && rows.length ? (
      <IllustratedMessage
        illustration="search"
        title="no matches"
        description="Nothing in this list fits the search and the language."
        headingLevel={3}
        actions={<Button onClick={clearFilters}>clear filters</Button>}
      />
    ) : (
      <IllustratedMessage illustration="done" title={EMPTY[tab].title} description={EMPTY[tab].description} headingLevel={3} />
    )
  } else {
    body = (
      <DataTable<SubtitleItem>
        aria-label="titles and their subtitles"
        columns={columns}
        rows={filtered}
        rowKey={key}
        rowLabel={rowName}
        sort={sort}
        onSortChange={setSort}
        selectable={admin && tab !== 'has'}
        isRowSelectable={findable}
        selected={selectedInView}
        onSelectionChange={setSelected}
        groupBy={groupOf}
        groupHeader={groupHeader}
        groupAction={admin ? groupAction : undefined}
        rowHighlight={(r) => (r.status === 'attention' ? 'error' : undefined)}
        growing={PAGE}
        growingResetKey={`${tab}|${language}|${q}`}
      />
    )
  }

  const listShown = rows.length > 0
  const canSelectAll = admin && selectedInView.size > 0 && selectedInView.size < findableInView.length
  const languageOptions = [{ value: '', label: 'all languages' }, ...(overview?.languages ?? []).map((l) => ({ value: l.tag, label: l.name }))]
  // the subtitle line says what the tab is, in the body face; the mono line holds data alone (the coverage names the languages)
  const subtitle = 'Subtitles for the library’s movies and episodes, from OpenSubtitles.com.'
  const facts = [coverageLine(overview), budgetLine(overview)].filter(Boolean).join(' · ')
  const more = headerDetails(overview)

  return (
    <div className="acq__view">
      <Worklist
        title="subtitles"
        subtitle={subtitle}
        headingLevel={2}
        headerInfo={
          <>
            {facts && <span>{facts}</span>}
            {rows.length >= LIMIT && (counts[tab] ?? 0) > rows.length && (
              <span>{`showing the first ${formatCount(rows.length)} of ${formatCount(counts[tab] ?? 0)}, in download order`}</span>
            )}
            {more.length > 0 && (
              <details className="acq__details acq__details--info">
                <summary>details</summary>
                {more.map((l) => (
                  <span key={l} className="acq__sub">
                    {l}
                  </span>
                ))}
              </details>
            )}
          </>
        }
        tabs={setupOnly ? undefined : tabs}
        activeTab={tab}
        onTabChange={(id) => {
          setTab(id as SubtitleStatus)
          setSelected(new Set())
        }}
        tabsLabel="filter subtitles by state"
        messages={strips.length ? strips : undefined}
        filterBar={
          listShown || filtersActive ? (
            <FilterBar
              search={{ value: q, onChange: setQ, placeholder: 'search titles or S04E07', label: 'search titles' }}
              filters={[
                { type: 'select', id: 'language', label: 'language', value: language, onChange: setLanguage, options: languageOptions },
              ]}
              resultCount={filtersActive ? `${formatCount(filtered.length)} of ${formatCount(rows.length)}` : undefined}
              onClearAll={clearFilters}
            />
          ) : undefined
        }
        toolbar={
          listShown && filtered.length > 0 ? (
            <SelectionToolbar
              label="titles and their subtitles"
              heading={`${formatCount(filtered.length)} ${GROUP_NOUN[tab]}`}
              selectedCount={selectedInView.size}
              onClearSelection={() => setSelected(new Set())}
              selectAll={
                canSelectAll
                  ? {
                      label: `select all ${formatCount(findableInView.length)}`,
                      onSelect: () => setSelected(new Set(findableInView.map(key))),
                    }
                  : null
              }
              primaryAction={(n) =>
                admin && n > 0
                  ? {
                      // design system 0.3.0 types the label as a string and renders it as the
                      // button's children: the node keeps the language's case
                      label: findLabel(selectedRows, n) as string,
                      icon: <Search size={14} aria-hidden />,
                      loading: busy === 'bulk',
                      onSelect: () =>
                        void find(
                          selectedRows.map((r) => ({ itemId: r.itemId, language: r.language })),
                          'bulk',
                          findWhat(selectedRows, n),
                        ),
                    }
                  : null
              }
            />
          ) : undefined
        }
      >
        {body}
      </Worklist>
      {picking && (
        <SubtitlePicker
          api={api}
          row={picking}
          name={rowName(picking)}
          onClose={() => setPicking(null)}
          onPicked={() => {
            toast(`Added ${rowName(picking)}.`, { state: 'success' })
            void load()
          }}
        />
      )}
      {previewing && (
        <Modal
          open
          onClose={() => setPreviewing(null)}
          width={720}
          title={
            <span>
              preview · <KeepCase>{rowName(previewing.row)}</KeepCase>
            </span>
          }
        >
          {previewing.text == null ? (
            <Text variant="muted">Loading…</Text>
          ) : previewing.text ? (
            <pre className="acq__preview">{previewing.text}</pre>
          ) : (
            <Text variant="muted">Nothing to preview.</Text>
          )}
          {previewing.row.release && (
            <p className="acq__note">
              <Badge tone="neutral">
                <KeepCase>OpenSubtitles.com</KeepCase>
              </Badge>{' '}
              {previewing.row.release} · {fitWords(previewing.row.match)}
            </p>
          )}
        </Modal>
      )}
      {detailing && (
        <Modal
          open
          onClose={() => setDetailing(null)}
          width={640}
          title={
            <span>
              details · <KeepCase>{rowName(detailing)}</KeepCase>
            </span>
          }
        >
          <StatusCell {...statusOf(detailing)} detail={detailing.detail} />
          <div className="acq__lines">
            {detailLines(detailing).map((l, i) => (
              <span key={i} className="acq__sub">
                {l}
              </span>
            ))}
          </div>
        </Modal>
      )}
    </div>
  )
}
