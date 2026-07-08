import { useRef } from 'react'
import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'

import { ThemeProvider } from '@/components/theme-provider'
import { DashboardHeader } from '@/components/dashboard-header'
import type { AuthControlsHandle } from '@/components/auth-controls'

// "My bandwidth" must be reachable from both the desktop icon row and the
// mobile burger menu, for every user (admin included) — regardless of
// whether auth is on. Regression test for a bug where the mobile menu only
// exposed it when auth was off, hiding it for every authenticated user.
function Harness(props: { authEnabled: boolean; isAdmin: boolean }) {
  const fileInputRef = useRef<HTMLInputElement>(null)
  const authRef = useRef<AuthControlsHandle>(null)
  return (
    <ThemeProvider>
      <DashboardHeader
        authEnabled={props.authEnabled}
        username={props.authEnabled ? (props.isAdmin ? 'admin' : 'alice') : undefined}
        isAdmin={props.isAdmin}
        uploading={false}
        wsConnected
        fileInputRef={fileInputRef}
        authRef={authRef}
        onAddClick={() => {}}
        onFilePicked={() => {}}
        onSignedOut={() => {}}
      />
    </ThemeProvider>
  )
}

describe.each([
  { authEnabled: false, isAdmin: false, label: 'auth off' },
  { authEnabled: true, isAdmin: false, label: 'auth on, regular user' },
  { authEnabled: true, isAdmin: true, label: 'auth on, admin' },
])('bandwidth menu access ($label)', ({ authEnabled, isAdmin }) => {
  it('exposes a "My bandwidth" trigger on the desktop toolbar', () => {
    render(<Harness authEnabled={authEnabled} isAdmin={isAdmin} />)
    // Auth-on mounts AuthControls twice (visible desktop copy + a hidden
    // mobile copy kept around for its ref/dialogs), so more than one match
    // is expected there — we just need at least one.
    expect(screen.getAllByRole('button', { name: 'My bandwidth' }).length).toBeGreaterThan(0)
  })

  it('exposes "My bandwidth" in the mobile burger menu', async () => {
    const user = userEvent.setup()
    render(<Harness authEnabled={authEnabled} isAdmin={isAdmin} />)
    await user.click(screen.getByRole('button', { name: 'Menu' }))
    expect(await screen.findByRole('menuitem', { name: /My bandwidth/ })).toBeInTheDocument()
  })
})
