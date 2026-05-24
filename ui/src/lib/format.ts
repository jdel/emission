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

/** formatRelative renders how long ago a past unix-ms timestamp was. */
export function formatRelative(atMs: number): string {
  const sec = Math.round((Date.now() - atMs) / 1000)
  if (sec < 60) return 'just now'
  const m = Math.floor(sec / 60)
  if (m < 60) return `${m}m ago`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h ago`
  return `${Math.floor(h / 24)}d ago`
}
