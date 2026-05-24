import { useEffect, useState } from 'react'

import { Button } from '@/components/ui/button'

const STORAGE_KEY = 'emission-cookie-ack'

/**
 * CookieNotice is a one-time, discrete banner about the lone session cookie
 * emission sets. The cookie is functionally required for login; this notice
 * just informs the user. Dismissed via localStorage so it never reappears
 * for a returning visitor on the same device.
 */
export function CookieNotice() {
  const [visible, setVisible] = useState(false)

  useEffect(() => {
    try {
      if (!localStorage.getItem(STORAGE_KEY)) setVisible(true)
    } catch {
      // localStorage may be unavailable (private mode); show once anyway.
      setVisible(true)
    }
  }, [])

  if (!visible) return null

  const accept = () => {
    try {
      localStorage.setItem(STORAGE_KEY, '1')
    } catch {
      // best effort
    }
    setVisible(false)
  }

  return (
    <div
      role="dialog"
      aria-label="Cookie notice"
      className="fixed right-4 bottom-4 z-50 max-w-sm rounded-lg border bg-card text-card-foreground shadow-lg"
    >
      <div className="flex flex-col gap-3 p-4 text-sm">
        <p>
          emission sets a single <span className="font-mono text-xs">emission_session</span>{' '}
          cookie to keep you logged in. No tracking, no analytics, no third
          parties.
        </p>
        <div className="flex justify-end">
          <Button size="sm" onClick={accept}>
            Got it
          </Button>
        </div>
      </div>
    </div>
  )
}
