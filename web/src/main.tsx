import { StrictMode, useEffect, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { AuthProvider, useAuth } from 'react-oidc-context'
import { WebStorageStateStore } from 'oidc-client-ts'
import { Button, Card, Heading, Spinner, Text } from '@nalet/design-system'
import '@nalet/design-system/styles.css'
import './app.css'
import { App } from './App'
import { apiBase, type Config } from './lib/api'

// The console is mounted wherever the deployment publishes it ('/acquire/' on
// the reference host), so the OIDC redirect target is derived, not hardcoded.
const redirectUri = window.location.origin + apiBase

// The IdP redirects back to the bare mount point, so the fragment identifying
// the requested surface (#/settings from a launchpad tile) does not survive the
// round trip. Park it before leaving and restore it on the way back.
const ROUTE_KEY = 'acquire.route'

function signIn(auth: ReturnType<typeof useAuth>) {
  if (window.location.hash) sessionStorage.setItem(ROUTE_KEY, window.location.hash)
  void auth.signinRedirect()
}

function Gate({ config }: { config: Config }) {
  const auth = useAuth()

  // Strip the auth code from the URL once the exchange is done, and put the
  // caller back on the surface they asked for.
  useEffect(() => {
    if (!auth.isAuthenticated) return
    const parked = sessionStorage.getItem(ROUTE_KEY)
    if (parked) sessionStorage.removeItem(ROUTE_KEY)
    if (window.location.search.includes('code=')) {
      const url = new URL(window.location.href)
      const q = url.searchParams.get('q')
      window.history.replaceState(
        {},
        '',
        apiBase + (q ? `?q=${encodeURIComponent(q)}` : '') + (parked || window.location.hash || ''),
      )
      if (parked) window.dispatchEvent(new HashChangeEvent('hashchange'))
    }
  }, [auth.isAuthenticated])

  if (auth.activeNavigator || auth.isLoading) {
    return (
      <div className="acq__center">
        <Spinner />
      </div>
    )
  }
  if (auth.error) {
    return (
      <div className="acq__center">
        <Card>
          <Heading level={2}>sign-in failed</Heading>
          <Text variant="muted" as="p">
            {auth.error.message}
          </Text>
          <Button variant="primary" onClick={() => signIn(auth)}>
            try again
          </Button>
        </Card>
      </div>
    )
  }
  if (!auth.isAuthenticated) {
    return (
      <div className="acq__center">
        <Card>
          <Heading level={2} chevron>
            acquire
          </Heading>
          <Text variant="muted" as="p">
            Sign in to request and manage downloads.
          </Text>
          <Button variant="primary" onClick={() => signIn(auth)}>
            sign in
          </Button>
        </Card>
      </div>
    )
  }
  return <App config={config} />
}

function Boot() {
  const [config, setConfig] = useState<Config | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    fetch(apiBase + 'api/config')
      .then((r) => r.json())
      .then(setConfig)
      .catch((e) => setError(String(e)))
  }, [])

  if (error)
    return (
      <div className="acq__center">
        <Text variant="muted">acquire is unreachable: {error}</Text>
      </div>
    )
  if (!config)
    return (
      <div className="acq__center">
        <Spinner />
      </div>
    )

  return (
    <AuthProvider
      authority={config.oidcIssuer}
      client_id={config.oidcClientId}
      redirect_uri={redirectUri}
      post_logout_redirect_uri={redirectUri}
      scope="openid profile email"
      // Keep the session across reloads and renew it silently, instead of
      // dropping the user back to the sign-in panel.
      userStore={new WebStorageStateStore({ store: window.localStorage })}
      automaticSilentRenew
      // Keep the fragment: Gate's effect restores the requested surface.
      onSigninCallback={() => {
        const parked = sessionStorage.getItem(ROUTE_KEY) || window.location.hash
        window.history.replaceState({}, '', apiBase + parked)
      }}
    >
      <Gate config={config} />
    </AuthProvider>
  )
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <Boot />
  </StrictMode>,
)
