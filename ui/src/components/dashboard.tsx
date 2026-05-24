import { useCallback, useEffect, useRef, useState } from 'react'
import { Activity, Gauge, Plus, Search, User } from 'lucide-react'
import { toast } from 'sonner'

import {
  connectStats,
  listTorrents,
  removeTorrent,
  uploadTorrent,
  type Torrent,
} from '@/lib/api'
import { formatRate } from '@/lib/format'
import { AuthControls } from '@/components/auth-controls'
import { ThemeToggle } from '@/components/theme-toggle'
import { TorrentCard } from '@/components/torrent-card'
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
  const [torrents, setTorrents] = useState<Torrent[]>([])
  const [total, setTotal] = useState(0)
  const [query, setQuery] = useState('')
  const [appliedQuery, setAppliedQuery] = useState('')
  const [loading, setLoading] = useState(true)
  const [loadingMore, setLoadingMore] = useState(false)
  const [uploading, setUploading] = useState(false)
  const [removingId, setRemovingId] = useState<string | null>(null)
  const [totalRate, setTotalRate] = useState(0)

  const sentinelRef = useRef<HTMLDivElement>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)

  // Debounce the search box; appliedQuery drives the actual fetch.
  useEffect(() => {
    const id = window.setTimeout(() => setAppliedQuery(query.trim()), 300)
    return () => window.clearTimeout(id)
  }, [query])

  // (Re)load the first page whenever the applied search term changes.
  useEffect(() => {
    let cancelled = false
    setLoading(true)
    listTorrents({ limit: PAGE_SIZE, offset: 0, q: appliedQuery })
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
  }, [appliedQuery])

  // reloadRef holds a closure that refetches the currently-visible range. It is
  // refreshed every render so the WebSocket handler always calls the latest.
  const reloadRef = useRef<() => void>(() => {})
  reloadRef.current = () => {
    const count = Math.max(torrents.length, PAGE_SIZE)
    listTorrents({ limit: count, offset: 0, q: appliedQuery })
      .then((r) => {
        setTorrents(r.items)
        setTotal(r.total)
      })
      .catch(() => {})
  }

  // Live updates: merge per-second stats into loaded rows; refetch on add/remove.
  useEffect(() => {
    return connectStats({
      onStats: (all) => {
        setTotalRate(all.reduce((n, t) => n + t.rateBytesPerSec, 0))
        const byId = new Map(all.map((t) => [t.id, t]))
        setTorrents((prev) => prev.map((t) => byId.get(t.id) ?? t))
      },
      onChanged: () => reloadRef.current(),
    })
  }, [])

  const loadMore = useCallback(async () => {
    setLoadingMore(true)
    try {
      const r = await listTorrents({
        limit: PAGE_SIZE,
        offset: torrents.length,
        q: appliedQuery,
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
  }, [torrents.length, appliedQuery])

  // Infinite scroll: load the next page when the sentinel scrolls into view.
  useEffect(() => {
    const el = sentinelRef.current
    if (!el) return
    const obs = new IntersectionObserver((entries) => {
      if (
        entries[0].isIntersecting &&
        !loading &&
        !loadingMore &&
        torrents.length < total
      ) {
        loadMore()
      }
    })
    obs.observe(el)
    return () => obs.disconnect()
  }, [loadMore, loading, loadingMore, torrents.length, total])

  async function handleUpload(files: File[]) {
    setUploading(true)
    try {
      for (const file of files) {
        const t = await uploadTorrent(file)
        toast.success(`Added ${t.name}`)
      }
      reloadRef.current()
    } catch (e) {
      toast.error(`Upload failed: ${String(e)}`)
    } finally {
      setUploading(false)
    }
  }

  function onFilePicked(list: FileList | null) {
    if (!list) return
    const files = Array.from(list).filter((f) =>
      f.name.toLowerCase().endsWith('.torrent'),
    )
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

  const hasMore = torrents.length < total

  return (
    <div className="bg-background text-foreground min-h-svh">
      <div className="mx-auto max-w-3xl px-4 py-8 sm:py-12">
        <header className="mb-8 flex items-center justify-between">
          <div className="flex items-center gap-3">
            <div className="bg-primary text-primary-foreground flex size-9 items-center justify-center rounded-lg">
              <Gauge className="size-5" />
            </div>
            <div>
              <h1 className="text-lg leading-tight font-semibold tracking-tight">emission</h1>
              <p className="text-muted-foreground text-xs">torrent ratio manager</p>
            </div>
          </div>
          <div className="flex items-center gap-2">
            {totalRate > 0 && (
              <span className="text-muted-foreground hidden items-center gap-1.5 text-sm tabular-nums sm:flex">
                <Activity className="size-4" />
                {formatRate(totalRate)}
              </span>
            )}
            {authEnabled && username && (
              <span className="text-muted-foreground hidden items-center gap-1.5 text-sm sm:flex">
                <User className="size-4" />
                {username}
              </span>
            )}
            <Button
              size="sm"
              disabled={uploading}
              onClick={() => fileInputRef.current?.click()}
              aria-label="Add torrent"
            >
              <Plus className="size-4" />
              <span className="hidden sm:inline">Add torrent</span>
            </Button>
            <input
              ref={fileInputRef}
              type="file"
              accept=".torrent,application/x-bittorrent"
              multiple
              hidden
              onChange={(e) => {
                onFilePicked(e.target.files)
                e.target.value = ''
              }}
            />
            {authEnabled && <AuthControls username={username} onSignedOut={onSignedOut} />}
            <ThemeToggle />
          </div>
        </header>

        <div className="mb-3 flex items-center justify-between gap-3">
          <h2 className="shrink-0 font-medium">
            Torrents <span className="text-muted-foreground tabular-nums">{total}</span>
          </h2>
          <div className="relative w-full max-w-56">
            <Search className="text-muted-foreground absolute top-1/2 left-2.5 size-4 -translate-y-1/2" />
            <Input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Search torrents…"
              className="pl-8"
            />
          </div>
        </div>

        {loading ? (
          <p className="text-muted-foreground py-12 text-center text-sm">Loading…</p>
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
        ) : (
          <div className="flex flex-col gap-4">
            {torrents.map((t) => (
              <TorrentCard
                key={t.id}
                torrent={t}
                onRemove={handleRemove}
                removing={removingId === t.id}
              />
            ))}
            {/* Infinite-scroll sentinel + status line. */}
            <div ref={sentinelRef} className="py-2 text-center">
              {loadingMore && (
                <span className="text-muted-foreground text-sm">Loading more…</span>
              )}
              {!hasMore && !loadingMore && (
                <span className="text-muted-foreground text-xs">
                  {total} torrent{total === 1 ? '' : 's'}
                </span>
              )}
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
