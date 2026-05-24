import { useEffect, useState } from 'react'
import {
  ArrowUp,
  FolderClosed,
  Gauge,
  HardDrive,
  SlidersHorizontal,
  Trash2,
  Users,
} from 'lucide-react'
import { toast } from 'sonner'

import { setClientOptions, type Torrent } from '@/lib/api'
import { formatBytes, formatETA, formatRate, formatRateInput, formatRelative } from '@/lib/format'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader } from '@/components/ui/card'
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

interface TorrentCardProps {
  torrent: Torrent
  onRemove: (id: string) => void
  removing?: boolean
}

export function TorrentCard({ torrent, onRemove, removing }: TorrentCardProps) {
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [speedOpen, setSpeedOpen] = useState(false)
  const [minInput, setMinInput] = useState('')
  const [maxInput, setMaxInput] = useState('')
  const [ratioInput, setRatioInput] = useState('')
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    if (speedOpen) {
      setMinInput(formatRateInput(torrent.minRateBytesPerSec))
      setMaxInput(formatRateInput(torrent.maxRateBytesPerSec))
      setRatioInput(String(torrent.maxRatio ?? 0))
    }
  }, [speedOpen, torrent.minRateBytesPerSec, torrent.maxRateBytesPerSec, torrent.maxRatio])

  const onSaveSpeed = async () => {
    const ratio = Number(ratioInput.trim())
    if (!Number.isFinite(ratio) || ratio < 0) {
      toast.error('Max ratio must be a non-negative number (0 = unlimited)')
      return
    }
    setSaving(true)
    try {
      await setClientOptions(torrent.id, minInput.trim(), maxInput.trim(), ratio)
      toast.success('Updated')
      setSpeedOpen(false)
    } catch (e) {
      toast.error(e instanceof Error ? e.message : 'Could not update')
    } finally {
      setSaving(false)
    }
  }

  const seeders = torrent.trackers.reduce((n, t) => n + t.seeders, 0)
  const leechers = torrent.trackers.reduce((n, t) => n + t.leechers, 0)

  return (
    <Card className="gap-0 overflow-hidden py-0">
      <CardHeader className="flex items-start justify-between gap-3 border-b py-4">
        <div className="min-w-0">
          <h3 className="truncate font-medium" title={torrent.name}>
            {torrent.name}
          </h3>
          <p
            className="text-muted-foreground mt-0.5 flex items-center gap-1 truncate font-mono text-xs"
            title={torrent.location}
          >
            <FolderClosed className="size-3 shrink-0" />
            <span className="truncate">{torrent.location}</span>
          </p>
        </div>
        <div className="flex shrink-0 items-center gap-1">
        <Dialog open={speedOpen} onOpenChange={setSpeedOpen}>
          <Button
            variant="ghost"
            size="icon"
            aria-label="Edit speed"
            className="text-muted-foreground hover:text-foreground shrink-0"
            onClick={() => setSpeedOpen(true)}
          >
            <SlidersHorizontal />
          </Button>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Edit upload speed</DialogTitle>
              <DialogDescription>
                Sets the simulated min/max upload rate for{' '}
                <span className="text-foreground font-medium">{torrent.name}</span>. Accepts values
                like <code>200K</code>, <code>1.5M</code>, <code>0</code>.
              </DialogDescription>
            </DialogHeader>
            <form
              onSubmit={(e) => {
                e.preventDefault()
                void onSaveSpeed()
              }}
            >
              <div className="grid gap-3 py-2">
                <div className="grid gap-1.5">
                  <Label htmlFor={`min-${torrent.id}`}>Minimum</Label>
                  <Input
                    id={`min-${torrent.id}`}
                    value={minInput}
                    onChange={(e) => setMinInput(e.target.value)}
                    placeholder="e.g. 200K"
                    autoComplete="off"
                    autoFocus
                  />
                </div>
                <div className="grid gap-1.5">
                  <Label htmlFor={`max-${torrent.id}`}>Maximum</Label>
                  <Input
                    id={`max-${torrent.id}`}
                    value={maxInput}
                    onChange={(e) => setMaxInput(e.target.value)}
                    placeholder="e.g. 1M"
                    autoComplete="off"
                  />
                </div>
                <div className="grid gap-1.5">
                  <Label htmlFor={`ratio-${torrent.id}`}>Max ratio</Label>
                  <Input
                    id={`ratio-${torrent.id}`}
                    type="number"
                    min="0"
                    step="0.1"
                    value={ratioInput}
                    onChange={(e) => setRatioInput(e.target.value)}
                    placeholder="0 = unlimited"
                    autoComplete="off"
                  />
                </div>
              </div>
              <DialogFooter className="mt-4">
                <DialogClose asChild>
                  <Button type="button" variant="outline" disabled={saving}>
                    Cancel
                  </Button>
                </DialogClose>
                <Button type="submit" disabled={saving}>
                  {saving ? 'Saving…' : 'Save'}
                </Button>
              </DialogFooter>
            </form>
          </DialogContent>
        </Dialog>
        <Dialog open={confirmOpen} onOpenChange={setConfirmOpen}>
          <Button
            variant="ghost"
            size="icon"
            aria-label="Remove torrent"
            className="text-muted-foreground hover:text-destructive shrink-0"
            disabled={removing}
            onClick={() => setConfirmOpen(true)}
          >
            <Trash2 />
          </Button>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Remove torrent?</DialogTitle>
              <DialogDescription>
                Stops seeding <span className="text-foreground font-medium">{torrent.name}</span>{' '}
                and sends a stopped announce to its trackers.
              </DialogDescription>
            </DialogHeader>
            <DialogFooter>
              <DialogClose asChild>
                <Button variant="outline">Cancel</Button>
              </DialogClose>
              <Button
                variant="destructive"
                onClick={() => {
                  setConfirmOpen(false)
                  onRemove(torrent.id)
                }}
              >
                Remove
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
        </div>
      </CardHeader>

      <CardContent className="grid grid-cols-2 gap-x-4 gap-y-3 py-4 sm:grid-cols-4">
        <Stat icon={<ArrowUp className="size-4" />} label="Uploaded">
          {formatBytes(torrent.uploadedBytes)}
        </Stat>
        <Stat icon={<Gauge className="size-4" />} label="Rate">
          {formatRate(torrent.rateBytesPerSec)}
        </Stat>
        <Stat icon={<Users className="size-4" />} label="Swarm">
          <span className="text-emerald-500">{seeders}</span>
          {' / '}
          <span className="text-amber-500">{leechers}</span>
        </Stat>
        <Stat icon={<HardDrive className="size-4" />} label="Size">
          {formatBytes(torrent.sizeBytes)}
        </Stat>
      </CardContent>

      <div className="bg-muted/30 border-t">
        {torrent.trackers.map((t) => (
          <div
            key={t.url}
            className="flex items-center justify-between gap-3 px-6 py-2.5 text-sm not-last:border-b"
          >
            <span className="text-muted-foreground truncate font-mono text-xs" title={t.url}>
              {trackerHost(t.url)}
            </span>
            <div className="flex shrink-0 items-center gap-3">
              <span className="text-muted-foreground text-xs tabular-nums">
                <span className="text-emerald-500">{t.seeders}</span>
                {' / '}
                <span className="text-amber-500">{t.leechers}</span>
              </span>
              <span className="text-muted-foreground hidden text-xs tabular-nums sm:inline">
                next {formatETA(t.nextAnnounceAt)}
              </span>
              <TrackerBadge status={t.status} />
            </div>
          </div>
        ))}
      </div>

      <div className="text-muted-foreground bg-muted/30 border-t px-6 py-2 text-xs">
        added {formatRelative(torrent.addedAt)}
      </div>
    </Card>
  )
}

function Stat({
  icon,
  label,
  children,
}: {
  icon: React.ReactNode
  label: string
  children: React.ReactNode
}) {
  return (
    <div>
      <div className="text-muted-foreground flex items-center gap-1.5 text-xs">
        {icon}
        {label}
      </div>
      <div className="mt-1 font-medium tabular-nums">{children}</div>
    </div>
  )
}

function TrackerBadge({ status }: { status: Torrent['trackers'][number]['status'] }) {
  if (status === 'failing') return <Badge variant="destructive">failing</Badge>
  if (status === 'pending') return <Badge variant="outline">pending</Badge>
  return (
    <Badge variant="secondary" className="text-emerald-500">
      ok
    </Badge>
  )
}

function trackerHost(url: string): string {
  try {
    return new URL(url).host
  } catch {
    return url
  }
}
