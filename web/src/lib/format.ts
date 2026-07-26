// Display helpers. Deliberately terse and lowercase, to match the house voice.

const UNITS = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']

export function bytes(n: number | null | undefined): string {
  if (!n || n <= 0) return ''
  let v = n
  let i = 0
  while (v >= 1024 && i < UNITS.length - 1) {
    v /= 1024
    i++
  }
  return `${v >= 10 || i === 0 ? Math.round(v) : v.toFixed(1)} ${UNITS[i]}`
}

export function speed(bps: number | null | undefined): string {
  return bps && bps > 0 ? `${bytes(bps)}/s` : ''
}

export function eta(sec: number | null | undefined): string {
  if (sec == null || sec < 0) return ''
  if (sec < 60) return `${sec}s`
  if (sec < 3600) return `${Math.round(sec / 60)}m`
  const h = Math.floor(sec / 3600)
  // Anything past a few days is noise — a stalled usenet job reports years.
  if (h > 72) return '—'
  return `${h}h ${Math.round((sec % 3600) / 60)}m`
}

export function pct(n: number | null | undefined): string {
  return `${Math.max(0, Math.min(100, n || 0)).toFixed(0)}%`
}

/** health is reported 0-1000 by usenet clients. */
export function health(h: number | null | undefined): string {
  return h == null ? '' : `${Math.round(h / 10)}%`
}

/**
 * Realm roles live in the ACCESS token, not in the ID-token profile that
 * oidc-client-ts exposes as `user.profile` — reading the profile silently
 * yields no roles, so the console would render as if you were not an admin.
 */
export function realmRoles(accessToken: string | undefined): string[] {
  if (!accessToken) return []
  try {
    const body = accessToken.split('.')[1]
    if (!body) return []
    const json = atob(body.replace(/-/g, '+').replace(/_/g, '/'))
    const claims = JSON.parse(json) as { realm_access?: { roles?: string[] } }
    return claims.realm_access?.roles ?? []
  } catch {
    return []
  }
}

export function ago(iso: string | null | undefined): string {
  if (!iso) return ''
  const then = new Date(iso).getTime()
  if (!Number.isFinite(then)) return ''
  const s = Math.max(0, Math.round((Date.now() - then) / 1000))
  if (s < 60) return 'just now'
  if (s < 3600) return `${Math.round(s / 60)}m ago`
  if (s < 86400) return `${Math.round(s / 3600)}h ago`
  return `${Math.round(s / 86400)}d ago`
}
