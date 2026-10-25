// Missing: what we want and do not have, and what is being done about it — a
// worklist.
//
// Every row is an aired, monitored episode or movie that the library does not
// hold — of a title whose holdings acquire knows. A title whose holdings are
// unknown (never asked of the catalog, or a catalog that cannot answer) has no
// rows here: nothing says its episodes are not on disk. One message strip says
// how many titles that leaves out, and why, in words; the catalog's own wording
// and the item id are behind its "details". The titles themselves are one tab
// away ("can't be checked").
//
// Three deliberate choices, all about not lying by omission:
//   * rows in search backoff, and rows whose grab failed, are SHOWN and say
//     why. A backlog that hides everything currently failing looks healthy
//     while being stuck — failures get their own tab, "needs attention".
//   * a row a grab is under way for stays until the library holds it, saying
//     where the grab is: queued, grabbed, downloaded, waiting for the catalog.
//   * a title with no id an indexer accepts says so on its row, so a row that
//     can only be searched as text does not look like an ordinary miss.
//
// The worklist groups rows by series and season (movies in a group of their
// own), filters them with the tabs, a search and two facets, and grows a
// hundred rows at a time. An admin grabs from here: per row (one action menu:
// pick a release, grab now), per group (grab all), and for a selection — the
// one emphasized action, "grab 8 selected". Grabs are queued and done one at a
// time; a toast says what was queued, the rows follow.
import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'
import {
  ActionMenu,
  Button,
  DataTable,
  DateCell,
  FilterBar,
  IdentifierCell,
  IllustratedMessage,
  MessageStrip,
  STATE_SEVERITY,
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
import { Download, RefreshCw } from 'lucide-react'
import type { Api, Counts, HoldingsSummary, MissingAnswer, MissingRow, SeriesRow } from '../lib/api'
import { ReleasePicker } from '../components/ReleasePicker'
import { ago, until } from '../lib/format'

// The worklist groups, counts and filters in the browser, so it asks for the
// whole backlog — the server's ceiling for one answer (intParam caps it at
// 5000). Past that the header says the list holds only the newest.
const LIMIT = 5000
const PAGE = 100
const DAY = 86_400_000

type Tab = 'missing' | 'way' | 'attention' | 'unaired' | 'unknown'

function coords(r: MissingRow) {
  if (r.season == null || r.episode == null) return 'movie'
  return `S${String(r.season).padStart(2, '0')}E${String(r.episode).padStart(2, '0')}`
}

/** How a row is named on its own: "The Quiet Harbour S04E07", or the movie's title. */
const rowName = (r: MissingRow) => (r.kind === 'movie' ? r.title : `${r.title} ${coords(r)}`)

const grabbable = (r: MissingRow) => (r.state === 'wanted' || r.state === 'failed') && !r.grabQueued

// The status vocabulary of a missing row, written once. A grab asked for in
// this tab (grabQueued) reads as on its way, like one the worker has taken.
const STATES = defineStates({
  missing: { state: 'neutral', text: 'missing' },
  way: { state: 'information', text: 'on its way' },
  attention: { state: 'error', text: 'needs attention' },
  held: { state: 'success', text: 'held' },
})

function statusOf(r: MissingRow): StateSpec {
  if (r.grabQueued) return STATES.way
  switch (r.state) {
    case 'grabbed':
    case 'downloaded':
    case 'waiting':
      return STATES.way
    case 'failed':
      return STATES.attention
    case 'held':
      return STATES.held
  }
  return STATES.missing
}

const onItsWay = (r: MissingRow) => statusOf(r) === STATES.way

/** The line under a row's status: why, or what happens next. Raw search errors stay on hover. */
function detailOf(r: MissingRow): ReactNode {
  if (r.grabQueued) return 'queued for a search and grab'
  switch (r.state) {
    case 'grabbed':
      return r.detail || 'grabbed'
    case 'downloaded':
      return r.detail || 'downloaded'
    case 'waiting':
      return r.detail || 'waiting for the catalog'
    case 'failed':
      return r.detail || 'the last grab failed'
  }
  if (!r.searchable) return 'no id a source accepts — text search only'
  const again = r.backoffUntil ? ` · again ${until(r.backoffUntil)}` : ''
  switch (r.searchOutcome) {
    case 'found':
      return <span title={r.searchDetail || undefined}>found a release</span>
    case 'none':
      return `nothing found${again}`
    case 'error':
      return <span title={r.searchDetail || undefined}>{`search failed${again}`}</span>
  }
  return 'not searched yet'
}

function airedBucket(r: MissingRow): '' | 'week' | 'month' | 'older' {
  if (!r.airDate) return ''
  const days = (Date.now() - new Date(r.airDate).getTime()) / DAY
  return days <= 7 ? 'week' : days <= 30 ? 'month' : 'older'
}

/** A series and season, or the movies: the worklist's groups. */
const groupOf = (r: MissingRow) => (r.kind === 'movie' ? 'movies' : `${r.titleId}:${r.season ?? 0}`)

/**
 * Why the catalog cannot say what the library holds, in words. The catalog's
 * own sentence (with the item id) goes into the strip's details.
 */
function holdingsReason(reason: string | undefined, one = false): string {
  const r = (reason ?? '').toLowerCase()
  const them = one ? 'it' : 'them'
  if (!r) return one ? "it hasn't been asked of the catalog yet" : "they haven't been asked of the catalog yet"
  if (r.includes('without an identity')) return `the catalog holds ${them} without an identity`
  if (r.includes('does not answer holdings')) return "the catalog doesn't answer holdings by identity yet"
  if (r.includes('refused')) return "the catalog refused acquire's sign-in"
  if (/http \d{3}|could not be read|unreachable|timeout|deadline/.test(r)) return "the catalog didn't answer"
  return `the catalog couldn't say what the library holds of ${them}`
}

/** What is grabbed on its own, as the search and grab settings say: the header's line. */
type Policy = Pick<MissingAnswer, 'autoGrab' | 'autoGrabNew' | 'newWithinDays' | 'airDelayHours' | 'airTimes' | 'airDelayAfterAirstampMinutes'>

/** When an automatic grab takes an episode, as the settings have it: ", an hour after it airs (else 30 hours after its air date)". */
function grabbedWhen(p: Policy): string {
  const date = p.airDelayHours ? `${formatCount(p.airDelayHours)} hours after its air date` : 'from its air date on'
  if (p.airTimes === 'off') return p.airDelayHours ? `, ${date}` : ''
  const m = p.airDelayAfterAirstampMinutes ?? 0
  const stamp =
    m === 0 ? 'from the moment it airs' : m === 60 ? 'an hour after it airs' : m % 60 === 0 ? `${m / 60} hours after it airs` : `${m} minutes after it airs`
  return `, ${stamp} (else ${date})`
}

function onItsOwn(p: Policy): string {
  const wait = grabbedWhen(p)
  if (p.autoGrab) return `everything missing is grabbed on its own${wait}, within the limits in settings`
  if (p.autoGrabNew)
    return `episodes that aired in the last ${formatCount(p.newWithinDays || 14)} days and what was requested are grabbed on their own${wait}; the rest is grabbed from here`
  return 'nothing is grabbed on its own — the sweep only searches; settings can switch it on'
}

/** "8 episodes queued", "2 movies queued", "8 episodes and 2 movies queued". */
function queuedLabel(rows: MissingRow[]): string {
  const movies = rows.filter((r) => r.kind === 'movie').length
  const episodes = rows.length - movies
  const part = (n: number, one: string, many: string) => `${formatCount(n)} ${n === 1 ? one : many}`
  if (movies && episodes) return `${part(episodes, 'episode', 'episodes')} and ${part(movies, 'movie', 'movies')} queued`
  if (movies) return `${part(movies, 'movie', 'movies')} queued`
  return `${part(episodes, 'episode', 'episodes')} queued`
}

export function Missing({ api, admin, tick }: { api: Api; admin: boolean; tick: number }) {
  const [rows, setRows] = useState<MissingRow[]>([])
  const [counts, setCounts] = useState<Counts | null>(null)
  const [holdings, setHoldings] = useState<HoldingsSummary | null>(null)
  const [policy, setPolicy] = useState<Policy | null>(null)
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [busy, setBusy] = useState('')
  const [picking, setPicking] = useState<MissingRow | null>(null)
  const [loadError, setLoadError] = useState('')
  const [actionError, setActionError] = useState<{ what: string; detail: string } | null>(null)
  const [loading, setLoading] = useState(true)
  const [loadedAt, setLoadedAt] = useState<string | null>(null)

  const [tab, setTab] = useState<Tab>('missing')
  const [q, setQ] = useState('')
  const [kind, setKind] = useState('')
  const [aired, setAired] = useState('')
  const [sort, setSort] = useState<DataTableSort | null>({ columnId: 'aired', direction: 'descending' })
  const [dismissedUnknown, setDismissedUnknown] = useState<number | null>(null)
  const [unknownTitles, setUnknownTitles] = useState<SeriesRow[] | null>(null)
  const [unknownError, setUnknownError] = useState('')

  const load = useCallback(async () => {
    try {
      const [m, c] = await Promise.all([api.missing(LIMIT), api.counts()])
      setRows(m.missing)
      setHoldings(m.holdings)
      setPolicy({ autoGrab: m.autoGrab, autoGrabNew: !!m.autoGrabNew, newWithinDays: m.newWithinDays, airDelayHours: m.airDelayHours,
        airTimes: m.airTimes, airDelayAfterAirstampMinutes: m.airDelayAfterAirstampMinutes })
      setCounts(c)
      setLoadError('')
      setLoadedAt(new Date().toISOString())
      // A row that can no longer be grabbed leaves the selection.
      setSelected((prev) => {
        const can = new Set(m.missing.filter(grabbable).map((r) => r.targetId))
        return new Set([...prev].filter((id) => can.has(id)))
      })
    } catch (e) {
      setLoadError(String((e as Error).message || e))
    } finally {
      setLoading(false)
    }
  }, [api])

  useEffect(() => {
    void load()
    const t = setInterval(() => void load(), 30000)
    return () => clearInterval(t)
  }, [load, tick])

  async function act(key: string, what: string, fn: () => Promise<void>) {
    setBusy(key)
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

  const grab = (ids: string[], key: string) =>
    act(key, "couldn't queue the grab.", async () => {
      const r = await api.grabTargets(ids)
      setSelected(new Set())
      const queued = new Set(r.queued)
      const label = queuedLabel(rows.filter((x) => queued.has(x.targetId)))
      toast(`${label}${r.skipped ? ` · ${formatCount(r.skipped)} skipped: held already or under way` : ''}`, {
        state: r.queued.length ? 'success' : 'neutral',
      })
    })

  const reconcile = () =>
    act('reconcile', "couldn't ask the catalog.", async () => {
      await api.reconcileHoldings()
      toast('asking the catalog what the library holds — the list follows', { state: 'information' })
    })

  // ---- what the tabs, the search and the facets keep ----------------------
  const unknownCount = holdings?.unknown ?? 0
  useEffect(() => {
    if (tab === 'unknown' && unknownCount === 0) setTab('missing')
  }, [tab, unknownCount])

  // The titles that can't be checked: the series view lists them; read on demand.
  useEffect(() => {
    if (tab !== 'unknown') return
    let live = true
    setUnknownError('')
    api
      .series()
      .then((s) => live && setUnknownTitles(s.filter((x) => x.monitored && !x.holdingsKnown)))
      .catch((e) => live && setUnknownError(String((e as Error).message || e)))
    return () => {
      live = false
    }
  }, [api, tab])

  const inTab = useMemo(
    () => (tab === 'way' ? rows.filter(onItsWay) : tab === 'attention' ? rows.filter((r) => r.state === 'failed') : rows),
    [rows, tab],
  )
  const filtered = useMemo(() => {
    const needle = q.trim().toLowerCase()
    return inTab.filter(
      (r) =>
        (!kind || r.kind === kind) &&
        (!aired || airedBucket(r) === aired) &&
        (!needle || r.title.toLowerCase().includes(needle) || coords(r).toLowerCase().includes(needle)),
    )
  }, [inTab, q, kind, aired])
  const filtersActive = !!(q.trim() || kind || aired)
  const clearFilters = () => {
    setQ('')
    setKind('')
    setAired('')
  }

  // Only what the person can see is selected: a filter that hides a row drops it from the grab.
  const inView = useMemo(() => new Set(filtered.map((r) => r.targetId)), [filtered])
  const selectedInView = useMemo(() => new Set([...selected].filter((id) => inView.has(id))), [selected, inView])
  const grabbableInView = useMemo(() => filtered.filter(grabbable), [filtered])

  const columns = useMemo<DataTableColumn<MissingRow>[]>(
    () => [
      {
        id: 'item',
        header: 'episode',
        cell: (r) => <IdentifierCell title={r.kind === 'movie' ? r.title : coords(r)} />,
        sortValue: (r) => rowName(r),
      },
      {
        id: 'aired',
        header: 'aired',
        cell: (r) => <DateCell value={r.airDate} />,
        sortValue: (r) => r.airDate,
        firstSortDirection: 'descending',
        importance: 'low',
      },
      {
        id: 'state',
        header: 'state',
        cell: (r) => <StatusCell {...statusOf(r)} detail={detailOf(r)} />,
        // most urgent first
        compare: (a, b) => STATE_SEVERITY[statusOf(b).state] - STATE_SEVERITY[statusOf(a).state],
      },
      ...(admin
        ? [
            {
              id: 'actions',
              header: <span className="acq__sr">actions</span>,
              align: 'right' as const,
              width: 44,
              cell: (r: MissingRow) => (
                <ActionMenu
                  label={`actions for ${rowName(r)}`}
                  loading={busy === r.targetId}
                  items={[
                    { id: 'pick', label: 'pick a release', disabled: !grabbable(r), onSelect: () => setPicking(r) },
                    { id: 'grab', label: 'grab now', disabled: !grabbable(r), onSelect: () => void grab([r.targetId], r.targetId) },
                  ]}
                />
              ),
            },
          ]
        : []),
    ],
    // grab closes over rows (for the toast's wording); the columns follow it and busy.
    [admin, busy, rows],
  )

  const noun = tab === 'way' ? 'on their way' : tab === 'attention' ? 'need attention' : 'missing'
  const groupHeader = (g: DataTableGroup<MissingRow>) => {
    const first = g.rows[0]
    const n = formatCount(g.rows.length)
    return g.key === 'movies' ? `movies · ${n} ${noun}` : `${first.title} · S${String(first.season ?? 0).padStart(2, '0')} · ${n} ${noun}`
  }
  const groupAction = (g: DataTableGroup<MissingRow>) => {
    if (!admin) return null
    const can = g.rows.filter(grabbable)
    if (!can.length) return null
    return (
      <Button
        size="sm"
        variant="ghost"
        loading={busy === `group:${g.key}`}
        leading={<Download size={14} aria-hidden />}
        onClick={() => void grab(can.map((r) => r.targetId), `group:${g.key}`)}
      >
        {`grab all ${formatCount(can.length)}`}
      </Button>
    )
  }

  // ---- the parts of the worklist -----------------------------------------
  // no counts until the backlog has loaded: "0 missing" after a failed load would be a lie.
  const known = rows.length > 0 || (!loading && !loadError)
  const wayCount = rows.filter(onItsWay).length
  const failedCount = rows.filter((r) => r.state === 'failed').length
  const tabs: FilterTab[] = [
    { id: 'missing', label: 'missing', count: known ? rows.length : undefined },
    { id: 'way', label: 'on their way', count: known ? wayCount : undefined, state: wayCount ? 'information' : undefined },
    {
      id: 'attention',
      label: 'needs attention',
      count: known ? failedCount : undefined,
      state: failedCount ? 'error' : undefined,
    },
    { id: 'unaired', label: 'not yet aired', count: counts?.unaired },
    ...(unknownCount > 0 ? [{ id: 'unknown', label: "can't be checked", count: unknownCount, state: 'warning' as const }] : []),
  ]

  const strips: ReactNode[] = []
  // a failed refresh over rows already shown is a warning strip; a failed first
  // load, with nothing to show, is the error message in the list's place.
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
        {`couldn't refresh the list; it shows what was loaded ${ago(loadedAt)}.`}
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
  if (holdings && unknownCount > 0 && tab !== 'unknown' && dismissedUnknown !== unknownCount) {
    const left = counts?.unknownTargets ?? 0
    strips.push(
      <MessageStrip
        key="unknown"
        state="warning"
        onDismiss={() => setDismissedUnknown(unknownCount)}
        actions={
          <>
            <Button size="sm" variant="ghost" onClick={() => setTab('unknown')}>
              show them
            </Button>
            {admin && (
              <Button
                size="sm"
                variant="ghost"
                loading={busy === 'reconcile'}
                leading={<RefreshCw size={14} aria-hidden />}
                onClick={() => void reconcile()}
              >
                ask the catalog again
              </Button>
            )}
          </>
        }
        details={
          holdings.reason
            ? `what most of them say: ${holdings.reason}`
            : 'acquire has not asked the catalog about them yet.'
        }
      >
        {`${formatCount(unknownCount)} ${unknownCount === 1 ? "title can't" : "titles can't"} be checked: ${holdingsReason(holdings.reason)}.`}
        {left > 0 ? ` ${formatCount(left)} of their aired episodes are not in this list.` : ''}
      </MessageStrip>,
    )
  }

  let body: ReactNode
  if (tab === 'unaired') {
    body = (
      <IllustratedMessage
        illustration="empty"
        title={`${formatCount(counts?.unaired ?? 0)} not yet aired`}
        description="episodes that haven't aired and requested movies that aren't out yet aren't missing. each joins this list on the day it comes out."
        headingLevel={3}
      />
    )
  } else if (tab === 'unknown') {
    const listed = unknownTitles?.length ?? 0
    body = unknownError ? (
      <MessageStrip state="error" details={unknownError}>
        couldn&apos;t load the titles.
      </MessageStrip>
    ) : (
      <>
        <DataTable<SeriesRow>
          aria-label="titles that can't be checked"
          columns={[
            {
              id: 'title',
              header: 'title',
              cell: (s) => <IdentifierCell title={s.title} text={s.year > 0 ? String(s.year) : undefined} />,
              sortValue: (s) => s.title,
            },
            {
              id: 'why',
              header: 'why',
              // the catalog's own sentence, with the item id, only behind "details".
              cell: (s) => (
                <div>
                  <Text variant="muted">{holdingsReason(s.holdingsError, true)}</Text>
                  {s.holdingsError ? (
                    <details className="acq__details">
                      <summary>details</summary>
                      <span className="acq__sub">{s.holdingsError}</span>
                    </details>
                  ) : null}
                </div>
              ),
            },
          ]}
          rows={unknownTitles ?? []}
          rowKey={(s) => s.titleId}
          loading={unknownTitles == null}
          growing={PAGE}
          empty={<IllustratedMessage size="sm" illustration="done" title="every series can be checked" headingLevel={3} />}
        />
        {unknownTitles && unknownCount > listed ? (
          <p className="acq__note">
            {`${formatCount(unknownCount - listed)} more ${unknownCount - listed === 1 ? 'is a movie' : 'are movies'}, which this list doesn't show.`}
          </p>
        ) : null}
      </>
    )
  } else if (loading && !rows.length && !loadError) {
    body = (
      <DataTable<MissingRow>
        aria-label="missing episodes and movies"
        columns={columns}
        rows={[]}
        rowKey={(r) => r.targetId}
        loading
        empty={<p className="acq__note">loading the backlog…</p>}
      />
    )
  } else if (!rows.length && loadError) {
    body = (
      <IllustratedMessage
        illustration="error"
        title="couldn't load the missing list"
        description="the backlog could not be read. the list fills in once it can."
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
  } else if (!rows.length) {
    body = holdings && holdings.known === 0 && unknownCount > 0 ? (
      <IllustratedMessage
        illustration="unknown"
        title="nothing can be checked yet"
        description={`the library's holdings are unknown for all ${formatCount(unknownCount)} titles, so nothing is known to be missing.`}
        headingLevel={3}
        actions={
          admin ? (
            <Button variant="primary" loading={busy === 'reconcile'} onClick={() => void reconcile()}>
              ask the catalog again
            </Button>
          ) : undefined
        }
      />
    ) : (
      <IllustratedMessage
        illustration="done"
        title="nothing missing"
        description={
          unknownCount > 0
            ? `nothing is missing of the titles whose holdings are known. ${formatCount(unknownCount)} can't be checked.`
            : 'every aired, monitored episode and movie is in the library — or nothing is monitored yet.'
        }
        headingLevel={3}
      />
    )
  } else if (!filtered.length) {
    body = filtersActive ? (
      <IllustratedMessage
        illustration="search"
        title="no matches"
        description="nothing in this list fits the search and filters."
        headingLevel={3}
        actions={<Button onClick={clearFilters}>clear filters</Button>}
      />
    ) : (
      <IllustratedMessage
        illustration="done"
        title={tab === 'attention' ? 'nothing needs attention' : 'nothing on its way'}
        description={tab === 'attention' ? 'no grab has failed.' : 'no grab is under way right now.'}
        headingLevel={3}
      />
    )
  } else {
    body = (
      <DataTable<MissingRow>
        aria-label="missing episodes and movies"
        columns={columns}
        rows={filtered}
        rowKey={(r) => r.targetId}
        rowLabel={rowName}
        sort={sort}
        onSortChange={setSort}
        selectable={admin}
        isRowSelectable={grabbable}
        selected={selectedInView}
        onSelectionChange={setSelected}
        groupBy={groupOf}
        groupHeader={groupHeader}
        groupAction={admin ? groupAction : undefined}
        rowHighlight={(r) => (r.state === 'failed' ? 'error' : undefined)}
        growing={PAGE}
        growingResetKey={`${tab}|${q}|${kind}|${aired}`}
      />
    )
  }

  const listShown = tab !== 'unaired' && tab !== 'unknown' && rows.length > 0
  // a partial selection can always be widened to everything grabbable the filters keep.
  const canSelectAll = admin && selectedInView.size > 0 && selectedInView.size < grabbableInView.length

  return (
    <div className="acq__view">
      <Worklist
        title="missing"
        subtitle="aired, monitored, and not in the library"
        headingLevel={2}
        headerInfo={
          <>
            {counts && (
              <span>
                {`${formatCount(counts.held)} held · ${formatCount(counts.series)} series · ${formatCount(counts.movies)} movies`}
              </span>
            )}
            {holdings?.lastAt && <span>{`holdings last asked ${ago(holdings.lastAt)}`}</span>}
            {policy && <span>{onItsOwn(policy)}</span>}
            {counts && rows.length < counts.missing && (
              <span>{`the list holds the newest ${formatCount(rows.length)} of ${formatCount(counts.missing)}`}</span>
            )}
          </>
        }
        tabs={tabs}
        activeTab={tab}
        onTabChange={(id) => setTab(id as Tab)}
        tabsLabel="filter missing by state"
        messages={strips.length ? strips : undefined}
        filterBar={
          listShown ? (
            <FilterBar
              search={{ value: q, onChange: setQ, placeholder: 'search titles or S04E07', label: 'search titles' }}
              filters={[
                {
                  type: 'select',
                  id: 'kind',
                  label: 'kind',
                  value: kind,
                  onChange: setKind,
                  options: [
                    { value: '', label: 'all' },
                    { value: 'series', label: 'series' },
                    { value: 'movie', label: 'movies' },
                  ],
                },
                {
                  type: 'select',
                  id: 'aired',
                  label: 'aired',
                  value: aired,
                  onChange: setAired,
                  options: [
                    { value: '', label: 'any time' },
                    { value: 'week', label: 'this week' },
                    { value: 'month', label: 'this month' },
                    { value: 'older', label: 'older' },
                  ],
                },
              ]}
              resultCount={filtersActive ? `${formatCount(filtered.length)} of ${formatCount(inTab.length)}` : undefined}
              onClearAll={clearFilters}
            />
          ) : undefined
        }
        toolbar={
          listShown && filtered.length > 0 ? (
            <SelectionToolbar
              label="missing episodes and movies"
              heading={`${formatCount(filtered.length)} ${noun}`}
              selectedCount={selectedInView.size}
              onClearSelection={() => setSelected(new Set())}
              selectAll={
                canSelectAll
                  ? {
                      label: `select all ${formatCount(grabbableInView.length)}`,
                      onSelect: () => setSelected(new Set(grabbableInView.map((r) => r.targetId))),
                    }
                  : null
              }
              primaryAction={(n) =>
                admin && n > 0
                  ? {
                      label: `grab ${formatCount(n)} selected`,
                      icon: <Download size={14} aria-hidden />,
                      loading: busy === 'bulk',
                      onSelect: () => void grab([...selectedInView], 'bulk'),
                    }
                  : null
              }
              actions={
                admin
                  ? [
                      {
                        id: 'reconcile',
                        label: 'ask the catalog again',
                        icon: <RefreshCw size={14} aria-hidden />,
                        overflow: true,
                        loading: busy === 'reconcile',
                        onSelect: () => void reconcile(),
                      },
                    ]
                  : []
              }
            />
          ) : undefined
        }
      >
        {body}
      </Worklist>
      {picking && (
        <ReleasePicker
          title={rowName(picking)}
          load={() => api.targetReleases(picking.targetId)}
          pick={(c) => api.pickTarget(picking.targetId, c)}
          onClose={() => setPicking(null)}
          onGrabbed={() => {
            toast(`picked a release for ${rowName(picking)}`, { state: 'success' })
            void load()
          }}
        />
      )}
    </div>
  )
}
