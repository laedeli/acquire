// Downloads: every client job, live. Active ones first, then what recently
// finished — so a failure stays visible instead of vanishing.
import { useState } from 'react'
import { Button, Table, Text, type TableColumn } from '@nalet/design-system'
import { Pause, Play, X } from 'lucide-react'
import type { Api, ClientStatus, Download } from '../lib/api'
import { ClientChips, ProgressBar, StatusBadge, TelemetryLine } from '../components/Bits'
import { ago } from '../lib/format'

export function Downloads({
  api,
  rows,
  clients,
  admin,
  refresh,
}: {
  api: Api
  rows: Download[]
  clients: ClientStatus[]
  admin: boolean
  refresh: () => void
}) {
  const [busy, setBusy] = useState('')
  const [error, setError] = useState('')

  async function control(d: Download, action: 'pause' | 'resume' | 'cancel') {
    if (action === 'cancel' && !window.confirm(`Cancel ${d.title || d.clientJobId}?`)) return
    setBusy(d.adapter + d.clientJobId)
    setError('')
    try {
      await api.control(d.adapter, d.clientJobId, action)
      refresh()
    } catch (e) {
      setError(String((e as Error).message || e))
    } finally {
      setBusy('')
    }
  }

  const columns: TableColumn<Download>[] = [
    {
      key: 'title',
      header: 'title',
      render: (d) => (
        <div>
          <span className="acq__mono">{d.title || d.clientJobId}</span>
          <div className="acq__sub">
            {d.adapter}
            {d.finishedAt ? ` · finished ${ago(d.finishedAt)}` : ''}
            {d.error ? ` · ${d.error}` : ''}
          </div>
        </div>
      ),
    },
    { key: 'state', header: 'state', render: (d) => <StatusBadge status={d.state} /> },
    {
      key: 'progressPct',
      header: 'progress',
      render: (d) => (
        <div>
          <ProgressBar value={d.progressPct} />
          <TelemetryLine d={d} />
        </div>
      ),
    },
    {
      key: 'clientJobId',
      header: '',
      align: 'right',
      render: (d) => {
        if (!admin) return null
        const live = d.state === 'downloading' || d.state === 'queued'
        const key = d.adapter + d.clientJobId
        return (
          <div className="acq__actions">
            {live && (
              <>
                <Button
                  size="sm"
                  variant="ghost"
                  loading={busy === key}
                  leading={<Pause size={14} />}
                  onClick={() => void control(d, 'pause')}
                >
                  pause
                </Button>
                <Button
                  size="sm"
                  variant="ghost"
                  leading={<Play size={14} />}
                  onClick={() => void control(d, 'resume')}
                >
                  resume
                </Button>
              </>
            )}
            <Button
              size="sm"
              variant="ghost"
              leading={<X size={14} />}
              onClick={() => void control(d, 'cancel')}
            >
              cancel
            </Button>
          </div>
        )
      },
    },
  ]

  return (
    <>
      <ClientChips clients={clients} />
      {error && <Text variant="muted">{error}</Text>}
      <Table
        columns={columns}
        rows={rows}
        rowKey={(d) => d.adapter + ':' + d.clientJobId}
        empty={<Text variant="muted">no downloads yet.</Text>}
      />
    </>
  )
}
