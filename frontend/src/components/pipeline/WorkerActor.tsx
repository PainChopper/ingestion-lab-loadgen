import { Minus, Plus } from 'lucide-react'
import { useCallback, useEffect, useRef, type KeyboardEvent, type PointerEvent as ReactPointerEvent } from 'react'
import type { DesiredControl } from '../../hooks/useDesiredControl'
import type { InspectorRowSegment } from '../inspectorViewModel'
import type {
  NumericControlSnapshot,
  RunState,
  SelectableId,
} from '../../model/loadgen'
import type { Point, TextPlacement, WorkerActorBounds } from './geometry'
import type { PipelineOrientation } from './pipelineLayout'
import {
  getWorkerActorLayout,
  type WorkerChipLayout,
} from './workerActorLayout'

export type WorkerActorId = 'reader' | 'sender'

const MAX_VISIBLE_WORKER_MARKERS = 32

interface WorkerActorProps {
  actor: WorkerActorId
  title: string
  titlePoint: TextPlacement
  titleTone: 'reader' | 'sender'
  bounds: WorkerActorBounds
  controls: {
    readonly x: number
    readonly y: number
    readonly width: number
    readonly height: number
  }
  workers: NumericControlSnapshot
  liveWorkers?: number
  drainingWorkers?: number
  activityCounts: Readonly<Record<'idle' | 'reading' | 'blocked' | 'in-flight' | 'backoff', number>>
  drainingActivityCounts: Readonly<Record<'idle' | 'reading' | 'blocked' | 'in-flight' | 'backoff', number>>
  runState: RunState
  active?: boolean
  inputPort?: Point
  outputPort: Point
  primaryMetric: string
  secondaryMetric?: string
  statusMetric?: string
  statusMetricSegments?: ReadonlyArray<InspectorRowSegment>
  metricPoints: {
    readonly primary: TextPlacement
    readonly secondary: TextPlacement
    readonly status: TextPlacement
  }
  orientation?: PipelineOrientation
  selected: boolean
  onSelect: (id: SelectableId) => void
  desiredControl: DesiredControl<number>
  muted?: boolean
  sourceErrorOperation?: string
}

function WorkerChip({
  x,
  y,
  scale,
  state,
  draining,
}: WorkerChipLayout & {
  state: 'idle' | 'reading' | 'blocked' | 'in-flight' | 'backoff' | 'active' | 'inactive'
  draining: boolean
}) {
  const pinOffsets = [6, 13, 20, 27]
  const chipX = 4
  const chipY = 4

  return (
    <g
      className={`pipeline-worker--${state}${draining ? ' pipeline-worker--draining' : ''}`}
      transform={`translate(${x} ${y}) scale(${scale})`}
    >
      <rect
        x={chipX}
        y={chipY}
        width="31"
        height="31"
        rx="3"
        className="pipeline-worker-chip"
      />
      {pinOffsets.map((offset) => (
        <g key={offset} className="pipeline-worker-chip-pin">
          <line
            x1={chipX + offset}
            y1={chipY - 4}
            x2={chipX + offset}
            y2={chipY}
          />
          <line
            x1={chipX + offset}
            y1={chipY + 31}
            x2={chipX + offset}
            y2={chipY + 35}
          />
          <line
            x1={chipX - 4}
            y1={chipY + offset}
            x2={chipX}
            y2={chipY + offset}
          />
          <line
            x1={chipX + 31}
            y1={chipY + offset}
            x2={chipX + 35}
            y2={chipY + offset}
          />
        </g>
      ))}
      <polyline
        points={`${chipX + 5},${chipY + 17} ${chipX + 10},${chipY + 17} ${chipX + 14},${chipY + 10} ${chipX + 18},${chipY + 23} ${chipX + 22},${chipY + 15} ${chipX + 27},${chipY + 15}`}
        className="pipeline-worker-wave"
      />
      <circle
        cx={chipX + 57}
        cy={chipY + 15.5}
        r="5.5"
        className="pipeline-worker-led"
      />
    </g>
  )
}

export function WorkerActor({
  actor,
  title,
  titlePoint,
  titleTone,
  bounds,
  controls,
  workers,
  liveWorkers,
  drainingWorkers,
  activityCounts,
  drainingActivityCounts,
  runState,
  active,
  inputPort,
  outputPort,
  primaryMetric,
  secondaryMetric,
  statusMetric,
  statusMetricSegments,
  metricPoints,
  orientation = 'landscape',
  selected,
  onSelect,
  desiredControl,
  muted = false,
  sourceErrorOperation,
}: WorkerActorProps) {
  const holdTimer = useRef<number | null>(null)
  const repeatTimer = useRef<number | null>(null)
  const releaseListeners = useRef<(() => void) | null>(null)
  const holdActive = useRef(false)
  const suppressClick = useRef(false)
  const desiredRef = useRef(desiredControl.desired)
  const commitDesired = desiredControl.commit
  const cancelDesired = desiredControl.cancel
  const previewDesired = desiredControl.preview


  const stopHold = useCallback((commit: boolean) => {
    if (holdTimer.current !== null) window.clearTimeout(holdTimer.current)
    if (repeatTimer.current !== null) window.clearInterval(repeatTimer.current)
    holdTimer.current = null
    repeatTimer.current = null
    releaseListeners.current?.()
    releaseListeners.current = null
    if (!holdActive.current) return
    holdActive.current = false
    if (commit) void commitDesired()
    else cancelDesired()
  }, [cancelDesired, commitDesired])

  useEffect(() => () => stopHold(false), [stopHold])


  const desiredWorkers = Math.min(
    Math.round(workers.max),
    Math.max(Math.round(workers.min), Math.round(desiredControl.desired)),
  )
  desiredRef.current = desiredWorkers
  const visibleWorkers = Math.min(liveWorkers ?? 0, MAX_VISIBLE_WORKER_MARKERS)
  const overflowWorkers = Math.max(0, (liveWorkers ?? 0) - visibleWorkers)
  const layout = getWorkerActorLayout(
    actor,
    bounds,
    workers,
    orientation,
    visibleWorkers,
    Math.min(desiredWorkers, MAX_VISIBLE_WORKER_MARKERS),
  )
  const workerMin = Math.round(workers.min)
  const workerMax = Math.round(workers.max)
  const workerStep = Math.max(1, Math.round(workers.step))
  const workerControlDisabled = !desiredControl.available || desiredControl.phase === 'pending'
  const states = actor === 'reader'
    ? ['blocked', 'reading', 'idle'] as const
    : ['backoff', 'in-flight', 'idle'] as const
  const visibleStates = states.flatMap((state) => Array.from(
    { length: activityCounts[state] },
    (_, index) => ({ state, draining: index < drainingActivityCounts[state] }),
  ))
  const chipState = (index: number) => muted
    ? { state: 'inactive' as const, draining: false }
    : visibleStates[index] ?? { state: (active ?? (runState === 'running')) ? 'active' as const : 'inactive' as const, draining: false }
  const actorAriaLabel = actor === 'reader' || liveWorkers === undefined
    ? `Inspect ${actor}`
    : `Inspect ${actor}, ${desiredWorkers} desired, ${liveWorkers} live, ${drainingWorkers ?? 0} draining`
  const handleKeyDown = (event: KeyboardEvent<SVGGElement>) => {
    if (event.key !== 'Enter' && event.key !== ' ') return
    event.preventDefault()
    onSelect(actor)
  }
  const stepWorkers = useCallback((delta: number) => {
    const next = Math.min(
      Math.round(workers.max),
      Math.max(Math.round(workers.min), desiredRef.current + delta),
    )
    if (next === desiredRef.current) return false
    desiredRef.current = next
    previewDesired(next)
    return true
  }, [previewDesired, workers.max, workers.min])
  const handleWorkerPointerDown = (
    event: ReactPointerEvent<HTMLButtonElement>,
    delta: number,
  ) => {
    event.preventDefault()
    if (!desiredControl.available) return
    stopHold(false)
    if (desiredControl.phase === 'pending' || !stepWorkers(delta)) return
    holdActive.current = true
    suppressClick.current = true
    const release = () => stopHold(true)
    const cancel = () => stopHold(false)
    const cancelKey = (keyboardEvent: globalThis.KeyboardEvent) => {
      if (keyboardEvent.key === 'Escape') cancel()
    }
    releaseListeners.current = () => {
      window.removeEventListener('pointerup', release)
      window.removeEventListener('pointercancel', cancel)
      window.removeEventListener('blur', cancel)
      window.removeEventListener('keydown', cancelKey)
    }
    window.addEventListener('pointerup', release)
    window.addEventListener('pointercancel', cancel)
    window.addEventListener('blur', cancel)
    window.addEventListener('keydown', cancelKey)
    event.currentTarget.setPointerCapture?.(event.pointerId)
    holdTimer.current = window.setTimeout(() => {
      repeatTimer.current = window.setInterval(() => {
        if (!stepWorkers(delta)) stopHold(true)
      }, 120)
    }, 420)
  }
  const handleWorkerClick = (delta: number) => {
    if (!desiredControl.available) return
    if (suppressClick.current) {
      suppressClick.current = false
      return
    }
    if (desiredControl.phase === 'pending' || !stepWorkers(delta)) return
    void desiredControl.commit()
  }

  return (
    <>
      <text
        x={titlePoint.x}
        y={titlePoint.y}
        textAnchor={titlePoint.anchor}
        className={`pipeline-title pipeline-title--${titleTone}`}
      >
        {title}
      </text>
      <foreignObject
        x={controls.x}
        y={controls.y}
        width={controls.width}
        height={controls.height}
        className="pipeline-worker-control"
      >
        <div
          className="pipeline-worker-control__body"
          aria-label={`${title.toLocaleLowerCase()} workers`}
        >
          <button
            id={`${actor}-minus`}
            type="button"
            onPointerDown={(event) => handleWorkerPointerDown(event, -workerStep)}
            onLostPointerCapture={() => stopHold(false)}
            onClick={() => handleWorkerClick(-workerStep)}
            disabled={workerControlDisabled || desiredWorkers <= workerMin}
            title={`Remove ${actor} worker`}
            aria-label={`Remove ${actor} worker`}
          >
            <Minus aria-hidden="true" />
          </button>
          <output
            id={`${actor}-count`}
            aria-label={
              desiredControl.phase === 'idle'
                ? `${title} worker count, ${desiredWorkers} applied`
                : `${title} worker count, ${workers.applied ?? 0} applied, ${desiredWorkers} ${desiredControl.phase}`
            }
          >
            {desiredWorkers}
          </output>
          <button
            id={`${actor}-plus`}
            type="button"
            onPointerDown={(event) => handleWorkerPointerDown(event, workerStep)}
            onLostPointerCapture={() => stopHold(false)}
            onClick={() => handleWorkerClick(workerStep)}
            disabled={workerControlDisabled || desiredWorkers >= workerMax}
            title={`Add ${actor} worker`}
            aria-label={`Add ${actor} worker`}
          >
            <Plus aria-hidden="true" />
          </button>
        </div>
      </foreignObject>
      <g
        id={`${actor}-actor`}
        className={`pipeline-actor pipeline-selectable${selected ? ' pipeline-selectable--selected' : ''}`}
        role="button"
        tabIndex={0}
        aria-label={actorAriaLabel}
        aria-pressed={selected}
        data-worker-count={desiredWorkers}
        data-worker-layout={layout.mode}
        data-worker-columns={layout.columns}
        data-worker-rows={layout.rows}
        onClick={() => onSelect(actor)}
        onKeyDown={handleKeyDown}
      >
        <rect
          x={bounds.x}
          y={layout.top}
          width={bounds.width}
          height={layout.height}
          rx="5"
          className="pipeline-actor-box"
        />
        {layout.chips.slice(0, visibleWorkers).map((chip, index) => (
          <WorkerChip
            key={index}
            {...chip}
            {...chipState(index)}
          />
        ))}
        {overflowWorkers > 0 && (
          <text
            x={bounds.x + bounds.width - 8}
            y={layout.top + 16}
            textAnchor="end"
            className="pipeline-small"
          >
            +{overflowWorkers} workers
          </text>
        )}
        {sourceErrorOperation !== undefined && (
          <g
            role="alert"
            aria-label={`Reader source error: ${sourceErrorOperation}`}
            pointerEvents="none"
            data-testid="reader-source-error-overlay"
          >
            <rect
              x={bounds.x}
              y={layout.top}
              width={bounds.width}
              height={layout.height}
              rx="5"
              fill="#5f1018"
              fillOpacity="0.84"
            />
            <text
              x={bounds.x + bounds.width / 2}
              y={layout.top + layout.height / 2}
              fill="#ffd9dc"
              fontSize="13"
              fontWeight="700"
              textAnchor="middle"
            >
              SOURCE ERROR
            </text>
          </g>
        )}
        {inputPort && (
          <circle
            cx={inputPort.x}
            cy={inputPort.y}
            r="7"
            className="pipeline-port"
          />
        )}
        <circle
          cx={outputPort.x}
          cy={outputPort.y}
          r="7"
          className="pipeline-port"
        />
        <text
          x={metricPoints.primary.x}
          y={metricPoints.primary.y}
          textAnchor={metricPoints.primary.anchor}
          className="pipeline-value pipeline-worker-primary"
        >
          {primaryMetric}
        </text>
        {secondaryMetric && (
          <text
            x={metricPoints.secondary.x}
            y={metricPoints.secondary.y}
            textAnchor={metricPoints.secondary.anchor}
            className="pipeline-small pipeline-worker-secondary"
          >
            {secondaryMetric}
          </text>
        )}
        {(statusMetric || statusMetricSegments) && (
          <text
            x={metricPoints.status.x}
            y={metricPoints.status.y}
            textAnchor={metricPoints.status.anchor}
            className="pipeline-worker-status"
          >
            {statusMetricSegments === undefined
              ? statusMetric
              : statusMetricSegments.map((segment, index) => (
                  <tspan
                    key={segment.value}
                    className={`worker-state worker-state--${segment.tone}`}
                  >
                    {index > 0 && ' · '}{segment.value}
                  </tspan>
                ))}
          </text>
        )}
      </g>
    </>
  )
}
