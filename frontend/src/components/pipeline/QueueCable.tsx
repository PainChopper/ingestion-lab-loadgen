import { useEffect, useRef, useState } from 'react'
import type {
  CSSProperties,
  KeyboardEvent as ReactKeyboardEvent,
  PointerEvent as ReactPointerEvent,
} from 'react'
import type {
  QueueId,
  QueueSnapshot,
  SelectableId,
} from '../../model/loadgen'
import { queuePressureColor } from '../../model/queueFlowState'
import {
  formatInteger,
  formatMilliseconds,
  formatRate,
} from './formatters'
import type { Point } from './geometry'
import type { QueueMarkerSlotSnapshot } from './markerLifecycle'
import {
  capacityFromKeyboard,
  capacityFromVerticalDrag,
  capacityToCableY,
  getCapacityTicks,
  getQueueCableGeometryPresentation,
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
  readonly control: QueueSnapshot['capacity']
  readonly handle: SVGGElement
  readonly svg: SVGSVGElement
  readonly queue: QueueId
  readonly commit: (queue: QueueId, value: number) => void
}

interface DragListeners {
  readonly pointerMove: (event: globalThis.PointerEvent) => void
  readonly pointerUp: (event: globalThis.PointerEvent) => void
  readonly pointerCancel: (event: globalThis.PointerEvent) => void
  readonly keyDown: (event: globalThis.KeyboardEvent) => void
}

// oxlint-disable-next-line react/only-export-components -- Directly tested UI presentation.
export function getQueueCablePresentation(
  snapshot: QueueSnapshot,
  start: Point,
  end: Point,
  dragPreview: number | null = null,
) {
  const geometry = getQueueCableGeometryPresentation(
    snapshot.capacity,
    start,
    end,
    dragPreview,
  )
  const { capacity } = geometry
  const waitingUpstream =
    snapshot.blockedSenders > 0
      ? `Waiting upstream ${formatInteger(snapshot.blockedSenders)}, oldest ${formatMilliseconds(snapshot.oldestBlockedSenderMs)}`
      : null

  return {
    ...geometry,
    capacity,
    handleCapacity: capacity.candidate,
    handleState: capacity.requestState,
    dragStartCapacity: capacity.candidate,
    appliedMarker:
      capacity.requestState !== null
        ? {
            capacity: capacity.applied,
            y: capacityToCableY(
              capacity.applied,
              snapshot.capacity,
              start.y,
              QUEUE_CABLE_MAX_LIFT,
            ),
          }
        : null,
    depth: `Depth ${formatInteger(snapshot.depthBatches)} / ${formatInteger(capacity.applied)} batches`,
    waitingUpstream,
    waitingUpstreamY: 548,
    capacityStatusY: waitingUpstream === null ? 548 : 566,
  }
}

function pointerYInSvg(
  svg: SVGSVGElement,
  clientX: number,
  clientY: number,
): number {
  const matrix = svg.getScreenCTM()
  if (matrix !== null) {
    const point = svg.createSVGPoint()
    point.x = clientX
    point.y = clientY
    return point.matrixTransform(matrix.inverse()).y
  }

  const bounds = svg.getBoundingClientRect()
  const viewBox = svg.viewBox.baseVal
  return viewBox.y + ((clientY - bounds.top) / bounds.height) * viewBox.height
}

function removeDragListeners(listeners: DragListeners) {
  window.removeEventListener('pointermove', listeners.pointerMove, true)
  window.removeEventListener('pointerup', listeners.pointerUp, true)
  window.removeEventListener('pointercancel', listeners.pointerCancel, true)
  window.removeEventListener('keydown', listeners.keyDown, true)
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
  const dragListeners = useRef<DragListeners | null>(null)
  const control = snapshot.capacity
  const presentation = getQueueCablePresentation(
    snapshot,
    start,
    end,
    dragPreview,
  )
  const { capacity } = presentation
  const centerX = (start.x + end.x) / 2
  const ticks = getCapacityTicks(control, start.y, QUEUE_CABLE_MAX_LIFT)
  const disabled = control.applyMode === 'unavailable'
  const queueStyle = {
    '--pipeline-queue-pressure-color': queuePressureColor(
      snapshot.displayedPressure,
    ),
  } as CSSProperties

  const capacityFromDrag = (
    clientX: number,
    clientY: number,
    session: DragSession,
  ) =>
    capacityFromVerticalDrag(
      session.capacity,
      pointerYInSvg(session.svg, clientX, clientY) - session.pointerY,
      session.control,
      QUEUE_CABLE_MAX_LIFT,
    )

  const closeDragSession = (pointerId?: number): DragSession | null => {
    const session = dragSession.current
    if (
      session === null ||
      (pointerId !== undefined && session.pointerId !== pointerId)
    ) {
      return null
    }

    dragSession.current = null
    const listeners = dragListeners.current
    dragListeners.current = null
    if (listeners !== null) removeDragListeners(listeners)
    setDragPreview(null)

    if (session.handle.hasPointerCapture(session.pointerId)) {
      session.handle.releasePointerCapture(session.pointerId)
    }

    return session
  }

  const cancelDrag = (pointerId?: number) => {
    closeDragSession(pointerId)
  }

  const handlePointerDown = (event: ReactPointerEvent<SVGGElement>) => {
    if (disabled || event.button !== 0 || dragSession.current !== null) return

    const svg = event.currentTarget.ownerSVGElement
    if (svg === null) return

    const session: DragSession = {
      pointerId: event.pointerId,
      pointerY: pointerYInSvg(svg, event.clientX, event.clientY),
      capacity: presentation.dragStartCapacity,
      control,
      handle: event.currentTarget,
      svg,
      queue: snapshot.id,
      commit: onCapacityChange,
    }
    const listeners: DragListeners = {
      pointerMove: (pointerEvent) => {
        if (dragSession.current?.pointerId !== pointerEvent.pointerId) return
        setDragPreview(capacityFromDrag(
          pointerEvent.clientX,
          pointerEvent.clientY,
          session,
        ))
      },
      pointerUp: (pointerEvent) => {
        if (dragSession.current?.pointerId !== pointerEvent.pointerId) return

        const nextCapacity = capacityFromDrag(
          pointerEvent.clientX,
          pointerEvent.clientY,
          session,
        )
        const closedSession = closeDragSession(pointerEvent.pointerId)
        if (
          closedSession !== null &&
          nextCapacity !== closedSession.capacity
        ) {
          closedSession.commit(closedSession.queue, nextCapacity)
        }
      },
      pointerCancel: (pointerEvent) => cancelDrag(pointerEvent.pointerId),
      keyDown: (keyboardEvent) => {
        if (keyboardEvent.key !== 'Escape') return
        keyboardEvent.preventDefault()
        cancelDrag()
      },
    }

    dragSession.current = session
    dragListeners.current = listeners
    event.currentTarget.setPointerCapture(event.pointerId)
    window.addEventListener('pointermove', listeners.pointerMove, true)
    window.addEventListener('pointerup', listeners.pointerUp, true)
    window.addEventListener('pointercancel', listeners.pointerCancel, true)
    window.addEventListener('keydown', listeners.keyDown, true)
  }

  useEffect(() => () => {
    const listeners = dragListeners.current
    dragListeners.current = null
    if (listeners !== null) removeDragListeners(listeners)

    const session = dragSession.current
    dragSession.current = null
    if (session?.handle.hasPointerCapture(session.pointerId)) {
      session.handle.releasePointerCapture(session.pointerId)
    }
  }, [])

  const handleKeyDown = (event: ReactKeyboardEvent<SVGGElement>) => {
    if (disabled) return

    const nextCapacity = capacityFromKeyboard(
      event.key,
      capacity.candidate,
      control,
    )
    if (nextCapacity === null) return

    event.preventDefault()
    if (nextCapacity !== capacity.candidate) {
      onCapacityChange(snapshot.id, nextCapacity)
    }
  }

  const handleSelectionKeyDown = (event: ReactKeyboardEvent<SVGGElement>) => {
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
      data-pressure={snapshot.displayedPressure.toFixed(2)}
      style={queueStyle}
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

      <path
        d={presentation.cablePath}
        className="pipeline-link-hit-area"
        aria-hidden="true"
      />
      <path
        d={presentation.cablePath}
        className="pipeline-queue-cable"
      />
      {presentation.requestedPath !== null && (
        <path
          d={presentation.requestedPath}
          className={`pipeline-queue-requested-cable pipeline-queue-requested-cable--${capacity.requestState}`}
          aria-hidden="true"
        />
      )}

      <g
        className="pipeline-queue-markers"
        aria-hidden="true"
        data-marker-path={presentation.markerPath}
      >
        {markers.map((marker) => {
          const markerStyle = {
            offsetPath: `path("${presentation.markerPath}")`,
            offsetDistance: `${marker.phase * 100}%`,
          } satisfies CSSProperties
          return (
            <circle
              key={marker.slotId}
              r={marker.kind === 'occupancy' ? 4.5 : 3.5}
              visibility={marker.state === 'inactive' ? 'hidden' : 'visible'}
              className={`pipeline-marker pipeline-marker--${marker.kind} pipeline-marker--${marker.state}${marker.queued ? ' pipeline-marker--queued' : ''}`}
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

      {presentation.appliedMarker !== null && (
        <g
          className={`pipeline-queue-capacity-applied pipeline-queue-capacity-applied--${snapshot.flowState}`}
          transform={`translate(${centerX - 50} ${presentation.appliedMarker.y})`}
          role="status"
          aria-label={`${snapshot.from} to ${snapshot.to} queue applied capacity ${formatInteger(presentation.appliedMarker.capacity)} batches`}
          data-capacity={presentation.appliedMarker.capacity}
        >
          <line x1="30" x2="50" />
          <rect x="-30" y="-9" width="60" height="18" rx="3" />
          <text y="3" textAnchor="middle">
            Applied {formatInteger(presentation.appliedMarker.capacity)}
          </text>
        </g>
      )}

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
        {presentation.depth}
      </text>
      {presentation.waitingUpstream !== null && (
        <text
          x={centerX}
          y={presentation.waitingUpstreamY}
          textAnchor="middle"
          className="pipeline-queue-wait-status"
        >
          {presentation.waitingUpstream}
        </text>
      )}
      {capacity.requestState !== null && (
        <text
          x={centerX}
          y={presentation.capacityStatusY}
          textAnchor="middle"
          className={`pipeline-queue-capacity-status pipeline-queue-capacity-status--${capacity.requestState}`}
        >
          {capacity.requestState === 'pending' ? 'Pending' : 'Preview'}{' '}
          {formatInteger(capacity.candidate)} batches
        </text>
      )}

      <g
        className={`pipeline-queue-handle pipeline-queue-handle--${snapshot.flowState}${presentation.handleState === null ? '' : ` pipeline-queue-handle--${presentation.handleState}`}${disabled ? ' pipeline-queue-handle--disabled' : ''}`}
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
        transform={`translate(${centerX} ${presentation.sliderY})`}
        onPointerDown={handlePointerDown}
        onLostPointerCapture={(event) => cancelDrag(event.pointerId)}
        onKeyDown={handleKeyDown}
      >
        <rect
          className="pipeline-queue-handle__body"
          x="-18"
          y="-12"
          width="36"
          height="24"
          rx="4"
        />
        {capacity.requestState === 'preview' && (
          <rect
            className="pipeline-queue-handle__request-ring"
            x="-21"
            y="-15"
            width="42"
            height="30"
            rx="6"
          />
        )}
        <rect
          className="pipeline-queue-handle__focus-ring"
          x="-24"
          y="-18"
          width="48"
          height="36"
          rx="8"
        />
        <text
          y="4"
          textAnchor="middle"
          className="pipeline-queue-handle__value"
        >
          {formatInteger(presentation.handleCapacity)}
        </text>
      </g>
    </g>
  )
}
