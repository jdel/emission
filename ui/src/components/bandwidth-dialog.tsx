import { useEffect, useState } from 'react'
import { toast } from 'sonner'

import { getBandwidth, setBandwidth, setUserBandwidth, type SeedingProfile } from '@/lib/api'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { formatRate, formatRateInput } from '@/lib/format'

interface BandwidthDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** Target user; omit to edit the current user's own bandwidth. */
  username?: string
  /** Prefill in bytes/sec (admin path); the own path fetches it instead. */
  initialBytes?: number
  /** Prefill profile (admin path); the own path fetches it instead. */
  initialProfile?: SeedingProfile
  onSaved?: () => void
}

const PROFILES: { value: SeedingProfile; label: string; hint: string }[] = [
  { value: 'stealth', label: 'Stealth', hint: 'Trickles; only ramps in big swarms' },
  { value: 'normal', label: 'Normal', hint: 'Balanced (default)' },
  { value: 'aggressive', label: 'Aggressive', hint: 'Near-max on almost any demand' },
]

/**
 * BandwidthDialog edits an upload-bandwidth ceiling — either the current user's
 * own (username omitted, fetched from the server) or a specific user's (admin).
 */
export function BandwidthDialog({
  open,
  onOpenChange,
  username,
  initialBytes,
  initialProfile,
  onSaved,
}: BandwidthDialogProps) {
  const [value, setValue] = useState('')
  const [profile, setProfile] = useState<SeedingProfile>('normal')
  const [serverDefault, setServerDefault] = useState<number | null>(null)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    if (!open) return
    if (username === undefined) {
      getBandwidth()
        .then((info) => {
          setValue(formatRateInput(info.bandwidth))
          setProfile(info.profile)
          setServerDefault(info.default)
        })
        .catch((e) => toast.error(e instanceof Error ? e.message : 'Load failed'))
      return
    }
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setValue(formatRateInput(initialBytes ?? 0))
    setProfile(initialProfile ?? 'normal')
    setServerDefault(null)
  }, [open, username, initialBytes, initialProfile])

  const onSave = async () => {
    setSaving(true)
    try {
      if (username === undefined) await setBandwidth(value.trim(), profile)
      else await setUserBandwidth(username, value.trim(), profile)
      toast.success('Bandwidth updated')
      onSaved?.()
      onOpenChange(false)
    } catch (e) {
      toast.error(e instanceof Error ? e.message : 'Could not update')
    } finally {
      setSaving(false)
    }
  }

  const title = username === undefined ? 'My bandwidth' : `Bandwidth for ${username}`

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>
            Total upload rate across all torrents on this account, shared between
            them by leecher count. It is a ceiling, left unused when leechers are
            scarce.
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={(e) => { e.preventDefault(); void onSave() }}>
          <div className="grid gap-1.5 py-2">
            <Label htmlFor="bw-input">
              Bandwidth{' '}
              <span className="text-muted-foreground font-normal">e.g. 2M, 500K</span>
            </Label>
            <Input
              id="bw-input"
              value={value}
              onChange={(e) => setValue(e.target.value)}
              placeholder="e.g. 2M"
              autoComplete="off"
              autoFocus
            />
            {serverDefault !== null && (
              <p className="text-muted-foreground text-xs">
                Server default: {formatRate(serverDefault)}
              </p>
            )}
          </div>
          <div className="grid gap-1.5 py-2">
            <Label>Seeding profile</Label>
            <div className="grid grid-cols-3 gap-2">
              {PROFILES.map((p) => (
                <button
                  key={p.value}
                  type="button"
                  onClick={() => setProfile(p.value)}
                  className={
                    'rounded-md border px-2 py-1.5 text-sm transition-colors ' +
                    (profile === p.value
                      ? 'border-primary bg-primary text-primary-foreground'
                      : 'border-input hover:bg-accent')
                  }
                >
                  {p.label}
                </button>
              ))}
            </div>
            <p className="text-muted-foreground text-xs">
              {PROFILES.find((p) => p.value === profile)?.hint}
            </p>
          </div>
          <DialogFooter className="mt-4">
            <DialogClose asChild>
              <Button type="button" variant="outline" disabled={saving}>Cancel</Button>
            </DialogClose>
            <Button type="submit" disabled={saving}>{saving ? 'Saving…' : 'Save'}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
