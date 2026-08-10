import type { CSSProperties } from 'react'
import type { LoadgenSnapshot, QueueSnapshot } from '../../model/loadgen'
import { queuePressureColor } from '../../model/queueFlowState'
import type {
  MarkerLifecycleSnapshot,
  MarkerStage,
  PipelineMarkerSlotSnapshot,
} from './markerLifecycle'
import { getMarkerStagePathGeometry } from './markerPaths'

interface PipelineMarkersProps {
  readonly snapshot: LoadgenSnapshot
  readonly markers: MarkerLifecycleSnapshot
}

function queueMarkerColor(queue: QueueSnapshot): string {
  if (queue.flowState === 'connection-error') return 'var(--red)'
  if (queue.flowState === 'stopped') return 'var(--muted)'
  return queuePressureColor(queue.displayedPressure)
}

function markerColor(
  marker: PipelineMarkerSlotSnapshot,
  snapshot: LoadgenSnapshot,
): string {
  if (marker.outcomeVisible && marker.outcome !== null) {
    return marker.outcome === 'success' ? 'var(--green)' : 'var(--red)'
  }
  if (marker.stage === 'queue1') return queueMarkerColor(snapshot.queue1)
  if (marker.stage === 'queue2') return queueMarkerColor(snapshot.queue2)
  if (marker.stage === 'reader') return 'var(--cyan)'
  if (marker.stage === 'target') return 'var(--purple)'
  if (marker.stage === 'http') return 'var(--green)'
  return 'var(--yellow)'
}

export function PipelineMarkers({ snapshot, markers }: PipelineMarkersProps) {
  const stagePaths = Object.fromEntries(([
    'reader',
    'queue1',
    'throttler',
    'queue2',
    'sender',
    'http',
    'target',
  ] satisfies readonly MarkerStage[]).map((stage) => [
    stage,
    getMarkerStagePathGeometry(
      stage,
      snapshot.queue1.capacity,
      snapshot.queue2.capacity,
    ),
  ])) as Record<MarkerStage, ReturnType<typeof getMarkerStagePathGeometry>>

  return (
    <g
      className="pipeline-marker-layer"
      aria-hidden="true"
      data-reduced-motion={markers.reducedMotion}
      data-run-state={snapshot.runState}
    >
      {markers.markers.map((marker) => {
        const geometry = stagePaths[marker.stage]
        const outcomeClass = marker.outcomeVisible && marker.outcome !== null
          ? ` pipeline-marker--outcome pipeline-marker--${marker.outcome}`
          : ''
        const markerStyle = {
          color: markerColor(marker, snapshot),
          offsetPath: `path("${geometry.path}")`,
          offsetDistance: `${marker.phase * 100}%`,
          filter: marker.outcomeVisible
            ? `drop-shadow(0 0 ${2 + marker.pulseProgress * 7}px currentColor)`
            : undefined,
          opacity: marker.outcomeVisible
            ? Math.max(0.28, 1 - marker.pulseProgress * 0.72)
            : undefined,
        } satisfies CSSProperties

        return (
          <circle
            key={marker.slotId}
            r={marker.outcomeVisible ? 4 + marker.pulseProgress * 2 : 4}
            visibility={marker.state === 'inactive' ? 'hidden' : 'visible'}
            className={`pipeline-marker pipeline-marker--${marker.state} pipeline-marker--stage-${marker.stage}${marker.queued ? ' pipeline-marker--queued' : ''}${outcomeClass}`}
            style={markerStyle}
            data-marker-id={marker.slotId}
            data-family-id={marker.familyId ?? ''}
            data-marker-stage={marker.stage}
            data-marker-state={marker.state}
            data-marker-phase={marker.phase.toFixed(4)}
            data-marker-outcome={marker.outcome ?? ''}
            data-marker-path={geometry.path}
          />
        )
      })}
    </g>
  )
}
