import {
  useEffect,
  useLayoutEffect,
  useMemo,
  useState,
  useSyncExternalStore,
} from 'react'
import type { LoadgenSnapshot, ChannelSnapshot } from '../../model/loadgen'
import {
  MarkerLifecycleController,
} from './markerLifecycle'
import type {
  MarkerLifecycleSnapshot,
  MarkerLifecycleTelemetry,
  ChannelMarkerTelemetry,
} from './markerLifecycle'
import {
  getMarkerStagePathGeometry,
  getValveMarkerPathGeometry,
} from './markerPaths'
import type { MarkerStage } from './markerLifecycle'
import { getChannelCapacityPresentation } from './channelCableGeometry'
import { effectiveValveOpeningIndex } from './throttlerValve'
import {
  createPipelineGeometry,
  type PipelineGeometry,
} from './geometry'
import { normalizedWorkerCount } from './workerActorLayout'

function usePrefersReducedMotion(): boolean {
  const [reducedMotion, setReducedMotion] = useState(() =>
    typeof window !== 'undefined' &&
    window.matchMedia('(prefers-reduced-motion: reduce)').matches,
  )

  useEffect(() => {
    const media = window.matchMedia('(prefers-reduced-motion: reduce)')
    const handleChange = () => setReducedMotion(media.matches)
    media.addEventListener('change', handleChange)
    return () => media.removeEventListener('change', handleChange)
  }, [])

  return reducedMotion
}

function channelReceiveActive(snapshot: ChannelSnapshot): boolean {
  if (
    snapshot.flowState === 'stopped' ||
    snapshot.flowState === 'connection-error'
  ) {
    return false
  }

  return snapshot.throughputTps !== null && snapshot.throughputTps > 0
}

function channelTelemetry(snapshot: ChannelSnapshot): ChannelMarkerTelemetry {
  return {
    id: snapshot.id,
    depthBatches: snapshot.depthBatches,
    appliedCapacity: getChannelCapacityPresentation(snapshot.capacity).applied,
    throughputTps: snapshot.throughputTps,
    receiveActive: channelReceiveActive(snapshot),
    blocked:
      snapshot.flowState === 'backpressure' || snapshot.blockedSenders > 0,
    sentBatchesTotal: snapshot.sentBatchesTotal,
    receivedBatchesTotal: snapshot.receivedBatchesTotal,
  }
}

export function markerTelemetryFromSnapshot(
  snapshot: LoadgenSnapshot,
  reducedMotion: boolean,
  geometry?: PipelineGeometry,
): MarkerLifecycleTelemetry {
  const resolvedGeometry = geometry ?? createPipelineGeometry({
    orientation: 'landscape',
    readerWorkers: normalizedWorkerCount(snapshot.reader.workers),
    senderWorkers: normalizedWorkerCount(snapshot.sender.workers),
  })
  const valveOpeningIndex = effectiveValveOpeningIndex(
    snapshot.throttler.installationMode.applied,
    snapshot.throttler.requestedTps,
  )
  const valveGeometry = getValveMarkerPathGeometry(
    valveOpeningIndex,
    resolvedGeometry,
  )
  const stages: readonly MarkerStage[] = [
    'reader',
    'readerChannel',
    'throttler',
    'senderChannel',
    'sender',
    'http',
    'target',
  ]
  const stageTravelLengths = Object.fromEntries(stages.map((stage) => [
    stage,
    getMarkerStagePathGeometry(
      stage,
      snapshot.readerChannel.capacity,
      snapshot.senderChannel.capacity,
      valveOpeningIndex,
      resolvedGeometry,
    ).length,
  ])) as Record<MarkerStage, number>

  return {
    runState: snapshot.runState,
    reducedMotion,
    valveOpeningIndex,
    valvePreAdmissionStopPhase: valveGeometry.preAdmissionStopPhase,
    valveExitPhase: valveGeometry.exitPhase,
    readerChannel: channelTelemetry(snapshot.readerChannel),
    senderChannel: channelTelemetry(snapshot.senderChannel),
    http: {
      inFlightRequests: snapshot.http.inFlightRequests ?? 0,
      requestsStartedTotal: snapshot.http.requestsStartedTotal,
      requestsCompletedTotal: snapshot.http.requestsCompletedTotal,
      requestsSucceededTotal: snapshot.http.requestsSucceededTotal,
      requestsFailedTotal: snapshot.http.requestsFailedTotal,
      retryAttemptsStartedTotal: snapshot.sender.retryAttemptsStartedTotal,
      connectionError:
        snapshot.http.connectionState === 'error' ||
        snapshot.http.connectionState === 'disconnected',
    },
    stageTravelLengths,
  }
}

export function usePipelineMarkerLifecycle(
  snapshot: LoadgenSnapshot,
  geometry: PipelineGeometry,
): MarkerLifecycleSnapshot {
  const reducedMotion = usePrefersReducedMotion()
  const telemetry = useMemo(
    () => markerTelemetryFromSnapshot(snapshot, reducedMotion, geometry),
    [snapshot, reducedMotion, geometry],
  )
  const [controller] = useState(
    () => new MarkerLifecycleController(telemetry),
  )

  useLayoutEffect(() => {
    controller.reconcile(telemetry)
  }, [controller, telemetry])

  useEffect(() => {
    let frameId = 0
    let previousTime = performance.now()
    const tick = (time: number) => {
      controller.advance(Math.min(100, Math.max(0, time - previousTime)))
      previousTime = time
      frameId = requestAnimationFrame(tick)
    }
    frameId = requestAnimationFrame(tick)
    return () => cancelAnimationFrame(frameId)
  }, [controller])

  return useSyncExternalStore(
    controller.subscribe,
    controller.getSnapshot,
    controller.getSnapshot,
  )
}
