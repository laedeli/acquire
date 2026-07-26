// Small shared pieces: the progress bar the design system doesn't ship, status
// tones, and the per-client health chips.
import { Badge } from '@nalet/design-system'
import type { ClientStatus, Download } from '../lib/api'
import { bytes, eta, health, pct, speed } from '../lib/format'

/** Square accent-filled bar on a border-coloured track — the house convention. */
export function ProgressBar({ value }: { value: number }) {
  const v = Math.max(0, Math.min(100, value || 0))
  return (
    <div className="acq__bar" role="progressbar" aria-valuenow={Math.round(v)}>
      <div className="acq__bar-fill" style={{ width: `${v}%` }} />
    </div>
  )
}

type Tone = 'green' | 'amber' | 'blue' | 'neutral'

export function statusTone(s: string): Tone {
  switch (s) {
    case 'fulfilled':
    case 'completed':
      return 'green'
    case 'failed':
      return 'amber'
    case 'downloading':
    case 'packaging':
    case 'queued':
    case 'pending':
      return 'blue'
    default:
      return 'neutral'
  }
}

export function StatusBadge({ status }: { status: string }) {
  return (
    <Badge tone={statusTone(status)} dot>
      {status}
    </Badge>
  )
}

/** One line of live numbers under a progress bar. */
export function TelemetryLine({ d }: { d: Download }) {
  const parts = [
    pct(d.progressPct),
    d.bytesTotal ? `${bytes(d.bytesDone)} / ${bytes(d.bytesTotal)}` : bytes(d.bytesDone),
    speed(d.speedBps),
    eta(d.etaSec) && `${eta(d.etaSec)} left`,
    d.seeders != null ? `${d.seeders} seeds` : health(d.health) && `health ${health(d.health)}`,
    d.nativeState,
  ].filter(Boolean)
  return <div className="acq__sub">{parts.join(' · ')}</div>
}

export function ClientChips({ clients }: { clients: ClientStatus[] }) {
  if (!clients.length) return null
  return (
    <div className="acq__chips">
      {clients.map((c) => {
        const bits: string[] = []
        if (c.down_bps > 0) bits.push(`↓ ${speed(c.down_bps)}`)
        if (c.free_disk_bytes) bits.push(`${bytes(c.free_disk_bytes)} free`)
        for (const k of Object.keys(c.detail || {})) bits.push(`${k.replace(/_/g, ' ')} ${c.detail![k]}`)
        if (c.paused) bits.push('paused')
        if (!c.reachable && c.error) bits.push(c.error)
        return (
          <Badge key={c.name} tone={c.reachable ? 'green' : 'amber'} dot>
            {c.name}
            {bits.length ? ` · ${bits.join(' · ')}` : ''}
          </Badge>
        )
      })}
    </div>
  )
}
