import { act, fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { useDesiredControl } from '../hooks/useDesiredControl'
import type { NumericControlSnapshot } from '../model/loadgen'
import { NumericControl } from './NumericControl'

function control(
  applied: number | null,
  applyMode: NumericControlSnapshot['applyMode'] = 'immediate',
): NumericControlSnapshot {
  return {
    applied,
    min: 0,
    max: 12,
    unit: 'batches',
    applyMode,
  }
}

function LiveNumericControl({
  snapshot,
  dispatch,
  revision = 1,
}: {
  snapshot: NumericControlSnapshot
  dispatch: (value: number) => Promise<{ accepted: boolean }>
  revision?: number
}) {
  const liveControl = useDesiredControl({
    applied: snapshot.applied ?? snapshot.min,
    revision,
    available: snapshot.applied !== null && snapshot.applyMode === 'immediate',
    dispatch,
  })
  return (
    <>
      <button type="button" onClick={() => liveControl.preview(7)}>
        Preview 7
      </button>
      <button type="button" onClick={liveControl.cancel}>
        Cancel preview
      </button>
      <output aria-label="Control phase">{liveControl.phase}</output>
      <NumericControl label="Capacity" control={snapshot} desiredControl={liveControl} />
    </>
  )
}

describe('NumericControl', () => {
  it.each(['Enter', 'blur'])(
    'commits an exact TPS between valve positions on %s',
    async (finish) => {
      const user = userEvent.setup()
      const dispatch = vi.fn().mockResolvedValue({ accepted: true })
      render(
        <LiveNumericControl
          snapshot={{ ...control(2_000_000), max: 4_000_000 }}
          dispatch={dispatch}
        />,
      )
      const input = screen.getByRole<HTMLInputElement>('spinbutton', { name: /^Capacity/ })
      await user.clear(input)
      await user.type(input, '2000001')
      expect(input.checkValidity()).toBe(true)
      if (finish === 'Enter') await user.keyboard('{Enter}')
      else await user.tab()
      expect(dispatch.mock.calls).toEqual([[2_000_001]])
      expect(input.value).toBe('2000001')
    },
  )

  it('shows the applied value after external cancellation while focused without committing on Tab', async () => {
    const user = userEvent.setup()
    const dispatch = vi.fn().mockResolvedValue({ accepted: true })
    render(<LiveNumericControl snapshot={control(4)} dispatch={dispatch} />)
    const input = screen.getByRole<HTMLInputElement>('spinbutton', { name: /^Capacity/ })

    await user.click(screen.getByRole('button', { name: 'Preview 7' }))
    await user.click(input)
    expect(input.value).toBe('7')
    expect(screen.getByLabelText('Control phase').textContent).toBe('preview')

    fireEvent.click(screen.getByRole('button', { name: 'Cancel preview' }))
    expect(document.activeElement).toBe(input)
    expect(input.value).toBe('4')
    expect(screen.getByLabelText('Control phase').textContent).toBe('idle')

    await user.tab()
    expect(document.activeElement).not.toBe(input)
    expect(dispatch).not.toHaveBeenCalled()
  })

  it('does not commit an external preview on Enter without editing', async () => {
    const user = userEvent.setup()
    const dispatch = vi.fn().mockResolvedValue({ accepted: true })
    render(<LiveNumericControl snapshot={control(4)} dispatch={dispatch} />)
    const input = screen.getByRole<HTMLInputElement>('spinbutton', { name: /^Capacity/ })

    await user.click(screen.getByRole('button', { name: 'Preview 7' }))
    await user.click(input)
    await user.keyboard('{Enter}')

    expect(input.value).toBe('7')
    expect(screen.getByLabelText('Control phase').textContent).toBe('preview')
    expect(dispatch).not.toHaveBeenCalled()
  })

  it.each([
    { reason: 'empty', value: '' },
    { reason: 'below min', value: '-1' },
    { reason: 'above max', value: '13' },
    { reason: 'off step', value: '7.5' },
  ])('cancels preview immediately while the draft is $reason', async ({ value }) => {
    const user = userEvent.setup()
    const dispatch = vi.fn()
    render(<LiveNumericControl snapshot={control(4)} dispatch={dispatch} />)
    const input = screen.getByRole<HTMLInputElement>('spinbutton', { name: /^Capacity/ })
    const phase = screen.getByLabelText('Control phase')

    expect(input.value).toBe('4')
    await user.click(input)
    fireEvent.change(input, { target: { value: '7' } })
    expect(input.value).toBe('7')
    expect(phase.textContent).toBe('preview')

    fireEvent.change(input, { target: { value } })
    expect(input.value).toBe(value)
    expect(document.activeElement).toBe(input)
    expect(dispatch).not.toHaveBeenCalled()
    expect(phase.textContent).toBe('idle')
  })

  it.each(['Escape', 'Enter'])(
    'restores the applied value when %s ends an empty draft',
    async (key) => {
      const user = userEvent.setup()
      const dispatch = vi.fn()
      render(<LiveNumericControl snapshot={control(4)} dispatch={dispatch} />)
      const input = screen.getByRole<HTMLInputElement>('spinbutton', { name: /^Capacity/ })

      await user.click(screen.getByRole('button', { name: 'Preview 7' }))
      expect(input.value).toBe('7')
      await user.click(input)
      expect(input.value).toBe('7')
      await user.clear(input)
      expect(input.value).toBe('')
      expect(screen.getByLabelText('Control phase').textContent).toBe('idle')

      await user.keyboard('{' + key + '}')
      expect(input.value).toBe('4')
      expect(screen.getByLabelText('Control phase').textContent).toBe('idle')
      expect(dispatch).not.toHaveBeenCalled()
    },
  )

  it('keeps the Enter value pending until an acknowledging snapshot', async () => {
    const user = userEvent.setup()
    const dispatch = vi.fn().mockResolvedValue({ accepted: true })
    const { rerender } = render(<LiveNumericControl snapshot={control(4)} dispatch={dispatch} />)
    const input = screen.getByRole<HTMLInputElement>('spinbutton', { name: /^Capacity/ })

    await user.clear(input)
    await user.type(input, '8{Enter}')
    expect(dispatch.mock.calls).toEqual([[8]])
    expect(input.disabled).toBe(true)
    expect(input.value).toBe('8')
    expect(screen.getByLabelText('Control phase').textContent).toBe('pending')

    rerender(<LiveNumericControl snapshot={control(5)} dispatch={dispatch} revision={2} />)
    expect(input.disabled).toBe(true)
    expect(input.value).toBe('8')
    expect(screen.getByLabelText('Control phase').textContent).toBe('pending')

    rerender(<LiveNumericControl snapshot={control(8)} dispatch={dispatch} revision={3} />)
    expect(input.disabled).toBe(false)
    expect(input.value).toBe('8')
    expect(screen.getByLabelText('Control phase').textContent).toBe('idle')
    expect(dispatch.mock.calls).toEqual([[8]])
  })

  it('returns to the latest applied value when the pending command is rejected', async () => {
    const user = userEvent.setup()
    let resolveReceipt!: (receipt: { accepted: boolean }) => void
    const dispatch = vi.fn(
      () =>
        new Promise<{ accepted: boolean }>((resolve) => {
          resolveReceipt = resolve
        }),
    )
    const { rerender } = render(<LiveNumericControl snapshot={control(4)} dispatch={dispatch} />)
    const input = screen.getByRole<HTMLInputElement>('spinbutton', { name: /^Capacity/ })

    await user.clear(input)
    await user.type(input, '8{Enter}')
    rerender(<LiveNumericControl snapshot={control(5)} dispatch={dispatch} revision={2} />)
    expect(input.value).toBe('8')
    expect(screen.getByLabelText('Control phase').textContent).toBe('pending')

    await act(async () => {
      resolveReceipt({ accepted: false })
    })
    expect(input.disabled).toBe(false)
    expect(input.value).toBe('5')
    expect(screen.getByLabelText('Control phase').textContent).toBe('idle')
    expect(dispatch.mock.calls).toEqual([[8]])
  })

  it('clears an external preview on loss of availability and displays the applied value on recovery', async () => {
    const user = userEvent.setup()
    const dispatch = vi.fn()
    const { rerender } = render(<LiveNumericControl snapshot={control(4)} dispatch={dispatch} />)
    const input = screen.getByRole<HTMLInputElement>('spinbutton', { name: /^Capacity/ })
    const phase = screen.getByLabelText('Control phase')

    expect(input.value).toBe('4')
    await user.click(screen.getByRole('button', { name: 'Preview 7' }))
    expect(input.value).toBe('7')
    expect(phase.textContent).toBe('preview')

    rerender(
      <LiveNumericControl
        snapshot={control(null, 'unavailable')}
        revision={2}
        dispatch={dispatch}
      />,
    )
    expect(input.disabled).toBe(true)
    expect(input.value).toBe('')
    expect(phase.textContent).toBe('idle')

    await user.click(screen.getByRole('button', { name: 'Preview 7' }))
    expect(input.value).toBe('')
    expect(phase.textContent).toBe('idle')

    rerender(<LiveNumericControl snapshot={control(5)} revision={3} dispatch={dispatch} />)
    expect(input.disabled).toBe(false)
    expect(input.value).toBe('5')
    expect(phase.textContent).toBe('idle')
    expect(dispatch).not.toHaveBeenCalled()
  })

  it('keeps typing local and commits once with Enter or blur', async () => {
    const user = userEvent.setup()
    const dispatch = vi.fn().mockResolvedValue({ accepted: true })
    const { rerender } = render(<LiveNumericControl snapshot={control(4)} dispatch={dispatch} />)
    const input = screen.getByRole<HTMLInputElement>('spinbutton', { name: /^Capacity/ })
    const phase = screen.getByLabelText('Control phase')

    await user.clear(input)
    await user.type(input, '7')
    expect(input.value).toBe('7')
    expect(phase.textContent).toBe('preview')
    expect(dispatch).not.toHaveBeenCalled()
    await user.keyboard('{Enter}')

    expect(dispatch.mock.calls).toEqual([[7]])
    expect(input).not.toBe(document.activeElement)
    expect(input.value).toBe('7')
    expect(phase.textContent).toBe('pending')
    rerender(<LiveNumericControl snapshot={control(7)} revision={2} dispatch={dispatch} />)
    expect(phase.textContent).toBe('idle')

    await user.click(input)
    await user.clear(input)
    await user.type(input, '8')
    await user.tab()

    expect(dispatch.mock.calls).toEqual([[7], [8]])
    expect(input.value).toBe('8')
    expect(phase.textContent).toBe('pending')
  })

  it('cancels Escape and invalid blur with the live controller', async () => {
    const user = userEvent.setup()
    const dispatch = vi.fn()
    render(<LiveNumericControl snapshot={control(4)} dispatch={dispatch} />)
    const input = screen.getByRole<HTMLInputElement>('spinbutton', { name: /^Capacity/ })
    const phase = screen.getByLabelText('Control phase')

    await user.clear(input)
    await user.type(input, '9')
    await user.keyboard('{Escape}')

    expect(dispatch).not.toHaveBeenCalled()
    expect(input.value).toBe('4')
    expect(phase.textContent).toBe('idle')
    expect(input).not.toBe(document.activeElement)

    await user.click(input)
    await user.clear(input)
    await user.tab()
    expect(dispatch).not.toHaveBeenCalled()
    expect(input.value).toBe('4')
    expect(phase.textContent).toBe('idle')

    await user.click(input)
    await user.clear(input)
    await user.type(input, '99')
    await user.tab()
    expect(dispatch).not.toHaveBeenCalled()
    expect(input.value).toBe('4')
    expect(phase.textContent).toBe('idle')
  })

  it('blocks editing when the control is unavailable', async () => {
    const user = userEvent.setup()
    const dispatch = vi.fn()
    render(<LiveNumericControl snapshot={control(4, 'unavailable')} dispatch={dispatch} />)
    const input = screen.getByRole<HTMLInputElement>('spinbutton', { name: /^Capacity/ })

    await user.click(input)
    await user.keyboard('9')

    expect(input.disabled).toBe(true)
    expect(input.value).toBe('4')
    expect(screen.getByLabelText('Control phase').textContent).toBe('idle')
    expect(dispatch).not.toHaveBeenCalled()
  })

  it('does not commit a draft after the control becomes unavailable', async () => {
    const user = userEvent.setup()
    const dispatch = vi.fn()
    const { rerender } = render(<LiveNumericControl snapshot={control(4)} dispatch={dispatch} />)
    const input = screen.getByRole<HTMLInputElement>('spinbutton', { name: /^Capacity/ })
    const phase = screen.getByLabelText('Control phase')

    await user.click(input)
    await user.clear(input)
    await user.type(input, '7')
    expect(phase.textContent).toBe('preview')
    rerender(
      <LiveNumericControl snapshot={control(4, 'unavailable')} revision={2} dispatch={dispatch} />,
    )

    expect(input.disabled).toBe(true)
    expect(input.value).toBe('4')
    expect(phase.textContent).toBe('idle')
    expect(dispatch).not.toHaveBeenCalled()

    fireEvent.blur(input)
    expect(dispatch).not.toHaveBeenCalled()
  })

  it('drops an incomplete draft through repeated unavailable renders and recovers without dispatch', async () => {
    const user = userEvent.setup()
    const dispatch = vi.fn()
    const unavailable = control(4, 'unavailable')
    const { rerender } = render(<LiveNumericControl snapshot={unavailable} dispatch={dispatch} />)
    const input = screen.getByRole<HTMLInputElement>('spinbutton', { name: /^Capacity/ })
    const phase = screen.getByLabelText('Control phase')
    expect(input.disabled).toBe(true)
    expect(phase.textContent).toBe('idle')
    expect(dispatch).not.toHaveBeenCalled()

    rerender(<LiveNumericControl snapshot={control(4)} revision={2} dispatch={dispatch} />)
    await user.click(input)
    await user.clear(input)
    await user.type(input, '7')
    expect(input.value).toBe('7')
    await user.clear(input)
    expect(input.value).toBe('')
    expect(phase.textContent).toBe('idle')

    rerender(<LiveNumericControl snapshot={unavailable} revision={3} dispatch={dispatch} />)
    expect(input.disabled).toBe(true)
    expect(input.value).toBe('4')
    expect(phase.textContent).toBe('idle')
    expect(dispatch).not.toHaveBeenCalled()

    fireEvent.blur(input)
    rerender(<LiveNumericControl snapshot={unavailable} revision={4} dispatch={dispatch} />)
    expect(input.disabled).toBe(true)
    expect(input.value).toBe('4')
    expect(phase.textContent).toBe('idle')
    expect(dispatch).not.toHaveBeenCalled()

    rerender(<LiveNumericControl snapshot={control(5)} revision={5} dispatch={dispatch} />)
    expect(input.disabled).toBe(false)
    expect(input.value).toBe('5')
    fireEvent.keyDown(input, { key: 'Enter' })
    fireEvent.blur(input)
    expect(input.value).toBe('5')
    expect(phase.textContent).toBe('idle')
    expect(dispatch).not.toHaveBeenCalled()
  })
})
