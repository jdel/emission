import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'

import { BandwidthDialog } from '@/components/bandwidth-dialog'
import { getUserProxy } from '@/lib/api'

// The seeding-profile slider is inverted (left = stealth/high halfSat, right
// = aggressive/low halfSat) so its visual order matches the preset buttons.
// That inversion (HS_MIN + HS_MAX - halfSat) is exactly the kind of thing
// that's easy to get backwards; pin it down here.
vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    getUserProxy: vi.fn().mockResolvedValue({ proxy: '', default: '', status: 'unknown' }),
  }
})

describe('BandwidthDialog seeding profile slider', () => {
  beforeEach(() => {
    vi.mocked(getUserProxy).mockClear()
  })

  async function renderDialog() {
    render(
      <BandwidthDialog
        open
        username="alice"
        initialBytes={0}
        initialHalfSat={4}
        onOpenChange={() => {}}
      />,
    )
    await waitFor(() => expect(getUserProxy).toHaveBeenCalled())
  }

  it('starts on the profile matching initialHalfSat', async () => {
    await renderDialog()
    expect(screen.getByText('Balanced (default)')).toBeInTheDocument()
  })

  it('clicking a preset moves the slider to the correctly inverted position', async () => {
    const user = userEvent.setup()
    await renderDialog()

    await user.click(screen.getByRole('button', { name: 'Stealth' }))
    // halfSat=10 -> slider value = HS_MIN+HS_MAX-halfSat = 1+10-10 = 1.
    expect(screen.getByRole('slider', { name: 'Seeding aggressiveness' })).toHaveValue('1')

    await user.click(screen.getByRole('button', { name: 'Aggressive' }))
    // halfSat=1 -> slider value = 1+10-1 = 10.
    expect(screen.getByRole('slider', { name: 'Seeding aggressiveness' })).toHaveValue('10')
  })

  it('dragging the slider to its left edge selects Stealth, not Aggressive', async () => {
    await renderDialog()
    const slider = screen.getByRole('slider', { name: 'Seeding aggressiveness' })
    fireEvent.change(slider, { target: { value: '1' } })
    expect(screen.getByText('Trickles; only ramps in big swarms')).toBeInTheDocument()
  })

  it('dragging the slider to its right edge selects Aggressive, not Stealth', async () => {
    await renderDialog()
    const slider = screen.getByRole('slider', { name: 'Seeding aggressiveness' })
    fireEvent.change(slider, { target: { value: '10' } })
    expect(screen.getByText('Near-max on almost any demand')).toBeInTheDocument()
  })
})
