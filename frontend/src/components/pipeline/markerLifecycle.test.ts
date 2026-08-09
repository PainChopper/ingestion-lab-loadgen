import { describe, expect, it } from 'vitest'
import type { QueueId, RunState } from '../../model/loadgen'
import {
  MarkerLifecycleController,
  MAX_FLOW_MARKERS,
  MAX_HTTP_ATTEMPT_MARKERS,
  MAX_OCCUPANCY_MARKERS,
  getFlowMarkerTarget,
  getOccupancyMarkerTarget,
} from './markerLifecycle'
import type {
  MarkerLifecycleTelemetry,
  QueueMarkerTelemetry,
} from './markerLifecycle'

function queueTelemetry(
  id: QueueId,
  overrides: Partial<QueueMarkerTelemetry> = {},
): QueueMarkerTelemetry {
  return {
    id,
    depthBatches: 2,
    appliedCapacity: 4,
    throughputTps: 50_000,
    flowActive: true,
    enqueuedBatchesTotal: 10,
    dequeuedBatchesTotal: 8,
    occupancyTravelLength: 400,
    flowTravelLength: 180,
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
      travelLength: 200,
      connectionError: false,
      ...options.http,
    },
  }
}

function visible<T extends { state: string }>(slots: readonly T[]): T[] {
  return slots.filter((slot) => slot.state !== 'inactive')
}

describe('MarkerLifecycleController', () => {
  it('keeps a fixed DOM pool and stable survivor identities and phases', () => {
    const initial = telemetry()
    const controller = new MarkerLifecycleController(initial)
    const before = controller.getSnapshot().queue1
    const activeBefore = before.filter(
      (slot) => slot.kind === 'occupancy' && slot.state === 'active',
    )

    controller.reconcile(initial)
    const same = controller.getSnapshot().queue1
    expect(same).toHaveLength(
      MAX_OCCUPANCY_MARKERS + MAX_FLOW_MARKERS,
    )
    expect(same.map((slot) => slot.slotId)).toEqual(before.map((slot) => slot.slotId))
    expect(
      same.filter((slot) => slot.kind === 'occupancy' && slot.state === 'active')
        .map(({ slotId, phase }) => ({ slotId, phase })),
    ).toEqual(activeBefore.map(({ slotId, phase }) => ({ slotId, phase })))

    controller.reconcile(telemetry({
      queue1: {
        depthBatches: 4,
        occupancyTravelLength: 720,
        flowTravelLength: 260,
      },
    }))
    const grown = controller.getSnapshot().queue1
    for (const survivor of activeBefore) {
      expect(grown.find((slot) => slot.slotId === survivor.slotId)?.phase)
        .toBe(survivor.phase)
    }

    controller.reconcile(telemetry({
      queue1: {
        depthBatches: 1,
        occupancyTravelLength: 280,
        flowTravelLength: 140,
      },
    }))
    const reduced = controller.getSnapshot().queue1
    for (const survivor of activeBefore.slice(0, getOccupancyMarkerTarget(1, 4))) {
      expect(reduced.find((slot) => slot.slotId === survivor.slotId)?.phase)
        .toBe(survivor.phase)
    }
  })

  it('retires excess occupancy only after it reaches downstream', () => {
    const controller = new MarkerLifecycleController(telemetry({
      queue1: { depthBatches: 4, appliedCapacity: 4 },
    }))
    controller.reconcile(telemetry({
      queue1: { depthBatches: 1, appliedCapacity: 4 },
    }))

    const retiring = controller.getSnapshot().queue1.filter(
      (slot) => slot.kind === 'occupancy' && slot.state === 'retiring',
    )
    expect(retiring.length).toBeGreaterThan(0)
    controller.advance(100)
    expect(
      controller.getSnapshot().queue1.filter(
        (slot) => retiring.some((item) => item.slotId === slot.slotId) &&
          slot.state === 'retiring',
      ).length,
    ).toBe(retiring.length)

    controller.advance(4_000)
    expect(
      controller.getSnapshot().queue1.filter(
        (slot) => retiring.some((item) => item.slotId === slot.slotId) &&
          slot.state !== 'inactive',
      ),
    ).toHaveLength(0)
  })

  it('freezes exact phases while paused and continues them on resume', () => {
    const controller = new MarkerLifecycleController(telemetry())
    controller.advance(600)
    const movingSlot = visible(controller.getSnapshot().queue1)
      .find((slot) => slot.kind === 'flow')
    expect(movingSlot).toBeDefined()

    controller.reconcile(telemetry({ runState: 'paused' }))
    const pausedPhase = controller.getSnapshot().queue1
      .find((slot) => slot.slotId === movingSlot?.slotId)?.phase
    controller.advance(3_000)
    expect(controller.getSnapshot().queue1
      .find((slot) => slot.slotId === movingSlot?.slotId)?.phase)
      .toBe(pausedPhase)

    controller.reconcile(telemetry())
    controller.advance(300)
    expect(controller.getSnapshot().queue1
      .find((slot) => slot.slotId === movingSlot?.slotId)?.phase)
      .toBeGreaterThan(pausedPhase ?? 0)
  })

  it('drains existing flow markers at TPS zero and keeps occupancy queued', () => {
    const controller = new MarkerLifecycleController(telemetry())
    controller.advance(500)
    controller.reconcile(telemetry({
      queue1: { flowActive: false },
    }))
    const stopped = controller.getSnapshot().queue1

    expect(stopped.filter((slot) => slot.kind === 'flow' && slot.state === 'retiring').length)
      .toBeGreaterThan(0)
    expect(stopped.filter(
      (slot) => slot.kind === 'occupancy' && slot.state === 'active' && slot.queued,
    ).length)
      .toBe(getOccupancyMarkerTarget(2, 4))

    controller.advance(4_000)
    const drained = controller.getSnapshot().queue1
    expect(drained.filter((slot) => slot.kind === 'flow' && slot.state !== 'inactive'))
      .toHaveLength(0)
    expect(drained.filter((slot) => slot.kind === 'occupancy' && slot.state === 'active'))
      .toHaveLength(getOccupancyMarkerTarget(2, 4))
  })

  it('keeps the same flow slot active as it loops across the full path', () => {
    const controller = new MarkerLifecycleController(telemetry({
      queue1: { throughputTps: 1, flowTravelLength: 190 },
    }))
    const before = controller.getSnapshot().queue1.find(
      (slot) => slot.kind === 'flow' && slot.state === 'active',
    )

    controller.advance(2_500)
    const nearEnd = controller.getSnapshot().queue1.find(
      (slot) => slot.slotId === before?.slotId,
    )
    controller.advance(300)
    const looped = controller.getSnapshot().queue1.find(
      (slot) => slot.slotId === before?.slotId,
    )

    expect(nearEnd?.phase).toBeGreaterThan(0.8)
    expect(looped).toMatchObject({ slotId: before?.slotId, state: 'active' })
    expect(looped?.phase).toBeLessThan(nearEnd?.phase ?? 0)
  })

  it('scales occupancy monotonically by depth and applied capacity', () => {
    expect(getOccupancyMarkerTarget(4, 4)).toBe(4)
    expect(getOccupancyMarkerTarget(4, 12)).toBe(4)
    expect(getOccupancyMarkerTarget(4, 100)).toBe(1)
    expect(getOccupancyMarkerTarget(15, 100)).toBe(4)
    expect(getOccupancyMarkerTarget(50, 100)).toBe(12)
    expect(getOccupancyMarkerTarget(100, 100)).toBe(MAX_OCCUPANCY_MARKERS)
    expect(getOccupancyMarkerTarget(4, 0)).toBe(0)
  })

  it('scales flow density monotonically from measured throughput', () => {
    expect(getFlowMarkerTarget(null)).toBe(0)
    expect(getFlowMarkerTarget(0)).toBe(0)
    expect(getFlowMarkerTarget(5_000)).toBe(1)
    expect(getFlowMarkerTarget(50_000)).toBe(3)
    expect(getFlowMarkerTarget(125_000)).toBe(6)
    expect(getFlowMarkerTarget(250_000)).toBe(MAX_FLOW_MARKERS)
    expect(getFlowMarkerTarget(500_000)).toBe(MAX_FLOW_MARKERS)
  })

  it('bounds retiring occupancy and revives its identities on growth', () => {
    const controller = new MarkerLifecycleController(telemetry({
      queue1: { depthBatches: 100, appliedCapacity: 100 },
    }))
    controller.reconcile(telemetry({
      queue1: { depthBatches: 15, appliedCapacity: 100 },
    }))
    const reduced = controller.getSnapshot().queue1.filter(
      (slot) => slot.kind === 'occupancy' && slot.state !== 'inactive',
    )
    const retiring = reduced.filter((slot) => slot.state === 'retiring')

    expect(reduced).toHaveLength(getOccupancyMarkerTarget(15, 100) + 2)
    expect(retiring).toHaveLength(2)

    controller.reconcile(telemetry({
      queue1: { depthBatches: 50, appliedCapacity: 100 },
    }))
    const grown = controller.getSnapshot().queue1
    for (const previous of retiring) {
      expect(grown.find((slot) => slot.slotId === previous.slotId)).toMatchObject({
        familyId: previous.familyId,
        phase: previous.phase,
        state: 'active',
      })
    }
    expect(grown.filter(
      (slot) => slot.kind === 'occupancy' && slot.state !== 'inactive',
    )).toHaveLength(getOccupancyMarkerTarget(50, 100))
  })

  it('scales flow count by throughput with stable ids and even phases', () => {
    const controller = new MarkerLifecycleController(telemetry({
      queue1: {
        appliedCapacity: 0,
        depthBatches: 0,
        throughputTps: 250_000,
      },
    }))
    const high = controller.getSnapshot().queue1.filter(
      (slot) => slot.kind === 'flow' && slot.state === 'active',
    )

    expect(high).toHaveLength(MAX_FLOW_MARKERS)
    expect(high.map((slot) => slot.phase)).toEqual(
      high.map((_, index) => index / MAX_FLOW_MARKERS),
    )

    controller.reconcile(telemetry({
      queue1: {
        appliedCapacity: 0,
        depthBatches: 0,
        throughputTps: 50_000,
      },
    }))
    const low = controller.getSnapshot().queue1.filter(
      (slot) => slot.kind === 'flow' && slot.state === 'active',
    )

    expect(low).toHaveLength(getFlowMarkerTarget(50_000))
    expect(low.map((slot) => slot.slotId)).toEqual(
      high.slice(0, low.length).map((slot) => slot.slotId),
    )
    for (let index = 1; index < low.length; index += 1) {
      expect(low[index].phase - low[index - 1].phase)
        .toBeCloseTo(1 / low.length)
    }
  })

  it('keeps HTTP attempts until a success or error outcome is shown', () => {
    const controller = new MarkerLifecycleController(telemetry())
    controller.reconcile(telemetry({
      http: { inFlightRequests: 1, requestsStartedTotal: 1 },
    }))
    const attempt = visible(controller.getSnapshot().http)[0]
    expect(attempt).toBeDefined()

    controller.reconcile(telemetry({
      http: {
        requestsStartedTotal: 1,
        requestsCompletedTotal: 1,
        requestsSucceededTotal: 1,
      },
    }))
    expect(controller.getSnapshot().http.find((slot) => slot.slotId === attempt.slotId)?.outcome)
      .toBe('success')

    for (let index = 0; index < 4; index += 1) controller.advance(500)
    const arrived = controller.getSnapshot().http.find(
      (slot) => slot.slotId === attempt.slotId,
    )
    expect(arrived?.outcomeVisible).toBe(true)
    expect(arrived?.state).toBe('retiring')

    controller.advance(600)
    expect(controller.getSnapshot().http.find(
      (slot) => slot.slotId === attempt.slotId,
    )?.state).toBe('inactive')
    expect(controller.getSnapshot().http).toHaveLength(MAX_HTTP_ATTEMPT_MARKERS)
  })

  it('disables travel and pulse while preserving reduced-motion meaning', () => {
    const controller = new MarkerLifecycleController(telemetry({
      reducedMotion: true,
      http: { inFlightRequests: 1 },
    }))
    const queueMarker = visible(controller.getSnapshot().queue1)[0]
    const httpMarker = visible(controller.getSnapshot().http)[0]

    controller.advance(10_000)
    expect(controller.getSnapshot().queue1.find(
      (slot) => slot.slotId === queueMarker.slotId,
    )?.phase).toBe(queueMarker.phase)
    expect(controller.getSnapshot().http.find(
      (slot) => slot.slotId === httpMarker.slotId,
    )?.phase).toBe(httpMarker.phase)
  })
})
