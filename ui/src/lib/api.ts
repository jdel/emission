// API client for the emission backend.
//
// REST under /api for the torrent list (paged, searchable) and mutations;
// a WebSocket at /api/ws for live stat updates; passkey auth under /api/auth.
// In dev, vite proxies everything to `emission seed --http.api`.

import { create, get } from '@github/webauthn-json'

export type TrackerStatus = 'ok' | 'failing' | 'pending'

export interface TrackerInfo {
  url: string
  seeders: number
  leechers: number
  intervalSec: number
  minIntervalSec: number
  nextAnnounceAt: number // unix ms, 0 if unknown
  status: TrackerStatus
}

export interface Torrent {
  id: string // info hash, hex
  name: string
  location: string // .torrent path relative to the watched directory
  sizeBytes: number
  uploadedBytes: number
  rateBytesPerSec: number
  minRateBytesPerSec: number // configured floor
  maxRateBytesPerSec: number // configured ceiling
  maxRatio: number // upload cap as N × torrent size; 0 = unlimited
  trackers: TrackerInfo[]
  addedAt: number // unix ms
}

export interface UploadResult {
  id: string
  name: string
}

export interface PagedTorrents {
  items: Torrent[]
  total: number
}

export interface ListParams {
  limit?: number // page size; 0 = all
  offset?: number
  q?: string // case-insensitive name filter
}

export interface UploadOptions {
  minSpeed?: string
  maxSpeed?: string
  maxRatio?: number
}

const BASE = '/api'

async function request(path: string, init?: RequestInit): Promise<Response> {
  const res = await fetch(BASE + path, init)
  if (!res.ok) {
    let message = `${res.status} ${res.statusText}`
    try {
      const body = (await res.json()) as { error?: string }
      if (body.error) message = body.error
    } catch {
      // non-JSON error body; keep the status line
    }
    throw new Error(message)
  }
  return res
}

/** listTorrents fetches one page of torrents plus the total count after filtering. */
export async function listTorrents(params: ListParams = {}): Promise<PagedTorrents> {
  const sp = new URLSearchParams()
  if (params.limit !== undefined) sp.set('limit', String(params.limit))
  if (params.offset !== undefined) sp.set('offset', String(params.offset))
  if (params.q) sp.set('q', params.q)
  const qs = sp.toString()
  const res = await request('/torrents' + (qs ? `?${qs}` : ''))
  return (await res.json()) as PagedTorrents
}

/**
 * uploadTorrent posts a .torrent file; optional speeds override server
 * defaults. The server validates and stores the file, then the directory
 * watcher seeds it — so the torrent appears in the list a moment later (via
 * the WebSocket "changed" event), not in this response.
 */
export async function uploadTorrent(
  file: File,
  opts: UploadOptions = {},
): Promise<UploadResult> {
  const form = new FormData()
  form.append('file', file)
  if (opts.minSpeed) form.append('min-speed', opts.minSpeed)
  if (opts.maxSpeed) form.append('max-speed', opts.maxSpeed)
  if (opts.maxRatio !== undefined) form.append('max-ratio', String(opts.maxRatio))
  const res = await request('/torrents', { method: 'POST', body: form })
  return (await res.json()) as UploadResult
}

/** removeTorrent stops seeding the torrent with the given id. */
export async function removeTorrent(id: string): Promise<void> {
  await request(`/torrents/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

/**
 * setClientOptions updates a torrent's per-client overrides: min/max upload
 * rate (human-readable strings, e.g. "200K", "1.5M") and ratio cap (multiple
 * of torrent size; 0 = unlimited / seed indefinitely). The change persists
 * via a sidecar JSON next to the .torrent.
 */
export async function setClientOptions(
  id: string,
  minSpeed: string,
  maxSpeed: string,
  maxRatio: number,
): Promise<void> {
  await request(`/torrents/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ minSpeed, maxSpeed, maxRatio }),
  })
}

// --- authentication (passkeys) ---------------------------------------------

export interface AuthStatus {
  authEnabled: boolean // false = no auth configured, everything is open
  authenticated: boolean
  username?: string // the logged-in user, when authenticated
  deviceCount?: number
  bootstrapAvailable?: boolean // admin-setup window is open
}

/** getAuthStatus reports whether auth is on and whether this client is in. */
export async function getAuthStatus(): Promise<AuthStatus> {
  const res = await request('/auth/status')
  return (await res.json()) as AuthStatus
}

async function postJSON(path: string, body?: unknown): Promise<Response> {
  return request(path, {
    method: 'POST',
    headers: body !== undefined ? { 'Content-Type': 'application/json' } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })
}

/** login runs a passkey login ceremony against a registered device. */
export async function login(): Promise<void> {
  const { ceremonyId, options } = (await (await postJSON('/auth/login/begin')).json()) as {
    ceremonyId: string
    options: Parameters<typeof get>[0]
  }
  const credential = await get(options)
  await postJSON(`/auth/login/finish?ceremony=${encodeURIComponent(ceremonyId)}`, credential)
}

export interface RegisterChallenge {
  ceremonyId: string
  options: Parameters<typeof create>[0]
  // username the invite enrols; "" means a bootstrap invite where the
  // registrant chooses their own name.
  username: string
}

/** beginRegister validates an invite and starts a passkey registration. */
export async function beginRegister(invite: string): Promise<RegisterChallenge> {
  const res = await postJSON('/auth/register/begin', { invite })
  return (await res.json()) as RegisterChallenge
}

/** finishRegister runs the WebAuthn ceremony and enrols the passkey. */
export async function finishRegister(challenge: RegisterChallenge): Promise<void> {
  const credential = await create(challenge.options)
  const qs = new URLSearchParams({ ceremony: challenge.ceremonyId })
  await postJSON(`/auth/register/finish?${qs}`, credential)
}

/** logout ends the current session. */
export async function logout(): Promise<void> {
  await postJSON('/auth/logout')
}

/**
 * createInvite mints a one-time device-registration link for a username.
 * Returns the full link and the 3-word code (the tail of the link), which is
 * easy to read aloud.
 */
export async function createInvite(
  username: string,
): Promise<{ url: string; code: string }> {
  const res = await postJSON('/auth/invite', { username })
  return (await res.json()) as { url: string; code: string }
}

// --- admin: device & user management ---------------------------------------

/** ADMIN_USERNAME is the fixed name of the privileged user. */
export const ADMIN_USERNAME = 'admin'

export interface Device {
  id: string // base64url credential id
  username: string
  addedAt: number // unix ms
}

/** listUsers returns every registered passkey (admin only). */
export async function listUsers(): Promise<Device[]> {
  const res = await request('/auth/users')
  return (await res.json()) as Device[]
}

/** removeCredential deletes one passkey by id (admin only). */
export async function removeCredential(id: string): Promise<void> {
  await request(`/auth/credentials/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

/** removeUser deletes a user, their passkeys, and their torrents (admin only). */
export async function removeUser(username: string): Promise<void> {
  await request(`/auth/users/${encodeURIComponent(username)}`, { method: 'DELETE' })
}

export interface StatsHandlers {
  /** onStats receives a full live snapshot of every torrent, ~once per second. */
  onStats: (torrents: Torrent[]) => void
  /** onChanged fires when a torrent is added or removed (by anyone). */
  onChanged: () => void
}

/**
 * connectStats opens the live-updates WebSocket and auto-reconnects if it
 * drops. Returns a function that closes the connection for good.
 */
export function connectStats(handlers: StatsHandlers): () => void {
  let closed = false
  let socket: WebSocket | null = null
  let retryTimer: number | undefined

  const open = () => {
    if (closed) return
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
    socket = new WebSocket(`${proto}//${location.host}${BASE}/ws`)
    socket.onmessage = (e) => {
      try {
        const msg = JSON.parse(e.data as string) as {
          type: string
          torrents?: Torrent[]
        }
        if (msg.type === 'stats') handlers.onStats(msg.torrents ?? [])
        else if (msg.type === 'changed') handlers.onChanged()
      } catch {
        // ignore malformed frames
      }
    }
    socket.onclose = () => {
      if (!closed) retryTimer = window.setTimeout(open, 2000)
    }
    socket.onerror = () => socket?.close()
  }

  open()
  return () => {
    closed = true
    window.clearTimeout(retryTimer)
    socket?.close()
  }
}
