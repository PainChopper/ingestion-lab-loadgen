import { act, fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
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
    preview: null,
    pending: null,
    min: 0,
    max: 12,
    step: 1,
    unit: 'batches',
    applyMode,
  }
}

function StatefulNumericControl({
  onValueChange,
}: {
  onValueChange: (value: number) => void
}) {
  const [applied, setApplied] = useState(4)
  return (
    <NumericControl
      label="Capacity"
      control={control(applied)}
      onValueChange={(value) => {
        onValueChange(value)
        setApplied(value)
      }}
    />
  )
}

function LiveNumericControl({
  snapshot,
  dispatch,
  onPreviewChange,
  revision = 1,
}: {
  snapshot: NumericControlSnapshot
  dispatch: (value: number) => Promise<{ accepted: boolean }>
  onPreviewChange?: (value: number | null) => void
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
      <button type="button" onClick={() => liveControl.preview(7)}>Preview 7</button>
      <button type="button" onClick={liveControl.cancel}>Cancel preview</button>
      <NumericControl
        label="Capacity"
        control={snapshot}
        desiredControl={liveControl}
        onPreviewChange={onPreviewChange}
      />
    </>
  )
}

describe('NumericControl', () => {
  it('shows the applied value after external cancellation while focused without committing on Tab', async () => {
    const user = userEvent.setup()
    const dispatch = vi.fn().mockResolvedValue({ accepted: true })
    render(<LiveNumericControl snapshot={control(4)} dispatch={dispatch} />)
    const input = screen.getByRole<HTMLInputElement>('spinbutton', { name: /^Capacity/ })

    await user.click(screen.getByRole('button', { name: 'Preview 7' }))
    await user.click(input)
    expect(input.value).toBe('7')

    fireEvent.click(screen.getByRole('button', { name: 'Cancel preview' }))
    expect(document.activeElement).toBe(input)
    expect(input.value).toBe('4')

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
    expect(dispatch).not.toHaveBeenCalled()
  })

  it.each(['Escape', 'Enter'])(
    'clears an external preview when %s ends an empty draft',
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

      await user.keyboard('{' + key + '}')
      expect(input.value).toBe('4')
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

    rerender(<LiveNumericControl snapshot={control(5)} dispatch={dispatch} revision={2} />)
    expect(input.disabled).toBe(true)
    expect(input.value).toBe('8')

    rerender(<LiveNumericControl snapshot={control(8)} dispatch={dispatch} revision={3} />)
    expect(input.disabled).toBe(false)
    expect(input.value).toBe('8')
    expect(dispatch.mock.calls).toEqual([[8]])
  })

  it('returns to the latest applied value when the pending command is rejected', async () => {
    const user = userEvent.setup()
    let resolveReceipt!: (receipt: { accepted: boolean }) => void
    const dispatch = vi.fn(() => new Promise<{ accepted: boolean }>((resolve) => {
      resolveReceipt = resolve
    }))
    const { rerender } = render(<LiveNumericControl snapshot={control(4)} dispatch={dispatch} />)
    const input = screen.getByRole<HTMLInputElement>('spinbutton', { name: /^Capacity/ })

    await user.clear(input)
    await user.type(input, '8{Enter}')
    rerender(<LiveNumericControl snapshot={control(5)} dispatch={dispatch} revision={2} />)
    expect(input.value).toBe('8')

    await act(async () => { resolveReceipt({ accepted: false }) })
    expect(input.disabled).toBe(false)
    expect(input.value).toBe('5')
    expect(dispatch.mock.calls).toEqual([[8]])
  })

  it('clears a live preview once on loss of availability and displays the applied value on recovery', async () => {
    const user = userEvent.setup()
    const dispatch = vi.fn()
    const onPreviewChange = vi.fn()
    const callbacks = { dispatch, onPreviewChange }
    const { rerender } = render(<LiveNumericControl snapshot={control(4)} {...callbacks} />)
    const input = screen.getByRole<HTMLInputElement>('spinbutton', { name: /^Capacity/ })

    expect(input.value).toBe('4')
    await user.click(screen.getByRole('button', { name: 'Preview 7' }))
    expect(input.value).toBe('7')

    rerender(<LiveNumericControl snapshot={control(null, 'unavailable')} revision={2} {...callbacks} />)
    expect(input.disabled).toBe(true)
    expect(input.value).toBe('')
    expect(onPreviewChange.mock.calls).toEqual([[null]])

    await user.click(screen.getByRole('button', { name: 'Preview 7' }))
    expect(input.value).toBe('')
    rerender(<LiveNumericControl snapshot={control(5)} revision={3} {...callbacks} />)
    expect(input.disabled).toBe(false)
    expect(input.value).toBe('5')
    expect(onPreviewChange.mock.calls).toEqual([[null]])
    expect(dispatch).not.toHaveBeenCalled()
  })

  it('keeps typing local and commits once with Enter or blur', async () => {
    const user = userEvent.setup()
    const onValueChange = vi.fn()
    render(<StatefulNumericControl onValueChange={onValueChange} />)
    const input = screen.getByRole('spinbutton', { name: /^Capacity/ })

    await user.clear(input)
    await user.type(input, '7')
    expect(onValueChange).not.toHaveBeenCalled()
    await user.keyboard('{Enter}')

    expect(onValueChange).toHaveBeenCalledTimes(1)
    expect(onValueChange).toHaveBeenNthCalledWith(1, 7)
    expect(input).not.toBe(document.activeElement)
    expect((input as HTMLInputElement).value).toBe('7')

    await user.click(input)
    await user.clear(input)
    await user.type(input, '8')
    await user.tab()

    expect(onValueChange).toHaveBeenCalledTimes(2)
    expect(onValueChange).toHaveBeenNthCalledWith(2, 8)
    expect((input as HTMLInputElement).value).toBe('8')
  })

  it('cancels Escape and invalid blur against a stateful parent', async () => {
    const user = userEvent.setup()
    const onValueChange = vi.fn()
    render(<StatefulNumericControl onValueChange={onValueChange} />)
    const input = screen.getByRole('spinbutton', { name: /^Capacity/ })

    await user.clear(input)
    await user.type(input, '9')
    await user.keyboard('{Escape}')

    expect(onValueChange).not.toHaveBeenCalled()
    expect((input as HTMLInputElement).value).toBe('4')
    expect(input).not.toBe(document.activeElement)

    await user.click(input)
    await user.clear(input)
    await user.tab()
    expect(onValueChange).not.toHaveBeenCalled()
    expect((input as HTMLInputElement).value).toBe('4')

    await user.click(input)
    await user.clear(input)
    await user.type(input, '99')
    await user.tab()
    expect(onValueChange).not.toHaveBeenCalled()
    expect((input as HTMLInputElement).value).toBe('4')
  })

  it('blocks editing when the control is unavailable', async () => {
    const user = userEvent.setup()
    const onValueChange = vi.fn()
    render(
      <NumericControl
        label="Capacity"
        control={control(4, 'unavailable')}
        onValueChange={onValueChange}
      />,
    )
    const input = screen.getByRole('spinbutton', { name: /^Capacity/ })

    await user.click(input)
    await user.keyboard('9')

    expect((input as HTMLInputElement).disabled).toBe(true)
    expect((input as HTMLInputElement).value).toBe('4')
    expect(onValueChange).not.toHaveBeenCalled()
  })

  it('does not commit a draft after the control becomes unavailable', async () => {
    const user = userEvent.setup()
    const onValueChange = vi.fn()
    const onPreviewChange = vi.fn()
    const { rerender } = render(
      <NumericControl
        label="Capacity"
        control={control(4)}
        onValueChange={onValueChange}
        onPreviewChange={onPreviewChange}
      />,
    )
    const input = screen.getByRole('spinbutton', { name: /^Capacity/ })

    await user.click(input)
    await user.clear(input)
    await user.type(input, '7')
    onPreviewChange.mockClear()
    rerender(
      <NumericControl
        label="Capacity"
        control={control(4, 'unavailable')}
        onValueChange={onValueChange}
        onPreviewChange={onPreviewChange}
      />,
    )

    expect((input as HTMLInputElement).disabled).toBe(true)
    expect((input as HTMLInputElement).value).toBe('4')
    expect(onValueChange).not.toHaveBeenCalled()
    expect(onPreviewChange).toHaveBeenCalledWith(null)

    fireEvent.blur(input)
    expect(onValueChange).not.toHaveBeenCalled()
  })

  it('clears preview once with a stateful parent when availability changes', async () => {
    const user = userEvent.setup()
    const onValueChange = vi.fn()
    const onPreviewChange = vi.fn()
    function Parent({ applyMode }: { applyMode: NumericControlSnapshot['applyMode'] }) {
      const [, setPreview] = useState<{ value: number | null }>({ value: null })

      return (
        <NumericControl
          label="Capacity"
          control={control(4, applyMode)}
          onValueChange={onValueChange}
          onPreviewChange={(value) => {
            onPreviewChange(value)
            setPreview({ value })
          }}
        />
      )
    }

    const { rerender } = render(<Parent applyMode="unavailable" />)
    const input = screen.getByRole('spinbutton', { name: /^Capacity/ })
    expect((input as HTMLInputElement).disabled).toBe(true)
    expect(onPreviewChange).not.toHaveBeenCalled()

    rerender(<Parent applyMode="immediate" />)
    await user.click(input)
    await user.clear(input)
    await user.type(input, '7')
    expect((input as HTMLInputElement).value).toBe('7')
    onPreviewChange.mockClear()

    rerender(<Parent applyMode="unavailable" />)
    expect((input as HTMLInputElement).disabled).toBe(true)
    expect((input as HTMLInputElement).value).toBe('4')
    expect(onPreviewChange).toHaveBeenCalledTimes(1)
    expect(onPreviewChange).toHaveBeenCalledWith(null)
    expect(onValueChange).not.toHaveBeenCalled()

    fireEvent.blur(input)
    rerender(<Parent applyMode="unavailable" />)
    expect(onPreviewChange).toHaveBeenCalledTimes(1)
    expect(onValueChange).not.toHaveBeenCalled()

    rerender(<Parent applyMode="immediate" />)
    expect((input as HTMLInputElement).value).toBe('4')
    fireEvent.keyDown(input, { key: 'Enter' })
    fireEvent.blur(input)
    expect(onPreviewChange).toHaveBeenCalledTimes(1)
    expect(onValueChange).not.toHaveBeenCalled()
  })
})
