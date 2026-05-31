import { type RefObject } from 'react'
import { Gauge, LogOut, Menu, Moon, Plus, Smartphone, Sun, User, Users, UserPlus } from 'lucide-react'
import { useTheme } from 'next-themes'
import { DropdownMenu } from 'radix-ui'

import { logout } from '@/lib/api'
import { AuthControls, type AuthControlsHandle } from '@/components/auth-controls'
import { ThemeToggle } from '@/components/theme-toggle'
import { Button } from '@/components/ui/button'

const MENU_ITEM =
  'flex cursor-default items-center gap-2 rounded-md px-2 py-1.5 text-sm outline-none hover:bg-accent'

interface DashboardHeaderProps {
  authEnabled: boolean
  username?: string
  isAdmin: boolean
  uploading: boolean
  wsConnected: boolean
  fileInputRef: RefObject<HTMLInputElement | null>
  authRef: RefObject<AuthControlsHandle | null>
  onAddClick: () => void
  onFilePicked: (files: FileList | null) => void
  onSignedOut: () => void
}

export function DashboardHeader({
  authEnabled,
  username,
  isAdmin,
  uploading,
  wsConnected,
  fileInputRef,
  authRef,
  onAddClick,
  onFilePicked,
  onSignedOut,
}: DashboardHeaderProps) {
  const { resolvedTheme, setTheme } = useTheme()

  return (
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
          <Button size="sm" disabled={uploading} onClick={onAddClick} aria-label="Add torrent">
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
          onClick={onAddClick}
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
                  className={MENU_ITEM}
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
                      <DropdownMenu.Item className={MENU_ITEM} onSelect={() => authRef.current?.addDevice()}>
                        <Smartphone className="size-4" /> Add device
                      </DropdownMenu.Item>
                    )}
                    <DropdownMenu.Item className={MENU_ITEM} onSelect={() => authRef.current?.openInvite()}>
                      <UserPlus className="size-4" /> Invite user
                    </DropdownMenu.Item>
                    {isAdmin && (
                      <DropdownMenu.Item className={MENU_ITEM} onSelect={() => authRef.current?.openManage()}>
                        <Users className="size-4" /> Manage users
                      </DropdownMenu.Item>
                    )}
                    <DropdownMenu.Separator className="-mx-1 my-1 h-px bg-border" />
                    <DropdownMenu.Item
                      className={`text-destructive ${MENU_ITEM}`}
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
  )
}
