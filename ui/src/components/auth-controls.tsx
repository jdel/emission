import { forwardRef, useCallback, useEffect, useImperativeHandle, useState } from 'react'
import { ArrowLeft, LogOut, Plus, Smartphone, Trash2, Users, UserPlus } from 'lucide-react'
import { QRCodeSVG } from 'qrcode.react'
import { toast } from 'sonner'

import {
  ADMIN_USERNAME,
  createInvite,
  deleteMyAccount,
  listMyDevices,
  logout,
  removeMyDevice,
  type Device,
} from '@/lib/api'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { formatRelative } from '@/lib/format'
import { ConfirmDialog, type ConfirmDialogProps } from '@/components/confirm-dialog'
import { ManageUsers } from '@/components/manage-users'

// ---------------------------------------------------------------------------
// MyDevices — self-service device management for non-admin users
// ---------------------------------------------------------------------------

interface MyDevicesProps {
  username: string
  open: boolean
  onOpenChange: (open: boolean) => void
  onSignedOut: () => void
}

function MyDevices({ username, open, onOpenChange, onSignedOut }: MyDevicesProps) {
  const [devices, setDevices] = useState<Device[]>([])
  const [loading, setLoading] = useState(false)
  const [view, setView] = useState<'list' | 'newDevice'>('list')
  const [inviteURL, setInviteURL] = useState('')
  const [inviteCode, setInviteCode] = useState('')
  const [busy, setBusy] = useState(false)
  const [pending, setPending] = useState<Omit<ConfirmDialogProps, 'open' | 'onOpenChange'> | null>(null)

  const refresh = useCallback(() => {
    setLoading(true)
    listMyDevices()
      .then(setDevices)
      .catch((e) => toast.error(`Load failed: ${String(e)}`))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    if (open) { setView('list'); refresh() }
  }, [open, refresh])

  async function addDevice() {
    setBusy(true)
    try {
      const { url, code } = await createInvite(username)
      setInviteURL(url)
      setInviteCode(code)
      setView('newDevice')
    } catch (e) {
      toast.error(`Could not create invite: ${String(e)}`)
    } finally {
      setBusy(false)
    }
  }

  function delDevice(id: string) {
    if (devices.length === 1) {
      setPending({
        title: 'Delete your account?',
        description:
          'This is your last device. Unregistering it will permanently delete your account and all your torrents. This cannot be undone.',
        confirmLabel: 'Delete my account',
        danger: true,
        onConfirm: async () => {
          try {
            await deleteMyAccount()
            onSignedOut()
          } catch (e) {
            toast.error(`Delete failed: ${String(e)}`)
          }
        },
      })
    } else {
      setPending({
        title: 'Unregister this device?',
        description: 'You will no longer be able to log in with this device.',
        confirmLabel: 'Unregister',
        onConfirm: async () => {
          try {
            await removeMyDevice(id)
            refresh()
          } catch (e) {
            toast.error(`Remove failed: ${String(e)}`)
          }
        },
      })
    }
  }

  return (
    <>
    {pending && (
      <ConfirmDialog
        open
        onOpenChange={(v) => { if (!v) setPending(null) }}
        {...pending}
      />
    )}
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {view === 'newDevice' ? (
              <button
                className="flex items-center gap-1 text-sm font-medium"
                onClick={() => { setView('list'); refresh() }}
              >
                <ArrowLeft className="h-4 w-4" /> My devices
              </button>
            ) : (
              'My devices'
            )}
          </DialogTitle>
          <DialogDescription>
            {view === 'newDevice'
              ? 'Scan the QR code or share the link on your new device. One-time use, expires in 24 hours.'
              : 'Manage devices registered to your account.'}
          </DialogDescription>
        </DialogHeader>

        {view === 'newDevice' ? (
          <div className="flex flex-col items-center gap-4">
            <div className="rounded-lg bg-white p-3">
              <QRCodeSVG value={inviteURL} size={184} />
            </div>
            <p className="text-center font-mono text-lg font-medium tracking-tight">{inviteCode}</p>
            <div className="flex w-full gap-2">
              <Input readOnly value={inviteURL} onFocus={(e) => e.target.select()} className="font-mono text-xs" />
              <Button
                onClick={() =>
                  navigator.clipboard
                    .writeText(inviteURL)
                    .then(() => toast.success('Invite link copied'))
                    .catch(() => toast.error('Could not copy — select the link manually'))
                }
              >
                Copy
              </Button>
            </div>
          </div>
        ) : loading ? (
          <p className="text-muted-foreground py-6 text-center text-sm">Loading…</p>
        ) : (
          <div className="flex flex-col gap-3">
            <Button variant="outline" size="sm" className="self-start" onClick={addDevice} disabled={busy}>
              <Plus className="mr-1 h-4 w-4" /> Register new device
            </Button>
            <ul className="flex flex-col gap-2">
              {devices.map((d) => (
                <li key={d.id} className="flex items-center justify-between rounded-lg border p-3 text-sm">
                  <span className="text-muted-foreground">Device · added {formatRelative(d.addedAt)}</span>
                  <Button
                    variant="ghost"
                    size="icon"
                    aria-label="Unregister device"
                    className="hover:text-destructive"
                    onClick={() => delDevice(d.id)}
                  >
                    <Trash2 />
                  </Button>
                </li>
              ))}
            </ul>
          </div>
        )}
      </DialogContent>
    </Dialog>
    </>
  )
}

type Phase = 'closed' | 'ask' | 'show'

export interface AuthControlsHandle {
  openInvite: () => void
  openManage: () => void
  addDevice: () => void
}

interface AuthControlsProps {
  username?: string
  onSignedOut: () => void
}

export const AuthControls = forwardRef<AuthControlsHandle, AuthControlsProps>(
  function AuthControls({ username, onSignedOut }, ref) {
    const [phase, setPhase] = useState<Phase>('closed')
    const [askName, setAskName] = useState('')
    const [inviteURL, setInviteURL] = useState('')
    const [inviteCode, setInviteCode] = useState('')
    const [inviteFor, setInviteFor] = useState('')
    const [busy, setBusy] = useState(false)
    const [manageOpen, setManageOpen] = useState(false)
    const [myDevicesOpen, setMyDevicesOpen] = useState(false)
    const [isSelf, setIsSelf] = useState(false)

    const isAdmin = username === ADMIN_USERNAME
    const invitingSelf = !isAdmin && askName.length > 0 && askName === username

    useImperativeHandle(ref, () => ({
      openInvite: () => { setAskName(''); setPhase('ask') },
      openManage: () => setManageOpen(true),
      addDevice: () => { if (username) void mintInvite(username, true) },
    }))

    async function mintInvite(forUser: string, self = false) {
      setBusy(true)
      try {
        const { url, code } = await createInvite(forUser)
        setInviteURL(url)
        setInviteCode(code)
        setInviteFor(forUser)
        setIsSelf(self)
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
        {!isAdmin && (
          <Button
            variant="ghost"
            size="icon"
            aria-label="Manage my devices"
            disabled={!username}
            onClick={() => setMyDevicesOpen(true)}
          >
            <Smartphone />
          </Button>
        )}
        <Button
          variant="ghost"
          size="icon"
          aria-label="Invite a user"
          onClick={() => { setAskName(''); setPhase('ask') }}
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
        {username && !isAdmin && (
          <MyDevices
            username={username}
            open={myDevicesOpen}
            onOpenChange={setMyDevicesOpen}
            onSignedOut={onSignedOut}
          />
        )}

        <Dialog open={phase !== 'closed'} onOpenChange={(open) => !open && setPhase('closed')}>
          <DialogContent>
            {phase === 'ask' && (
              <>
                <DialogHeader>
                  <DialogTitle>Invite a user</DialogTitle>
                  <DialogDescription>Enter a username.</DialogDescription>
                </DialogHeader>
                <form
                  className="flex gap-2"
                  onSubmit={(e) => {
                    e.preventDefault()
                    if (!busy && askName.length > 0 && !invitingSelf) void mintInvite(askName)
                  }}
                >
                  <Input
                    value={askName}
                    onChange={(e) =>
                      setAskName(e.target.value.replace(/[^A-Za-z]/g, '').slice(0, 20))
                    }
                    placeholder="Username (letters only)"
                    maxLength={20}
                    autoFocus
                  />
                  <Button type="submit" disabled={busy || askName.length === 0 || invitingSelf}>
                    Invite
                  </Button>
                </form>
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
                  <DialogTitle>
                    {isSelf ? 'Add a new device' : <>Invite for &ldquo;{inviteFor}&rdquo;</>}
                  </DialogTitle>
                  <DialogDescription>
                    {isSelf
                      ? 'Open this link on the new device and follow the prompts to register it. One-time use, expires in 24 hours.'
                      : 'Scan the QR code on the new device, share the link, or read the code aloud. One-time use, expires in 24 hours.'}
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
)
