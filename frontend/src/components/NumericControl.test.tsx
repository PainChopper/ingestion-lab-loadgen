import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import type { NumericControlSnapshot } from '../model/loadgen'
import { NumericControl } from './NumericControl'

function control(
  applied: number,
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

describe('NumericControl', () => {
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
})
