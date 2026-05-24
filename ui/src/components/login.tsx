import { useState } from 'react'
import { Fingerprint, Gauge } from 'lucide-react'

import { login } from '@/lib/api'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'

// Login is the passkey sign-in screen, shown when auth is enabled and the
// client has no valid session.
export function Login({ onDone }: { onDone: () => void }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  async function signIn() {
    setBusy(true)
    setError('')
    try {
      await login()
      onDone()
    } catch (e) {
      setError(String(e))
      setBusy(false)
    }
  }

  return (
    <div className="bg-background text-foreground flex min-h-svh items-center justify-center px-4">
      <Card className="w-full max-w-sm">
        <CardHeader>
          <div className="flex items-center gap-3">
            <div className="bg-primary text-primary-foreground flex size-10 shrink-0 items-center justify-center rounded-lg">
              <Gauge className="size-5" />
            </div>
            <CardTitle className="text-2xl">emission</CardTitle>
          </div>
          <CardDescription className="mt-2">
            Sign in with your passkey to continue.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <Button className="w-full" disabled={busy} onClick={signIn}>
            <Fingerprint />
            {busy ? 'Waiting for passkey…' : 'Sign in with passkey'}
          </Button>
          {error && <p className="text-destructive mt-3 text-center text-sm">{error}</p>}
        </CardContent>
      </Card>
    </div>
  )
}
