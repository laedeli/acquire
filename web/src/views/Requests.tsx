// Requests: what was asked for, what it is doing now, and what an admin can do
// about it. A downloading request shows its live bar inline, so you do not have
// to cross-reference the downloads view.
//
// A request for a series follows it: the row says how much of it the library
// holds and what comes next, and its episodes are grabbed one by one — on
// their own, or from the missing tab —, never the series as a whole. A
// request for a movie waits for the movie to come out, and its grab is the
// movie's. A request from someone who is not an admin may await an admin's
// approval: its title is followed meanwhile, nothing of it searched or
// grabbed on its own, and an admin approves or declines it here.
import { useState } from 'react'
import { Button, Table, Text, type TableColumn } from '@nalet/design-system'
import { Check, Download as DownloadIcon, Link2, Search, Trash2, X } from 'lucide-react'
import type { Api, Download, Wanted } from '../lib/api'
import { ProgressBar, StatusBadge, TelemetryLine } from '../components/Bits'
import { ReleasePicker } from '../components/ReleasePicker'
import { ago } from '../lib/format'

/** A request that follows a series: grabbed episode by episode, never as a whole. */
const followsSeries = (w: Wanted) => w.progress?.kind === 'series'

/** What a request that follows its title says of what is grabbed on its own. */
function onItsOwn(w: Wanted): string {
  if (!w.progress || w.status === 'fulfilled' || w.approval === 'declined') return ''
  if (w.approval === 'awaiting') return 'an admin approves or declines it in the requests tab'
  if (w.progress.kind === 'series')
    return w.autoGrab
      ? 'new episodes are grabbed on their own after they air, as releases turn up'
      : 'its episodes are grabbed from the missing tab — grabbing on its own is off in settings'
  if (w.status === 'waiting' || w.status === 'pending')
    return w.autoGrab
      ? 'grabbed on its own once it is out and a release turns up'
      : 'grabbed from here or the missing tab — grabbing on its own is off in settings'
  return ''
}

export function Requests({
  api,
  rows,
  downloads,
  admin,
  autoGrab,
  refresh,
}: {
  api: Api
  rows: Wanted[]
  downloads: Download[]
  admin: boolean
  autoGrab: boolean
  refresh: () => void
}) {
  const [busy, setBusy] = useState('')
  const [picking, setPicking] = useState<Wanted | null>(null)
  const [error, setError] = useState('')

  // A followed movie downloads as its target's grab: the download names the grab.
  const activeFor = (w: Wanted) =>
    downloads.find(
      (d) =>
        (d.wantedId === w.id || (!!w.progress?.movie?.grabId && d.wantedId === w.progress.movie.grabId)) &&
        (d.state === 'downloading' || d.state === 'queued'),
    )

  async function act(id: string, fn: () => Promise<unknown>) {
    setBusy(id)
    setError('')
    try {
      await fn()
      refresh()
    } catch (e) {
      setError(String((e as Error).message || e))
    } finally {
      setBusy('')
    }
  }

  const columns: TableColumn<Wanted>[] = [
    {
      key: 'title',
      header: 'title',
      render: (w) => (
        <div>
          <span className="acq__mono">{w.title}</span>{' '}
          <Text variant="muted" as="span">
            {w.year || ''}
          </Text>
          <div className="acq__sub">requested {ago(w.requestedAt)}</div>
        </div>
      ),
    },
    { key: 'status', header: 'status', render: (w) => <StatusBadge status={w.status} /> },
    {
      key: 'detail',
      header: 'detail',
      render: (w) => {
        const d = activeFor(w)
        const p = w.progress
        const own = onItsOwn(w)
        return (
          <div>
            <Text variant="muted" as="div">
              {w.detail}
            </Text>
            {p?.kind === 'series' && p.episodes > 0 && <ProgressBar value={Math.round((p.held / p.episodes) * 100)} />}
            {d && (
              <>
                <ProgressBar value={d.progressPct} />
                <TelemetryLine d={d} />
              </>
            )}
            {own && <div className="acq__sub">{own}</div>}
          </div>
        )
      },
    },
    {
      key: 'id',
      header: '',
      align: 'right',
      render: (w) => {
        if (!admin) return null
        const grabbable = (w.status === 'pending' || w.status === 'failed') && !followsSeries(w)
        return (
          <div className="acq__actions">
            {(w.approval === 'awaiting' || w.approval === 'declined') && (
              <Button
                size="sm"
                variant="primary"
                loading={busy === w.id + ':approve'}
                leading={<Check size={14} />}
                onClick={() => void act(w.id + ':approve', () => api.approve(w.id))}
              >
                approve
              </Button>
            )}
            {w.approval === 'awaiting' && (
              <Button
                size="sm"
                variant="ghost"
                loading={busy === w.id + ':decline'}
                leading={<X size={14} />}
                onClick={() => void act(w.id + ':decline', () => api.decline(w.id))}
              >
                decline
              </Button>
            )}
            {grabbable && autoGrab && (
              <>
                <Button
                  size="sm"
                  variant="primary"
                  loading={busy === w.id}
                  leading={<DownloadIcon size={14} />}
                  onClick={() => void act(w.id, () => api.autograb(w.id))}
                >
                  find &amp; grab
                </Button>
                <Button
                  size="sm"
                  leading={<Search size={14} />}
                  onClick={() => setPicking(w)}
                >
                  releases
                </Button>
              </>
            )}
            {grabbable && (
              <Button
                size="sm"
                variant="ghost"
                leading={<Link2 size={14} />}
                onClick={() => {
                  const src = window.prompt('magnet or .torrent URL')
                  if (src) void act(w.id, () => api.grabMagnet(w.id, src))
                }}
              >
                magnet
              </Button>
            )}
            <Button
              size="sm"
              variant="ghost"
              leading={<Trash2 size={14} />}
              onClick={() => {
                if (window.confirm(`Remove the request for ${w.title}?`))
                  void act(w.id, () => api.remove(w.id))
              }}
            >
              remove
            </Button>
          </div>
        )
      },
    },
  ]

  return (
    <>
      {error && <Text variant="muted">{error}</Text>}
      <Table
        columns={columns}
        rows={rows}
        rowKey={(w) => w.id}
        empty={<Text variant="muted">nothing requested yet.</Text>}
      />
      {picking && (
        <ReleasePicker
          title={`${picking.title}${picking.year ? ` (${picking.year})` : ''}`}
          load={() => api.releases(picking.id)}
          pick={(c) => api.pick(picking.id, c)}
          onClose={() => setPicking(null)}
          onGrabbed={refresh}
        />
      )}
    </>
  )
}
