import type { KeyboardEvent } from 'react'
import type { SelectableId, TargetSnapshot } from '../../model/loadgen'
import { formatInteger, formatRate } from './formatters'
import type { PipelineGeometry } from './geometry'

interface TargetActorProps {
  snapshot: TargetSnapshot
  selected: boolean
  onSelect: (id: SelectableId) => void
  geometry: PipelineGeometry['actors']['target']
}

export function TargetActor({
  snapshot,
  selected,
  onSelect,
  geometry,
}: TargetActorProps) {
  const { center, labels } = geometry
  const rejectionPercent = formatInteger(snapshot.errorRatePercent.applied)
  const rejectedTps = formatInteger(snapshot.rejectedTps)
  const handleKeyDown = (event: KeyboardEvent<SVGGElement>) => {
    if (event.key !== 'Enter' && event.key !== ' ') return
    event.preventDefault()
    onSelect(snapshot.id)
  }

  return (
    <>
      <text
        x={geometry.title.x}
        y={geometry.title.y}
        textAnchor={geometry.title.anchor}
        className="pipeline-title pipeline-title--target"
      >
        TARGET
      </text>
      <g
        id="target-actor"
        className={`pipeline-actor pipeline-selectable${selected ? ' pipeline-selectable--selected' : ''}`}
        role="button"
        tabIndex={0}
        aria-label="Inspect target"
        aria-pressed={selected}
        onClick={() => onSelect(snapshot.id)}
        onKeyDown={handleKeyDown}
      >
        <rect
          x={geometry.bounds.x}
          y={geometry.bounds.y}
          width={geometry.bounds.width}
          height={geometry.bounds.height}
          rx="5"
          className="pipeline-actor-box"
        />
        <circle
          cx={geometry.ports.input.x}
          cy={geometry.ports.input.y}
          r="7"
          className="pipeline-port"
        />
        <circle cx={center.x} cy={center.y} r="30" className="pipeline-target-ring" />
        <circle cx={center.x} cy={center.y} r="17" className="pipeline-target-ring" />
        <circle cx={center.x} cy={center.y} r="4" className="pipeline-target-center" />
        <line x1={center.x} y1={center.y - 37} x2={center.x} y2={center.y + 37} className="pipeline-target-ring" />
        <line x1={center.x - 37} y1={center.y} x2={center.x + 37} y2={center.y} className="pipeline-target-ring" />
        <text x={labels.caption.x} y={labels.caption.y} textAnchor={labels.caption.anchor} className="pipeline-small pipeline-target-secondary">
          Accepted TPS
        </text>
        <text x={labels.value.x} y={labels.value.y} textAnchor={labels.value.anchor} className="pipeline-value pipeline-target-primary">
          {formatRate(snapshot.acceptedTps)}
        </text>
        <text x={labels.failure.x} y={labels.failure.y} textAnchor={labels.failure.anchor} className="pipeline-small pipeline-target-secondary pipeline-target-failure">
          {rejectionPercent === '—' ? rejectionPercent : `${rejectionPercent}%`} 503 rate · {rejectedTps} rejected tx/s
        </text>
        <text x={labels.state.x} y={labels.state.y} textAnchor={labels.state.anchor} className="pipeline-small pipeline-target-secondary">
          {snapshot.connectionState}
        </text>
      </g>
    </>
  )
}
