import { useEffect, useRef, useState } from 'react'
import {
  ArrowUp,
  ChevronDown,
  FolderClosed,
  Gauge,
  Globe,
  HardDrive,
  Lock,
  Scale,
  SlidersHorizontal,
  Trash2,
  Users,
} from 'lucide-react'
import { Tooltip } from 'radix-ui'
import { toast } from 'sonner'

import { setClientOptions, type StatsPoint, type Torrent } from '@/lib/api'
import { formatBytes, formatDateTime, formatETA, formatRate, formatRateInput, formatRelative, formatUptime } from '@/lib/format'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader } from '@/components/ui/card'
import { Checkbox } from '@/components/ui/checkbox'
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

function useNow(): number {
  const [now, setNow] = useState(Date.now)
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 60_000)
    return () => clearInterval(id)
  }, [])
  return now
}

function useLerpValue(target: number): number {
  const [display, setDisplay] = useState(target)
  const valueRef = useRef(target)
  const rafRef = useRef(0)

  useEffect(() => {
    const animate = () => {
      const diff = target - valueRef.current
      if (Math.abs(diff) < 512) {
        valueRef.current = target
        setDisplay(target)
        rafRef.current = 0
        return
      }
      valueRef.current += diff * 0.15
      setDisplay(Math.round(valueRef.current))
      rafRef.current = requestAnimationFrame(animate)
    }
    cancelAnimationFrame(rafRef.current)
    rafRef.current = requestAnimationFrame(animate)
    return () => cancelAnimationFrame(rafRef.current)
  }, [target])

  return display
}

interface TorrentCardProps {
  torrent: Torrent
  onRemove: (id: string) => void
  removing?: boolean
  compact?: boolean
  statsPoints?: StatsPoint[]
}

export function TorrentCard({ torrent, onRemove, removing, compact, statsPoints }: TorrentCardProps) {
  const now = useNow()
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [speedOpen, setSpeedOpen] = useState(false)
  const [detailsOpen, setDetailsOpen] = useState(false)
  const [maxInput, setMaxInput] = useState('')
  const [ratioInput, setRatioInput] = useState('')
  const [deleteOnCapInput, setDeleteOnCapInput] = useState(false)
  const [saving, setSaving] = useState(false)

  const smoothRate = useLerpValue(torrent.rateBytesPerSec)

  function openSpeedDialog() {
    setMaxInput(formatRateInput(torrent.maxRateBytesPerSec))
    setRatioInput(String(torrent.maxRatio ?? 0))
    setDeleteOnCapInput(torrent.deleteOnCap)
    setSpeedOpen(true)
  }

  const onSaveSpeed = async () => {
    const ratio = Number(ratioInput.trim())
    if (!Number.isFinite(ratio) || ratio < 0) {
      toast.error('Max ratio must be a non-negative number (0 = unlimited)')
      return
    }
    setSaving(true)
    try {
      await setClientOptions(torrent.id, maxInput.trim(), ratio, deleteOnCapInput)
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
  const torrentState = torrent.capped ? 'capped' : leechers === 0 ? 'waiting' : 'seeding'
  const borderColor = { seeding: 'border-l-emerald-500', waiting: 'border-l-sky-500', capped: 'border-l-amber-500' }[torrentState]

  const ratioProgress =
    torrent.maxRatio > 0 && torrent.sizeBytes > 0
      ? Math.min(torrent.uploadedBytes / (torrent.sizeBytes * torrent.maxRatio), 1)
      : -1

  const editDialog = (
    <Dialog open={speedOpen} onOpenChange={setSpeedOpen}>
      <DialogContent>
        <DialogHeader className="min-w-0">
          <DialogTitle className="truncate">Edit settings</DialogTitle>
          <DialogDescription className="truncate font-medium text-foreground/80" title={torrent.name}>
            {torrent.name}
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={(e) => { e.preventDefault(); void onSaveSpeed() }}>
          <div className="grid gap-3 py-2">
            <div className="grid gap-1.5">
              <Label htmlFor={`max-${torrent.id}`}>
                Maximum upload rate{' '}
                <span className="text-muted-foreground font-normal">e.g. 1M</span>
              </Label>
              <Input
                id={`max-${torrent.id}`}
                value={maxInput}
                onChange={(e) => setMaxInput(e.target.value)}
                placeholder="e.g. 1M"
                autoComplete="off"
                autoFocus
              />
            </div>
            <div className="grid gap-1.5">
              <Label htmlFor={`ratio-${torrent.id}`}>
                Max ratio{' '}
                <span className="text-muted-foreground font-normal">0 = seed forever</span>
              </Label>
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
            <div className="flex items-center gap-2.5">
              <Checkbox
                id={`doc-${torrent.id}`}
                checked={deleteOnCapInput}
                onCheckedChange={(v) => setDeleteOnCapInput(v === true)}
              />
              <Label htmlFor={`doc-${torrent.id}`} className="cursor-pointer font-normal">
                Remove when capped
              </Label>
            </div>
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

  const confirmDialog = (
    <Dialog open={confirmOpen} onOpenChange={setConfirmOpen}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Remove torrent?</DialogTitle>
          <DialogDescription>
            Stops seeding{' '}
            <span className="text-foreground font-medium">{torrent.name}</span> and sends a stopped
            announce to its trackers.
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <DialogClose asChild>
            <Button variant="outline">Cancel</Button>
          </DialogClose>
          <Button variant="destructive" onClick={() => { setConfirmOpen(false); onRemove(torrent.id) }}>
            Remove
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )

  if (compact) {
    const currentRatio = torrent.sizeBytes > 0 ? torrent.uploadedBytes / torrent.sizeBytes : null

    return (
      <>
        <div className={`relative flex items-center gap-3 overflow-hidden px-4 py-2.5 border-l-[3px] ${borderColor} transition-colors hover:bg-muted/30`}>
          <StatusDot state={torrentState} />
          <span className="min-w-0 flex-1 truncate text-sm font-medium" title={torrent.name}>
            {torrent.name}
          </span>
          {torrent.deleteOnCap && (
            <Badge variant="outline" className="text-muted-foreground shrink-0 text-xs">
              auto-remove
            </Badge>
          )}
          <Tip label="Uploaded">
            <span className="text-muted-foreground w-20 text-right text-xs tabular-nums">
              {formatBytes(torrent.uploadedBytes)}
            </span>
          </Tip>
          <Tip label="Rate">
            <span className="text-muted-foreground hidden w-24 text-right text-xs tabular-nums sm:block">
              {formatRate(smoothRate)}
            </span>
          </Tip>
          <Tip label="Ratio">
            <span className="text-muted-foreground hidden w-14 text-right text-xs tabular-nums sm:block">
              {currentRatio !== null ? currentRatio.toFixed(2) : '—'}
            </span>
          </Tip>
          <div className="flex shrink-0 items-center gap-0.5">
            <Button
              variant="ghost"
              size="icon"
              className="text-muted-foreground hover:text-foreground size-7"
              onClick={openSpeedDialog}
            >
              <SlidersHorizontal className="size-3.5" />
            </Button>
            <Button
              variant="ghost"
              size="icon"
              className="text-muted-foreground hover:text-destructive size-7"
              disabled={removing}
              onClick={() => setConfirmOpen(true)}
            >
              <Trash2 className="size-3.5" />
            </Button>
          </div>
          {ratioProgress >= 0 && (
            <div className="absolute bottom-0 left-0 right-0 h-[2px]">
              <div
                className={`h-full transition-all duration-1000 ${torrent.capped ? 'bg-amber-500/50' : 'bg-primary/35'}`}
                style={{ width: `${ratioProgress * 100}%` }}
              />
            </div>
          )}
        </div>
        {editDialog}
        {confirmDialog}
      </>
    )
  }

  return (
    <Card className={`gap-0 overflow-hidden border-l-[3px] py-0 ${borderColor}`}>
      <CardHeader className="flex items-start justify-between gap-3 border-b py-4">
        <button
          type="button"
          className="min-w-0 flex-1 cursor-pointer select-none text-left"
          onClick={() => setDetailsOpen((o) => !o)}
          aria-expanded={detailsOpen}
        >
          <div className="flex items-center gap-2">
            <h3 className="truncate font-medium" title={torrent.name}>
              {torrent.name}
            </h3>
            {torrent.deleteOnCap && (
              <Badge variant="outline" className="text-muted-foreground shrink-0 text-xs">
                auto-remove
              </Badge>
            )}
            <ChevronDown
              className={`text-muted-foreground ml-auto size-3.5 shrink-0 transition-transform duration-200 ${detailsOpen ? 'rotate-180' : ''}`}
            />
          </div>
          <p
            className="text-muted-foreground mt-0.5 flex items-center gap-1 truncate font-mono text-xs"
            title={torrent.location}
          >
            <FolderClosed className="size-3 shrink-0" />
            <span className="truncate">{torrent.location}</span>
          </p>
        </button>
        <div className="flex shrink-0 items-center gap-1">
          <Button
            variant="ghost"
            size="icon"
            aria-label="Edit settings"
            className="text-muted-foreground hover:text-foreground shrink-0"
            onClick={openSpeedDialog}
          >
            <SlidersHorizontal />
          </Button>
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
        </div>
      </CardHeader>

      {editDialog}
      {confirmDialog}

      <CardContent className="grid grid-cols-2 gap-x-4 gap-y-3 py-4 sm:grid-cols-6">
        <Stat icon={<StatusDot state={torrentState} />} label="Status">
          <Tip label={formatUptime(torrent.addedAt, now)}>
            <span className={`cursor-default ${stateConfig[torrentState].className}`}>
              {stateConfig[torrentState].label}
            </span>
          </Tip>
        </Stat>
        <Stat icon={<ArrowUp className="size-4" />} label="Uploaded">
          {formatBytes(torrent.uploadedBytes)}
        </Stat>
        <Stat icon={<Gauge className="size-4" />} label="Rate">
          {formatRate(smoothRate)}
        </Stat>
        <Stat icon={<Scale className="size-4" />} label="Ratio">
          {torrent.sizeBytes > 0
            ? (torrent.uploadedBytes / torrent.sizeBytes).toFixed(2)
            : '—'}
        </Stat>
        <Stat icon={<Users className="size-4" />} label="Swarm">
          <span className="text-emerald-500">{seeders}</span>
          {' / '}
          <span className="text-amber-500">{leechers}</span>
        </Stat>
        <Stat icon={<HardDrive className="size-4" />} label="Size">
          {formatBytes(torrent.sizeBytes)}
        </Stat>

        {ratioProgress >= 0 && (
          <div className="col-span-2 sm:col-span-6">
            <div className="bg-muted h-1 w-full overflow-hidden rounded-full">
              <div
                className={`h-full rounded-full transition-all duration-1000 ${torrent.capped ? 'bg-amber-500' : 'bg-primary'}`}
                style={{ width: `${ratioProgress * 100}%` }}
              />
            </div>
          </div>
        )}
      </CardContent>

      {detailsOpen && (
        <div className="border-t">
          <TorrentChart points={statsPoints ?? []} />

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
                  <PrivacyBadge isPrivate={torrent.private} />
                  <TrackerBadge status={t.status} />
                </div>
              </div>
            ))}
          </div>

          <div className="text-muted-foreground bg-muted/30 border-t px-6 py-2 text-xs">
            added {formatUptime(torrent.addedAt, now)} ago
          </div>
        </div>
      )}
    </Card>
  )
}

export function TorrentCardSkeleton() {
  return (
    <Card className="gap-0 overflow-hidden border-l-[3px] border-l-border py-0">
      <CardHeader className="flex items-start justify-between gap-3 border-b py-4">
        <div className="min-w-0 flex-1 space-y-2">
          <div className="bg-muted h-4 w-2/3 animate-pulse rounded" />
          <div className="bg-muted h-3 w-1/3 animate-pulse rounded" />
        </div>
        <div className="flex gap-1">
          <div className="bg-muted h-8 w-8 animate-pulse rounded" />
          <div className="bg-muted h-8 w-8 animate-pulse rounded" />
        </div>
      </CardHeader>
      <CardContent className="grid grid-cols-2 gap-x-4 gap-y-3 py-4 sm:grid-cols-6">
        {Array.from({ length: 6 }).map((_, i) => (
          <div key={i} className="space-y-1.5">
            <div className="bg-muted h-3 w-12 animate-pulse rounded" />
            <div className="bg-muted h-4 w-16 animate-pulse rounded" />
          </div>
        ))}
      </CardContent>
    </Card>
  )
}

// graphWindowMs caps the chart to the most recent day so long-lived torrents
// stay readable. The full history is still kept and streamed — only the drawing
// is windowed.
const graphWindowMs = 24 * 60 * 60 * 1000

// chartBudget is the most columns the chart draws. Raw points are bucketed to at
// most this many, so a dense series stays readable instead of a fuzzy full-
// density line. ~the chart's rendered width in px.
const chartBudget = 240

interface ChartPoint {
  t: number
  l: number // leechers (bucket's last value — a step series)
  r: number // mean rate over the bucket
}

// downsample buckets points into at most `budget` columns, averaging the rate in
// each so a dense series renders as a clean line instead of a fuzzy point-per-
// tick one. Leechers take the bucket's last value (a step series).
function downsample(points: StatsPoint[], budget: number): ChartPoint[] {
  const stride = Math.max(1, Math.ceil(points.length / budget))
  const out: ChartPoint[] = []
  for (let i = 0; i < points.length; i += stride) {
    let rSum = 0
    let j = i
    for (; j < points.length && j < i + stride; j++) rSum += points[j].r
    const n = j - i
    out.push({ t: points[i].t, l: points[j - 1].l, r: Math.round(rSum / n) })
  }
  return out
}

function TorrentChart({ points: allPoints }: { points: StatsPoint[] }) {
  const [hover, setHover] = useState<{ idx: number; svgX: number } | null>(null)
  const now = useNow()

  const windowed = allPoints.filter((p) => p.t >= now - graphWindowMs)
  const points = downsample(windowed, chartBudget)
  if (points.length < 2) {
    return (
      <div className="px-6 py-3 text-xs text-muted-foreground">
        Collecting stats…
      </div>
    )
  }

  // SVG dimensions (viewBox units)
  const W = 400, H = 90
  const PL = 34, PR = 46, PT = 6, PB = 16
  const iW = W - PL - PR
  const iH = H - PT - PB

  const tMin = points[0].t
  const tMax = points[points.length - 1].t
  const tRange = tMax - tMin || 1
  const lMax = Math.max(...points.map((p) => p.l), 1)
  const rTop = Math.max(...points.map((p) => p.r), 1)

  const xOf = (t: number) => PL + ((t - tMin) / tRange) * iW
  const lYOf = (l: number) => PT + (1 - l / lMax) * iH
  const rYOf = (r: number) => PT + (1 - r / rTop) * iH

  const lPath = points
    .map((p, i) => `${i === 0 ? 'M' : 'L'}${xOf(p.t).toFixed(1)},${lYOf(p.l).toFixed(1)}`)
    .join(' ')
  const rPath = points
    .map((p, i) => `${i === 0 ? 'M' : 'L'}${xOf(p.t).toFixed(1)},${rYOf(p.r).toFixed(1)}`)
    .join(' ')

  const handleMouseMove = (e: React.MouseEvent<SVGSVGElement>) => {
    const rect = e.currentTarget.getBoundingClientRect()
    const svgX = ((e.clientX - rect.left) / rect.width) * W
    const t = tMin + Math.max(0, Math.min((svgX - PL) / iW, 1)) * tRange
    // Binary search for nearest point by time.
    let lo = 0, hi = points.length - 1
    while (lo < hi) {
      const mid = (lo + hi) >> 1
      if (points[mid].t < t) lo = mid + 1
      else hi = mid
    }
    const idx =
      lo > 0 && Math.abs(points[lo - 1].t - t) < Math.abs(points[lo].t - t)
        ? lo - 1
        : lo
    setHover({ idx, svgX: xOf(points[idx].t) })
  }

  const hoveredPoint = hover !== null ? points[hover.idx] : null
  const tooltipPct = hover !== null ? (hover.svgX / W) * 100 : 0

  return (
    <div className="px-4 pt-3 pb-1">
      <div className="mb-1 flex items-center gap-3 px-2 text-xs text-muted-foreground">
        <span className="flex items-center gap-1">
          <span className="inline-block size-2 rounded-full bg-amber-500/70" />
          leechers
        </span>
        <span className="flex items-center gap-1">
          <span className="bg-primary/70 inline-block size-2 rounded-full" />
          rate
        </span>
      </div>
      <div className="relative" onMouseLeave={() => setHover(null)}>
        {hoveredPoint && (
          <div
            className="pointer-events-none absolute z-10 rounded-md border bg-popover px-2 py-1.5 text-xs shadow-md"
            style={
              tooltipPct > 60
                ? { right: `${100 - tooltipPct}%`, top: 0 }
                : { left: `${tooltipPct}%`, top: 0 }
            }
          >
            <p className="mb-1 text-muted-foreground">{formatDateTime(hoveredPoint.t)}</p>
            <p className="text-amber-500">{hoveredPoint.l} leecher{hoveredPoint.l !== 1 ? 's' : ''}</p>
            <p className="text-primary">{formatRate(hoveredPoint.r)}</p>
          </div>
        )}
        <svg
          viewBox={`0 0 ${W} ${H}`}
          className="w-full"
          aria-hidden="true"
          onMouseMove={handleMouseMove}
          style={{ cursor: 'crosshair' }}
        >
          {/* Axis lines */}
          <line x1={PL} y1={PT} x2={PL} y2={PT + iH} stroke="currentColor" strokeWidth="0.5" className="text-border" />
          <line x1={PL + iW} y1={PT} x2={PL + iW} y2={PT + iH} stroke="currentColor" strokeWidth="0.5" className="text-border" />
          <line x1={PL} y1={PT + iH} x2={PL + iW} y2={PT + iH} stroke="currentColor" strokeWidth="0.5" className="text-border" />
          <line x1={PL} y1={PT} x2={PL + iW} y2={PT} stroke="currentColor" strokeWidth="0.3" strokeDasharray="2,2" className="text-border" />

          {/* Leecher line (amber, left Y) */}
          <g className="text-amber-500">
            <path d={lPath} fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinejoin="round" opacity="0.75" />
            <text x={PL - 2} y={PT + 3.5} textAnchor="end" fontSize="5.5" fill="currentColor" opacity="0.8">{lMax}</text>
            <text x={PL - 2} y={PT + iH + 0.5} textAnchor="end" fontSize="5.5" fill="currentColor" opacity="0.8">0</text>
          </g>

          {/* Rate line (primary, right Y) */}
          <g className="text-primary">
            <path d={rPath} fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinejoin="round" opacity="0.75" />
            <text x={PL + iW + 2} y={PT + 3.5} textAnchor="start" fontSize="5.5" fill="currentColor" opacity="0.8">{formatRate(rTop)}</text>
            <text x={PL + iW + 2} y={PT + iH + 0.5} textAnchor="start" fontSize="5.5" fill="currentColor" opacity="0.8">0</text>
          </g>

          {/* X axis labels */}
          <text x={PL} y={H - 1} textAnchor="start" fontSize="5" className="fill-muted-foreground">{formatRelative(tMin)}</text>
          <text x={PL + iW} y={H - 1} textAnchor="end" fontSize="5" className="fill-muted-foreground">{formatRelative(tMax)}</text>

          {/* Hover crosshair + dots */}
          {hover !== null && hoveredPoint && (
            <g>
              <line
                x1={hover.svgX} y1={PT} x2={hover.svgX} y2={PT + iH}
                stroke="currentColor" strokeWidth="0.5" strokeDasharray="2,2"
                className="text-foreground/40"
              />
              <circle cx={hover.svgX} cy={lYOf(hoveredPoint.l)} r="2.5" fill="currentColor" className="text-amber-500" />
              <circle cx={hover.svgX} cy={rYOf(hoveredPoint.r)} r="2.5" fill="currentColor" className="text-primary" />
            </g>
          )}
        </svg>
      </div>
    </div>
  )
}

function Tip({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <Tooltip.Provider delayDuration={300}>
      <Tooltip.Root>
        <Tooltip.Trigger asChild>{children}</Tooltip.Trigger>
        <Tooltip.Portal>
          <Tooltip.Content
            sideOffset={6}
            className="bg-popover text-popover-foreground animate-in fade-in-0 zoom-in-95 z-50 rounded-md border px-2 py-1 text-xs shadow-md"
          >
            {label}
            <Tooltip.Arrow className="fill-popover" />
          </Tooltip.Content>
        </Tooltip.Portal>
      </Tooltip.Root>
    </Tooltip.Provider>
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
      <div className="mt-1 font-mono text-sm font-medium tabular-nums">{children}</div>
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

function PrivacyBadge({ isPrivate }: { isPrivate: boolean }) {
  if (isPrivate) {
    return (
      <Badge variant="outline" className="gap-1 text-amber-500">
        <Lock className="size-3" />
        private
      </Badge>
    )
  }
  return (
    <Badge variant="outline" className="text-muted-foreground gap-1">
      <Globe className="size-3" />
      public
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


const stateConfig = {
  seeding: { label: 'uploading', className: 'text-emerald-500', tip: 'Uploading data to peers' },
  waiting: { label: 'waiting',   className: 'text-sky-500',     tip: 'Waiting for peers to connect' },
  capped:  { label: 'capped',    className: 'text-amber-500',   tip: 'Max ratio reached — no longer uploading' },
} as const

type TorrentState = keyof typeof stateConfig

function StatusDot({ state }: { state: TorrentState }) {
  const color = { seeding: 'bg-emerald-500', waiting: 'bg-sky-500', capped: 'bg-amber-500' }[state]
  return (
    <span className="relative flex size-2.5 shrink-0">
      {state === 'seeding' && (
        <span className={`absolute inline-flex size-full animate-ping rounded-full opacity-60 ${color}`} />
      )}
      <span className={`relative inline-flex size-2.5 rounded-full ${color}`} />
    </span>
  )
}
