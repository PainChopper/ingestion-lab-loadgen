import { Minus, Plus } from 'lucide-react'
import type {
  NumericControlSnapshot,
  RunState,
  SelectableId,
} from '../../model/loadgen'
import type { Point, WorkerActorBounds } from './geometry'
import type { KeyboardEvent } from 'react'
import type { PipelineOrientation } from './pipelineLayout'
import {
  getWorkerActorLayout,
  type WorkerChipLayout,
} from './workerActorLayout'

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
  metricPoints?: {
    readonly primary: Point
    readonly secondary: Point
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
  active,
}: WorkerChipLayout & { active: boolean }) {
  const pinOffsets = [6, 13, 20, 27]
  const chipX = 4
  const chipY = 4

  return (
    <g
      className={active ? undefined : 'pipeline-worker--inactive'}
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
  runState,
  inputPort,
  outputPort,
  primaryMetric,
  secondaryMetric,
  metricPoints,
  orientation = 'landscape',
  selected,
  onSelect,
  onWorkerCountChange,
}: WorkerActorProps) {
  const layout = getWorkerActorLayout(actor, bounds, workers, orientation)
  const workerMin = Math.round(workers.min)
  const workerMax = Math.round(workers.max)
  const workerStep = Math.max(1, Math.round(workers.step))
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
            onClick={() =>
              onWorkerCountChange(
                actor,
                Math.max(workerMin, layout.workerCount - workerStep),
              )
            }
            disabled={layout.workerCount <= workerMin}
            title={`Remove ${actor} worker`}
            aria-label={`Remove ${actor} worker`}
          >
            <Minus aria-hidden="true" />
          </button>
          <output id={`${actor}-count`} aria-label={`${title} worker count`}>
            {layout.workerCount}
          </output>
          <button
            id={`${actor}-plus`}
            type="button"
            onClick={() =>
              onWorkerCountChange(
                actor,
                Math.min(workerMax, layout.workerCount + workerStep),
              )
            }
            disabled={layout.workerCount >= workerMax}
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
        data-worker-count={layout.workerCount}
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
          <WorkerChip
            key={index}
            {...chip}
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
          x={metricPoints?.primary.x ?? centerX}
          y={metricPoints?.primary.y ?? 510}
          textAnchor="middle"
          className="pipeline-value pipeline-worker-primary"
        >
          {primaryMetric}
        </text>
        <text
          x={metricPoints?.secondary.x ?? centerX}
          y={metricPoints?.secondary.y ?? 531}
          textAnchor="middle"
          className="pipeline-small pipeline-worker-secondary"
        >
          {secondaryMetric}
        </text>
      </g>
    </>
  )
}
