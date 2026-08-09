import type { CSSProperties } from 'react'
import type { LoadgenSnapshot } from '../../model/loadgen'
import type {
  HttpMarkerSlotSnapshot,
  MarkerLifecycleSnapshot,
} from './markerLifecycle'
import {
  getHttpTraversalPath,
  HTTP_TRAVERSAL_POINTS,
} from './markerPaths'

interface PipelineMarkersProps {
  readonly snapshot: LoadgenSnapshot
  readonly markers: MarkerLifecycleSnapshot
}

function HttpMarkerPool({
  markers,
}: {
  readonly markers: readonly HttpMarkerSlotSnapshot[]
}) {
  const path = getHttpTraversalPath()
  const targetPoint = HTTP_TRAVERSAL_POINTS.at(-1)

  return (
    <g className="pipeline-http-markers">
      {markers.map((marker) => {
        const outcomeClass = marker.outcomeVisible && marker.outcome !== null
          ? ` pipeline-http-marker--${marker.outcome}`
          : ''
        const markerStyle = {
          offsetPath: `path("${path}")`,
          offsetDistance: `${marker.phase * 100}%`,
        } satisfies CSSProperties
        return (
          <circle
            key={marker.slotId}
            r="4"
            visibility={marker.state === 'inactive' ? 'hidden' : 'visible'}
            className={`pipeline-http-marker${outcomeClass}`}
            style={markerStyle}
            data-marker-id={marker.slotId}
            data-family-id={marker.familyId ?? ''}
            data-marker-state={marker.state}
            data-marker-phase={marker.phase.toFixed(4)}
            data-marker-outcome={marker.outcome ?? ''}
          />
        )
      })}
      {targetPoint !== undefined && markers.map((marker) => (
        <circle
          key={`${marker.slotId}-outcome`}
          cx={targetPoint.x}
          cy={targetPoint.y}
          r={8 + marker.pulseProgress * 16}
          visibility={marker.outcomeVisible ? 'visible' : 'hidden'}
          className={`pipeline-target-outcome pipeline-target-outcome--${marker.outcome ?? 'success'}`}
          style={{ opacity: Math.max(0, 0.85 - marker.pulseProgress * 0.85) }}
          data-outcome-for={marker.slotId}
        />
      ))}
    </g>
  )
}

function ActorProcessingSlots() {
  return (
    <g className="pipeline-processing-slots">
      <circle cx="96" cy="392" r="9" className="pipeline-processing-slot pipeline-processing-slot--reader" />
      <circle cx="384" cy="398" r="8" className="pipeline-processing-slot pipeline-processing-slot--throttler" />
      <rect x="768" y="388" width="30" height="18" rx="4" className="pipeline-processing-slot pipeline-processing-slot--sender" />
      <circle cx="962" cy="415" r="8" className="pipeline-processing-slot pipeline-processing-slot--target" />
    </g>
  )
}

export function PipelineMarkers({ snapshot, markers }: PipelineMarkersProps) {
  return (
    <g
      className="pipeline-marker-layer"
      aria-hidden="true"
      data-reduced-motion={markers.reducedMotion}
      data-run-state={snapshot.runState}
    >
      <ActorProcessingSlots />
      <HttpMarkerPool markers={markers.http} />
    </g>
  )
}
