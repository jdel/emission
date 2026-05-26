import { useEffect, useState } from 'react'

import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'

// CountdownButton starts desaturated with a red fill growing left-to-right over
// `seconds` seconds. The button becomes clickable only when fully filled.
function CountdownButton({
  children,
  seconds = 5,
  onConfirm,
}: {
  children: React.ReactNode
  seconds?: number
  onConfirm: () => void
}) {
  const [progress, setProgress] = useState(0)
  const ready = progress >= 100

  useEffect(() => {
    const start = Date.now()
    const total = seconds * 1000
    const id = setInterval(() => {
      const p = Math.min(100, ((Date.now() - start) / total) * 100)
      setProgress(p)
      if (p >= 100) clearInterval(id)
    }, 50)
    return () => clearInterval(id)
  }, [seconds])

  return (
    <button
      type="button"
      disabled={!ready}
      onClick={ready ? onConfirm : undefined}
      className="relative overflow-hidden rounded-md border px-4 py-2 text-sm font-medium"
      style={{
        borderColor: 'var(--destructive)',
        backgroundColor: ready ? 'var(--destructive)' : 'transparent',
        cursor: ready ? 'pointer' : 'not-allowed',
      }}
    >
      {!ready && (
        <span
          className="pointer-events-none absolute inset-0"
          style={{
            background: `linear-gradient(to right, var(--destructive) ${progress}%, transparent ${progress}%)`,
            opacity: 0.2,
          }}
        />
      )}
      <span
        className="pointer-events-none relative z-10"
        style={{ color: ready ? 'var(--destructive-foreground)' : 'var(--foreground)' }}
      >
        {children}
      </span>
    </button>
  )
}

export interface ConfirmDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  description: string
  confirmLabel?: string
  /** When true the confirm button uses a 5-second countdown fill. */
  danger?: boolean
  onConfirm: () => void
}

export function ConfirmDialog({
  open,
  onOpenChange,
  title,
  description,
  confirmLabel = 'Confirm',
  danger = false,
  onConfirm,
}: ConfirmDialogProps) {
  function handleConfirm() {
    onOpenChange(false)
    onConfirm()
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          {danger ? (
            <CountdownButton onConfirm={handleConfirm}>{confirmLabel}</CountdownButton>
          ) : (
            <Button variant="destructive" onClick={handleConfirm}>
              {confirmLabel}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
