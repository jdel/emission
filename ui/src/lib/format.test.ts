import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import {
  formatBytes,
  formatETA,
  formatRate,
  formatRateInput,
  formatRelative,
  formatUptime,
  parseRateInput,
} from '@/lib/format'

describe('formatBytes', () => {
  it.each([
    [0, '0 B'],
    [500, '500 B'],
    [1024, '1.0 kB'],
    [1536, '1.5 kB'],
    [1.5 * 1024 ** 3, '1.5 GB'],
    [Number.MAX_SAFE_INTEGER, expect.stringMatching(/ PB$/) as unknown as string], // clamps to largest unit
  ])('formatBytes(%p) -> %p', (n, want) => {
    expect(formatBytes(n)).toEqual(want)
  })
})

describe('formatRate', () => {
  it('appends /s to formatBytes', () => {
    expect(formatRate(1024)).toBe('1.0 kB/s')
  })
})

describe('formatRateInput / parseRateInput round-trip', () => {
  it.each([
    [0, '0'],
    [51200, '50K'],
    [1572864, '1.5M'],
  ])('formatRateInput(%p) -> %p', (n, want) => {
    expect(formatRateInput(n)).toBe(want)
  })

  it.each([
    ['2M', 2 * 1024 ** 2],
    ['500K', 500 * 1024],
    ['1.5G', 1.5 * 1024 ** 3],
    ['abc', NaN],
  ])('parseRateInput(%p) -> %p', (s, want) => {
    if (Number.isNaN(want)) expect(parseRateInput(s)).toBeNaN()
    else expect(parseRateInput(s)).toBe(want)
  })

  it('round-trips through both directions', () => {
    expect(parseRateInput(formatRateInput(2 * 1024 ** 2))).toBe(2 * 1024 ** 2)
  })
})

describe('formatUptime', () => {
  const base = Date.UTC(2026, 0, 1)

  it.each([
    [0, '0s'],
    [45_000, '45s'],
    [150_000, '2m30s'],
    [2 * 86400_000, '2d'], // exact days: trailing zero units dropped
    [(2 * 86400 + 3 * 3600 + 52 * 60 + 10) * 1000, '2d3h52m'], // capped at 3 units
    [(365 + 90 + 5) * 86400_000, '1Y3M5d'], // year/month use capital letters
    [2592000_000, '1M'], // exactly 1 month, not confusable with 1 minute
  ])('formatUptime(%p ms ago) -> %p', (deltaMs, want) => {
    expect(formatUptime(base - deltaMs, base)).toBe(want)
  })
})

describe('formatRelative', () => {
  const base = Date.UTC(2026, 0, 1)

  it.each([
    [30_000, 'just now'],
    [90_000, '1m ago'],
    [3_661_000, '1h ago'],
    [90_000_000, '1d ago'],
  ])('formatRelative(%p ms ago) -> %p', (deltaMs, want) => {
    expect(formatRelative(base - deltaMs, base)).toBe(want)
  })
})

describe('formatETA', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(Date.UTC(2026, 0, 1))
  })
  afterEach(() => vi.useRealTimers())

  it.each([
    [-1000, 'now'],
    [5_000, '5s'],
    [65_000, '1m 5s'],
    [3_665_000, '1h 1m'],
  ])('formatETA(now + %p ms) -> %p', (deltaMs, want) => {
    expect(formatETA(Date.now() + deltaMs)).toBe(want)
  })
})
