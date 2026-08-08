import type { KeyboardEvent } from 'react'
import type { SelectableId, TargetSnapshot } from '../../model/loadgen'
import { formatRate } from './formatters'
import { ACTOR_GEOMETRY } from './geometry'

interface TargetActorProps {
  snapshot: TargetSnapshot
  selected: boolean
  onSelect: (id: SelectableId) => void
}

export function TargetActor({
  snapshot,
  selected,
  onSelect,
}: TargetActorProps) {
  const geometry = ACTOR_GEOMETRY.target
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
        textAnchor="middle"
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
        <circle cx="1000" cy="342" r="30" className="pipeline-target-ring" />
        <circle cx="1000" cy="342" r="17" className="pipeline-target-ring" />
        <circle cx="1000" cy="342" r="4" className="pipeline-target-center" />
        <line x1="1000" y1="305" x2="1000" y2="379" className="pipeline-target-ring" />
        <line x1="963" y1="342" x2="1037" y2="342" className="pipeline-target-ring" />
        <text x="1000" y="407" textAnchor="middle" className="pipeline-small">
          Accepted TPS
        </text>
        <text x="1000" y="431" textAnchor="middle" className="pipeline-value">
          {formatRate(snapshot.acceptedTps)}
        </text>
        <text x="1000" y="456" textAnchor="middle" className="pipeline-small">
          {snapshot.connectionState}
        </text>
      </g>
    </>
  )
}
