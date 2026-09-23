import { useEffect, useId, useRef, useState } from 'react'
import type { ChangeEvent, KeyboardEvent } from 'react'
import type { NumericControlSnapshot } from '../model/loadgen'
import type { DesiredControl } from '../hooks/useDesiredControl'

interface NumericControlProps {
  readonly label: string
  readonly control: NumericControlSnapshot
  readonly onValueChange?: (value: number) => void
  readonly onPreviewChange?: (value: number | null) => void
  readonly desiredControl?: DesiredControl<number>
  readonly className?: string
}

export function NumericControl({
  label,
  control,
  onValueChange,
  onPreviewChange,
  desiredControl,
  className,
}: NumericControlProps) {
  const inputId = useId()
  const unitId = `${inputId}-unit`
  const [editing, setEditing] = useState(false)
  const suppressBlurCommit = useRef(false)
  const displayed = desiredControl?.desired ?? control.applied
  const [draft, setDraft] = useState(displayed === null ? '' : String(displayed))
  const appliedDraft = displayed === null ? '' : String(displayed)
  const unavailable = control.applyMode === 'unavailable' ||
    desiredControl?.available === false
  const pending = desiredControl?.phase === 'pending'
  const wasUnavailable = useRef(unavailable)

  useEffect(() => {
    if (unavailable) {
      const becameUnavailable = !wasUnavailable.current
      wasUnavailable.current = true
      setEditing(false)
      setDraft(appliedDraft)
      if (becameUnavailable) onPreviewChange?.(null)
      return
    }

    wasUnavailable.current = false
    if (!editing) {
      setDraft(appliedDraft)
    }
  }, [appliedDraft, editing, onPreviewChange, unavailable])

  const handleChange = (event: ChangeEvent<HTMLInputElement>) => {
    setDraft(event.currentTarget.value)
    const value = event.currentTarget.valueAsNumber
    onPreviewChange?.(
      event.currentTarget.checkValidity() && Number.isFinite(value)
        ? value
        : null,
    )
    if (event.currentTarget.checkValidity() && Number.isFinite(value)) {
      desiredControl?.preview(value)
    }
  }

  const commitDraft = (input: HTMLInputElement) => {
    if (unavailable) {
      setDraft(appliedDraft)
      return
    }

    const value = input.valueAsNumber
    if (input.checkValidity() && Number.isFinite(value)) {
      if (desiredControl === undefined) onValueChange?.(value)
      else void desiredControl.commit(value)
    } else {
      setDraft(appliedDraft)
      onPreviewChange?.(null)
      desiredControl?.cancel()
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
      onPreviewChange?.(null)
      desiredControl?.cancel()
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
        disabled={unavailable || pending}
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
