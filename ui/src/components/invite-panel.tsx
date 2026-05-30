import { QRCodeSVG } from 'qrcode.react'

import { copyInvite } from '@/lib/invite'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

// InvitePanel renders the shared QR code + spoken code + copyable link shown
// whenever an invite is presented (new device, invite a user, resend invite).
export function InvitePanel({ url, code }: { url: string; code: string }) {
  return (
    <div className="flex flex-col items-center gap-4">
      <div className="rounded-lg bg-white p-3">
        <QRCodeSVG value={url} size={184} />
      </div>
      <p className="text-center font-mono text-lg font-medium tracking-tight">{code}</p>
      <div className="flex w-full gap-2">
        <Input
          readOnly
          value={url}
          onFocus={(e) => e.target.select()}
          className="font-mono text-xs"
        />
        <Button onClick={() => copyInvite(url)}>Copy</Button>
      </div>
    </div>
  )
}
