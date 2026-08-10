import { describe, expect, it } from 'vitest'
import { SimulationAdapter } from '../../adapters/SimulationAdapter'
import type {
  LoadgenSnapshot,
  NumericControlSnapshot,
  QueueId,
  RunState,
} from '../../model/loadgen'
import { QueueFlowStateDeriver } from '../../model/queueFlowState'
import {
  getFlowMarkerTarget,
  getQueueDepthFamilyTarget,
  MARKER_STAGE_ORDER,
  markerMicroJitter,
  markerWaitingOffset,
  MarkerLifecycleController,
  MAX_FLOW_MARKERS,
  MAX_PENDING_OUTCOMES,
  MAX_PIPELINE_MARKERS,
  MAX_QUEUE_DEPTH_FAMILIES,
  MIN_FLOW_MARKERS,
  valveAdmissionIntervalMs,
} from './markerLifecycle'
import type {
  MarkerLifecycleTelemetry,
  MarkerStage,
  PipelineMarkerSlotSnapshot,
  QueueMarkerTelemetry,
} from './markerLifecycle'
import {
  getMarkerStagePathGeometry,
  getValveMarkerPathGeometry,
} from './markerPaths'
import { markerTelemetryFromSnapshot } from './usePipelineMarkerLifecycle'

function capacityControl(
  applied: number,
  max: number,
  step: number,
): NumericControlSnapshot {
  return {
    applied,
    preview: null,
    pending: null,
    min: 0,
    max,
    step,
    unit: 'batches',
    applyMode: 'immediate',
  }
}

const Q1_CONTROL = capacityControl(4, 12, 1)
const Q2_CONTROL = capacityControl(100, 160, 10)
const PRODUCTION_STAGE_LENGTHS = Object.fromEntries(
  MARKER_STAGE_ORDER.map((stage) => [
    stage,
    getMarkerStagePathGeometry(stage, Q1_CONTROL, Q2_CONTROL).length,
  ]),
) as Record<MarkerStage, number>

function queueTelemetry(
  id: QueueId,
  overrides: Partial<QueueMarkerTelemetry> = {},
): QueueMarkerTelemetry {
  const queue1 = id === 'reader-to-throttler'
  return {
    id,
    depthBatches: queue1 ? 4 : 100,
    appliedCapacity: queue1 ? 4 : 100,
    throughputTps: 50_000,
    dequeueActive: true,
    blocked: false,
    enqueuedBatchesTotal: 100,
    dequeuedBatchesTotal: 90,
    ...overrides,
  }
}

function telemetry(
  options: {
    runState?: RunState
    reducedMotion?: boolean
    valveOpeningIndex?: number
    queue1?: Partial<QueueMarkerTelemetry>
    queue2?: Partial<QueueMarkerTelemetry>
    http?: Partial<MarkerLifecycleTelemetry['http']>
  } = {},
): MarkerLifecycleTelemetry {
  const valveOpeningIndex = options.valveOpeningIndex ?? 11
  const valveGeometry = getValveMarkerPathGeometry(valveOpeningIndex)
  return {
    runState: options.runState ?? 'running',
    reducedMotion: options.reducedMotion ?? false,
    valveOpeningIndex,
    valvePreAdmissionStopPhase: valveGeometry.preAdmissionStopPhase,
    valveExitPhase: valveGeometry.exitPhase,
    queue1: queueTelemetry('reader-to-throttler', options.queue1),
    queue2: queueTelemetry('throttler-to-sender', options.queue2),
    http: {
      inFlightRequests: 0,
      requestsStartedTotal: 0,
      requestsCompletedTotal: 0,
      requestsSucceededTotal: 0,
      requestsFailedTotal: 0,
      connectionError: false,
      ...options.http,
    },
    stageTravelLengths: {
      ...PRODUCTION_STAGE_LENGTHS,
      throttler: valveGeometry.length,
    },
  }
}

function visible(controller: MarkerLifecycleController) {
  return controller.getSnapshot().markers.filter(
    (marker) => marker.state !== 'inactive',
  )
}

function familiesAtStage(
  controller: MarkerLifecycleController,
  stage: 'queue1' | 'queue2',
) {
  return flow(controller).filter((marker) => marker.stage === stage)
}

function flow(controller: MarkerLifecycleController) {
  return visible(controller).filter((marker) => marker.state === 'active')
}

function waitingAtValve(controller: MarkerLifecycleController) {
  return flow(controller).filter((marker) =>
    marker.stage === 'throttler' &&
    marker.queued
  )
}

function markerByFamily(
  controller: MarkerLifecycleController,
  familyId: string,
): PipelineMarkerSlotSnapshot {
  return controller.getSnapshot().markers.find(
    (marker) => marker.familyId === familyId,
  )!
}

function routeDistance(marker: PipelineMarkerSlotSnapshot): number {
  const stageIndex = MARKER_STAGE_ORDER.indexOf(marker.stage)
  const preceding = MARKER_STAGE_ORDER
    .slice(0, stageIndex)
    .reduce((sum, stage) => sum + PRODUCTION_STAGE_LENGTHS[stage], 0)
  return preceding + marker.phase * PRODUCTION_STAGE_LENGTHS[marker.stage]
}

function markerPosition(marker: PipelineMarkerSlotSnapshot) {
  return {
    slotId: marker.slotId,
    stage: marker.stage,
    phase: marker.phase,
  }
}

function expectFlowSurvivors(
  before: readonly PipelineMarkerSlotSnapshot[],
  after: readonly PipelineMarkerSlotSnapshot[],
): void {
  const beforeByFamily = new Map(before.map((marker) => [
    marker.familyId!,
    markerPosition(marker),
  ]))
  const survivors = after.filter((marker) =>
    marker.familyId !== null && beforeByFamily.has(marker.familyId)
  )

  expect(survivors.length).toBeGreaterThan(0)
  expect(survivors).toHaveLength(Math.min(before.length, after.length))
  for (const survivor of survivors) {
    expect(markerPosition(survivor)).toEqual(
      beforeByFamily.get(survivor.familyId!),
    )
  }
}

function advanceUntil(
  controller: MarkerLifecycleController,
  predicate: () => boolean,
  timeoutMs = 30_000,
): void {
  let elapsed = 0
  while (!predicate() && elapsed < timeoutMs) {
    controller.advance(16)
    elapsed += 16
  }
  expect(predicate()).toBe(true)
}

describe('MarkerLifecycleController', () => {
  it('does not create cold-start flow families from stale activity', () => {
    const adapter = new SimulationAdapter()
    const base = new QueueFlowStateDeriver().derive(adapter.getSnapshot(), 0)
    adapter.dispose()
    const snapshot: LoadgenSnapshot = {
      ...base,
      runState: 'running',
      queue1: {
        ...base.queue1,
        depthBatches: 0,
        flowState: 'normal',
        inputBatchesPerSecond: 10,
        outputBatchesPerSecond: 0,
        throughputTps: 0,
        handoffBatches: 7,
      },
      queue2: {
        ...base.queue2,
        depthBatches: 0,
        flowState: 'normal',
        inputBatchesPerSecond: 8,
        outputBatchesPerSecond: 6,
        throughputTps: 0,
        handoffBatches: 5,
      },
      http: { ...base.http, inFlightRequests: 3 },
    }

    const staleTelemetry = markerTelemetryFromSnapshot(snapshot, false)
    expect(staleTelemetry.queue1.dequeueActive).toBe(false)
    expect(staleTelemetry.queue2.dequeueActive).toBe(false)
    expect(flow(new MarkerLifecycleController(staleTelemetry))).toHaveLength(0)

    const beforeRestart = telemetry({
      queue1: { depthBatches: 0 },
      queue2: { depthBatches: 0 },
      http: { inFlightRequests: 3, requestsStartedTotal: 20 },
    })
    const restarted = new MarkerLifecycleController(beforeRestart)
    expect(flow(restarted)).toHaveLength(MIN_FLOW_MARKERS)
    restarted.reconcile(telemetry({
      queue1: {
        depthBatches: 0,
        throughputTps: 0,
        dequeueActive: false,
        enqueuedBatchesTotal: 0,
        dequeuedBatchesTotal: 0,
      },
      queue2: {
        depthBatches: 0,
        throughputTps: 0,
        dequeueActive: false,
        enqueuedBatchesTotal: 0,
        dequeuedBatchesTotal: 0,
      },
      http: { inFlightRequests: 99 },
    }))
    expect(flow(restarted)).toHaveLength(0)
  })

  it('hydrates queue depth as families and adds cadence families on start', () => {
    const idle = telemetry({
      runState: 'idle',
      queue1: { throughputTps: 0, dequeueActive: false },
      queue2: { throughputTps: 0, dequeueActive: false },
    })
    const controller = new MarkerLifecycleController(idle)
    const idleFamilies = flow(controller).map(markerPosition)

    expect(familiesAtStage(controller, 'queue1')).toHaveLength(4)
    expect(familiesAtStage(controller, 'queue2')).toHaveLength(24)
    expect(flow(controller)).toHaveLength(28)
    expect(flow(controller).every((marker) => marker.familyId !== null)).toBe(true)

    controller.reconcile(telemetry())

    expect(flow(controller).filter((marker) =>
      idleFamilies.some((before) => before.slotId === marker.slotId)
    ).map(markerPosition)).toEqual(idleFamilies)
    expect(flow(controller)).toHaveLength(28 + MIN_FLOW_MARKERS)
    expect(flow(controller).every((marker) => marker.familyId !== null)).toBe(true)
  })

  it('moves a q2 flow family every frame for three seconds at 70 px/s', () => {
    const controller = new MarkerLifecycleController(telemetry())
    const tracked = flow(controller).find((marker) => marker.stage === 'queue2')!
    const familyId = tracked.familyId!
    let previousDistance = routeDistance(tracked)
    let traveled = 0

    for (let elapsed = 0; elapsed < 3_000; elapsed += 16) {
      const frameMs = Math.min(16, 3_000 - elapsed)
      controller.advance(frameMs)
      const current = markerByFamily(controller, familyId)
      const currentDistance = routeDistance(current)
      const frameDistance = currentDistance - previousDistance

      expect(current.stage).toBe('queue2')
      expect(frameDistance).toBeGreaterThan(0)
      expect(frameDistance).toBeCloseTo(70 * frameMs / 1_000, 8)
      previousDistance = currentDistance
      traveled += frameDistance
    }

    expect(traveled).toBeCloseTo(210, 8)
  })

  it('freezes queue families in FIFO order when dequeue stops', () => {
    const stopped = telemetry({
      queue1: {
        throughputTps: 0,
        dequeueActive: false,
        blocked: true,
      },
    })
    const controller = new MarkerLifecycleController(stopped)
    const before = familiesAtStage(controller, 'queue1')
      .map(({ slotId, phase }) => ({ slotId, phase }))
      .sort((left, right) => right.phase - left.phase)

    for (let elapsed = 0; elapsed < 5_000; elapsed += 16) {
      controller.advance(16)
    }

    const after = familiesAtStage(controller, 'queue1')
      .map(({ slotId, phase }) => ({ slotId, phase }))
      .sort((left, right) => right.phase - left.phase)
    expect(after).toEqual(before)
    expect(after.map(({ phase }) => phase)).toEqual([0.875, 0.625, 0.375, 0.125])
  })

  it('keeps an admitted family moving when q2 throughput reaches zero', () => {
    const controller = new MarkerLifecycleController(telemetry())
    const tracked = flow(controller).find((marker) => marker.stage === 'queue2')!
    const familyId = tracked.familyId!
    const beforeDistance = routeDistance(tracked)
    controller.reconcile(telemetry({
      queue2: { throughputTps: 0, dequeueActive: false, blocked: true },
    }))

    controller.advance(16)
    const advanced = markerByFamily(controller, familyId)
    expect(advanced.familyId).toBe(familyId)
    expect(routeDistance(advanced) - beforeDistance).toBeCloseTo(1.12, 8)
    expect(advanced.queued).toBe(false)
  })

  it('drains an in-queue family at zero throughput while run remains active', () => {
    const controller = new MarkerLifecycleController(telemetry())
    const tracked = flow(controller).find((marker) => marker.stage === 'queue2')!
    const familyId = tracked.familyId!
    controller.advance(160)
    const moving = markerByFamily(controller, familyId)
    expect(moving.stage).toBe('queue2')

    controller.reconcile(telemetry({
      queue2: { throughputTps: 0, dequeueActive: true, blocked: false },
      http: { inFlightRequests: 3 },
    }))
    const beforeDrain = routeDistance(markerByFamily(controller, familyId))
    controller.advance(4_000)
    const drained = markerByFamily(controller, familyId)
    expect(drained.familyId).toBe(familyId)
    expect(routeDistance(drained)).toBeGreaterThan(beforeDrain)
    expect(drained.queued).toBe(false)
  })

  it('holds pre-valve FIFO while finite downstream families drain at zero', () => {
    const closed = telemetry({
      valveOpeningIndex: 0,
      queue2: { depthBatches: 4, appliedCapacity: 4 },
    })
    const controller = new MarkerLifecycleController(closed)
    advanceUntil(controller, () => waitingAtValve(controller).length >= 2)
    const waitingBefore = waitingAtValve(controller).map((marker) => ({
      familyId: marker.familyId!,
      phase: marker.phase,
    }))
    const downstreamIds = new Set(flow(controller)
      .filter((marker) => MARKER_STAGE_ORDER.indexOf(marker.stage) >
        MARKER_STAGE_ORDER.indexOf('throttler'))
      .map((marker) => marker.familyId!))
    expect(downstreamIds.size).toBeGreaterThan(0)

    const stopped = (succeededTotal = 0) => telemetry({
      valveOpeningIndex: 0,
      queue1: { throughputTps: 0, dequeueActive: false },
      queue2: {
        depthBatches: 0,
        appliedCapacity: 4,
        throughputTps: 0,
        dequeueActive: false,
      },
      http: {
        requestsCompletedTotal: succeededTotal,
        requestsSucceededTotal: succeededTotal,
      },
    })
    controller.reconcile(stopped())
    advanceUntil(controller, () => flow(controller).every(
      (marker) => marker.stage !== 'queue2',
    ), 20_000)
    expect(flow(controller).filter((marker) => marker.stage === 'queue2'))
      .toHaveLength(0)
    const waitingAfterDrain = waitingBefore.map(({ familyId }) =>
      markerByFamily(controller, familyId)
    )
    expect(waitingAfterDrain.map((marker) => marker.familyId))
      .toEqual(waitingBefore.map(({ familyId }) => familyId))
    expect(waitingAfterDrain.every((marker) => marker.queued)).toBe(true)
    expect(waitingAfterDrain[0].phase).toBe(waitingBefore[0].phase)
    expect(waitingAfterDrain[1].phase).toBeLessThan(waitingAfterDrain[0].phase)

    let succeededTotal = 0
    for (let elapsed = 0; elapsed < 60_000; elapsed += 100) {
      const ready = flow(controller).filter((marker) =>
        downstreamIds.has(marker.familyId!) &&
        marker.stage === 'target' &&
        marker.phase === 1 &&
        marker.outcome === null
      )
      if (ready.length > 0) {
        succeededTotal += ready.length
        controller.reconcile(stopped(succeededTotal))
      }
      controller.advance(100)
      if (![...downstreamIds].some((familyId) =>
        flow(controller).some((marker) => marker.familyId === familyId)
      )) break
    }
    expect([...downstreamIds].some((familyId) =>
      flow(controller).some((marker) => marker.familyId === familyId)
    )).toBe(false)
    expect(flow(controller).filter((marker) => marker.stage === 'queue2'))
      .toHaveLength(0)
  })

  it('freezes retiring families only for explicit pause', () => {
    const controller = new MarkerLifecycleController(telemetry())
    controller.advance(160)
    controller.reconcile(telemetry({ queue1: { depthBatches: 2 } }))
    const retiringBeforeZero = visible(controller)
      .filter((marker) => marker.state === 'retiring')
      .map((marker) => ({
        familyId: marker.familyId,
        state: marker.state,
        stage: marker.stage,
        phase: marker.phase,
      }))
    expect(retiringBeforeZero).toHaveLength(2)

    controller.reconcile(telemetry({
      runState: 'paused',
      queue1: { depthBatches: 2, throughputTps: 0, dequeueActive: false },
      queue2: { throughputTps: 0, dequeueActive: false },
    }))
    controller.advance(4_000)
    const paused = visible(controller)
      .filter((marker) => marker.state === 'retiring')
      .map((marker) => ({
        familyId: marker.familyId,
        state: marker.state,
        stage: marker.stage,
        phase: marker.phase,
      }))
    expect(paused).toEqual(retiringBeforeZero)

    controller.reconcile(telemetry({
      queue1: { depthBatches: 2, throughputTps: 0, dequeueActive: false },
      queue2: { throughputTps: 0, dequeueActive: false },
    }))
    controller.advance(1_000)
    expect(visible(controller).some((marker) =>
      paused.some((before) => before.familyId === marker.familyId)
    )).toBe(false)
  })

  it('preserves flow family IDs and exact positions across pause and resume', () => {
    const controller = new MarkerLifecycleController(telemetry())
    controller.advance(160)
    controller.reconcile(telemetry({ runState: 'paused' }))
    const paused = flow(controller).map(({ slotId, familyId, stage, phase }) => ({
      slotId,
      familyId,
      stage,
      phase,
    }))

    controller.advance(3_000)
    controller.reconcile(telemetry({
      runState: 'paused',
      queue1: { throughputTps: 125_000 },
      queue2: { throughputTps: 125_000 },
    }))
    expect(flow(controller).map(({ slotId, familyId, stage, phase }) => ({
      slotId,
      familyId,
      stage,
      phase,
    }))).toEqual(paused)

    controller.reconcile(telemetry())
    const tracked = paused.find((marker) => marker.stage === 'queue2')!
    const beforeDistance = routeDistance(markerByFamily(controller, tracked.familyId!))
    controller.advance(16)
    const resumed = markerByFamily(controller, tracked.familyId!)
    expect(routeDistance(resumed) - beforeDistance).toBeCloseTo(1.12, 8)
  })

  it('crosses the valve endpoint without coordinate or speed discontinuity', () => {
    const controller = new MarkerLifecycleController(telemetry())
    const tracked = flow(controller).find((marker) => marker.stage === 'queue1')!
    const familyId = tracked.familyId!
    let previous = markerByFamily(controller, familyId)

    while (previous.stage === 'queue1') {
      const previousDistance = routeDistance(previous)
      controller.advance(16)
      const current = markerByFamily(controller, familyId)
      expect(routeDistance(current) - previousDistance).toBeCloseTo(1.12, 8)
      previous = current
    }

    expect(previous.stage).toBe('throttler')
    const queueEnd = getMarkerStagePathGeometry(
      'queue1',
      Q1_CONTROL,
      Q2_CONTROL,
    ).end
    const valveStart = getMarkerStagePathGeometry(
      'throttler',
      Q1_CONTROL,
      Q2_CONTROL,
    ).start
    expect(queueEnd).toEqual(valveStart)
  })

  it('holds pre-valve families in FIFO and releases them without changing identity', () => {
    const controller = new MarkerLifecycleController(telemetry())
    controller.reconcile(telemetry({ valveOpeningIndex: 0 }))

    advanceUntil(controller, () => waitingAtValve(controller).length >= 2, 60_000)
    const waiting = waitingAtValve(controller)
      .sort((left, right) => right.phase - left.phase)
      .slice(0, 2)
      .map((marker) => ({ familyId: marker.familyId!, phase: marker.phase }))

    expect(waiting[1].phase).toBeLessThan(waiting[0].phase)
    controller.advance(2_000)
    const held = waiting.map(({ familyId }) => markerByFamily(controller, familyId))
    expect(held.map((marker) => marker.familyId))
      .toEqual(waiting.map(({ familyId }) => familyId))
    expect(held.every((marker) => marker.queued)).toBe(true)
    expect(held[0].phase).toBe(waiting[0].phase)
    expect(held[1].phase).toBeGreaterThanOrEqual(waiting[1].phase)
    expect(held[1].phase).toBeLessThan(held[0].phase)

    const downstream = flow(controller).find((marker) => marker.stage === 'queue2')!
    const downstreamBefore = routeDistance(downstream)
    controller.advance(16)
    expect(routeDistance(markerByFamily(controller, downstream.familyId!)))
      .toBeGreaterThan(downstreamBefore)

    controller.reconcile(telemetry({ valveOpeningIndex: 11 }))
    const atOpen = waiting.map(({ familyId }) =>
      markerByFamily(controller, familyId).phase
    )
    controller.advance(16)
    const released = waiting.map(({ familyId }) => markerByFamily(controller, familyId))
    expect(released.map((marker) => marker.familyId))
      .toEqual(waiting.map(({ familyId }) => familyId))
    expect(released[0].phase).toBeGreaterThan(atOpen[0])
    expect(released[1].phase).toBeGreaterThan(atOpen[1])
  })

  it('uses applied opening for bounded monotonic FIFO admission cadence', () => {
    const prepare = () => {
      const controller = new MarkerLifecycleController(telemetry({
        valveOpeningIndex: 0,
        queue2: { depthBatches: 0 },
      }))
      const backlogFamilies = new Set(
        familiesAtStage(controller, 'queue1')
          .slice(0, getQueueDepthFamilyTarget(4, 4))
          .map((marker) => marker.familyId),
      )
      advanceUntil(controller, () => [...backlogFamilies].every((familyId) => {
        const marker = markerByFamily(controller, familyId!)
        return marker.stage === 'throttler' && marker.queued
      }), 60_000)
      return { controller, backlogFamilies }
    }
    const releasedCount = (
      controller: MarkerLifecycleController,
      backlogFamilies: ReadonlySet<string | null>,
    ) => [...backlogFamilies].filter((familyId) => {
      const marker = markerByFamily(controller, familyId!)
      return marker.stage !== 'throttler' || !marker.queued
    }).length

    const closed = prepare()
    expect(waitingAtValve(closed.controller).filter((marker) =>
      closed.backlogFamilies.has(marker.familyId)
    )).toHaveLength(getQueueDepthFamilyTarget(4, 4))
    const waitingFamily = waitingAtValve(closed.controller)[0]
    const jitterBefore = markerMicroJitter(
      waitingFamily.familyId!,
      waitingFamily.slotId,
    )
    closed.controller.advance(5_000)
    expect(releasedCount(closed.controller, closed.backlogFamilies)).toBe(0)
    expect(markerMicroJitter(waitingFamily.familyId!, waitingFamily.slotId))
      .toEqual(jitterBefore)
    const movingOffset = markerWaitingOffset(
      waitingFamily.familyId!,
      waitingFamily.slotId,
      500,
      false,
    )
    expect(markerWaitingOffset(
      waitingFamily.familyId!,
      waitingFamily.slotId,
      500,
      false,
    )).toEqual(movingOffset)
    expect(markerWaitingOffset(
      waitingFamily.familyId!,
      waitingFamily.slotId,
      900,
      false,
    )).not.toEqual(movingOffset)
    expect(Math.abs(movingOffset.x)).toBeLessThanOrEqual(2.45)
    expect(Math.abs(movingOffset.y)).toBeLessThanOrEqual(1.65)
    expect(markerWaitingOffset(
      waitingFamily.familyId!,
      waitingFamily.slotId,
      900,
      true,
    )).toEqual({ x: 0, y: 0 })

    const releases = [1, 5, 10].map((valveOpeningIndex) => {
      const partial = prepare()
      partial.controller.reconcile(telemetry({
        valveOpeningIndex,
        queue2: { depthBatches: 0 },
      }))
      for (let elapsed = 0; elapsed < 600; elapsed += 16) {
        partial.controller.advance(Math.min(16, 600 - elapsed))
      }
      return {
        ...partial,
        count: releasedCount(partial.controller, partial.backlogFamilies),
      }
    })
    expect(releases.map(({ count }) => count)).toEqual([0, 1, 3])
    expect([
      valveAdmissionIntervalMs(1),
      valveAdmissionIntervalMs(5),
      valveAdmissionIntervalMs(10),
    ]).toEqual([900, 553.3333333333334, 120])

    const oneAtATime = prepare()
    oneAtATime.controller.reconcile(telemetry({
      valveOpeningIndex: 10,
      queue2: { depthBatches: 0 },
    }))
    oneAtATime.controller.advance(600)
    expect(releasedCount(oneAtATime.controller, oneAtATime.backlogFamilies))
      .toBe(1)
    oneAtATime.controller.advance(1)
    expect(releasedCount(oneAtATime.controller, oneAtATime.backlogFamilies))
      .toBe(1)

    const fullyOpen = prepare()
    fullyOpen.controller.reconcile(telemetry({
      valveOpeningIndex: 11,
      queue2: { depthBatches: 0 },
    }))
    const openBefore = new Map([...fullyOpen.backlogFamilies].map((familyId) => [
      familyId,
      markerByFamily(fullyOpen.controller, familyId!).phase,
    ]))
    fullyOpen.controller.advance(16)
    expect([...fullyOpen.backlogFamilies].every((familyId) =>
      markerByFamily(fullyOpen.controller, familyId!).phase >
        openBefore.get(familyId)!
    )).toBe(true)

    const released = releases[1]
    const tracked = [...released.backlogFamilies]
      .map((familyId) => markerByFamily(released.controller, familyId!))
      .find((marker) => marker.stage !== 'throttler' || !marker.queued)!
    const before = markerPosition(tracked)
    released.controller.advance(16)
    const after = markerByFamily(released.controller, tracked.familyId!)
    expect(after.stage).toBe(before.stage)
    expect((after.phase - before.phase) *
      getValveMarkerPathGeometry(5).length).toBeCloseTo(1.12, 8)
  })

  it('ignores preview and pending when projecting applied valve admission', () => {
    const adapter = new SimulationAdapter()
    const base = new QueueFlowStateDeriver().derive(adapter.getSnapshot(), 0)
    adapter.dispose()
    const withCandidate: LoadgenSnapshot = {
      ...base,
      throttler: {
        ...base.throttler,
        requestedTps: {
          ...base.throttler.requestedTps,
          applied: 0,
          preview: 250_000,
          pending: 225_000,
        },
      },
    }

    expect(markerTelemetryFromSnapshot(withCandidate, false).valveOpeningIndex)
      .toBe(0)
    expect(markerTelemetryFromSnapshot({
      ...withCandidate,
      throttler: {
        ...withCandidate.throttler,
        requestedTps: {
          ...withCandidate.throttler.requestedTps,
          applied: 135_000,
          preview: 0,
          pending: 0,
        },
      },
    }, false).valveOpeningIndex).toBe(6)

    const pendingBypass: LoadgenSnapshot = {
      ...withCandidate,
      throttler: {
        ...withCandidate.throttler,
        installationMode: {
          ...withCandidate.throttler.installationMode,
          applied: 'installed',
          pending: 'bypass',
        },
      },
    }
    expect(markerTelemetryFromSnapshot(pendingBypass, false).valveOpeningIndex)
      .toBe(0)
    expect(markerTelemetryFromSnapshot({
      ...pendingBypass,
      throttler: {
        ...pendingBypass.throttler,
        installationMode: {
          ...pendingBypass.throttler.installationMode,
          applied: 'bypass',
          pending: null,
        },
      },
    }, false).valveOpeningIndex).toBe(11)
  })

  it('freezes family identity and marker paths across paused mode changes', () => {
    const controller = new MarkerLifecycleController(telemetry({
      valveOpeningIndex: 0,
    }))
    advanceUntil(controller, () => waitingAtValve(controller).length >= 2, 60_000)
    controller.reconcile(telemetry({
      runState: 'paused',
      valveOpeningIndex: 0,
    }))
    const before = new Map(visible(controller).map((marker) => [
      marker.familyId,
      markerPosition(marker),
    ]))

    controller.reconcile(telemetry({
      runState: 'paused',
      valveOpeningIndex: 11,
    }))
    controller.advance(1_000)

    expect(controller.getSnapshot().valveOpeningIndex).toBe(0)
    expect(new Map(visible(controller).map((marker) => [
      marker.familyId,
      markerPosition(marker),
    ]))).toEqual(before)

    controller.reconcile(telemetry({ valveOpeningIndex: 11 }))
    expect(controller.getSnapshot().valveOpeningIndex).toBe(11)
    expect(visible(controller).every((marker) =>
      marker.familyId !== null && before.has(marker.familyId)
    )).toBe(true)
  })

  it('keeps exactly 60 fixed slots with unique active family IDs', () => {
    const controller = new MarkerLifecycleController(telemetry({
      queue1: { throughputTps: 500_000 },
      queue2: { throughputTps: 500_000 },
    }))
    const slots = controller.getSnapshot().markers
    const families = flow(controller).map((marker) => marker.familyId!)

    expect(MAX_PIPELINE_MARKERS).toBe(60)
    expect(slots).toHaveLength(MAX_PIPELINE_MARKERS)
    expect(new Set(slots.map((marker) => marker.slotId)).size).toBe(slots.length)
    expect(new Set(families).size).toBe(families.length)
    expect(families.every(Boolean)).toBe(true)
    expect(slots.filter((marker) => marker.state !== 'inactive').every(
      (marker) => marker.familyId !== null,
    )).toBe(true)
    expect(flow(controller)).toHaveLength(4 + 24 + MAX_FLOW_MARKERS)
    expect(getQueueDepthFamilyTarget(100, 100))
      .toBe(MAX_QUEUE_DEPTH_FAMILIES)
    expect(getFlowMarkerTarget(1)).toBe(MIN_FLOW_MARKERS)
    expect(getFlowMarkerTarget(500_000)).toBe(MAX_FLOW_MARKERS)
  })

  it('keeps target outcome on the arrived flow slot and recycles after the pulse', () => {
    const controller = new MarkerLifecycleController(telemetry())
    advanceUntil(controller, () => flow(controller).some((marker) =>
      marker.stage === 'target' && marker.phase === 1
    ))
    const tracked = flow(controller).find((marker) =>
      marker.stage === 'target' && marker.phase === 1
    )!
    const familyId = tracked.familyId!
    const slotId = tracked.slotId

    controller.reconcile(telemetry({
      http: {
        requestsCompletedTotal: 1,
        requestsSucceededTotal: 1,
      },
    }))
    expect(markerByFamily(controller, familyId)).toMatchObject({
      slotId,
      stage: 'target',
      outcome: 'success',
      outcomeVisible: true,
    })

    controller.advance(520)
    const recycled = controller.getSnapshot().markers.find(
      (marker) => marker.slotId === slotId,
    )!
    expect(recycled).toMatchObject({
      stage: 'reader',
      phase: 0,
      outcome: null,
    })
    expect(recycled.familyId).not.toBe(familyId)
  })

  it('services only the bounded FIFO after overflow outcomes arrive', () => {
    const controller = new MarkerLifecycleController(telemetry({
      runState: 'idle',
      queue1: { throughputTps: 0, dequeueActive: false },
      queue2: { throughputTps: 0, dequeueActive: false },
    }))
    const familiesBefore = flow(controller).map(markerPosition)

    controller.reconcile(telemetry({
      runState: 'idle',
      queue1: { throughputTps: 0, dequeueActive: false },
      queue2: { throughputTps: 0, dequeueActive: false },
      http: {
        requestsCompletedTotal: 30,
        requestsFailedTotal: 30,
      },
    }))
    controller.reconcile(telemetry({
      runState: 'idle',
      queue1: { throughputTps: 0, dequeueActive: false },
      queue2: { throughputTps: 0, dequeueActive: false },
      http: {
        requestsCompletedTotal: MAX_PENDING_OUTCOMES + 10,
        requestsSucceededTotal: MAX_PENDING_OUTCOMES - 20,
        requestsFailedTotal: 30,
      },
    }))

    expect(flow(controller)).toHaveLength(28)
    expect(visible(controller).filter((marker) => marker.stage === 'target'))
      .toHaveLength(0)
    expect(flow(controller).map(markerPosition)).toEqual(familiesBefore)

    controller.reconcile(telemetry({
      http: {
        requestsCompletedTotal: MAX_PENDING_OUTCOMES + 10,
        requestsSucceededTotal: MAX_PENDING_OUTCOMES - 20,
        requestsFailedTotal: 30,
      },
    }))
    expect(flow(controller)).toHaveLength(28 + MIN_FLOW_MARKERS)

    const servicedFamilies = new Set<string>()
    const servicedOutcomes: string[] = []
    for (
      let elapsed = 0;
      elapsed < 600_000 && servicedFamilies.size < MAX_PENDING_OUTCOMES;
      elapsed += 100
    ) {
      controller.advance(100)
      for (const marker of flow(controller)) {
        if (
          marker.familyId !== null &&
          marker.outcomeVisible &&
          !servicedFamilies.has(marker.familyId)
        ) {
          expect(marker.stage).toBe('target')
          servicedFamilies.add(marker.familyId)
          servicedOutcomes.push(marker.outcome!)
        }
      }
    }

    expect(servicedFamilies).toHaveLength(MAX_PENDING_OUTCOMES)
    expect(servicedOutcomes.slice(0, 30).every((outcome) => outcome === 'error'))
      .toBe(true)
    expect(servicedOutcomes.slice(30).every((outcome) => outcome === 'success'))
      .toBe(true)

    for (let elapsed = 0; elapsed < 60_000; elapsed += 100) {
      controller.advance(100)
      for (const marker of flow(controller)) {
        if (marker.familyId !== null && marker.outcomeVisible) {
          servicedFamilies.add(marker.familyId)
        }
      }
    }
    expect(servicedFamilies).toHaveLength(MAX_PENDING_OUTCOMES)
    expect(flow(controller).filter((marker) => marker.outcomeVisible))
      .toHaveLength(0)
  })

  it('carries one family through production geometry, q1 wait, outcome, and recycle', () => {
    let succeededTotal = 0
    const withTotals = (
      queue1: Partial<QueueMarkerTelemetry> = {},
      queue2: Partial<QueueMarkerTelemetry> = {},
    ) => telemetry({
      queue1,
      queue2,
      http: {
        requestsCompletedTotal: succeededTotal,
        requestsSucceededTotal: succeededTotal,
      },
    })
    const controller = new MarkerLifecycleController(withTotals())
    const seed = flow(controller)
      .slice()
      .sort((left, right) => routeDistance(right) - routeDistance(left))[0]
    const slotId = seed.slotId

    advanceUntil(controller, () => {
      const marker = controller.getSnapshot().markers.find(
        (item) => item.slotId === slotId,
      )!
      return marker.stage === 'target' && marker.phase === 1
    })
    while (markerByFamily(controller, seed.familyId!).outcome === null) {
      succeededTotal += 1
      controller.reconcile(withTotals())
    }
    controller.advance(520)

    const fresh = controller.getSnapshot().markers.find(
      (marker) => marker.slotId === slotId,
    )!
    const familyId = fresh.familyId!
    expect(fresh).toMatchObject({ stage: 'reader', phase: 0 })
    expect(familyId).not.toBe(seed.familyId)

    const seenStages: MarkerStage[] = ['reader']
    const recordStage = () => {
      const stage = markerByFamily(controller, familyId).stage
      if (seenStages.at(-1) !== stage) seenStages.push(stage)
    }
    const advanceContinuously = () => {
      const before = markerByFamily(controller, familyId)
      const beforeDistance = routeDistance(before)
      controller.advance(16)
      const after = markerByFamily(controller, familyId)
      recordStage()
      const traveled = routeDistance(after) - beforeDistance
      if (after.stage === 'target' && after.phase === 1) {
        expect(traveled).toBeGreaterThan(0)
        expect(traveled).toBeLessThanOrEqual(1.12000001)
      } else {
        expect(traveled).toBeCloseTo(1.12, 8)
      }
    }

    while (markerByFamily(controller, familyId).stage === 'reader') {
      advanceContinuously()
    }
    while (
      markerByFamily(controller, familyId).stage === 'queue1' &&
      markerByFamily(controller, familyId).phase < 0.25
    ) {
      advanceContinuously()
    }

    controller.reconcile(withTotals(
      { throughputTps: 0, dequeueActive: false, blocked: true },
    ))
    const q1Frozen = markerByFamily(controller, familyId)
    controller.advance(2_000)
    expect(markerByFamily(controller, familyId)).toMatchObject({
      stage: 'queue1',
      phase: q1Frozen.phase,
      familyId,
    })
    controller.reconcile(withTotals())
    advanceContinuously()

    while (markerByFamily(controller, familyId).stage === 'queue1') {
      advanceContinuously()
    }

    while (true) {
      const tracked = markerByFamily(controller, familyId)
      if (tracked.stage === 'target' && tracked.phase === 1) break

      const waitingAhead = flow(controller).find((marker) =>
        marker.familyId !== familyId &&
        marker.stage === 'target' &&
        marker.phase === 1 &&
        marker.outcome === null
      )
      if (waitingAhead !== undefined) {
        succeededTotal += 1
        controller.reconcile(withTotals())
      }
      advanceContinuously()
    }

    expect(seenStages).toEqual(MARKER_STAGE_ORDER)
    expect(markerByFamily(controller, familyId).outcome).toBeNull()
    while (markerByFamily(controller, familyId).outcome === null) {
      succeededTotal += 1
      controller.reconcile(withTotals())
    }
    expect(markerByFamily(controller, familyId)).toMatchObject({
      slotId,
      stage: 'target',
      outcome: 'success',
      outcomeVisible: true,
    })

    controller.advance(520)
    const recycled = controller.getSnapshot().markers.find(
      (marker) => marker.slotId === slotId,
    )!
    expect(recycled).toMatchObject({
      stage: 'reader',
      phase: 0,
      outcome: null,
    })
    expect(recycled.familyId).not.toBe(familyId)
    expect(flow(controller)).toHaveLength(28 + MIN_FLOW_MARKERS)
    expect(controller.getSnapshot().markers).toHaveLength(MAX_PIPELINE_MARKERS)
  })

  it('reconciles depth-driven family cardinality without recreating survivors', () => {
    const controller = new MarkerLifecycleController(telemetry())
    const initial = flow(controller)

    controller.reconcile(telemetry({ queue1: { depthBatches: 2 } }))
    const reduced = flow(controller)
    const retiring = visible(controller).filter((marker) =>
      marker.state === 'retiring'
    )
    expect(reduced).toHaveLength(2 + 24 + MIN_FLOW_MARKERS)
    expectFlowSurvivors(initial, reduced)
    expect(retiring).toHaveLength(2)
    expect(retiring.every((marker) => marker.familyId !== null)).toBe(true)
  })

  it('preserves family identity and phase when capacity changes depth density', () => {
    const controller = new MarkerLifecycleController(telemetry({
      queue2: { depthBatches: 50, appliedCapacity: 50 },
    }))
    const before = flow(controller)

    controller.reconcile(telemetry({
      queue2: { depthBatches: 50, appliedCapacity: 100 },
    }))
    const after = flow(controller)

    expect(after).toHaveLength(4 + 12 + MIN_FLOW_MARKERS)
    expectFlowSurvivors(before, after)
    expect(after.every((marker) => marker.familyId !== null)).toBe(true)
  })

  it('uses semantic downstream steps and no smooth waiting motion when reduced', () => {
    const controller = new MarkerLifecycleController(telemetry({
      reducedMotion: true,
    }))
    const before = flow(controller).map(({ slotId, familyId, stage, phase }) => ({
      slotId,
      familyId,
      stage,
      phase,
    }))
    const upstreamBefore = before.filter((marker) =>
      marker.stage === 'reader' || marker.stage === 'queue1'
    )
    controller.advance(10_000)
    const after = flow(controller).map(({ slotId, familyId, stage, phase }) => ({
      slotId,
      familyId,
      stage,
      phase,
    }))
    expect(after.filter((marker) => upstreamBefore.some(
      (upstream) => upstream.familyId === marker.familyId,
    ))).toEqual(upstreamBefore)
    expect(after.some((marker) => {
      const previous = before.find((item) => item.familyId === marker.familyId)
      return previous !== undefined &&
        previous.stage !== 'reader' &&
        previous.stage !== 'queue1' &&
        (previous.stage !== marker.stage || previous.phase !== marker.phase)
    })).toBe(true)
    expect(controller.getSnapshot().motionElapsedMs).toBe(0)

    controller.reconcile(telemetry({
      reducedMotion: true,
      http: { requestsCompletedTotal: 1, requestsSucceededTotal: 1 },
    }))
    const outcome = flow(controller).find((marker) => marker.outcome !== null)!
    expect(after.some((marker) => marker.familyId === outcome.familyId)).toBe(true)
    expect(outcome).toMatchObject({
      stage: 'target',
      phase: 1,
      outcome: 'success',
      outcomeVisible: true,
    })

    controller.advance(520)
    const recycled = controller.getSnapshot().markers.find(
      (marker) => marker.slotId === outcome.slotId,
    )!
    expect(recycled).toMatchObject({ stage: 'reader', phase: 0, outcome: null })
    expect(recycled.familyId).not.toBe(outcome.familyId)
  })

  it('covers null, zero, low, mid, and max throughput density', () => {
    expect([
      getFlowMarkerTarget(null),
      getFlowMarkerTarget(0),
      getFlowMarkerTarget(1),
      getFlowMarkerTarget(125_000),
      getFlowMarkerTarget(250_000),
      getFlowMarkerTarget(500_000),
    ]).toEqual([0, 0, MIN_FLOW_MARKERS, 6, MAX_FLOW_MARKERS, MAX_FLOW_MARKERS])
  })

  it('preserves exact flow family positions across repeated cardinality revisions', () => {
    const controller = new MarkerLifecycleController(telemetry({
      queue1: { throughputTps: 250_000 },
      queue2: { throughputTps: 250_000 },
    }))
    const revisions = [125_000, 50_000, 250_000, 250_000, 1, 125_000, 250_000]
    for (const throughputTps of revisions) {
      const before = flow(controller)
      controller.reconcile(telemetry({
        queue1: { throughputTps },
        queue2: { throughputTps },
      }))
      const after = flow(controller)
      expect(after).toHaveLength(28 + getFlowMarkerTarget(throughputTps))
      expectFlowSurvivors(before, after)
    }
  })

  it('hydrates only missing transaction families when queue depth grows', () => {
    const controller = new MarkerLifecycleController(telemetry({
      queue2: { depthBatches: 15, appliedCapacity: 100 },
    }))
    const initial = flow(controller)
    controller.reconcile(telemetry({
      queue2: { depthBatches: 50, appliedCapacity: 100 },
    }))
    const grown = flow(controller)

    expect(initial).toHaveLength(4 + 4 + MIN_FLOW_MARKERS)
    expect(grown).toHaveLength(4 + 12 + MIN_FLOW_MARKERS)
    expectFlowSurvivors(initial, grown)
    expect(grown.every((marker) => marker.familyId !== null)).toBe(true)
  })

  it('keeps exact unified family targets and survivors through depth churn', () => {
    const capacity = 24
    const depths = [6, 18, 3, 24, 12, 21, 9, 24]
    const controller = new MarkerLifecycleController(telemetry({
      queue1: { depthBatches: 12, appliedCapacity: capacity },
      queue2: { depthBatches: 18, appliedCapacity: capacity },
    }))

    for (let revision = 1; revision <= 24; revision += 1) {
      const before = flow(controller)
      const queue1Depth = depths[revision % depths.length]
      const queue2Depth = depths[(revision + 3) % depths.length]
      controller.reconcile(telemetry({
        queue1: {
          depthBatches: queue1Depth,
          appliedCapacity: capacity,
          enqueuedBatchesTotal: 100 + revision,
          dequeuedBatchesTotal: 90 + revision,
        },
        queue2: {
          depthBatches: queue2Depth,
          appliedCapacity: capacity,
          enqueuedBatchesTotal: 100 + revision,
          dequeuedBatchesTotal: 90 + revision,
        },
      }))

      const after = flow(controller)
      expect(after).toHaveLength(
        getQueueDepthFamilyTarget(queue1Depth, capacity) +
          getQueueDepthFamilyTarget(queue2Depth, capacity) +
          MIN_FLOW_MARKERS,
      )
      expectFlowSurvivors(before, after)
      expect(new Set(after.map((marker) => marker.familyId)).size)
        .toBe(after.length)
    }
  })

  it('holds a completed outcome until the same HTTP family fully reaches target', () => {
    const controller = new MarkerLifecycleController(telemetry())
    const tracked = flow(controller)
      .slice()
      .sort((left, right) => routeDistance(right) - routeDistance(left))[0]
    const familyId = tracked.familyId!

    advanceUntil(controller, () => {
      const marker = markerByFamily(controller, familyId)
      return marker.stage === 'http' && marker.phase > 0 && marker.phase < 1
    })
    controller.reconcile(telemetry({
      http: { requestsCompletedTotal: 1, requestsSucceededTotal: 1 },
    }))
    expect(markerByFamily(controller, familyId)).toMatchObject({
      stage: 'http',
      outcome: null,
    })

    advanceUntil(controller, () => {
      const marker = markerByFamily(controller, familyId)
      return marker.stage === 'target' && marker.phase > 0 && marker.phase < 1
    })
    expect(markerByFamily(controller, familyId)).toMatchObject({
      familyId,
      stage: 'target',
      outcome: null,
      outcomeVisible: false,
    })

    advanceUntil(controller, () =>
      markerByFamily(controller, familyId).phase === 1
    )
    expect(markerByFamily(controller, familyId)).toMatchObject({
      familyId,
      stage: 'target',
      phase: 1,
      outcome: 'success',
      outcomeVisible: true,
    })
  })

  it('maps HTTP attempts, failures, and connection errors onto real flow families', () => {
    const attempts = new MarkerLifecycleController(telemetry({
      queue1: { depthBatches: 0, throughputTps: 0, dequeueActive: false },
      queue2: { depthBatches: 0, throughputTps: 0, dequeueActive: false },
      http: { inFlightRequests: 99, requestsStartedTotal: 99 },
    }))
    expect(flow(attempts)).toHaveLength(0)

    const controller = new MarkerLifecycleController(telemetry())
    const tracked = flow(controller)
      .slice()
      .sort((left, right) => routeDistance(right) - routeDistance(left))[0]
    const familyId = tracked.familyId!
    advanceUntil(controller, () =>
      markerByFamily(controller, familyId).stage === 'target' &&
      markerByFamily(controller, familyId).phase === 1
    )
    controller.reconcile(telemetry({
      http: { requestsCompletedTotal: 1, requestsFailedTotal: 1 },
    }))
    expect(markerByFamily(controller, familyId)).toMatchObject({
      outcome: 'error',
      outcomeVisible: true,
    })

    const connectionController = new MarkerLifecycleController(telemetry())
    const connectionFamilies = new Set(
      flow(connectionController).map((marker) => marker.familyId),
    )
    connectionController.reconcile(telemetry({
      http: { connectionError: true },
    }))
    advanceUntil(connectionController, () => flow(connectionController).some(
      (marker) => marker.outcomeVisible,
    ))
    const connectionOutcome = flow(connectionController).find(
      (marker) => marker.outcomeVisible,
    )!
    expect(connectionFamilies.has(connectionOutcome.familyId)).toBe(true)
    expect(connectionOutcome).toMatchObject({
      stage: 'target',
      outcome: 'error',
      outcomeVisible: true,
    })
  })
})
