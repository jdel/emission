// Human-readable formatting helpers.

const UNITS = ['B', 'kB', 'MB', 'GB', 'TB', 'PB']

/** formatBytes renders a byte count with a binary-scaled unit, e.g. "1.38 GB". */
export function formatBytes(n: number): string {
  if (n < 1) return '0 B'
  const i = Math.min(Math.floor(Math.log(n) / Math.log(1024)), UNITS.length - 1)
  const v = n / 1024 ** i
  return `${v.toFixed(i === 0 ? 0 : v >= 100 ? 0 : 1)} ${UNITS[i]}`
}

/** formatRate renders a bytes-per-second value, e.g. "384 kB/s". */
export function formatRate(n: number): string {
  return `${formatBytes(n)}/s`
}

/**
 * formatRateInput renders a byte rate in a short, ParseRate-parseable form —
 * e.g. 51200 -> "50K", 1572864 -> "1.5M". Suitable as the default value for
 * the speed-edit input.
 */
export function formatRateInput(n: number): string {
  if (n <= 0) return '0'
  const units = ['', 'K', 'M', 'G']
  let v = n
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return (v % 1 === 0 ? v.toString() : v.toFixed(1)) + units[i]
}

/**
 * parseRateInput parses a short rate string (e.g. "2M", "500K", "1.5G") into
 * bytes/sec using 1024-scaled units. Returns NaN when unparseable.
 */
export function parseRateInput(s: string): number {
  const m = /^\s*([\d.]+)\s*([KMGT]?)\s*$/i.exec(s)
  if (!m) return NaN
  const n = Number(m[1])
  if (!Number.isFinite(n)) return NaN
  const exp = { '': 0, K: 1, M: 2, G: 3, T: 4 }[m[2].toUpperCase()] ?? 0
  return n * 1024 ** exp
}

/** formatETA renders the gap until a future unix-ms timestamp, e.g. "29m 4s". */
export function formatETA(atMs: number): string {
  const sec = Math.round((atMs - Date.now()) / 1000)
  if (sec <= 0) return 'now'
  const h = Math.floor(sec / 3600)
  const m = Math.floor((sec % 3600) / 60)
  const s = sec % 60
  if (h > 0) return `${h}h ${m}m`
  if (m > 0) return `${m}m ${s}s`
  return `${s}s`
}

/** formatDateTime renders a unix-ms timestamp as a locale date+time string. */
export function formatDateTime(ms: number): string {
  return new Date(ms).toLocaleString(undefined, {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  })
}

/**
 * formatUptime renders how long since a past unix-ms timestamp in a compact
 * form: the most-significant non-zero unit plus the next two down to seconds
 * (max 3 units), e.g. "2m30s", "2d3h52m", "1y3m5d". Year/month are approximate
 * (365d / 30d); trailing zero units are dropped, interior zeros kept. Units
 * never overlap in one output, so "m" is months only beside y/d and minutes
 * only beside h/s.
 */
export function formatUptime(atMs: number, nowMs = Date.now()): string {
  let sec = Math.floor((nowMs - atMs) / 1000)
  if (sec <= 0) return '0s'
  const units: [number, string][] = [
    [31536000, 'y'], // 365 days
    [2592000, 'm'], // 30 days (month)
    [86400, 'd'],
    [3600, 'h'],
    [60, 'm'], // minute
    [1, 's'],
  ]
  const vals = units.map(([size]) => {
    const v = Math.floor(sec / size)
    sec -= v * size
    return v
  })
  const lead = vals.findIndex((v) => v > 0)
  if (lead < 0) return '0s'
  const parts: string[] = []
  for (let i = lead; i < lead + 3 && i < units.length; i++) {
    parts.push(`${vals[i]}${units[i][1]}`)
  }
  // Drop trailing zero units (exactly 2 days → "2d", not "2d0h0m").
  while (parts.length > 1 && parts[parts.length - 1].startsWith('0')) parts.pop()
  return parts.join('')
}

/** formatRelative renders how long ago a past unix-ms timestamp was. */
export function formatRelative(atMs: number, nowMs = Date.now()): string {
  const sec = Math.round((nowMs - atMs) / 1000)
  if (sec < 60) return 'just now'
  const m = Math.floor(sec / 60)
  if (m < 60) return `${m}m ago`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h ago`
  return `${Math.floor(h / 24)}d ago`
}
