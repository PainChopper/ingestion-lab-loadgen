import type { KeyboardEvent } from 'react'
import type { SelectableId, ThrottlerSnapshot } from '../../model/loadgen'
import { getBarrierAngle } from './barrierAngle'
import { formatRate } from './formatters'
import { ACTOR_GEOMETRY } from './geometry'

interface ThrottlerActorProps {
  snapshot: ThrottlerSnapshot
  selected: boolean
  onSelect: (id: SelectableId) => void
}

export function ThrottlerActor({
  snapshot,
  selected,
  onSelect,
}: ThrottlerActorProps) {
  const geometry = ACTOR_GEOMETRY.throttler
  const requestedTps = snapshot.requestedTps.applied
  const barrierAngle = getBarrierAngle(
    requestedTps,
    snapshot.admittedTps,
    snapshot.state,
  )
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
        className="pipeline-title pipeline-title--throttler"
      >
        THROTTLER
      </text>
      <g
        id="throttler-actor"
        className={`pipeline-actor pipeline-selectable${selected ? ' pipeline-selectable--selected' : ''}`}
        role="button"
        tabIndex={0}
        aria-label="Inspect throttler"
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
        <circle
          cx={geometry.ports.output.x}
          cy={geometry.ports.output.y}
          r="7"
          className="pipeline-port"
        />
        <line x1="384" y1="376" x2="384" y2="423" className="pipeline-barrier-stand" />
        <line x1="373" y1="423" x2="397" y2="423" className="pipeline-barrier-stand" />
        <circle cx="384" cy="376" r="7" className="pipeline-barrier-pivot" />
        <g
          id="barrier-arm"
          className="pipeline-barrier-arm"
          style={{ transform: `rotate(${barrierAngle}deg)` }}
        >
          <path d="M384 370 h83 a4 4 0 0 1 4 4 v7 a4 4 0 0 1-4 4 h-83 z" />
          <path
            d="M400 370 h11 l-10 15 h-11 z M430 370 h11 l-10 15 h-11 z M460 370 h7 a4 4 0 0 1 4 4 v2 l-6 9 h-15 z"
            className="pipeline-barrier-stripe"
          />
        </g>
        <text x="430" y="314" textAnchor="middle" className="pipeline-small">
          Requested TPS
        </text>
        <text
          id="requested-display"
          x="430"
          y="337"
          textAnchor="middle"
          className="pipeline-value"
        >
          {formatRate(requestedTps)}
        </text>
        <line x1="377" y1="347" x2="483" y2="347" className="pipeline-divider" />
        <text x="430" y="442" textAnchor="middle" className="pipeline-small">
          Admitted TPS
        </text>
        <text
          id="admitted-display"
          x="430"
          y="462"
          textAnchor="middle"
          className="pipeline-value"
        >
          {formatRate(snapshot.admittedTps)}
        </text>
      </g>
    </>
  )
}
