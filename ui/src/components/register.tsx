import { useEffect, useState } from 'react'
import { Fingerprint, SatelliteDish } from 'lucide-react'

import { beginRegister, finishRegister, type RegisterChallenge } from '@/lib/api'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'

// Register is the device-enrolment screen, reached via an invite link
// (/register?invite=…). The invite fixes the username; the registrant only
// creates the passkey.
export function Register({ invite, onDone }: { invite: string; onDone: () => void }) {
  const [challenge, setChallenge] = useState<RegisterChallenge | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  // Validate the invite up front so we know whose passkey this is.
  useEffect(() => {
    beginRegister(invite)
      .then(setChallenge)
      .catch((e) => setError(String(e)))
  }, [invite])

  async function enroll() {
    if (!challenge) return
    setBusy(true)
    setError('')
    try {
      await finishRegister(challenge)
      window.history.replaceState({}, '', '/') // drop the one-time token
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
              <SatelliteDish className="size-5" />
            </div>
            <CardTitle className="text-2xl">emission</CardTitle>
          </div>
          <CardDescription className="mt-2">
            {challenge
              ? `Register a device for "${challenge.username}".`
              : 'Register a device to access emission.'}
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          {!challenge && !error && (
            <p className="text-muted-foreground text-center text-sm">Checking invite…</p>
          )}
          {challenge && (
            <Button className="w-full" disabled={busy} onClick={enroll}>
              <Fingerprint />
              {busy ? 'Registering…' : 'Register device'}
            </Button>
          )}
          {error && <p className="text-destructive text-center text-sm">{error}</p>}
        </CardContent>
      </Card>
    </div>
  )
}
