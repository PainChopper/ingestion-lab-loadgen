import type { PipelineGeometry } from './geometry'

interface TargetActorProps {
  geometry: PipelineGeometry['actors']['target']
}

export function TargetActor({ geometry }: TargetActorProps) {
  const [receiver, storage, outbox, kafka] = geometry.stages

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
      <g id="target-actor" className="pipeline-actor">
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
        {geometry.connectors.map((connector) => (
          <line
            key={connector.y1}
            x1={connector.x}
            y1={connector.y1}
            x2={connector.x}
            y2={connector.y2}
            className="pipeline-service-connector"
          />
        ))}
        {[receiver, storage, outbox, kafka].map((stage) => (
          <rect
            key={stage.y}
            x={stage.x}
            y={stage.y}
            width={stage.width}
            height={stage.height}
            rx="4"
            className="pipeline-service-stage"
          />
        ))}
        <text
          x={receiver.label.x}
          y={receiver.label.y}
          textAnchor={receiver.label.anchor}
          className="pipeline-small-strong"
        >
          HTTP receiver
        </text>
        <text
          x={storage.label.x}
          y={storage.label.y}
          textAnchor={storage.label.anchor}
          className="pipeline-small-strong"
        >
          S3 / Vaultbox
        </text>
        <text
          x={outbox.label.x}
          y={outbox.label.y}
          textAnchor={outbox.label.anchor}
          className="pipeline-small-strong"
        >
          Postgres outbox
        </text>
        <text
          x={kafka.label.x}
          y={kafka.label.y}
          textAnchor={kafka.label.anchor}
          className="pipeline-small-strong"
        >
          Kafka
        </text>
      </g>
    </>
  )
}
