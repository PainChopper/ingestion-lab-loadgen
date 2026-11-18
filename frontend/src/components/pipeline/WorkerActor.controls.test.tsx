import { act, fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { useDesiredControl } from '../../hooks/useDesiredControl'
import { ACTOR_GEOMETRY } from './geometry'
import { WorkerActor } from './WorkerActor'

const sender = ACTOR_GEOMETRY.sender
const activityCounts = { idle: 0, reading: 0, blocked: 0, 'in-flight': 0, backoff: 0 }

function SenderWorkers({ onCommand }: { onCommand: (value: number) => void }) {
  const [applied, setApplied] = useState(5)
  const [revision, setRevision] = useState(0)
  const desiredControl = useDesiredControl({
    applied,
    revision,
    available: true,
    dispatch: async (value) => {
      onCommand(value)
      setApplied(value)
      setRevision((current) => current + 1)
      return { accepted: true }
    },
  })

  return (
    <svg>
      <WorkerActor
        actor="sender"
        title="Sender"
        titlePoint={sender.title}
        titleTone="sender"
        bounds={sender.bounds}
        controls={sender.controls}
        workers={{
          applied,
          preview: null,
          pending: null,
          min: 2,
          max: 10,
          step: 3,
          unit: 'workers',
          applyMode: 'immediate',
        }}
        activityCounts={activityCounts}
        drainingActivityCounts={activityCounts}
        runState="idle"
        outputPort={sender.ports.output}
        primaryMetric=""
        metricPoints={sender.metrics}
        selected={false}
        onSelect={() => undefined}
        desiredControl={desiredControl}
      />
    </svg>
  )
}

afterEach(() => vi.useRealTimers())

describe('WorkerActor controls', () => {
  it('disables the plus button after a click reaches the last valid worker count', async () => {
    const onCommand = vi.fn()
    const user = userEvent.setup()
    render(<SenderWorkers onCommand={onCommand} />)
    const increase = screen.getByRole<HTMLButtonElement>('button', { name: 'Add sender worker' })

    await user.click(increase)
    expect(screen.getByLabelText('Sender worker count, 8 applied').textContent).toBe('8')
    expect(increase.disabled).toBe(true)
    await user.click(increase)

    expect(onCommand.mock.calls).toEqual([[8]])
  })

  it.each([
    { name: 'Add sender worker', expected: 8 },
    { name: 'Remove sender worker', expected: 2 },
  ])('commits the held $name button once at the limit', async ({ name, expected }) => {
    vi.useFakeTimers()
    const onCommand = vi.fn()
    render(<SenderWorkers onCommand={onCommand} />)
    const button = screen.getByRole<HTMLButtonElement>('button', { name })

    fireEvent.pointerDown(button, { pointerId: 1 })
    expect(screen.getByRole('status').textContent).toBe(String(expected))
    expect(onCommand).not.toHaveBeenCalled()

    await act(async () => vi.advanceTimersByTime(660))
    expect(onCommand.mock.calls).toEqual([[expected]])
    expect(screen.getByLabelText(`Sender worker count, ${expected} applied`)).not.toBeNull()
    expect(button.disabled).toBe(true)

    fireEvent.pointerUp(window, { pointerId: 1 })
    fireEvent.click(button, { detail: 1 })
    act(() => vi.advanceTimersByTime(1_000))
    expect(onCommand.mock.calls).toEqual([[expected]])
  })
})
