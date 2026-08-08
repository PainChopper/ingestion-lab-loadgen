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
  getHttpTraversalLength,
  getQueueMarkerPathGeometry,
} from './markerPaths'
import { getQueueCapacityPresentation } from './queueCableGeometry'

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

function queueFlowActive(snapshot: QueueSnapshot): boolean {
  if (
    snapshot.flowState === 'stopped' ||
    snapshot.flowState === 'connection-error'
  ) {
    return false
  }

  return snapshot.inputBatchesPerSecond > 0 ||
    snapshot.outputBatchesPerSecond > 0 ||
    snapshot.handoffBatches > 0 ||
    (snapshot.throughputTps !== null && snapshot.throughputTps > 0)
}

function queueTelemetry(snapshot: QueueSnapshot): QueueMarkerTelemetry {
  const paths = getQueueMarkerPathGeometry(snapshot.id, snapshot.capacity)
  return {
    id: snapshot.id,
    depthBatches: snapshot.depthBatches,
    appliedCapacity: getQueueCapacityPresentation(snapshot.capacity).applied,
    flowActive: queueFlowActive(snapshot),
    handoffBatches: snapshot.handoffBatches,
    handoffBatchesTotal: snapshot.handoffBatchesTotal,
    enqueuedBatchesTotal: snapshot.enqueuedBatchesTotal,
    dequeuedBatchesTotal: snapshot.dequeuedBatchesTotal,
    occupancyTravelLength: paths.occupancyLength,
    flowTravelLength: paths.flowLength,
    handoffTravelLength: paths.handoffLength,
  }
}

export function markerTelemetryFromSnapshot(
  snapshot: LoadgenSnapshot,
  reducedMotion: boolean,
): MarkerLifecycleTelemetry {
  return {
    runState: snapshot.runState,
    reducedMotion,
    queue1: queueTelemetry(snapshot.queue1),
    queue2: queueTelemetry(snapshot.queue2),
    http: {
      inFlightRequests: snapshot.http.inFlightRequests ?? 0,
      requestsStartedTotal: snapshot.http.requestsStartedTotal,
      requestsCompletedTotal: snapshot.http.requestsCompletedTotal,
      requestsSucceededTotal: snapshot.http.requestsSucceededTotal,
      requestsFailedTotal: snapshot.http.requestsFailedTotal,
      travelLength: getHttpTraversalLength(),
      connectionError:
        snapshot.http.connectionState === 'error' ||
        snapshot.http.connectionState === 'disconnected',
    },
  }
}

export function usePipelineMarkerLifecycle(
  snapshot: LoadgenSnapshot,
): MarkerLifecycleSnapshot {
  const reducedMotion = usePrefersReducedMotion()
  const telemetry = useMemo(
    () => markerTelemetryFromSnapshot(snapshot, reducedMotion),
    [snapshot, reducedMotion],
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
