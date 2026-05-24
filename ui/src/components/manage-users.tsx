import { useCallback, useEffect, useState } from 'react'
import { Trash2 } from 'lucide-react'
import { toast } from 'sonner'

import {
  ADMIN_USERNAME,
  listUsers,
  removeCredential,
  removeUser,
  type Device,
} from '@/lib/api'
import { formatRelative } from '@/lib/format'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'

interface ManageUsersProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

// ManageUsers is the admin dialog for inspecting and removing users and their
// passkeys. The admin's own user and device are protected and have no remove
// controls.
export function ManageUsers({ open, onOpenChange }: ManageUsersProps) {
  const [devices, setDevices] = useState<Device[]>([])
  const [loading, setLoading] = useState(false)

  const refresh = useCallback(() => {
    setLoading(true)
    listUsers()
      .then(setDevices)
      .catch((e) => toast.error(`Load failed: ${String(e)}`))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => {
    if (open) refresh()
  }, [open, refresh])

  async function delDevice(id: string) {
    try {
      await removeCredential(id)
      toast.success('Device removed')
      refresh()
    } catch (e) {
      toast.error(`Remove failed: ${String(e)}`)
    }
  }

  async function delUser(username: string) {
    if (
      !window.confirm(
        `Remove "${username}"? This deletes all their passkeys and torrents. This cannot be undone.`,
      )
    ) {
      return
    }
    try {
      await removeUser(username)
      toast.success(`Removed ${username}`)
      refresh()
    } catch (e) {
      toast.error(`Remove failed: ${String(e)}`)
    }
  }

  // Group devices by username, admin first.
  const groups = new Map<string, Device[]>()
  for (const d of devices) {
    const list = groups.get(d.username) ?? []
    list.push(d)
    groups.set(d.username, list)
  }
  const usernames = [...groups.keys()].sort((a, b) =>
    a === ADMIN_USERNAME ? -1 : b === ADMIN_USERNAME ? 1 : a.localeCompare(b),
  )

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Users &amp; devices</DialogTitle>
          <DialogDescription>
            Remove a single passkey, or a whole user — removing a user also
            deletes their torrents.
          </DialogDescription>
        </DialogHeader>

        {loading ? (
          <p className="text-muted-foreground py-6 text-center text-sm">Loading…</p>
        ) : (
          <div className="flex max-h-96 flex-col gap-3 overflow-y-auto">
            {usernames.map((username) => {
              const admin = username === ADMIN_USERNAME
              return (
                <div key={username} className="rounded-lg border p-3">
                  <div className="flex items-center justify-between">
                    <span className="font-medium">
                      {username}
                      {admin && <span className="text-muted-foreground"> · admin</span>}
                    </span>
                    {!admin && (
                      <Button
                        variant="ghost"
                        size="sm"
                        className="text-destructive"
                        onClick={() => delUser(username)}
                      >
                        Remove user
                      </Button>
                    )}
                  </div>
                  <ul className="mt-2 flex flex-col gap-1">
                    {(groups.get(username) ?? []).map((d) => (
                      <li
                        key={d.id}
                        className="text-muted-foreground flex items-center justify-between text-sm"
                      >
                        <span>Passkey · added {formatRelative(d.addedAt)}</span>
                        {!admin && (
                          <Button
                            variant="ghost"
                            size="icon"
                            aria-label="Remove passkey"
                            className="hover:text-destructive"
                            onClick={() => delDevice(d.id)}
                          >
                            <Trash2 />
                          </Button>
                        )}
                      </li>
                    ))}
                  </ul>
                </div>
              )
            })}
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}
