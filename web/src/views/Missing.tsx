// Missing: what we want and do not have, and what is being done about it.
//
// Every row is an aired, monitored episode or movie that the library does not
// hold — of a title whose holdings acquire knows. A title whose holdings are
// unknown (never asked of the catalog, or a catalog that cannot answer) has no
// rows here: nothing says its episodes are not on disk, and the header says
// how many titles that leaves out, and why.
//
// Three deliberate choices, all about not lying by omission:
//   * rows in search backoff, and rows whose grab failed, are SHOWN and say
//     why. A backlog that hides everything currently failing looks healthy
//     while being stuck.
//   * a row a grab is under way for stays until the library holds it, saying
//     where the grab is: grabbed, downloaded, waiting for the catalog.
//   * a title with no id an indexer accepts is marked unsearchable, so a row
//     that cannot progress says why instead of sitting there looking ordinary.
//
// An admin grabs from here: per row (search and grab the best acceptable
// release, or pick one among the row's releases) and for a selection. Grabs
// are queued and done one at a time; the rows follow.
import { useCallback, useEffect, useState } from 'react'
import { Badge, Button, Checkbox, Table, Text, type TableColumn } from '@nalet/design-system'
import { Download as DownloadIcon, RefreshCw, Search } from 'lucide-react'
import type { Api, Counts, HoldingsSummary, MissingRow } from '../lib/api'
import { ReleasePicker } from '../components/ReleasePicker'
import { ago, until } from '../lib/format'

function coords(r: MissingRow) {
  if (r.season == null || r.episode == null) return 'movie'
  return `S${String(r.season).padStart(2, '0')}E${String(r.episode).padStart(2, '0')}`
}

const grabbable = (r: MissingRow) => (r.state === 'wanted' || r.state === 'failed') && !r.grabQueued

function StateBadge({ r }: { r: MissingRow }) {
  if (r.grabQueued) return <Badge tone="blue">queued</Badge>
  switch (r.state) {
    case 'grabbed':
      return <Badge tone="blue" dot>grabbed</Badge>
    case 'downloaded':
      return <Badge tone="blue" dot>downloaded</Badge>
    case 'waiting':
      return <Badge tone="amber">waiting for the catalog</Badge>
    case 'failed':
      return <Badge tone="amber">failed</Badge>
  }
  if (!r.searchable) return <Badge tone="amber">no id a source accepts — text search only</Badge>
  switch (r.searchOutcome) {
    case 'found':
      return <Badge tone="green">found</Badge>
    case 'none':
      return <Badge tone="neutral">nothing found{r.backoffUntil ? ` · again ${until(r.backoffUntil)}` : ''}</Badge>
    case 'error':
      return <Badge tone="amber">search failed{r.backoffUntil ? ` · again ${until(r.backoffUntil)}` : ''}</Badge>
  }
  return <Badge tone="neutral">not searched yet</Badge>
}

export function Missing({ api, admin, tick }: { api: Api; admin: boolean; tick: number }) {
  const [rows, setRows] = useState<MissingRow[]>([])
  const [counts, setCounts] = useState<Counts | null>(null)
  const [holdings, setHoldings] = useState<HoldingsSummary | null>(null)
  const [sweepGrabs, setSweepGrabs] = useState(false)
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [busy, setBusy] = useState('')
  const [picking, setPicking] = useState<MissingRow | null>(null)
  const [note, setNote] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)

  const load = useCallback(async () => {
    try {
      const [m, c] = await Promise.all([api.missing(500), api.counts()])
      setRows(m.missing)
      setHoldings(m.holdings)
      setSweepGrabs(m.autoGrab)
      setCounts(c)
      setError('')
      // A row that can no longer be grabbed leaves the selection.
      setSelected((prev) => {
        const can = new Set(m.missing.filter(grabbable).map((r) => r.targetId))
        return new Set([...prev].filter((id) => can.has(id)))
      })
    } catch (e) {
      setError(String((e as Error).message || e))
    } finally {
      setLoading(false)
    }
  }, [api])

  useEffect(() => {
    void load()
    const t = setInterval(() => void load(), 30000)
    return () => clearInterval(t)
  }, [load, tick])

  async function act(key: string, fn: () => Promise<string>) {
    setBusy(key)
    setError('')
    setNote('')
    try {
      setNote(await fn())
      await load()
    } catch (e) {
      setError(String((e as Error).message || e))
    } finally {
      setBusy('')
    }
  }

  const grab = (ids: string[]) =>
    act(ids.length === 1 ? ids[0] : 'bulk', async () => {
      const r = await api.grabTargets(ids)
      setSelected(new Set())
      const n = r.queued.length
      return `${n} queued for a search and grab${r.skipped ? ` · ${r.skipped} skipped: held already or under way` : ''}`
    })

  const reconcile = () =>
    act('reconcile', async () => {
      await api.reconcileHoldings()
      return 'asking the catalog what the library holds — the rows follow'
    })

  const can = rows.filter(grabbable)
  const allSelected = can.length > 0 && can.every((r) => selected.has(r.targetId))
  const toggle = (id: string) =>
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })

  const cols: TableColumn<MissingRow>[] = [
    ...(admin
      ? [
          {
            key: 'targetId' as keyof MissingRow & string,
            header: (
              <Checkbox
                aria-label="select every row that can be grabbed"
                checked={allSelected}
                indeterminate={!allSelected && selected.size > 0}
                disabled={can.length === 0}
                onChange={() => setSelected(allSelected ? new Set() : new Set(can.map((r) => r.targetId)))}
              />
            ),
            width: 32,
            render: (r: MissingRow) => (
              <Checkbox
                aria-label={`select ${r.title} ${coords(r)}`}
                checked={selected.has(r.targetId)}
                disabled={!grabbable(r)}
                onChange={() => toggle(r.targetId)}
              />
            ),
          },
        ]
      : []),
    {
      key: 'title',
      header: 'title',
      render: (r) => (
        <div>
          <Text>{r.title}</Text> <Text variant="dim">{coords(r)}</Text>
        </div>
      ),
    },
    {
      key: 'airDate',
      header: 'aired',
      render: (r) => <Text variant="muted">{r.airDate ? ago(r.airDate) : '—'}</Text>,
    },
    {
      key: 'state',
      header: 'state',
      render: (r) => (
        <div>
          <StateBadge r={r} />
          {(r.detail || r.searchDetail) && <div className="acq__sub">{r.detail || r.searchDetail}</div>}
        </div>
      ),
    },
    ...(admin
      ? [
          {
            key: 'kind' as keyof MissingRow & string,
            header: '',
            align: 'right' as const,
            render: (r: MissingRow) => (
              <div className="acq__actions">
                <Button
                  size="sm"
                  variant="primary"
                  disabled={!grabbable(r)}
                  loading={busy === r.targetId}
                  leading={<DownloadIcon size={14} />}
                  onClick={() => void grab([r.targetId])}
                >
                  grab
                </Button>
                <Button
                  size="sm"
                  disabled={!grabbable(r)}
                  leading={<Search size={14} />}
                  onClick={() => setPicking(r)}
                >
                  releases
                </Button>
              </div>
            ),
          },
        ]
      : []),
  ]

  return (
    <div className="acq__view">
      {counts && (
        <div className="acq__counts">
          <Text variant="ui">
            {counts.missing} missing · {counts.inFlight} on their way · {counts.failed} failed · {counts.held} held ·{' '}
            {counts.unaired} not yet aired
          </Text>{' '}
          <Text variant="dim">
            across {counts.series} series and {counts.movies} movies
          </Text>
        </div>
      )}
      {holdings && (
        <div className="acq__toolbar">
          {holdings.unknown > 0 ? (
            <>
              <Badge tone="amber">
                holdings unknown for {holdings.unknown} title{holdings.unknown === 1 ? '' : 's'}
              </Badge>
              <Text variant="muted">
                {counts && counts.unknownTargets > 0 ? `${counts.unknownTargets} of their episodes are left out` : 'left out'}
                {holdings.reason ? ` — ${holdings.reason}` : ' — not asked of the catalog yet'}
              </Text>
            </>
          ) : (
            <Text variant="dim">
              what the library holds is the catalog's{holdings.lastAt ? `, last asked ${ago(holdings.lastAt)}` : ''}
            </Text>
          )}
          {admin && (
            <Button size="sm" variant="ghost" loading={busy === 'reconcile'} leading={<RefreshCw size={14} />} onClick={() => void reconcile()}>
              ask the catalog now
            </Button>
          )}
        </div>
      )}
      <div className="acq__toolbar">
        <Text variant="dim">
          {sweepGrabs
            ? 'the sweep grabs what it finds missing, within the limits in settings'
            : 'the sweep only searches — grabbing on its own is off in settings'}
        </Text>
        {admin && (
          <Button
            size="sm"
            variant="primary"
            disabled={selected.size === 0}
            loading={busy === 'bulk'}
            leading={<DownloadIcon size={14} />}
            onClick={() => void grab([...selected])}
          >
            grab selected{selected.size ? ` (${selected.size})` : ''}
          </Button>
        )}
      </div>
      {note && <Text variant="muted">{note}</Text>}
      {error && <Text variant="muted">{error}</Text>}
      {loading && !rows.length && <Text variant="muted">loading…</Text>}
      {!loading && !rows.length && !error && (
        <Text variant="muted">
          nothing missing of the titles whose holdings are known — either everything is in the library, or
          nothing is monitored yet
        </Text>
      )}
      {rows.length > 0 && <Table columns={cols} rows={rows} rowKey={(r) => r.targetId} />}
      {picking && (
        <ReleasePicker
          title={`${picking.title} ${coords(picking)}`}
          load={() => api.targetReleases(picking.targetId)}
          pick={(c) => api.pickTarget(picking.targetId, c)}
          onClose={() => setPicking(null)}
          onGrabbed={() => void load()}
        />
      )}
    </div>
  )
}
