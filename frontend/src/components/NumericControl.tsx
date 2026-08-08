import { useEffect, useId, useState } from 'react'
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
  const [draft, setDraft] = useState(
    control.applied === null ? '' : String(control.applied),
  )

  useEffect(() => {
    if (!editing) {
      setDraft(control.applied === null ? '' : String(control.applied))
    }
  }, [control.applied, editing])

  const handleChange = (event: ChangeEvent<HTMLInputElement>) => {
    setDraft(event.currentTarget.value)
    const value = event.currentTarget.valueAsNumber
    if (Number.isFinite(value)) onValueChange(value)
  }

  const handleKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key === 'Enter') event.currentTarget.blur()
    if (event.key === 'Escape') {
      setDraft(control.applied === null ? '' : String(control.applied))
      event.currentTarget.blur()
    }
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
        onBlur={() => setEditing(false)}
        onChange={handleChange}
        onKeyDown={handleKeyDown}
      />
      <span id={unitId} className="numeric-control__unit">
        {control.unit}
      </span>
    </label>
  )
}
