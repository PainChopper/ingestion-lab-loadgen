import { describe, expect, it } from 'vitest'
import { SimulationAdapter } from '../adapters/SimulationAdapter'
import type {
  ConnectionState,
  LoadgenTelemetrySnapshot,
  QueueSnapshot,
  RunState,
} from './loadgen'
import {
  blockingPressure,
  effectiveQueuePressure,
  occupancyPressure,
  QUEUE_PRESSURE_GREEN,
  QUEUE_PRESSURE_RED,
  QUEUE_PRESSURE_YELLOW,
  queuePressureColor,
  QueueFlowStateDeriver,
} from './queueFlowState'

function baseTelemetry(): LoadgenTelemetrySnapshot {
  const adapter = new SimulationAdapter()
  const snapshot = adapter.getSnapshot()
  adapter.dispose()
  return {
    ...snapshot,
    runState: 'running',
    reader: { ...snapshot.reader, state: 'running' },
    throttler: { ...snapshot.throttler, state: 'running' },
    sender: { ...snapshot.sender, state: 'running' },
  }
}

function queueInput(
  snapshot: LoadgenTelemetrySnapshot,
  values: {
    depth?: number | null
    applied?: number | null
    preview?: number | null
    pending?: number | null
    blockedSenders?: number
    oldestBlockedSenderMs?: number
    runState?: RunState
    connectionState?: ConnectionState
  },
): LoadgenTelemetrySnapshot {
  return {
    ...snapshot,
    revision: snapshot.revision + 1,
    runState: values.runState ?? snapshot.runState,
    connectionState: values.connectionState ?? snapshot.connectionState,
    queue1: {
      ...snapshot.queue1,
      depthBatches:
        values.depth === undefined
          ? snapshot.queue1.depthBatches
          : values.depth,
      blockedSenders:
        values.blockedSenders ?? snapshot.queue1.blockedSenders,
      oldestBlockedSenderMs:
        values.oldestBlockedSenderMs ??
        snapshot.queue1.oldestBlockedSenderMs,
      capacity: {
        ...snapshot.queue1.capacity,
        applied:
          values.applied === undefined
            ? snapshot.queue1.capacity.applied
            : values.applied,
        preview:
          values.preview === undefined
            ? snapshot.queue1.capacity.preview
            : values.preview,
        pending:
          values.pending === undefined
            ? snapshot.queue1.capacity.pending
            : values.pending,
      },
    },
  }
}

function deriveSequence(
  deriver: QueueFlowStateDeriver,
  initial: LoadgenTelemetrySnapshot,
  observations: ReadonlyArray<{
    atMs: number
    values: Parameters<typeof queueInput>[1]
  }>,
): QueueSnapshot[] {
  let current = initial
  return observations.map(({ atMs, values }) => {
    current = queueInput(current, values)
    return deriver.derive(current, atMs).queue1
  })
}

describe('queue flow state derivation', () => {
  it('derives pressure only from applied occupancy and current blocking', () => {
    const telemetry = baseTelemetry().queue1

    expect([
      occupancyPressure(0, 20),
      occupancyPressure(10, 20),
      occupancyPressure(25, 20),
      occupancyPressure(0, 0),
      occupancyPressure(null, 20),
    ]).toEqual([0, 0.5, 1, 0, 0])
    expect([0, 10, 100, 300, 500, 900].map((oldest) =>
      blockingPressure(oldest === 0 ? 0 : 1, oldest),
    )).toEqual([0, 0, 0, 0.5, 1, 1])
    expect(effectiveQueuePressure({
      ...telemetry,
      depthBatches: 10,
      blockedSenders: 1,
      oldestBlockedSenderMs: 200,
      capacity: {
        ...telemetry.capacity,
        applied: 20,
        preview: 0,
        pending: 0,
      },
    })).toBe(0.5)
    expect(effectiveQueuePressure({
      ...telemetry,
      depthBatches: 10,
      blockedSenders: 0,
      oldestBlockedSenderMs: 0,
      capacity: {
        ...telemetry.capacity,
        applied: 10,
        preview: 0,
        pending: 0,
      },
    })).toBe(1)
    expect([
      queuePressureColor(0),
      queuePressureColor(0.5),
      queuePressureColor(1),
    ]).toEqual([
      QUEUE_PRESSURE_GREEN,
      QUEUE_PRESSURE_YELLOW,
      QUEUE_PRESSURE_RED,
    ])
  })

  it('applies rendezvous waiter pressure immediately at every waiter age', () => {
    const base = baseTelemetry()
    const deriver = new QueueFlowStateDeriver()
    const idle = deriver.derive(queueInput(base, {
      depth: 0,
      applied: 0,
      preview: 12,
      pending: 12,
      blockedSenders: 0,
      oldestBlockedSenderMs: 0,
    }), 0).queue1

    expect(idle).toMatchObject({
      depthBatches: 0,
      displayedPressure: 0,
      flowState: 'normal',
    })

    const waiting = deriveSequence(deriver, base, [10, 20, 50].map((age) => ({
      atMs: age,
      values: {
        depth: 0,
        applied: 0,
        preview: 12,
        pending: 12,
        blockedSenders: 1,
        oldestBlockedSenderMs: age,
      },
    })))

    expect(waiting.map((queue) => [
      queue.displayedPressure,
      queue.flowState,
      queuePressureColor(queue.displayedPressure),
    ])).toEqual([
      [1, 'backpressure', QUEUE_PRESSURE_RED],
      [1, 'backpressure', QUEUE_PRESSURE_RED],
      [1, 'backpressure', QUEUE_PRESSURE_RED],
    ])
  })

  it('recovers from rendezvous waiter pressure at two units per second', () => {
    const base = baseTelemetry()
    const deriver = new QueueFlowStateDeriver()
    const waiting = queueInput(base, {
      depth: 0,
      applied: 0,
      blockedSenders: 1,
      oldestBlockedSenderMs: 10,
    })

    expect(deriver.derive(waiting, 0).queue1.displayedPressure).toBe(1)
    const recovery = deriveSequence(deriver, waiting, [100, 200, 300, 400, 500]
      .map((atMs) => ({
        atMs,
        values: {
          depth: 0,
          applied: 0,
          blockedSenders: 0,
          oldestBlockedSenderMs: 0,
        },
      })))

    expect(recovery.map((queue) => queue.displayedPressure)).toEqual([
      0.8,
      0.6,
      0.4,
      0.2,
      0,
    ])
    expect(recovery.map((queue) => queue.flowState)).toEqual([
      'near-limit',
      'near-limit',
      'normal',
      'normal',
      'normal',
    ])
  })

  it('paces persistent pressure by elapsed time across rapid revisions', () => {
    const base = baseTelemetry()
    const rapidDeriver = new QueueFlowStateDeriver()
    expect(rapidDeriver.derive(base, 0).queue1).toMatchObject({
      displayedPressure: 0,
      flowState: 'normal',
    })

    const rapid = deriveSequence(
      rapidDeriver,
      base,
      [1, 2, 3, 4, 5].map((atMs) => ({
        atMs,
        values: {
          depth: 160,
          applied: 160,
          preview: 0,
          pending: 0,
        },
      })),
    )
    expect(rapid.map((queue) => queue.displayedPressure)).toEqual([
      0.002,
      0.004,
      0.006,
      0.008,
      0.01,
    ])

    const bottleneckDeriver = new QueueFlowStateDeriver()
    bottleneckDeriver.derive(base, 0)
    const bottleneck = deriveSequence(bottleneckDeriver, base, [
      { atMs: 100, values: { depth: 10, applied: 10 } },
      { atMs: 250, values: { depth: 10, applied: 10 } },
      { atMs: 499, values: { depth: 10, applied: 10 } },
      { atMs: 500, values: { depth: 10, applied: 10 } },
    ])
    expect(bottleneck.map((queue) => queue.displayedPressure)).toEqual([
      0.2,
      0.5,
      0.998,
      1,
    ])
    expect(bottleneck.at(-1)?.flowState).toBe('backpressure')

    const invalidTime = deriveSequence(bottleneckDeriver, base, [
      { atMs: 400, values: { depth: 0, applied: 160 } },
      { atMs: Number.NaN, values: { depth: 0, applied: 160 } },
      { atMs: 600, values: { depth: 0, applied: 160 } },
    ])
    expect(invalidTime.map((queue) => queue.displayedPressure)).toEqual([
      1,
      1,
      1,
    ])
  })

  it('overrides stopped and error immediately without resume catch-up', () => {
    const base = baseTelemetry()
    const deriver = new QueueFlowStateDeriver()
    deriver.derive(base, 0)
    const states = deriveSequence(deriver, base, [
      {
        atMs: 10,
        values: {
          depth: 0,
          applied: 0,
          blockedSenders: 1,
          oldestBlockedSenderMs: 10,
        },
      },
      {
        atMs: 600,
        values: {
          depth: 0,
          applied: 0,
          blockedSenders: 0,
          oldestBlockedSenderMs: 0,
          runState: 'paused',
        },
      },
      {
        atMs: 5_000,
        values: {
          depth: null,
          applied: null,
          connectionState: 'error',
        },
      },
      {
        atMs: 10_000,
        values: {
          depth: 0,
          applied: 0,
          blockedSenders: 0,
          oldestBlockedSenderMs: 0,
          runState: 'running',
          connectionState: 'connected',
        },
      },
      {
        atMs: 10_100,
        values: { depth: 0, applied: 0 },
      },
    ])

    expect(states.map((queue) => [
      queue.displayedPressure,
      queue.flowState,
    ])).toEqual([
      [1, 'backpressure'],
      [1, 'stopped'],
      [1, 'connection-error'],
      [1, 'backpressure'],
      [0.8, 'near-limit'],
    ])
  })

  it('derives q2 pressure only from unsent occupancy during retry saturation', () => {
    const base = baseTelemetry()
    const deriver = new QueueFlowStateDeriver()
    const retryHeavy: LoadgenTelemetrySnapshot = {
      ...base,
      revision: base.revision + 1,
      sender: {
        ...base.sender,
        workerStates: { idle: 0, inFlight: 12, backoff: 20 },
        attemptsStartedTotal: 900,
        retryAttemptsStartedTotal: 600,
        retries: 600,
      },
      queue2: {
        ...base.queue2,
        depthBatches: 100,
        blockedSenders: 1,
        oldestBlockedSenderMs: 500,
        capacity: {
          ...base.queue2.capacity,
          applied: 100,
        },
      },
    }
    deriver.derive(base, 0)
    const saturated = deriver.derive(retryHeavy, 500).queue2
    expect(saturated).toMatchObject({
      displayedPressure: 1,
      flowState: 'backpressure',
      depthBatches: 100,
      dequeuedBatchesTotal: base.queue2.dequeuedBatchesTotal,
    })

    const retryCountersOnly: LoadgenTelemetrySnapshot = {
      ...retryHeavy,
      revision: retryHeavy.revision + 1,
      sender: {
        ...retryHeavy.sender,
        attemptsStartedTotal: 9_000,
        retryAttemptsStartedTotal: 8_000,
        retries: 8_000,
      },
    }
    expect(deriver.derive(retryCountersOnly, 600).queue2.displayedPressure)
      .toBe(1)
  })
})
