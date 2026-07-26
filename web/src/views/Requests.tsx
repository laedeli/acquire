// Requests: what was asked for, what it is doing now, and what an admin can do
// about it. A downloading request shows its live bar inline, so you do not have
// to cross-reference the downloads view.
import { useState } from 'react'
import { Button, Table, Text, type TableColumn } from '@nalet/design-system'
import { Download as DownloadIcon, Link2, Search, Trash2 } from 'lucide-react'
import type { Api, Download, Wanted } from '../lib/api'
import { ProgressBar, StatusBadge, TelemetryLine } from '../components/Bits'
import { ReleasePicker } from '../components/ReleasePicker'
import { ago } from '../lib/format'

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

  const activeFor = (id: string) =>
    downloads.find((d) => d.wantedId === id && (d.state === 'downloading' || d.state === 'queued'))

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
        const d = activeFor(w.id)
        return (
          <div>
            <Text variant="muted" as="div">
              {w.detail}
            </Text>
            {d && (
              <>
                <ProgressBar value={d.progressPct} />
                <TelemetryLine d={d} />
              </>
            )}
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
        const grabbable = w.status === 'pending' || w.status === 'failed'
        return (
          <div className="acq__actions">
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
          api={api}
          wanted={picking}
          onClose={() => setPicking(null)}
          onGrabbed={refresh}
        />
      )}
    </>
  )
}
