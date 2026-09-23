import type { DesiredControl } from '../../hooks/useDesiredControl'
import type { NumericControlSnapshot } from '../../model/loadgen'
import type { Point } from './geometry'
import { formatInteger } from './formatters'

interface PipelineBatchControlProps {
  readonly control: NumericControlSnapshot
  readonly desiredControl: DesiredControl<number>
  readonly anchor: Point
}

export function PipelineBatchControl({
  control,
  desiredControl,
  anchor,
}: PipelineBatchControlProps) {
  const applied = control.applied
  const snapshotCandidate = control.preview ?? control.pending
  const localActive = desiredControl.phase !== 'idle'
  const shown = localActive
    ? desiredControl.desired
    : snapshotCandidate ?? applied ?? desiredControl.desired ?? control.min
  const pending = desiredControl.phase === 'pending' || control.pending !== null
  const preview = desiredControl.phase === 'preview' ||
    (control.preview !== null && !pending)
  const unavailable = control.applyMode !== 'immediate' ||
    applied === null ||
    !desiredControl.available
  const busy = pending || preview
  const status = pending
    ? 'Pending'
    : preview
      ? 'Desired'
      : applied === null
        ? 'Unavailable'
        : 'Applied'
  const statusText = `${status} ${formatInteger(shown)} tx${busy && applied !== null ? `; Applied ${formatInteger(applied)} tx` : ''}`

  const step = (direction: -1 | 1) => {
    if (unavailable || busy || applied === null) return
    const next = Math.min(
      control.max,
      Math.max(control.min, applied + direction * control.step),
    )
    if (next !== applied) void desiredControl.commit(next)
  }

  return (
    <g
      className={`pipeline-batch-stepper${unavailable ? ' pipeline-batch-stepper--unavailable' : ''}${busy ? ' pipeline-batch-stepper--busy' : ''}`}
      role="group"
      aria-label={`Batch: ${statusText}`}
      transform={`translate(${anchor.x} ${anchor.y})`}
    >
      <rect
        className="pipeline-batch-stepper__body"
        x="-86"
        y="-18"
        width="172"
        height="36"
        rx="5"
      />
      <g
        className="pipeline-batch-stepper__button"
        role="button"
        tabIndex={unavailable || busy || shown <= control.min ? -1 : 0}
        aria-label="Decrease Batch"
        aria-disabled={unavailable || busy || shown <= control.min}
        onClick={() => {
          if (shown > control.min) step(-1)
        }}
        onKeyDown={(event) => {
          if (event.key !== 'Enter' && event.key !== ' ') return
          event.preventDefault()
          if (shown > control.min) step(-1)
        }}
      >
        <rect x="-86" y="-18" width="34" height="36" rx="5" />
        <text x="-69" y="5" textAnchor="middle" aria-hidden="true">−</text>
      </g>
      <text className="pipeline-batch-stepper__value" y="5" textAnchor="middle">
        Batch {formatInteger(shown)} tx
      </text>
      <g
        className="pipeline-batch-stepper__button"
        role="button"
        tabIndex={unavailable || busy || shown >= control.max ? -1 : 0}
        aria-label="Increase Batch"
        aria-disabled={unavailable || busy || shown >= control.max}
        onClick={() => {
          if (shown < control.max) step(1)
        }}
        onKeyDown={(event) => {
          if (event.key !== 'Enter' && event.key !== ' ') return
          event.preventDefault()
          if (shown < control.max) step(1)
        }}
      >
        <rect x="52" y="-18" width="34" height="36" rx="5" />
        <text x="69" y="5" textAnchor="middle" aria-hidden="true">+</text>
      </g>
    </g>
  )
}
