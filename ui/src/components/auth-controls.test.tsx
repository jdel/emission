import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'

import { AuthControls } from '@/components/auth-controls'

describe('AuthControls admin gating', () => {
  it('shows "Manage users" but not "Manage my devices" for the admin', () => {
    render(<AuthControls username="admin" onSignedOut={() => {}} />)
    expect(screen.getByRole('button', { name: 'Manage users' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Manage my devices' })).not.toBeInTheDocument()
  })

  it('shows "Manage my devices" but not "Manage users" for a regular user', () => {
    render(<AuthControls username="alice" onSignedOut={() => {}} />)
    expect(screen.getByRole('button', { name: 'Manage my devices' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Manage users' })).not.toBeInTheDocument()
  })
})

describe('AuthControls self-invite guard', () => {
  // UI-only nudge toward "Add device" — the backend does not reject a
  // self-targeted invite (creator is derived from the session, so it can't
  // be used to invite as someone else), so this is guidance, not a security
  // boundary.
  it('disables inviting your own username and explains why', async () => {
    const user = userEvent.setup()
    render(<AuthControls username="alice" onSignedOut={() => {}} />)
    await user.click(screen.getByRole('button', { name: 'Invite a user' }))
    await user.type(screen.getByPlaceholderText('Username (letters only)'), 'alice')
    expect(screen.getByRole('button', { name: 'Invite' })).toBeDisabled()
    expect(screen.getByText(/own username/)).toBeInTheDocument()
  })

  it('allows inviting a different username', async () => {
    const user = userEvent.setup()
    render(<AuthControls username="alice" onSignedOut={() => {}} />)
    await user.click(screen.getByRole('button', { name: 'Invite a user' }))
    await user.type(screen.getByPlaceholderText('Username (letters only)'), 'bob')
    expect(screen.getByRole('button', { name: 'Invite' })).toBeEnabled()
  })
})
