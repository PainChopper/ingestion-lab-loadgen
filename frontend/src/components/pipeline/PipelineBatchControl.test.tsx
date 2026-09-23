import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import type { DesiredControl } from '../../hooks/useDesiredControl'
import type { NumericControlSnapshot } from '../../model/loadgen'
import { PipelineBatchControl } from './PipelineBatchControl'

const CONTROL: NumericControlSnapshot = {
  applied: 1_000,
  preview: null,
  pending: null,
  min: 1_000,
  max: 3_000,
  step: 1_000,
  unit: 'tx',
  applyMode: 'immediate',
}

function desiredControl(
  overrides: Partial<DesiredControl<number>> = {},
): DesiredControl<number> {
  return {
    applied: 1_000,
    desired: 1_000,
    phase: 'idle',
    error: null,
    available: true,
    preview: vi.fn(),
    cancel: vi.fn(),
    commit: vi.fn(async () => true),
    ...overrides,
  }
}

function renderControl(
  control: NumericControlSnapshot = CONTROL,
  desired = desiredControl(),
) {
  return render(
    <svg>
      <PipelineBatchControl
        control={control}
        desiredControl={desired}
        anchor={{ x: 430, y: 112 }}
      />
    </svg>,
  )
}

describe('PipelineBatchControl', () => {
  it('shows only the Batch value while keeping state in its accessible name', () => {
    const desired = desiredControl({ desired: 2_000, phase: 'preview' })
    const view = renderControl(CONTROL, desired)

    expect(screen.getByText('Batch 2,000 tx')).not.toBeNull()
    expect(view.container.textContent).toBe('−Batch 2,000 tx+')
    expect(view.container.textContent).not.toContain('Applied')
    expect(view.container.textContent).not.toContain('Pending')
    expect(view.container.textContent).not.toContain('Desired')
    expect(screen.getByRole('group').getAttribute('aria-label')).toBe(
      'Batch: Desired 2,000 tx; Applied 1,000 tx',
    )
  })

  it('commits one bounded step from click or keyboard activation', async () => {
    const user = userEvent.setup()
    const commit = vi.fn(async () => true)
    renderControl(CONTROL, desiredControl({ commit }))
    const increase = screen.getByRole('button', { name: 'Increase Batch' })

    await user.click(increase)
    fireEvent.keyDown(increase, { key: 'Enter' })

    expect(commit).toHaveBeenNthCalledWith(1, 2_000)
    expect(commit).toHaveBeenNthCalledWith(2, 2_000)
  })

  it('keeps unavailable and pending controls visible but inert', async () => {
    const user = userEvent.setup()
    const commit = vi.fn(async () => true)
    const desired = desiredControl({
      desired: 2_000,
      phase: 'pending',
      available: false,
      commit,
    })
    renderControl({ ...CONTROL, pending: 2_000 }, desired)
    const increase = screen.getByRole('button', { name: 'Increase Batch' })

    expect(screen.getByText('Batch 2,000 tx')).not.toBeNull()
    expect(increase.getAttribute('aria-disabled')).toBe('true')
    expect(increase.getAttribute('tabindex')).toBe('-1')
    await user.click(increase)
    expect(commit).not.toHaveBeenCalled()
  })
})
