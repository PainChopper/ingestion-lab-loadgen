import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { SimulationAdapter } from '../adapters/SimulationAdapter'
import { LabShell } from './LabShell'

let adapter: SimulationAdapter | null = null

afterEach(() => {
  adapter?.dispose()
  adapter = null
})

describe('LabShell', () => {
  it('runs, pauses, and resets through the mounted toolbar', async () => {
    const user = userEvent.setup()
    adapter = new SimulationAdapter()
    render(<LabShell adapter={adapter} />)

    await user.click(screen.getByRole('button', { name: 'Start run' }))
    await waitFor(() => {
      expect(screen.getByText('running')).not.toBeNull()
    })

    await user.click(screen.getByRole('button', { name: 'Pause run' }))
    await waitFor(() => {
      expect(screen.getByText('paused')).not.toBeNull()
    })

    await user.click(screen.getByRole('button', { name: 'Reset run' }))
    await waitFor(() => {
      expect(screen.getByText('idle')).not.toBeNull()
      expect(screen.getByText('00:00:00')).not.toBeNull()
    })
  })

  it('opens a real actor inspector and clears the selection', async () => {
    const user = userEvent.setup()
    adapter = new SimulationAdapter()
    render(<LabShell adapter={adapter} />)

    expect(screen.getByText('No pipeline object selected')).not.toBeNull()
    await user.click(screen.getByRole('button', { name: 'Inspect reader' }))

    expect(screen.getByRole('heading', { name: 'READER' })).not.toBeNull()
    expect(screen.getByLabelText('Reader configuration')).not.toBeNull()
    expect(screen.getByRole('button', { name: 'Inspect reader' })
      .getAttribute('aria-pressed')).toBe('true')

    await user.click(screen.getByRole('button', { name: 'Clear selection' }))
    expect(screen.getByText('No pipeline object selected')).not.toBeNull()
    expect(screen.getByRole('button', { name: 'Inspect reader' })
      .getAttribute('aria-pressed')).toBe('false')
  })

  it('shares numeric TPS preview and absolute command with the valve', async () => {
    const user = userEvent.setup()
    adapter = new SimulationAdapter()
    const dispatch = vi.spyOn(adapter, 'dispatch')
    render(<LabShell adapter={adapter} />)
    const input = screen.getByRole('spinbutton', { name: /^Requested TPS/ })
    const valve = screen.getByRole('slider', { name: 'Throttle opening' })

    expect(valve.getAttribute('aria-valuenow')).toBe('5')
    await user.clear(input)
    await user.type(input, '135000')
    expect(valve.getAttribute('aria-valuenow')).toBe('6')
    expect(dispatch).not.toHaveBeenCalled()

    await user.keyboard('{Escape}')
    expect(valve.getAttribute('aria-valuenow')).toBe('5')
    await user.click(input)
    await user.clear(input)
    await user.type(input, '135000')
    await user.keyboard('{Enter}')

    await waitFor(() => {
      expect(dispatch).toHaveBeenCalledWith({
        type: 'set-requested-tps',
        value: 135_000,
      })
      expect(valve.getAttribute('aria-valuenow')).toBe('6')
      expect(screen.getByText('55% OPEN')).not.toBeNull()
    })
  })
})
