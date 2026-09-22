import { Minus, Plus } from 'lucide-react'
import type {
  NumericControlSnapshot,
  RunState,
  SelectableId,
  SenderWorkerState,
  SenderWorkerSlotSnapshot,
} from '../../model/loadgen'
import type { Point, TextPlacement, WorkerActorBounds } from './geometry'
import type { KeyboardEvent } from 'react'
import type { PipelineOrientation } from './pipelineLayout'
import {
  getWorkerActorLayout,
  normalizedWorkerCount,
  type WorkerChipLayout,
} from './workerActorLayout'

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
  workerSlots?: readonly SenderWorkerSlotSnapshot[] | null
  runState: RunState
  active?: boolean
  inputPort?: Point
  outputPort: Point
  primaryMetric: string
  secondaryMetric: string
  statusMetric?: string
  metricPoints: {
    readonly primary: TextPlacement
    readonly secondary: TextPlacement
    readonly status: TextPlacement
  }
  orientation?: PipelineOrientation
  selected: boolean
  onSelect: (id: SelectableId) => void
  onWorkerCountChange: (actor: WorkerActorId, value: number) => void
}

function WorkerChip({
  x,
  y,
  scale,
  state,
  slot,
}: WorkerChipLayout & {
  state: SenderWorkerState | 'active' | 'inactive' | 'draining' | 'terminal-error'
  slot?: SenderWorkerSlotSnapshot
}) {
  const pinOffsets = [6, 13, 20, 27]
  const chipX = 4
  const chipY = 4

  return (
    <g
      className={`pipeline-worker--${state}`}
      transform={`translate(${x} ${y}) scale(${scale})`}
      data-worker-slot-id={slot?.id}
      data-worker-ordinal={slot?.ordinal}
      data-worker-activity={slot?.activity}
      data-worker-lifecycle={slot?.lifecycle}
      data-worker-terminal-error={slot?.terminalError}
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
  workerSlots,
  runState,
  active,
  inputPort,
  outputPort,
  primaryMetric,
  secondaryMetric,
  statusMetric,
  metricPoints,
  orientation = 'landscape',
  selected,
  onSelect,
  onWorkerCountChange,
}: WorkerActorProps) {
  const desiredWorkers = normalizedWorkerCount(workers)
  const visibleWorkers = actor === 'sender' ? workerSlots?.length ?? 0 : desiredWorkers
  const layout = getWorkerActorLayout(actor, bounds, workers, orientation, visibleWorkers)
  const workerMin = Math.round(workers.min)
  const workerMax = Math.round(workers.max)
  const workerStep = Math.max(1, Math.round(workers.step))
  const chipState = (index: number): SenderWorkerState | 'active' | 'inactive' | 'draining' | 'terminal-error' => {
    if (workerSlots === undefined || workerSlots === null) {
      return (active ?? (runState === 'running')) ? 'active' : 'inactive'
    }
    const slot = workerSlots[index]
    if (slot === undefined) return 'inactive'
    if (slot.terminalError) return 'terminal-error'
    if (slot.activity === 'backoff') return 'backoff'
    if (slot.lifecycle === 'draining') return 'draining'
    return slot.activity
  }
  const actorAriaLabel = liveWorkers === undefined
    ? `Inspect ${actor}`
    : `Inspect ${actor}, ${workers.applied ?? 0} desired, ${liveWorkers} live, ${drainingWorkers ?? 0} draining`
  const handleKeyDown = (event: KeyboardEvent<SVGGElement>) => {
    if (event.key !== 'Enter' && event.key !== ' ') return
    event.preventDefault()
    onSelect(actor)
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
            onClick={() =>
              onWorkerCountChange(
                actor,
                Math.max(workerMin, desiredWorkers - workerStep),
              )
            }
            disabled={desiredWorkers <= workerMin}
            title={`Remove ${actor} worker`}
            aria-label={`Remove ${actor} worker`}
          >
            <Minus aria-hidden="true" />
          </button>
          <output
            id={`${actor}-count`}
            aria-label={
              workers.pending === null
                ? `${title} worker count, ${desiredWorkers} applied`
                : `${title} worker count, ${desiredWorkers} applied, ${workers.pending} pending`
            }
          >
            {desiredWorkers}
          </output>
          <button
            id={`${actor}-plus`}
            type="button"
            onClick={() =>
              onWorkerCountChange(
                actor,
                Math.min(workerMax, desiredWorkers + workerStep),
              )
            }
            disabled={desiredWorkers >= workerMax}
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
        {layout.chips.filter((_, index) => actor !== 'sender' || (workerSlots !== null && workerSlots !== undefined && index < workerSlots.length)).map((chip, index) => (
          <WorkerChip
            key={workerSlots?.[index]?.id ?? index}
            {...chip}
            state={chipState(index)}
            slot={workerSlots?.[index]}
          />
        ))}
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
        <text
          x={metricPoints.secondary.x}
          y={metricPoints.secondary.y}
          textAnchor={metricPoints.secondary.anchor}
          className="pipeline-small pipeline-worker-secondary"
        >
          {secondaryMetric}
        </text>
        {statusMetric && (
          <text
            x={metricPoints.status.x}
            y={metricPoints.status.y}
            textAnchor={metricPoints.status.anchor}
            className="pipeline-worker-status"
          >
            {statusMetric}
          </text>
        )}
      </g>
    </>
  )
}
