import { useState } from 'react'
import { LogOut, Smartphone, Users, UserPlus } from 'lucide-react'
import { QRCodeSVG } from 'qrcode.react'
import { toast } from 'sonner'

import { ADMIN_USERNAME, createInvite, logout } from '@/lib/api'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { ManageUsers } from '@/components/manage-users'

type Phase = 'closed' | 'ask' | 'show'

// AuthControls is the header cluster shown when authenticated: add one of your
// own devices, invite another user, and sign out.
export function AuthControls({
  username,
  onSignedOut,
}: {
  username?: string
  onSignedOut: () => void
}) {
  const [phase, setPhase] = useState<Phase>('closed')
  const [askName, setAskName] = useState('')
  const [inviteURL, setInviteURL] = useState('')
  const [inviteCode, setInviteCode] = useState('')
  const [inviteFor, setInviteFor] = useState('')
  const [busy, setBusy] = useState(false)
  const [manageOpen, setManageOpen] = useState(false)

  const isAdmin = username === ADMIN_USERNAME

  // A non-admin typing their own name should use "Add a device" instead —
  // "Invite a user" is for other people.
  const invitingSelf = !isAdmin && askName.length > 0 && askName === username

  async function mintInvite(forUser: string) {
    setBusy(true)
    try {
      const { url, code } = await createInvite(forUser)
      setInviteURL(url)
      setInviteCode(code)
      setInviteFor(forUser)
      setPhase('show')
    } catch (e) {
      toast.error(`Invite failed: ${String(e)}`)
    } finally {
      setBusy(false)
    }
  }

  async function signOut() {
    try {
      await logout()
    } finally {
      onSignedOut()
    }
  }

  function copy() {
    navigator.clipboard
      .writeText(inviteURL)
      .then(() => toast.success('Invite link copied'))
      .catch(() => toast.error('Could not copy — select the link manually'))
  }

  return (
    <>
      {/* The admin has exactly one device, so it cannot add more. */}
      {!isAdmin && (
        <Button
          variant="ghost"
          size="icon"
          aria-label="Add one of your devices"
          disabled={!username}
          onClick={() => username && mintInvite(username)}
        >
          <Smartphone />
        </Button>
      )}
      <Button
        variant="ghost"
        size="icon"
        aria-label="Invite a user"
        onClick={() => {
          setAskName('')
          setPhase('ask')
        }}
      >
        <UserPlus />
      </Button>
      {isAdmin && (
        <Button
          variant="ghost"
          size="icon"
          aria-label="Manage users"
          onClick={() => setManageOpen(true)}
        >
          <Users />
        </Button>
      )}
      <Button variant="ghost" size="icon" aria-label="Sign out" onClick={signOut}>
        <LogOut />
      </Button>

      <ManageUsers open={manageOpen} onOpenChange={setManageOpen} />

      <Dialog open={phase !== 'closed'} onOpenChange={(open) => !open && setPhase('closed')}>
        <DialogContent>
          {phase === 'ask' && (
            <>
              <DialogHeader>
                <DialogTitle>Invite a user</DialogTitle>
                <DialogDescription>
                  Enter a username. A new name creates a new user; an existing
                  one adds another device for them.
                </DialogDescription>
              </DialogHeader>
              <div className="flex gap-2">
                <Input
                  value={askName}
                  onChange={(e) =>
                    setAskName(e.target.value.replace(/[^A-Za-z]/g, '').slice(0, 20))
                  }
                  placeholder="Username (letters only)"
                  maxLength={20}
                  autoFocus
                />
                <Button
                  disabled={busy || askName.length === 0 || invitingSelf}
                  onClick={() => mintInvite(askName)}
                >
                  Create
                </Button>
              </div>
              {invitingSelf && (
                <p className="text-sm text-amber-500">
                  That&rsquo;s your own username. To add another of your own
                  devices, use the &ldquo;Add a device&rdquo; button instead.
                </p>
              )}
            </>
          )}
          {phase === 'show' && (
            <>
              <DialogHeader>
                <DialogTitle>Invite for &ldquo;{inviteFor}&rdquo;</DialogTitle>
                <DialogDescription>
                  Scan the QR code on the new device, share the link, or read
                  the code aloud. One-time use, expires in 24 hours.
                </DialogDescription>
              </DialogHeader>
              <div className="flex flex-col items-center gap-4">
                <div className="rounded-lg bg-white p-3">
                  <QRCodeSVG value={inviteURL} size={184} />
                </div>
                <p className="text-center font-mono text-lg font-medium tracking-tight">
                  {inviteCode}
                </p>
                <div className="flex w-full gap-2">
                  <Input
                    readOnly
                    value={inviteURL}
                    onFocus={(e) => e.target.select()}
                    className="font-mono text-xs"
                  />
                  <Button onClick={copy}>Copy</Button>
                </div>
              </div>
            </>
          )}
        </DialogContent>
      </Dialog>
    </>
  )
}
