import { useCallback, useEffect, useRef, useState } from 'react'
import { hierarchy, tree } from 'd3-hierarchy'
import { Gauge, List, Network, Ticket, Trash2, Users } from 'lucide-react'
import { toast } from 'sonner'

import {
  ADMIN_USERNAME,
  getUserProxy,
  listUsers,
  listInvites,
  listTorrents,
  removeCredential,
  removeUser,
  revokeInvite,
  type Device,
  type PendingInvite,
} from '@/lib/api'
import { formatETA, formatRelative } from '@/lib/format'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { ConfirmDialog, type ConfirmDialogProps } from '@/components/confirm-dialog'
import { InvitePanel } from '@/components/invite-panel'
import { BandwidthDialog } from '@/components/bandwidth-dialog'

// ownerOf returns the username that owns a torrent, from the first path segment
// of its location ("" = root / shared, not attributed to a named user).
function ownerOf(location: string): string {
  const i = location.indexOf('/')
  return i >= 0 ? location.slice(0, i) : ''
}

interface ManageUsersProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

// ---------------------------------------------------------------------------
// Graph view
// ---------------------------------------------------------------------------

interface GraphNode {
  username: string // unique identity; for pending invites this is "invite:<token>"
  label?: string // display override (pending invites show their target/"pending")
  deviceCount: number
  pending?: boolean // an outstanding invite, not a registered user
  children: GraphNode[]
}

function buildGraph(devices: Device[], invites: PendingInvite[]): GraphNode[] {
  const byUser = new Map<string, string[]>()
  const allUsers = new Set<string>()
  const deviceCounts = new Map<string, number>()

  for (const d of devices) {
    allUsers.add(d.username)
    deviceCounts.set(d.username, (deviceCounts.get(d.username) ?? 0) + 1)
    // skip self-invites (user has multiple devices; second invite created by themselves)
    if (d.invitedBy && d.invitedBy !== d.username) {
      const list = byUser.get(d.invitedBy) ?? []
      if (!list.includes(d.username)) list.push(d.username)
      byUser.set(d.invitedBy, list)
    }
  }

  // Outstanding invites hang off their creator as leaf nodes.
  const pendingByUser = new Map<string, PendingInvite[]>()
  for (const inv of invites) {
    if (!inv.createdBy) continue
    const list = pendingByUser.get(inv.createdBy) ?? []
    list.push(inv)
    pendingByUser.set(inv.createdBy, list)
  }

  function makeNode(username: string, visited: Set<string>): GraphNode {
    const next = new Set([...visited, username])
    const userChildren = (byUser.get(username) ?? [])
      .filter((u) => allUsers.has(u) && !visited.has(u))
      .sort((a, b) => a.localeCompare(b))
      .map((u) => makeNode(u, next))
    const pendingChildren: GraphNode[] = (pendingByUser.get(username) ?? []).map((inv) => ({
      username: `invite:${inv.token}`,
      label: inv.username || 'pending',
      deviceCount: 0,
      pending: true,
      children: [],
    }))
    return {
      username,
      deviceCount: deviceCounts.get(username) ?? 1,
      children: [...userChildren, ...pendingChildren],
    }
  }

  const roots: GraphNode[] = []
  if (allUsers.has(ADMIN_USERNAME)) roots.push(makeNode(ADMIN_USERNAME, new Set()))
  for (const u of allUsers) {
    if (u === ADMIN_USERNAME) continue
    const parent = devices.find(
      (dev) => dev.username === u && dev.invitedBy && dev.invitedBy !== u && allUsers.has(dev.invitedBy),
    )?.invitedBy
    if (!parent) roots.push(makeNode(u, new Set()))
  }
  return roots
}

interface PlacedNode {
  username: string
  node: GraphNode
  x: number
  y: number
  parent?: string
}

const NODE_R = 42
const SIBLING_GAP = 110 // horizontal spacing between siblings
const DEPTH_GAP = 140 // vertical spacing between invite depths
const SVG_W = 960
const SVG_H = 560

// layoutTree places the invite hierarchy as a tidy top-down tree
// (Reingold–Tilford, via d3-hierarchy) — the root sits at the top and depth
// grows downward, siblings spread horizontally, and no branches overlap. Only
// expanded nodes contribute children, so collapsing a node hides its subtree.
function layoutTree(root: GraphNode, expanded: Set<string>): PlacedNode[] {
  const h = hierarchy(root, (n) => (expanded.has(n.username) ? n.children : []))
  tree<GraphNode>().nodeSize([SIBLING_GAP, DEPTH_GAP])(h)
  const placed: PlacedNode[] = []
  h.each((d) => {
    placed.push({
      username: d.data.username,
      node: d.data,
      x: d.x ?? 0, // breadth → horizontal
      y: d.y ?? 0, // depth → vertical
      parent: d.parent?.data.username,
    })
  })
  return placed
}

function UserGraph({ roots }: { roots: GraphNode[] }) {
  const [expanded, setExpanded] = useState<Set<string>>(new Set())
  const [view, setView] = useState({ zoom: 1, panX: SVG_W / 2, panY: NODE_R + 24 })
  const [dragging, setDragging] = useState(false)
  const svgRef = useRef<SVGSVGElement>(null)
  const dragRef = useRef<{ x: number; y: number } | null>(null)

  useEffect(() => {
    const el = svgRef.current
    if (!el) return
    const onWheel = (e: WheelEvent) => {
      e.preventDefault()
      const rect = el.getBoundingClientRect()
      const curX = (e.clientX - rect.left) * (SVG_W / rect.width)
      const curY = (e.clientY - rect.top) * (SVG_H / rect.height)
      const factor = e.deltaY < 0 ? 1.025 : 1 / 1.025
      setView((v) => {
        const newZoom = Math.max(0.2, Math.min(5, v.zoom * factor))
        return {
          zoom: newZoom,
          panX: curX - ((curX - v.panX) / v.zoom) * newZoom,
          panY: curY - ((curY - v.panY) / v.zoom) * newZoom,
        }
      })
    }
    el.addEventListener('wheel', onWheel, { passive: false })
    return () => el.removeEventListener('wheel', onWheel)
  }, [])

  const adminRoot = roots.find((r) => r.username === ADMIN_USERNAME) ?? roots[0]
  const placed: PlacedNode[] = adminRoot ? layoutTree(adminRoot, expanded) : []

  function toggle(username: string) {
    setExpanded((prev) => {
      const next = new Set(prev)
      if (next.has(username)) next.delete(username)
      else next.add(username)
      return next
    })
  }

  function collectExpandable(n: GraphNode, acc: Set<string>) {
    if (n.children.length > 0) acc.add(n.username)
    n.children.forEach((c) => collectExpandable(c, acc))
  }

  function expandAll() {
    const all = new Set<string>()
    roots.forEach((r) => collectExpandable(r, all))
    setExpanded(all)
  }

  function onSvgMouseDown(e: React.MouseEvent<SVGSVGElement>) {
    dragRef.current = { x: e.clientX, y: e.clientY }
    setDragging(true)
  }

  function onSvgMouseMove(e: React.MouseEvent<SVGSVGElement>) {
    if (!dragRef.current || !svgRef.current) return
    const rect = svgRef.current.getBoundingClientRect()
    const dx = (e.clientX - dragRef.current.x) * (SVG_W / rect.width)
    const dy = (e.clientY - dragRef.current.y) * (SVG_H / rect.height)
    dragRef.current = { x: e.clientX, y: e.clientY }
    setView((v) => ({ ...v, panX: v.panX + dx, panY: v.panY + dy }))
  }

  function onSvgMouseUp() {
    dragRef.current = null
    setDragging(false)
  }

  return (
    <div className="flex flex-col gap-2">
      <div className="flex justify-end gap-2">
        <Button variant="outline" size="sm" onClick={expandAll}>Expand all</Button>
        <Button variant="outline" size="sm" onClick={() => setExpanded(new Set())}>Collapse all</Button>
      </div>

      <svg
        ref={svgRef}
        viewBox={`0 0 ${SVG_W} ${SVG_H}`}
        className="w-full rounded-lg border"
        style={{ height: SVG_H * 0.7, cursor: dragging ? 'grabbing' : 'grab' }}
        onMouseDown={onSvgMouseDown}
        onMouseMove={onSvgMouseMove}
        onMouseUp={onSvgMouseUp}
        onMouseLeave={onSvgMouseUp}
      >
        <g transform={`translate(${view.panX}, ${view.panY}) scale(${view.zoom})`}>
          {placed.map(({ username, parent, x, y }) => {
            if (!parent) return null
            const p = placed.find((n) => n.username === parent)
            if (!p) return null
            const midY = (p.y + y) / 2
            return (
              <path key={`${parent}-${username}`}
                d={`M ${p.x} ${p.y} C ${p.x} ${midY}, ${x} ${midY}, ${x} ${y}`}
                fill="none"
                style={{ stroke: 'var(--border)', strokeWidth: 1.5 }}
              />
            )
          })}

          {placed.map(({ username, node, x, y }) => {
            const isAdmin = username === ADMIN_USERNAME
            const isPending = node.pending === true
            const hasChildren = node.children.length > 0
            const isExpanded = expanded.has(username)
            const label = node.label ?? username
            const lines: { text: string; dim: boolean }[] = [
              { text: label.length > 11 ? label.slice(0, 10) + '…' : label, dim: false },
            ]
            if (node.deviceCount > 1) lines.push({ text: `${node.deviceCount} devices`, dim: true })
            if (node.children.length > 0) lines.push({ text: `${node.children.length} invited`, dim: true })
            const lineH = 11
            const topY = y - ((lines.length - 1) * lineH) / 2

            return (
              <g key={username}
                onClick={() => hasChildren && toggle(username)}
                onMouseDown={(e) => e.stopPropagation()}
                style={{ cursor: hasChildren ? 'pointer' : 'default', userSelect: 'none' }}
              >
                <circle cx={x} cy={y} r={NODE_R}
                  style={{
                    fill: isAdmin ? 'var(--primary)' : 'var(--card)',
                    stroke: isAdmin ? 'var(--primary)' : isPending ? '#3b82f6' : isExpanded ? 'var(--ring)' : 'var(--border)',
                    strokeWidth: isPending || (isExpanded && !isAdmin) ? 2 : 1.5,
                  }}
                />
                {lines.map((line, i) => (
                  <text key={i} x={x} y={topY + i * lineH}
                    textAnchor="middle" dominantBaseline="middle"
                    fontSize={i === 0 ? 11 : 10} fontWeight={i === 0 ? 600 : 400}
                    style={{
                      fill: line.dim
                        ? isAdmin ? 'var(--primary-foreground)' : 'var(--muted-foreground)'
                        : isAdmin ? 'var(--primary-foreground)' : 'var(--foreground)',
                    }}
                  >
                    {line.text}
                  </text>
                ))}
              </g>
            )
          })}
        </g>
      </svg>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Main dialog
// ---------------------------------------------------------------------------

// ManageUsers is the admin dialog for inspecting and removing users and their
// devices. Includes a search filter and a graph view of invitation relationships.
export function ManageUsers({ open, onOpenChange }: ManageUsersProps) {
  const [devices, setDevices] = useState<Device[]>([])
  const [invites, setInvites] = useState<PendingInvite[]>([])
  const [loading, setLoading] = useState(false)
  const [resendInvite, setResendInvite] = useState<PendingInvite | null>(null)
  const [pending, setPending] = useState<Omit<ConfirmDialogProps, 'open' | 'onOpenChange'> | null>(null)
  const [search, setSearch] = useState('')
  const [showUsers, setShowUsers] = useState(true)
  const [showInvites, setShowInvites] = useState(true)
  const [viewMode, setViewMode] = useState<'list' | 'graph'>('list')
  const [bwUser, setBwUser] = useState<{
    username: string
    bytes: number
    halfSat: number
  } | null>(null)
  const [torrentCounts, setTorrentCounts] = useState<Record<string, number>>({})
  const [proxyMap, setProxyMap] = useState<Record<string, string>>({})

  const refresh = useCallback(() => {
    setLoading(true)
    Promise.all([listUsers(), listInvites(), listTorrents({ limit: 0 })])
      .then(([d, i, page]) => {
        setDevices(d)
        setInvites(i)
        const counts: Record<string, number> = {}
        for (const tr of page.items) {
          const owner = ownerOf(tr.location)
          if (owner) counts[owner] = (counts[owner] ?? 0) + 1
        }
        setTorrentCounts(counts)
        const unames = [...new Set(d.map((dev) => dev.username))]
        Promise.allSettled(unames.map((u) => getUserProxy(u))).then((results) => {
          const pm: Record<string, string> = {}
          for (let idx = 0; idx < unames.length; idx++) {
            const r = results[idx]
            if (r.status === 'fulfilled') pm[unames[idx]] = r.value.proxy
          }
          setProxyMap(pm)
        })
      })
      .catch((e) => toast.error(`Load failed: ${String(e)}`))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    if (open) refresh()
  }, [open, refresh])

  function delDevice(id: string) {
    setPending({
      title: 'Unregister device',
      description: 'This device will be unregistered. The user will no longer be able to log in with it.',
      confirmLabel: 'Unregister',
      onConfirm: async () => {
        try {
          await removeCredential(id)
          toast.success('Device unregistered')
          refresh()
        } catch (e) {
          toast.error(`Remove failed: ${String(e)}`)
        }
      },
    })
  }

  async function delInvite(token: string) {
    try {
      await revokeInvite(token)
      toast.success('Invite revoked')
      refresh()
    } catch (e) {
      toast.error(`Revoke failed: ${String(e)}`)
    }
  }

  function delUser(username: string) {
    setPending({
      title: `Remove "${username}"?`,
      description: 'This permanently deletes all their devices and torrents. This cannot be undone.',
      confirmLabel: 'Remove user',
      onConfirm: async () => {
        try {
          await removeUser(username)
          toast.success(`Removed ${username}`)
          refresh()
        } catch (e) {
          toast.error(`Remove failed: ${String(e)}`)
        }
      },
    })
  }

  // Group devices by username for list view.
  const groups = new Map<string, Device[]>()
  for (const d of devices) {
    const list = groups.get(d.username) ?? []
    list.push(d)
    groups.set(d.username, list)
  }
  const q = search.trim().toLowerCase()
  const usernames = [...groups.keys()]
    .filter((u) => !q || u.toLowerCase().includes(q))
    .sort((a, b) =>
      a === ADMIN_USERNAME ? -1 : b === ADMIN_USERNAME ? 1 : a.localeCompare(b),
    )
  const filteredInvites = invites.filter(
    (i) => !q || (i.username || '').toLowerCase().includes(q),
  )

  const graphRoots = buildGraph(devices, invites)

  return (
    <>
      <Dialog open={open} onOpenChange={onOpenChange}>
        <DialogContent className="sm:max-w-5xl">
          <DialogHeader>
            <DialogTitle>Users &amp; devices</DialogTitle>
            <DialogDescription>
              Unregister a device, or remove a whole user — removing a user also
              deletes their torrents.
            </DialogDescription>
          </DialogHeader>

          {/* View toggle */}
          <div className="flex gap-2">
            <Button
              variant={viewMode === 'list' ? 'secondary' : 'outline'}
              size="sm"
              onClick={() => setViewMode('list')}
            >
              <List className="mr-2 h-4 w-4" /> List
            </Button>
            <Button
              variant={viewMode === 'graph' ? 'secondary' : 'outline'}
              size="sm"
              onClick={() => setViewMode('graph')}
            >
              <Network className="mr-2 h-4 w-4" /> Graph
            </Button>
          </div>

          {loading ? (
            <p className="text-muted-foreground py-6 text-center text-sm">Loading…</p>
          ) : viewMode === 'graph' ? (
            <UserGraph roots={graphRoots} />
          ) : (
            <div className="flex gap-4">
              {/* Left column: filters */}
              <div className="flex w-44 shrink-0 flex-col gap-2">
                <Input
                  placeholder="Filter by user…"
                  value={search}
                  onChange={(e) => setSearch(e.target.value)}
                  className="h-8 text-sm"
                  list="mu-user-filter"
                />
                <datalist id="mu-user-filter">
                  {[...groups.keys()].sort().map((u) => (
                    <option key={u} value={u} />
                  ))}
                </datalist>
                <Button
                  variant={showUsers ? 'secondary' : 'outline'}
                  size="sm"
                  className="justify-start"
                  onClick={() => setShowUsers((v) => !v)}
                >
                  <Users className="mr-2 h-4 w-4" /> Users
                </Button>
                <Button
                  variant={showInvites ? 'secondary' : 'outline'}
                  size="sm"
                  className="justify-start"
                  onClick={() => setShowInvites((v) => !v)}
                >
                  <Ticket className="mr-2 h-4 w-4" /> Invites
                </Button>
              </div>

              {/* Right column: list */}
              <div className="flex min-w-0 max-h-[32rem] flex-1 flex-col gap-4 overflow-y-auto">
                {showUsers && (
                  <div className="flex flex-col gap-3">
                    {usernames.map((username) => {
                      const admin = username === ADMIN_USERNAME
                      return (
                        <div key={username} className="rounded-lg border p-3">
                          <div className="flex items-center justify-between">
                            <span className="font-medium">
                              {username}
                              {admin && (
                                <span className="text-muted-foreground"> · admin</span>
                              )}
                              <span className="text-muted-foreground font-normal">
                                {' · '}
                                {torrentCounts[username] ?? 0} torrent
                                {(torrentCounts[username] ?? 0) === 1 ? '' : 's'}
                              </span>
                              {(() => {
                                const pendingCount = invites.filter((i) => i.createdBy === username).length
                                return pendingCount > 0 ? (
                                  <span className="text-muted-foreground font-normal">
                                    {' · '}{pendingCount} pending invite{pendingCount === 1 ? '' : 's'}
                                  </span>
                                ) : null
                              })()}
                              {proxyMap[username] && (
                                <span className="text-muted-foreground font-normal font-mono text-xs">
                                  {' · '}{proxyMap[username]}
                                </span>
                              )}
                            </span>
                            <div className="flex items-center gap-1">
                              <Button
                                variant="ghost"
                                size="icon"
                                aria-label="Set bandwidth"
                                onClick={() =>
                                  setBwUser({
                                    username,
                                    bytes: (groups.get(username) ?? [])[0]?.bandwidth ?? 0,
                                    halfSat: (groups.get(username) ?? [])[0]?.halfSaturation ?? 4,
                                  })
                                }
                              >
                                <Gauge />
                              </Button>
                              {!admin && (
                                <Button
                                  variant="ghost"
                                  size="sm"
                                  className="text-destructive"
                                  onClick={() => delUser(username)}
                                >
                                  Remove user
                                </Button>
                              )}
                            </div>
                          </div>
                          <ul className="mt-2 flex flex-col gap-1">
                            {(groups.get(username) ?? []).map((d) => (
                              <li
                                key={d.id}
                                className="text-muted-foreground flex items-center justify-between text-sm"
                              >
                                <span>Device · added {formatRelative(d.addedAt)}</span>
                                {!admin && (
                                  <Button
                                    variant="ghost"
                                    size="icon"
                                    aria-label="Unregister device"
                                    className="hover:text-destructive"
                                    onClick={() => delDevice(d.id)}
                                  >
                                    <Trash2 />
                                  </Button>
                                )}
                              </li>
                            ))}
                          </ul>
                        </div>
                      )
                    })}
                  </div>
                )}

                {showInvites && filteredInvites.length > 0 && (
                  <div>
                    <p className="text-muted-foreground mb-2 text-xs font-medium uppercase tracking-wide">
                      Pending invites
                    </p>
                    <div className="flex flex-col gap-2">
                      {filteredInvites.map((inv) => (
                        <div
                          key={inv.token}
                          className="flex items-center justify-between rounded-lg border p-3"
                        >
                          <div>
                            <button
                              className="font-medium underline-offset-2 hover:underline"
                              onClick={() => setResendInvite(inv)}
                            >
                              {inv.username || <em>bootstrap</em>}
                            </button>
                            <span className="text-muted-foreground ml-2 text-sm">
                              · expires in {formatETA(inv.expiresAt)}
                            </span>
                          </div>
                          <Button
                            variant="ghost"
                            size="icon"
                            aria-label="Revoke invite"
                            className="hover:text-destructive"
                            onClick={() => delInvite(inv.token)}
                          >
                            <Trash2 />
                          </Button>
                        </div>
                      ))}
                    </div>
                  </div>
                )}
              </div>
            </div>
          )}
        </DialogContent>
      </Dialog>

      {resendInvite && (
        <Dialog open onOpenChange={(open) => !open && setResendInvite(null)}>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Invite for &ldquo;{resendInvite.username}&rdquo;</DialogTitle>
              <DialogDescription>
                Scan the QR code on the new device, share the link, or read the code
                aloud. One-time use, expires in {formatETA(resendInvite.expiresAt)}.
              </DialogDescription>
            </DialogHeader>
            <InvitePanel
              url={`${window.location.origin}/r/${resendInvite.token}`}
              code={resendInvite.token}
            />
          </DialogContent>
        </Dialog>
      )}

      {pending && (
        <ConfirmDialog
          open
          onOpenChange={(v) => { if (!v) setPending(null) }}
          {...pending}
        />
      )}

      {bwUser && (
        <BandwidthDialog
          open
          onOpenChange={(v) => { if (!v) setBwUser(null) }}
          username={bwUser.username}
          initialBytes={bwUser.bytes}
          initialHalfSat={bwUser.halfSat}
          onSaved={refresh}
        />
      )}
    </>
  )
}
