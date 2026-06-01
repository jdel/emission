import { useCallback, useEffect, useRef, useState } from 'react'
import { Activity, ArrowUp, Gauge, LayoutGrid, LayoutList, Search, Users } from 'lucide-react'
import { toast } from 'sonner'

import {
  ADMIN_USERNAME,
  connectStats,
  listTorrents,
  listUsers,
  removeTorrent,
  uploadTorrent,
  type StatsPoint,
  type Torrent,
} from '@/lib/api'
import { formatBytes, formatRate } from '@/lib/format'
import { torrentFiles, useFileDrop, useInfiniteScroll, useUploadHotkey } from '@/lib/dashboard-effects'
import { type AuthControlsHandle } from '@/components/auth-controls'
import { DashboardHeader } from '@/components/dashboard-header'
import { TorrentCard, TorrentCardSkeleton } from '@/components/torrent-card'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from '@/components/ui/empty'

const PAGE_SIZE = 10

interface DashboardProps {
  // authEnabled controls whether the invite / sign-out controls are shown.
  authEnabled: boolean
  username?: string
  onSignedOut: () => void
}

export function Dashboard({ authEnabled, username, onSignedOut }: DashboardProps) {
  const authRef = useRef<AuthControlsHandle>(null)
  const isAdmin = username === ADMIN_USERNAME
  const [torrents, setTorrents] = useState<Torrent[]>([])
  const [total, setTotal] = useState(0)
  const [query, setQuery] = useState('')
  const [appliedQuery, setAppliedQuery] = useState('')
  // Admin-only filter to one owner's torrents.
  const [owner, setOwner] = useState('')
  const [appliedOwner, setAppliedOwner] = useState('')
  const [users, setUsers] = useState<string[]>([])
  const [loading, setLoading] = useState(true)
  const [loadingMore, setLoadingMore] = useState(false)
  const [uploading, setUploading] = useState(false)
  const [removingId, setRemovingId] = useState<string | null>(null)
  const [totalRate, setTotalRate] = useState(0)
  const [totalUploaded, setTotalUploaded] = useState(0)
  const [wsConnected, setWsConnected] = useState(true)
  const [statsMap, setStatsMap] = useState<Record<string, StatsPoint[]>>({})
  const [view, setView] = useState<'card' | 'compact'>(() => {
    try { return (localStorage.getItem('emission-view') as 'card' | 'compact') ?? 'card' }
    catch { return 'card' }
  })

  const sentinelRef = useRef<HTMLDivElement>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)

  // Debounce the search box; appliedQuery drives the actual fetch.
  useEffect(() => {
    const id = window.setTimeout(() => setAppliedQuery(query.trim()), 300)
    return () => window.clearTimeout(id)
  }, [query])

  // Same for the admin owner filter.
  useEffect(() => {
    const id = window.setTimeout(() => setAppliedOwner(owner.trim()), 300)
    return () => window.clearTimeout(id)
  }, [owner])

  // The owner dropdown lists every known username (admin only).
  useEffect(() => {
    if (!isAdmin) return
    listUsers()
      .then((d) => setUsers([...new Set(d.map((u) => u.username))].sort()))
      .catch(() => {})
  }, [isAdmin])

  // (Re)load the first page whenever the applied search term changes.
  useEffect(() => {
    let cancelled = false
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setLoading(true)
    listTorrents({ limit: PAGE_SIZE, offset: 0, q: appliedQuery, owner: appliedOwner })
      .then((r) => {
        if (cancelled) return
        setTorrents(r.items)
        setTotal(r.total)
      })
      .catch((e) => !cancelled && toast.error(`Load failed: ${String(e)}`))
      .finally(() => !cancelled && setLoading(false))
    return () => {
      cancelled = true
    }
  }, [appliedQuery, appliedOwner])

  // reloadRef holds a closure that refetches the currently-visible range. It is
  // updated via an effect so the WebSocket handler always calls the latest.
  const reloadRef = useRef<() => void>(() => {})
  const reload = useCallback(() => {
    const count = Math.max(torrents.length, PAGE_SIZE)
    listTorrents({ limit: count, offset: 0, q: appliedQuery, owner: appliedOwner })
      .then((r) => {
        setTorrents(r.items)
        setTotal(r.total)
      })
      .catch(() => {})
  }, [torrents.length, appliedQuery, appliedOwner])
  useEffect(() => { reloadRef.current = reload }, [reload])

  // Live updates: merge per-second stats into loaded rows; refetch on add/remove.
  useEffect(() => {
    return connectStats({
      onStats: (all) => {
        setTotalRate(all.reduce((n, t) => n + t.rateBytesPerSec, 0))
        setTotalUploaded(all.reduce((n, t) => n + t.uploadedBytes, 0))
        const byId = new Map(all.map((t) => [t.id, t]))
        setTorrents((prev) => prev.map((t) => byId.get(t.id) ?? t))
      },
      onChanged: () => reloadRef.current(),
      onConnected: () => setWsConnected(true),
      onDisconnected: () => setWsConnected(false),
      onStatsHistory: (history) => setStatsMap(history),
      onStatPoint: (id, point) =>
        setStatsMap((prev) => {
          const existing = prev[id] ?? []
          const updated = [...existing, point]
          return { ...prev, [id]: updated.length > 20_000 ? updated.slice(-20_000) : updated }
        }),
    })
  }, [])

  const loadMore = useCallback(async () => {
    setLoadingMore(true)
    try {
      const r = await listTorrents({
        limit: PAGE_SIZE,
        offset: torrents.length,
        q: appliedQuery,
        owner: appliedOwner,
      })
      setTorrents((prev) => {
        const have = new Set(prev.map((t) => t.id))
        return [...prev, ...r.items.filter((t) => !have.has(t.id))]
      })
      setTotal(r.total)
    } catch (e) {
      toast.error(`Load failed: ${String(e)}`)
    } finally {
      setLoadingMore(false)
    }
  }, [torrents.length, appliedQuery, appliedOwner])

  const handleUpload = useCallback(async (files: File[]) => {
    setUploading(true)
    try {
      for (const file of files) {
        const t = await uploadTorrent(file)
        toast.success(`Added ${t.name}`)
        if (t.notice) toast.warning(t.notice)
      }
      reloadRef.current()
    } catch (e) {
      toast.error(`Upload failed: ${String(e)}`)
    } finally {
      setUploading(false)
    }
  }, [])

  // Infinite scroll, global file drop, and the "u" upload hotkey.
  useInfiniteScroll(sentinelRef, !loading && !loadingMore && torrents.length < total, loadMore)
  const isDragging = useFileDrop(handleUpload)
  useUploadHotkey(() => fileInputRef.current?.click())

  function onFilePicked(list: FileList | null) {
    const files = torrentFiles(list)
    if (files.length) handleUpload(files)
  }

  async function handleRemove(id: string) {
    const name = torrents.find((t) => t.id === id)?.name ?? 'torrent'
    setRemovingId(id)
    try {
      await removeTorrent(id)
      toast.success(`Removed ${name}`)
      reloadRef.current()
    } catch (e) {
      toast.error(`Remove failed: ${String(e)}`)
    } finally {
      setRemovingId(null)
    }
  }

  function selectView(v: 'card' | 'compact') {
    setView(v)
    try { localStorage.setItem('emission-view', v) } catch { /* ignore */ }
  }

  const hasMore = torrents.length < total

  // Infinite-scroll sentinel + status line, shared by both list layouts.
  const sentinel = (
    <div ref={sentinelRef} className="py-2 text-center">
      {loadingMore && <span className="text-muted-foreground text-sm">Loading more…</span>}
      {!hasMore && !loadingMore && (
        <span className="text-muted-foreground text-xs">Powered by <a href="https://github.com/jdel/emission" target="_blank" rel="noreferrer" className="underline-offset-2 hover:underline">emission</a></span>
      )}
    </div>
  )

  return (
    <div className="bg-background text-foreground min-h-svh">
      {isDragging && (
        <div className="border-primary/50 bg-background/80 pointer-events-none fixed inset-0 z-50 m-4 flex items-center justify-center rounded-xl border-2 border-dashed backdrop-blur-sm">
          <p className="text-primary text-lg font-medium">Drop .torrent files to add</p>
        </div>
      )}
      <div className="mx-auto max-w-3xl px-4 pb-8 sm:pb-12">
        <DashboardHeader
          authEnabled={authEnabled}
          username={username}
          isAdmin={isAdmin}
          uploading={uploading}
          wsConnected={wsConnected}
          fileInputRef={fileInputRef}
          authRef={authRef}
          onAddClick={() => fileInputRef.current?.click()}
          onFilePicked={onFilePicked}
          onSignedOut={onSignedOut}
        />

        <div className="mb-3 flex items-center justify-between gap-3">
          <h2 className="shrink-0 font-medium">
            Torrents <span className="text-muted-foreground tabular-nums">{total}</span>
          </h2>
          <div className="flex items-center gap-2">
            <div className="relative w-full max-w-48">
              <Search className="text-muted-foreground absolute top-1/2 left-2.5 size-4 -translate-y-1/2" />
              <Input
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder="Search torrents…"
                className="pl-8"
              />
            </div>
            {isAdmin && (
              <div className="relative w-full max-w-40">
                <Users className="text-muted-foreground absolute top-1/2 left-2.5 size-4 -translate-y-1/2" />
                <Input
                  value={owner}
                  onChange={(e) => setOwner(e.target.value)}
                  placeholder="All users"
                  list="dashboard-owner-filter"
                  className="pl-8"
                />
                <datalist id="dashboard-owner-filter">
                  {users.map((u) => (
                    <option key={u} value={u} />
                  ))}
                </datalist>
              </div>
            )}
            <div className="flex overflow-hidden rounded-md border">
              <Button
                variant="ghost"
                size="icon"
                title="Card view"
                className={`h-9 w-9 rounded-none ${view === 'card' ? 'bg-muted' : ''}`}
                onClick={() => selectView('card')}
              >
                <LayoutGrid className="size-4" />
              </Button>
              <Button
                variant="ghost"
                size="icon"
                title="Compact view"
                className={`h-9 w-9 rounded-none border-l ${view === 'compact' ? 'bg-muted' : ''}`}
                onClick={() => selectView('compact')}
              >
                <LayoutList className="size-4" />
              </Button>
            </div>
          </div>
        </div>

        {!loading && (totalUploaded > 0 || totalRate > 0) && (
          <div className="text-muted-foreground mb-3 flex items-center gap-4 text-xs">
            {totalUploaded > 0 && (
              <span className="flex items-center gap-1">
                <ArrowUp className="size-3" />
                {formatBytes(totalUploaded)} total
              </span>
            )}
            {totalRate > 0 && (
              <span className="flex items-center gap-1">
                <Activity className="size-3" />
                {formatRate(totalRate)} combined
              </span>
            )}
          </div>
        )}

        {loading ? (
          <div className="flex flex-col gap-4">
            {Array.from({ length: 3 }).map((_, i) => (
              <TorrentCardSkeleton key={i} />
            ))}
          </div>
        ) : torrents.length === 0 ? (
          <Empty className="border border-dashed">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                {appliedQuery ? <Search /> : <Gauge />}
              </EmptyMedia>
              <EmptyTitle>{appliedQuery ? 'No matches' : 'No torrents yet'}</EmptyTitle>
              <EmptyDescription>
                {appliedQuery
                  ? `Nothing matches “${appliedQuery}”.`
                  : 'Click "Add torrent" above to start seeding one.'}
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : view === 'compact' ? (
          <>
            <div className="overflow-hidden rounded-xl border">
              <div className="divide-y">
                {torrents.map((t) => (
                  <TorrentCard
                    key={t.id}
                    torrent={t}
                    onRemove={handleRemove}
                    removing={removingId === t.id}
                    compact
                  />
                ))}
              </div>
            </div>
            {sentinel}
          </>
        ) : (
          <div className="flex flex-col gap-4">
            {torrents.map((t) => (
              <TorrentCard
                key={t.id}
                torrent={t}
                onRemove={handleRemove}
                removing={removingId === t.id}
                statsPoints={statsMap[t.id]}
              />
            ))}
            {sentinel}
          </div>
        )}
      </div>
    </div>
  )
}
