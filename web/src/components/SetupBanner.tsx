// SetupBanner: what still stands between this install and a working grab.
//
// It reads the same checklist the platform shows next to the addon, so the
// console and the portal never disagree about whether acquire is usable. Only
// admins see it — they are the ones who can act on it.
import { useEffect, useState } from 'react'
import { Badge, Button, Text } from '@nalet/design-system'
import type { Api, SetupSection, SetupStatus } from '../lib/api'

/** Which console tab edits each checklist section. */
const TARGET: Record<string, string> = {
  sources: 'indexers',
  clients: 'clients',
  'grab-policy': 'settings',
}

const TITLE: Record<string, string> = {
  sources: 'search sources',
  clients: 'download clients',
  'grab-policy': 'search and grab',
}

export function SetupBanner({
  api,
  admin,
  onNavigate,
}: {
  api: Api
  admin: boolean
  onNavigate: (tab: string) => void
}) {
  const [setup, setSetup] = useState<SetupStatus | null>(null)

  useEffect(() => {
    if (!admin) return
    let live = true
    api
      .setup()
      .then((s) => live && setSetup(s))
      .catch(() => live && setSetup(null))
    return () => {
      live = false
    }
  }, [api, admin])

  if (!admin || !setup || setup.state === 'ready') return null
  const open = setup.sections.filter((s: SetupSection) => s.state !== 'ready')

  return (
    <div className="acq__banner" role="status">
      <div className="acq__banner-head">
        <Badge tone="amber" dot>
          {setup.state === 'needs-setup' ? 'setup needed' : 'degraded'}
        </Badge>
        <Text variant="muted" as="span">
          {setup.state === 'needs-setup'
            ? 'acquire cannot grab anything until these are done.'
            : 'acquire works, but not completely.'}
        </Text>
      </div>
      {open.map((s) => (
        <div className="acq__banner-row" key={s.key}>
          <span className="acq__mono">{TITLE[s.key] || s.key}</span>
          {/* Summaries are plain text from the API; React escapes them. */}
          <Text variant="muted" as="span">
            {s.summary}
          </Text>
          {TARGET[s.key] && (
            <Button size="sm" variant="ghost" onClick={() => onNavigate(TARGET[s.key])}>
              configure
            </Button>
          )}
        </div>
      ))}
    </div>
  )
}
