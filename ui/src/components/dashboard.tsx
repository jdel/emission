import { useCallback, useEffect, useRef, useState } from 'react'
import { Activity, ArrowUp, Gauge, LayoutGrid, LayoutList, LogOut, Menu, Moon, Plus, Search, Smartphone, Sun, User, Users, UserPlus } from 'lucide-react'
import { useTheme } from 'next-themes'
import { DropdownMenu } from 'radix-ui'
import { toast } from 'sonner'

import {
  ADMIN_USERNAME,
  connectStats,
  listTorrents,
  logout,
  removeTorrent,
  uploadTorrent,
  type StatsPoint,
  type Torrent,
} from '@/lib/api'
import { formatBytes, formatRate } from '@/lib/format'
import { AuthControls, type AuthControlsHandle } from '@/components/auth-controls'
import { ThemeToggle } from '@/components/theme-toggle'
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
  const { resolvedTheme, setTheme } = useTheme()
  const isAdmin = username === ADMIN_USERNAME
  const [torrents, setTorrents] = useState<Torrent[]>([])
  const [total, setTotal] = useState(0)
  const [query, setQuery] = useState('')
  const [appliedQuery, setAppliedQuery] = useState('')
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

  const [isDragging, setIsDragging] = useState(false)

  const sentinelRef = useRef<HTMLDivElement>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const dragDepth = useRef(0)
  // Stable ref so drag handlers always call the latest handleUpload.
  const handleUploadRef = useRef<(files: File[]) => void>(() => {})

  // Debounce the search box; appliedQuery drives the actual fetch.
  useEffect(() => {
    const id = window.setTimeout(() => setAppliedQuery(query.trim()), 300)
    return () => window.clearTimeout(id)
  }, [query])

  // (Re)load the first page whenever the applied search term changes.
  useEffect(() => {
    let cancelled = false
    // eslint-disable-next-line react-hooks/set-state-in-effect
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
  // updated via an effect so the WebSocket handler always calls the latest.
  const reloadRef = useRef<() => void>(() => {})
  const reload = useCallback(() => {
    const count = Math.max(torrents.length, PAGE_SIZE)
    listTorrents({ limit: count, offset: 0, q: appliedQuery })
      .then((r) => {
        setTorrents(r.items)
        setTotal(r.total)
      })
      .catch(() => {})
  }, [torrents.length, appliedQuery])
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
  useEffect(() => { handleUploadRef.current = handleUpload }, [handleUpload])

  useEffect(() => {
    function onDragEnter(e: DragEvent) {
      if (!e.dataTransfer?.types.includes('Files')) return
      e.preventDefault()
      if (dragDepth.current++ === 0) setIsDragging(true)
    }
    function onDragLeave() {
      if (--dragDepth.current === 0) setIsDragging(false)
    }
    function onDragOver(e: DragEvent) {
      if (e.dataTransfer?.types.includes('Files')) e.preventDefault()
    }
    function onDrop(e: DragEvent) {
      e.preventDefault()
      dragDepth.current = 0
      setIsDragging(false)
      const files = Array.from(e.dataTransfer?.files ?? []).filter((f) =>
        f.name.toLowerCase().endsWith('.torrent'),
      )
      if (files.length) handleUploadRef.current(files)
    }
    document.addEventListener('dragenter', onDragEnter)
    document.addEventListener('dragleave', onDragLeave)
    document.addEventListener('dragover', onDragOver)
    document.addEventListener('drop', onDrop)
    return () => {
      document.removeEventListener('dragenter', onDragEnter)
      document.removeEventListener('dragleave', onDragLeave)
      document.removeEventListener('dragover', onDragOver)
      document.removeEventListener('drop', onDrop)
    }
  }, [])

  useEffect(() => {
    function onKeyDown(e: KeyboardEvent) {
      if (e.key !== 'u' || e.ctrlKey || e.metaKey || e.altKey) return
      const el = document.activeElement
      if (el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement) return
      fileInputRef.current?.click()
    }
    document.addEventListener('keydown', onKeyDown)
    return () => document.removeEventListener('keydown', onKeyDown)
  }, [])

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
      {isDragging && (
        <div className="border-primary/50 bg-background/80 pointer-events-none fixed inset-0 z-50 m-4 flex items-center justify-center rounded-xl border-2 border-dashed backdrop-blur-sm">
          <p className="text-primary text-lg font-medium">Drop .torrent files to add</p>
        </div>
      )}
      <div className="mx-auto max-w-3xl px-4 pt-4 pb-8 sm:pt-6 sm:pb-12">
        <header className="bg-background/75 border-border/60 sticky top-0 z-10 -mx-4 mb-8 flex items-center justify-between border-b px-4 py-3 backdrop-blur-md">
          <div className="flex items-center gap-3">
            <div className="bg-primary text-primary-foreground flex size-9 items-center justify-center rounded-lg">
              <Gauge className="size-5" />
            </div>
            <div>
              <h1 className="text-lg leading-tight font-semibold tracking-tight">emission</h1>
              <p className="text-muted-foreground text-xs">torrent ratio manager</p>
            </div>
            {authEnabled && username && (
              <span className="text-muted-foreground hidden items-center gap-1.5 text-sm sm:flex">
                <User className="size-4" />
                {username}
              </span>
            )}
          </div>
          <div className="flex items-center gap-2">
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

            {/* Desktop: add torrent, theme, auth */}
            <div className="hidden items-center gap-1 sm:flex">
              {!wsConnected && (
                <span className="flex items-center gap-1.5 px-1 text-xs text-amber-500">
                  <span className="inline-block size-1.5 animate-pulse rounded-full bg-amber-500" />
                  Reconnecting…
                </span>
              )}
              <Button
                size="sm"
                disabled={uploading}
                onClick={() => fileInputRef.current?.click()}
                aria-label="Add torrent"
              >
                <Plus className="size-4" />
                Add torrent
              </Button>
              <ThemeToggle />
              {authEnabled && (
                <AuthControls ref={authRef} username={username} onSignedOut={onSignedOut} />
              )}
            </div>

            {/* Mobile: add torrent icon + burger */}
            <Button
              size="icon"
              variant="ghost"
              disabled={uploading}
              onClick={() => fileInputRef.current?.click()}
              aria-label="Add torrent"
              className="sm:hidden"
            >
              <Plus className="size-4" />
            </Button>
            <div className="sm:hidden">
              {authEnabled && (
                <span className="hidden">
                  <AuthControls ref={authRef} username={username} onSignedOut={onSignedOut} />
                </span>
              )}
              <DropdownMenu.Root>
                <DropdownMenu.Trigger asChild>
                  <Button variant="ghost" size="icon" aria-label="Menu">
                    <Menu className="size-4" />
                  </Button>
                </DropdownMenu.Trigger>
                <DropdownMenu.Portal>
                  <DropdownMenu.Content
                    align="end"
                    sideOffset={8}
                    className="bg-popover text-popover-foreground z-50 min-w-48 overflow-hidden rounded-lg border p-1 shadow-lg data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 data-[state=closed]:zoom-out-95 data-[state=open]:zoom-in-95"
                  >
                    {/* Info labels */}
                    {(!wsConnected || (authEnabled && username)) && (
                      <>
                        {!wsConnected && (
                          <DropdownMenu.Item disabled className="flex items-center gap-2 px-2 py-1.5 text-xs text-amber-500 outline-none">
                            <span className="inline-block size-1.5 animate-pulse rounded-full bg-amber-500" /> Reconnecting…
                          </DropdownMenu.Item>
                        )}
                        {authEnabled && username && (
                          <DropdownMenu.Item disabled className="flex items-center gap-2 px-2 py-1.5 text-sm opacity-60 outline-none">
                            <User className="size-4" /> {username}
                          </DropdownMenu.Item>
                        )}
                        <DropdownMenu.Separator className="-mx-1 my-1 h-px bg-border" />
                      </>
                    )}

                    {/* Theme */}
                    <DropdownMenu.Item
                      className="flex cursor-default items-center gap-2 rounded-md px-2 py-1.5 text-sm outline-none hover:bg-accent"
                      onSelect={() => setTheme(resolvedTheme === 'dark' ? 'light' : 'dark')}
                    >
                      {resolvedTheme === 'dark' ? <Sun className="size-4" /> : <Moon className="size-4" />}
                      {resolvedTheme === 'dark' ? 'Light mode' : 'Dark mode'}
                    </DropdownMenu.Item>

                    {/* Auth actions */}
                    {authEnabled && (
                      <>
                        <DropdownMenu.Separator className="-mx-1 my-1 h-px bg-border" />
                        {!isAdmin && (
                          <DropdownMenu.Item
                            className="flex cursor-default items-center gap-2 rounded-md px-2 py-1.5 text-sm outline-none hover:bg-accent"
                            onSelect={() => authRef.current?.addDevice()}
                          >
                            <UserPlus className="size-4" /> Add device
                          </DropdownMenu.Item>
                        )}
                        <DropdownMenu.Item
                          className="flex cursor-default items-center gap-2 rounded-md px-2 py-1.5 text-sm outline-none hover:bg-accent"
                          onSelect={() => authRef.current?.openInvite()}
                        >
                          <Smartphone className="size-4" /> Invite user
                        </DropdownMenu.Item>
                        {isAdmin && (
                          <DropdownMenu.Item
                            className="flex cursor-default items-center gap-2 rounded-md px-2 py-1.5 text-sm outline-none hover:bg-accent"
                            onSelect={() => authRef.current?.openManage()}
                          >
                            <Users className="size-4" /> Manage users
                          </DropdownMenu.Item>
                        )}
                        <DropdownMenu.Separator className="-mx-1 my-1 h-px bg-border" />
                        <DropdownMenu.Item
                          className="text-destructive flex cursor-default items-center gap-2 rounded-md px-2 py-1.5 text-sm outline-none hover:bg-accent"
                          onSelect={async () => { try { await logout() } finally { onSignedOut() } }}
                        >
                          <LogOut className="size-4" /> Sign out
                        </DropdownMenu.Item>
                      </>
                    )}
                  </DropdownMenu.Content>
                </DropdownMenu.Portal>
              </DropdownMenu.Root>
            </div>
          </div>
        </header>

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
            <div className="flex overflow-hidden rounded-md border">
              <Button
                variant="ghost"
                size="icon"
                title="Card view"
                className={`h-9 w-9 rounded-none ${view === 'card' ? 'bg-muted' : ''}`}
                onClick={() => { setView('card'); try { localStorage.setItem('emission-view', 'card') } catch { /* ignore */ } }}
              >
                <LayoutGrid className="size-4" />
              </Button>
              <Button
                variant="ghost"
                size="icon"
                title="Compact view"
                className={`h-9 w-9 rounded-none border-l ${view === 'compact' ? 'bg-muted' : ''}`}
                onClick={() => { setView('compact'); try { localStorage.setItem('emission-view', 'compact') } catch { /* ignore */ } }}
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
            <div ref={sentinelRef} className="py-2 text-center">
              {loadingMore && <span className="text-muted-foreground text-sm">Loading more…</span>}
              {!hasMore && !loadingMore && (
                <span className="text-muted-foreground text-xs">Powered by <a href="https://github.com/jdel/emission" target="_blank" rel="noreferrer" className="underline-offset-2 hover:underline">emission</a></span>
              )}
            </div>
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
            {/* Infinite-scroll sentinel + status line. */}
            <div ref={sentinelRef} className="py-2 text-center">
              {loadingMore && (
                <span className="text-muted-foreground text-sm">Loading more…</span>
              )}
              {!hasMore && !loadingMore && (
                <span className="text-muted-foreground text-xs">Powered by <a href="https://github.com/jdel/emission" target="_blank" rel="noreferrer" className="underline-offset-2 hover:underline">emission</a></span>
              )}
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
