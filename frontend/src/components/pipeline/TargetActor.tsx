import type { KeyboardEvent } from 'react'
import type { SelectableId, TargetSnapshot } from '../../model/loadgen'
import { formatRate } from './formatters'
import type { PipelineGeometry } from './geometry'

interface TargetActorProps {
  snapshot: TargetSnapshot
  telemetryAvailable: boolean
  selected: boolean
  onSelect: (id: SelectableId) => void
  geometry: PipelineGeometry['actors']['target']
}

export function TargetActor({
  snapshot,
  telemetryAvailable,
  selected,
  onSelect,
  geometry,
}: TargetActorProps) {
  const [receiver, storage, outbox, kafka] = geometry.stages
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
        INGESTION SERVICE
      </text>
      <g
        id="target-actor"
        className={`pipeline-actor pipeline-selectable${selected ? ' pipeline-selectable--selected' : ''}`}
        role="button"
        tabIndex={0}
        aria-label="Inspect ingestion service"
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
        {geometry.connectors.map((connector) => <line key={connector.y1} x1={connector.x} y1={connector.y1} x2={connector.x} y2={connector.y2} className="pipeline-service-connector" />)}
        {[receiver, storage, outbox, kafka].map((stage) => <rect key={stage.y} x={stage.x} y={stage.y} width={stage.width} height={stage.height} rx="4" className="pipeline-service-stage" />)}
        <text x={receiver.label.x} y={receiver.label.y} textAnchor={receiver.label.anchor} className="pipeline-small-strong">HTTP receiver</text>
        <text x={receiver.detail!.x} y={receiver.detail!.y} textAnchor={receiver.detail!.anchor} className="pipeline-small pipeline-target-secondary">{telemetryAvailable ? `Accepted TPS ${formatRate(snapshot.acceptedTps)}` : 'TELEMETRY UNAVAILABLE'}</text>
        <text x={storage.label.x} y={storage.label.y} textAnchor={storage.label.anchor} className="pipeline-small-strong">S3 / Vaultbox</text>
        <text x={outbox.label.x} y={outbox.label.y} textAnchor={outbox.label.anchor} className="pipeline-small-strong">Postgres outbox</text>
        <text x={kafka.label.x} y={kafka.label.y} textAnchor={kafka.label.anchor} className="pipeline-small-strong">Kafka</text>
        <text x={receiver.label.x} y={receiver.label.y - 8} textAnchor={receiver.label.anchor} className="pipeline-small pipeline-target-secondary">POST batch</text>
      </g>
    </>
  )
}
