// The embed entry point.
//
// The portal shell hosts this console IN ITS OWN PAGE — no iframe. It loads this
// module through its app proxy and calls mount(), handing over where the API
// lives (a proxied path) and the signed-in user's token. The console renders
// into the element the shell provides and never runs an auth flow of its own.
//
// The contract is deliberately plain ES modules rather than a bundler-level
// federation link: the shell and each app are built and deployed from separate
// repos on their own cadence, and a mount function survives that independence.
// The cost is a second React instance in the page — the app renders its own
// root — which is a fair trade for not version-locking every app to the shell.
import { createRoot, type Root } from 'react-dom/client'
import { StrictMode } from 'react'
import { Console } from './Console'
import '@nalet/design-system/styles.css'
import './app.css'

export interface MountOptions {
  /** Where the app's API is reachable from the browser, e.g.
   *  "/api/portal/apps/acquire/". Must end in a slash. */
  apiBase: string
  /** The signed-in user's access token; the console forwards it on every call. */
  token?: string
  /** Optional: the shell already knows. Otherwise derived from the token. */
  isAdmin?: boolean
  /** Called when the API reports the session has lapsed, so the shell can
   *  re-authenticate — the app must not start its own login. */
  onUnauthorized?: () => void
}

export interface MountHandle {
  /** Update the token (silent renew) or options without remounting. */
  update(opts: Partial<MountOptions>): void
  /** Tear the console down and release the React root. */
  unmount(): void
}

// In library mode the stylesheet is emitted next to the bundle rather than
// injected, so a bare import() would render the console unstyled. Resolve it
// relative to this module and add it once — the shell should not have to know
// the app's asset layout.
const STYLE_ID = 'acquire-console-styles'

function ensureStyles() {
  if (typeof document === 'undefined' || document.getElementById(STYLE_ID)) return
  const link = document.createElement('link')
  link.id = STYLE_ID
  link.rel = 'stylesheet'
  link.href = new URL('./console.css', import.meta.url).href
  document.head.appendChild(link)
}

export function mount(el: HTMLElement, opts: MountOptions): MountHandle {
  ensureStyles()
  const root: Root = createRoot(el)
  let current = opts

  const render = () =>
    root.render(
      <StrictMode>
        <Console
          apiBase={current.apiBase}
          token={current.token}
          isAdmin={current.isAdmin}
          onUnauthorized={current.onUnauthorized}
        />
      </StrictMode>,
    )

  render()
  return {
    update(patch) {
      current = { ...current, ...patch }
      render()
    },
    unmount() {
      root.unmount()
    },
  }
}

export default mount
