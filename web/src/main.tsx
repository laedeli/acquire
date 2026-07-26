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

function Gate({ config }: { config: Config }) {
  const auth = useAuth()

  // Strip the auth code from the URL once the exchange is done.
  useEffect(() => {
    if (auth.isAuthenticated && window.location.search.includes('code=')) {
      const url = new URL(window.location.href)
      const q = url.searchParams.get('q')
      window.history.replaceState({}, '', apiBase + (q ? `?q=${encodeURIComponent(q)}` : ''))
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
          <Button variant="primary" onClick={() => void auth.signinRedirect()}>
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
          <Button variant="primary" onClick={() => void auth.signinRedirect()}>
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
      onSigninCallback={() => window.history.replaceState({}, '', apiBase)}
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
