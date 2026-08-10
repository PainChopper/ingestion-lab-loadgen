import type { CSSProperties } from 'react'
import type { LoadgenSnapshot, QueueSnapshot } from '../../model/loadgen'
import { queuePressureColor } from '../../model/queueFlowState'
import type {
  MarkerLifecycleSnapshot,
  MarkerStage,
  PipelineMarkerSlotSnapshot,
} from './markerLifecycle'
import {
  markerWaitingOffset,
  MAX_VISIBLE_WAITING_FAMILIES,
} from './markerLifecycle'
import { getMarkerStagePathGeometry } from './markerPaths'
import {
  VALVE_APERTURE,
  valueToOpeningIndex,
} from './throttlerValve'
import {
  createPipelineGeometry,
  type PipelineGeometry,
} from './geometry'
import { normalizedWorkerCount } from './workerActorLayout'

interface PipelineMarkersProps {
  readonly snapshot: LoadgenSnapshot
  readonly markers: MarkerLifecycleSnapshot
  readonly geometry?: PipelineGeometry
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

export function PipelineMarkers({
  snapshot,
  markers,
  geometry,
}: PipelineMarkersProps) {
  const resolvedGeometry = geometry ?? createPipelineGeometry({
    orientation: 'landscape',
    readerWorkers: normalizedWorkerCount(snapshot.reader.workers),
    senderWorkers: normalizedWorkerCount(snapshot.sender.workers),
  })
  const orientation = resolvedGeometry.orientation
  const valveOpeningIndex = valueToOpeningIndex(
    snapshot.throttler.requestedTps.applied,
    snapshot.throttler.requestedTps,
  )
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
      valveOpeningIndex,
      resolvedGeometry,
    ),
  ])) as Record<MarkerStage, ReturnType<typeof getMarkerStagePathGeometry>>
  const waitingMarkers = markers.markers
    .filter((marker) => marker.stage === 'throttler' && marker.queued)
    .sort((left, right) =>
      right.phase - left.phase ||
      Number(left.familyId?.split('-').at(-1)) -
        Number(right.familyId?.split('-').at(-1))
    )
  const waitingTarget = Math.min(
    Math.max(0, Math.floor(snapshot.queue1.depthBatches ?? 0)),
    MAX_VISIBLE_WAITING_FAMILIES,
  )
  const visibleWaitingIds = new Set(
    waitingMarkers.slice(0, waitingTarget).map((marker) => marker.slotId),
  )
  const waitingRanks = new Map(
    waitingMarkers.map((marker, index) => [marker.slotId, index]),
  )
  const valveMarkerClipId = 'pipeline-valve-marker-clip'

  return (
    <g
      className="pipeline-marker-layer"
      aria-hidden="true"
      data-reduced-motion={markers.reducedMotion}
      data-run-state={snapshot.runState}
    >
      <defs>
        <clipPath id={valveMarkerClipId}>
          <g transform={`translate(${resolvedGeometry.actors.throttler.transform.x} ${resolvedGeometry.actors.throttler.transform.y})`}>
            {orientation === 'portrait' ? (
              <>
                <rect x="378" y="278" width="56" height="141" />
                <ellipse cx="430" cy="415" rx={VALVE_APERTURE.radiusX} ry={VALVE_APERTURE.radiusY} />
                <rect x="445" y="411" width="37" height="68" />
                <rect x="426" y="471" width="56" height="8" />
              </>
            ) : (
              <>
              <rect x="355" y="398" width="60" height="34" />
              <ellipse
                cx={VALVE_APERTURE.centerX}
                cy={VALVE_APERTURE.centerY}
                rx={VALVE_APERTURE.radiusX}
                ry={VALVE_APERTURE.radiusY}
              />
              <rect x="445" y="398" width="60" height="34" />
              </>
            )}
          </g>
        </clipPath>
      </defs>
      {markers.markers.map((marker) => {
        const geometry = stagePaths[marker.stage]
        const jitter = marker.queued && marker.stage === 'throttler'
          ? markerWaitingOffset(
            marker.familyId ?? '',
            marker.slotId,
            markers.motionElapsedMs,
            markers.reducedMotion,
          )
          : { x: 0, y: 0 }
        const waitingVisible = marker.stage !== 'throttler' ||
          !marker.queued || visibleWaitingIds.has(marker.slotId)
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
          transform: jitter.x === 0 && jitter.y === 0
            ? undefined
            : `translate(${jitter.x}px, ${jitter.y}px)`,
        } satisfies CSSProperties

        return (
          <g
            key={marker.slotId}
            clipPath={marker.stage === 'throttler'
              ? `url(#${valveMarkerClipId})`
              : undefined}
            data-marker-mask={marker.stage === 'throttler'
              ? 'aperture-and-body'
              : undefined}
          >
            <circle
            r={marker.outcomeVisible ? 4 + marker.pulseProgress * 2 : 4}
            visibility={marker.state === 'inactive' || !waitingVisible
              ? 'hidden'
              : 'visible'}
            className={`pipeline-marker pipeline-marker--${marker.state} pipeline-marker--stage-${marker.stage}${marker.queued ? ' pipeline-marker--queued' : ''}${outcomeClass}`}
            style={markerStyle}
            data-marker-id={marker.slotId}
            data-family-id={marker.familyId ?? ''}
            data-marker-stage={marker.stage}
            data-marker-state={marker.state}
            data-marker-phase={marker.phase.toFixed(4)}
            data-marker-outcome={marker.outcome ?? ''}
            data-marker-path={geometry.path}
            data-marker-jitter-x={jitter.x}
            data-marker-jitter-y={jitter.y}
            data-marker-waiting-rank={waitingRanks.get(marker.slotId) ?? ''}
            data-marker-rendered={waitingVisible}
            />
          </g>
        )
      })}
    </g>
  )
}
