import { Minus, Plus } from 'lucide-react'
import type {
  NumericControlSnapshot,
  RunState,
  SelectableId,
} from '../../model/loadgen'
import type { Point, WorkerActorBounds } from './geometry'
import type { KeyboardEvent } from 'react'

export type WorkerActorId = 'reader' | 'sender'

interface WorkerActorProps {
  actor: WorkerActorId
  title: string
  titlePoint: Point
  titleTone: 'reader' | 'sender'
  bounds: WorkerActorBounds
  controls: {
    readonly x: number
    readonly y: number
    readonly width: number
    readonly height: number
  }
  workers: NumericControlSnapshot
  runState: RunState
  inputPort?: Point
  outputPort: Point
  primaryMetric: string
  secondaryMetric: string
  selected: boolean
  onSelect: (id: SelectableId) => void
  onWorkerCountChange: (actor: WorkerActorId, value: number) => void
}

const WORKER_MIN = 1
const WORKER_MAX = 7

function normalizedWorkerCount(workers: NumericControlSnapshot): number {
  const value = workers.applied ?? workers.min
  return Math.min(WORKER_MAX, Math.max(WORKER_MIN, Math.round(value)))
}

function WorkerChip({ x, y, active }: Point & { active: boolean }) {
  const pinOffsets = [6, 13, 20, 27]

  return (
    <g className={active ? undefined : 'pipeline-worker--inactive'}>
      <rect
        x={x}
        y={y}
        width="31"
        height="31"
        rx="3"
        className="pipeline-worker-chip"
      />
      {pinOffsets.map((offset) => (
        <g key={offset} className="pipeline-worker-chip-pin">
          <line x1={x + offset} y1={y - 4} x2={x + offset} y2={y} />
          <line
            x1={x + offset}
            y1={y + 31}
            x2={x + offset}
            y2={y + 35}
          />
          <line x1={x - 4} y1={y + offset} x2={x} y2={y + offset} />
          <line
            x1={x + 31}
            y1={y + offset}
            x2={x + 35}
            y2={y + offset}
          />
        </g>
      ))}
      <polyline
        points={`${x + 5},${y + 17} ${x + 10},${y + 17} ${x + 14},${y + 10} ${x + 18},${y + 23} ${x + 22},${y + 15} ${x + 27},${y + 15}`}
        className="pipeline-worker-wave"
      />
      <circle
        cx={x + 57}
        cy={y + 15.5}
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
  runState,
  inputPort,
  outputPort,
  primaryMetric,
  secondaryMetric,
  selected,
  onSelect,
  onWorkerCountChange,
}: WorkerActorProps) {
  const workerCount = normalizedWorkerCount(workers)
  const height = workerCount * bounds.rowHeight + bounds.padding * 2
  const top = bounds.bottom - height
  const centerX = bounds.x + bounds.width / 2
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
        textAnchor="middle"
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
            onClick={() => onWorkerCountChange(actor, workerCount - 1)}
            disabled={workerCount <= WORKER_MIN}
            title={`Remove ${actor} worker`}
            aria-label={`Remove ${actor} worker`}
          >
            <Minus aria-hidden="true" />
          </button>
          <output id={`${actor}-count`} aria-label={`${title} worker count`}>
            {workerCount}
          </output>
          <button
            id={`${actor}-plus`}
            type="button"
            onClick={() => onWorkerCountChange(actor, workerCount + 1)}
            disabled={workerCount >= WORKER_MAX}
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
        aria-label={`Inspect ${actor}`}
        aria-pressed={selected}
        onClick={() => onSelect(actor)}
        onKeyDown={handleKeyDown}
      >
        <rect
          x={bounds.x}
          y={top}
          width={bounds.width}
          height={height}
          rx="5"
          className="pipeline-actor-box"
        />
        {Array.from({ length: workerCount }, (_, index) => (
          <WorkerChip
            key={index}
            x={bounds.x + 24}
            y={top + bounds.padding + index * bounds.rowHeight}
            active={runState === 'running'}
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
          x={centerX}
          y="510"
          textAnchor="middle"
          className="pipeline-value"
        >
          {primaryMetric}
        </text>
        <text
          x={centerX}
          y="531"
          textAnchor="middle"
          className="pipeline-small"
        >
          {secondaryMetric}
        </text>
      </g>
    </>
  )
}
