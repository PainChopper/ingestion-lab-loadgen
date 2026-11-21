import { clsx } from 'clsx'
import { useId, useRef, useState } from 'react'
import type { ChangeEvent, FocusEvent, KeyboardEvent } from 'react'
import type { NumericControlSnapshot } from '../model/loadgen'
import type { DesiredControl } from '../hooks/useDesiredControl'

interface NumericControlProps {
  readonly label: string
  readonly control: NumericControlSnapshot
  readonly desiredControl: DesiredControl<number>
  readonly className?: string
}

export function NumericControl({ label, control, desiredControl, className }: NumericControlProps) {
  const inputId = useId()
  const unitId = `${inputId}-unit`
  const [draft, setDraft] = useState<string | null>(null)
  const suppressBlurCommit = useRef(false)
  const unavailable = control.applyMode === 'unavailable' || !desiredControl.available
  const pending = desiredControl.phase === 'pending'
  const editable = !unavailable && !pending
  const displayed =
    !unavailable && desiredControl.phase !== 'idle' ? desiredControl.desired : control.applied
  const displayedValue = displayed === null ? '' : String(displayed)
  if (!editable && draft !== null) setDraft(null)

  const handleFocus = () => {
    if (!editable) return
    suppressBlurCommit.current = false
  }

  const handleChange = (event: ChangeEvent<HTMLInputElement>) => {
    if (!editable) return
    setDraft(event.currentTarget.value)
    const value = event.currentTarget.valueAsNumber
    const valid = event.currentTarget.checkValidity() && Number.isSafeInteger(value)
    if (valid) {
      desiredControl.preview(value)
    } else {
      desiredControl.cancel()
    }
  }

  const commitDraft = (input: HTMLInputElement) => {
    if (!editable) return

    const value = input.valueAsNumber
    if (input.checkValidity() && Number.isSafeInteger(value)) {
      void desiredControl.commit(value)
    } else {
      desiredControl.cancel()
    }
  }

  const handleKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (!editable || draft === null) return
    if (event.key === 'Enter') {
      event.preventDefault()
      suppressBlurCommit.current = true
      setDraft(null)
      commitDraft(event.currentTarget)
      event.currentTarget.blur()
    }
    if (event.key === 'Escape') {
      event.preventDefault()
      suppressBlurCommit.current = true
      setDraft(null)
      desiredControl.cancel()
      event.currentTarget.blur()
    }
  }

  const handleBlur = (event: FocusEvent<HTMLInputElement>) => {
    setDraft(null)
    if (suppressBlurCommit.current) {
      suppressBlurCommit.current = false
      return
    }
    if (draft !== null) commitDraft(event.currentTarget)
  }

  return (
    <label className={clsx('numeric-control', className)} htmlFor={inputId}>
      <span className="numeric-control__label">{label}</span>
      <input
        id={inputId}
        type="number"
        min={control.min}
        max={control.max}
        step={1}
        value={editable ? (draft ?? displayedValue) : displayedValue}
        disabled={!editable}
        inputMode="numeric"
        aria-describedby={unitId}
        onFocus={handleFocus}
        onBlur={handleBlur}
        onChange={handleChange}
        onKeyDown={handleKeyDown}
      />
      <span id={unitId} className="numeric-control__unit">
        {control.unit}
      </span>
    </label>
  )
}
