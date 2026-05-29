import { useCallback, useEffect, useState } from 'react'

import { getAuthStatus, type AuthStatus } from '@/lib/api'
import { CookieNotice } from '@/components/cookie-notice'
import { Dashboard } from '@/components/dashboard'
import { Login } from '@/components/login'
import { Register } from '@/components/register'

// App is the authentication gate. It checks auth status, then renders the
// login or device-registration screen, or the dashboard.
function App() {
  const [status, setStatus] = useState<AuthStatus | null>(null)

  const refresh = useCallback(() => {
    getAuthStatus()
      .then(setStatus)
      .catch(() => setStatus({ authEnabled: true, authenticated: false }))
  }, [])

  useEffect(() => {
    refresh()
  }, [refresh])

  if (!status) {
    return (
      <div className="bg-background text-muted-foreground flex min-h-svh items-center justify-center text-sm">
        Loading…
      </div>
    )
  }

  // The cookie notice only matters when auth is on (no auth = no session
  // cookie set).
  const notice = status.authEnabled ? <CookieNotice /> : null

  // The bootstrap admin-registration screen lives at /start (server-gated:
  // the route only renders the SPA while the bootstrap window is open). A
  // logged-in user landing on it gets quietly sent home.
  const onStart = window.location.pathname === '/start'
  if (onStart && status.authenticated) {
    window.history.replaceState({}, '', '/')
  }

  if (status.authEnabled && !status.authenticated) {
    const invite = new URLSearchParams(window.location.search).get('invite')
    // An invite link → register that user. /start → register the admin.
    // Otherwise → log in.
    let screen
    if (invite) screen = <Register invite={invite} onDone={refresh} />
    else if (onStart) screen = <Register invite="" onDone={refresh} />
    else screen = <Login onDone={refresh} />
    return (
      <>
        {screen}
        {notice}
      </>
    )
  }

  return (
    <>
      <Dashboard
        authEnabled={status.authEnabled}
        username={status.username}
        onSignedOut={refresh}
      />
      {notice}
    </>
  )
}

export default App
