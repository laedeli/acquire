// Settings: how acquire searches and grabs, the quality profile that decides
// which release wins, and the subtitles: their languages, their source and
// whether they come on their own.
//
// Everything in the profile feeds the scorer, and the scores it produces are
// what the search and picker views show — so a change is visible immediately
// in the "why" line next to each release.
import { useEffect, useState } from 'react'
import { Badge, Button, Checkbox, Divider, Field, Input, Select, Switch, Text } from '@nalet/design-system'
import { Save } from 'lucide-react'
import {
  ApiError,
  type AirTimesStatus,
  type Api,
  type QualityProfile,
  type RequestSettings,
  type SearchSettings,
  type SubtitleOverview,
  type SubtitleSettings,
} from '../lib/api'

const RESOLUTIONS = ['2160p', '1080p', '720p', '480p']

export function Settings({ api, admin }: { api: Api; admin: boolean }) {
  return (
    <div className="acq__settings-stack">
      {admin && <SearchAndGrab api={api} />}
      {admin && <Requests api={api} />}
      {admin && <Subtitles api={api} />}
      <QualityProfileSettings api={api} admin={admin} />
    </div>
  )
}

/**
 * The search and grab policy. It used to be deployment environment only; the
 * environment now just seeds it. Two switches say what is grabbed on its own:
 * the new things (what aired in the last days, what was requested), and
 * everything missing. Switching either on while series that run have no air
 * times from TVmaze yet asks the admin to confirm it first.
 */
function SearchAndGrab({ api }: { api: Api }) {
  const [draft, setDraft] = useState<SearchSettings | null>(null)
  const [fields, setFields] = useState<Record<string, string>>({})
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)

  useEffect(() => {
    api
      .searchSettings()
      .then(setDraft)
      .catch((e) => setError(String(e.message || e)))
  }, [api])

  if (error && !draft) return <Text variant="muted">{error}</Text>
  if (!draft) return null

  async function save() {
    if (!draft) return
    setSaving(true)
    setError('')
    setFields({})
    let confirm = ''
    try {
      for (;;) {
        try {
          setDraft(await api.saveSearchSettings(draft, confirm))
          break
        } catch (e) {
          // A refusal an admin may confirm: it says why; on a yes it goes again.
          if (e instanceof ApiError && e.status === 409 && e.confirm && !confirm && window.confirm(e.message)) {
            confirm = e.confirm
            continue
          }
          throw e
        }
      }
      setSaved(true)
      setTimeout(() => setSaved(false), 2500)
    } catch (e) {
      if (e instanceof ApiError && e.status === 422) setFields(e.fields)
      setError(String((e as Error).message || e))
    } finally {
      setSaving(false)
    }
  }

  const airTimes = draft.airTimes === 'tvmaze' ? airTimesLine(draft.airTimesStatus) : ''

  return (
    <div className="acq__settings">
      <div className="acq__settings-head">
        <div>
          <span className="acq__mono">search and grab</span>
          <Text variant="muted" as="div">
            which protocol is searched first, when acquire refuses to start a download, and what is
            grabbed on its own.
          </Text>
        </div>
        <Button variant="primary" loading={saving} leading={<Save size={14} />} onClick={() => void save()}>
          {saved ? 'saved' : 'save'}
        </Button>
      </div>
      {error && <Text variant="muted">{error}</Text>}
      <div className="acq__settings-row">
        <Field label="search first" error={fields.preferProtocol} hint="the other protocol is searched only when this finds nothing">
          <Select
            value={draft.preferProtocol}
            onChange={(e) =>
              setDraft({ ...draft, preferProtocol: e.currentTarget.value as SearchSettings['preferProtocol'] })
            }
            options={[
              { label: 'usenet (NZB first)', value: 'usenet' },
              { label: 'torrent first', value: 'torrent' },
            ]}
          />
        </Field>
        <Field label="free space floor (GB)" error={fields.storageFloorGb} hint="grabs are refused below this">
          <Input
            type="number"
            value={String(draft.storageFloorGb)}
            onChange={(e) => setDraft({ ...draft, storageFloorGb: Number(e.currentTarget.value) || 0 })}
          />
        </Field>
        <Field label="downloads at once" error={fields.maxConcurrentGrabs} hint="in flight across all clients">
          <Input
            type="number"
            value={String(draft.maxConcurrentGrabs)}
            onChange={(e) => setDraft({ ...draft, maxConcurrentGrabs: Number(e.currentTarget.value) || 0 })}
          />
        </Field>
      </div>
      <div className="acq__settings-row">
        <Field
          label="new episodes and requests"
          error={fields.autoGrabNew}
          hint={`on: an episode that aired in the last ${newDays(draft)} days, every episode of a series a request made — of one acquire tracked before, the episodes TMDB lists after the request —, and a requested movie once it is out to watch at home are grabbed as soon as a release turns up after the wait below — in a source's newest releases, or when the backlog sweep finds one. Never on their own: what a request awaiting an admin's approval asks for, a movie TMDB dates in cinemas only, a series whose releases name no season and episode (an anime, a daily show), a series whose air times TVmaze has not given yet. off: they wait in the missing tab like the rest.`}
        >
          <Switch
            checked={draft.autoGrabNew}
            onChange={(e) => setDraft({ ...draft, autoGrabNew: e.currentTarget.checked })}
            label={draft.autoGrabNew ? 'grabbed on their own' : 'grabbed from the missing tab'}
          />
        </Field>
        <Field label="new means aired in the last (days)" error={fields.newWithinDays} hint="how recently an episode aired to count as new">
          <Input
            type="number"
            value={String(draft.newWithinDays)}
            onChange={(e) => setDraft({ ...draft, newWithinDays: Number(e.currentTarget.value) || 0 })}
          />
        </Field>
        <Field
          label="wait after the air date (hours)"
          error={fields.airDelayHours}
          hint="TMDB dates the day an episode airs, not the hour: for an episode whose air time is not known, an automatic grab waits this long after 00:00 UTC of that day — and of the day a requested movie comes out to watch at home. A release published before the day began where the series comes from — of a series streamed from the United States, before 21:00 in New York the evening before — is taken for a fake and skipped; a person may still pick it. 0: from the air date on."
        >
          <Input
            type="number"
            value={String(draft.airDelayHours)}
            onChange={(e) => setDraft({ ...draft, airDelayHours: Number(e.currentTarget.value) || 0 })}
          />
        </Field>
      </div>
      <div className="acq__settings-row">
        <Field
          label="exact air times"
          error={fields.airTimes}
          hint="TVmaze: acquire reads from TVmaze's public schedule how a series airs — broadcast on a network, or streamed — and when each episode airs and how long it runs, asking by the series' TVDB or IMDb id and nothing else; an automatic grab then takes nothing of a broadcast published before it ended — of one TVmaze gives no hour for, by its usual hour —, nothing of a streamed episode published more than 30 minutes before it streams, and nothing of a series before its air times are read. An episode TVmaze dates later than TMDB is told by TVmaze's date; one the two date apart and cannot tell the same is held until they agree. Off: an episode is told by its air date alone."
        >
          <Select
            value={draft.airTimes}
            onChange={(e) => setDraft({ ...draft, airTimes: e.currentTarget.value as SearchSettings['airTimes'] })}
            options={[
              { label: "TVmaze's schedule", value: 'tvmaze' },
              { label: 'off: the air date alone', value: 'off' },
            ]}
          />
        </Field>
        <Field
          label="wait after it airs (minutes)"
          error={fields.airDelayAfterAirstampMinutes}
          hint="how long an automatic grab waits after an episode's broadcast ends, or after it streams, when its air time is known. 0: from then on."
        >
          <Input
            type="number"
            value={String(draft.airDelayAfterAirstampMinutes)}
            onChange={(e) => setDraft({ ...draft, airDelayAfterAirstampMinutes: Number(e.currentTarget.value) || 0 })}
          />
        </Field>
      </div>
      {airTimes && (
        <Text variant="muted" as="div">
          {airTimes}
        </Text>
      )}
      {draft.airTimes === 'tvmaze' && draft.airTimesStatus?.on && draft.airTimesStatus.warning && (
        <Text variant="muted" as="div">
          {draft.airTimesStatus.warning}
        </Text>
      )}
      <Field
        label="everything missing"
        error={fields.autoGrab}
        hint="on: the backlog sweep and the sources' newest releases grab the best release of whatever is missing, the older backlog too, within the floor and the downloads at once above — never a season pack for one episode, a title whose holdings the catalog has not told, a movie in cinemas only, or an anime or a daily show. off: the sweep only searches, and the rest of the backlog is grabbed from the missing tab."
      >
        <Switch
          checked={draft.autoGrab}
          onChange={(e) => setDraft({ ...draft, autoGrab: e.currentTarget.checked })}
          label={draft.autoGrab ? 'grabbed on its own' : 'only searched'}
        />
      </Field>
      <Divider />
    </div>
  )
}

/**
 * How requests are taken: approved at once whoever makes them, or — the
 * default — awaiting an admin's approval when someone who is not an admin
 * makes one.
 */
function Requests({ api }: { api: Api }) {
  const [draft, setDraft] = useState<RequestSettings | null>(null)
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)

  useEffect(() => {
    api
      .requestSettings()
      .then(setDraft)
      .catch((e) => setError(String(e.message || e)))
  }, [api])

  if (error && !draft) return <Text variant="muted">{error}</Text>
  if (!draft) return null

  async function save() {
    if (!draft) return
    setSaving(true)
    setError('')
    try {
      setDraft(await api.saveRequestSettings(draft))
      setSaved(true)
      setTimeout(() => setSaved(false), 2500)
    } catch (e) {
      setError(String((e as Error).message || e))
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="acq__settings">
      <div className="acq__settings-head">
        <div>
          <span className="acq__mono">requests</span>
          <Text variant="muted" as="div">
            whether a request from someone who is not an admin is approved at once.
          </Text>
        </div>
        <Button variant="primary" loading={saving} leading={<Save size={14} />} onClick={() => void save()}>
          {saved ? 'saved' : 'save'}
        </Button>
      </div>
      {error && <Text variant="muted">{error}</Text>}
      <Field
        label="approve requests"
        hint="on: anyone's request is approved at once, and what it asks for is grabbed as the search and grab settings say. off: a request from someone who is not an admin awaits an admin's approval in the requests tab, and changes nothing until then — a title acquire tracks goes on as before, one it does not is only read from TMDB, nothing of it searched or grabbed. An admin's own request is always approved at once; turning this on does not approve the requests already waiting."
      >
        <Switch
          checked={draft.autoApprove}
          onChange={(e) => setDraft({ ...draft, autoApprove: e.currentTarget.checked })}
          label={draft.autoApprove ? 'approved at once' : 'an admin approves each'}
        />
      </Field>
      <Divider />
    </div>
  )
}

/**
 * The subtitles: the languages every title is to have (the platform's, or the
 * deployment's), the source and its account, the day's downloads, and what
 * the console sets — whether they come on their own, and whether a
 * machine-translated one may be taken. The account and the budget are the
 * deployment's (the addon's values), shown, not edited.
 */
function Subtitles({ api }: { api: Api }) {
  const [draft, setDraft] = useState<SubtitleSettings | null>(null)
  const [overview, setOverview] = useState<SubtitleOverview | null>(null)
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)

  useEffect(() => {
    api
      .subtitleSettings()
      .then(setDraft)
      .catch((e) => setError(String(e.message || e)))
    api
      .subtitleOverview()
      .then(setOverview)
      .catch(() => undefined)
  }, [api])

  if (error && !draft) return <Text variant="muted">{error}</Text>
  if (!draft) return null

  async function save() {
    if (!draft) return
    setSaving(true)
    setError('')
    try {
      setDraft(await api.saveSubtitleSettings(draft))
      setSaved(true)
      setTimeout(() => setSaved(false), 2500)
    } catch (e) {
      setError(String((e as Error).message || e))
    } finally {
      setSaving(false)
    }
  }

  const o = overview
  // the source field: the source's name as the data gives it, then how it stands
  const state = !o
    ? ''
    : !o.enabled
      ? 'switched off (subtitles.enabled)'
      : !o.configured
        ? `not set up: missing subtitles.opensubtitles.${(o.missing ?? []).join(', ')}`
        : o.problem
          ? `not working: ${o.problem}`
          : o.budget.account?.known
            ? `signed in · ${o.budget.account.allowed} downloads a day, ${o.budget.account.remaining} left today`
            : 'set up'
  const account = o ? `${o.source || 'the subtitle source'} · ${state}` : ''

  return (
    <div className="acq__settings">
      <div className="acq__settings-head">
        <div>
          <span className="acq__mono">subtitles</span>
          <Text variant="muted" as="div">
            new arrivals first, then the rest of the library, up to the daily limit.
          </Text>
        </div>
        <Button variant="primary" loading={saving} leading={<Save size={14} />} onClick={() => void save()}>
          {saved ? 'saved' : 'save'}
        </Button>
      </div>
      {error && <Text variant="muted">{error}</Text>}
      <div className="acq__settings-row">
        <Field
          label="languages"
          hint={
            o?.from?.setting
              ? o.from.scope === 'deployment'
                ? `set for acquire (${o.from.setting})`
                : `set for the whole platform (${o.from.setting})`
              : undefined
          }
        >
          <Text as="div">{o?.languages.length ? o.languages.map((l) => l.name).join(', ') : '—'}</Text>
        </Field>
        <Field label="source" hint="set in subtitles.opensubtitles.*">
          <Text as="div">{account || '—'}</Text>
        </Field>
        <Field
          label="automatic downloads a day"
          hint="the daily limit for automatic downloads (subtitles.dailyBudget); the rest of the account's downloads are for versions you pick"
        >
          <Text as="div">{o ? `${o.budget.daily} · ${o.budget.used} used today` : '—'}</Text>
        </Field>
      </div>
      <Field
        label="downloading"
        hint="on: the best match in each language is downloaded, up to the daily limit. off: titles are still searched, and only what you find or pick is downloaded."
      >
        <Switch
          checked={draft.auto}
          onChange={(e) => setDraft({ ...draft, auto: e.currentTarget.checked })}
          label={draft.auto ? 'downloads on its own' : 'downloads only what you ask for'}
        />
      </Field>
      <Field
        label="machine translations"
        hint="subtitles a source marks as machine- or AI-translated. off: never downloaded automatically, but you can still pick one."
      >
        <Switch
          checked={draft.allowMachineTranslated}
          onChange={(e) => setDraft({ ...draft, allowMachineTranslated: e.currentTarget.checked })}
          label={draft.allowMachineTranslated ? 'downloaded like any other' : 'never downloaded automatically'}
        />
      </Field>
      <Divider />
    </div>
  )
}

function QualityProfileSettings({ api, admin }: { api: Api; admin: boolean }) {
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

/**
 * How far TVmaze's air times are of the series that run, as the server counts
 * them: "TVmaze's air times: 790 of the 814 series that run read; 20 not known
 * to TVmaze …; 4 not read yet …". Nothing without a schedule source to ask.
 */
function airTimesLine(st: AirTimesStatus | undefined): string {
  if (!st || !st.on) return ''
  const parts: string[] = []
  if (st.running === 0) parts.push('no series that runs is tracked yet')
  else parts.push(`${st.read} of the ${st.running} series that run read`)
  if (st.unknown) parts.push(`${st.unknown} not known to TVmaze, told by their air date`)
  if (st.pending) {
    const earlier = st.earlier ?? 0
    const never = st.pending - earlier
    const what = [
      never ? `${never} not read yet` : '',
      earlier ? `${earlier} read by an earlier version of the rules, read again` : '',
    ]
      .filter(Boolean)
      .join(', ')
    parts.push(
      `${what} — nothing of them is grabbed on its own until they are, on the next refreshes` +
        (st.lastError ? ` (${st.lastError})` : ''),
    )
  }
  if (st.held) {
    const moved = st.heldMoved ? `, ${st.heldMoved} of them after TMDB moved ${st.heldMoved === 1 ? 'it' : 'them'}, until TVmaze is read again` : ''
    parts.push(
      `${st.held} ${st.held === 1 ? 'episode' : 'episodes'} held — not grabbed on ${st.held === 1 ? 'its' : 'their'} own until TVmaze dates ${st.held === 1 ? 'it' : 'them'} as TMDB does, or ${st.held === 1 ? 'it has' : 'they have'} an air time${moved}`,
    )
  }
  if (st.failingEnded) parts.push(`TVmaze did not answer for ${st.failingEnded} series that ended, when last asked`)
  return "TVmaze's air times: " + parts.join('; ')
}

/** How recently an episode aired to count as new, as the draft says or, while it says none, as the server does. */
function newDays(s: SearchSettings): number {
  return s.newWithinDays > 0 ? s.newWithinDays : 14
}

function splitList(v: string): string[] {
  return v
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean)
}
