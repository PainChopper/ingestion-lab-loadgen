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
  MarkerLifecycleController,
  MAX_FLOW_MARKERS,
  MAX_PENDING_OUTCOMES,
  MAX_PIPELINE_MARKERS,
  MAX_QUEUE_DEPTH_FAMILIES,
  MIN_FLOW_MARKERS,
} from './markerLifecycle'
import type {
  MarkerLifecycleTelemetry,
  MarkerStage,
  PipelineMarkerSlotSnapshot,
  QueueMarkerTelemetry,
} from './markerLifecycle'
import { getMarkerStagePathGeometry } from './markerPaths'
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
    queue1?: Partial<QueueMarkerTelemetry>
    queue2?: Partial<QueueMarkerTelemetry>
    http?: Partial<MarkerLifecycleTelemetry['http']>
  } = {},
): MarkerLifecycleTelemetry {
  return {
    runState: options.runState ?? 'running',
    reducedMotion: options.reducedMotion ?? false,
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
    stageTravelLengths: PRODUCTION_STAGE_LENGTHS,
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

  it('holds a flow family at the q2 upstream boundary when q2 stops', () => {
    const controller = new MarkerLifecycleController(telemetry())
    const tracked = flow(controller).find((marker) => marker.stage === 'queue1')!
    const familyId = tracked.familyId!
    controller.reconcile(telemetry({
      queue2: { throughputTps: 0, dequeueActive: false, blocked: true },
    }))

    advanceUntil(controller, () => {
      const marker = markerByFamily(controller, familyId)
      return marker.stage === 'throttler' && marker.phase === 1
    })
    const held = markerByFamily(controller, familyId)
    expect(held).toMatchObject({
      stage: 'throttler',
      phase: 1,
      queued: true,
    })

    controller.advance(2_000)
    expect(markerByFamily(controller, familyId)).toMatchObject({
      stage: held.stage,
      phase: held.phase,
      familyId,
    })

    controller.reconcile(telemetry())
    controller.advance(16)
    const resumed = markerByFamily(controller, familyId)
    expect(resumed.familyId).toBe(familyId)
    expect(routeDistance(resumed) - routeDistance(held)).toBeCloseTo(1.12, 8)
  })

  it('freezes an in-queue family by zero throughput despite stale dequeue activity', () => {
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
    const frozen = markerByFamily(controller, familyId)
    controller.advance(4_000)
    expect(markerByFamily(controller, familyId)).toMatchObject({
      familyId,
      stage: frozen.stage,
      phase: frozen.phase,
      queued: true,
    })

    controller.reconcile(telemetry())
    expect(markerByFamily(controller, familyId)).toMatchObject({
      familyId,
      stage: frozen.stage,
      phase: frozen.phase,
    })
    controller.advance(16)
    expect(
      routeDistance(markerByFamily(controller, familyId)) - routeDistance(frozen),
    ).toBeCloseTo(1.12, 8)
  })

  it('freezes every current family at zero throughput and resumes in place', () => {
    const controller = new MarkerLifecycleController(telemetry())
    controller.advance(160)
    const before = flow(controller).map((marker) => ({
      familyId: marker.familyId,
      ...markerPosition(marker),
    }))

    controller.reconcile(telemetry({
      queue1: { throughputTps: 0, dequeueActive: false },
      queue2: { throughputTps: 0, dequeueActive: false },
    }))
    expect(flow(controller).map((marker) => ({
      familyId: marker.familyId,
      ...markerPosition(marker),
    }))).toEqual(before)

    controller.advance(4_000)
    expect(flow(controller).map((marker) => ({
      familyId: marker.familyId,
      ...markerPosition(marker),
    }))).toEqual(before)

    controller.reconcile(telemetry())
    controller.advance(16)
    const resumed = flow(controller)
    expect(resumed.map((marker) => marker.familyId))
      .toEqual(before.map((marker) => marker.familyId))
    expect(resumed.some((marker, index) =>
      marker.stage !== before[index].stage || marker.phase !== before[index].phase
    )).toBe(true)
  })

  it('freezes retiring families beyond grace at zero throughput and resumes retirement', () => {
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
      queue1: { depthBatches: 2, throughputTps: 0, dequeueActive: false },
      queue2: { throughputTps: 0, dequeueActive: false },
    }))
    controller.advance(4_000)
    const frozen = visible(controller)
      .filter((marker) => marker.state === 'retiring')
      .map((marker) => ({
        familyId: marker.familyId,
        state: marker.state,
        stage: marker.stage,
        phase: marker.phase,
      }))
    expect(frozen).toEqual(retiringBeforeZero)

    controller.reconcile(telemetry({ queue1: { depthBatches: 2 } }))
    expect(visible(controller)
      .filter((marker) => marker.state === 'retiring')
      .map((marker) => ({
        familyId: marker.familyId,
        state: marker.state,
        stage: marker.stage,
        phase: marker.phase,
      }))).toEqual(frozen)

    controller.advance(16)
    const resumed = visible(controller)
      .filter((marker) => marker.state === 'retiring')
    expect(resumed.map((marker) => marker.familyId))
      .toEqual(frozen.map((marker) => marker.familyId))
    expect(resumed.some((marker, index) =>
      marker.stage !== frozen[index].stage || marker.phase !== frozen[index].phase
    )).toBe(true)

    controller.advance(984)
    expect(visible(controller).some((marker) =>
      frozen.some((before) => before.familyId === marker.familyId)
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

  it('crosses the barrier endpoint without coordinate or speed discontinuity', () => {
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
    const barrierStart = getMarkerStagePathGeometry(
      'throttler',
      Q1_CONTROL,
      Q2_CONTROL,
    ).start
    expect(queueEnd).toEqual(barrierStart)
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

  it('carries one family through production geometry, boundary waits, outcome, and recycle', () => {
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
    controller.reconcile(withTotals(
      {},
      { throughputTps: 0, dequeueActive: false, blocked: true },
    ))
    while (markerByFamily(controller, familyId).phase < 1) {
      const beforeDistance = routeDistance(markerByFamily(controller, familyId))
      controller.advance(16)
      const traveled =
        routeDistance(markerByFamily(controller, familyId)) - beforeDistance
      recordStage()
      expect(traveled).toBeGreaterThan(0)
      expect(traveled).toBeLessThanOrEqual(1.12000001)
    }
    const q2Boundary = markerByFamily(controller, familyId)
    expect(q2Boundary).toMatchObject({
      stage: 'throttler',
      phase: 1,
      queued: true,
      familyId,
    })
    controller.advance(2_000)
    expect(markerByFamily(controller, familyId)).toMatchObject({
      stage: q2Boundary.stage,
      phase: q2Boundary.phase,
      familyId,
    })
    controller.reconcile(withTotals())

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

  it('restores reduced-motion outcome and recycle lifecycle without travel', () => {
    const controller = new MarkerLifecycleController(telemetry({
      reducedMotion: true,
    }))
    const before = flow(controller).map(({ slotId, familyId, stage, phase }) => ({
      slotId,
      familyId,
      stage,
      phase,
    }))
    controller.advance(10_000)
    expect(flow(controller).map(({ slotId, familyId, stage, phase }) => ({
      slotId,
      familyId,
      stage,
      phase,
    }))).toEqual(before)

    controller.reconcile(telemetry({
      reducedMotion: true,
      http: { requestsCompletedTotal: 1, requestsSucceededTotal: 1 },
    }))
    const outcome = flow(controller).find((marker) => marker.outcome !== null)!
    expect(before.some((marker) => marker.familyId === outcome.familyId)).toBe(true)
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
