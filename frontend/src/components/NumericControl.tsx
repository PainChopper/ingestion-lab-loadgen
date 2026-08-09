import { useEffect, useId, useRef, useState } from 'react'
import type { ChangeEvent, KeyboardEvent } from 'react'
import type { NumericControlSnapshot } from '../model/loadgen'

interface NumericControlProps {
  readonly label: string
  readonly control: NumericControlSnapshot
  readonly onValueChange: (value: number) => void
  readonly className?: string
}

export function NumericControl({
  label,
  control,
  onValueChange,
  className,
}: NumericControlProps) {
  const inputId = useId()
  const unitId = `${inputId}-unit`
  const [editing, setEditing] = useState(false)
  const suppressBlurCommit = useRef(false)
  const [draft, setDraft] = useState(
    control.applied === null ? '' : String(control.applied),
  )
  const appliedDraft = control.applied === null ? '' : String(control.applied)

  useEffect(() => {
    if (!editing) {
      setDraft(appliedDraft)
    }
  }, [appliedDraft, editing])

  const handleChange = (event: ChangeEvent<HTMLInputElement>) => {
    setDraft(event.currentTarget.value)
  }

  const commitDraft = (input: HTMLInputElement) => {
    const value = input.valueAsNumber
    if (input.checkValidity() && Number.isFinite(value)) {
      onValueChange(value)
    } else {
      setDraft(appliedDraft)
    }
  }

  const handleKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key === 'Enter') {
      event.preventDefault()
      suppressBlurCommit.current = true
      commitDraft(event.currentTarget)
      event.currentTarget.blur()
    }
    if (event.key === 'Escape') {
      event.preventDefault()
      suppressBlurCommit.current = true
      setDraft(appliedDraft)
      event.currentTarget.blur()
    }
  }

  const handleBlur = (event: ChangeEvent<HTMLInputElement>) => {
    setEditing(false)
    if (suppressBlurCommit.current) {
      suppressBlurCommit.current = false
      return
    }
    commitDraft(event.currentTarget)
  }

  return (
    <label
      className={`numeric-control${className ? ` ${className}` : ''}`}
      htmlFor={inputId}
    >
      <span className="numeric-control__label">{label}</span>
      <input
        id={inputId}
        type="number"
        min={control.min}
        max={control.max}
        step={control.step}
        value={draft}
        disabled={control.applyMode === 'unavailable'}
        inputMode="numeric"
        aria-describedby={unitId}
        onFocus={() => setEditing(true)}
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
