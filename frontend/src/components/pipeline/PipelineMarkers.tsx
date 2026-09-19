import type { CSSProperties } from 'react'
import type { LoadgenSnapshot, ChannelSnapshot } from '../../model/loadgen'
import { channelPressureColor } from '../../model/channelFlowState'
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

function channelMarkerColor(channel: ChannelSnapshot): string {
  if (channel.flowState === 'connection-error') return 'var(--red)'
  if (channel.flowState === 'stopped') return 'var(--muted)'
  return channelPressureColor(channel.displayedPressure)
}

function markerColor(
  marker: PipelineMarkerSlotSnapshot,
  snapshot: LoadgenSnapshot,
): string {
  if (marker.outcomeVisible && marker.outcome !== null) {
    return marker.outcome === 'success' ? 'var(--green)' : 'var(--red)'
  }
  if (marker.stage === 'readerChannel') return channelMarkerColor(snapshot.readerChannel)
  if (marker.stage === 'senderChannel') return channelMarkerColor(snapshot.senderChannel)
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
  const valveOpeningIndex = markers.valveOpeningIndex
  const stagePaths = Object.fromEntries(([
    'reader',
    'readerChannel',
    'throttler',
    'senderChannel',
    'sender',
    'http',
    'target',
  ] satisfies readonly MarkerStage[]).map((stage) => [
    stage,
    getMarkerStagePathGeometry(
      stage,
      snapshot.readerChannel.capacity,
      snapshot.senderChannel.capacity,
      valveOpeningIndex,
      resolvedGeometry,
    ),
  ])) as Record<MarkerStage, ReturnType<typeof getMarkerStagePathGeometry>>
  const waitingMarkers = markers.markers
    .filter((marker) => marker.stage === 'throttler' && marker.buffered)
    .sort((left, right) =>
      right.phase - left.phase ||
      Number(left.familyId?.split('-').at(-1)) -
        Number(right.familyId?.split('-').at(-1))
    )
  const waitingTarget = Math.min(
    Math.max(0, Math.floor(snapshot.readerChannel.depthBatches ?? 0)),
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
        const jitter = marker.buffered && marker.stage === 'throttler'
          ? markerWaitingOffset(
            marker.familyId ?? '',
            marker.slotId,
            markers.motionElapsedMs,
            markers.reducedMotion,
          )
          : { x: 0, y: 0 }
        const waitingVisible = marker.stage !== 'throttler' ||
          !marker.buffered || visibleWaitingIds.has(marker.slotId)
        const outcomeClass = marker.outcomeVisible && marker.outcome !== null
          ? ` pipeline-marker--outcome pipeline-marker--${marker.outcome}`
          : ''
        const pulseProgress = markers.reducedMotion ? 0 : marker.pulseProgress
        const markerStyle = {
          color: markerColor(marker, snapshot),
          offsetPath: `path("${geometry.path}")`,
          offsetDistance: `${marker.phase * 100}%`,
          filter: marker.outcomeVisible && !markers.reducedMotion
            ? `drop-shadow(0 0 ${2 + pulseProgress * 7}px currentColor)`
            : undefined,
          opacity: marker.outcomeVisible && !markers.reducedMotion
            ? Math.max(0.28, 1 - pulseProgress * 0.72)
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
            r={marker.outcomeVisible ? 4 + pulseProgress * 2 : 4}
            visibility={marker.state === 'inactive' || !waitingVisible
              ? 'hidden'
              : 'visible'}
            className={`pipeline-marker pipeline-marker--${marker.state} pipeline-marker--stage-${marker.stage}${marker.buffered ? ' pipeline-marker--buffered' : ''}${outcomeClass}`}
            style={markerStyle}
            data-marker-id={marker.slotId}
            data-family-id={marker.familyId ?? ''}
            data-marker-stage={marker.stage}
            data-marker-state={marker.state}
            data-marker-phase={marker.phase.toFixed(4)}
            data-marker-outcome={marker.outcome ?? ''}
            data-marker-retry-attempt={marker.retryAttempt === true}
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
