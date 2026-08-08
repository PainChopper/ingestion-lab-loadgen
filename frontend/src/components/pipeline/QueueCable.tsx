import { useRef, useState } from 'react'
import type {
  CSSProperties,
  KeyboardEvent,
  PointerEvent,
} from 'react'
import type {
  QueueId,
  QueueSnapshot,
  SelectableId,
} from '../../model/loadgen'
import { formatInteger, formatRate } from './formatters'
import type { Point } from './geometry'
import type { QueueMarkerSlotSnapshot } from './markerLifecycle'
import {
  buildQueueCablePath,
  capacityFromVerticalDrag,
  capacityToCableY,
  getCapacityTicks,
  getQueueCapacityPresentation,
  normalizeCapacity,
  QUEUE_CABLE_MAX_LIFT,
} from './queueCableGeometry'

interface QueueCableProps {
  snapshot: QueueSnapshot
  start: Point
  end: Point
  selected: boolean
  markers: readonly QueueMarkerSlotSnapshot[]
  onSelect: (id: SelectableId) => void
  onCapacityChange: (queue: QueueId, value: number) => void
}

interface DragSession {
  readonly pointerId: number
  readonly pointerY: number
  readonly capacity: number
}

function pointerYInSvg(event: PointerEvent<SVGGElement>): number {
  const svg = event.currentTarget.ownerSVGElement
  if (svg === null) return event.clientY

  const matrix = svg.getScreenCTM()
  if (matrix !== null) {
    const point = svg.createSVGPoint()
    point.x = event.clientX
    point.y = event.clientY
    return point.matrixTransform(matrix.inverse()).y
  }

  const bounds = svg.getBoundingClientRect()
  const viewBox = svg.viewBox.baseVal
  return viewBox.y + ((event.clientY - bounds.top) / bounds.height) * viewBox.height
}

export function QueueCable({
  snapshot,
  start,
  end,
  selected,
  markers,
  onSelect,
  onCapacityChange,
}: QueueCableProps) {
  const [dragPreview, setDragPreview] = useState<number | null>(null)
  const dragSession = useRef<DragSession | null>(null)
  const control = snapshot.capacity
  const capacity = getQueueCapacityPresentation(control, dragPreview)
  const appliedTopY = capacityToCableY(
    capacity.applied,
    control,
    start.y,
    QUEUE_CABLE_MAX_LIFT,
  )
  const candidateTopY = capacityToCableY(
    capacity.candidate,
    control,
    start.y,
    QUEUE_CABLE_MAX_LIFT,
  )
  const path = buildQueueCablePath(start, end, appliedTopY)
  const centerX = (start.x + end.x) / 2
  const ticks = getCapacityTicks(control, start.y, QUEUE_CABLE_MAX_LIFT)
  const disabled = control.applyMode === 'unavailable'
  const depth = formatInteger(snapshot.depthBatches)

  const capacityFromDrag = (
    event: PointerEvent<SVGGElement>,
    session: DragSession,
  ) =>
    capacityFromVerticalDrag(
      session.capacity,
      pointerYInSvg(event) - session.pointerY,
      control,
      QUEUE_CABLE_MAX_LIFT,
    )

  const handlePointerDown = (event: PointerEvent<SVGGElement>) => {
    if (disabled || event.button !== 0 || dragSession.current !== null) return

    dragSession.current = {
      pointerId: event.pointerId,
      pointerY: pointerYInSvg(event),
      capacity: capacity.candidate,
    }
    event.currentTarget.setPointerCapture(event.pointerId)
  }

  const handlePointerMove = (event: PointerEvent<SVGGElement>) => {
    const session = dragSession.current
    if (session?.pointerId !== event.pointerId) return
    setDragPreview(capacityFromDrag(event, session))
  }

  const handlePointerUp = (event: PointerEvent<SVGGElement>) => {
    const session = dragSession.current
    if (session?.pointerId !== event.pointerId) return

    const nextCapacity = capacityFromDrag(event, session)
    dragSession.current = null
    setDragPreview(null)
    if (event.currentTarget.hasPointerCapture(event.pointerId)) {
      event.currentTarget.releasePointerCapture(event.pointerId)
    }
    if (nextCapacity !== session.capacity) {
      onCapacityChange(snapshot.id, nextCapacity)
    }
  }

  const handlePointerCancel = (event: PointerEvent<SVGGElement>) => {
    if (dragSession.current?.pointerId !== event.pointerId) return
    dragSession.current = null
    setDragPreview(null)
  }

  const handleKeyDown = (event: KeyboardEvent<SVGGElement>) => {
    if (disabled) return

    let nextCapacity: number | null = null
    switch (event.key) {
      case 'ArrowUp':
      case 'ArrowRight':
        nextCapacity = capacity.candidate + control.step
        break
      case 'ArrowDown':
      case 'ArrowLeft':
        nextCapacity = capacity.candidate - control.step
        break
      case 'PageUp':
        nextCapacity = capacity.candidate + control.step * 5
        break
      case 'PageDown':
        nextCapacity = capacity.candidate - control.step * 5
        break
      case 'Home':
        nextCapacity = control.min
        break
      case 'End':
        nextCapacity = control.max
        break
      default:
        return
    }

    event.preventDefault()
    const normalized = normalizeCapacity(nextCapacity, control)
    if (normalized !== capacity.candidate) {
      onCapacityChange(snapshot.id, normalized)
    }
  }

  const handleSelectionKeyDown = (event: KeyboardEvent<SVGGElement>) => {
    if (event.key !== 'Enter' && event.key !== ' ') return
    event.preventDefault()
    onSelect(snapshot.id)
  }

  return (
    <g
      id={`queue-${snapshot.id}`}
      className={`pipeline-queue pipeline-selectable pipeline-queue--${snapshot.flowState}${selected ? ' pipeline-selectable--selected' : ''}`}
      role="button"
      tabIndex={0}
      aria-label={`Inspect ${snapshot.from} to ${snapshot.to} queue`}
      aria-pressed={selected}
      onClick={() => onSelect(snapshot.id)}
      onKeyDown={handleSelectionKeyDown}
    >
      <g className="pipeline-queue-scale" aria-hidden="true">
        <line
          x1={centerX}
          y1={start.y - QUEUE_CABLE_MAX_LIFT}
          x2={centerX}
          y2={start.y}
          className="pipeline-queue-scale__line"
        />
        {ticks.map((tick) => (
          <g key={tick.value}>
            <line
              x1={centerX - (tick.major ? 5 : 3)}
              y1={tick.y}
              x2={centerX + (tick.major ? 5 : 3)}
              y2={tick.y}
              className="pipeline-queue-scale__tick"
            />
            {tick.major && (
              <text
                x={centerX + 10}
                y={tick.y + 4}
                className="pipeline-queue-scale__label"
              >
                {formatInteger(tick.value)}
              </text>
            )}
          </g>
        ))}
      </g>

      <path d={path} className="pipeline-link-hit-area" aria-hidden="true" />
      <path d={path} className="pipeline-queue-cable" />

      <g className="pipeline-queue-occupancy" aria-hidden="true">
        {markers
          .filter((marker) => marker.kind === 'occupancy')
          .map((marker) => {
            const markerStyle = {
              offsetPath: `path("${path}")`,
              offsetDistance: `${marker.phase * 100}%`,
            } satisfies CSSProperties
            return (
              <circle
                key={marker.slotId}
                r="4.5"
                visibility={marker.state === 'inactive' ? 'hidden' : 'visible'}
                className={`pipeline-marker pipeline-marker--occupancy pipeline-marker--${marker.state}${marker.queued ? ' pipeline-marker--queued' : ''}`}
                style={markerStyle}
                data-marker-id={marker.slotId}
                data-family-id={marker.familyId ?? ''}
                data-marker-kind={marker.kind}
                data-marker-state={marker.state}
                data-marker-phase={marker.phase.toFixed(4)}
              />
            )
          })}
      </g>

      <g
        className={`pipeline-queue-handle${capacity.requestState === null ? '' : ` pipeline-queue-handle--${capacity.requestState}`}${disabled ? ' pipeline-queue-handle--disabled' : ''}`}
        role="slider"
        aria-orientation="vertical"
        tabIndex={disabled ? -1 : 0}
        aria-label={`${snapshot.from} to ${snapshot.to} queue capacity`}
        aria-valuemin={control.min}
        aria-valuemax={control.max}
        aria-valuenow={capacity.candidate}
        aria-valuetext={
          capacity.requestState === null
            ? `${formatInteger(capacity.applied)} batches applied`
            : `${capacity.requestState === 'pending' ? 'Pending' : 'Preview'} ${formatInteger(capacity.candidate)} batches; ${formatInteger(capacity.applied)} batches applied`
        }
        aria-disabled={disabled}
        transform={`translate(${centerX} ${candidateTopY})`}
        onPointerDown={handlePointerDown}
        onPointerMove={handlePointerMove}
        onPointerUp={handlePointerUp}
        onPointerCancel={handlePointerCancel}
        onLostPointerCapture={handlePointerCancel}
        onKeyDown={handleKeyDown}
      >
        <rect x="-18" y="-12" width="36" height="24" rx="4" />
        <text y="4" textAnchor="middle">
          {formatInteger(capacity.candidate)}
        </text>
      </g>

      <text
        x={centerX}
        y="507"
        textAnchor="middle"
        className="pipeline-small-strong pipeline-queue-metric"
      >
        {formatRate(snapshot.throughputTps)}
      </text>
      <text
        x={centerX}
        y="528"
        textAnchor="middle"
        className="pipeline-small pipeline-queue-metric"
      >
        Depth {depth} / {formatInteger(capacity.applied)} batches
      </text>
      {capacity.requestState !== null && (
        <text
          x={centerX}
          y="548"
          textAnchor="middle"
          className={`pipeline-queue-capacity-status pipeline-queue-capacity-status--${capacity.requestState}`}
        >
          {capacity.requestState === 'pending' ? 'Pending' : 'Preview'}{' '}
          {formatInteger(capacity.candidate)} batches
        </text>
      )}
    </g>
  )
}
