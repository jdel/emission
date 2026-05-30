import { useCallback, useState } from 'react'
import { toast } from 'sonner'

import { createInvite } from '@/lib/api'

// copyInvite copies an invite link to the clipboard, with toast feedback.
export function copyInvite(url: string) {
  navigator.clipboard
    .writeText(url)
    .then(() => toast.success('Invite link copied'))
    .catch(() => toast.error('Could not copy — select the link manually'))
}

// useInvite manages minting a one-time invite link for a username.
export function useInvite() {
  const [url, setUrl] = useState('')
  const [code, setCode] = useState('')
  const [busy, setBusy] = useState(false)

  const mint = useCallback(async (username: string): Promise<boolean> => {
    setBusy(true)
    try {
      const res = await createInvite(username)
      setUrl(res.url)
      setCode(res.code)
      return true
    } catch (e) {
      toast.error(`Invite failed: ${String(e)}`)
      return false
    } finally {
      setBusy(false)
    }
  }, [])

  return { url, code, busy, mint }
}
