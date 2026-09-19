import { useEffect, useRef, useState } from 'react'
import type {
  CSSProperties,
  KeyboardEvent as ReactKeyboardEvent,
  PointerEvent as ReactPointerEvent,
} from 'react'
import type {
  ChannelId,
  ChannelSnapshot,
  SelectableId,
} from '../../model/loadgen'
import { channelPressureColor } from '../../model/channelFlowState'
import {
  formatInteger,
  formatMilliseconds,
  formatRate,
} from './formatters'
import type { PipelineChannelGeometry, Point } from './geometry'
import type { PipelineOrientation } from './pipelineLayout'
import {
  capacityFromKeyboard,
  capacityFromVerticalDrag,
  capacityToCableY,
  type CapacityValues,
  getCapacityTicks,
  getChannelCableGeometryPresentation,
  PORTRAIT_CHANNEL_CABLE_MAX_LIFT,
  CHANNEL_CABLE_MAX_LIFT,
} from './channelCableGeometry'

interface ChannelCableProps {
  snapshot: ChannelSnapshot
  geometry?: PipelineChannelGeometry
  start?: Point
  end?: Point
  selected: boolean
  onSelect: (id: SelectableId) => void
  onCapacityChange: (channel: ChannelId, value: number) => void
  orientation?: PipelineOrientation
  capacityValues?: CapacityValues
}

interface DragSession {
  readonly pointerId: number
  readonly pointerAxis: number
  readonly capacity: number
  readonly control: ChannelSnapshot['capacity']
  readonly handle: SVGGElement
  readonly svg: SVGSVGElement
  readonly channel: ChannelId
  readonly commit: (channel: ChannelId, value: number) => void
  readonly orientation: PipelineOrientation
}

interface DragListeners {
  readonly pointerMove: (event: globalThis.PointerEvent) => void
  readonly pointerUp: (event: globalThis.PointerEvent) => void
  readonly pointerCancel: (event: globalThis.PointerEvent) => void
  readonly keyDown: (event: globalThis.KeyboardEvent) => void
}

// oxlint-disable-next-line react/only-export-components -- Directly tested UI presentation.
export function getChannelCablePresentation(
  snapshot: ChannelSnapshot,
  start: Point,
  end: Point,
  dragPreview: number | null = null,
  orientation: PipelineOrientation = 'landscape',
  capacityValues?: CapacityValues,
) {
  const maxLift = orientation === 'portrait'
    ? PORTRAIT_CHANNEL_CABLE_MAX_LIFT
    : CHANNEL_CABLE_MAX_LIFT
  const geometry = getChannelCableGeometryPresentation(
    snapshot.capacity,
    start,
    end,
    dragPreview,
    orientation,
    maxLift,
    capacityValues,
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
              maxLift,
              capacityValues,
            ),
            x: capacityToCableY(
              capacity.applied,
              snapshot.capacity,
              start.x,
              maxLift,
              capacityValues,
            ),
          }
        : null,
    depth: `Depth ${formatInteger(snapshot.depthBatches)} / ${formatInteger(capacity.applied)} batches`,
    waitingUpstream,
    waitingUpstreamY: 548,
    capacityStatusY: waitingUpstream === null ? 548 : 566,
  }
}

function pointerInSvg(
  svg: SVGSVGElement,
  clientX: number,
  clientY: number,
): Point {
  const matrix = svg.getScreenCTM()
  if (matrix !== null) {
    const point = svg.createSVGPoint()
    point.x = clientX
    point.y = clientY
    return point.matrixTransform(matrix.inverse())
  }

  const bounds = svg.getBoundingClientRect()
  const viewBox = svg.viewBox.baseVal
  return {
    x: viewBox.x + ((clientX - bounds.left) / bounds.width) * viewBox.width,
    y: viewBox.y + ((clientY - bounds.top) / bounds.height) * viewBox.height,
  }
}

function removeDragListeners(listeners: DragListeners) {
  window.removeEventListener('pointermove', listeners.pointerMove, true)
  window.removeEventListener('pointerup', listeners.pointerUp, true)
  window.removeEventListener('pointercancel', listeners.pointerCancel, true)
  window.removeEventListener('keydown', listeners.keyDown, true)
}

export function ChannelCable({
  snapshot,
  geometry,
  start: startProp,
  end: endProp,
  selected,
  onSelect,
  onCapacityChange,
  orientation = 'landscape',
  capacityValues,
}: ChannelCableProps) {
  const start = geometry?.start ?? startProp ?? { x: 0, y: 0 }
  const end = geometry?.end ?? endProp ?? start
  const metrics = geometry?.metrics ?? {
    x: (start.x + end.x) / 2,
    throughputY: orientation === 'portrait'
      ? snapshot.id === 'reader-to-throttler' ? 328 : 750
      : 507,
    depthY: orientation === 'portrait'
      ? snapshot.id === 'reader-to-throttler' ? 349 : 771
      : 528,
    waitingY: orientation === 'portrait'
      ? snapshot.id === 'reader-to-throttler' ? 370 : 792
      : 548,
    requestY: orientation === 'portrait'
      ? snapshot.id === 'reader-to-throttler' ? 388 : 810
      : 566,
  }
  const [dragPreview, setDragPreview] = useState<number | null>(null)
  const dragSession = useRef<DragSession | null>(null)
  const dragListeners = useRef<DragListeners | null>(null)
  const control = snapshot.capacity
  const presentation = getChannelCablePresentation(
    snapshot,
    start,
    end,
    dragPreview,
    orientation,
    capacityValues,
  )
  const { capacity } = presentation
  const centerX = (start.x + end.x) / 2
  const portrait = orientation === 'portrait'
  const maxLift = portrait
    ? PORTRAIT_CHANNEL_CABLE_MAX_LIFT
    : CHANNEL_CABLE_MAX_LIFT
  const ticks = getCapacityTicks(
    control,
    portrait ? start.x : start.y,
    maxLift,
    capacityValues,
  )
  const centerY = (start.y + end.y) / 2
  const disabled = control.applyMode === 'unavailable'
  const flowActive = snapshot.id === 'reader-to-throttler' && (
    snapshot.inputTransactionsPerSecond > 0 ||
    snapshot.outputTransactionsPerSecond > 0
  )
  const channelStyle = {
    '--pipeline-channel-pressure-color': channelPressureColor(
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
      (session.orientation === 'portrait'
        ? pointerInSvg(session.svg, clientX, clientY).x
        : pointerInSvg(session.svg, clientX, clientY).y) - session.pointerAxis,
      session.control,
      maxLift,
      capacityValues,
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
      pointerAxis: portrait
        ? pointerInSvg(svg, event.clientX, event.clientY).x
        : pointerInSvg(svg, event.clientX, event.clientY).y,
      capacity: presentation.dragStartCapacity,
      control,
      handle: event.currentTarget,
      svg,
      channel: snapshot.id,
      commit: onCapacityChange,
      orientation,
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
          closedSession.commit(closedSession.channel, nextCapacity)
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
      capacityValues,
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
      id={`channel-${snapshot.id}`}
      className={`pipeline-channel pipeline-selectable pipeline-channel--${snapshot.flowState}${flowActive ? ' pipeline-channel--flow-active' : ''}${selected ? ' pipeline-selectable--selected' : ''}`}
      role="button"
      tabIndex={0}
      aria-label={`Inspect ${snapshot.from} to ${snapshot.to} channel`}
      aria-pressed={selected}
      data-pressure={snapshot.displayedPressure.toFixed(2)}
      data-input-active={snapshot.inputTransactionsPerSecond > 0}
      data-output-active={snapshot.outputTransactionsPerSecond > 0}
      style={channelStyle}
      onClick={() => onSelect(snapshot.id)}
      onKeyDown={handleSelectionKeyDown}
    >
      <g className="pipeline-channel-scale" aria-hidden="true">
        <line
          x1={portrait ? start.x - maxLift : centerX}
          y1={portrait ? centerY : start.y - maxLift}
          x2={portrait ? start.x : centerX}
          y2={portrait ? centerY : start.y}
          className="pipeline-channel-scale__line"
        />
        {ticks.map((tick) => (
          <g key={tick.value}>
            <line
              x1={portrait ? tick.y : centerX - (tick.major ? 5 : 3)}
              y1={portrait ? centerY - (tick.major ? 5 : 3) : tick.y}
              x2={portrait ? tick.y : centerX + (tick.major ? 5 : 3)}
              y2={portrait ? centerY + (tick.major ? 5 : 3) : tick.y}
              className="pipeline-channel-scale__tick"
            />
            {tick.major && (
              <text
                x={portrait ? tick.y : centerX + 10}
                y={portrait ? centerY + 18 : tick.y + 4}
                textAnchor={portrait ? 'middle' : undefined}
                className="pipeline-channel-scale__label"
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
        className="pipeline-channel-cable"
      />
      {presentation.requestedPath !== null && (
        <path
          d={presentation.requestedPath}
          className={`pipeline-channel-requested-cable pipeline-channel-requested-cable--${capacity.requestState}`}
          aria-hidden="true"
        />
      )}

      {presentation.appliedMarker !== null && (
        <g
          className={`pipeline-channel-capacity-applied pipeline-channel-capacity-applied--${snapshot.flowState}`}
          transform={portrait
            ? `translate(${presentation.appliedMarker.x} ${centerY - 28})`
            : `translate(${centerX - 50} ${presentation.appliedMarker.y})`}
          role="status"
          aria-label={`${snapshot.from} to ${snapshot.to} channel applied capacity ${formatInteger(presentation.appliedMarker.capacity)} batches`}
          data-capacity={presentation.appliedMarker.capacity}
        >
          <line x1={portrait ? 0 : 30} y1={portrait ? 9 : 0} x2={portrait ? 0 : 50} y2={portrait ? 28 : 0} />
          <rect x="-30" y="-9" width="60" height="18" rx="3" />
          <text y="3" textAnchor="middle">
            Applied {formatInteger(presentation.appliedMarker.capacity)}
          </text>
        </g>
      )}

      <text
        x={metrics.x}
        y={metrics.throughputY}
        textAnchor="middle"
        className="pipeline-small-strong pipeline-channel-metric"
      >
        {formatRate(snapshot.throughputTps)}
      </text>
      <text
        x={metrics.x}
        y={metrics.depthY}
        textAnchor="middle"
        className="pipeline-small pipeline-channel-metric"
      >
        {presentation.depth}
      </text>
      {presentation.waitingUpstream !== null && (
        <text
          x={metrics.x}
          y={metrics.waitingY}
          textAnchor="middle"
          className="pipeline-channel-wait-status"
        >
          {presentation.waitingUpstream}
        </text>
      )}
      {capacity.requestState !== null && (
        <text
          x={metrics.x}
          y={metrics.requestY}
          textAnchor="middle"
          className={`pipeline-channel-capacity-status pipeline-channel-capacity-status--${capacity.requestState}`}
        >
          {capacity.requestState === 'pending' ? 'Pending' : 'Preview'}{' '}
          {formatInteger(capacity.candidate)} batches
        </text>
      )}

      <g
        className={`pipeline-channel-handle pipeline-channel-handle--${snapshot.flowState}${presentation.handleState === null ? '' : ` pipeline-channel-handle--${presentation.handleState}`}${disabled ? ' pipeline-channel-handle--disabled' : ''}`}
        role="slider"
        aria-orientation={portrait ? 'horizontal' : 'vertical'}
        tabIndex={disabled ? -1 : 0}
        aria-label={`${snapshot.from} to ${snapshot.to} channel capacity`}
        aria-valuemin={control.min}
        aria-valuemax={control.max}
        aria-valuenow={capacity.candidate}
        aria-valuetext={
          capacity.requestState === null
            ? `${formatInteger(capacity.applied)} batches applied`
            : `${capacity.requestState === 'pending' ? 'Pending' : 'Preview'} ${formatInteger(capacity.candidate)} batches; ${formatInteger(capacity.applied)} batches applied`
        }
        aria-disabled={disabled}
        transform={`translate(${presentation.sliderX} ${presentation.sliderY})`}
        onPointerDown={handlePointerDown}
        onLostPointerCapture={(event) => cancelDrag(event.pointerId)}
        onKeyDown={handleKeyDown}
      >
        <rect
          className="pipeline-channel-handle__body"
          x="-18"
          y="-12"
          width="36"
          height="24"
          rx="4"
        />
        {capacity.requestState === 'preview' && (
          <rect
            className="pipeline-channel-handle__request-ring"
            x="-21"
            y="-15"
            width="42"
            height="30"
            rx="6"
          />
        )}
        <rect
          className="pipeline-channel-handle__focus-ring"
          x="-24"
          y="-18"
          width="48"
          height="36"
          rx="8"
        />
        <text
          y="4"
          textAnchor="middle"
          className="pipeline-channel-handle__value"
        >
          {formatInteger(presentation.handleCapacity)}
        </text>
      </g>
    </g>
  )
}
