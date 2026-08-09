import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it } from 'vitest'
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
})
