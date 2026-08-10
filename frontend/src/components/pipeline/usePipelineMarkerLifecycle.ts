import {
  useEffect,
  useLayoutEffect,
  useMemo,
  useState,
  useSyncExternalStore,
} from 'react'
import type { LoadgenSnapshot, QueueSnapshot } from '../../model/loadgen'
import {
  MarkerLifecycleController,
} from './markerLifecycle'
import type {
  MarkerLifecycleSnapshot,
  MarkerLifecycleTelemetry,
  QueueMarkerTelemetry,
} from './markerLifecycle'
import {
  getMarkerStagePathGeometry,
  getValveMarkerPathGeometry,
} from './markerPaths'
import type { MarkerStage } from './markerLifecycle'
import { getQueueCapacityPresentation } from './queueCableGeometry'
import { valueToOpeningIndex } from './throttlerValve'
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

function queueDequeueActive(snapshot: QueueSnapshot): boolean {
  if (
    snapshot.flowState === 'stopped' ||
    snapshot.flowState === 'connection-error'
  ) {
    return false
  }

  return snapshot.throughputTps !== null && snapshot.throughputTps > 0
}

function queueTelemetry(snapshot: QueueSnapshot): QueueMarkerTelemetry {
  return {
    id: snapshot.id,
    depthBatches: snapshot.depthBatches,
    appliedCapacity: getQueueCapacityPresentation(snapshot.capacity).applied,
    throughputTps: snapshot.throughputTps,
    dequeueActive: queueDequeueActive(snapshot),
    blocked:
      snapshot.flowState === 'backpressure' || snapshot.blockedSenders > 0,
    enqueuedBatchesTotal: snapshot.enqueuedBatchesTotal,
    dequeuedBatchesTotal: snapshot.dequeuedBatchesTotal,
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
  const valveOpeningIndex = valueToOpeningIndex(
    snapshot.throttler.requestedTps.applied,
    snapshot.throttler.requestedTps,
  )
  const valveGeometry = getValveMarkerPathGeometry(
    valveOpeningIndex,
    resolvedGeometry,
  )
  const stages: readonly MarkerStage[] = [
    'reader',
    'queue1',
    'throttler',
    'queue2',
    'sender',
    'http',
    'target',
  ]
  const stageTravelLengths = Object.fromEntries(stages.map((stage) => [
    stage,
    getMarkerStagePathGeometry(
      stage,
      snapshot.queue1.capacity,
      snapshot.queue2.capacity,
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
    queue1: queueTelemetry(snapshot.queue1),
    queue2: queueTelemetry(snapshot.queue2),
    http: {
      inFlightRequests: snapshot.http.inFlightRequests ?? 0,
      requestsStartedTotal: snapshot.http.requestsStartedTotal,
      requestsCompletedTotal: snapshot.http.requestsCompletedTotal,
      requestsSucceededTotal: snapshot.http.requestsSucceededTotal,
      requestsFailedTotal: snapshot.http.requestsFailedTotal,
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
