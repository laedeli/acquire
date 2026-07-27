// Settings: the quality profile that decides which release wins.
//
// Everything here feeds the scorer, and the scores it produces are what the
// search and picker views show — so a change is visible immediately in the
// "why" line next to each release.
import { useEffect, useState } from 'react'
import { Badge, Button, Checkbox, Field, Input, Select, Text } from '@nalet/design-system'
import { Save } from 'lucide-react'
import type { Api, QualityProfile } from '../lib/api'

const RESOLUTIONS = ['2160p', '1080p', '720p', '480p']

export function Settings({ api, admin }: { api: Api; admin: boolean }) {
  const [profiles, setProfiles] = useState<QualityProfile[]>([])
  const [draft, setDraft] = useState<QualityProfile | null>(null)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [saved, setSaved] = useState(false)

  useEffect(() => {
    api
      .profiles()
      .then((ps) => {
        setProfiles(ps)
        setDraft(ps.find((p) => p.isDefault) || ps[0] || null)
      })
      .catch((e) => setError(String(e.message || e)))
  }, [api])

  if (error) return <Text variant="muted">{error}</Text>
  if (!draft) return <Text variant="muted">no quality profile configured.</Text>

  const cfg = draft.config
  const set = (patch: Partial<QualityProfile['config']>) =>
    setDraft({ ...draft, config: { ...cfg, ...patch } })

  async function save() {
    if (!draft) return
    setSaving(true)
    setError('')
    try {
      await api.saveProfile(draft)
      setSaved(true)
      setTimeout(() => setSaved(false), 2500)
    } catch (e) {
      setError(String((e as Error).message || e))
    } finally {
      setSaving(false)
    }
  }

  // Resolutions are an ORDERED preference, so moving one changes which wins.
  function moveResolution(r: string, dir: -1 | 1) {
    const list = [...cfg.resolutions]
    const i = list.indexOf(r)
    if (i < 0) return
    const j = i + dir
    if (j < 0 || j >= list.length) return
    ;[list[i], list[j]] = [list[j], list[i]]
    set({ resolutions: list })
  }

  function toggleResolution(r: string) {
    const list = cfg.resolutions.includes(r)
      ? cfg.resolutions.filter((x) => x !== r)
      : [...cfg.resolutions, r]
    set({ resolutions: list })
  }

  return (
    <div className="acq__settings">
      <div className="acq__settings-head">
        <div>
          <span className="acq__mono">{draft.name}</span>{' '}
          {draft.isDefault && <Badge tone="green">default</Badge>}
          <Text variant="muted" as="div">
            {profiles.length} profile{profiles.length === 1 ? '' : 's'} · this one decides what
            auto-grab picks.
          </Text>
        </div>
        {admin && (
          <Button variant="primary" loading={saving} leading={<Save size={14} />} onClick={() => void save()}>
            {saved ? 'saved' : 'save'}
          </Button>
        )}
      </div>

      <Field label="prefer protocol" hint="which kind of source wins when both are available">
        <Select
          value={cfg.preferProtocol}
          onChange={(e) => set({ preferProtocol: e.currentTarget.value })}
          options={[
            { label: 'usenet (NZB first)', value: 'usenet' },
            { label: 'torrent first', value: 'torrent' },
            { label: 'no preference', value: 'any' },
          ]}
        />
      </Field>

      <Field
        label="resolutions"
        hint="ordered: the first one listed wins. unticked resolutions are allowed but never preferred."
      >
        <div className="acq__reslist">
          {RESOLUTIONS.map((r) => {
            const on = cfg.resolutions.includes(r)
            const rank = cfg.resolutions.indexOf(r)
            return (
              <div className="acq__resrow" key={r}>
                <Checkbox checked={on} onChange={() => toggleResolution(r)} label={r} />
                {on && (
                  <>
                    <Badge tone={rank === 0 ? 'green' : 'neutral'}>#{rank + 1}</Badge>
                    <Button size="sm" variant="ghost" onClick={() => moveResolution(r, -1)}>
                      ↑
                    </Button>
                    <Button size="sm" variant="ghost" onClick={() => moveResolution(r, 1)}>
                      ↓
                    </Button>
                  </>
                )}
              </div>
            )
          })}
        </div>
      </Field>

      <Field label="preferred codecs" hint="comma separated, e.g. x265, hevc">
        <Input
          value={cfg.preferredCodecs.join(', ')}
          onChange={(e) => set({ preferredCodecs: splitList(e.currentTarget.value) })}
        />
      </Field>

      <Field label="reject terms" hint="a release whose name contains one of these is never grabbed">
        <Input
          value={cfg.rejectTerms.join(', ')}
          onChange={(e) => set({ rejectTerms: splitList(e.currentTarget.value) })}
        />
      </Field>

      <div className="acq__settings-row">
        <Field label="min size (MB)" hint="anything smaller is treated as junk">
          <Input
            type="number"
            value={String(cfg.minSizeMb)}
            onChange={(e) => set({ minSizeMb: Number(e.currentTarget.value) || 0 })}
          />
        </Field>
        <Field label="max size (MB)" hint="keeps 70 GB remuxes out">
          <Input
            type="number"
            value={String(cfg.maxSizeMb)}
            onChange={(e) => set({ maxSizeMb: Number(e.currentTarget.value) || 0 })}
          />
        </Field>
        <Field label="min seeders" hint="torrents only">
          <Input
            type="number"
            value={String(cfg.minSeeders)}
            onChange={(e) => set({ minSeeders: Number(e.currentTarget.value) || 0 })}
          />
        </Field>
      </div>

      <Checkbox
        checked={cfg.preferHdr}
        onChange={(e) => set({ preferHdr: e.currentTarget.checked })}
        label="prefer HDR / Dolby Vision when available"
      />
    </div>
  )
}

function splitList(v: string): string[] {
  return v
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean)
}
