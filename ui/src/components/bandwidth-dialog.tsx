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
import { formatRate, formatRateInput, parseRateInput } from '@/lib/format'

interface BandwidthDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** Target user; omit to edit the current user's own bandwidth. */
  username?: string
  /** Prefill in bytes/sec (admin path); the own path fetches it instead. */
  initialBytes?: number
  /** Prefill half-saturation (admin path); the own path fetches it instead. */
  initialHalfSat?: number
  onSaved?: () => void
}

// halfSat values mirror the presets in internal/seeder/bandwidth.go: the leecher
// count at which the reported rate reaches half of the ceiling.
const PROFILES: { value: SeedingProfile; label: string; hint: string; halfSat: number }[] = [
  { value: 'stealth', label: 'Stealth', hint: 'Trickles; only ramps in big swarms', halfSat: 10 },
  { value: 'normal', label: 'Normal', hint: 'Balanced (default)', halfSat: 4 },
  { value: 'aggressive', label: 'Aggressive', hint: 'Near-max on almost any demand', halfSat: 1 },
]

// Custom-drag bounds (leechers), mirroring the server's min/maxHalfSaturation.
const HS_MIN = 1
const HS_MAX = 10

// SeedingCurve plots leecher count (Y) against the resulting upload speed
// (X, 0 → the user's bandwidth ceiling), where speed = bandwidth · L/(L+halfSat).
// It marks the selected curve's half-speed and ~full-speed (90% of ceiling)
// points. Display only — the value is set via the slider.
function SeedingCurve({ halfSat, bandwidth }: { halfSat: number; bandwidth: number }) {
  const W = 320, H = 231
  const PL = 22, PR = 12, PT = 12, PB = 30
  const iW = W - PL - PR
  const iH = H - PT - PB
  // Tall enough to show stealth's full-speed point (9 · maxHalfSaturation).
  const Lmax = 9 * HS_MAX

  const xOf = (frac: number) => PL + frac * iW
  const yOf = (l: number) => PT + (1 - l / Lmax) * iH
  const pathFor = (k: number) =>
    Array.from({ length: 61 }, (_, i) => {
      const l = (i / 60) * Lmax
      return `${i === 0 ? 'M' : 'L'}${xOf(l / (l + k)).toFixed(1)},${yOf(l).toFixed(1)}`
    }).join(' ')

  const hx = xOf(0.5)
  const hy = yOf(halfSat)
  const ceiling = bandwidth > 0 ? formatRate(bandwidth) : 'max'
  const halfLabel = bandwidth > 0 ? formatRate(bandwidth / 2) : '½ max'

  // "Full speed" is asymptotic, so mark a practical 90%-of-ceiling point at
  // L = 9·halfSat. Only shown when it falls within the visible leecher range.
  const fullK = 9 * halfSat
  const showFull = fullK <= Lmax
  const fx = xOf(0.9)
  const fy = yOf(fullK)
  const fullLabelY = fy < PT + 16 ? fy + 12 : fy - 6

  return (
    <svg viewBox={`0 0 ${W} ${H}`} className="w-full" aria-hidden="true">
      {/* Axes */}
      <line x1={PL} y1={PT} x2={PL} y2={PT + iH} stroke="currentColor" strokeWidth="0.5" className="text-border" />
      <line x1={PL} y1={PT + iH} x2={PL + iW} y2={PT + iH} stroke="currentColor" strokeWidth="0.5" className="text-border" />
      {/* Half-ceiling gridline */}
      <line x1={hx} y1={PT} x2={hx} y2={PT + iH} stroke="currentColor" strokeWidth="0.3" strokeDasharray="2,2" className="text-border" />

      {/* Y labels (leechers) */}
      <text x={PL - 4} y={PT + 4} textAnchor="end" fontSize="9" className="fill-muted-foreground">{Lmax}</text>
      <text x={PL - 4} y={PT + iH} textAnchor="end" fontSize="9" className="fill-muted-foreground">0</text>
      {/* X labels (speed) */}
      <text x={PL} y={H - 6} textAnchor="start" fontSize="9" className="fill-muted-foreground">0</text>
      <text x={hx} y={H - 6} textAnchor="middle" fontSize="9" className="fill-muted-foreground">{halfLabel}</text>
      <text x={PL + iW} y={H - 6} textAnchor="end" fontSize="9" className="fill-muted-foreground">{ceiling}</text>

      {/* Unselected preset curves, faint */}
      {PROFILES.filter((p) => p.halfSat !== halfSat).map((p) => (
        <path key={p.value} d={pathFor(p.halfSat)} fill="none" stroke="currentColor" strokeWidth="1" className="text-muted-foreground" opacity="0.3" />
      ))}

      {/* Selected curve + draggable half-saturation marker */}
      <path d={pathFor(halfSat)} fill="none" stroke="currentColor" strokeWidth="2" strokeLinejoin="round" className="text-primary" />
      <line x1={PL} y1={hy} x2={hx} y2={hy} stroke="currentColor" strokeWidth="0.5" strokeDasharray="2,2" className="text-primary" opacity="0.5" />
      <text x={hx - 7} y={hy - 6} textAnchor="end" fontSize="9" className="fill-primary">
        half speed at {halfSat % 1 === 0 ? halfSat : halfSat.toFixed(1)} leechers
      </text>
      {/* Full-speed marker (~90% of ceiling), when within range */}
      {showFull && (
        <>
          <line x1={PL} y1={fy} x2={fx} y2={fy} stroke="currentColor" strokeWidth="0.5" strokeDasharray="2,2" className="text-emerald-500" opacity="0.5" />
          <text x={fx - 7} y={fullLabelY} textAnchor="end" fontSize="9" className="fill-emerald-500">
            full speed at {Math.round(fullK)} leechers
          </text>
          <circle cx={fx} cy={fy} r="3.5" className="fill-emerald-500 stroke-background" strokeWidth="1.5" />
        </>
      )}

      <circle cx={hx} cy={hy} r="3.5" className="fill-primary stroke-background" strokeWidth="1.5" />
    </svg>
  )
}

/**
 * BandwidthDialog edits an upload-bandwidth ceiling and seeding curve — either
 * the current user's own (username omitted, fetched from the server) or a
 * specific user's (admin).
 */
export function BandwidthDialog({
  open,
  onOpenChange,
  username,
  initialBytes,
  initialHalfSat,
  onSaved,
}: BandwidthDialogProps) {
  const [value, setValue] = useState('')
  const [halfSat, setHalfSat] = useState(4)
  const [serverDefault, setServerDefault] = useState<number | null>(null)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    if (!open) return
    if (username === undefined) {
      getBandwidth()
        .then((info) => {
          setValue(formatRateInput(info.bandwidth))
          setHalfSat(info.halfSaturation)
          setServerDefault(info.default)
        })
        .catch((e) => toast.error(e instanceof Error ? e.message : 'Load failed'))
      return
    }
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setValue(formatRateInput(initialBytes ?? 0))
    setHalfSat(initialHalfSat || 4)
    setServerDefault(null)
  }, [open, username, initialBytes, initialHalfSat])

  const onSave = async () => {
    setSaving(true)
    try {
      if (username === undefined) await setBandwidth(value.trim(), halfSat)
      else await setUserBandwidth(username, value.trim(), halfSat)
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
  const activePreset = PROFILES.find((p) => p.halfSat === halfSat)

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
                  onClick={() => setHalfSat(p.halfSat)}
                  className={
                    'rounded-md border px-2 py-1.5 text-sm transition-colors ' +
                    (activePreset?.value === p.value
                      ? 'border-primary bg-primary text-primary-foreground'
                      : 'border-input hover:bg-accent')
                  }
                >
                  {p.label}
                </button>
              ))}
            </div>
            <p className="text-muted-foreground text-xs">
              {activePreset
                ? activePreset.hint
                : `Custom — half speed at ${halfSat} leechers`}
            </p>
            {/* Inverted so the slider matches the preset button order:
                left = stealth (high k), right = aggressive (low k). */}
            <input
              type="range"
              min={HS_MIN}
              max={HS_MAX}
              step={1}
              value={HS_MIN + HS_MAX - halfSat}
              onChange={(e) => setHalfSat(HS_MIN + HS_MAX - Number(e.target.value))}
              aria-label="Seeding aggressiveness"
              className="accent-primary w-full"
            />
            <SeedingCurve
              halfSat={halfSat}
              bandwidth={parseRateInput(value) || serverDefault || 0}
            />
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
