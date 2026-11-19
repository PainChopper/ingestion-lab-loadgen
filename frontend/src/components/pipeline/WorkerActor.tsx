import { clsx } from 'clsx'
import { Minus, Plus } from 'lucide-react'
import {
  useLayoutEffect,
  useRef,
  type KeyboardEvent,
  type MouseEvent,
  type PointerEvent as ReactPointerEvent,
} from 'react'
import type { DesiredControl } from '../../hooks/useDesiredControl'
import { useHoldRepeat } from '../../hooks/useHoldRepeat'
import type { InspectorRowSegment } from '../inspectorViewModel'
import type { NumericControlSnapshot, RunState, SelectableId } from '../../model/loadgen'
import type { Point, TextPlacement, WorkerActorBounds } from './geometry'
import type { PipelineOrientation } from './pipelineLayout'
import { getWorkerActorLayout, type WorkerChipLayout } from './workerActorLayout'

export type WorkerActorId = 'reader' | 'sender'

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
  drainingActivityCounts: Readonly<
    Record<'idle' | 'reading' | 'blocked' | 'in-flight' | 'backoff', number>
  >
  runState: RunState | null
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
      className={clsx(`pipeline-worker--${state}`, draining && 'pipeline-worker--draining')}
      transform={`translate(${x} ${y}) scale(${scale})`}
    >
      <rect x={chipX} y={chipY} width="31" height="31" rx="3" className="pipeline-worker-chip" />
      {pinOffsets.map((offset) => (
        <g key={offset} className="pipeline-worker-chip-pin">
          <line x1={chipX + offset} y1={chipY - 4} x2={chipX + offset} y2={chipY} />
          <line x1={chipX + offset} y1={chipY + 31} x2={chipX + offset} y2={chipY + 35} />
          <line x1={chipX - 4} y1={chipY + offset} x2={chipX} y2={chipY + offset} />
          <line x1={chipX + 31} y1={chipY + offset} x2={chipX + 35} y2={chipY + offset} />
        </g>
      ))}
      <polyline
        points={`${chipX + 5},${chipY + 17} ${chipX + 10},${chipY + 17} ${chipX + 14},${chipY + 10} ${chipX + 18},${chipY + 23} ${chipX + 22},${chipY + 15} ${chipX + 27},${chipY + 15}`}
        className="pipeline-worker-wave"
      />
      <circle cx={chipX + 57} cy={chipY + 15.5} r="5.5" className="pipeline-worker-led" />
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
  const suppressClick = useRef(false)
  const desiredWorkers = Math.min(
    Math.round(workers.max),
    Math.max(Math.round(workers.min), Math.round(desiredControl.desired)),
  )
  const desiredRef = useRef(desiredWorkers)
  // A batched rollback can leave the rendered value unchanged after the ref advances.
  useLayoutEffect(() => {
    desiredRef.current = desiredWorkers
  })
  const layout = getWorkerActorLayout(
    bounds,
    workers,
    orientation,
    liveWorkers ?? 0,
    desiredWorkers,
  )
  const overflowWorkers = (liveWorkers ?? 0) - layout.chips.length
  const workerMin = Math.round(workers.min)
  const workerStep = Math.max(1, Math.round(workers.step))
  const workerMax =
    workerMin + Math.floor((Math.round(workers.max) - workerMin) / workerStep) * workerStep
  const workerControlDisabled = !desiredControl.available || desiredControl.phase === 'pending'
  const states =
    actor === 'reader'
      ? (['blocked', 'reading', 'idle'] as const)
      : (['backoff', 'in-flight', 'idle'] as const)
  const chipState = (index: number) => {
    if (muted) return { state: 'inactive' as const, draining: false }
    for (const state of states) {
      if (index < activityCounts[state])
        return { state, draining: index < drainingActivityCounts[state] }
      index -= activityCounts[state]
    }
    return {
      state: (active ?? runState === 'running') ? ('active' as const) : ('inactive' as const),
      draining: false,
    }
  }
  const actorAriaLabel =
    actor === 'reader' || liveWorkers === undefined
      ? `Inspect ${actor}`
      : `Inspect ${actor}, ${desiredWorkers} desired, ${liveWorkers} live, ${drainingWorkers ?? 0} draining`
  const handleKeyDown = (event: KeyboardEvent<SVGGElement>) => {
    if (event.key !== 'Enter' && event.key !== ' ') return
    event.preventDefault()
    onSelect(actor)
  }
  const stepWorkers = (direction: -1 | 1) => {
    if (workerControlDisabled) return false
    const next = Math.min(
      workerMax,
      Math.max(workerMin, desiredRef.current + direction * workerStep),
    )
    if (next === desiredRef.current) return false
    desiredRef.current = next
    desiredControl.preview(next)
    return true
  }
  const hold = useHoldRepeat({
    step: stepWorkers,
    onCommit: desiredControl.commit,
    onCancel: desiredControl.cancel,
    commitAtLimit: true,
  })
  const handleWorkerPointerDown = (
    event: ReactPointerEvent<HTMLButtonElement>,
    direction: -1 | 1,
  ) => {
    event.preventDefault()
    if (!hold.start(event.pointerId, direction)) return
    suppressClick.current = true
    event.currentTarget.setPointerCapture?.(event.pointerId)
  }
  const handleWorkerClick = (event: MouseEvent<HTMLButtonElement>, direction: -1 | 1) => {
    if (event.detail > 0 && suppressClick.current) {
      suppressClick.current = false
      return
    }
    if (!stepWorkers(direction)) return
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
          role="group"
          aria-label={`${title.toLocaleLowerCase()} workers`}
        >
          <button
            id={`${actor}-minus`}
            type="button"
            onPointerDown={(event) => handleWorkerPointerDown(event, -1)}
            onLostPointerCapture={hold.cancel}
            onClick={(event) => handleWorkerClick(event, -1)}
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
            onPointerDown={(event) => handleWorkerPointerDown(event, 1)}
            onLostPointerCapture={hold.cancel}
            onClick={(event) => handleWorkerClick(event, 1)}
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
        className={clsx(
          'pipeline-actor pipeline-selectable',
          selected && 'pipeline-selectable--selected',
        )}
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
        {layout.chips.map((chip, index) => (
          <WorkerChip key={index} {...chip} {...chipState(index)} />
        ))}
        {overflowWorkers > 0 && (
          <text
            x={bounds.x + bounds.width / 2}
            y={bounds.bottom - (orientation === 'portrait' ? 13 : 9)}
            textAnchor="middle"
            className="pipeline-worker-overflow"
          >
            +{overflowWorkers} {overflowWorkers === 1 ? 'worker' : 'workers'}
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
              className="pipeline-reader-source-error__background"
            />
            <text
              x={bounds.x + bounds.width / 2}
              y={layout.top + layout.height / 2}
              className="pipeline-reader-source-error__label"
              textAnchor="middle"
            >
              SOURCE ERROR
            </text>
          </g>
        )}
        {inputPort && <circle cx={inputPort.x} cy={inputPort.y} r="7" className="pipeline-port" />}
        <circle cx={outputPort.x} cy={outputPort.y} r="7" className="pipeline-port" />
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
                    {index > 0 && ' · '}
                    {segment.value}
                  </tspan>
                ))}
          </text>
        )}
      </g>
    </>
  )
}
