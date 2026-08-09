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
      { atMs: 100, values: { depth: 160, applied: 160 } },
      { atMs: 250, values: { depth: 160, applied: 160 } },
      { atMs: 499, values: { depth: 160, applied: 160 } },
      { atMs: 500, values: { depth: 160, applied: 160 } },
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
      { atMs: 500, values: { depth: 160, applied: 160 } },
      {
        atMs: 600,
        values: { depth: 0, applied: 160, runState: 'paused' },
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
          applied: 160,
          runState: 'running',
          connectionState: 'connected',
        },
      },
      {
        atMs: 10_100,
        values: { depth: 0, applied: 160 },
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
})
