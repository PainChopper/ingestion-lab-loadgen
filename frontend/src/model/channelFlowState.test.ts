import { describe, expect, it } from 'vitest'
import { SimulationAdapter } from '../adapters/SimulationAdapter'
import type {
  ConnectionState,
  LoadgenTelemetrySnapshot,
  ChannelSnapshot,
  RunState,
} from './loadgen'
import {
  blockingPressure,
  effectiveChannelPressure,
  occupancyPressure,
  CHANNEL_PRESSURE_GREEN,
  CHANNEL_PRESSURE_RED,
  CHANNEL_PRESSURE_YELLOW,
  channelPressureColor,
  ChannelFlowStateDeriver,
} from './channelFlowState'

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

function channelInput(
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
    readerChannel: {
      ...snapshot.readerChannel,
      depthBatches:
        values.depth === undefined
          ? snapshot.readerChannel.depthBatches
          : values.depth,
      blockedSenders:
        values.blockedSenders ?? snapshot.readerChannel.blockedSenders,
      oldestBlockedSenderMs:
        values.oldestBlockedSenderMs ??
        snapshot.readerChannel.oldestBlockedSenderMs,
      capacity: {
        ...snapshot.readerChannel.capacity,
        applied:
          values.applied === undefined
            ? snapshot.readerChannel.capacity.applied
            : values.applied,
        preview:
          values.preview === undefined
            ? snapshot.readerChannel.capacity.preview
            : values.preview,
        pending:
          values.pending === undefined
            ? snapshot.readerChannel.capacity.pending
            : values.pending,
      },
    },
  }
}

function deriveSequence(
  deriver: ChannelFlowStateDeriver,
  initial: LoadgenTelemetrySnapshot,
  observations: ReadonlyArray<{
    atMs: number
    values: Parameters<typeof channelInput>[1]
  }>,
): ChannelSnapshot[] {
  let current = initial
  return observations.map(({ atMs, values }) => {
    current = channelInput(current, values)
    return deriver.derive(current, atMs).readerChannel
  })
}

describe('channel flow state derivation', () => {
  it('derives pressure only from applied occupancy and current blocking', () => {
    const telemetry = baseTelemetry().readerChannel

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
    expect(effectiveChannelPressure({
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
    expect(effectiveChannelPressure({
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
      channelPressureColor(0),
      channelPressureColor(0.5),
      channelPressureColor(1),
    ]).toEqual([
      CHANNEL_PRESSURE_GREEN,
      CHANNEL_PRESSURE_YELLOW,
      CHANNEL_PRESSURE_RED,
    ])
  })

  it('applies rendezvous waiter pressure immediately at every waiter age', () => {
    const base = baseTelemetry()
    const deriver = new ChannelFlowStateDeriver()
    const idle = deriver.derive(channelInput(base, {
      depth: 0,
      applied: 0,
      preview: 12,
      pending: 12,
      blockedSenders: 0,
      oldestBlockedSenderMs: 0,
    }), 0).readerChannel

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

    expect(waiting.map((channel) => [
      channel.displayedPressure,
      channel.flowState,
      channelPressureColor(channel.displayedPressure),
    ])).toEqual([
      [1, 'backpressure', CHANNEL_PRESSURE_RED],
      [1, 'backpressure', CHANNEL_PRESSURE_RED],
      [1, 'backpressure', CHANNEL_PRESSURE_RED],
    ])
  })

  it('recovers from rendezvous waiter pressure at two units per second', () => {
    const base = baseTelemetry()
    const deriver = new ChannelFlowStateDeriver()
    const waiting = channelInput(base, {
      depth: 0,
      applied: 0,
      blockedSenders: 1,
      oldestBlockedSenderMs: 10,
    })

    expect(deriver.derive(waiting, 0).readerChannel.displayedPressure).toBe(1)
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

    expect(recovery.map((channel) => channel.displayedPressure)).toEqual([
      0.8,
      0.6,
      0.4,
      0.2,
      0,
    ])
    expect(recovery.map((channel) => channel.flowState)).toEqual([
      'near-limit',
      'near-limit',
      'normal',
      'normal',
      'normal',
    ])
  })

  it('paces persistent pressure by elapsed time across rapid revisions', () => {
    const base = baseTelemetry()
    const rapidDeriver = new ChannelFlowStateDeriver()
    expect(rapidDeriver.derive(base, 0).readerChannel).toMatchObject({
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
    expect(rapid.map((channel) => channel.displayedPressure)).toEqual([
      0.002,
      0.004,
      0.006,
      0.008,
      0.01,
    ])

    const bottleneckDeriver = new ChannelFlowStateDeriver()
    bottleneckDeriver.derive(base, 0)
    const bottleneck = deriveSequence(bottleneckDeriver, base, [
      { atMs: 100, values: { depth: 10, applied: 10 } },
      { atMs: 250, values: { depth: 10, applied: 10 } },
      { atMs: 499, values: { depth: 10, applied: 10 } },
      { atMs: 500, values: { depth: 10, applied: 10 } },
    ])
    expect(bottleneck.map((channel) => channel.displayedPressure)).toEqual([
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
    expect(invalidTime.map((channel) => channel.displayedPressure)).toEqual([
      1,
      1,
      1,
    ])
  })

  it('overrides stopped and error immediately without resume catch-up', () => {
    const base = baseTelemetry()
    const deriver = new ChannelFlowStateDeriver()
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

    expect(states.map((channel) => [
      channel.displayedPressure,
      channel.flowState,
    ])).toEqual([
      [1, 'backpressure'],
      [1, 'stopped'],
      [1, 'connection-error'],
      [1, 'backpressure'],
      [0.8, 'near-limit'],
    ])
  })

  it('derives senderChannel pressure only from unsent occupancy during retry saturation', () => {
    const base = baseTelemetry()
    const deriver = new ChannelFlowStateDeriver()
    const retryHeavy: LoadgenTelemetrySnapshot = {
      ...base,
      revision: base.revision + 1,
      sender: {
        ...base.sender,
        attemptsStartedTotal: 900,
        retryAttemptsStartedTotal: 600,
        retries: 600,
      },
      senderChannel: {
        ...base.senderChannel,
        depthBatches: 100,
        blockedSenders: 1,
        oldestBlockedSenderMs: 500,
        capacity: {
          ...base.senderChannel.capacity,
          applied: 100,
        },
      },
    }
    deriver.derive(base, 0)
    const saturated = deriver.derive(retryHeavy, 500).senderChannel
    expect(saturated).toMatchObject({
      displayedPressure: 1,
      flowState: 'backpressure',
      depthBatches: 100,
      receivedBatchesTotal: base.senderChannel.receivedBatchesTotal,
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
    expect(deriver.derive(retryCountersOnly, 600).senderChannel.displayedPressure)
      .toBe(1)
  })
})
