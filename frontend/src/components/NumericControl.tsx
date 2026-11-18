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
  const [draft, setDraft] = useState<string | null>(null)
  const suppressBlurCommit = useRef(false)
  const unavailable = control.applyMode === 'unavailable' ||
    desiredControl?.available === false
  const pending = desiredControl?.phase === 'pending'
  const editable = !unavailable && !pending
  const displayed = !unavailable && desiredControl !== undefined && desiredControl.phase !== 'idle'
    ? desiredControl.desired
    : control.applied
  const displayedValue = displayed === null ? '' : String(displayed)
  const wasUnavailable = useRef(unavailable)
  const cancelPreview = desiredControl?.cancel

  if (!editable && draft !== null) setDraft(null)

  useEffect(() => {
    const becameUnavailable = unavailable && !wasUnavailable.current
    wasUnavailable.current = unavailable
    if (becameUnavailable) {
      onPreviewChange?.(null)
    }
  }, [onPreviewChange, unavailable])

  const handleFocus = () => {
    if (!editable) return
    suppressBlurCommit.current = false
  }

  const handleChange = (event: ChangeEvent<HTMLInputElement>) => {
    if (!editable) return
    setDraft(event.currentTarget.value)
    const value = event.currentTarget.valueAsNumber
    const valid = event.currentTarget.checkValidity() && Number.isFinite(value)
    onPreviewChange?.(valid ? value : null)
    if (valid) desiredControl?.preview(value)
  }

  const commitDraft = (input: HTMLInputElement) => {
    if (!editable) return

    const value = input.valueAsNumber
    if (input.checkValidity() && Number.isFinite(value)) {
      if (desiredControl === undefined) onValueChange?.(value)
      else void desiredControl.commit(value)
    } else {
      onPreviewChange?.(null)
      cancelPreview?.()
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
      onPreviewChange?.(null)
      cancelPreview?.()
      event.currentTarget.blur()
    }
  }

  const handleBlur = (event: ChangeEvent<HTMLInputElement>) => {
    setDraft(null)
    if (suppressBlurCommit.current) {
      suppressBlurCommit.current = false
      return
    }
    if (draft !== null) commitDraft(event.currentTarget)
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
        value={editable ? draft ?? displayedValue : displayedValue}
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
